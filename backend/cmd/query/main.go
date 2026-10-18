package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
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
	source := flag.String("source", "", "Explicit source grant")
	dataset := flag.String("dataset", "", "Explicit dataset grant")
	fields := flag.String("fields", "", "Comma-separated granted metric/dimension IDs")
	unrestricted := flag.Bool("unrestricted-rows", false, "Confirm this local operator may read all dataset rows")
	flag.Parse()
	if *source == "" || *dataset == "" || *fields == "" || !*unrestricted {
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
	conn, err := sourceapp.Open(ctx, cat, *source, os.Getenv, mssql.DefaultConfig())
	if err != nil {
		return err
	}
	defer conn.Close()
	engine := analytics.Engine{Catalog: cat, Store: metastore.Store{DB: db.TransactionalClient()}, MaxAge: 15 * time.Minute, Connectors: map[string]domain.Connector{*source: conn}}
	result, err := engine.Execute(ctx, spec, analytics.Grants{Principal: "local-operator", Source: *source, Dataset: *dataset, Fields: strings.Split(*fields, ","), UnrestrictedRows: *unrestricted})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
