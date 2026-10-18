package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	metadata "github.com/nightnoryu/tesseract/internal/metadata/domain"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

type snapshots struct {
	hash     string
	snapshot metadata.Snapshot
	err      error
}

func (s *snapshots) Latest(_ context.Context, _ string, hash string) (metadata.Snapshot, error) {
	if s.hash != hash {
		return metadata.Snapshot{}, nil
	}
	return s.snapshot, s.err
}

type connector struct{ calls int }

func (c *connector) InspectCatalogTables(context.Context, []string) ([]source.Table, error) {
	panic("unexpected inspection")
}
func (c *connector) ExecuteQuery(ctx context.Context, q source.ValidatedQuery) (source.Result, error) {
	if err := q.CheckAccess(ctx); err != nil {
		return source.Result{}, err
	}
	c.calls++
	return source.Result{}, nil
}
func (c *connector) Close() error { return nil }
func TestQueryGates(t *testing.T) {
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := cat.Hash()
	cases := []struct {
		name string
		edit func(*Engine, *Grants, *snapshots, *source.QuerySpec)
		want error
	}{
		{"allowed", func(*Engine, *Grants, *snapshots, *source.QuerySpec) {}, nil},
		{"anonymous", func(_ *Engine, g *Grants, _ *snapshots, _ *source.QuerySpec) { g.Principal = "" }, source.ErrAccessDenied},
		{"wrong source", func(_ *Engine, g *Grants, _ *snapshots, _ *source.QuerySpec) { g.Source = "other" }, source.ErrAccessDenied},
		{"wrong dataset", func(_ *Engine, g *Grants, _ *snapshots, _ *source.QuerySpec) { g.Dataset = "other" }, source.ErrAccessDenied},
		{"row scope required", func(_ *Engine, g *Grants, _ *snapshots, _ *source.QuerySpec) { g.UnrestrictedRows = false }, source.ErrAccessDenied},
		{"filter field denied", func(_ *Engine, _ *Grants, _ *snapshots, q *source.QuerySpec) {
			q.Filters = []source.Filter{{Dimension: "channel", Operator: "is_null"}}
		}, source.ErrAccessDenied},
		{"metric denied", func(_ *Engine, g *Grants, _ *snapshots, _ *source.QuerySpec) { g.Fields = nil }, source.ErrAccessDenied},
		{"unverified", func(_ *Engine, _ *Grants, s *snapshots, _ *source.QuerySpec) { s.snapshot.Tables = nil }, source.ErrSchemaUnavailable},
		{"drift", func(_ *Engine, _ *Grants, s *snapshots, _ *source.QuerySpec) {
			s.snapshot.Tables[0].Reason = "incompatible_type"
		}, source.ErrSchemaUnavailable},
		{"stale", func(_ *Engine, _ *Grants, s *snapshots, _ *source.QuerySpec) {
			s.snapshot.Tables[0].InspectedAt = time.Now().Add(-time.Hour)
		}, source.ErrSchemaUnavailable},
		{"future", func(_ *Engine, _ *Grants, s *snapshots, _ *source.QuerySpec) {
			s.snapshot.LastScan = time.Now().Add(time.Hour)
		}, source.ErrSchemaUnavailable},
		{"new catalog hash", func(_ *Engine, _ *Grants, s *snapshots, _ *source.QuerySpec) { s.hash = "old" }, source.ErrSchemaUnavailable},
		{"store failure", func(_ *Engine, _ *Grants, s *snapshots, _ *source.QuerySpec) { s.err = errors.New("offline") }, source.ErrSchemaUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			store := &snapshots{hash: hash, snapshot: metadata.Snapshot{LastScan: now, Tables: []metadata.Table{{Dataset: "leads", Schema: "mart", Name: "MarketingLeads", Present: true, InspectedAt: now}}}}
			conn := &connector{}
			engine := Engine{Catalog: cat, Store: store, MaxAge: time.Minute, Connectors: map[string]source.Connector{"crm_dwh": conn}}
			grants := Grants{Principal: "operator", Source: "crm_dwh", Dataset: "leads", Fields: []string{"lead_count"}, UnrestrictedRows: true}
			spec := source.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 10}
			tc.edit(&engine, &grants, store, &spec)
			_, err := engine.Execute(context.Background(), spec, grants)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v got %v", tc.want, err)
			}
			if (conn.calls > 0) != (tc.want == nil) {
				t.Fatal("gate did not protect execution")
			}
		})
	}
}
