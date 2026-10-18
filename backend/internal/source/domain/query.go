package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"time"

	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
)

const MaxQueryBytes = 65536

var ErrSchemaUnavailable = errors.New("query_schema_unavailable")

// QuerySpec contains semantic IDs only. Values are typed strings so decimal and
// integer filters retain precision across JSON clients.
type QuerySpec struct {
	Dataset    string           `json:"dataset"`
	Metrics    []string         `json:"metrics"`
	Dimensions []QueryDimension `json:"dimensions,omitempty"`
	Filters    []Filter         `json:"filters,omitempty"`
	DateRange  *DateRange       `json:"date_range,omitempty"`
	Order      []Order          `json:"order,omitempty"`
	Limit      int              `json:"limit"`
}
type QueryDimension struct {
	ID    string `json:"id"`
	Grain string `json:"grain,omitempty"`
}
type Value struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (v *Value) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type  *string `json:"type"`
		Value *string `json:"value"`
	}
	if err := strictJSON(data, &raw); err != nil || raw.Type == nil || raw.Value == nil {
		return ErrInvalidQuery
	}
	v.Type, v.Value = *raw.Type, *raw.Value
	return nil
}

type Filter struct {
	Dimension string  `json:"dimension"`
	Operator  string  `json:"operator"`
	Values    []Value `json:"values,omitempty"`
}
type DateRange struct {
	Dimension string `json:"dimension"`
	Start     Value  `json:"start"`
	End       Value  `json:"end"`
}
type Order struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

// QueryAccess is supplied by trusted application code, never request data.
// It checks current metadata and the caller's grants on every execution.
type QueryAccess interface {
	CheckQueryAccess(context.Context, semantic.Dataset, string, []string) error
}

type ValidatedQuery struct {
	spec    QuerySpec
	hash    string
	dataset semantic.Dataset
	fields  []string
	access  QueryAccess
}

func (q ValidatedQuery) Dataset() string     { return q.spec.Dataset }
func (q ValidatedQuery) CatalogHash() string { return q.hash }
func (q ValidatedQuery) Spec() QuerySpec {
	b, _ := json.Marshal(q.spec)
	var out QuerySpec
	_ = json.Unmarshal(b, &out)
	return out
}
func (q ValidatedQuery) CheckAccess(ctx context.Context) error {
	if q.access == nil || q.hash == "" {
		return ErrInvalidQuery
	}
	// Do not let a policy implementation mutate the plan's snapshot.
	b, _ := json.Marshal(q.dataset)
	var d semantic.Dataset
	_ = json.Unmarshal(b, &d)
	return q.access.CheckQueryAccess(ctx, d, q.hash, slices.Clone(q.fields))
}

func DecodeQuery(r io.Reader) (QuerySpec, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxQueryBytes+1))
	if err != nil || len(data) > MaxQueryBytes {
		return QuerySpec{}, ErrInvalidQuery
	}
	var q QuerySpec
	// A strict decoder rejects SQL, joins, formulas and unknown nested fields.
	err = strictJSON(data, &q)
	return q, err
}

