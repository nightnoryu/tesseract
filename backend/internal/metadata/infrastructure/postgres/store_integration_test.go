//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/nightnoryu/go-kita/jsonlog"
	"github.com/nightnoryu/tesseract/internal/metadata/domain"
	platform "github.com/nightnoryu/tesseract/internal/platform/postgres"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"testing"
	"time"
)

func TestAtomicPublicationAndSourceLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{Image: "postgres:18.3-alpine", Env: map[string]string{"POSTGRES_USER": "test", "POSTGRES_PASSWORD": "test", "POSTGRES_DB": "test"}, ExposedPorts: []string{"5432/tcp"}, WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2)}, Started: true})
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
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "test"})
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
	table := domain.Table{Dataset: "leads", Schema: "mart", Name: "Leads", Present: true, InspectedAt: time.Now()}
	publish := func(tables ...domain.Table) error {
		return s.Scan(ctx, "crm", "hash", func() []domain.Table { return tables })
	}
	if err := publish(table); err != nil {
		t.Fatal(err)
	}
	current := func() string {
		var id string
		if err := s.DB.GetContext(ctx, &id, "SELECT scan_id::text FROM tesseract_app.schema_current WHERE source_id='crm' AND catalog_hash='hash'"); err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := current()
	err = s.Scan(ctx, "crm", "hash", func() []domain.Table {
		if current() != old {
			t.Error("snapshot changed before publication")
		}
		if err := s.Scan(ctx, "crm", "different-hash", func() []domain.Table { t.Error("overlapping inspection"); return nil }); !errors.Is(err, ErrOverlap) {
			t.Errorf("lock: %v", err)
		}
		table.Reason = "scan_failed"
		return []domain.Table{table}
	})
	if err != nil {
		t.Fatal(err)
	}
	if current() == old {
		t.Fatal("failure did not replace green snapshot")
	}
	old = current()
	if err := publish(table, table); err == nil {
		t.Fatal("duplicate snapshot accepted")
	}
	if current() != old {
		t.Fatal("partial publication visible")
	}
	var count int
	if err := s.DB.GetContext(ctx, &count, "SELECT count(*) FROM tesseract_app.schema_scans"); err != nil || count != 2 {
		t.Fatalf("rollback: %d %v", count, err)
	}
}
