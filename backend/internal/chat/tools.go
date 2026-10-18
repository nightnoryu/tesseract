package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"

	dashboard "github.com/nightnoryu/tesseract/internal/dashboard/app"
	domain "github.com/nightnoryu/tesseract/internal/dashboard/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

func strict(raw string, value any) error {
	if len(raw) > 60<<10 {
		return ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func object(properties map[string]any, required ...string) any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

var stringSchema = map[string]any{"type": "string", "maxLength": 200}

func tool(name, desc string, schema any) Tool {
	return Tool{"function", ToolDefinition{name, desc, schema}}
}
func toolDefinitions() []Tool {
	return []Tool{
		tool("search_catalog", "Search allowed available datasets by semantic ID or description; at most 20 results.", object(map[string]any{"search": stringSchema}, "search")),
		tool("get_dataset_definition", "Read authorized semantic measures and dimensions for one available dataset.", object(map[string]any{"dataset": stringSchema}, "dataset")),
		tool("get_catalog_availability", "List at most 20 authorized and schema-compatible datasets. Unavailable datasets are omitted.", object(map[string]any{})),
		tool("validate_query", "Validate a typed QuerySpec. No SQL, joins or formulas. Query fields: dataset:string, metrics:string[], dimensions:[{id,grain?}], filters:[{dimension,operator,values:[{type,value}]}], date_range?:{dimension,start:{type,value},end:{type,value}}, order:[{field,direction}], limit:1..1000. Dates are ISO date/datetime strings; end is exclusive.", object(map[string]any{"query": schemaOf(reflect.TypeOf(source.QuerySpec{}))}, "query")),
		tool("preview_query", "Execute a validated QuerySpec with limit capped at 10. Returns execution status only, never rows or values. Same QuerySpec format as validate_query.", object(map[string]any{"query": schemaOf(reflect.TypeOf(source.QuerySpec{}))}, "query")),
		tool("create_dashboard", "Create one draft using a typed operation {type:create_dashboard,spec:{version:1,title,filters:[],widgets:[{id,title,kind:kpi|line|bar|table,layout:{x,y,width,height},query:QuerySpec,filter_mappings:[]}]}}. Layout uses 12 columns, non-overlapping. Only catalog-approved metrics; no guessed formulas. Use only after the user's intent is unambiguous. This ends the turn.", object(map[string]any{"type": map[string]any{"type": "string", "enum": []string{"create_dashboard"}}, "spec": schemaOf(reflect.TypeOf(domain.Spec{}))}, "type", "spec")),
	}
}

type catalogEntry struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Measures    []field `json:"measures"`
	Dimensions  []field `json:"dimensions"`
}
type field struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Type        string   `json:"type,omitempty"`
	Grains      []string `json:"grains,omitempty"`
	Operators   []string `json:"operators,omitempty"`
}

