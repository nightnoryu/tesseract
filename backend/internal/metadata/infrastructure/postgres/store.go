package postgres

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/nightnoryu/go-kita/postgresql"
	"github.com/nightnoryu/tesseract/internal/metadata/domain"
	"time"
)

var ErrOverlap = errors.New("metadata_scan_already_running")

type Store struct {
	DB postgresql.TransactionalClient
}

// The transaction lock spans inspection and publication, and releases on crash.
// Readers see either the previous complete snapshot or the new complete snapshot.
func (s Store) Scan(ctx context.Context, source, hash string, inspect func() []domain.Table) error {
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return errors.New("metadata_store_unavailable")
	}
	defer tx.Rollback()
	var locked bool
	if err := tx.GetContext(ctx, &locked, "SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))", "tesseract.metadata."+source); err != nil {
		return errors.New("metadata_lock_failed")
	}
	if !locked {
		return ErrOverlap
	}
	started := time.Now().UTC()
	tables := inspect()
	if err := ctx.Err(); err != nil {
		return err
	}
	id := uuid.NewString()
	success := true
	for _, t := range tables {
		if t.Reason != "" {
			success = false
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO tesseract_app.schema_scans VALUES ($1,$2,$3,$4,$5,$6)", id, source, hash, started, time.Now().UTC(), success)
	if err != nil {
		return errors.New("metadata_publish_failed")
	}
	for _, t := range tables {
		_, err = tx.ExecContext(ctx, "INSERT INTO tesseract_app.schema_tables VALUES ($1,$2,$3,$4,$5,$6,$7)", id, t.Dataset, t.Schema, t.Name, t.Present, t.Reason, t.InspectedAt)
		if err != nil {
			return errors.New("metadata_publish_failed")
		}
		for _, c := range t.Columns {
			_, err = tx.ExecContext(ctx, "INSERT INTO tesseract_app.schema_columns VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)", id, t.Dataset, c.ID, c.Name, c.ExpectedType, c.Present, c.Actual.Type, c.Actual.Nullable, c.Actual.MaxLength, c.Actual.Precision, c.Actual.Scale, c.Reason)
			if err != nil {
				return errors.New("metadata_publish_failed")
			}
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO tesseract_app.schema_current VALUES ($1,$2,$3) ON CONFLICT (source_id,catalog_hash) DO UPDATE SET scan_id=EXCLUDED.scan_id", source, hash, id)
	if err != nil {
		return errors.New("metadata_publish_failed")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("metadata_publish_failed")
	}
	return nil
}
