//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nightnoryu/go-kita/jsonlog"
	access "github.com/nightnoryu/tesseract/internal/access/app"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	authstore "github.com/nightnoryu/tesseract/internal/auth/infrastructure/postgres"
	"github.com/nightnoryu/tesseract/internal/chat"
	dashboard "github.com/nightnoryu/tesseract/internal/dashboard/app"
	domain "github.com/nightnoryu/tesseract/internal/dashboard/domain"
	dashboardstore "github.com/nightnoryu/tesseract/internal/dashboard/infrastructure/postgres"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/domain"
	platform "github.com/nightnoryu/tesseract/internal/platform/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type dashboardSnapshot struct{ drift bool }

func (s *dashboardSnapshot) Latest(ctx context.Context, src, hash string) (metadata.Snapshot, error) {
	snap, err := (querySnapshot{}).Latest(ctx, src, hash)
	if s.drift {
		snap.Tables[0].Reason = "missing_column"
	}
	return snap, err
}
func TestDashboardLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	user, other := uuid.NewString(), uuid.NewString()
	for _, id := range []string{user, other} {
		if _, err := db.TransactionalClient().ExecContext(ctx, `INSERT INTO tesseract_app.users(id,email) VALUES ($1,$2)`, id, id+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	token, otherToken := strings.Repeat("a", 52), strings.Repeat("b", 52)
	for id, tok := range map[string]string{user: token, other: otherToken} {
		if _, err := db.TransactionalClient().ExecContext(ctx, `INSERT INTO tesseract_app.sessions(token_hash,user_id,expires_at) VALUES ($1,$2,$3)`, auth.Digest(tok), id, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := cat.Hash()
	policy, err := access.Load(strings.NewReader(fmt.Sprintf(`{"version":1,"catalog_hash":%q,"grants":[{"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count","channel"],"unrestricted_rows":true}]}`, hash, user)), cat)
	if err != nil {
		t.Fatal(err)
	}
	snap := &dashboardSnapshot{}
	conn := &queryConnector{}
	engine := analytics.Engine{Catalog: cat, Store: snap, MaxAge: time.Minute, Connectors: map[string]source.Connector{"crm_dwh": conn}}
	service := &dashboard.Service{Store: dashboardstore.Store{DB: db.TransactionalClient()}, Engine: engine, Policy: policy}
	llmCalls := 0
	llmReply := chat.Message{}
	assistant := &chat.Service{Dashboards: service, Client: dashboardCompleter(func(_ context.Context, _ []chat.Message, _ []chat.Tool) (chat.Message, error) {
		llmCalls++
		return llmReply, nil
	})}
	server := New(db.Ping, &capture{}, telemetry.New(), CatalogConfig{Auth: &AuthConfig{Service: auth.New(authstore.Store{DB: db.TransactionalClient()}), Origin: "https://app.example"}, Dashboards: service, Chat: assistant})
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
	request("POST", "/api/v1/dashboards", spec, "", 401)
	request("POST", "/api/v1/dashboards", spec, otherToken, 403)
	d := read(request("POST", "/api/v1/dashboards", spec, token, 200))
	path := "/api/v1/dashboards/" + d.ID
	if d.Revision != 1 || d.Owner != user || d.PublishedRevision != 0 || d.WidgetStates[0].Status != "ready" {
		t.Fatal(d)
	}
	request("GET", path, nil, otherToken, 404)
	if w := request("GET", "/api/v1/dashboards", nil, otherToken, 200); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	request("GET", path+"/revisions", nil, otherToken, 404)
	request("PUT", path+"/draft", map[string]any{"expected_revision": 1, "spec": spec}, otherToken, 404)
	request("POST", path+"/publish", map[string]int{"expected_revision": 1}, otherToken, 404)
	request("DELETE", path+"?expected_revision=1", nil, otherToken, 404)
	var injected map[string]any
	b, _ := json.Marshal(spec)
	_ = json.Unmarshal(b, &injected)
	injected["sql"] = "SELECT secret"
	request("POST", "/api/v1/dashboards", injected, token, 400)
	injected["widgets"].([]any)[0].(map[string]any)["query"].(map[string]any)["sql"] = "SELECT secret"
	delete(injected, "sql")
	request("POST", "/api/v1/dashboards", injected, token, 400)
	d = read(request("POST", path+"/publish", map[string]int{"expected_revision": 1}, token, 200))
	if d.Revision != 2 || d.PublishedRevision != 2 {
		t.Fatal(d)
	}
	spec.Title = "Edited"
	d = read(request("PUT", path+"/draft", map[string]any{"expected_revision": 2, "spec": spec}, token, 200))
	if d.Revision != 3 || d.PublishedRevision != 2 {
		t.Fatal(d)
	}
	request("POST", path+"/publish", map[string]int{"expected_revision": 2}, token, 409)
	request("PUT", path+"/draft", map[string]any{"expected_revision": 2, "spec": spec}, token, 409)
	d = read(request("GET", path, nil, token, 200))
	if d.Spec.Title != "Edited" {
		t.Fatal(d)
	}
	var history []domain.Revision
	w := request("GET", path+"/revisions", nil, token, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || !history[1].Published || history[1].Spec.Title != "Leads" {
		t.Fatal(history)
	}
	// Restore a historical spec by saving it as a fresh revision.
	d = read(request("PUT", path+"/draft", map[string]any{"expected_revision": 3, "spec": history[2].Spec}, token, 200))
	if d.Revision != 4 || d.Spec.Title != "Leads" {
		t.Fatal(d)
	}
	snap.drift = true
	d = read(request("GET", path, nil, token, 200))
	if d.WidgetStates[0].Status != "unavailable" {
		t.Fatal(d)
	}
	request("POST", path+"/publish", map[string]int{"expected_revision": 4}, token, 503)
	request("PUT", path+"/draft", map[string]any{"expected_revision": 4, "spec": spec}, token, 503)
	snap.drift = false
	// Concurrent writers using one revision must have exactly one winner.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := service.Save(ctx, user, d.ID, 4, spec, false); results <- err })
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch err {
		case nil:
			wins++
		case domain.ErrConflict:
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	if conn.calls != 0 {
		t.Fatal("dashboard persistence executed analytical queries")
	}
	var count int
	if err := db.TransactionalClient().GetContext(ctx, &count, `SELECT count(*) FROM tesseract_app.dashboard_revisions WHERE dashboard_id=$1`, d.ID); err != nil || count != 5 {
		t.Fatal(count, err)
	}
	request("DELETE", path+"?expected_revision=4", nil, token, 409)
	request("DELETE", path+"?expected_revision=5", nil, token, 204)
	request("GET", path, nil, token, 404)
	if err := db.TransactionalClient().GetContext(ctx, &count, `SELECT count(*) FROM tesseract_app.dashboard_revisions WHERE dashboard_id=$1`, d.ID); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	// Chat creation and iterative editing share the same durable revision store.
	mutation := func(name string, args any) {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		llmReply = chat.Message{Role: "assistant", Calls: []chat.Call{{ID: "mutation", Type: "function", Function: chat.Function{Name: name, Arguments: string(raw)}}}}
	}
	spec.Title = "Chat draft"
	mutation("create_dashboard", map[string]any{"type": "create_dashboard", "spec": spec})
	var chatResult chat.Result
	w = request("POST", "/api/v1/chat", map[string]any{"message": "Create leads"}, token, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &chatResult); err != nil || chatResult.Dashboard == nil {
		t.Fatal(w.Body.String(), err)
	}
	d = *chatResult.Dashboard
	path = "/api/v1/dashboards/" + d.ID
	historyTurns := func() []domain.Turn {
		t.Helper()
		var turns []domain.Turn
		w := request("GET", path+"/chat", nil, token, 200)
		if err := json.Unmarshal(w.Body.Bytes(), &turns); err != nil {
			t.Fatal(err)
		}
		return turns
	}
	if turns := historyTurns(); len(turns) != 1 || turns[0].Kind != "create" || turns[0].Revision != 1 || len(turns[0].Entries) != 2 {
		t.Fatal(turns)
	}
	request("GET", path+"/chat", nil, otherToken, 404)
	request("GET", path+"/chat", nil, "", 401)
	editBody := map[string]any{"dashboard_id": d.ID, "expected_revision": 1, "message": "Rename"}
	mutation("edit_dashboard", map[string]any{"operations": []domain.Operation{{Type: "rename_dashboard", Title: "Chat edited"}}})
	w = request("POST", "/api/v1/chat", editBody, token, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &chatResult); err != nil || chatResult.Dashboard == nil || chatResult.Dashboard.Revision != 2 {
		t.Fatal(w.Body.String(), err)
	}
	if turns := historyTurns(); len(turns) != 2 || turns[0].Operations[0].Type != "rename_dashboard" || turns[0].Revision != 2 {
		t.Fatal(turns)
	}
	beforeCalls := llmCalls
	request("POST", "/api/v1/chat", editBody, token, 409)
	request("POST", "/api/v1/chat", editBody, otherToken, 404)
	if llmCalls != beforeCalls {
		t.Fatal("stale or unauthorized edit reached model")
	}
	editBody["expected_revision"] = 2
	mutation("edit_dashboard", map[string]any{"operations": []domain.Operation{{Type: "rename_dashboard", Title: "Partial"}, {Type: "remove_widget", ID: "absent"}}})
	request("POST", "/api/v1/chat", editBody, token, 400)
	d = read(request("GET", path, nil, token, 200))
	if d.Revision != 2 || d.Spec.Title != "Chat edited" || len(historyTurns()) != 2 {
		t.Fatal("partial edit was persisted", d)
	}
	badWidget := spec.Widgets[0]
	badWidget.Query.Metrics = []string{"invented"}
	mutation("edit_dashboard", map[string]any{"operations": []domain.Operation{{Type: "update_widget", ID: badWidget.ID, Widget: &badWidget}}})
	request("POST", "/api/v1/chat", editBody, token, 400)
	mutation("edit_dashboard", map[string]any{"operations": []domain.Operation{{Type: "rename_dashboard", Title: "Drifted"}}})
	snap.drift = true
	request("POST", "/api/v1/chat", editBody, token, 503)
	snap.drift = false
	// Force history insertion to fail after the revision INSERT. Both must roll back.
	_, err = service.Store.SaveTurn(ctx, user, d.ID, 2, spec, domain.Turn{Kind: "invalid"})
	if err == nil {
		t.Fatal("invalid history accepted")
	}
	d = read(request("GET", path, nil, token, 200))
	if d.Revision != 2 || d.Spec.Title != "Chat edited" || len(historyTurns()) != 2 {
		t.Fatal("transaction did not roll back")
	}
	llmReply = chat.Message{Role: "assistant", Content: "Which chart should change?"}
	// A fresh chat service has no in-memory session, but resumes this draft.
	restarted := &chat.Service{Dashboards: service, Client: assistant.Client}
	if _, err := restarted.SendEdit(ctx, user, d.ID, 2, "Change a chart"); err != nil {
		t.Fatal(err)
	}
	if len(historyTurns()) != 3 {
		t.Fatal("clarification not durable")
	}
	d = read(request("POST", path+"/publish", map[string]int{"expected_revision": 2}, token, 200))
	if d.PublishedRevision != 3 {
		t.Fatal(d)
	}
	editBody["expected_revision"] = 3
	mutation("edit_dashboard", map[string]any{"operations": []domain.Operation{{Type: "remove_widget", ID: "count"}}})
	request("POST", "/api/v1/chat", editBody, token, 200)
	request("POST", path+"/discard", map[string]int{"expected_revision": 3}, token, 409)
	request("POST", path+"/discard", map[string]int{"expected_revision": 4}, otherToken, 404)
	d = read(request("POST", path+"/discard", map[string]int{"expected_revision": 4}, token, 200))
	if d.Revision != 5 || d.PublishedRevision != 3 || len(d.Spec.Widgets) != 1 || d.Spec.Title != "Chat edited" {
		t.Fatal(d)
	}
	if turns := historyTurns(); len(turns) != 5 || turns[0].Kind != "discard" {
		t.Fatal(turns)
	}
	// Two concurrent edit batches still have exactly one winner and one history event.
	results = make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := service.Edit(ctx, user, d.ID, 5, []domain.Operation{{Type: "rename_dashboard", Title: "Concurrent"}}, nil)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins, conflicts = 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 || len(historyTurns()) != 6 {
		t.Fatal(wins, conflicts)
	}
	// Unpublished discard restores revision 1 and keeps its edit audit trail.
	fresh, err := service.Create(ctx, user, spec)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err = service.Edit(ctx, user, fresh.ID, 1, []domain.Operation{{Type: "rename_dashboard", Title: "Temporary"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err = service.Discard(ctx, user, fresh.ID, 2)
	if err != nil || fresh.Spec.Title != spec.Title || fresh.Revision != 3 {
		t.Fatal(fresh, err)
	}
	request("DELETE", path+"?expected_revision=6", nil, token, 204)
	if err := db.TransactionalClient().GetContext(ctx, &count, `SELECT count(*) FROM tesseract_app.dashboard_chat WHERE dashboard_id=$1`, d.ID); err != nil || count != 0 {
		t.Fatal(count, err)
	}

}

type dashboardCompleter func(context.Context, []chat.Message, []chat.Tool) (chat.Message, error)

func (f dashboardCompleter) Complete(ctx context.Context, messages []chat.Message, tools []chat.Tool) (chat.Message, error) {
	return f(ctx, messages, tools)
}
