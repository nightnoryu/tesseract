package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Catalog struct {
	Version  int       `toml:"version"`
	Sources  []Source  `toml:"sources"`
	Datasets []Dataset `toml:"-"`
}
type Source struct {
	ID        string `toml:"id"`
	Kind      string `toml:"kind"`
	SecretEnv string `toml:"secret_env"`
}
type Dataset struct {
	ID          string      `toml:"id"`
	Source      string      `toml:"source"`
	Schema      string      `toml:"schema"`
	Table       string      `toml:"table"`
	Description string      `toml:"description"`
	Synonyms    []string    `toml:"synonyms" json:"Synonyms,omitempty"`
	Columns     []Column    `toml:"-"`
	Dimensions  []Dimension `toml:"-" json:"Dimensions,omitempty"`
	Measures    []Measure   `toml:"-"`
}
type Column struct {
	ID                string `toml:"id"`
	SourceColumn      string `toml:"source_column"`
	Type              string `toml:"type"`
	ExposeToAnalytics *bool  `toml:"expose_to_analytics"`
}
type Dimension struct {
	ID                   string   `toml:"id"`
	Column               string   `toml:"column"`
	Description          string   `toml:"description"`
	Synonyms             []string `toml:"synonyms" json:"Synonyms,omitempty"`
	AllowedGranularities []string `toml:"allowed_granularities" json:"AllowedGranularities,omitempty"`
	Operators            []string `toml:"operators" json:"Operators,omitempty"`
}
type Measure struct {
	ID          string   `toml:"id"`
	Column      string   `toml:"column"`
	Operation   string   `toml:"operation"`
	Description string   `toml:"description"`
	Synonyms    []string `toml:"synonyms" json:"Synonyms,omitempty"`
}

