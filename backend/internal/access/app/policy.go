package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/google/uuid"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

var ErrPolicy = errors.New("invalid_access_policy")

// Policy is an immutable, operator-managed snapshot. No grants come from HTTP,
// OIDC claims, or dashboard ownership.
type Policy struct {
	hash   string
	grants map[string]map[string]analytics.Grants
}
type rule struct {
	UserID           string          `json:"user_id"`
	Source           string          `json:"source"`
	Dataset          string          `json:"dataset"`
	Fields           []string        `json:"fields"`
	UnrestrictedRows bool            `json:"unrestricted_rows"`
	RowFilters       []source.Filter `json:"row_filters"`
}
type document struct {
	Version     int    `json:"version"`
	CatalogHash string `json:"catalog_hash"`
	Grants      []rule `json:"grants"`
}
type validationAccess struct{}

func (validationAccess) CheckQueryAccess(context.Context, semantic.Dataset, string, []string) error {
	return nil
}

func Load(r io.Reader, cat semantic.Catalog) (*Policy, error) {
	var doc document
	data, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrPolicy
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&doc) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, ErrPolicy
	}
	hash, err := cat.Hash()
	if err != nil || doc.Version != 1 || doc.CatalogHash != hash {
		return nil, ErrPolicy
	}
	p := &Policy{hash: hash, grants: map[string]map[string]analytics.Grants{}}
	for _, rule := range doc.Grants {
		id, err := uuid.Parse(rule.UserID)
		if err != nil || id == uuid.Nil || id.String() != rule.UserID {
			return nil, ErrPolicy
		}
		var dataset semantic.Dataset
		for _, d := range cat.Datasets {
			if d.ID == rule.Dataset && d.Source == rule.Source {
				dataset = d
			}
		}
		if dataset.ID == "" || len(rule.Fields) == 0 || rule.UnrestrictedRows == (len(rule.RowFilters) > 0) {
			return nil, ErrPolicy
		}
		fields := map[string]bool{}
		for _, m := range dataset.Measures {
			fields[m.ID] = true
		}
		for _, d := range dataset.Dimensions {
			fields[d.ID] = true
		}
		seen := map[string]bool{}
		for _, f := range rule.Fields {
			if !fields[f] || seen[f] {
				return nil, ErrPolicy
			}
			seen[f] = true
		}
		// Validate scope operators, values and references using the same rules as queries.
		_, err = source.ValidateQuery(context.Background(), cat, source.QuerySpec{Dataset: dataset.ID, Metrics: []string{dataset.Measures[0].ID}, Filters: rule.RowFilters, Limit: 1}, validationAccess{})
		if err != nil {
			return nil, ErrPolicy
		}
		if p.grants[rule.UserID] == nil {
			p.grants[rule.UserID] = map[string]analytics.Grants{}
		}
		if _, exists := p.grants[rule.UserID][rule.Dataset]; exists {
			return nil, ErrPolicy
		}
		p.grants[rule.UserID][rule.Dataset] = analytics.Grants{Principal: rule.UserID, Source: rule.Source, Dataset: rule.Dataset, Fields: rule.Fields, UnrestrictedRows: rule.UnrestrictedRows, RowFilters: rule.RowFilters}
	}
	return p, nil
}
func (p *Policy) Resolve(user, dataset, hash string) (analytics.Grants, error) {
	if p == nil || p.hash != hash || user == "" {
		return analytics.Grants{}, source.ErrAccessDenied
	}
	g, ok := p.grants[user][dataset]
	if !ok {
		return analytics.Grants{}, source.ErrAccessDenied
	}
	// Keep callers from mutating the published policy.
	g.Fields = slices.Clone(g.Fields)
	g.RowFilters = slices.Clone(g.RowFilters)
	for i := range g.RowFilters {
		g.RowFilters[i].Values = slices.Clone(g.RowFilters[i].Values)
	}
	return g, nil
}

// Execute evaluates the current viewer, including when a query belongs to
// another user's dashboard. No owner identity or ACL can supply data grants.
func (p *Policy) Execute(ctx context.Context, engine analytics.Engine, user string, spec source.QuerySpec) (source.Result, error) {
	hash, err := engine.Catalog.Hash()
	if err != nil {
		return source.Result{}, source.ErrAccessDenied
	}
	grants, err := p.Resolve(user, spec.Dataset, hash)
	if err != nil {
		return source.Result{}, err
	}
	return engine.Execute(ctx, spec, grants)
}
