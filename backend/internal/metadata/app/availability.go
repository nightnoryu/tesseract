package app

import (
	"context"
	"errors"
	"time"

	"github.com/nightnoryu/tesseract/internal/metadata/domain"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
)

type SnapshotReader interface {
	Latest(context.Context, string, string) (domain.Snapshot, error)
}
type Availability struct {
	Catalog semantic.Catalog
	Store   SnapshotReader
	MaxAge  time.Duration
}
type TableAvailability struct {
	Dataset, Schema, Name, Reason string
	InspectedAt                   time.Time
	Stale                         bool
}
type SourceAvailability struct {
	ID                                        string
	Configured, Available, Unavailable, Stale int
	LastScan, LastSuccessfulScan              time.Time
	Tables                                    []TableAvailability
}

func (a Availability) Read(ctx context.Context, now time.Time) (string, []SourceAvailability, error) {
	hash, err := a.Catalog.Hash()
	if err != nil {
		return "", nil, errors.New("invalid_catalog")
	}
	if a.MaxAge <= 0 || a.MaxAge > 24*time.Hour {
		return "", nil, errors.New("invalid_metadata_max_age")
	}
	result := make([]SourceAvailability, 0, len(a.Catalog.Sources))
	for _, source := range a.Catalog.Sources {
		snapshot, err := a.Store.Latest(ctx, source.ID, hash)
		if err != nil {
			return "", nil, err
		}
		s := SourceAvailability{ID: source.ID, LastScan: snapshot.LastScan, LastSuccessfulScan: snapshot.LastSuccessfulScan, Tables: []TableAvailability{}}
		for _, d := range a.Catalog.Datasets {
			if d.Source != source.ID {
				continue
			}
			t := TableAvailability{Dataset: d.ID, Schema: d.Schema, Name: d.Table, Reason: "not_scanned"}
			for _, stored := range snapshot.Tables {
				if stored.Dataset != d.ID || stored.Schema != d.Schema || stored.Name != d.Table {
					continue
				}
				t.InspectedAt = stored.InspectedAt
				t.Reason = stored.Reason
				switch t.Reason {
				case "", "missing_table", "missing_column", "incompatible_type", "access_denied", "scan_failed":
				default:
					t.Reason = "scan_failed"
				}
				if t.Reason == "" && !stored.Present {
					t.Reason = "missing_table"
				}
				t.Stale = snapshot.LastScan.IsZero() || stored.InspectedAt.IsZero() || now.Sub(stored.InspectedAt) >= a.MaxAge || stored.InspectedAt.After(now) || snapshot.LastScan.After(now)
				if t.Stale && t.Reason == "" {
					t.Reason = "stale"
				}
				break
			}
			s.Configured++
			if t.Reason == "" {
				s.Available++
			} else {
				s.Unavailable++
			}
			if t.Stale {
				s.Stale++
			}
			s.Tables = append(s.Tables, t)
		}
		result = append(result, s)
	}
	return hash, result, nil
}
