// Package database opens the SQL database the server keeps its state in.
//
// Postgres and MySQL are both supported, through gorm, and every query a store
// makes has to be correct on both: the stores are tested against the two real
// engines (databasetest), not against a stand-in.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Dialect names the engine behind a DB.
type Dialect string

// The supported engines.
const (
	Postgres Dialect = "postgres"
	MySQL    Dialect = "mysql"
)

// DB is an open database and the engine it is.
type DB struct {
	*gorm.DB
	Dialect Dialect
}

// ErrNoURL is returned when the server was given no database to use.
var ErrNoURL = errors.New("no database URL: set -database-url or NITPICK_DATABASE_URL to a postgres:// or mysql:// URL")

// Pool sizes. Several replicas each hold a pool, so the total against one
// database is replicas times MaxOpen: keep it under the server's connection limit.
const (
	maxOpen     = 20
	maxIdle     = 5
	maxLifetime = 30 * time.Minute
)

// Open connects to the database named by a URL, postgres:// or mysql://, and
// checks it answers. The URL carries the credentials, so an error never repeats
// it.
func Open(ctx context.Context, rawURL string) (*DB, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, ErrNoURL
	}
	dialect, dialector, err := dialectorFor(rawURL)
	if err != nil {
		return nil, err
	}
	g, err := gorm.Open(dialector, &gorm.Config{
		Logger:  logger.Default.LogMode(logger.Silent),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open the %s database: %w", dialect, redact(err, rawURL))
	}
	sqlDB, err := g.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(maxLifetime)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("reach the %s database: %w", dialect, redact(err, rawURL))
	}
	return &DB{DB: g, Dialect: dialect}, nil
}

// Close closes the connection pool.
func (d *DB) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// SQL is the underlying handle, for the few things gorm does not cover.
func (d *DB) SQL() (*sql.DB, error) { return d.DB.DB() }

func dialectorFor(rawURL string) (Dialect, gorm.Dialector, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, errors.New("the database URL is not a URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "postgres", "postgresql":
		// pgx reads a postgres:// URL directly, including sslmode and the rest.
		return Postgres, postgres.Open(rawURL), nil
	case "mysql":
		dsn, err := mysqlDSN(u)
		if err != nil {
			return "", nil, err
		}
		return MySQL, mysql.Open(dsn), nil
	}
	return "", nil, fmt.Errorf("the database URL scheme %q is not postgres or mysql", u.Scheme)
}

// mysqlDSN turns mysql://user:pass@host:port/db?tls=true into the driver's own
// form. utf8mb4 and UTC are fixed: a record's text is arbitrary and its times are
// compared across replicas, so neither may depend on the server's defaults.
func mysqlDSN(u *url.URL) (string, error) {
	cfg := mysqldriver.NewConfig()
	cfg.Net = "tcp"
	host := u.Host
	if host == "" {
		return "", errors.New("the mysql URL has no host")
	}
	if u.Port() == "" {
		host += ":3306"
	}
	cfg.Addr = host
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	if cfg.DBName == "" {
		return "", errors.New("the mysql URL names no database")
	}
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Collation = "utf8mb4_bin"
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	for key, vals := range u.Query() {
		if len(vals) == 0 {
			continue
		}
		if key == "tls" {
			cfg.TLSConfig = vals[0]
			continue
		}
		cfg.Params[key] = vals[0]
	}
	return cfg.FormatDSN(), nil
}

// redact removes the URL's secrets from an error that quotes it.
func redact(err error, rawURL string) error {
	msg := err.Error()
	if u, perr := url.Parse(rawURL); perr == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "REDACTED")
		}
	}
	return errors.New(msg)
}
