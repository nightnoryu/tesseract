package app

import (
	"context"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	"time"
)

type OIDCAttempt struct {
	StateHash      string    `db:"state_hash"`
	BrowserHash    string    `db:"browser_hash"`
	Nonce          string    `db:"nonce"`
	Verifier       string    `db:"verifier"`
	OldSessionHash string    `db:"old_session_hash"`
	Expires        time.Time `db:"expires_at"`
}
type OIDCStore interface {
	SaveOIDCAttempt(context.Context, OIDCAttempt) error
	ConsumeOIDCAttempt(context.Context, string, string) (OIDCAttempt, error)
	IssueOIDC(context.Context, string, string, string, string, time.Time) (domain.Session, error)
}
