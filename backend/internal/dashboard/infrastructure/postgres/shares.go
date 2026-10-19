package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
)

// Get remains owner-only for draft edits and private chat context.
func (s Store) Read(ctx context.Context, user, id string) (domain.Dashboard, error) {
	var r row
	err := s.DB.GetContext(ctx, &r, `SELECT d.id,d.owner,r.revision,d.published_revision,d.created_at,r.created_at AS updated_at,r.spec
 FROM tesseract_app.dashboards d JOIN tesseract_app.dashboard_revisions r
 ON r.dashboard_id=d.id AND r.revision=CASE WHEN d.owner=$2 THEN d.revision ELSE d.published_revision END
 WHERE d.id=$1 AND (d.owner=$2 OR EXISTS(SELECT 1 FROM tesseract_app.dashboard_shares a WHERE a.dashboard_id=d.id AND a.user_id=$2))`, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Dashboard{}, domain.ErrDenied
	}
	if err != nil {
		return domain.Dashboard{}, err
	}
	d, err := r.dashboard()
	if d.Owner != user {
		d.Role = "viewer"
	}
	return d, err
}
func (s Store) Shares(ctx context.Context, user, id string) ([]domain.Share, error) {
	if _, err := s.Get(ctx, user, id); err != nil {
		return nil, err
	}
	out := []domain.Share{}
	err := s.DB.SelectContext(ctx, &out, `SELECT u.id AS user_id,u.email FROM tesseract_app.dashboard_shares a JOIN tesseract_app.users u ON u.id=a.user_id JOIN tesseract_app.dashboards d ON d.id=a.dashboard_id WHERE d.id=$1 AND d.owner=$2 ORDER BY u.email`, id, user)
	return out, err
}
func (s Store) Grant(ctx context.Context, user, id, email string) error {
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	err = tx.GetContext(ctx, &owner, `SELECT owner FROM tesseract_app.dashboards WHERE id=$1 AND owner=$2 FOR UPDATE`, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	var recipient string
	err = tx.GetContext(ctx, &recipient, `SELECT id FROM tesseract_app.users WHERE email=$1 AND disabled=false AND id<>$2`, strings.ToLower(strings.TrimSpace(email)), user)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrRecipient
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO tesseract_app.dashboard_shares(dashboard_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, recipient)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) Revoke(ctx context.Context, user, id, recipient string) error {
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	err = tx.GetContext(ctx, &owner, `SELECT owner FROM tesseract_app.dashboards WHERE id=$1 AND owner=$2 FOR UPDATE`, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM tesseract_app.dashboard_shares WHERE dashboard_id=$1 AND user_id=$2`, id, recipient)
	if err != nil {
		return err
	}
	return tx.Commit()
}