var semanticID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var physicalID = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
var envID = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func uniqueID(seen map[string]bool, id string) error {
	if !semanticID.MatchString(id) || seen[id] {
		return fmt.Errorf("invalid or duplicate ID %q", id)
	}
	seen[id] = true
	return nil
}
func synonyms(values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" || value != strings.TrimSpace(value) || seen[key] {
			return fmt.Errorf("empty or duplicate synonym")
		}
		seen[key] = true
	}
	return nil
}
func (c Catalog) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported catalog version %d", c.Version)
	}
	if len(c.Sources) == 0 || len(c.Datasets) == 0 {
		return fmt.Errorf("catalog requires sources and datasets")
	}
	sources := map[string]bool{}
	for _, s := range c.Sources {
		if err := uniqueID(sources, s.ID); err != nil {
			return err
		}
		if s.Kind != "mssql" || !envID.MatchString(s.SecretEnv) {
			return fmt.Errorf("source %s: unsupported kind or invalid secret_env", s.ID)
		}
	}
	datasets := map[string]bool{}
	tables := map[string]bool{}
	for _, d := range c.Datasets {
		if err := uniqueID(datasets, d.ID); err != nil {
			return err
		}
		if !sources[d.Source] || !physicalID.MatchString(d.Schema) || !physicalID.MatchString(d.Table) {
			return fmt.Errorf("dataset %s: unknown source or invalid physical identifier", d.ID)
		}
		table := d.Source + "." + strings.ToLower(d.Schema+"."+d.Table)
		if tables[table] {
			return fmt.Errorf("dataset %s: duplicate physical table", d.ID)
		}
		tables[table] = true
		if err := synonyms(d.Synonyms); err != nil {
			return err
		}
		if len(d.Columns) == 0 || len(d.Measures) == 0 {
			return fmt.Errorf("dataset %s requires columns and measures", d.ID)
		}
		columns := map[string]Column{}
		ids, physical := map[string]bool{}, map[string]bool{}
		for _, col := range d.Columns {
			if err := uniqueID(ids, col.ID); err != nil {
				return err
			}
			name := strings.ToLower(col.SourceColumn)
			if !physicalID.MatchString(col.SourceColumn) || physical[name] {
				return fmt.Errorf("column %s: invalid or duplicate physical identifier", col.ID)
			}
			physical[name] = true
			if !slices.Contains([]string{"string", "integer", "decimal", "boolean", "date", "datetime"}, col.Type) || col.ExposeToAnalytics == nil {
				return fmt.Errorf("column %s: unsupported type or missing exposure", col.ID)
			}
			columns[col.ID] = col
		}
		ids = map[string]bool{}
		for _, dim := range d.Dimensions {
			if err := uniqueID(ids, dim.ID); err != nil {
				return err
			}
			col, ok := columns[dim.Column]
			if !ok || !*col.ExposeToAnalytics {
				return fmt.Errorf("dimension %s: unknown or hidden column", dim.ID)
			}
			if err := synonyms(dim.Synonyms); err != nil {
				return err
			}
			temporal := col.Type == "date" || col.Type == "datetime"
			grains := map[string]bool{}
			for _, grain := range dim.AllowedGranularities {
				if !temporal || !slices.Contains([]string{"day", "week", "month", "quarter", "year"}, grain) || grains[grain] {
					return fmt.Errorf("dimension %s: invalid time grain", dim.ID)
				}
				grains[grain] = true
			}
			if temporal && len(grains) == 0 {
				return fmt.Errorf("dimension %s: time grains required", dim.ID)
			}
			ops := map[string]bool{}
			for _, op := range dim.Operators {
				allowed := slices.Contains([]string{"eq", "ne", "in", "not_in", "is_null", "is_not_null"}, op)
				allowed = allowed || (col.Type != "boolean" && col.Type != "string" && slices.Contains([]string{"lt", "lte", "gt", "gte", "between"}, op))
				if !allowed || ops[op] {
					return fmt.Errorf("dimension %s: invalid operator", dim.ID)
				}
				ops[op] = true
			}
		}
		for _, m := range d.Measures {
			if err := uniqueID(ids, m.ID); err != nil {
				return err
			}
			col, ok := columns[m.Column]
			if !ok {
				return fmt.Errorf("measure %s: unknown column", m.ID)
			}
			if err := synonyms(m.Synonyms); err != nil {
				return err
			}
			switch m.Operation {
			case "count", "count_distinct":
			case "sum", "avg":
				if col.Type != "integer" && col.Type != "decimal" {
					return fmt.Errorf("measure %s: numeric column required", m.ID)
				}
			case "min", "max":
				if col.Type != "integer" && col.Type != "decimal" && col.Type != "date" && col.Type != "datetime" {
					return fmt.Errorf("measure %s: ordered numeric or temporal column required", m.ID)
				}
			default:
				return fmt.Errorf("measure %s: unsupported operation", m.ID)
			}
		}
	}
	return nil
}

// Hash canonicalizes set ordering; descriptions and exposure changes invalidate the catalog too.
func (c Catalog) Hash() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	// Deep-copy before sorting so callers retain their original ordering.
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	var canonical Catalog
	if err := json.Unmarshal(b, &canonical); err != nil {
		return "", err
	}
	slices.SortFunc(canonical.Sources, func(a, b Source) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(canonical.Datasets, func(a, b Dataset) int { return strings.Compare(a.ID, b.ID) })
	for i := range canonical.Datasets {
		d := &canonical.Datasets[i]
		slices.Sort(d.Synonyms)
		slices.SortFunc(d.Columns, func(a, b Column) int { return strings.Compare(a.ID, b.ID) })
		slices.SortFunc(d.Dimensions, func(a, b Dimension) int { return strings.Compare(a.ID, b.ID) })
		slices.SortFunc(d.Measures, func(a, b Measure) int { return strings.Compare(a.ID, b.ID) })
		for j := range d.Dimensions {
			v := &d.Dimensions[j]
			slices.Sort(v.Synonyms)
			slices.Sort(v.AllowedGranularities)
			slices.Sort(v.Operators)
		}
		for j := range d.Measures {
			slices.Sort(d.Measures[j].Synonyms)
		}
	}
	b, err = json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
