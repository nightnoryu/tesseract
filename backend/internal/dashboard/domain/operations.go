package domain

import (
	"encoding/json"
	"slices"
	"time"
)

// Each operation changes one named element; absent payloads never mean deletion.
type Operation struct {
	Type   string  `json:"type"`
	ID     string  `json:"id,omitempty"`
	Title  string  `json:"title,omitempty"`
	Widget *Widget `json:"widget,omitempty"`
	Filter *Filter `json:"filter,omitempty"`
	Layout *Layout `json:"layout,omitempty"`
}
type Entry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Turn struct {
	Revision   int64       `json:"revision"`
	Kind       string      `json:"kind"`
	Entries    []Entry     `json:"entries"`
	Operations []Operation `json:"operations"`
	CreatedAt  time.Time   `json:"created_at"`
}

func Apply(spec Spec, ops []Operation) (Spec, error) {
	if len(ops) == 0 || len(ops) > 24 {
		return Spec{}, ErrInvalid
	}
	// Work on a deep copy so even a rejected batch cannot alter the caller's draft.
	raw, err := json.Marshal(spec)
	if err != nil {
		return Spec{}, ErrInvalid
	}
	var next Spec
	if json.Unmarshal(raw, &next) != nil {
		return Spec{}, ErrInvalid
	}
	for _, op := range ops {
		wi := slices.IndexFunc(next.Widgets, func(w Widget) bool { return w.ID == op.ID })
		fi := slices.IndexFunc(next.Filters, func(f Filter) bool { return f.ID == op.ID })
		switch op.Type {
		case "rename_dashboard":
			if op.ID != "" || op.Title == "" || op.Widget != nil || op.Filter != nil || op.Layout != nil {
				return Spec{}, ErrInvalid
			}
			next.Title = op.Title
		case "add_widget", "update_widget":
			if op.Widget == nil || op.ID != op.Widget.ID || op.Title != "" || op.Filter != nil || op.Layout != nil {
				return Spec{}, ErrInvalid
			}
			if op.Type == "add_widget" {
				if wi >= 0 {
					return Spec{}, ErrInvalid
				}
				next.Widgets = append(next.Widgets, *op.Widget)
			} else {
				if wi < 0 {
					return Spec{}, ErrInvalid
				}
				next.Widgets[wi] = *op.Widget
			}
		case "remove_widget", "move_widget":
			if wi < 0 || op.Title != "" || op.Widget != nil || op.Filter != nil {
				return Spec{}, ErrInvalid
			}
			if op.Type == "move_widget" {
				if op.Layout == nil {
					return Spec{}, ErrInvalid
				}
				next.Widgets[wi].Layout = *op.Layout
			} else {
				if op.Layout != nil {
					return Spec{}, ErrInvalid
				}
				next.Widgets = slices.Delete(next.Widgets, wi, wi+1)
			}
		case "add_filter", "update_filter":
			if op.Filter == nil || op.ID != op.Filter.ID || op.Title != "" || op.Widget != nil || op.Layout != nil {
				return Spec{}, ErrInvalid
			}
			if op.Type == "add_filter" {
				if fi >= 0 {
					return Spec{}, ErrInvalid
				}
				next.Filters = append(next.Filters, *op.Filter)
			} else {
				if fi < 0 {
					return Spec{}, ErrInvalid
				}
				next.Filters[fi] = *op.Filter
			}
		case "remove_filter":
			if fi < 0 || op.Title != "" || op.Widget != nil || op.Filter != nil || op.Layout != nil {
				return Spec{}, ErrInvalid
			}
			next.Filters = slices.Delete(next.Filters, fi, fi+1)
			for i := range next.Widgets {
				next.Widgets[i].FilterMappings = slices.DeleteFunc(next.Widgets[i].FilterMappings, func(m Mapping) bool { return m.Filter == op.ID })
			}
		default:
			return Spec{}, ErrInvalid
		}
	}
	if err := next.Validate(); err != nil {
		return Spec{}, err
	}
	return next, nil
}
