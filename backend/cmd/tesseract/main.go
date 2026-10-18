package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nightnoryu/go-kita/jsonlog"
	"github.com/nightnoryu/go-kita/log"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	authstore "github.com/nightnoryu/tesseract/internal/auth/infrastructure/postgres"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
	metastore "github.com/nightnoryu/tesseract/internal/metadata/infrastructure/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	"github.com/nightnoryu/tesseract/internal/transport/httpapi"
)

func main() { os.Exit(mainCode()) }

func mainCode() int {
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "tesseract"})
	if err != nil {
		return 1
	}
	defer func() { _ = logger.Sync() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.WithFields(log.Fields{"error_code": err.Error()}).Error(err, "api_stopped")
		return 1
	}
	return 0
}

func run(ctx context.Context, logger log.Logger) error {
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := postgres.Open(startup, os.Getenv("TESSERACT_DATABASE_URL"), logger)
	if err != nil {
		return err
	}
	defer func() {
		if db.Close() != nil {
			logger.WithFields(log.Fields{"error_code": "database_close_failed"}).Warn("database_close_failed")
		}
	}()
	cancel()
	address := os.Getenv("TESSERACT_HTTP_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	catalogDir := os.Getenv("TESSERACT_CATALOG_DIR")
	if catalogDir == "" {
		catalogDir = "../catalog"
	}
	cat, err := catalog.Load(os.DirFS(catalogDir))
	if err != nil {
		return errors.New("invalid_catalog")
	}
	maxAge := 15 * time.Minute
	if raw := os.Getenv("TESSERACT_METADATA_MAX_AGE"); raw != "" {
		maxAge, err = time.ParseDuration(raw)
		if err != nil || maxAge <= 0 || maxAge > 24*time.Hour {
			return errors.New("invalid_metadata_max_age")
		}
	}
	authConfig := httpapi.AuthConfig{Service: auth.New(authstore.Store{DB: db.TransactionalClient()}), Origin: os.Getenv("TESSERACT_PUBLIC_ORIGIN"), InsecureDevelopment: os.Getenv("TESSERACT_AUTH_INSECURE_DEVELOPMENT") == "true"}
	if err := authConfig.Validate(); err != nil {
		return err
	}
	handler := httpapi.New(db.Ping, logger, telemetry.New(), httpapi.CatalogConfig{Availability: metadata.Availability{Catalog: cat, Store: metastore.Store{DB: db.TransactionalClient()}, MaxAge: maxAge}, Auth: &authConfig})
	requests, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
		BaseContext: func(net.Listener) context.Context { return requests },
		// net/http can log raw panic values and request bytes; middleware handles safe request logging.
		ErrorLog: stdLogger(logger),
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("http_listen_failed")
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("api_started")
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("http_serve_failed")
		}
		return nil
	case <-ctx.Done():
		handler.Drain()
		logger.Info("api_draining")
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := server.Shutdown(shutdown); err != nil {
			cancelRequests()
			_ = server.Close()
			return errors.New("http_shutdown_timeout")
		}
		<-done
		logger.Info("api_stopped")
		return nil
	}
}
