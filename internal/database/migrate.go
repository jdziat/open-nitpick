package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// migrateLockKey is the advisory lock the migration takes. Postgres wants a
// number and MySQL a name, so each engine reads the one it needs.
const (
	migrateLockID   int64 = 7106239874502
	migrateLockName       = "nitpick-migrate"
	migrateLockWait       = 2 * time.Minute
)

// Migrate brings the schema of the given models up to date, once however many
// replicas start together. The others wait on a lock and then find nothing to do.
// It only adds: a column or table is never dropped, so a replica still running the
// previous release keeps working while the new one starts.
func (d *DB) Migrate(ctx context.Context, models ...any) error {
	sqlDB, err := d.SQL()
	if err != nil {
		return err
	}
	// The lock belongs to one connection, which stays out of the pool until the
	// migration is done.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("take a connection for the migration: %w", err)
	}
	defer func() { _ = conn.Close() }()

	switch d.Dialect {
	case Postgres:
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrateLockID); err != nil {
			return fmt.Errorf("wait for the migration lock: %w", err)
		}
		defer func() {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrateLockID)
		}()
	case MySQL:
		var got int
		row := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", migrateLockName, int(migrateLockWait/time.Second))
		if err := row.Scan(&got); err != nil {
			return fmt.Errorf("wait for the migration lock: %w", err)
		}
		if got != 1 {
			return errors.New("another server held the migration lock for longer than two minutes")
		}
		defer func() {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK(?)", migrateLockName)
		}()
	default:
		return fmt.Errorf("no migration lock for %q", d.Dialect)
	}

	if err := d.WithContext(ctx).AutoMigrate(models...); err != nil {
		return fmt.Errorf("migrate the schema: %w", err)
	}
	return nil
}

// deadlock reports an error a transaction may be run again after: a deadlock or a
// serialization failure, which both engines raise when two writers meet.
func deadlock(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "40001" || pg.Code == "40P01"
	}
	var my *mysqldriver.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1213 || my.Number == 1205
	}
	return false
}

// IsDuplicate reports a unique-key violation, which is how two writers racing to
// insert the same row show up.
func IsDuplicate(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "23505"
	}
	var my *mysqldriver.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1062
	}
	return false
}
