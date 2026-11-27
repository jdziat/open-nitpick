package wire

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Summary is what a listing says about a record: enough to find one and see
// whether it can be replayed, without the answers, which are the size of the
// review.
type Summary struct {
	Hash     string    `json:"hash"`
	Created  time.Time `json:"created"`
	Tool     string    `json:"tool"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Source   string    `json:"source"`
	Effort   string    `json:"effort,omitempty"`
	Complete bool      `json:"complete"`
	Batches  int       `json:"batches"` // how many answers make it complete
	Answers  int       `json:"answers"` // how many it holds
	Clears   int       `json:"clears"`
}

// ListQuery selects and pages a listing. Records are listed newest first.
type ListQuery struct {
	Model string    // exact, when set
	Tool  string    // exact, when set: the tool version that wrote it
	Since time.Time // records created at or after, when set
	Until time.Time // records created before, when set
	Limit int       // at most this many; 0 means 50, and 200 is the most
	// Cursor continues a listing from the Next that the last page returned.
	Cursor string
}

// Lister is a Store that can say what it holds. It is separate from Store so a
// store that cannot list is still a store.
type Lister interface {
	// List returns a page of summaries and the cursor of the next, empty when
	// there is no more. A record that cannot be read is skipped, not an error:
	// one corrupt file must not hide the others.
	List(ctx context.Context, q ListQuery) (page []Summary, next string, err error)
}

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// List implements Lister. Order is by file modification time, newest first,
// which is when a record was last written; a record is filtered on what it says,
// so a page can hold fewer than Limit when the filter is narrow and the scan
// reached the end.
func (s *FileStore) List(ctx context.Context, q ListQuery) ([]Summary, string, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	limit = min(limit, maxListLimit)

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, "", fmt.Errorf("list store: %w", err)
	}
	type file struct {
		hash string
		at   int64 // modification time, nanoseconds
	}
	var files []file
	for _, e := range entries {
		hash, ok := strings.CutSuffix(e.Name(), ".json")
		// Only a name that is a record hash. What the file holds is checked when it
		// is read, so a directory or a stray file named like one is skipped there.
		if !ok || !hashRE.MatchString(hash) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed between the listing and now
		}
		files = append(files, file{hash, info.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].at != files[j].at {
			return files[i].at > files[j].at
		}
		return files[i].hash > files[j].hash
	})

	afterAt, afterHash, err := parseCursor(q.Cursor)
	if err != nil {
		return nil, "", err
	}
	var page []Summary
	var last file
	for _, f := range files {
		if q.Cursor != "" && (f.at > afterAt || (f.at == afterAt && f.hash >= afterHash)) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		sum, ok := s.summary(f.hash)
		if !ok || !matches(sum, q) {
			continue
		}
		if len(page) == limit {
			// One more match exists, so there is a next page, and it starts after
			// the last record of this one.
			return page, formatCursor(last.at, last.hash), nil
		}
		page = append(page, sum)
		last = f
	}
	return page, "", nil
}

func matches(s Summary, q ListQuery) bool {
	if q.Model != "" && s.Model != q.Model {
		return false
	}
	if q.Tool != "" && s.Tool != q.Tool {
		return false
	}
	if !q.Since.IsZero() && s.Created.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && !s.Created.Before(q.Until) {
		return false
	}
	return true
}

// summary reads a record for its summary without taking the store's lock or
// moving a corrupt file aside: listing observes, it does not repair.
func (s *FileStore) summary(hash string) (Summary, bool) {
	b, err := os.ReadFile(filepath.Join(s.dir, hash+".json"))
	if err != nil {
		return Summary{}, false
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil || r.Hash != hash {
		return Summary{}, false
	}
	return Summary{
		Hash: r.Hash, Created: r.Created, Tool: r.Tool, Provider: r.Provider, Model: r.Model,
		Source: r.Source, Effort: r.Effort, Complete: r.Complete, Batches: r.Batches,
		Answers: len(r.Answers), Clears: r.Clears,
	}, true
}

func formatCursor(at int64, hash string) string { return strconv.FormatInt(at, 10) + "-" + hash }

func parseCursor(c string) (int64, string, error) {
	if c == "" {
		return 0, "", nil
	}
	at, hash, ok := strings.Cut(c, "-")
	n, err := strconv.ParseInt(at, 10, 64)
	if !ok || err != nil || !hashRE.MatchString(hash) {
		return 0, "", fmt.Errorf("%w: the cursor is not one this listing returned", ErrBadQuery)
	}
	return n, hash, nil
}
