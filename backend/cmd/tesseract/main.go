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
	"github.com/nightnoryu/tesseract/internal/platform/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
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
	handler := httpapi.New(db.Ping, logger, telemetry.New())
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
