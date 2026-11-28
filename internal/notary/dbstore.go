package notary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

// recordRow is one stored review. The body is the record as JSON and is the
// source of truth; the other columns are copies of what a listing and its filters
// read, so a page of summaries never loads an answer, which is the size of the
// review.
type recordRow struct {
	Hash     string    `gorm:"primaryKey;size:64"`
	Model    string    `gorm:"size:400;index:idx_notary_model"`
	Tool     string    `gorm:"size:400;index:idx_notary_tool"`
	Provider string    `gorm:"size:400"`
	Source   string    `gorm:"size:400"`
	Effort   string    `gorm:"size:100"`
	Complete bool      `gorm:"not null"`
	Batches  int       `gorm:"not null"`
	Answers  int       `gorm:"not null"`
	Clears   int       `gorm:"not null"`
	Created  time.Time `gorm:"precision:6;not null;index:idx_notary_created"`
	// Written is when the row was last written, which is what a listing orders by.
	Written time.Time `gorm:"precision:6;not null;index:idx_notary_order,priority:1"`
	Body    []byte
}

func (recordRow) TableName() string { return "notary_records" }

// historyRow keeps what a clear or a repair took out of a record.
type historyRow struct {
	ID     uint      `gorm:"primaryKey;autoIncrement"`
	Hash   string    `gorm:"size:64;index:idx_notary_history_hash"`
	Kind   string    `gorm:"size:16"` // "cleared" or "corrupt"
	Clears int       `gorm:"not null"`
	At     time.Time `gorm:"precision:6;not null"`
	Body   []byte
}

func (historyRow) TableName() string { return "notary_history" }

// Models are the tables the notary keeps, for the migration.
func Models() []any { return []any{&recordRow{}, &historyRow{}} }

// epoch stands for "no time". A stub left by a clear has no creation time, and a
// zero time is not a valid value in a MySQL datetime.
var epoch = time.Unix(0, 0).UTC()

// DBStore is a wire.Store and wire.Lister over a SQL database, safe for any
// number of servers writing at once.
type DBStore struct{ db *database.DB }

// NewDBStore returns a store over db. The tables must have been migrated with Models.
func NewDBStore(db *database.DB) *DBStore { return &DBStore{db: db} }

var (
	_ wire.Store  = (*DBStore)(nil)
	_ wire.Lister = (*DBStore)(nil)
)

var errCorrupt = errors.New("corrupt record")

// clip makes the copy of a field a listing keeps: no longer than its column and
// free of NUL, which a Postgres text column refuses. The record's body keeps the
// field as written.
func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func rowFor(rec wire.Record, body []byte, now time.Time) recordRow {
	created := rec.Created.UTC()
	if created.IsZero() {
		created = epoch
	}
	return recordRow{
		Hash: rec.Hash, Model: clip(rec.Model, 400), Tool: clip(rec.Tool, 400), Provider: clip(rec.Provider, 400),
		Source: clip(rec.Source, 400), Effort: clip(rec.Effort, 100), Complete: rec.Complete, Batches: rec.Batches,
		Answers: len(rec.Answers), Clears: rec.Clears, Created: created, Written: now, Body: body,
	}
}

func decode(row recordRow) (wire.Record, error) {
	var rec wire.Record
	if err := json.Unmarshal(row.Body, &rec); err != nil || rec.Hash != row.Hash {
		return wire.Record{}, fmt.Errorf("decode record %s: %w", row.Hash, errCorrupt)
	}
	return rec, nil
}

// lock reads a record's row and holds it until the transaction ends, so two
// servers working on one hash take turns.
func lock(tx *gorm.DB, hash string) (recordRow, bool, error) {
	var row recordRow
	res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("hash = ?", hash).Limit(1).Find(&row)
	return row, res.RowsAffected > 0, res.Error
}

