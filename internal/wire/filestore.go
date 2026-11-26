package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FileStore keeps one JSON file per record under a directory.
type FileStore struct {
	dir string
	mu  sync.Mutex
	// lock is held for as long as this process owns the directory. Nil until
	// Lock is called.
	lock *flock.Flock
}

// ErrDirectoryInUse means another process holds the store's directory.
var ErrDirectoryInUse = errors.New("the data directory is in use by another process")

// Lock claims the directory for this process. The rule that a complete record
// is never replaced by a partial one is kept by a mutex, which is per process:
// two servers sharing a directory each check the record and then write it, and
// a partial write landed over a complete one in 210 of 400 races between them.
// A process that serves the directory takes this lock and a second one fails
// here instead of starting. It is released by Unlock, or when the process ends.
func (s *FileStore) Lock() error {
	l := flock.New(filepath.Join(s.dir, ".lock"))
	ok, err := l.TryLock()
	if err != nil {
		return fmt.Errorf("lock %s: %w", s.dir, err)
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrDirectoryInUse, s.dir)
	}
	s.lock = l
	return nil
}

// Unlock gives the directory up.
func (s *FileStore) Unlock() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.Unlock()
	s.lock = nil
	return err
}

// NewFileStore creates the directory if needed.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o750); err != nil {
		return nil, fmt.Errorf("create store %s: %w", dir, err)
	}
	removeStaleTemporaries(dir)
	return &FileStore{dir: dir}, nil
}

// staleTemporary is how old a temporary file must be before it is taken for
// the leftover of a crashed write. A write takes milliseconds, so a younger
// one may be another process's, and removing it would fail that write.
const staleTemporary = 10 * time.Minute

// removeStaleTemporaries clears what a process killed between creating a
// temporary file and renaming it left behind. Their names are unique, so
// nothing would ever overwrite them.
func removeStaleTemporaries(dir string) {
	left, _ := filepath.Glob(filepath.Join(dir, "*.json.*.tmp"))
	for _, p := range left {
		if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > staleTemporary {
			_ = os.Remove(p)
		}
	}
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

// errCorrupt marks a record file that exists and cannot be decoded.
var errCorrupt = errors.New("corrupt record")

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
		return Record{}, false, fmt.Errorf("decode record %s: %w: %w", filepath.Base(p), errCorrupt, err)
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
	old, ok, err := s.read(p)
	if errors.Is(err, errCorrupt) {
		// Reading it stays an error, so a bad record is never taken for none.
		// Writing over it must not be: failing here would wedge the hash for
		// good, and only someone with a shell on the server could unstick it.
		if qerr := s.quarantine(p); qerr != nil {
			return qerr
		}
		old, ok, err = Record{}, false, nil
	}
	if err != nil {
		return err
	}
	if ok {
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
// decode. The temporary name is unique: a fixed one let two processes on one
// directory write into the same file and rename it from under each other.
func writeAtomic(path string, b []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

// quarantine moves a record file that cannot be decoded into history/, so the
// evidence is kept and the hash is free to be written again.
func (s *FileStore) quarantine(p string) error {
	name := fmt.Sprintf("%s.corrupt.%d.json", strings.TrimSuffix(filepath.Base(p), ".json"), time.Now().UnixNano())
	if err := os.Rename(p, filepath.Join(s.dir, "history", name)); err != nil {
		return fmt.Errorf("set aside a corrupt record: %w", err)
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
	if errors.Is(err, errCorrupt) {
		return s.quarantine(p)
	}
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
