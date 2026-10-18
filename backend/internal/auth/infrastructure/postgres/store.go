package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/nightnoryu/go-kita/postgresql"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	"time"
)

type Store struct {
	DB postgresql.TransactionalClient
}

func (s Store) Account(ctx context.Context, email string) (domain.Account, error) {
	var a domain.Account
	err := s.DB.GetContext(ctx, &a, `SELECT u.id,u.email,u.disabled,c.password_hash FROM tesseract_app.users u JOIN tesseract_app.local_credentials c ON c.user_id=u.id WHERE email=$1`, email)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrCredentials
	}
	return a, err
}
func (s Store) Allow(ctx context.Context, key string, max int) (bool, error) {
	var count int
	err := s.DB.GetContext(ctx, &count, `INSERT INTO tesseract_app.login_limits AS l VALUES ($1,1,now()+interval '15 minutes') ON CONFLICT (key) DO UPDATE SET attempts=CASE WHEN l.expires_at<=now() THEN 1 ELSE least(l.attempts+1,1000000) END, expires_at=CASE WHEN l.expires_at<=now() THEN now()+interval '15 minutes' ELSE l.expires_at END RETURNING attempts`, key)
	return count <= max, err
}
func (s Store) Issue(ctx context.Context, a domain.Account, token, old string, expires time.Time) error {
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var hash string
	err = tx.GetContext(ctx, &hash, `SELECT c.password_hash FROM tesseract_app.users u JOIN tesseract_app.local_credentials c ON c.user_id=u.id WHERE u.id=$1 AND NOT u.disabled FOR UPDATE OF u,c`, a.ID)
	if err != nil || hash != a.Hash {
		return domain.ErrCredentials
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM tesseract_app.sessions WHERE token_hash=$1`, old); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tesseract_app.sessions VALUES ($1,$2,$3)`, token, a.ID, expires); err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) Session(ctx context.Context, hash string) (domain.Session, error) {
	var session domain.Session
	err := s.DB.GetContext(ctx, &session, `SELECT u.id,u.email,s.expires_at FROM tesseract_app.sessions s JOIN tesseract_app.users u ON u.id=s.user_id WHERE token_hash=$1 AND expires_at>now() AND NOT disabled`, hash)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrCredentials
	}
	return session, err
}
func (s Store) Revoke(ctx context.Context, hash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM tesseract_app.sessions WHERE token_hash=$1`, hash)
	return err
}
func (s Store) Change(ctx context.Context, id, old, hash string) error {
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked string
	if err = tx.GetContext(ctx, &locked, `SELECT id FROM tesseract_app.users WHERE id=$1 AND NOT disabled FOR UPDATE`, id); err != nil {
		return domain.ErrCredentials
	}
	result, err := tx.ExecContext(ctx, `UPDATE tesseract_app.local_credentials SET password_hash=$1 WHERE user_id=$2 AND password_hash=$3`, hash, id, old)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrCredentials
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM tesseract_app.sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Admin commands require database access; there is no public registration/reset endpoint.
func (s Store) Provision(ctx context.Context, email, hash string) error {
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO tesseract_app.users(id,email) VALUES ($1,$2)`, id, email); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tesseract_app.local_credentials VALUES ($1,$2)`, id, hash); err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) SetDisabled(ctx context.Context, email string, disabled bool) error {
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	if err = tx.GetContext(ctx, &id, `UPDATE tesseract_app.users SET disabled=$1 WHERE email=$2 RETURNING id`, disabled, email); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM tesseract_app.sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) Cleanup(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM tesseract_app.sessions WHERE expires_at<=now()`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM tesseract_app.login_limits WHERE expires_at<=now()`)
	return err
}
