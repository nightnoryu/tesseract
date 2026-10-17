package postgres

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nightnoryu/go-kita/log"
	"github.com/nightnoryu/go-kita/postgresql"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ParseDSN rejects options go-kita cannot preserve. TLS is configured through PGSSL*.
func ParseDSN(raw string) (postgresql.DSN, error) {
	invalid := errors.New("invalid_database_configuration")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.User.Username() == "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return postgresql.DSN{}, invalid
	}
	database := strings.TrimPrefix(u.Path, "/")
	if database == "" || strings.Contains(database, "/") {
		return postgresql.DSN{}, invalid
	}
	port := 5432
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return postgresql.DSN{}, invalid
		}
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil && strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	password, _ := u.User.Password()
	return postgresql.DSN{Host: host, Port: port, Database: database, User: u.User.Username(), Password: password}, nil
}

func Open(ctx context.Context, raw string, logger log.Logger) (postgresql.Connector, error) {
	dsn, err := ParseDSN(raw)
	if err != nil {
		return nil, err
	}
	db := postgresql.NewConnector()
	if err := db.Open(ctx, dsn, postgresql.Config{
		MaxOpenConnections: 10, MaxIdleConnections: 2,
		ConnectionMaxLifetime: 30 * time.Minute, ConnectionMaxIdleTime: 5 * time.Minute,
		ConnectTimeout: 5 * time.Second, MigrationAdvisoryLockID: 0x74657373657261,
	}); err != nil {
		return nil, errors.New("database_connect_failed")
	}
	files, err := fs.Sub(migrations, "migrations")
	if err == nil {
		var migrator postgresql.Migrator
		migrator, err = db.Migrator(logger, files)
		if err == nil {
			err = migrator.MigrateUp(ctx)
		}
	}
	if err != nil {
		_ = db.Close()
		return nil, errors.New("database_migration_failed")
	}
	return db, nil
}
