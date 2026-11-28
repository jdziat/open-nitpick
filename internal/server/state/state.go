// Package state is the small shared memory the server's replicas need: sessions,
// the CSRF key, sign-in flows in progress, the SAML replay guard and the sign-in
// rate limits. It is one table of keyed values with an expiry, since all of them
// need the same few operations: read, write, take once, and change under a lock so
// two replicas cannot both win.
package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/nitpick-oss/nitpick/internal/database"
)

type row struct {
	Key     string `gorm:"column:state_key;primaryKey;size:200"`
	Value   []byte
	Expires time.Time `gorm:"column:expires_at;precision:6;not null;index:idx_server_state_expires"`
}

func (row) TableName() string { return "server_state" }

// Models are the tables the server keeps its shared state in, for the migration.
func Models() []any { return []any{&row{}} }

// Entry is one value and when it stops existing. An entry past its expiry is
// treated as absent by every operation, whether or not it has been swept yet.
type Entry struct {
	Value   []byte
	Expires time.Time
}

// Store is the shared memory over a database.
type Store struct {
	db  *database.DB
	now func() time.Time
}

// New returns a store over db. The table must have been migrated with Models.
func New(db *database.DB) *Store {
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock returns a store that reads the time from now, for a test of expiry.
func (s *Store) WithClock(now func() time.Time) *Store { return &Store{db: s.db, now: now} }

// Now is the store's clock.
func (s *Store) Now() time.Time { return s.now().UTC() }

// Key builds a key from a kind and any text. The text is hashed: it may be an
// account name or an address, which are of any length and are not for a database
// administrator to read off a key.
func Key(kind, text string) string {
	sum := sha256.Sum256([]byte(text))
	return kind + ":" + hex.EncodeToString(sum[:])
}

func lock(tx *gorm.DB, key string) (row, bool, error) {
	var r row
	res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("state_key = ?", key).Limit(1).Find(&r)
	return r, res.RowsAffected > 0, res.Error
}

func (s *Store) live(r row, found bool) bool { return found && r.Expires.After(s.now()) }

// Get returns an entry without locking it.
func (s *Store) Get(ctx context.Context, key string) (Entry, bool, error) {
	var r row
	res := s.db.WithContext(ctx).Where("state_key = ?", key).Limit(1).Find(&r)
	if res.Error != nil {
		return Entry{}, false, fmt.Errorf("read shared state: %w", res.Error)
	}
	if !s.live(r, res.RowsAffected > 0) {
		return Entry{}, false, nil
	}
	return Entry{Value: r.Value, Expires: r.Expires}, true, nil
}

// Put writes an entry, replacing any under the key.
func (s *Store) Put(ctx context.Context, key string, e Entry) error {
	r := row{Key: key, Value: e.Value, Expires: e.Expires.UTC()}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&r).Error
	if err != nil {
		return fmt.Errorf("write shared state: %w", err)
	}
	return nil
}

// Delete removes an entry, if there is one.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.db.WithContext(ctx).Where("state_key = ?", key).Delete(&row{}).Error; err != nil {
		return fmt.Errorf("delete shared state: %w", err)
	}
	return nil
}

// Take reads an entry and removes it in one step, so of any number of servers
// asking at once for one key, one gets it. That is what makes a sign-in answer
// good for one use.
func (s *Store) Take(ctx context.Context, key string) (Entry, bool, error) {
	var out Entry
	var ok bool
	err := s.db.Transact(ctx, func(tx *gorm.DB) error {
		out, ok = Entry{}, false
		r, found, err := lock(tx, key)
		if err != nil {
			return fmt.Errorf("read shared state: %w", err)
		}
		if !found {
			return nil
		}
		if err := tx.Where("state_key = ?", key).Delete(&row{}).Error; err != nil {
			return fmt.Errorf("delete shared state: %w", err)
		}
		if s.live(r, true) {
			out, ok = Entry{Value: r.Value, Expires: r.Expires}, true
		}
		return nil
	})
	return out, ok, err
}

// Update changes an entry under a lock on its key: fn is given the current entry,
// or nil when there is none, and returns the entry to keep, or nil for none. Two
// servers updating one key take turns, and each sees what the other left. fn may
// run more than once, so it must have no effect outside what it returns.
func (s *Store) Update(ctx context.Context, key string, fn func(cur *Entry) (*Entry, error)) error {
	return s.db.Transact(ctx, func(tx *gorm.DB) error {
		r, found, err := lock(tx, key)
		if err != nil {
			return fmt.Errorf("read shared state: %w", err)
		}
		var cur *Entry
		if s.live(r, found) {
			cur = &Entry{Value: r.Value, Expires: r.Expires}
		}
		next, err := fn(cur)
		if err != nil {
			return err
		}
		switch {
		case next == nil && found:
			return tx.Where("state_key = ?", key).Delete(&row{}).Error
		case next == nil:
			return nil
		case cur != nil && bytes.Equal(cur.Value, next.Value) && cur.Expires.Equal(next.Expires):
			return nil // unchanged, so no write
		}
		w := row{Key: key, Value: next.Value, Expires: next.Expires.UTC()}
		if found {
			return tx.Save(&w).Error
		}
		if err := tx.Create(&w).Error; err != nil {
			if database.IsDuplicate(err) {
				// Another server created it between the read and now.
				return database.ErrRetry
			}
			return fmt.Errorf("write shared state: %w", err)
		}
		return nil
	})
}

// Count is how many live entries have a key beginning with prefix.
func (s *Store) Count(ctx context.Context, prefix string) (int, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&row{}).Where("state_key LIKE ? AND expires_at > ?", prefix+"%", s.now()).Count(&n).Error
	return int(n), err
}

// DeleteOldest removes the live entry with the earliest expiry among those whose
// key begins with prefix, and reports whether there was one. It makes room in a
// table that must not grow without bound.
func (s *Store) DeleteOldest(ctx context.Context, prefix string) (bool, error) {
	var key string
	err := s.db.WithContext(ctx).Model(&row{}).Where("state_key LIKE ? AND expires_at > ?", prefix+"%", s.now()).
		Order("expires_at ASC").Limit(1).Pluck("state_key", &key).Error
	if err != nil || key == "" {
		return false, err
	}
	res := s.db.WithContext(ctx).Where("state_key = ?", key).Delete(&row{})
	return res.RowsAffected > 0, res.Error
}

// Sweep deletes what has expired. Reads already ignore it, so this is only to
// give the space back.
func (s *Store) Sweep(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("expires_at <= ?", s.now()).Delete(&row{}).Error
}
