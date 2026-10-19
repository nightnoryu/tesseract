package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/nightnoryu/go-kita/postgresql"
	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
	"time"
)

type Store struct {
	DB postgresql.TransactionalClient
}
type row struct {
	ID                string    `db:"id"`
	Owner             string    `db:"owner"`
	Revision          int64     `db:"revision"`
	PublishedRevision int64     `db:"published_revision"`
	CreatedAt         time.Time `db:"created_at"`
	UpdatedAt         time.Time `db:"updated_at"`
	Spec              []byte    `db:"spec"`
}

func (r row) dashboard() (domain.Dashboard, error) {
	d := domain.Dashboard{ID: r.ID, Owner: r.Owner, Revision: r.Revision, PublishedRevision: r.PublishedRevision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	err := json.Unmarshal(r.Spec, &d.Spec)
	return d, err
}

const selection = `SELECT d.id,d.owner,d.revision,d.published_revision,d.created_at,d.updated_at,r.spec FROM tesseract_app.dashboards d JOIN tesseract_app.dashboard_revisions r ON r.dashboard_id=d.id AND r.revision=d.revision `

func (s Store) Get(ctx context.Context, user, id string) (domain.Dashboard, error) {
	var r row
	err := s.DB.GetContext(ctx, &r, selection+`WHERE d.id=$1 AND d.owner=$2`, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Dashboard{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Dashboard{}, err
	}
	return r.dashboard()
}
func (s Store) List(ctx context.Context, user string, limit, offset int) ([]domain.Dashboard, error) {
	var rows []row
	err := s.DB.SelectContext(ctx, &rows, selection+`WHERE d.owner=$1 ORDER BY d.updated_at DESC,d.id LIMIT $2 OFFSET $3`, user, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Dashboard, 0, len(rows))
	for _, r := range rows {
		d, err := r.dashboard()
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}
func (s Store) Create(ctx context.Context, user string, spec domain.Spec) (domain.Dashboard, error) {
	return s.create(ctx, user, spec, nil)
}
func (s Store) CreateTurn(ctx context.Context, user string, spec domain.Spec, turn domain.Turn) (domain.Dashboard, error) {
	return s.create(ctx, user, spec, &turn)
}
func (s Store) create(ctx context.Context, user string, spec domain.Spec, turn *domain.Turn) (domain.Dashboard, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return domain.Dashboard{}, err
	}
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return domain.Dashboard{}, err
	}
	defer tx.Rollback()
	var r row
	err = tx.GetContext(ctx, &r, `INSERT INTO tesseract_app.dashboards(id,owner,revision) VALUES ($1,$2,1) RETURNING id,owner,revision,published_revision,created_at,updated_at`, uuid.NewString(), user)
	if err != nil {
		return domain.Dashboard{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tesseract_app.dashboard_revisions(dashboard_id,revision,published,spec) VALUES ($1,1,false,$2)`, r.ID, string(raw)); err != nil {
		return domain.Dashboard{}, err
	}
	if turn != nil {
		entries, ops, e := turnJSON(*turn)
		if e != nil {
			return domain.Dashboard{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO tesseract_app.dashboard_chat(dashboard_id,revision,kind,entries,operations) VALUES ($1,$2,$3,$4,$5)`, r.ID, r.Revision, turn.Kind, entries, ops); e != nil {
			return domain.Dashboard{}, e
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.Dashboard{}, err
	}
	r.Spec = raw
	return r.dashboard()
}
func (s Store) Save(ctx context.Context, user, id string, expected int64, spec domain.Spec, publish bool) (domain.Dashboard, error) {
	return s.save(ctx, user, id, expected, spec, publish, nil)
}
func (s Store) SaveTurn(ctx context.Context, user, id string, expected int64, spec domain.Spec, turn domain.Turn) (domain.Dashboard, error) {
	return s.save(ctx, user, id, expected, spec, false, &turn)
}
func (s Store) save(ctx context.Context, user, id string, expected int64, spec domain.Spec, publish bool, turn *domain.Turn) (domain.Dashboard, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return domain.Dashboard{}, err
	}
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return domain.Dashboard{}, err
	}
	defer tx.Rollback()
	var r row
	err = tx.GetContext(ctx, &r, `SELECT id,owner,revision,published_revision,created_at,updated_at FROM tesseract_app.dashboards WHERE id=$1 AND owner=$2 FOR UPDATE`, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Dashboard{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Dashboard{}, err
	}
	if expected < 1 || r.Revision != expected {
		return domain.Dashboard{}, domain.ErrConflict
	}
	err = tx.GetContext(ctx, &r, `UPDATE tesseract_app.dashboards SET revision=revision+1,published_revision=CASE WHEN $3 THEN revision+1 ELSE published_revision END,updated_at=clock_timestamp() WHERE id=$1 AND owner=$2 RETURNING id,owner,revision,published_revision,created_at,updated_at`, id, user, publish)
	if err != nil {
		return domain.Dashboard{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tesseract_app.dashboard_revisions(dashboard_id,revision,published,spec) VALUES ($1,$2,$3,$4)`, id, r.Revision, publish, string(raw)); err != nil {
		return domain.Dashboard{}, err
	}
	if turn != nil {
		entries, ops, e := turnJSON(*turn)
		if e != nil {
			return domain.Dashboard{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO tesseract_app.dashboard_chat(dashboard_id,revision,kind,entries,operations) VALUES ($1,$2,$3,$4,$5)`, r.ID, r.Revision, turn.Kind, entries, ops); e != nil {
			return domain.Dashboard{}, e
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.Dashboard{}, err
	}
	r.Spec = raw
	return r.dashboard()
}
func (s Store) Delete(ctx context.Context, user, id string, expected int64) error {
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
	if _, err = tx.ExecContext(ctx, `DELETE FROM tesseract_app.dashboards WHERE id=$1 AND owner=$2`, id, user); err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) History(ctx context.Context, user, id string, limit, offset int) ([]domain.Revision, error) {
	if _, err := s.Get(ctx, user, id); err != nil {
		return nil, err
	}
	var rows []struct {
		Revision  int64     `db:"revision"`
		Published bool      `db:"published"`
		CreatedAt time.Time `db:"created_at"`
		Spec      []byte    `db:"spec"`
	}
	err := s.DB.SelectContext(ctx, &rows, `SELECT r.revision,r.published,r.created_at,r.spec FROM tesseract_app.dashboard_revisions r JOIN tesseract_app.dashboards d ON d.id=r.dashboard_id WHERE d.id=$1 AND d.owner=$2 ORDER BY r.revision DESC LIMIT $3 OFFSET $4`, id, user, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Revision, 0, len(rows))
	for _, r := range rows {
		v := domain.Revision{Revision: r.Revision, Published: r.Published, CreatedAt: r.CreatedAt}
		if err = json.Unmarshal(r.Spec, &v.Spec); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
