package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	"time"
)

type Store interface {
	Account(context.Context, string) (domain.Account, error)
	Allow(context.Context, string, int) (bool, error)
	Issue(context.Context, domain.Account, string, string, time.Time) error
	Session(context.Context, string) (domain.Session, error)
	Revoke(context.Context, string) error
	Change(context.Context, string, string, string) error
}
type Service struct {
	Store Store
	dummy string
	slots chan struct{}
}

func New(store Store) *Service {
	dummy, _ := Hash(rand.Text())
	return &Service{Store: store, dummy: dummy, slots: make(chan struct{}, 4)}
}
func Digest(token string) string { h := sha256.Sum256([]byte(token)); return hex.EncodeToString(h[:]) }
func (s *Service) acquire() bool {
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}
func (s *Service) Login(ctx context.Context, email, password, ip, old string) (domain.Session, string, error) {
	normalized, err := domain.Email(email)
	if err != nil {
		normalized = "invalid"
	}
	for _, limit := range []struct {
		key string
		max int
	}{{"ip:" + ip, 50}, {"email:" + normalized, 10}} {
		ok, err := s.Store.Allow(ctx, Digest(limit.key), limit.max)
		if err != nil {
			return domain.Session{}, "", err
		}
		if !ok {
			return domain.Session{}, "", domain.ErrLimited
		}
	}
	if !s.acquire() {
		return domain.Session{}, "", domain.ErrLimited
	}
	defer func() { <-s.slots }()
	account, err := s.Store.Account(ctx, normalized)
	hash := s.dummy
	if err == nil {
		hash = account.Hash
	}
	valid := Verify(hash, password)
	if err != nil || !valid || account.Disabled {
		return domain.Session{}, "", domain.ErrCredentials
	}
	token := rand.Text() + rand.Text()
	expires := time.Now().UTC().Add(12 * time.Hour)
	if err := s.Store.Issue(ctx, account, Digest(token), Digest(old), expires); err != nil {
		return domain.Session{}, "", err
	}
	return domain.Session{ID: account.ID, Email: account.Email, Expires: expires}, token, nil
}
func (s *Service) Change(ctx context.Context, session domain.Session, current, next string) error {
	ok, err := s.Store.Allow(ctx, Digest("change:"+session.ID), 10)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrLimited
	}
	if !s.acquire() {
		return domain.ErrLimited
	}
	defer func() { <-s.slots }()
	a, err := s.Store.Account(ctx, session.Email)
	if err != nil {
		return err
	}
	if a.Disabled || !Verify(a.Hash, current) {
		return domain.ErrCredentials
	}
	hash, err := Hash(next)
	if err != nil {
		return err
	}
	return s.Store.Change(ctx, a.ID, a.Hash, hash)
}
