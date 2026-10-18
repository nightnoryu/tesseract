package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/nightnoryu/tesseract/internal/generated/api"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
)

type CatalogConfig struct {
	Availability  metadata.Availability
	OperatorToken string
}

func (h *contract) HandleCatalogToken(ctx context.Context, _ api.OperationName, token api.CatalogToken) (context.Context, error) {
	if h.catalog == nil || len(h.catalog.OperatorToken) < 32 {
		return ctx, errors.New("unauthorized")
	}
	expected, supplied := sha256.Sum256([]byte(h.catalog.OperatorToken)), sha256.Sum256([]byte(token.Token))
	if subtle.ConstantTimeCompare(expected[:], supplied[:]) != 1 {
		return ctx, errors.New("unauthorized")
	}
	return ctx, nil
}

func (h *contract) GetCatalog(ctx context.Context) (*api.CatalogAvailability, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	hash, sources, err := h.catalog.Availability.Read(ctx, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	result := &api.CatalogAvailability{CatalogHash: hash, Sources: make([]api.SourceAvailability, 0, len(sources))}
	for _, s := range sources {
		out := api.SourceAvailability{SourceID: s.ID, Configured: s.Configured, Available: s.Available, Unavailable: s.Unavailable, Stale: s.Stale, Tables: []api.TableAvailability{}}
		if !s.LastScan.IsZero() {
			out.LastScan.SetTo(s.LastScan)
		}
		if !s.LastSuccessfulScan.IsZero() {
			out.LastSuccessfulScan.SetTo(s.LastSuccessfulScan)
		}
		for _, t := range s.Tables {
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
		result.Sources = append(result.Sources, out)
	}
	return result, nil
}
