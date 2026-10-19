//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nightnoryu/go-kita/jsonlog"
	access "github.com/nightnoryu/tesseract/internal/access/app"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	oidc "github.com/nightnoryu/tesseract/internal/auth/infrastructure/oidc"
	"github.com/nightnoryu/tesseract/internal/auth/infrastructure/oidctest"
	authstore "github.com/nightnoryu/tesseract/internal/auth/infrastructure/postgres"
	"github.com/nightnoryu/tesseract/internal/chat"
	dashboard "github.com/nightnoryu/tesseract/internal/dashboard/app"
	domain "github.com/nightnoryu/tesseract/internal/dashboard/domain"
	dashboardstore "github.com/nightnoryu/tesseract/internal/dashboard/infrastructure/postgres"
	"github.com/nightnoryu/tesseract/internal/generated/api"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
	metastore "github.com/nightnoryu/tesseract/internal/metadata/infrastructure/postgres"
	platform "github.com/nightnoryu/tesseract/internal/platform/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestRelease1Flow crosses the HTTP, authentication, metadata, chat, dashboard,
// ACL and real DWH boundaries. Only the external identity and LLM providers are fake.
func TestRelease1Flow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{Image: "postgres:18.3-alpine", Env: map[string]string{"POSTGRES_USER": "test", "POSTGRES_PASSWORD": "test", "POSTGRES_DB": "test"}, ExposedPorts: []string{"5432/tcp"}, WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2)}, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	defer container.Terminate(context.Background())
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSSLMODE", "disable")
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "dashboard-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Sync()
	db, err := platform.Open(ctx, fmt.Sprintf("postgres://test:test@%s:%s/test", host, port.Port()), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	password := "Release-one-test-password!"
	passwordHash, err := auth.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range users {
		if _, err := db.TransactionalClient().ExecContext(ctx, `INSERT INTO tesseract_app.users(id,email) VALUES ($1,$2)`, id, id+"@example.com"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.TransactionalClient().ExecContext(ctx, `INSERT INTO tesseract_app.local_credentials VALUES ($1,$2)`, id, passwordHash); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := cat.Hash()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.Load(strings.NewReader(fmt.Sprintf(`{"version":1,"catalog_hash":%q,"grants":[{"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count","channel"],"unrestricted_rows":true},{"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count"],"row_filters":[{"dimension":"channel","operator":"eq","values":[{"type":"string","value":"paid"}]}]}]}`, hash, users[0], users[1])), cat)
	if err != nil {
		t.Fatal(err)
	}
	warehouse := newFlowWarehouse(t, ctx, cat)
	store := metastore.Store{DB: db.TransactionalClient()}
	worker := metadata.Worker{Catalog: cat, Store: store, Open: warehouse.open, Timeout: 5 * time.Second}
	scan := func() {
		t.Helper()
		if err := worker.Scan(ctx, "crm_dwh"); err != nil {
			t.Fatal(err)
		}
	}
	scan()
	snapshot, err := store.Latest(ctx, "crm_dwh", hash)
	if err != nil || len(snapshot.Tables) != 1 {
		t.Fatalf("metadata: %+v %v", snapshot, err)
	}
	var physicalColumns int
	if err := store.DB.GetContext(ctx, &physicalColumns, `SELECT count(*) FROM tesseract_app.schema_columns c JOIN tesseract_app.schema_current s ON s.scan_id=c.scan_id WHERE c.present AND c.actual_type IN ('nvarchar','datetime2') AND c.reason=''`); err != nil || physicalColumns != 3 {
		t.Fatalf("stored physical types: %d %v", physicalColumns, err)
	}
	// Inspect every application table, not just tables named after the warehouse.
	// There must be no copied business values in generic JSON/text storage either.
	assertNoBusinessRows := func() {
		t.Helper()
		var tables []string
		if err := db.TransactionalClient().SelectContext(ctx, &tables, `SELECT tablename FROM pg_tables WHERE schemaname='tesseract_app'`); err != nil {
			t.Fatal(err)
		}
		for _, table := range tables {
			var rows []string
			quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
			if err := db.TransactionalClient().SelectContext(ctx, &rows, `SELECT row_to_json(r)::text FROM tesseract_app.`+quoted+` r`); err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if strings.Contains(row, "BUSINESS-SECRET-MUST-NOT-BE-COPIED") || strings.Contains(row, "BUSINESS-LEAD-MUST-NOT-BE-COPIED") || strings.Contains(row, "SecretColumn") || strings.Contains(row, "Unlisted") {
					t.Fatalf("business data or unlisted metadata copied into %s", table)
				}
			}
		}
	}
	assertNoBusinessRows()
	engine := analytics.Engine{Catalog: cat, Store: store, MaxAge: time.Minute, Connectors: map[string]source.Connector{"crm_dwh": warehouse.connector}}
	service := &dashboard.Service{Store: dashboardstore.Store{DB: db.TransactionalClient()}, Engine: engine, Policy: policy}
	spec := domain.Spec{Version: 1, Title: "Leads", Filters: []domain.Filter{}, Widgets: []domain.Widget{{ID: "count", Title: "Count", Kind: "kpi", Layout: domain.Layout{Width: 3, Height: 2}, Query: source.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 1}, FilterMappings: []domain.Mapping{}}}}
	call := func(name string, args any) chat.Message {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		return chat.Message{Role: "assistant", Calls: []chat.Call{{ID: name, Type: "function", Function: chat.Function{Name: name, Arguments: string(raw)}}}}
	}
	llmCalls := 0
	assistant := &chat.Service{Dashboards: service, Client: dashboardCompleter(func(_ context.Context, messages []chat.Message, _ []chat.Tool) (chat.Message, error) {
		llmCalls++
		switch llmCalls {
		case 1:
			return call("get_catalog_availability", map[string]any{}), nil
		case 2:
			last := messages[len(messages)-1]
			if last.Role != "tool" || !strings.Contains(last.Content, "lead_count") || strings.Contains(last.Content, "Unlisted") || strings.Contains(last.Content, "SecretColumn") {
				t.Fatalf("unsafe or empty LLM catalog: %+v", last)
			}
			return call("get_dataset_definition", map[string]any{"dataset": "Unlisted"}), nil
		case 3:
			if messages[len(messages)-1].Content != "[]" {
				t.Fatal("unlisted dataset exposed to LLM")
			}
			return call("create_dashboard", map[string]any{"type": "create_dashboard", "spec": spec}), nil
		case 4:
			return call("edit_dashboard", map[string]any{"operations": []domain.Operation{{Type: "rename_dashboard", Title: "Refined leads"}}}), nil
		default:
			t.Fatal("unexpected LLM retry")
			return chat.Message{}, nil
		}
	})}
	authDB := authstore.Store{DB: db.TransactionalClient()}
	fake := oidctest.New(t)
	provider, err := oidc.New(ctx, oidc.Config{Enabled: true, Issuer: fake.Issuer, ClientID: "client", ClientSecret: "secret", RedirectURI: "http://localhost/api/v1/auth/oidc/callback", InsecureDevelopment: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := authDB.LinkOIDC(ctx, users[1]+"@example.com", fake.Issuer, fake.Subject); err != nil {
		t.Fatal(err)
	}
	config := CatalogConfig{Auth: &AuthConfig{Service: auth.New(authDB), Origin: "https://app.example", OIDC: provider, OIDCStore: authDB}, Dashboards: service, Chat: assistant, Engine: engine, Policy: policy, Availability: metadata.Availability{Catalog: cat, Store: store, MaxAge: time.Minute}}
	server := New(db.Ping, &capture{}, telemetry.New(), config)
	request := func(method, path string, body any, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw := ""
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(b)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(raw)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.example")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: want %d got %d %s", method, path, want, w.Code, w.Body.String())
		}
		return w
	}
	cookie := func(w *httptest.ResponseRecorder, name string) *http.Cookie {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("missing %s cookie", name)
		return nil
	}
	login := func(id string) *http.Cookie {
		t.Helper()
		return cookie(request("POST", "/api/v1/auth/login", map[string]string{"email": id + "@example.com", "password": password}, nil, 200), "tesseract_session")
	}
	request("POST", "/api/v1/auth/login", map[string]string{"email": users[0] + "@example.com", "password": "wrong"}, nil, 401)
	owner := login(users[0])
	outsider := login(users[2])
	readAvailability := func(reason string) {
		t.Helper()
		w := request("GET", "/api/v1/catalog", nil, owner, 200)
		var got api.CatalogAvailability
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Sources) != 1 || got.Sources[0].Configured != 1 || len(got.Sources[0].Tables) != 1 {
			t.Fatal(got)
		}
		s := got.Sources[0]
		if string(s.Tables[0].Reason.Value) != reason || (s.Available == 1) != (reason == "") || s.Available+s.Unavailable != 1 || strings.Contains(w.Body.String(), "Unlisted") {
			t.Fatal(got)
		}
	}
	readAvailability("")
	decodeChat := func(w *httptest.ResponseRecorder) domain.Dashboard {
		t.Helper()
		var got chat.Result
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Dashboard == nil {
			t.Fatalf("chat: %s %v", w.Body.String(), err)
		}
		return *got.Dashboard
	}
	d := decodeChat(request("POST", "/api/v1/chat", map[string]string{"message": "Build a leads count dashboard"}, owner, 200))
	path := "/api/v1/dashboards/" + d.ID
	edit := map[string]any{"dashboard_id": d.ID, "expected_revision": d.Revision, "message": "Rename to Refined leads"}
	d = decodeChat(request("POST", "/api/v1/chat", edit, owner, 200))
	if d.Revision != 2 || d.Spec.Title != "Refined leads" {
		t.Fatal(d)
	}
	request("POST", "/api/v1/chat", edit, owner, 409)
	if llmCalls != 4 {
		t.Fatal("conflict reached LLM")
	}
	request("POST", path+"/publish", map[string]int64{"expected_revision": d.Revision}, owner, 200)
	request("POST", path+"/shares", map[string]string{"email": users[1] + "@example.com"}, owner, 204)
	request("GET", path, nil, nil, 401)
	request("GET", path, nil, outsider, 403)
	// The recipient signs in through the real code/PKCE/token callback and PostgreSQL identity mapping.
	start := request("POST", "/api/v1/auth/oidc/start", nil, nil, 200)
	var redirect struct {
		URL string `json:"authorization_url"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &redirect); err != nil {
		t.Fatal(err)
	}
	callback := fake.Authorize(t, redirect.URL)
	browser := cookie(start, "tesseract_oidc")
	viewer := cookie(request("GET", callback.RequestURI(), nil, browser, 302), "tesseract_session")
	var session api.Session
	if err := json.Unmarshal(request("GET", "/api/v1/auth/session", nil, viewer, 200).Body.Bytes(), &session); err != nil || session.UserID.String() != users[1] {
		t.Fatal("OIDC lost stable user ID", session, err)
	}
	// Recreate the services to prove the saved URL survives in-memory state loss.
	restarted := &dashboard.Service{Store: dashboardstore.Store{DB: db.TransactionalClient()}, Engine: engine, Policy: policy}
	config.Dashboards = restarted
	config.Chat = &chat.Service{Dashboards: restarted, Client: assistant.Client}
	server = New(db.Ping, &capture{}, telemetry.New(), config)
	var shared domain.Dashboard
	if err := json.Unmarshal(request("GET", path, nil, viewer, 200).Body.Bytes(), &shared); err != nil || shared.Role != "viewer" || shared.Spec.Title != "Refined leads" || shared.Revision != 3 {
		t.Fatal(shared, err)
	}
	queryPath := path + "/widgets/count/query?expectedRevision=3"
	for _, tc := range []struct {
		cookie *http.Cookie
		count  string
	}{{owner, "3"}, {viewer, "2"}, {owner, "3"}, {viewer, "2"}} {
		var result source.Result
		if err := json.Unmarshal(request("POST", queryPath, []domain.Filter{}, tc.cookie, 200).Body.Bytes(), &result); err != nil || len(result.Rows) != 1 || result.Rows[0][0] != tc.count {
			t.Fatal(result, err)
		}
	}
	request("POST", queryPath, []domain.Filter{}, outsider, 403)
	request("POST", path+"/publish", map[string]int64{"expected_revision": 3}, viewer, 404)
	request("POST", "/api/v1/queries", source.QuerySpec{Dataset: "unlisted", Metrics: []string{"lead_count"}, Limit: 1}, owner, 403)
	if _, err := db.TransactionalClient().ExecContext(ctx, `UPDATE tesseract_app.schema_tables SET inspected_at=now()-interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	readAvailability("stale")
	request("POST", queryPath, []domain.Filter{}, viewer, 503)
	scan()
	readAvailability("")
	assertNoBusinessRows()
	warehouse.stop()
	request("POST", queryPath, []domain.Filter{}, viewer, 503)
	scan()
	readAvailability("scan_failed")
	if _, err := db.TransactionalClient().ExecContext(ctx, `UPDATE tesseract_app.sessions SET expires_at=now()-interval '1 second' WHERE user_id=$1`, users[1]); err != nil {
		t.Fatal(err)
	}
	request("GET", path, nil, viewer, 401)
	request("POST", "/api/v1/auth/logout", nil, owner, 204)
	request("GET", path, nil, owner, 401)
}