// Get implements wire.Store.
func (s *DBStore) Get(ctx context.Context, hash string) (wire.Record, bool, error) {
	if !wire.ValidHash(hash) {
		return wire.Record{}, false, fmt.Errorf("%q is not a record hash", hash)
	}
	var row recordRow
	res := s.db.WithContext(ctx).Where("hash = ?", hash).Limit(1).Find(&row)
	if res.Error != nil {
		return wire.Record{}, false, fmt.Errorf("read record: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return wire.Record{}, false, nil
	}
	rec, err := decode(row)
	if err != nil {
		return wire.Record{}, false, err
	}
	return rec, true, nil
}

// Put implements wire.Store. A complete record is never replaced by an incomplete
// one, and a record keeps the clears it has had, whichever server wrote it.
func (s *DBStore) Put(ctx context.Context, rec wire.Record) error {
	if !wire.ValidHash(rec.Hash) {
		return fmt.Errorf("%q is not a record hash", rec.Hash)
	}
	return s.db.Transact(ctx, func(tx *gorm.DB) error {
		row, found, err := lock(tx, rec.Hash)
		if err != nil {
			return fmt.Errorf("read record: %w", err)
		}
		out := rec
		if found {
			old, derr := decode(row)
			switch {
			case errors.Is(derr, errCorrupt):
				// Reading it stays an error. Writing over it must not be, or the hash
				// would be stuck until someone repaired the table by hand.
				if err := tx.Create(&historyRow{Hash: rec.Hash, Kind: "corrupt", At: time.Now().UTC(), Body: row.Body}).Error; err != nil {
					return fmt.Errorf("keep the corrupt record: %w", err)
				}
			case derr != nil:
				return derr
			default:
				if old.Complete && !rec.Complete {
					return nil
				}
				if rec.Clears < old.Clears {
					out.Clears, out.ClearedBy = old.Clears, old.ClearedBy
				}
			}
		}
		body, err := json.Marshal(out)
		if err != nil {
			return fmt.Errorf("encode record: %w", err)
		}
		next := rowFor(out, body, time.Now().UTC())
		if !found {
			if err := tx.Create(&next).Error; err != nil {
				if database.IsDuplicate(err) {
					// Another server inserted this hash between the read and now.
					return database.ErrRetry
				}
				return fmt.Errorf("write record: %w", err)
			}
			return nil
		}
		if err := tx.Save(&next).Error; err != nil {
			return fmt.Errorf("write record: %w", err)
		}
		return nil
	})
}

// Clear implements wire.Store. The record goes to history, and a stub keeps the
// count and the names for the next record under the hash.
func (s *DBStore) Clear(ctx context.Context, hash, by string) error {
	if !wire.ValidHash(hash) {
		return fmt.Errorf("%q is not a record hash", hash)
	}
	return s.db.Transact(ctx, func(tx *gorm.DB) error {
		row, found, err := lock(tx, hash)
		if err != nil {
			return fmt.Errorf("read record: %w", err)
		}
		if !found {
			return nil
		}
		rec, derr := decode(row)
		if errors.Is(derr, errCorrupt) {
			if err := tx.Create(&historyRow{Hash: hash, Kind: "corrupt", At: time.Now().UTC(), Body: row.Body}).Error; err != nil {
				return fmt.Errorf("keep the corrupt record: %w", err)
			}
			return tx.Delete(&recordRow{}, "hash = ?", hash).Error
		}
		if derr != nil {
			return derr
		}
		rec.Clears++
		rec.ClearedBy = append(rec.ClearedBy, by)
		full, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("encode record: %w", err)
		}
		now := time.Now().UTC()
		if err := tx.Create(&historyRow{Hash: hash, Kind: "cleared", Clears: rec.Clears, At: now, Body: full}).Error; err != nil {
			return fmt.Errorf("write history: %w", err)
		}
		stub := wire.Record{Schema: rec.Schema, Hash: hash, Clears: rec.Clears, ClearedBy: rec.ClearedBy}
		sb, err := json.Marshal(stub)
		if err != nil {
			return fmt.Errorf("encode stub: %w", err)
		}
		next := rowFor(stub, sb, now)
		if err := tx.Save(&next).Error; err != nil {
			return fmt.Errorf("write stub: %w", err)
		}
		return nil
	})
}

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// List implements wire.Lister, newest written first. It reads columns only, never
// a body.
func (s *DBStore) List(ctx context.Context, q wire.ListQuery) ([]wire.Summary, string, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	limit = min(limit, maxListLimit)

	afterAt, afterHash, err := parseCursor(q.Cursor)
	if err != nil {
		return nil, "", err
	}
	tx := s.db.WithContext(ctx).Model(&recordRow{}).
		Select("hash", "model", "tool", "provider", "source", "effort", "complete", "batches", "answers", "clears", "created", "written")
	if q.Model != "" {
		tx = tx.Where("model = ?", q.Model)
	}
	if q.Tool != "" {
		tx = tx.Where("tool = ?", q.Tool)
	}
	if !q.Since.IsZero() {
		tx = tx.Where("created >= ?", q.Since.UTC())
	}
	if !q.Until.IsZero() {
		tx = tx.Where("created < ?", q.Until.UTC())
	}
	if q.Cursor != "" {
		at := time.UnixMicro(afterAt).UTC()
		tx = tx.Where("written < ? OR (written = ? AND hash < ?)", at, at, afterHash)
	}
	var rows []recordRow
	// One more than asked for says whether there is a next page.
	if err := tx.Order("written DESC, hash DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", fmt.Errorf("list records: %w", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = formatCursor(last.Written.UnixMicro(), last.Hash)
	}
	page := make([]wire.Summary, 0, len(rows))
	for _, r := range rows {
		created := r.Created.UTC()
		if created.Equal(epoch) {
			created = time.Time{}
		}
		page = append(page, wire.Summary{
			Hash: r.Hash, Created: created, Tool: r.Tool, Provider: r.Provider, Model: r.Model, Source: r.Source,
			Effort: r.Effort, Complete: r.Complete, Batches: r.Batches, Answers: r.Answers, Clears: r.Clears,
		})
	}
	return page, next, nil
}

func formatCursor(at int64, hash string) string { return strconv.FormatInt(at, 10) + "-" + hash }

func parseCursor(c string) (int64, string, error) {
	if c == "" {
		return 0, "", nil
	}
	at, hash, ok := strings.Cut(c, "-")
	n, err := strconv.ParseInt(at, 10, 64)
	if !ok || err != nil || n < 0 || !wire.ValidHash(hash) {
		return 0, "", fmt.Errorf("%w: the cursor is not one this listing returned", wire.ErrBadQuery)
	}
	return n, hash, nil
}
