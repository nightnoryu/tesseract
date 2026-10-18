package domain

import (
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"strings"
	"time"
)

type Column struct {
	ID           string
	Name         string
	ExpectedType string
	Present      bool
	Actual       source.Column
	Reason       string
}
type Table struct {
	Dataset     string
	Schema      string
	Name        string
	Present     bool
	Reason      string
	InspectedAt time.Time
	Columns     []Column
}

func Compatible(expected, actual string) bool {
	switch expected {
	case "string":
		return contains(actual, "char", "varchar", "nchar", "nvarchar", "uniqueidentifier")
	case "integer":
		return contains(actual, "tinyint", "smallint", "int", "bigint")
	case "decimal":
		return contains(actual, "decimal", "numeric", "money", "smallmoney")
	case "boolean":
		return actual == "bit"
	case "date":
		return actual == "date"
	case "datetime":
		return contains(actual, "datetime", "datetime2", "smalldatetime")
	}
	return false
}
func contains(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

// Reconcile projects onto approved columns even if a connector returns extras.
func Reconcile(d semantic.Dataset, actual source.Table, failure string) Table {
	t := Table{Dataset: d.ID, Schema: d.Schema, Name: d.Table, InspectedAt: time.Now().UTC(), Reason: failure}
	if failure == "" {
		if actual.Dataset != d.ID || actual.Schema != d.Schema || actual.Name != d.Table {
			t.Reason = "scan_failed"
		} else {
			t.Present = actual.Present
			if !t.Present {
				t.Reason = "missing_table"
			}
		}
	}
	for _, c := range d.Columns {
		col := Column{ID: c.ID, Name: c.SourceColumn, ExpectedType: c.Type, Reason: t.Reason}
		if t.Present {
			col.Reason = "missing_column"
			for _, a := range actual.Columns {
				if strings.EqualFold(a.Name, c.SourceColumn) {
					col.Present = true
					col.Actual = a
					col.Reason = ""
					if !Compatible(c.Type, a.Type) {
						col.Reason = "incompatible_type"
					}
					break
				}
			}
			if t.Reason == "" && col.Reason != "" {
				t.Reason = col.Reason
			}
		}
		t.Columns = append(t.Columns, col)
	}
	return t
}
