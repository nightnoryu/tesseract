package domain

import (
	"encoding/json"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"testing"
)

func TestSpecBoundaries(t *testing.T) {
	valid := Spec{Version: 1, Title: "Leads", Filters: []Filter{}, Widgets: []Widget{{ID: "count", Title: "Count", Kind: "kpi", Layout: Layout{Width: 3, Height: 2}, Query: source.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 1}, FilterMappings: []Mapping{}}}}
	for _, tc := range []struct {
		name string
		edit func(*Spec)
	}{
		{"version", func(s *Spec) { s.Version = 2 }},
		{"executable kind", func(s *Spec) { s.Widgets[0].Kind = "javascript" }},
		{"duplicate widget", func(s *Spec) { s.Widgets = append(s.Widgets, s.Widgets[0]) }},
		{"overlap", func(s *Spec) { w := s.Widgets[0]; w.ID = "other"; s.Widgets = append(s.Widgets, w) }},
		{"out of bounds", func(s *Spec) { s.Widgets[0].Layout.X = 12 }},
		{"negative position", func(s *Spec) { s.Widgets[0].Layout.Y = -1 }},
		{"kpi grouping", func(s *Spec) { s.Widgets[0].Query.Dimensions = []source.QueryDimension{{ID: "channel"}} }},
		{"non temporal line", func(s *Spec) {
			s.Widgets[0].Kind = "line"
			s.Widgets[0].Query.Dimensions = []source.QueryDimension{{ID: "channel"}}
		}},
		{"undefined mapping", func(s *Spec) { s.Widgets[0].FilterMappings = []Mapping{{Filter: "missing", Dimension: "channel"}} }},
		{"unused filter", func(s *Spec) { s.Filters = []Filter{{ID: "channel", Operator: "eq"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			var s Spec
			_ = json.Unmarshal(raw, &s)
			tc.edit(&s)
			if s.Validate() == nil {
				t.Fatal("accepted invalid spec")
			}
		})
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	valid.Filters = []Filter{{ID: "channel", Operator: "eq", Values: []source.Value{{Type: "string", Value: "paid"}}}}
	valid.Widgets[0].FilterMappings = []Mapping{{Filter: "channel", Dimension: "channel"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	q := valid.Query(valid.Widgets[0])
	if len(q.Filters) != 1 || q.Filters[0].Dimension != "channel" || len(valid.Widgets[0].Query.Filters) != 0 {
		t.Fatal("mapping or spec mutation")
	}
}
