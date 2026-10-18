package app

import (
	"context"
	"time"

	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

// Grants are resolved by trusted application policy, never decoded from requests.
type Grants struct {
	Principal        string
	Source           string
	Dataset          string
	Fields           []string
	UnrestrictedRows bool
	RowFilters       []source.Filter
}
type Engine struct {
	Catalog    semantic.Catalog
	Store      metadata.SnapshotReader
	MaxAge     time.Duration
	Connectors map[string]source.Connector
}
type access struct {
	store  metadata.SnapshotReader
	maxAge time.Duration
	grants Grants
}

func (a access) CheckQueryAccess(ctx context.Context, d semantic.Dataset, hash string, fields []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g := a.grants
	if g.Principal == "" || g.Source != d.Source || g.Dataset != d.ID || (!g.UnrestrictedRows && len(g.RowFilters) == 0) || (g.UnrestrictedRows && len(g.RowFilters) != 0) {
		return source.ErrAccessDenied
	}
	allowed := map[string]bool{}
	for _, f := range g.Fields {
		allowed[f] = true
	}
	for _, f := range fields {
		if !allowed[f] {
			return source.ErrAccessDenied
		}
	}
	if a.store == nil || a.maxAge <= 0 || a.maxAge > 24*time.Hour {
		return source.ErrSchemaUnavailable
	}
	snap, err := a.store.Latest(ctx, d.Source, hash)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return source.ErrSchemaUnavailable
	}
	now := time.Now().UTC()
	if snap.LastScan.IsZero() || snap.LastScan.After(now) || now.Sub(snap.LastScan) >= a.maxAge {
		return source.ErrSchemaUnavailable
	}
	for _, t := range snap.Tables {
		if t.Dataset == d.ID && t.Schema == d.Schema && t.Name == d.Table && t.Present && t.Reason == "" && !t.InspectedAt.IsZero() && !t.InspectedAt.After(now) && now.Sub(t.InspectedAt) < a.maxAge {
			return nil
		}
	}
	return source.ErrSchemaUnavailable
}
func (e Engine) Execute(ctx context.Context, spec source.QuerySpec, grants Grants) (source.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	grants.Fields = append([]string(nil), grants.Fields...)
	q, err := source.ValidateQuery(ctx, e.Catalog, spec, access{e.Store, e.MaxAge, grants})
	if err != nil {
		return source.Result{}, err
	}
	if len(grants.RowFilters) > 0 {
		// Authorize the original request before allowing policy-only filter fields.
		// Both filter sets are ANDed by the compiler; callers cannot replace scope.
		spec = q.Spec()
		spec.Filters = append(spec.Filters, grants.RowFilters...)
		for _, f := range grants.RowFilters {
			grants.Fields = append(grants.Fields, f.Dimension)
		}
		q, err = source.ValidateQuery(ctx, e.Catalog, spec, access{e.Store, e.MaxAge, grants})
		if err != nil {
			return source.Result{}, err
		}
	}
	conn := e.Connectors[grants.Source]
	if conn == nil {
		return source.Result{}, source.ErrUnavailable
	}
	return conn.ExecuteQuery(ctx, q)
}
