//go:build integration

package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/nightnoryu/go-kita/jsonlog"
	access "github.com/nightnoryu/tesseract/internal/access/app"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	authstore "github.com/nightnoryu/tesseract/internal/auth/infrastructure/postgres"
	dashboard "github.com/nightnoryu/tesseract/internal/dashboard/app"
	domain "github.com/nightnoryu/tesseract/internal/dashboard/domain"
	dashboardstore "github.com/nightnoryu/tesseract/internal/dashboard/infrastructure/postgres"
	platform "github.com/nightnoryu/tesseract/internal/platform/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"github.com/nightnoryu/tesseract/internal/source/infrastructure/mssql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDashboardSharing(t *testing.T) {
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
	user, other, third := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{user, other, third} {
		if _, err := db.TransactionalClient().ExecContext(ctx, `INSERT INTO tesseract_app.users(id,email) VALUES ($1,$2)`, id, id+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	token, otherToken := strings.Repeat("a", 52), strings.Repeat("b", 52)
	for id, tok := range map[string]string{user: token, other: otherToken, third: strings.Repeat("c", 52)} {
		if _, err := db.TransactionalClient().ExecContext(ctx, `INSERT INTO tesseract_app.sessions(token_hash,user_id,expires_at) VALUES ($1,$2,$3)`, auth.Digest(tok), id, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := cat.Hash()
	policy, err := access.Load(strings.NewReader(fmt.Sprintf(`{"version":1,"catalog_hash":%q,"grants":[{"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count","channel"],"unrestricted_rows":true},{"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count"],"row_filters":[{"dimension":"channel","operator":"eq","values":[{"type":"string","value":"paid"}]}]}]}`, hash, user, other)), cat)
	if err != nil {
		t.Fatal(err)
	}
	snap := &dashboardSnapshot{}
	conn := sharingWarehouse(t, ctx, cat)
	engine := analytics.Engine{Catalog: cat, Store: snap, MaxAge: time.Minute, Connectors: map[string]source.Connector{"crm_dwh": conn}}
	service := &dashboard.Service{Store: dashboardstore.Store{DB: db.TransactionalClient()}, Engine: engine, Policy: policy}

	server := New(db.Ping, &capture{}, telemetry.New(), CatalogConfig{Auth: &AuthConfig{Service: auth.New(authstore.Store{DB: db.TransactionalClient()}), Origin: "https://app.example"}, Dashboards: service})
	request := func(method, path string, body any, tok string, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw := ""
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(b)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.example")
		if tok != "" {
			r.AddCookie(&http.Cookie{Name: "tesseract_session", Value: tok})
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: want %d got %d %s", method, path, want, w.Code, w.Body.String())
		}
		return w
	}
	spec := domain.Spec{Version: 1, Title: "Leads", Filters: []domain.Filter{}, Widgets: []domain.Widget{{ID: "count", Title: "Count", Kind: "kpi", Layout: domain.Layout{Width: 3, Height: 2}, Query: source.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 1}, FilterMappings: []domain.Mapping{}}}}
	read := func(w *httptest.ResponseRecorder) domain.Dashboard {
		t.Helper()
		var d domain.Dashboard
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := read(request("POST", "/api/v1/dashboards", spec, token, 200))
	path := "/api/v1/dashboards/" + d.ID
	request("GET", path, nil, "", 401)
	request("GET", path, nil, otherToken, 403)
	request("POST", path+"/shares", map[string]string{"email": other + "@example.com"}, otherToken, 404)
	request("POST", path+"/shares", map[string]string{"email": "missing@example.com"}, token, 400)
	request("POST", path+"/shares", map[string]string{"email": other + "@example.com"}, token, 204)
	request("GET", path, nil, otherToken, 403) // A grant does not expose an unpublished draft.
	d = read(request("POST", path+"/publish", map[string]int64{"expected_revision": d.Revision}, token, 200))
	spec.Title = "Private draft"
	read(request("PUT", path+"/draft", map[string]any{"expected_revision": d.Revision, "spec": spec}, token, 200))
	viewer := read(request("GET", path, nil, otherToken, 200))
	if viewer.Role != "viewer" || viewer.Spec.Title != "Leads" || viewer.Revision != 2 {
		t.Fatal(viewer)
	}
	request("GET", path, nil, strings.Repeat("c", 52), 403)
	request("GET", path+"/shares", nil, otherToken, 404)
	request("GET", path+"/chat", nil, otherToken, 404)
	request("GET", path+"/revisions", nil, otherToken, 404)
	request("POST", path+"/publish", map[string]int64{"expected_revision": 2}, otherToken, 404)
	request("POST", path+"/discard", map[string]int64{"expected_revision": 2}, otherToken, 404)
	request("POST", "/api/v1/chat", map[string]any{"dashboard_id": d.ID, "expected_revision": 2, "message": "rename it"}, otherToken, 503)
	var shares []domain.Share
	if err := json.Unmarshal(request("GET", path+"/shares", nil, token, 200).Body.Bytes(), &shares); err != nil || len(shares) != 1 || shares[0].UserID != other {
		t.Fatal(shares, err)
	}
	var listed []domain.Dashboard
	if err := json.Unmarshal(request("GET", "/api/v1/dashboards", nil, otherToken, 200).Body.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].Spec.Title != "Leads" {
		t.Fatal(listed, err)
	}
	queryPath := path + "/widgets/count/query?expectedRevision=2"
	request("POST", path+"/widgets/count/query?expectedRevision=1", []domain.Filter{}, otherToken, 409)
	for _, tc := range []struct{ tok, want string }{{token, "3"}, {otherToken, "2"}, {token, "3"}, {otherToken, "2"}} {
		var result source.Result
		if err := json.Unmarshal(request("POST", path+"/widgets/count/query?expectedRevision="+map[string]string{token: "3", otherToken: "2"}[tc.tok], []domain.Filter{}, tc.tok, 200).Body.Bytes(), &result); err != nil || len(result.Rows) != 1 || result.Rows[0][0] != tc.want {
			t.Fatal(result, err)
		}
	}
	request("POST", queryPath, []domain.Filter{}, strings.Repeat("c", 52), 403)
	request("POST", queryPath, []domain.Filter{{ID: "invented", Operator: "eq"}}, otherToken, 400)
	// Sharing with an account lacking data entitlements produces a denied widget.
	request("POST", path+"/shares", map[string]string{"email": third + "@example.com"}, token, 204)
	denied := read(request("GET", path, nil, strings.Repeat("c", 52), 200))
	if denied.WidgetStates[0].Status != "denied" {
		t.Fatal(denied)
	}
	request("POST", queryPath, []domain.Filter{}, strings.Repeat("c", 52), 403)
	snap.drift = true
	unavailable := read(request("GET", path, nil, otherToken, 200))
	if unavailable.WidgetStates[0].Status != "unavailable" {
		t.Fatal(unavailable)
	}
	request("POST", queryPath, []domain.Filter{}, otherToken, 503)
	snap.drift = false
	request("DELETE", path+"/shares/"+other, nil, otherToken, 404)
	request("DELETE", path+"/shares/"+other, nil, token, 204)
	request("GET", path, nil, otherToken, 403)
	request("POST", queryPath, []domain.Filter{}, otherToken, 403)
}

func sharingWarehouse(t *testing.T, ctx context.Context, cat semantic.Catalog) source.Connector {
	t.Helper()
	const password = "Test-Only-Mssql-2026!"
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image: "mcr.microsoft.com/mssql/server:2022-CU23-ubuntu-22.04", Env: map[string]string{"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": password}, ExposedPorts: []string{"1433/tcp"}, WaitingFor: wait.ForAll(wait.ForLog("SQL Server is now ready for client connections"), wait.ForExec([]string{"/bin/sh", "-c", `SQLCMDPASSWORD="$MSSQL_SA_PASSWORD" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -Q 'SELECT 1' -b -o /dev/null`})).WithDeadline(3 * time.Minute),
	}, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dsn := func(user, db string) string {
		u := url.URL{Scheme: "sqlserver", Host: host + ":" + port.Port(), User: url.UserPassword(user, password)}
		q := u.Query()
		q.Set("database", db)
		q.Set("encrypt", "disable")
		u.RawQuery = q.Encode()
		return u.String()
	}
	admin, err := sql.Open("sqlserver", dsn("sa", "master"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE warehouse"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlserver", dsn("sa", "warehouse"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE SCHEMA mart",
		"CREATE TABLE mart.MarketingLeads (LeadId nvarchar(64), CreatedAt datetime2, ChannelName nvarchar(100), SecretColumn int)",
		"INSERT INTO mart.MarketingLeads(LeadId,ChannelName) VALUES ('a','paid'),('b','paid'),('c','organic')",
		"CREATE LOGIN probe WITH PASSWORD='" + password + "'",
		"CREATE USER probe FOR LOGIN probe",
		"ALTER ROLE db_denydatawriter ADD MEMBER probe",
		"GRANT SELECT ON OBJECT::mart.MarketingLeads TO probe",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := mssql.Open(ctx, cat, "crm_dwh", dsn("probe", "warehouse"), mssql.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}
