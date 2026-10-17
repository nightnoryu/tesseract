package mssql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	driver "github.com/microsoft/go-mssqldb"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	"github.com/nightnoryu/tesseract/internal/source/domain"
)

type Config struct {
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
	MaxOpen        int
	MaxIdle        int
	Concurrency    int
	MaxRows        int
}

func DefaultConfig() Config { return Config{10 * time.Second, 30 * time.Second, 4, 2, 4, 1000} }
func (c Config) Validate() error {
	if c.ConnectTimeout <= 0 || c.ConnectTimeout > time.Minute || c.QueryTimeout <= 0 || c.QueryTimeout > 5*time.Minute || c.MaxOpen < 1 || c.MaxOpen > 64 || c.MaxIdle < 0 || c.MaxIdle > c.MaxOpen || c.Concurrency < 1 || c.Concurrency > c.MaxOpen || c.MaxRows < 1 || c.MaxRows > 10000 {
		return errors.New("invalid_dwh_limits")
	}
	return nil
}

type Connector struct {
	db       *sql.DB
	cfg      Config
	datasets map[string]semantic.Dataset
	hash     string
	slots    chan struct{}
}

var _ domain.Connector = (*Connector)(nil)

func Open(ctx context.Context, catalog semantic.Catalog, sourceID, dsn string, cfg Config) (*Connector, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	hash, err := catalog.Hash()
	if err != nil {
		return nil, err
	}
	found := false
	for _, s := range catalog.Sources {
		if s.ID == sourceID {
			found = s.Kind == "mssql"
		}
	}
	if !found {
		return nil, errors.New("unsupported_or_unknown_dwh_source")
	}
	if dsn == "" {
		return nil, errors.New("missing_dwh_secret")
	}
	// Own the catalog snapshot; later caller mutations must not widen the allowlist.
	raw, _ := json.Marshal(catalog)
	var snapshot semantic.Catalog
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, domain.ErrUnavailable
	}
	dc, err := driver.NewConnector(dsn)
	if err != nil {
		return nil, errors.New("invalid_dwh_dsn")
	}
	db := sql.OpenDB(dc)
	db.SetMaxOpenConns(cfg.MaxOpen)
	db.SetMaxIdleConns(cfg.MaxIdle)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	c := &Connector{db: db, cfg: cfg, hash: hash, datasets: map[string]semantic.Dataset{}, slots: make(chan struct{}, cfg.Concurrency)}
	for _, d := range snapshot.Datasets {
		if d.Source == sourceID {
			c.datasets[d.ID] = d
		}
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, safeError(ctx)
	}
	if err := c.checkReadOnly(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}
