package mssql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	driver "github.com/microsoft/go-mssqldb"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/domain"
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

// New configures a pool without contacting the warehouse. Each operation is
// bounded and checks read-only privileges before accessing cataloged tables.
func New(catalog semantic.Catalog, sourceID, dsn string, cfg Config) (*Connector, error) {
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
	return c, nil
}

func Open(ctx context.Context, catalog semantic.Catalog, sourceID, dsn string, cfg Config) (*Connector, error) {
	c, err := New(catalog, sourceID, dsn, cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := c.db.PingContext(ctx); err != nil {
		c.db.Close()
		return nil, safeError(ctx, err)
	}
	if err := c.checkReadOnly(ctx); err != nil {
		c.db.Close()
		return nil, err
	}
	return c, nil
}
func (c *Connector) Close() error { return c.db.Close() }
func safeError(ctx context.Context, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var sqlError driver.Error
	if errors.As(cause, &sqlError) {
		switch sqlError.Number {
		case 229, 230, 262, 297, 916, 18456:
			return domain.ErrAccessDenied
		}
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
		return safeError(ctx, err)
	}
	if unsafe != 0 {
		return domain.ErrReadOnly
	}
	for _, d := range c.datasets {
		err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.fn_my_permissions(@object, 'OBJECT')
 WHERE permission_name NOT IN ('SELECT', 'VIEW DEFINITION')`, sql.Named("object", quote(d.Schema)+"."+quote(d.Table))).Scan(&unsafe)
		if err != nil {
			return safeError(ctx, err)
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
	if err := c.checkReadOnly(ctx); err != nil {
		return nil, err
	}

	result := make([]domain.Table, 0, len(ids))
	for _, id := range ids {
		d := c.datasets[id]
		t := domain.Table{Dataset: id, Schema: d.Schema, Name: d.Table, Columns: []domain.Column{}}
		var present int
		err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.tables t JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name=@schema AND t.name=@table`, sql.Named("schema", d.Schema), sql.Named("table", d.Table)).Scan(&present)
		if err != nil {
			return nil, safeError(ctx, err)
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
					return nil, safeError(ctx, err)
				}
				t.Columns = append(t.Columns, actual)
			}
			// Metadata visibility does not imply SELECT permission. TOP (0)
			// validates table and column permissions without reading business rows.
			if len(t.Columns) == len(d.Columns) {
				columns := make([]string, 0, len(d.Columns))
				for _, col := range d.Columns {
					columns = append(columns, quote(col.SourceColumn))
				}
				rows, err := c.db.QueryContext(ctx, "SELECT TOP (0) "+strings.Join(columns, ", ")+" FROM "+quote(d.Schema)+"."+quote(d.Table))
				if err != nil {
					return nil, safeError(ctx, err)
				}
				if err := rows.Close(); err != nil {
					return nil, safeError(ctx, err)
				}
			}
		}
		result = append(result, t)
	}
	return result, nil
}

func (c *Connector) ExecuteQuery(ctx context.Context, q domain.ValidatedQuery) (domain.Result, error) {
	statement, args, names, err := c.compile(q)
	if err != nil {
		return domain.Result{}, err
	}
	if err := q.CheckAccess(ctx); err != nil {
		return domain.Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.QueryTimeout)
	defer cancel()
	// A fresh inspection catches drift since ingestion, including type changes
	// that SQL Server could otherwise silently convert.
	tables, err := c.InspectCatalogTables(ctx, []string{q.Dataset()})
	if err != nil {
		return domain.Result{}, err
	}
	if len(tables) != 1 || metadata.Reconcile(c.datasets[q.Dataset()], tables[0], "").Reason != "" {
		return domain.Result{}, domain.ErrSchemaUnavailable
	}

	if err := c.acquire(ctx); err != nil {
		return domain.Result{}, err
	}
	defer func() { <-c.slots }()
	if err := q.CheckAccess(ctx); err != nil {
		return domain.Result{}, err
	}
	rows, err := c.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.Result{}, safeError(ctx, err)
	}
	defer rows.Close()
	result := domain.Result{Columns: names, Rows: [][]any{}}
	for rows.Next() {
		if len(result.Rows) >= c.cfg.MaxRows {
			return domain.Result{}, domain.ErrLimit
		}
		values := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return domain.Result{}, safeError(ctx, err)
		}
		for i, value := range values {
			switch v := value.(type) {
			case []byte:
				values[i] = string(v)
			case int64:
				values[i] = strconv.FormatInt(v, 10)
			}
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return domain.Result{}, safeError(ctx, err)
	}
	return result, nil
}
