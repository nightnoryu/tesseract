package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	access "github.com/nightnoryu/tesseract/internal/access/app"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	dashboard "github.com/nightnoryu/tesseract/internal/dashboard/app"
	domain "github.com/nightnoryu/tesseract/internal/dashboard/domain"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/domain"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestClientConfigurationAndRedaction(t *testing.T) {
	if c, err := NewClient(Config{}, nil); c != nil || err != nil {
		t.Fatal(c, err)
	}
	for _, c := range []Config{{Endpoint: "https://host", Model: "m"}, {Endpoint: "https://user:secret@host", Model: "m", Token: "t"}, {Endpoint: "https://host?secret=x", Model: "m", Token: "t"}} {
		if _, err := NewClient(c, nil); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
	c, err := NewClient(Config{Endpoint: "https://host/v1/chat/completions", Model: "custom", Token: "secret", InsecureSkipTLSVerify: true}, telemetry.New())
	if err != nil {
		t.Fatal(err)
	}
	if !c.http.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS option ignored")
	}
	if c.http.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect allowed")
	}
	c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authorization")
		}
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("upstream secret"))}, nil
	})
	if _, err = c.Complete(context.Background(), nil, nil); err != ErrUnavailable {
		t.Fatal(err)
	}
	c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"partial"}}]}`))}, nil
	})
	if _, err = c.Complete(context.Background(), nil, nil); err != ErrUnavailable {
		t.Fatal("accepted truncated completion", err)
	}
}

type snapshot struct{ reason string }

func (s *snapshot) Latest(context.Context, string, string) (metadata.Snapshot, error) {
	now := time.Now()
	return metadata.Snapshot{LastScan: now, Tables: []metadata.Table{{Dataset: "leads", Schema: "mart", Name: "MarketingLeads", Present: true, InspectedAt: now, Reason: s.reason}}}, nil
}

type warehouse struct {
	source.Connector
	calls int
}

func (w *warehouse) ExecuteQuery(ctx context.Context, q source.ValidatedQuery) (source.Result, error) {
	w.calls++
	if q.Spec().Limit > 10 {
		return source.Result{}, errors.New("preview limit")
	}
	return source.Result{Rows: [][]any{{"BUSINESS_SECRET"}}}, q.CheckAccess(ctx)
}

type store struct {
	dashboard.Store
	creates     int
	afterCreate func()
}

func (s *store) Create(_ context.Context, user string, spec domain.Spec) (domain.Dashboard, error) {
	s.creates++
	if s.afterCreate != nil {
		s.afterCreate()
	}
	return domain.Dashboard{ID: "00000000-0000-0000-0000-000000000099", Owner: user, Revision: 1, Spec: spec}, nil
}

const user = "00000000-0000-0000-0000-000000000001"

func fixture(t *testing.T) (*dashboard.Service, *snapshot, *store, *warehouse) {
	t.Helper()
	cat, err := catalog.Load(os.DirFS("../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := cat.Hash()
	p, err := access.Load(strings.NewReader(fmt.Sprintf(`{"version":1,"catalog_hash":%q,"grants":[{"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count","channel"],"unrestricted_rows":true}]}`, hash, user)), cat)
	if err != nil {
		t.Fatal(err)
	}
	snap := &snapshot{}
	st := &store{}
	w := &warehouse{}
	return &dashboard.Service{Store: st, Engine: analytics.Engine{Catalog: cat, Store: snap, MaxAge: time.Minute, Connectors: map[string]source.Connector{"crm_dwh": w}}, Policy: p}, snap, st, w
}
func call(name, args string) Call {
	return Call{ID: "c", Type: "function", Function: Function{name, args}}
}

const query = `{"query":{"dataset":"leads","metrics":["lead_count"],"limit":100}}`
const creation = `{"type":"create_dashboard","spec":{"version":1,"title":"Leads","filters":[],"widgets":[{"id":"count","title":"Count","kind":"kpi","layout":{"x":0,"y":0,"width":3,"height":2},"query":{"dataset":"leads","metrics":["lead_count"],"limit":100},"filter_mappings":[]}]}}`

func TestToolsPrivacyValidationAndCreation(t *testing.T) {
	s, snap, st, w := fixture(t)
	ctx := context.Background()
	entries := definitions(ctx, s, user, "", "")
	raw, _ := json.Marshal(entries)
	for _, secret := range []string{"MarketingLeads", "source_column", "SecretEnv", "created_at"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("catalog leaked %s", secret)
		}
	}
	if len(entries) != 1 || len(entries[0].Measures) != 1 || len(entries[0].Dimensions) != 1 {
		t.Fatal(entries)
	}
	if len(definitions(ctx, s, "other", "", "")) != 0 {
		t.Fatal("cross-user catalog disclosure")
	}
	result, _, err := runTool(ctx, s, user, call("preview_query", query))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(result)
	if strings.Contains(string(raw), "BUSINESS_SECRET") || w.calls != 1 {
		t.Fatal(string(raw), w.calls)
	}
	for _, args := range []string{strings.Replace(query, "lead_count", "invented", 1), strings.Replace(query, `"limit":100`, `"limit":100,"sql":"SELECT secret"`, 1)} {
		if _, _, err := runTool(ctx, s, user, call("validate_query", args)); err == nil {
			t.Fatal("unsafe query accepted")
		}
	}
	if _, _, err := runTool(ctx, s, "other", call("create_dashboard", creation)); err == nil || st.creates != 0 {
		t.Fatal("unauthorized creation")
	}
	snap.reason = "incompatible_type"
	if len(definitions(ctx, s, user, "", "")) != 0 {
		t.Fatal("drifted schema exposed")
	}
	if _, _, err := runTool(ctx, s, user, call("create_dashboard", creation)); err == nil || st.creates != 0 {
		t.Fatal("drifted creation")
	}
	snap.reason = ""
	_, d, err := runTool(ctx, s, user, call("create_dashboard", creation))
	if err != nil || d == nil || st.creates != 1 {
		t.Fatal(d, err, st.creates)
	}
}

type completer func(context.Context, []Message, []Tool) (Message, error)

func (f completer) Complete(c context.Context, m []Message, t []Tool) (Message, error) {
	return f(c, m, t)
}
func TestSessionIsolationAndBudgets(t *testing.T) {
	ds, _, st, _ := fixture(t)
	s := &Service{Dashboards: ds, Client: completer(func(context.Context, []Message, []Tool) (Message, error) {
		return Message{Role: "assistant", Content: "Which metric?"}, nil
	})}
	r, err := s.Send(context.Background(), user, "", "Build a dashboard")
	if err != nil || len(r.History) != 2 {
		t.Fatal(r, err)
	}
	if _, err = s.Send(context.Background(), "other", r.SessionID, "continue"); err != ErrInvalid {
		t.Fatal("cross-user history", err)
	}
	s.Client = completer(func(_ context.Context, m []Message, _ []Tool) (Message, error) {
		return Message{Role: "assistant", Calls: []Call{call("create_dashboard", creation), {ID: "other", Type: "function", Function: Function{"search_catalog", `{"search":""}`}}}}, nil
	})
	if _, err = s.Send(context.Background(), user, r.SessionID, "create"); err != ErrInvalid || st.creates != 0 {
		t.Fatal("mixed mutation batch accepted", err)
	}
	turns := 0
	s.Client = completer(func(_ context.Context, m []Message, _ []Tool) (Message, error) {
		turns++
		raw, _ := json.Marshal(m)
		if strings.Contains(string(raw), "BUSINESS_SECRET") {
			t.Fatal("rows reached model")
		}
		return Message{Role: "assistant", Calls: []Call{call("preview_query", query)}}, nil
	})
	if _, err = s.Send(context.Background(), user, r.SessionID, "continue"); err != nil || turns != 6 {
		t.Fatal(err, turns)
	}
	s.Client = completer(func(context.Context, []Message, []Tool) (Message, error) {
		return Message{Role: "assistant", Calls: []Call{call("create_dashboard", creation)}}, nil
	})
	r, err = s.Send(context.Background(), user, r.SessionID, "Create leads count")
	if err != nil || r.Dashboard == nil || st.creates != 1 {
		t.Fatal(r, err)
	}
	if _, err = s.Send(context.Background(), user, r.SessionID, "create again"); err != ErrInvalid || st.creates != 1 {
		t.Fatal("duplicate creation", err)
	}
}

func TestCommittedDraftTerminatesAfterDecorationFailure(t *testing.T) {
	ds, _, st, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st.afterCreate = cancel
	_, d, err := runTool(ctx, ds, user, call("create_dashboard", creation))
	if err != nil || d == nil || d.ID == "" || st.creates != 1 || len(d.WidgetStates) != 1 || d.WidgetStates[0].Status != "unavailable" {
		t.Fatalf("committed draft lost: %+v %v", d, err)
	}
}

func TestToolSchemasHaveValidRequiredArrays(t *testing.T) {
	raw, err := json.Marshal(toolDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"required":null`) {
		t.Fatal("invalid JSON schema required:null")
	}
	var decoded []map[string]any
	if json.Unmarshal(raw, &decoded) != nil || len(decoded) != 6 {
		t.Fatal("invalid tools")
	}
	if !strings.Contains(string(raw), `"date_range"`) || !strings.Contains(string(raw), `"filter_mappings"`) {
		t.Fatal("typed query/dashboard schema missing")
	}
}
