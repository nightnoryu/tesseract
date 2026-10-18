package domain

import (
	"encoding/json"
	"errors"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalid  = errors.New("invalid_dashboard")
	ErrNotFound = errors.New("dashboard_not_found")
	ErrConflict = errors.New("dashboard_revision_conflict")
)

const MaxSpecBytes = 60 << 10

type Spec struct {
	Version int      `json:"version"`
	Title   string   `json:"title"`
	Filters []Filter `json:"filters"`
	Widgets []Widget `json:"widgets"`
}
type Filter struct {
	ID       string         `json:"id"`
	Operator string         `json:"operator"`
	Values   []source.Value `json:"values,omitempty"`
}
type Mapping struct {
	Filter    string `json:"filter"`
	Dimension string `json:"dimension"`
}
type Layout struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}
type Widget struct {
	ID             string           `json:"id"`
	Title          string           `json:"title"`
	Kind           string           `json:"kind"`
	Layout         Layout           `json:"layout"`
	Query          source.QuerySpec `json:"query"`
	FilterMappings []Mapping        `json:"filter_mappings"`
}
type State struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type Dashboard struct {
	ID                string    `json:"id"`
	Owner             string    `json:"owner"`
	Revision          int64     `json:"revision"`
	PublishedRevision int64     `json:"published_revision"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Spec              Spec      `json:"spec"`
	WidgetStates      []State   `json:"widget_states"`
}
type Revision struct {
	Revision     int64     `json:"revision"`
	Published    bool      `json:"published"`
	CreatedAt    time.Time `json:"created_at"`
	Spec         Spec      `json:"spec"`
	WidgetStates []State   `json:"widget_states"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func (s Spec) Validate() error {
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > MaxSpecBytes || s.Version != 1 || strings.TrimSpace(s.Title) == "" || len(s.Title) > 200 || len(s.Widgets) > 24 || len(s.Filters) > 16 {
		return ErrInvalid
	}
	filters := map[string]bool{}
	for _, f := range s.Filters {
		if !identifier.MatchString(f.ID) || filters[f.ID] {
			return ErrInvalid
		}
		filters[f.ID] = true
	}
	ids := map[string]bool{}
	used := map[string]bool{}
	for i, w := range s.Widgets {
		if !identifier.MatchString(w.ID) || ids[w.ID] || strings.TrimSpace(w.Title) == "" || len(w.Title) > 200 || len(w.FilterMappings) > 16 {
			return ErrInvalid
		}
		ids[w.ID] = true
		l := w.Layout
		if l.X < 0 || l.X > 11 || l.Y < 0 || l.Y > 99 || l.Width < 1 || l.Width > 12 || l.Height < 1 || l.Height > 100 || l.X+l.Width > 12 || l.Y+l.Height > 100 {
			return ErrInvalid
		}
		for _, other := range s.Widgets[:i] {
			r := other.Layout
			if l.X < r.X+r.Width && r.X < l.X+l.Width && l.Y < r.Y+r.Height && r.Y < l.Y+l.Height {
				return ErrInvalid
			}
		}
		switch w.Kind {
		case "kpi":
			if len(w.Query.Metrics) != 1 || len(w.Query.Dimensions) != 0 {
				return ErrInvalid
			}
		case "line":
			if len(w.Query.Dimensions) != 1 || w.Query.Dimensions[0].Grain == "" {
				return ErrInvalid
			}
		case "bar":
			if len(w.Query.Dimensions) != 1 {
				return ErrInvalid
			}
		case "table":
		default:
			return ErrInvalid
		}
		mapped := map[string]bool{}
		for _, m := range w.FilterMappings {
			if !filters[m.Filter] || mapped[m.Filter] || !identifier.MatchString(m.Dimension) {
				return ErrInvalid
			}
			mapped[m.Filter] = true
			used[m.Filter] = true
		}
	}
	for id := range filters {
		if !used[id] {
			return ErrInvalid
		}
	}
	return nil
}

// Only explicit mappings apply global filters; widgets can intentionally omit them.
func (s Spec) Query(w Widget) source.QuerySpec {
	q := w.Query
	q.Filters = append([]source.Filter(nil), q.Filters...)
	for _, m := range w.FilterMappings {
		for _, f := range s.Filters {
			if f.ID == m.Filter {
				q.Filters = append(q.Filters, source.Filter{Dimension: m.Dimension, Operator: f.Operator, Values: f.Values})
			}
		}
	}
	return q
}
