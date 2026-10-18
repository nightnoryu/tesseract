//go:build integration

package mssql

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	"github.com/nightnoryu/tesseract/internal/source/domain"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestCatalogInspection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	const password = "Test-Only-Mssql-2026!"
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image: "mcr.microsoft.com/mssql/server:2022-CU23-ubuntu-22.04", Env: map[string]string{"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": password}, ExposedPorts: []string{"1433/tcp"}, WaitingFor: wait.ForAll(wait.ForLog("SQL Server is now ready for client connections"), wait.ForExec([]string{"/bin/sh", "-c", `SQLCMDPASSWORD="$MSSQL_SA_PASSWORD" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -Q 'SELECT 1' -b -o /dev/null`})).WithDeadline(3 * time.Minute),
	}, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	defer container.Terminate(context.Background())
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dsn := func(user, db string) string {
		u := url.URL{Scheme: "sqlserver", Host: host + ":" + port.Port(), User: url.UserPassword(user, password)}
		q := u.Query()
		q.Set("database", db)
		q.Set("encrypt", "disable")
		u.RawQuery = q.Encode()
		return u.String()
	}
	admin, err := sql.Open("sqlserver", dsn("sa", "master"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE warehouse"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlserver", dsn("sa", "warehouse"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE SCHEMA mart",
		"CREATE TABLE mart.MarketingLeads (LeadId nvarchar(64), CreatedAt datetime2, ChannelName nvarchar(100), SecretColumn int)",
		"CREATE TABLE mart.Unlisted (Secret int)",
		"CREATE LOGIN probe WITH PASSWORD='" + password + "'",
		"CREATE USER probe FOR LOGIN probe",
		"ALTER ROLE db_denydatawriter ADD MEMBER probe",
		"GRANT SELECT ON OBJECT::mart.MarketingLeads TO probe",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := catalog.Load(os.DirFS("../../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := Open(ctx, cat, "crm_dwh", dsn("sa", "warehouse"), DefaultConfig()); !errors.Is(err, domain.ErrReadOnly) {
		if conn != nil {
			conn.Close()
		}
		t.Fatalf("admin accepted: %v", err)
	}
	lazyAdmin, err := New(cat, "crm_dwh", dsn("sa", "warehouse"), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer lazyAdmin.Close()
	if _, err := lazyAdmin.InspectCatalogTables(ctx, []string{"leads"}); !errors.Is(err, domain.ErrReadOnly) {
		t.Fatalf("deferred pool accepted admin: %v", err)
	}
	conn, err := Open(ctx, cat, "crm_dwh", dsn("probe", "warehouse"), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader, err := sql.Open("sqlserver", dsn("probe", "warehouse"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.ExecContext(ctx, "INSERT INTO mart.MarketingLeads (LeadId) VALUES ('forbidden')"); err == nil {
		t.Fatal("reader could write")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := conn.InspectCatalogTables(cancelled, []string{"leads"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	tables, err := conn.InspectCatalogTables(ctx, []string{"leads"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || !tables[0].Present || len(tables[0].Columns) != 3 {
		t.Fatalf("metadata: %+v", tables)
	}
	for _, col := range tables[0].Columns {
		if col.Name == "SecretColumn" {
			t.Fatal("unlisted column disclosed")
		}
	}
	if _, err := conn.InspectCatalogTables(ctx, []string{"mart.Unlisted"}); !errors.Is(err, domain.ErrNotCataloged) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE mart.MarketingLeads"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, cat, "crm_dwh", dsn("probe", "warehouse"), DefaultConfig())
	if err != nil {
		t.Fatalf("open with missing table: %v", err)
	}
	defer reopened.Close()
	tables, err = reopened.InspectCatalogTables(ctx, []string{"leads"})
	if err != nil || len(tables) != 1 || tables[0].Present {
		t.Fatalf("missing: %+v %v", tables, err)
	}
}
