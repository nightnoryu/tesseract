package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nightnoryu/tesseract/internal/metadata/domain"
)

func (s Store) Latest(ctx context.Context, source, hash string) (domain.Snapshot, error) {
	// A single statement keeps scan identity, tables and timestamps consistent.
	var rows []struct {
		Completed  time.Time      `db:"completed_at"`
		Successful sql.NullTime   `db:"successful_at"`
		Dataset    sql.NullString `db:"dataset_id"`
		Schema     sql.NullString `db:"schema_name"`
		Name       sql.NullString `db:"table_name"`
		Present    sql.NullBool   `db:"present"`
		Reason     sql.NullString `db:"reason"`
		Inspected  sql.NullTime   `db:"inspected_at"`
	}
	err := s.DB.SelectContext(ctx, &rows, `SELECT r.completed_at,
 (SELECT max(completed_at) FROM tesseract_app.schema_scans WHERE source_id=$1 AND catalog_hash=$2 AND successful) AS successful_at,
 t.dataset_id, t.schema_name, t.table_name, t.present, t.reason, t.inspected_at
 FROM tesseract_app.schema_current c JOIN tesseract_app.schema_scans r ON r.id=c.scan_id
 LEFT JOIN tesseract_app.schema_tables t ON t.scan_id=r.id
 WHERE c.source_id=$1 AND c.catalog_hash=$2`, source, hash)
	if err != nil {
		return domain.Snapshot{}, errors.New("metadata_read_failed")
	}
	var result domain.Snapshot
	for _, r := range rows {
		result.LastScan = r.Completed
		result.LastSuccessfulScan = r.Successful.Time
		if r.Dataset.Valid {
			result.Tables = append(result.Tables, domain.Table{Dataset: r.Dataset.String, Schema: r.Schema.String, Name: r.Name.String, Present: r.Present.Bool, Reason: r.Reason.String, InspectedAt: r.Inspected.Time})
		}
	}
	return result, nil
}
