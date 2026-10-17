package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	"github.com/nightnoryu/tesseract/internal/source/app"
	"github.com/nightnoryu/tesseract/internal/source/infrastructure/mssql"
)

func main() {
	dir := flag.String("catalog", "../catalog", "catalog directory")
	source := flag.String("source", "crm_dwh", "catalog source ID")
	dataset := flag.String("dataset", "leads", "catalog dataset ID")
	cfg := mssql.DefaultConfig()
	flag.DurationVar(&cfg.ConnectTimeout, "connect-timeout", cfg.ConnectTimeout, "connection timeout")
	flag.DurationVar(&cfg.QueryTimeout, "query-timeout", cfg.QueryTimeout, "operation timeout including queue")
	flag.IntVar(&cfg.MaxOpen, "max-open", cfg.MaxOpen, "maximum open connections")
	flag.IntVar(&cfg.MaxIdle, "max-idle", cfg.MaxIdle, "maximum idle connections")
	flag.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "maximum concurrent operations")
	flag.IntVar(&cfg.MaxRows, "max-rows", cfg.MaxRows, "maximum result rows/groups")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c, err := catalog.Load(os.DirFS(*dir))
	if err != nil {
		fail(err)
	}
	conn, err := app.Open(ctx, c, *source, os.Getenv, cfg)
	if err != nil {
		fail(err)
	}
	defer conn.Close()
	tables, err := conn.InspectCatalogTables(ctx, []string{*dataset})
	if err != nil {
		fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(tables); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