func definitions(ctx context.Context, s *dashboard.Service, user, search, dataset string) []catalogEntry {
	out := []catalogEntry{}
	hash, err := s.Engine.Catalog.Hash()
	if err != nil {
		return out
	}
	for _, d := range s.Engine.Catalog.Datasets {
		if dataset != "" && d.ID != dataset {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(d.ID+" "+d.Description+" "+strings.Join(d.Synonyms, " ")), strings.ToLower(search)) {
			continue
		}
		g, err := s.Policy.Resolve(user, d.ID, hash)
		if err != nil {
			continue
		}
		entry := catalogEntry{ID: d.ID, Description: d.Description, Measures: []field{}, Dimensions: []field{}}
		for _, m := range d.Measures {
			if slices.Contains(g.Fields, m.ID) {
				entry.Measures = append(entry.Measures, field{ID: m.ID, Description: m.Description})
			}
		}
		if len(entry.Measures) == 0 {
			continue
		}
		if _, err = s.Engine.Validate(ctx, source.QuerySpec{Dataset: d.ID, Metrics: []string{entry.Measures[0].ID}, Limit: 1}, g); err != nil {
			continue
		}
		for _, dim := range d.Dimensions {
			if slices.Contains(g.Fields, dim.ID) {
				typ := ""
				for _, col := range d.Columns {
					if col.ID == dim.Column {
						typ = col.Type
					}
				}
				entry.Dimensions = append(entry.Dimensions, field{ID: dim.ID, Description: dim.Description, Type: typ, Grains: dim.AllowedGranularities, Operators: dim.Operators})
			}
		}
		out = append(out, entry)
		if len(out) == 20 {
			break
		}
	}
	return out
}
func runTool(ctx context.Context, s *dashboard.Service, user string, c Call) (any, *domain.Dashboard, error) {
	switch c.Function.Name {
	case "search_catalog", "get_dataset_definition", "get_catalog_availability":
		var args struct {
			Search  string `json:"search"`
			Dataset string `json:"dataset"`
		}
		if strict(c.Function.Arguments, &args) != nil || len(args.Search) > 200 || len(args.Dataset) > 200 {
			return nil, nil, ErrInvalid
		}
		if c.Function.Name == "get_dataset_definition" && args.Dataset == "" {
			return nil, nil, ErrInvalid
		}
		return definitions(ctx, s, user, args.Search, args.Dataset), nil, nil
	case "validate_query", "preview_query":
		var args struct {
			Query json.RawMessage `json:"query"`
		}
		if strict(c.Function.Arguments, &args) != nil {
			return nil, nil, ErrInvalid
		}
		q, err := source.DecodeQuery(bytes.NewReader(args.Query))
		if err != nil {
			return nil, nil, ErrInvalid
		}
		hash, err := s.Engine.Catalog.Hash()
		if err != nil {
			return nil, nil, ErrInvalid
		}
		g, err := s.Policy.Resolve(user, q.Dataset, hash)
		if err != nil {
			return nil, nil, ErrInvalid
		}
		if _, err = s.Engine.Validate(ctx, q, g); err != nil {
			return nil, nil, ErrInvalid
		}
		if c.Function.Name == "preview_query" {
			q.Limit = min(q.Limit, 10)
			if _, err = s.Engine.Execute(ctx, q, g); err != nil {
				return nil, nil, ErrInvalid
			}
		}
		return map[string]any{"valid": true}, nil, nil
	case "create_dashboard":
		var op struct {
			Type string      `json:"type"`
			Spec domain.Spec `json:"spec"`
		}
		if strict(c.Function.Arguments, &op) != nil || op.Type != "create_dashboard" || len(op.Spec.Widgets) == 0 {
			return nil, nil, ErrInvalid
		}
		if err := op.Spec.Validate(); err != nil {
			return nil, nil, ErrInvalid
		}
		// Verify each widget against the live warehouse before committing the draft.
		// Results are deliberately discarded and never enter a model message.
		for _, w := range op.Spec.Widgets {
			q := op.Spec.Query(w)
			q.Limit = min(q.Limit, 10)
			if _, err := s.Policy.Execute(ctx, s.Engine, user, q); err != nil {
				return nil, nil, ErrInvalid
			}
		}
		d, err := s.Create(ctx, user, op.Spec)
		if err != nil && d.ID == "" {
			return nil, nil, ErrInvalid
		}
		// Metadata decoration may fail after commit. Never retry a persisted draft.
		if err != nil {
			d.WidgetStates = make([]domain.State, 0, len(d.Spec.Widgets))
			for _, w := range d.Spec.Widgets {
				d.WidgetStates = append(d.WidgetStates, domain.State{ID: w.ID, Status: "unavailable"})
			}
		}
		return map[string]any{"created": true}, &d, nil
	}
	return nil, nil, ErrInvalid
}

// Generate tool schemas from the same Go types decoded and validated by the backend.
func schemaOf(t reflect.Type) any {
	if t.Kind() == reflect.Pointer {
		return schemaOf(t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			parts := strings.Split(f.Tag.Get("json"), ",")
			if parts[0] == "" || parts[0] == "-" {
				continue
			}
			props[parts[0]] = schemaOf(f.Type)
			if !slices.Contains(parts, "omitempty") {
				required = append(required, parts[0])
			}
		}
		return object(props, required...)
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem()), "maxItems": 24}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	default:
		return map[string]any{"type": "string", "maxLength": 2048}
	}
}
