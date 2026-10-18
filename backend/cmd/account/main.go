package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/nightnoryu/go-kita/jsonlog"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	store "github.com/nightnoryu/tesseract/internal/auth/infrastructure/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/postgres"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "account command failed (check action, account and database configuration)")
		os.Exit(1)
	}
}
func run() error {
	action := flag.String("action", "", "create, reset-password, disable, enable, link-oidc, or cleanup")
	raw := flag.String("email", "", "account email")
	issuer := flag.String("issuer", "", "exact OIDC issuer")
	subject := flag.String("subject", "", "verified provider subject")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "account"})
	if err != nil {
		return err
	}
	defer logger.Sync()
	db, err := postgres.Open(ctx, os.Getenv("TESSERACT_DATABASE_URL"), logger)
	if err != nil {
		return err
	}
	defer db.Close()
	s := store.Store{DB: db.TransactionalClient()}
	if *action == "cleanup" {
		return s.Cleanup(ctx)
	}
	email, err := domain.Email(*raw)
	if err != nil {
		return err
	}
	switch *action {
	case "link-oidc":
		return s.LinkOIDC(ctx, email, *issuer, *subject)
	case "disable", "enable":
		return s.SetDisabled(ctx, email, *action == "disable")
	case "create", "reset-password":
		// Read a secret from stdin, never from process arguments or logs.
		value, err := io.ReadAll(io.LimitReader(os.Stdin, 1026))
		if err != nil {
			return err
		}
		password := strings.TrimSuffix(string(value), "\n")
		hash, err := auth.Hash(password)
		if err != nil {
			return err
		}
		if *action == "create" {
			return s.Provision(ctx, email, hash)
		}
		a, err := s.Account(ctx, email)
		if err != nil {
			return err
		}
		return s.Change(ctx, a.ID, a.Hash, hash)
	default:
		return fmt.Errorf("invalid action")
	}
}
