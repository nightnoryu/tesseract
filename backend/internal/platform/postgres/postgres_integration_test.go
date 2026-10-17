//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nightnoryu/go-kita/jsonlog"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	"github.com/nightnoryu/tesseract/internal/transport/httpapi"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPostgresMigrationsAndOutage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image: "postgres:18.3-alpine", Env: map[string]string{"POSTGRES_USER": "tesseract", "POSTGRES_PASSWORD": "test-only", "POSTGRES_DB": "tesseract"}, ExposedPorts: []string{"5432/tcp"}, WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
	}, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSSLMODE", "disable")
	raw := fmt.Sprintf("postgres://tesseract:test-only@%s:%s/tesseract", host, port.Port())
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Sync() }()
	db, err := Open(ctx, raw, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, raw, logger)
	if err != nil {
		t.Fatalf("second startup: %v", err)
	}
	defer func() { _ = db.Close() }()
	inspect, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer inspect.Close()
	var count int
	if err := inspect.QueryRowContext(ctx, "SELECT count(*) FROM schema_migration WHERE version = '001'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration history: %d %v", count, err)
	}
	if err := inspect.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'tesseract_app'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("application schema: %d %v", count, err)
	}
	s := httpapi.New(db.Ping, logger, telemetry.New())
	check := func(path string, status int) {
		t.Helper()
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	check("/readyz", 200)
	if err := container.Stop(ctx, nil); err != nil {
		t.Fatal(err)
	}
	check("/readyz", 503)
	check("/healthz", 200)
}
