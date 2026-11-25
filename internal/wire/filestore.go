package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FileStore keeps one JSON file per record under a directory.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore creates the directory if needed.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o750); err != nil {
		return nil, fmt.Errorf("create store %s: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) path(hash string) (string, error) {
	if !hashRE.MatchString(hash) {
		return "", fmt.Errorf("invalid record hash %q", hash)
	}
	return filepath.Join(s.dir, hash+".json"), nil
}

// Get implements Store.
func (s *FileStore) Get(_ context.Context, hash string) (Record, bool, error) {
	p, err := s.path(hash)
	if err != nil {
		return Record{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(p)
}

func (s *FileStore) read(p string) (Record, bool, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read record: %w", err)
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, false, fmt.Errorf("decode record %s: %w", filepath.Base(p), err)
	}
	return r, true, nil
}

// Put implements Store. It writes atomically, and keeps a complete record
// when an incomplete one arrives.
func (s *FileStore) Put(_ context.Context, rec Record) error {
	p, err := s.path(rec.Hash)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok, err := s.read(p); err != nil {
		return err
	} else if ok {
		if old.Complete && !rec.Complete {
			return nil
		}
		if rec.Clears < old.Clears {
			rec.Clears, rec.ClearedBy = old.Clears, old.ClearedBy
		}
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	return writeAtomic(p, b)
}

// writeAtomic replaces path through a temporary file, so a crash leaves the old
// content or the new and never a truncated record that every later Get fails to
// decode.
func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

// Clear implements Store. The record moves to history/; the clear count and the
// names of who cleared it carry into the next record under that hash.
func (s *FileStore) Clear(_ context.Context, hash, by string) error {
	p, err := s.path(hash)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok, err := s.read(p)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	rec.Clears++
	rec.ClearedBy = append(rec.ClearedBy, by)
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	hist := filepath.Join(s.dir, "history", fmt.Sprintf("%s.%d.json", hash, rec.Clears))
	if err := os.WriteFile(hist, b, 0o640); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	// Leave a stub so the next Put can read the clear count.
	stub := Record{Schema: rec.Schema, Hash: hash, Clears: rec.Clears, ClearedBy: rec.ClearedBy}
	sb, err := json.Marshal(stub)
	if err != nil {
		return fmt.Errorf("encode stub: %w", err)
	}
	return writeAtomic(p, sb)
}
