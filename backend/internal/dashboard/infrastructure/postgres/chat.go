package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
)

func turnJSON(turn domain.Turn) (string, string, error) {
	if turn.Entries == nil {
		turn.Entries = []domain.Entry{}
	}
	if turn.Operations == nil {
		turn.Operations = []domain.Operation{}
	}
	entries, err := json.Marshal(turn.Entries)
	if err != nil {
		return "", "", err
	}
	ops, err := json.Marshal(turn.Operations)
	if err != nil {
		return "", "", err
	}
	if len(entries) > 120<<10 || len(ops) > domain.MaxSpecBytes {
		return "", "", domain.ErrInvalid
	}
	return string(entries), string(ops), nil
}

func (s Store) RecordTurn(ctx context.Context, user, id string, expected int64, turn domain.Turn) error {
	entries, ops, err := turnJSON(turn)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int64
	err = tx.GetContext(ctx, &revision, `SELECT revision FROM tesseract_app.dashboards WHERE id=$1 AND owner=$2 FOR UPDATE`, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if expected < 1 || revision != expected {
		return domain.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tesseract_app.dashboard_chat(dashboard_id,revision,kind,entries,operations) VALUES ($1,$2,$3,$4,$5)`, id, revision, turn.Kind, entries, ops); err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) Turns(ctx context.Context, user, id string, limit, offset int) ([]domain.Turn, error) {
	if limit < 1 || limit > 50 || offset < 0 {
		return nil, domain.ErrInvalid
	}
	if _, err := s.Get(ctx, user, id); err != nil {
		return nil, err
	}
	var rows []struct {
		Revision   int64     `db:"revision"`
		Kind       string    `db:"kind"`
		Entries    []byte    `db:"entries"`
		Operations []byte    `db:"operations"`
		CreatedAt  time.Time `db:"created_at"`
	}
	err := s.DB.SelectContext(ctx, &rows, `SELECT c.revision,c.kind,c.entries,c.operations,c.created_at FROM tesseract_app.dashboard_chat c JOIN tesseract_app.dashboards d ON d.id=c.dashboard_id WHERE d.id=$1 AND d.owner=$2 ORDER BY c.id DESC LIMIT $3 OFFSET $4`, id, user, limit, offset)
	if err != nil {
		return nil, err
	}
	turns := make([]domain.Turn, 0, len(rows))
	for _, r := range rows {
		t := domain.Turn{Revision: r.Revision, Kind: r.Kind, CreatedAt: r.CreatedAt}
		if err = json.Unmarshal(r.Entries, &t.Entries); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(r.Operations, &t.Operations); err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}
	return turns, nil
}
func (s Store) Revision(ctx context.Context, user, id string, revision int64) (domain.Spec, error) {
	var raw []byte
	err := s.DB.GetContext(ctx, &raw, `SELECT r.spec FROM tesseract_app.dashboard_revisions r JOIN tesseract_app.dashboards d ON d.id=r.dashboard_id WHERE d.id=$1 AND d.owner=$2 AND r.revision=$3`, id, user, revision)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Spec{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Spec{}, err
	}
	var spec domain.Spec
	err = json.Unmarshal(raw, &spec)
	return spec, err
}
