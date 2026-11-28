package notary

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gorm.io/gorm"

	"github.com/nitpick-oss/nitpick/internal/wire"
)

// ImportCounts say what ImportDir did with a data directory. Every file it looked
// at lands in exactly one of them, so a run can be checked against `ls`.
type ImportCounts struct {
	// Records and History were copied into the database.
	Records, History int
	// Present were already in the database and were left as they were.
	Present int
	// Skipped could not be read as a record, or are not one. Names lists them, to a
	// limit.
	Skipped int
	Names   []string
}

const maxSkippedNames = 20

func (c *ImportCounts) skip(name string) {
	c.Skipped++
	if len(c.Names) < maxSkippedNames {
		c.Names = append(c.Names, name)
	}
}

var historyName = regexp.MustCompile(`^([0-9a-f]{64})\.([0-9]+)\.json$`)

// ImportDir copies the records and the history of a directory the file store wrote
// into the database, once. A record the database already has is left alone, so
// running it again, or after the server has taken new records, never puts an older
// copy over a newer one. The directory is only read.
func (s *DBStore) ImportDir(ctx context.Context, dir string) (ImportCounts, error) {
	var c ImportCounts
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return c, err
	}
	for _, p := range files {
		if err := ctx.Err(); err != nil {
			return c, err
		}
		if err := s.importRecord(ctx, p, &c); err != nil {
			return c, err
		}
	}
	hist, err := filepath.Glob(filepath.Join(dir, "history", "*.json"))
	if err != nil {
		return c, err
	}
	for _, p := range hist {
		if err := ctx.Err(); err != nil {
			return c, err
		}
		if err := s.importHistory(ctx, p, &c); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (s *DBStore) importRecord(ctx context.Context, p string, c *ImportCounts) error {
	name := filepath.Base(p)
	hash := name[:len(name)-len(".json")]
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	var rec wire.Record
	if !wire.ValidHash(hash) || json.Unmarshal(b, &rec) != nil || rec.Hash != hash {
		c.skip(name)
		return nil
	}
	info, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("stat %s: %w", name, err)
	}
	body, err := json.Marshal(rec)
	if err != nil {
		c.skip(name)
		return nil
	}
	// The file's own time is when the record was last written, so a listing keeps
	// the order the file store had.
	next := rowFor(rec, body, info.ModTime().UTC())
	var moved bool
	err = s.db.Transact(ctx, func(tx *gorm.DB) error {
		moved = false
		_, found, err := lock(tx, hash)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		if err := tx.Create(&next).Error; err != nil {
			return err
		}
		moved = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("import %s: %w", name, err)
	}
	if moved {
		c.Records++
	} else {
		c.Present++
	}
	return nil
}

func (s *DBStore) importHistory(ctx context.Context, p string, c *ImportCounts) error {
	name := filepath.Base(p)
	m := historyName.FindStringSubmatch(name)
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("read history/%s: %w", name, err)
	}
	var rec wire.Record
	if m == nil || json.Unmarshal(b, &rec) != nil || rec.Hash != m[1] {
		c.skip("history/" + name)
		return nil
	}
	n, _ := strconv.Atoi(m[2])
	info, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("stat history/%s: %w", name, err)
	}
	var moved bool
	err = s.db.Transact(ctx, func(tx *gorm.DB) error {
		moved = false
		var have int64
		if err := tx.Model(&historyRow{}).Where("hash = ? AND kind = ? AND clears = ?", rec.Hash, "cleared", n).Count(&have).Error; err != nil {
			return err
		}
		if have > 0 {
			return nil
		}
		body, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if err := tx.Create(&historyRow{Hash: rec.Hash, Kind: "cleared", Clears: n, At: info.ModTime().UTC(), Body: body}).Error; err != nil {
			return err
		}
		moved = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("import history/%s: %w", name, err)
	}
	if moved {
		c.History++
	} else {
		c.Present++
	}
	return nil
}