func ValidateQuery(ctx context.Context, catalog semantic.Catalog, spec QuerySpec, access QueryAccess) (ValidatedQuery, error) {
	invalid := func() (ValidatedQuery, error) { return ValidatedQuery{}, ErrInvalidQuery }
	hash, err := catalog.Hash()
	if err != nil || access == nil {
		return invalid()
	}
	b, err := json.Marshal(spec)
	if err != nil || len(b) > MaxQueryBytes {
		return invalid()
	}
	var q QuerySpec
	_ = json.Unmarshal(b, &q)
	var d semantic.Dataset
	for _, candidate := range catalog.Datasets {
		if candidate.ID == q.Dataset {
			d = candidate
			break
		}
	}
	if d.ID == "" || len(q.Metrics) < 1 || len(q.Metrics) > 16 || len(q.Dimensions) > 8 || len(q.Filters) > 16 || len(q.Order) > 24 || q.Limit < 1 || q.Limit > 1000 {
		return invalid()
	}
	columns := map[string]semantic.Column{}
	dims := map[string]semantic.Dimension{}
	metrics := map[string]semantic.Measure{}
	for _, c := range d.Columns {
		columns[c.ID] = c
	}
	for _, v := range d.Dimensions {
		dims[v.ID] = v
	}
	for _, v := range d.Measures {
		metrics[v.ID] = v
	}
	selected := map[string]bool{}
	fields := []string{}
	for _, id := range q.Metrics {
		if _, ok := metrics[id]; !ok || selected[id] {
			return invalid()
		}
		selected[id] = true
		fields = append(fields, id)
	}
	for _, v := range q.Dimensions {
		dim, ok := dims[v.ID]
		if !ok || selected[v.ID] {
			return invalid()
		}
		temporal := columns[dim.Column].Type == "date" || columns[dim.Column].Type == "datetime"
		if temporal && !slices.Contains(dim.AllowedGranularities, v.Grain) || !temporal && v.Grain != "" {
			return invalid()
		}
		selected[v.ID] = true
		fields = append(fields, v.ID)
	}
	for _, f := range q.Filters {
		dim, ok := dims[f.Dimension]
		if !ok || !slices.Contains(dim.Operators, f.Operator) {
			return invalid()
		}
		n := len(f.Values)
		switch f.Operator {
		case "is_null", "is_not_null":
			if n != 0 {
				return invalid()
			}
		case "in", "not_in":
			if n < 1 || n > 100 {
				return invalid()
			}
		case "between":
			if n != 2 {
				return invalid()
			}
		default:
			if n != 1 {
				return invalid()
			}
		}
		for _, v := range f.Values {
			if v.Type != columns[dim.Column].Type {
				return invalid()
			}
			if _, err := v.Parameter(); err != nil {
				return invalid()
			}
		}
		fields = append(fields, f.Dimension)
	}
	if r := q.DateRange; r != nil {
		dim, ok := dims[r.Dimension]
		typ := columns[dim.Column].Type
		if !ok || (typ != "date" && typ != "datetime") || r.Start.Type != typ || r.End.Type != typ || !slices.Contains(dim.Operators, "gte") || !slices.Contains(dim.Operators, "lt") {
			return invalid()
		}
		start, e1 := r.Start.Parameter()
		end, e2 := r.End.Parameter()
		if e1 != nil || e2 != nil || !start.(time.Time).Before(end.(time.Time)) {
			return invalid()
		}
		fields = append(fields, r.Dimension)
	}
	ordered := map[string]bool{}
	for _, o := range q.Order {
		if !selected[o.Field] || ordered[o.Field] || (o.Direction != "asc" && o.Direction != "desc") {
			return invalid()
		}
		ordered[o.Field] = true
	}
	b, _ = json.Marshal(d)
	var snapshot semantic.Dataset
	_ = json.Unmarshal(b, &snapshot)
	out := ValidatedQuery{spec: q, hash: hash, dataset: snapshot, fields: fields, access: access}
	if err := out.CheckAccess(ctx); err != nil {
		return ValidatedQuery{}, err
	}
	return out, nil
}

var decimalValue = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,19})(\.[0-9]{1,18})?$`)

func (v Value) Parameter() (any, error) {
	switch v.Type {
	case "string":
		if len(v.Value) <= 2048 {
			return v.Value, nil
		}
	case "integer":
		n, err := strconv.ParseInt(v.Value, 10, 64)
		if err == nil {
			return n, nil
		}
	case "decimal":
		if decimalValue.MatchString(v.Value) {
			return v.Value, nil
		}
	case "boolean":
		if v.Value == "true" {
			return true, nil
		}
		if v.Value == "false" {
			return false, nil
		}
	case "date", "datetime":
		format := "2006-01-02"
		if v.Type == "datetime" {
			format = time.RFC3339Nano
		}
		t, err := time.Parse(format, v.Value)
		if err == nil && t.Year() >= 1 && t.Year() <= 9999 {
			utc := t.UTC()
			if utc.Year() >= 1 && utc.Year() <= 9999 {
				return utc, nil
			}
		}
	}
	return nil, ErrInvalidQuery
}

func strictJSON(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return ErrInvalidQuery
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return ErrInvalidQuery
	}
	return nil
}
