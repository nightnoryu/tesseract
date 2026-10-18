package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	access "github.com/nightnoryu/tesseract/internal/access/app"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nightnoryu/go-kita/jsonlog"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	metastore "github.com/nightnoryu/tesseract/internal/metadata/infrastructure/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/postgres"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	sourceapp "github.com/nightnoryu/tesseract/internal/source/app"
	"github.com/nightnoryu/tesseract/internal/source/domain"
	"github.com/nightnoryu/tesseract/internal/source/infrastructure/mssql"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("catalog", "../catalog", "Reviewed catalog directory")
	user := flag.String("user", "", "Local user UUID whose data entitlements apply")
	flag.Parse()
	if *user == "" {
		return domain.ErrAccessDenied
	}
	spec, err := domain.DecodeQuery(os.Stdin)
	if err != nil {
		return err
	}
	cat, err := catalog.Load(os.DirFS(*dir))
	if err != nil {
		return domain.ErrInvalidQuery
	}
	f, err := os.Open(os.Getenv("TESSERACT_ACCESS_POLICY"))
	if err != nil {
		return access.ErrPolicy
	}
	policy, err := access.Load(f, cat)
	_ = f.Close()
	if err != nil {
		return err
	}
	hash, _ := cat.Hash()
	grants, err := policy.Resolve(*user, spec.Dataset, hash)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "tesseract-query"})
	if err != nil {
		return err
	}
	defer logger.Sync()
	db, err := postgres.Open(ctx, os.Getenv("TESSERACT_DATABASE_URL"), logger)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := sourceapp.Open(ctx, cat, grants.Source, os.Getenv, mssql.DefaultConfig())
	if err != nil {
		return err
	}
	defer conn.Close()
	engine := analytics.Engine{Catalog: cat, Store: metastore.Store{DB: db.TransactionalClient()}, MaxAge: 15 * time.Minute, Connectors: map[string]domain.Connector{grants.Source: conn}}
	result, err := policy.Execute(ctx, engine, *user, spec)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
