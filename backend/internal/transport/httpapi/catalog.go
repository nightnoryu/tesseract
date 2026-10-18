package httpapi

import (
	"context"
	access "github.com/nightnoryu/tesseract/internal/access/app"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	auth "github.com/nightnoryu/tesseract/internal/auth/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"time"

	"github.com/nightnoryu/tesseract/internal/generated/api"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
)

type CatalogConfig struct {
	Availability metadata.Availability
	Auth         *AuthConfig
	Policy       *access.Policy
	Engine       analytics.Engine
}

func (h *contract) GetCatalog(ctx context.Context) (*api.CatalogAvailability, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	session, ok := ctx.Value(sessionKey{}).(auth.Session)
	if !ok || h.catalog == nil {
		return nil, source.ErrAccessDenied
	}
	hash, err := h.catalog.Availability.Catalog.Hash()
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, d := range h.catalog.Availability.Catalog.Datasets {
		if _, err := h.catalog.Policy.Resolve(session.ID, d.ID, hash); err == nil {
			allowed[d.ID] = true
		}
	}
	if len(allowed) == 0 {
		return &api.CatalogAvailability{CatalogHash: hash, Sources: []api.SourceAvailability{}}, nil
	}
	hash, sources, err := h.catalog.Availability.Read(ctx, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	result := &api.CatalogAvailability{CatalogHash: hash, Sources: make([]api.SourceAvailability, 0, len(sources))}
	for _, s := range sources {
		out := api.SourceAvailability{SourceID: s.ID, Tables: []api.TableAvailability{}}
		if !s.LastScan.IsZero() {
			out.LastScan.SetTo(s.LastScan)
		}
		if !s.LastSuccessfulScan.IsZero() {
			out.LastSuccessfulScan.SetTo(s.LastSuccessfulScan)
		}
		for _, t := range s.Tables {
			if !allowed[t.Dataset] {
				continue
			}
			out.Configured++
			if t.Reason == "" {
				out.Available++
			} else {
				out.Unavailable++
			}
			if t.Stale {
				out.Stale++
			}
			table := api.TableAvailability{DatasetID: t.Dataset, Schema: t.Schema, Table: t.Name, Status: api.TableAvailabilityStatusAvailable, Stale: t.Stale}
			if t.Reason != "" {
				table.Status = api.TableAvailabilityStatusUnavailable
				table.Reason.SetTo(api.TableAvailabilityReason(t.Reason))
			}
			if !t.InspectedAt.IsZero() {
				table.InspectedAt.SetTo(t.InspectedAt)
			}
			out.Tables = append(out.Tables, table)
		}
		if out.Configured > 0 {
			result.Sources = append(result.Sources, out)
		}
	}
	return result, nil
}
