package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	access "github.com/nightnoryu/tesseract/internal/access/app"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	authdomain "github.com/nightnoryu/tesseract/internal/auth/domain"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
	metadomain "github.com/nightnoryu/tesseract/internal/metadata/domain"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

type querySnapshot struct{}

func (querySnapshot) Latest(context.Context, string, string) (metadomain.Snapshot, error) {
	now := time.Now()
	return metadomain.Snapshot{LastScan: now, Tables: []metadomain.Table{{Dataset: "leads", Schema: "mart", Name: "MarketingLeads", Present: true, InspectedAt: now}}}, nil
}

type queryConnector struct {
	calls int
	query source.QuerySpec
}

func (*queryConnector) InspectCatalogTables(context.Context, []string) ([]source.Table, error) {
	panic("unexpected")
}
func (*queryConnector) Close() error { return nil }
func (c *queryConnector) ExecuteQuery(ctx context.Context, q source.ValidatedQuery) (source.Result, error) {
	if err := q.CheckAccess(ctx); err != nil {
		return source.Result{}, err
	}
	c.calls++
	c.query = q.Spec()
	return source.Result{Columns: []string{"lead_count"}, Rows: [][]any{{"3"}}}, nil
}
func TestQueryHTTPAuthorization(t *testing.T) {
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := cat.Hash()
	user := "00000000-0000-0000-0000-000000000001"
	p, err := access.Load(strings.NewReader(fmt.Sprintf(`{"version":1,"catalog_hash":%q,"grants":[{"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count"],"row_filters":[{"dimension":"channel","operator":"eq","values":[{"type":"string","value":"paid"}]}]}]}`, hash, user)), cat)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 52)
	store := &authStore{account: authdomain.Account{ID: user}, tokens: map[string]time.Time{auth.Digest(token): time.Now().Add(time.Hour)}}
	conn := &queryConnector{}
	engine := analytics.Engine{Catalog: cat, Store: querySnapshot{}, MaxAge: time.Minute, Connectors: map[string]source.Connector{"crm_dwh": conn}}
	server := New(func(context.Context) error { return nil }, &capture{}, telemetry.New(), CatalogConfig{Auth: &AuthConfig{Service: auth.New(store), Origin: "https://app.example"}, Policy: p, Engine: engine, Availability: metadata.Availability{Catalog: cat, Store: querySnapshot{}, MaxAge: time.Minute}})
	for _, tc := range []struct {
		name, method, path, body, user, token string
		status                                int
	}{
		{"allowed", "POST", "/api/v1/queries", `{"dataset":"leads","metrics":["lead_count"],"limit":10}`, user, token, 200},
		{"field denied", "POST", "/api/v1/queries", `{"dataset":"leads","metrics":["lead_count"],"dimensions":[{"id":"channel"}],"limit":10}`, user, token, 403},
		{"dataset denied", "POST", "/api/v1/queries", `{"dataset":"sales","metrics":["lead_count"],"limit":10}`, user, token, 403},
		{"other viewer", "POST", "/api/v1/queries", `{"dataset":"leads","metrics":["lead_count"],"limit":10}`, "00000000-0000-0000-0000-000000000002", token, 403},
		{"anonymous", "POST", "/api/v1/queries", `{"dataset":"leads","metrics":["lead_count"],"limit":10}`, user, "", 401},
		{"forged grants", "POST", "/api/v1/queries", `{"dataset":"leads","metrics":["lead_count"],"limit":10,"unrestricted_rows":true}`, user, token, 400},
		{"catalog denied", "GET", "/api/v1/catalog", "", "00000000-0000-0000-0000-000000000002", token, 200},
		{"catalog filtered", "GET", "/api/v1/catalog", "", user, token, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.account.ID = tc.user
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://app.example")
			if tc.token != "" {
				r.AddCookie(&http.Cookie{Name: "tesseract_session", Value: tc.token})
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cacheable response")
			}
			if tc.name == "catalog denied" && strings.Contains(w.Body.String(), "MarketingLeads") {
				t.Fatal("catalog leak")
			}
			if tc.name == "catalog filtered" && (!strings.Contains(w.Body.String(), `"configured":1`) || strings.Contains(w.Body.String(), "orders")) {
				t.Fatal(w.Body.String())
			}
		})
	}
	if conn.calls != 1 || len(conn.query.Filters) != 1 || conn.query.Filters[0].Values[0].Value != "paid" {
		t.Fatal("scope missing or denied request executed")
	}
}
