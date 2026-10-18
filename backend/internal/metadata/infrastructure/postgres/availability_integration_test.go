//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/nightnoryu/go-kita/jsonlog"
	"github.com/nightnoryu/tesseract/internal/generated/api"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
	platform "github.com/nightnoryu/tesseract/internal/platform/postgres"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	"github.com/nightnoryu/tesseract/internal/source/infrastructure/mssql"
	"github.com/nightnoryu/tesseract/internal/transport/httpapi"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	"github.com/nightnoryu/tesseract/internal/source/domain"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestAvailabilitySync(t *testing.T) {
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

	pg, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image: "postgres:18.3-alpine", Env: map[string]string{"POSTGRES_USER": "test", "POSTGRES_PASSWORD": "test", "POSTGRES_DB": "test"}, ExposedPorts: []string{"5432/tcp"}, WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
	}, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Terminate(context.Background())
	pgHost, err := pg.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pgPort, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSSLMODE", "disable")
	logger, err := jsonlog.NewLogger(&jsonlog.Config{AppName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Sync()
	appDB, err := platform.Open(ctx, fmt.Sprintf("postgres://test:test@%s:%s/test", pgHost, pgPort.Port()), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer appDB.Close()
	store := Store{DB: appDB.TransactionalClient()}
	availability := metadata.Availability{Catalog: cat, Store: store, MaxAge: 15 * time.Minute}
	worker := metadata.Worker{Catalog: cat, Store: store, Timeout: 10 * time.Second, Open: func(ctx context.Context, c semantic.Catalog, id string) (domain.Connector, error) {
		return mssql.Open(ctx, c, id, dsn("probe", "warehouse"), mssql.DefaultConfig())
	}}
	token := strings.Repeat("a", 32)
	server := httpapi.New(appDB.Ping, logger, telemetry.New(), httpapi.CatalogConfig{Availability: availability, OperatorToken: token})
	read := func(reason string) api.SourceAvailability {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/catalog", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("API: %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "Unlisted") || strings.Contains(response.Body.String(), "SecretColumn") {
			t.Fatal("unlisted metadata exposed")
		}
		var body api.CatalogAvailability
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Sources) != 1 || len(body.Sources[0].Tables) != 1 {
			t.Fatalf("unexpected sources: %+v", body)
		}
		s := body.Sources[0]
		if s.Configured != 1 || s.Available+s.Unavailable != 1 || string(s.Tables[0].Reason.Value) != reason || (s.Available == 1) != (reason == "") {
			t.Fatalf("availability: %+v", s)
		}
		return s
	}
	scan := func() {
		t.Helper()
		if err := worker.Scan(ctx, "crm_dwh"); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(statement string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	read("not_scanned")
	scan()
	healthy := read("")
	if !healthy.LastSuccessfulScan.Set {
		t.Fatal("missing successful timestamp")
	}
	testAnalyticalQueries(t, ctx, db, cat, store, worker)
	// Even poisoned storage cannot promote an unlisted physical table into the API.
	hash, _ := cat.Hash()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO tesseract_app.schema_tables SELECT scan_id,'unlisted','mart','Unlisted',true,'',now() FROM tesseract_app.schema_current WHERE source_id=$1 AND catalog_hash=$2`, "crm_dwh", hash); err != nil {
		t.Fatal(err)
	}
	read("")
	exec("GRANT VIEW DEFINITION ON OBJECT::mart.MarketingLeads TO probe")
	exec("DENY SELECT ON OBJECT::mart.MarketingLeads TO probe")
	scan()
	read("access_denied")
	exec("REVOKE SELECT ON OBJECT::mart.MarketingLeads FROM probe")
	exec("GRANT SELECT ON OBJECT::mart.MarketingLeads TO probe")
	exec("DENY SELECT ON OBJECT::mart.MarketingLeads (ChannelName) TO probe")
	scan()
	read("access_denied")
	exec("GRANT SELECT ON OBJECT::mart.MarketingLeads (ChannelName) TO probe")
	scan()
	healthy = read("")
	exec("ALTER TABLE mart.MarketingLeads ALTER COLUMN ChannelName int")
	scan()
	drift := read("incompatible_type")
	if !drift.LastSuccessfulScan.Value.Equal(healthy.LastSuccessfulScan.Value) {
		t.Fatal("failed scan replaced last success")
	}
	exec("ALTER TABLE mart.MarketingLeads DROP COLUMN ChannelName")
	scan()
	read("missing_column")
	exec("DROP TABLE mart.MarketingLeads")
	scan()
	read("missing_table")
	changed := cat
	changed.Datasets = append([]semantic.Dataset(nil), cat.Datasets...)
	changed.Datasets[0].Description = "new version"
	_, sources, err := (metadata.Availability{Catalog: changed, Store: store, MaxAge: time.Minute}).Read(ctx, time.Now())
	if err != nil || sources[0].Tables[0].Reason != "not_scanned" {
		t.Fatalf("hash invalidation: %+v %v", sources, err)
	}
	if err := container.Stop(ctx, nil); err != nil {
		t.Fatal(err)
	}
	scan()
	read("scan_failed")
	var count int
	if err := store.DB.GetContext(ctx, &count, `SELECT count(*) FROM information_schema.tables WHERE table_schema='tesseract_app' AND table_name IN ('MarketingLeads','Unlisted')`); err != nil || count != 0 {
		t.Fatalf("business tables copied: %d %v", count, err)
	}
}
