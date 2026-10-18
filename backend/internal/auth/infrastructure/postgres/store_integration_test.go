//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/nightnoryu/go-kita/jsonlog"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	platform "github.com/nightnoryu/tesseract/internal/platform/postgres"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image: "postgres:18.3-alpine", Env: map[string]string{"POSTGRES_USER": "test", "POSTGRES_PASSWORD": "test", "POSTGRES_DB": "test"}, ExposedPorts: []string{"5432/tcp"}, WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2)}, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	defer container.Terminate(context.Background())
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSSLMODE", "disable")
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "auth-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Sync()
	db, err := platform.Open(ctx, fmt.Sprintf("postgres://test:test@%s:%s/test", host, port.Port()), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Store{DB: db.TransactionalClient()}
	service := auth.New(s)
	hash, _ := auth.Hash("correct horse battery staple")
	if err := s.Provision(ctx, "user@example.com", hash); err != nil {
		t.Fatal(err)
	}
	if err := s.Provision(ctx, "user@example.com", hash); err == nil {
		t.Fatal("duplicate provision succeeded")
	}
	for _, email := range []string{"missing@example.com", "user@example.com"} {
		if _, _, err := service.Login(ctx, email, "wrong", "local", ""); !errors.Is(err, domain.ErrCredentials) {
			t.Fatal(err)
		}
	}
	session, token, err := service.Login(ctx, " User@Example.com ", "correct horse battery staple", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(ctx, auth.Digest(token)); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.DB.GetContext(ctx, &stored, `SELECT token_hash FROM tesseract_app.sessions WHERE user_id=$1`, session.ID); err != nil || stored == token {
		t.Fatal("session storage", err)
	}
	_, next, err := service.Login(ctx, "user@example.com", "correct horse battery staple", "local", token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, auth.Digest(token)); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("old token valid", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE tesseract_app.sessions SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, auth.Digest(next)); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("expired token valid", err)
	}
	_, next, err = service.Login(ctx, "user@example.com", "correct horse battery staple", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	oldAccount, err := s.Account(ctx, "user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Change(ctx, session, "correct horse battery staple", "replacement password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, auth.Digest(next)); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("reset left session valid", err)
	}
	if err := s.Issue(ctx, oldAccount, auth.Digest("stale login"), "", time.Now().Add(time.Hour)); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("stale credential issued session", err)
	}
	_, next, err = service.Login(ctx, "user@example.com", "replacement password", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(ctx, "user@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, auth.Digest(next)); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("disabled session valid", err)
	}
	if _, _, err := service.Login(ctx, "user@example.com", "replacement password", "local", ""); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("disabled login accepted", err)
	}
	if err := s.SetDisabled(ctx, "user@example.com", false); err != nil {
		t.Fatal(err)
	}
	_, next, err = service.Login(ctx, "user@example.com", "replacement password", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, auth.Digest(next)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, auth.Digest(next)); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("revoked session valid", err)
	}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			ok, err := s.Allow(ctx, "concurrent", 10)
			if err != nil {
				t.Error(err)
			}
			if ok {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatal("non-atomic rate limit", allowed.Load())
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE tesseract_app.login_limits SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Allow(ctx, "concurrent", 10); err != nil || !ok {
		t.Fatal("window did not reset", err)
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
}
