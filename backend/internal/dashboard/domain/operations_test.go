package domain

import (
	"encoding/json"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"testing"
)

func TestOperationBatches(t *testing.T) {
	spec := Spec{Version: 1, Title: "Original", Filters: []Filter{}, Widgets: []Widget{{ID: "count", Title: "Count", Kind: "kpi", Layout: Layout{Width: 3, Height: 2}, Query: source.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 1}, FilterMappings: []Mapping{}}}}
	original, _ := json.Marshal(spec)
	widget := spec.Widgets[0]
	widget.ID = "second"
	widget.Layout.X = 3
	mapped := spec.Widgets[0]
	mapped.FilterMappings = []Mapping{{Filter: "channel", Dimension: "channel"}}
	filter := Filter{ID: "channel", Operator: "eq", Values: []source.Value{{Type: "string", Value: "paid"}}}
	tests := []struct {
		name  string
		ops   []Operation
		valid bool
	}{
		{"rename", []Operation{{Type: "rename_dashboard", Title: "Renamed"}}, true},
		{"add and move", []Operation{{Type: "add_widget", ID: "second", Widget: &widget}, {Type: "move_widget", ID: "second", Layout: &Layout{X: 6, Width: 3, Height: 2}}}, true},
		{"mapped filter", []Operation{{Type: "add_filter", ID: "channel", Filter: &filter}, {Type: "update_widget", ID: "count", Widget: &mapped}}, true},
		{"remove widget", []Operation{{Type: "remove_widget", ID: "count"}}, true},
		{"partial failure", []Operation{{Type: "rename_dashboard", Title: "Lost"}, {Type: "remove_widget", ID: "missing"}}, false},
		{"duplicate", []Operation{{Type: "add_widget", ID: "count", Widget: &spec.Widgets[0]}}, false},
		{"wrong payload", []Operation{{Type: "remove_widget", ID: "count", Title: "ignored?"}}, false},
		{"overlap", []Operation{{Type: "add_widget", ID: "second", Widget: &widget}, {Type: "move_widget", ID: "second", Layout: &Layout{Width: 3, Height: 2}}}, false},
		{"missing payload", []Operation{{Type: "update_widget", ID: "count"}}, false},
		{"unmapped filter", []Operation{{Type: "add_filter", ID: "channel", Filter: &filter}}, false},
		{"unknown", []Operation{{Type: "replace_dashboard"}}, false},
		{"empty", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			next, err := Apply(spec, tc.ops)
			if (err == nil) != tc.valid {
				t.Fatalf("%+v %v", next, err)
			}
			after, _ := json.Marshal(spec)
			if string(after) != string(original) {
				t.Fatal("input mutated")
			}
		})
	}
	next, err := Apply(spec, []Operation{{Type: "add_filter", ID: "channel", Filter: &filter}, {Type: "update_widget", ID: "count", Widget: &mapped}})
	if err != nil {
		t.Fatal(err)
	}
	filter.Values[0].Value = "organic"
	next, err = Apply(next, []Operation{{Type: "update_filter", ID: "channel", Filter: &filter}})
	if err != nil || next.Filters[0].Values[0].Value != "organic" {
		t.Fatal(next, err)
	}
	next, err = Apply(next, []Operation{{Type: "remove_filter", ID: "channel"}})
	if err != nil || len(next.Filters) != 0 || len(next.Widgets[0].FilterMappings) != 0 {
		t.Fatal(next, err)
	}
}