func (c *Connector) Close() error { return c.db.Close() }
func safeError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return domain.ErrUnavailable
}
func (c *Connector) acquire(ctx context.Context) error {
	select {
	case c.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Require a deliberately restricted principal. ApplicationIntent=ReadOnly is
// routing advice, not a SQL Server authorization boundary.
func (c *Connector) checkReadOnly(ctx context.Context) error {
	var unsafe int
	err := c.db.QueryRowContext(ctx, `SELECT CASE WHEN IS_SRVROLEMEMBER('sysadmin') = 0
 AND IS_MEMBER('db_owner') = 0
 AND IS_MEMBER('db_denydatawriter') = 1
 AND NOT EXISTS (SELECT 1 FROM sys.fn_my_permissions(NULL, 'DATABASE')
 WHERE permission_name NOT IN ('CONNECT', 'SELECT', 'VIEW DEFINITION', 'SHOWPLAN',
 'VIEW ANY COLUMN ENCRYPTION KEY DEFINITION', 'VIEW ANY COLUMN MASTER KEY DEFINITION'))
 THEN 0 ELSE 1 END`).Scan(&unsafe)
	if err != nil {
		return safeError(ctx)
	}
	if unsafe != 0 {
		return domain.ErrReadOnly
	}
	for _, d := range c.datasets {
		err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.fn_my_permissions(@object, 'OBJECT')
 WHERE permission_name NOT IN ('SELECT', 'VIEW DEFINITION')`, sql.Named("object", quote(d.Schema)+"."+quote(d.Table))).Scan(&unsafe)
		if err != nil {
			return safeError(ctx)
		}
		if unsafe != 0 {
			return domain.ErrReadOnly
		}
	}
	return nil
}
func quote(s string) string { return "[" + s + "]" }

func (c *Connector) InspectCatalogTables(ctx context.Context, ids []string) ([]domain.Table, error) {
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := c.datasets[id]; !ok {
			return nil, domain.ErrNotCataloged
		}
		if seen[id] {
			return nil, errors.New("duplicate_dwh_dataset")
		}
		seen[id] = true
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.QueryTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.slots }()
	result := make([]domain.Table, 0, len(ids))
	for _, id := range ids {
		d := c.datasets[id]
		t := domain.Table{Dataset: id, Schema: d.Schema, Name: d.Table, Columns: []domain.Column{}}
		var present int
		err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.tables t JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name=@schema AND t.name=@table`, sql.Named("schema", d.Schema), sql.Named("table", d.Table)).Scan(&present)
		if err != nil {
			return nil, safeError(ctx)
		}
		t.Present = present == 1
		if t.Present {
			for _, col := range d.Columns {
				var actual domain.Column
				err := c.db.QueryRowContext(ctx, `SELECT c.name, TYPE_NAME(c.system_type_id), c.is_nullable, c.max_length, c.precision, c.scale
FROM sys.columns c JOIN sys.tables t ON t.object_id=c.object_id JOIN sys.schemas s ON s.schema_id=t.schema_id
WHERE s.name=@schema AND t.name=@table AND c.name=@column`, sql.Named("schema", d.Schema), sql.Named("table", d.Table), sql.Named("column", col.SourceColumn)).Scan(&actual.Name, &actual.Type, &actual.Nullable, &actual.MaxLength, &actual.Precision, &actual.Scale)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return nil, safeError(ctx)
				}
				t.Columns = append(t.Columns, actual)
			}
		}
		result = append(result, t)
	}
	return result, nil
}

func (c *Connector) ExecuteQuery(ctx context.Context, q domain.ValidatedQuery) (domain.Result, error) {
	// No caller can currently mint a validated query. Keep the execution boundary
	// ready without bypassing milestone 8's compatibility and authorization gate.
	d, ok := c.datasets[q.Dataset()]
	if !ok || q.CatalogHash() != c.hash {
		return domain.Result{}, domain.ErrInvalidQuery
	}
	var expression string
	for _, m := range d.Measures {
		if m.ID == q.Measure() {
			var column string
			for _, col := range d.Columns {
				if col.ID == m.Column {
					column = quote(col.SourceColumn)
				}
			}
			switch m.Operation {
			case "count_distinct":
				expression = "COUNT_BIG(DISTINCT " + column + ")"
			case "count":
				expression = "COUNT_BIG(" + column + ")"
			case "sum", "avg", "min", "max":
				expression = m.Operation + "(" + column + ")"
			}
		}
	}
	if expression == "" {
		return domain.Result{}, domain.ErrInvalidQuery
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.QueryTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return domain.Result{}, err
	}
	defer func() { <-c.slots }()
	rows, err := c.db.QueryContext(ctx, fmt.Sprintf("SELECT TOP (@limit) %s AS [value] FROM %s.%s", expression, quote(d.Schema), quote(d.Table)), sql.Named("limit", c.cfg.MaxRows+1))
	if err != nil {
		return domain.Result{}, safeError(ctx)
	}
	defer rows.Close()
	result := domain.Result{Columns: []string{"value"}, Rows: [][]any{}}
	for rows.Next() {
		if len(result.Rows) >= c.cfg.MaxRows {
			return domain.Result{}, domain.ErrLimit
		}
		var value any
		if err := rows.Scan(&value); err != nil {
			return domain.Result{}, safeError(ctx)
		}
		if b, ok := value.([]byte); ok {
			value = string(b)
		}
		result.Rows = append(result.Rows, []any{value})
	}
	if rows.Err() != nil {
		return domain.Result{}, safeError(ctx)
	}
	return result, nil
}
