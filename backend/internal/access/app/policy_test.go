package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/domain"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

const owner = "00000000-0000-0000-0000-000000000001"
const viewer = "00000000-0000-0000-0000-000000000002"

type snapshots struct{}

func (snapshots) Latest(context.Context, string, string) (metadata.Snapshot, error) {
	now := time.Now()
	return metadata.Snapshot{LastScan: now, Tables: []metadata.Table{{Dataset: "leads", Schema: "mart", Name: "MarketingLeads", Present: true, InspectedAt: now}}}, nil
}

type connector struct{ specs []source.QuerySpec }

func (*connector) InspectCatalogTables(context.Context, []string) ([]source.Table, error) {
	panic("unexpected")
}
func (*connector) Close() error { return nil }
func (c *connector) ExecuteQuery(ctx context.Context, q source.ValidatedQuery) (source.Result, error) {
	if err := q.CheckAccess(ctx); err != nil {
		return source.Result{}, err
	}
	c.specs = append(c.specs, q.Spec())
	return source.Result{}, nil
}
func TestViewerScopesAndDenials(t *testing.T) {
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := cat.Hash()
	doc := document{Version: 1, CatalogHash: hash, Grants: []rule{
		{UserID: owner, Source: "crm_dwh", Dataset: "leads", Fields: []string{"lead_count", "channel"}, UnrestrictedRows: true},
		{UserID: viewer, Source: "crm_dwh", Dataset: "leads", Fields: []string{"lead_count"}, RowFilters: []source.Filter{{Dimension: "channel", Operator: "eq", Values: []source.Value{{Type: "string", Value: "paid"}}}}},
	}}
	raw, _ := json.Marshal(doc)
	p, err := Load(strings.NewReader(string(raw)), cat)
	if err != nil {
		t.Fatal(err)
	}
	c := &connector{}
	engine := analytics.Engine{Catalog: cat, Store: snapshots{}, MaxAge: time.Minute, Connectors: map[string]source.Connector{"crm_dwh": c}}
	spec := source.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 10}
	for _, user := range []string{owner, viewer} {
		if _, err := p.Execute(context.Background(), engine, user, spec); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.specs[0].Filters) != 0 || len(c.specs[1].Filters) != 1 || c.specs[1].Filters[0].Values[0].Value != "paid" {
		t.Fatal("viewer inherited owner scope")
	}
	// Policy-only scope fields cannot be selected or used for caller filtering.
	spec.Filters = []source.Filter{{Dimension: "channel", Operator: "ne", Values: []source.Value{{Type: "string", Value: "paid"}}}}
	if _, err := p.Execute(context.Background(), engine, viewer, spec); !errors.Is(err, source.ErrAccessDenied) {
		t.Fatal(err)
	}
	spec.Filters = nil
	for _, user := range []string{"", "00000000-0000-0000-0000-000000000003"} {
		if _, err := p.Execute(context.Background(), engine, user, spec); !errors.Is(err, source.ErrAccessDenied) {
			t.Fatal(err)
		}
	}
	g, _ := p.Resolve(viewer, "leads", hash)
	g.RowFilters[0].Values[0].Value = "other"
	again, _ := p.Resolve(viewer, "leads", hash)
	if again.RowFilters[0].Values[0].Value != "paid" {
		t.Fatal("policy mutation")
	}
	if len(c.specs) != 2 {
		t.Fatal("denied query executed")
	}
	for _, edit := range []func(*document){
		func(d *document) { d.CatalogHash = "old" },
		func(d *document) { d.Grants[1].RowFilters = nil },
		func(d *document) { d.Grants[1].UnrestrictedRows = true },
		func(d *document) { d.Grants[1].RowFilters[0].Dimension = "invented" },
		func(d *document) { d.Grants[1].RowFilters[0].Values[0].Type = "integer" },
		func(d *document) { d.Grants[1].Fields = []string{"lead_id"} },
		func(d *document) { d.Grants = append(d.Grants, d.Grants[0]) },
	} {
		var changed document
		_ = json.Unmarshal(raw, &changed)
		edit(&changed)
		b, _ := json.Marshal(changed)
		if _, err := Load(strings.NewReader(string(b)), cat); !errors.Is(err, ErrPolicy) {
			t.Fatalf("accepted invalid policy: %s", b)
		}
	}
}
