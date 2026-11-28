package database

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// maxAttempts bounds how often a transaction is run again after a deadlock.
const maxAttempts = 6

// ErrRetry asks Transact to run it again. It is for
// a transaction that finds the world changed under it, such as an insert that lost
// a race to another server and now has a row to read.
var ErrRetry = errors.New("retry the transaction")

// Transact runs fn in a transaction and runs it again, in a new one, when the
// engine reports a deadlock or a serialization failure, or fn returns ErrRetry. fn must therefore be safe
// to run more than once: it reads what it needs inside the transaction and has no
// effect outside it.
func (d *DB) Transact(ctx context.Context, fn func(tx *gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = d.WithContext(ctx).Transaction(fn)
		if err == nil || (!deadlock(err) && !errors.Is(err, ErrRetry)) {
			return err
		}
		// A short, growing pause, so the writers that met do not meet again at once.
		select {
		case <-time.After(time.Duration(attempt+1) * 15 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}
