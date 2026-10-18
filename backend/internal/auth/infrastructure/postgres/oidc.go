package postgres

import (
	"context"
	"database/sql"
	"errors"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	"time"
)

func (s Store) LinkOIDC(ctx context.Context, email, issuer, subject string) error {
	if issuer == "" || subject == "" || len(subject) > 255 {
		return domain.ErrCredentials
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO tesseract_app.oidc_identities(issuer,subject,user_id) SELECT $1,$2,id FROM tesseract_app.users WHERE email=$3 AND NOT disabled`, issuer, subject, email)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return domain.ErrCredentials
	}
	return err
}
func (s Store) SaveOIDCAttempt(ctx context.Context, a auth.OIDCAttempt) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO tesseract_app.oidc_attempts VALUES ($1,$2,$3,$4,$5,$6)`, a.StateHash, a.BrowserHash, a.Nonce, a.Verifier, a.OldSessionHash, a.Expires)
	return err
}
func (s Store) ConsumeOIDCAttempt(ctx context.Context, state, browser string) (auth.OIDCAttempt, error) {
	var a auth.OIDCAttempt
	err := s.DB.GetContext(ctx, &a, `DELETE FROM tesseract_app.oidc_attempts WHERE state_hash=$1 AND browser_hash=$2 AND expires_at>now() RETURNING *`, state, browser)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrCredentials
	}
	return a, err
}
func (s Store) IssueOIDC(ctx context.Context, issuer, subject, token, old string, expires time.Time) (domain.Session, error) {
	tx, err := s.DB.BeginTransaction(ctx, nil)
	if err != nil {
		return domain.Session{}, err
	}
	defer tx.Rollback()
	var session domain.Session
	// Lock the user alongside the mapping so disable cannot race session issuance.
	err = tx.GetContext(ctx, &session, `SELECT u.id,u.email,$3::timestamptz AS expires_at FROM tesseract_app.users u JOIN tesseract_app.oidc_identities i ON i.user_id=u.id WHERE i.issuer=$1 AND i.subject=$2 AND NOT u.disabled FOR UPDATE OF u,i`, issuer, subject, expires)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrCredentials
	}
	if err != nil {
		return session, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM tesseract_app.sessions WHERE token_hash=$1`, old); err != nil {
		return session, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tesseract_app.sessions VALUES ($1,$2,$3)`, token, session.ID, expires); err != nil {
		return session, err
	}
	return session, tx.Commit()
}
