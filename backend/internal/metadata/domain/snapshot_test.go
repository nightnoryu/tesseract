package domain

import (
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"testing"
)

func TestReconcile(t *testing.T) {
	d := semantic.Dataset{ID: "leads", Schema: "mart", Table: "Leads", Columns: []semantic.Column{{ID: "id", SourceColumn: "ID", Type: "integer"}, {ID: "name", SourceColumn: "Name", Type: "string"}}}
	for _, tc := range []struct {
		name          string
		actual        source.Table
		failure, want string
	}{
		{"compatible", source.Table{Dataset: "leads", Schema: "mart", Name: "Leads", Present: true, Columns: []source.Column{{Name: "ID", Type: "bigint"}, {Name: "Name", Type: "nvarchar"}, {Name: "Secret", Type: "varchar"}}}, "", ""},
		{"missing table", source.Table{Dataset: "leads", Schema: "mart", Name: "Leads"}, "", "missing_table"},
		{"missing column", source.Table{Dataset: "leads", Schema: "mart", Name: "Leads", Present: true}, "", "missing_column"},
		{"drift", source.Table{Dataset: "leads", Schema: "mart", Name: "Leads", Present: true, Columns: []source.Column{{Name: "ID", Type: "varchar"}, {Name: "Name", Type: "nvarchar"}}}, "", "incompatible_type"},
		{"wrong identity", source.Table{Dataset: "other", Present: true}, "", "scan_failed"},
		{"outage", source.Table{}, "scan_failed", "scan_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Reconcile(d, tc.actual, tc.failure)
			if got.Reason != tc.want || len(got.Columns) != 2 {
				t.Fatalf("%+v", got)
			}
			if tc.name == "drift" && got.Columns[1].Reason != "" {
				t.Fatalf("healthy column: %+v", got.Columns[1])
			}
		})
	}
}
func TestCompatibilityFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		expected, actual string
		want             bool
	}{
		{"integer", "bigint", true}, {"decimal", "numeric", true}, {"decimal", "float", false}, {"datetime", "datetime2", true}, {"datetime", "datetimeoffset", false}, {"boolean", "int", false}, {"string", "xml", false}, {"date", "datetime", false}, {"integer", "unknown", false},
	} {
		if got := Compatible(tc.expected, tc.actual); got != tc.want {
			t.Errorf("%s/%s: %v", tc.expected, tc.actual, got)
		}
	}
}
