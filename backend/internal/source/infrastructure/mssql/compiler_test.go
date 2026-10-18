package mssql

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	"github.com/nightnoryu/tesseract/internal/source/domain"
)

type testAccess struct{ err error }

func (a *testAccess) CheckQueryAccess(context.Context, semantic.Dataset, string, []string) error {
	return a.err
}
func testCatalog(t *testing.T) semantic.Catalog {
	t.Helper()
	c, err := catalog.Load(os.DirFS("../../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestCompileParameterizedQuery(t *testing.T) {
	cat := testCatalog(t)
	hash, _ := cat.Hash()
	c := &Connector{hash: hash, cfg: DefaultConfig(), datasets: map[string]semantic.Dataset{"leads": cat.Datasets[0]}}
	attack := "x'); DROP TABLE mart.MarketingLeads;--"
	spec := domain.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Dimensions: []domain.QueryDimension{{ID: "created_at", Grain: "month"}, {ID: "channel"}}, Filters: []domain.Filter{{Dimension: "channel", Operator: "eq", Values: []domain.Value{{Type: "string", Value: attack}}}}, DateRange: &domain.DateRange{Dimension: "created_at", Start: domain.Value{Type: "datetime", Value: "2026-01-01T00:00:00Z"}, End: domain.Value{Type: "datetime", Value: "2026-03-01T00:00:00Z"}}, Order: []domain.Order{{Field: "lead_count", Direction: "desc"}}, Limit: 10}
	access := &testAccess{}
	q, err := domain.ValidateQuery(context.Background(), cat, spec, access)
	if err != nil {
		t.Fatal(err)
	}
	// Caller mutation cannot alter the opaque validated plan.
	spec.Metrics[0] = "evil"
	spec.Filters[0].Values[0].Value = "changed"
	copied := q.Spec()
	copied.Metrics[0] = "evil"
	statement, args, names, err := c.compile(q)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"COUNT_BIG(DISTINCT [LeadId]) AS [lead_count]", "[ChannelName] = @p1", "DATEFROMPARTS(YEAR([CreatedAt]), MONTH([CreatedAt]), 1)", "[CreatedAt] >= CAST(@p2 AS datetime2)", "ORDER BY [lead_count] DESC, [created_at] ASC, [channel] ASC"} {
		if !strings.Contains(statement, want) {
			t.Fatalf("missing %q in %s", want, statement)
		}
	}
	if strings.Contains(statement, attack) || len(args) != 4 || args[1].(sql.NamedArg).Value != attack || strings.Join(names, ",") != "created_at,channel,lead_count" {
		t.Fatalf("unsafe compilation: %s %#v", statement, args)
	}
	access.err = domain.ErrAccessDenied
	if !errors.Is(q.CheckAccess(context.Background()), domain.ErrAccessDenied) {
		t.Fatal("reused plan ignored revoked access")
	}
	c.hash = "changed"
	if _, _, _, err := c.compile(q); !errors.Is(err, domain.ErrInvalidQuery) {
		t.Fatal("old catalog accepted")
	}
}

func TestRejectUnapprovedQuery(t *testing.T) {
	cat := testCatalog(t)
	cases := []struct {
		name string
		edit func(*domain.QuerySpec)
	}{
		{"dataset injection", func(q *domain.QuerySpec) { q.Dataset = "leads; DROP TABLE x" }},
		{"unknown metric", func(q *domain.QuerySpec) { q.Metrics = []string{"revenue"} }},
		{"physical column", func(q *domain.QuerySpec) { q.Dimensions = []domain.QueryDimension{{ID: "ChannelName"}} }},
		{"hidden column", func(q *domain.QuerySpec) { q.Dimensions = []domain.QueryDimension{{ID: "lead_id"}} }},
		{"unknown grain", func(q *domain.QuerySpec) { q.Dimensions = []domain.QueryDimension{{ID: "created_at", Grain: "hour"}} }},
		{"missing grain", func(q *domain.QuerySpec) { q.Dimensions = []domain.QueryDimension{{ID: "created_at"}} }},
		{"operator", func(q *domain.QuerySpec) {
			q.Filters = []domain.Filter{{Dimension: "channel", Operator: "like", Values: []domain.Value{{Type: "string", Value: "%"}}}}
		}},
		{"typed filter", func(q *domain.QuerySpec) {
			q.Filters = []domain.Filter{{Dimension: "channel", Operator: "eq", Values: []domain.Value{{Type: "integer", Value: "1"}}}}
		}},
		{"null comparison", func(q *domain.QuerySpec) { q.Filters = []domain.Filter{{Dimension: "channel", Operator: "eq"}} }},
		{"empty in", func(q *domain.QuerySpec) { q.Filters = []domain.Filter{{Dimension: "channel", Operator: "in"}} }},
		{"order injection", func(q *domain.QuerySpec) { q.Order = []domain.Order{{Field: "lead_count", Direction: "desc;--"}} }},
		{"unselected order", func(q *domain.QuerySpec) { q.Order = []domain.Order{{Field: "channel", Direction: "asc"}} }},
		{"limit", func(q *domain.QuerySpec) { q.Limit = 1001 }},
		{"duplicate", func(q *domain.QuerySpec) { q.Metrics = []string{"lead_count", "lead_count"} }},
		{"reversed dates", func(q *domain.QuerySpec) {
			q.DateRange = &domain.DateRange{Dimension: "created_at", Start: domain.Value{Type: "datetime", Value: "2026-03-01T00:00:00Z"}, End: domain.Value{Type: "datetime", Value: "2026-01-01T00:00:00Z"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := domain.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 10}
			tc.edit(&spec)
			if _, err := domain.ValidateQuery(context.Background(), cat, spec, &testAccess{}); !errors.Is(err, domain.ErrInvalidQuery) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}
