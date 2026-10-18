package domain

import (
	"errors"
	"net/mail"
	"strings"
	"time"
)

var ErrCredentials = errors.New("invalid_credentials")
var ErrLimited = errors.New("rate_limited")
var ErrPassword = errors.New("invalid_password")

type Account struct {
	ID       string `db:"id"`
	Email    string `db:"email"`
	Hash     string `db:"password_hash"`
	Disabled bool   `db:"disabled"`
}
type Session struct {
	ID      string    `db:"id"`
	Email   string    `db:"email"`
	Expires time.Time `db:"expires_at"`
}

func Email(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || len(value) > 254 {
		return "", ErrCredentials
	}
	return value, nil
}
