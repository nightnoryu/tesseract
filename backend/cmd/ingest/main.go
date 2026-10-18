package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nightnoryu/go-kita/jsonlog"
	"github.com/nightnoryu/go-kita/log"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
	store "github.com/nightnoryu/tesseract/internal/metadata/infrastructure/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/postgres"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	sourceapp "github.com/nightnoryu/tesseract/internal/source/app"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"github.com/nightnoryu/tesseract/internal/source/infrastructure/mssql"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("catalog", "../catalog", "catalog directory (loaded at startup)")
	sourceID := flag.String("source", "", "scan one source; empty scans all")
	once := flag.Bool("once", false, "run one scan and exit")
	interval := flag.Duration("interval", 15*time.Minute, "delay between scan cycles")
	timeout := flag.Duration("scan-timeout", 5*time.Minute, "maximum inspection time per source")
	flag.Parse()
	if *interval < time.Second || *interval > 24*time.Hour || *timeout <= 0 || *timeout > time.Hour {
		return errors.New("invalid_ingest_schedule")
	}
	c, err := catalog.Load(os.DirFS(*dir))
	if err != nil {
		return err
	}
	ids := []string{}
	for _, s := range c.Sources {
		if *sourceID == "" || *sourceID == s.ID {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return errors.New("unknown_dwh_source")
	}
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "tesseract-ingest"})
	if err != nil {
		return errors.New("logger_configuration_failed")
	}
	defer logger.Sync()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	db, err := postgres.Open(ctx, os.Getenv("TESSERACT_DATABASE_URL"), logger)
	if err != nil {
		return err
	}
	defer db.Close()
	w := metadata.Worker{Catalog: c, Store: store.Store{DB: db.TransactionalClient()}, Timeout: *timeout,
		Open: func(ctx context.Context, c semantic.Catalog, id string) (source.Connector, error) {
			return sourceapp.Open(ctx, c, id, os.Getenv, mssql.DefaultConfig())
		}}
	for {
		var failed bool
		for _, id := range ids {
			err := w.Scan(ctx, id)
			if ctx.Err() != nil {
				return nil
			}
			fields := log.Fields{"source_id": id}
			if err != nil {
				failed = true
				fields["error_code"] = err.Error()
				logger.WithFields(fields).Warn("metadata_scan_not_published")
			} else {
				logger.WithFields(fields).Info("metadata_scan_published")
			}
		}
		if *once {
			if failed {
				return errors.New("metadata_scan_not_published")
			}
			return nil
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
