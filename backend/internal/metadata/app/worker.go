package app

import (
	"context"
	"errors"
	"github.com/nightnoryu/tesseract/internal/metadata/domain"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"time"
)

type Store interface {
	Scan(context.Context, string, string, func() []domain.Table) error
}
type Open func(context.Context, semantic.Catalog, string) (source.Connector, error)
type Worker struct {
	Catalog semantic.Catalog
	Store   Store
	Open    Open
	Timeout time.Duration
}

func (w Worker) Scan(ctx context.Context, sourceID string) error {
	hash, err := w.Catalog.Hash()
	if err != nil {
		return err
	}
	if w.Timeout <= 0 || w.Timeout > time.Hour {
		return errors.New("invalid_scan_timeout")
	}
	found := false
	for _, s := range w.Catalog.Sources {
		if s.ID == sourceID {
			found = true
		}
	}
	if !found {
		return errors.New("unknown_dwh_source")
	}
	// Allow bounded publication time after a DWH deadline, so timeout failures
	// replace an earlier green snapshot instead of rolling back to it.
	storeCtx, cancel := context.WithTimeout(ctx, w.Timeout+30*time.Second)
	defer cancel()
	return w.Store.Scan(storeCtx, sourceID, hash, func() []domain.Table {
		scanCtx, stop := context.WithTimeout(ctx, w.Timeout)
		defer stop()
		conn, openErr := w.Open(scanCtx, w.Catalog, sourceID)
		if openErr == nil {
			defer conn.Close()
		}
		var result []domain.Table
		for _, d := range w.Catalog.Datasets {
			if d.Source != sourceID {
				continue
			}
			failure := ""
			var actual source.Table
			if openErr != nil {
				failure = "scan_failed"
				if errors.Is(openErr, source.ErrAccessDenied) {
					failure = "access_denied"
				}
			} else {
				tables, err := conn.InspectCatalogTables(scanCtx, []string{d.ID})
				if err != nil || len(tables) != 1 {
					failure = "scan_failed"
					if errors.Is(err, source.ErrAccessDenied) {
						failure = "access_denied"
					}
				} else {
					actual = tables[0]
				}
			}
			result = append(result, domain.Reconcile(d, actual, failure))
		}
		return result
	})
}
