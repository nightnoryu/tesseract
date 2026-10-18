//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	access "github.com/nightnoryu/tesseract/internal/access/app"
	"strings"
	"testing"
	"time"

	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

// Uses the real PostgreSQL ingestion store and restricted SQL Server principal
// created by TestAvailabilitySync. No analytical result enters the app store.
func testAnalyticalQueries(t *testing.T, ctx context.Context, db *sql.DB, cat semantic.Catalog, store Store, worker metadata.Worker) {
	t.Helper()
	exec := func(statement string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	exec("ALTER TABLE mart.MarketingLeads ADD Amount decimal(18,2), Quantity bigint")
	exec("INSERT INTO mart.MarketingLeads (LeadId,CreatedAt,ChannelName,Amount,Quantity) VALUES ('a','2026-01-01','paid',10.25,1), ('b','2026-01-02','paid',20.50,2), ('b','2026-01-02','paid',NULL,NULL), ('c','2026-02-01','organic',0,0), (NULL,'2026-02-02',NULL,NULL,NULL)")
	// Extend only this test's reviewed catalog, then ingest its exact hash.
	cat.Datasets = append([]semantic.Dataset(nil), cat.Datasets...)
	d := cat.Datasets[0]
	exposed := true
	d.Columns = append(append([]semantic.Column(nil), d.Columns...), semantic.Column{ID: "amount", SourceColumn: "Amount", Type: "decimal", ExposeToAnalytics: &exposed}, semantic.Column{ID: "quantity", SourceColumn: "Quantity", Type: "integer", ExposeToAnalytics: &exposed})
	d.Measures = append(append([]semantic.Measure(nil), d.Measures...), semantic.Measure{ID: "total", Column: "amount", Operation: "sum"}, semantic.Measure{ID: "average", Column: "quantity", Operation: "avg"}, semantic.Measure{ID: "minimum", Column: "amount", Operation: "min"}, semantic.Measure{ID: "maximum", Column: "amount", Operation: "max"}, semantic.Measure{ID: "records", Column: "lead_id", Operation: "count"}, semantic.Measure{ID: "largest", Column: "quantity", Operation: "max"})
	d.Dimensions = append([]semantic.Dimension(nil), d.Dimensions...)
	for i := range d.Dimensions {
		if d.Dimensions[i].ID == "created_at" {
			d.Dimensions[i].AllowedGranularities = []string{"day", "week", "month", "quarter", "year"}
		}
	}
	cat.Datasets[0] = d
	worker.Catalog = cat
	if err := worker.Scan(ctx, "crm_dwh"); err != nil {
		t.Fatal(err)
	}
	conn, err := worker.Open(ctx, cat, "crm_dwh")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	engine := analytics.Engine{Catalog: cat, Store: store, MaxAge: time.Minute, Connectors: map[string]source.Connector{"crm_dwh": conn}}
	grants := analytics.Grants{Principal: "test-operator", Source: "crm_dwh", Dataset: "leads", Fields: []string{"lead_count", "channel", "created_at", "total", "average", "minimum", "maximum", "records", "largest"}, UnrestrictedRows: true}
	query := func(spec source.QuerySpec) source.Result {
		t.Helper()
		r, err := engine.Execute(ctx, spec, grants)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	base := source.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 10}
	// Execute an identical saved query as two viewers on the same pool.
	hash, _ := cat.Hash()
	owner := "00000000-0000-0000-0000-000000000001"
	viewer := "00000000-0000-0000-0000-000000000002"
	policy, err := access.Load(strings.NewReader(fmt.Sprintf(`{"version":1,"catalog_hash":%q,"grants":[
 {"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count","channel"],"unrestricted_rows":true},
 {"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count","channel"],"row_filters":[{"dimension":"channel","operator":"eq","values":[{"type":"string","value":"paid"}]}]}
 ]}`, hash, owner, viewer)), cat)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ user, want string }{{owner, "3"}, {viewer, "2"}, {owner, "3"}, {viewer, "2"}} {
		result, err := policy.Execute(ctx, engine, tc.user, base)
		if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != tc.want {
			t.Fatalf("viewer scope: %#v %v", result, err)
		}
	}
	conflicting := base
	conflicting.Filters = []source.Filter{{Dimension: "channel", Operator: "eq", Values: []source.Value{{Type: "string", Value: "organic"}}}}
	restricted, err := policy.Execute(ctx, engine, viewer, conflicting)
	if err != nil || restricted.Rows[0][0] != "0" {
		t.Fatalf("caller replaced scope: %#v %v", restricted, err)
	}
	if _, err := policy.Execute(ctx, engine, "00000000-0000-0000-0000-000000000003", base); !errors.Is(err, source.ErrAccessDenied) {
		t.Fatal("unentitled viewer executed query", err)
	}
	grouped := base
	grouped.Dimensions = []source.QueryDimension{{ID: "channel"}}
	grouped.Order = []source.Order{{Field: "lead_count", Direction: "desc"}}
	r := query(grouped)
	if len(r.Rows) != 3 || r.Rows[0][0] != "paid" || r.Rows[0][1] != "2" || r.Rows[2][0] != nil || r.Rows[2][1] != "0" {
		t.Fatalf("aggregates: %#v", r)
	}
	grouped.Limit = 1
	if r := query(grouped); len(r.Rows) != 1 || r.Rows[0][0] != "paid" {
		t.Fatalf("limit: %+v", r)
	}
	for _, grain := range []string{"day", "week", "month", "quarter", "year"} {
		temporal := base
		temporal.Dimensions = []source.QueryDimension{{ID: "created_at", Grain: grain}}
		r := query(temporal)
		if len(r.Rows) == 0 {
			t.Fatal("missing time groups")
		}
		if grain == "month" && (len(r.Rows) != 2 || r.Rows[0][1] != "2" || r.Rows[1][1] != "1") {
			t.Fatalf("monthly: %#v", r)
		}
		if grain == "week" && r.Rows[0][0].(time.Time).Weekday() != time.Monday {
			t.Fatal("week does not start Monday")
		}
	}
	filtered := base
	filtered.Filters = []source.Filter{{Dimension: "channel", Operator: "in", Values: []source.Value{{Type: "string", Value: "paid"}, {Type: "string", Value: "x'); DROP TABLE mart.MarketingLeads;--"}}}}
	if r := query(filtered); r.Rows[0][0] != "2" {
		t.Fatalf("parameter binding: %+v", r)
	}
	filtered.Filters[0].Operator = "eq"
	filtered.Filters[0].Values = filtered.Filters[0].Values[1:]
	if r := query(filtered); r.Rows[0][0] != "0" {
		t.Fatal("injection changed predicate")
	}
	filtered.Filters = []source.Filter{{Dimension: "channel", Operator: "is_null"}}
	if r := query(filtered); r.Rows[0][0] != "0" {
		t.Fatal("null count")
	}
	filtered.Filters = nil
	filtered.DateRange = &source.DateRange{Dimension: "created_at", Start: source.Value{Type: "datetime", Value: "2026-01-01T00:00:00Z"}, End: source.Value{Type: "datetime", Value: "2026-02-01T00:00:00Z"}}
	filtered.Metrics = []string{"lead_count", "total", "average", "minimum", "maximum", "records"}
	r = query(filtered)
	if fmt.Sprint(r.Rows[0]) != "[2 30.75 1.500000 10.25 20.50 3]" {
		t.Fatalf("numeric aggregates: %#v", r.Rows)
	}
	filtered.DateRange.Start.Value = "2027-01-01T00:00:00Z"
	filtered.DateRange.End.Value = "2027-02-01T00:00:00Z"
	r = query(filtered)
	if r.Rows[0][0] != "0" || r.Rows[0][1] != nil || r.Rows[0][2] != nil || r.Rows[0][5] != "0" {
		t.Fatalf("empty aggregate NULL handling: %#v", r)
	}

	exec("UPDATE mart.MarketingLeads SET Quantity=9007199254740993 WHERE LeadId='a'")
	exact := base
	exact.Metrics = []string{"average", "largest"}
	// AVG over one bigint must preserve values above JavaScript's safe range.
	exact.Filters = []source.Filter{{Dimension: "created_at", Operator: "lt", Values: []source.Value{{Type: "datetime", Value: "2026-01-02T00:00:00Z"}}}}
	if r := query(exact); r.Rows[0][0] != "9007199254740993.000000" || r.Rows[0][1] != "9007199254740993" {
		t.Fatalf("big integer precision lost: %#v", r)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := engine.Execute(canceled, base, grants); err == nil {
		t.Fatal("canceled query executed")
	}

	// A warehouse lock forces real database work to wait until its deadline.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT COUNT_BIG(*) FROM mart.MarketingLeads WITH (TABLOCKX, HOLDLOCK)"); err != nil {
		t.Fatal(err)
	}
	blocked, stopBlocked := context.WithTimeout(ctx, 300*time.Millisecond)
	_, err = engine.Execute(blocked, base, grants)
	stopBlocked()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked query ignored deadline: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if r := query(base); r.Rows[0][0] != "3" {
		t.Fatal("connection unusable after cancellation")
	}
	// Catch a compatible SQL conversion before the next scheduled ingestion.
	exec("ALTER TABLE mart.MarketingLeads ALTER COLUMN ChannelName varchar(100)")
	exec("UPDATE mart.MarketingLeads SET ChannelName=NULL")
	exec("ALTER TABLE mart.MarketingLeads ALTER COLUMN ChannelName int")
	if _, err := engine.Execute(ctx, base, grants); !errors.Is(err, source.ErrSchemaUnavailable) {
		t.Fatalf("live drift accepted: %v", err)
	}
	exec("ALTER TABLE mart.MarketingLeads ALTER COLUMN ChannelName nvarchar(100)")
	exec("DELETE FROM mart.MarketingLeads")
}
