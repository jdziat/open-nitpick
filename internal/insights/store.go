// Package insights keeps the reviews that nitpick runs report to a server, and
// answers the questions a team asks of them: how many findings, in which repos,
// and which files keep producing them.
//
// It is a component of the nitpick server. Reviews are posted by `nitpick
// review -insights`, held in an append-only file and indexed in memory.
package insights

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	stdlog "log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"

	"github.com/nitpick-oss/nitpick/internal/redact"
)

// Limits on what one review may bring. They bound memory and the width of a page
// more than they judge the review.
const (
	MaxFindings = 500
	maxRepo     = 200
	maxPath     = 500
	maxText     = 4000
	maxSkew     = 5 * time.Minute
)

// severityScale is the one list of severities, least severe first, with what a
// finding of each adds to a file's hotspot score. The spacing is deliberate: one
// critical outranks several nits, but not a pile of errors.
var severityScale = []struct {
	name   string
	weight float64
}{{"nit", 0.25}, {"info", 0.5}, {"warning", 1}, {"error", 3}, {"critical", 8}}

// Severities lists the accepted values, least severe first.
var Severities = func() []string {
	names := make([]string, len(severityScale))
	for i, s := range severityScale {
		names[i] = s.name
	}
	return names
}()

var severityWeight = func() map[string]float64 {
	w := make(map[string]float64, len(severityScale))
	for _, s := range severityScale {
		w[s.name] = s.weight
	}
	return w
}()

// Finding is one published finding, in the shape `review -format json` prints.
type Finding struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	EndLine   int    `json:"end_line,omitempty"`
	Severity  string `json:"severity"`
	Class     string `json:"class"`
	Category  string `json:"category,omitempty"`
	Title     string `json:"title"`
	Rationale string `json:"rationale,omitempty"`
}

// Review is one run of nitpick against one change.
type Review struct {
	ID       string    `json:"id"`
	Repo     string    `json:"repo"`
	Ref      string    `json:"ref,omitempty"`
	Commit   string    `json:"commit,omitempty"`
	At       time.Time `json:"at"`
	Result   string    `json:"result"`
	Complete bool      `json:"complete"`
	Findings []Finding `json:"findings"`
	// Truncated is how many findings past MaxFindings were dropped.
	Truncated int    `json:"truncated,omitempty"`
	Withheld  int    `json:"withheld,omitempty"`
	Source    string `json:"source,omitempty"`
}

// Filter narrows a listing of reviews.
type Filter struct {
	Repo  string
	Since time.Time
}

// Store holds reviews. Add appends to the file and the index under one lock, so
// the index never holds a review the file lost.
type Store struct {
	mu      sync.Mutex
	f       *os.File
	lock    *flock.Flock
	write   func([]byte) (int, error) // f.Write; a test replaces it to fail a write
	byID    map[string]int
	reviews []Review
	skipped int
}

// OpenStore opens the reviews file in dir, creating it. A line that does not
// parse, such as the tail a crash left, is skipped and counted, so one bad line
// does not keep the server from starting and is not dropped silently.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create the insights directory: %w", err)
	}
	// One process appends to the file. A second would interleave its lines with
	// the first's, and neither would see the other's reviews until a restart.
	l := flock.New(filepath.Join(dir, ".lock"))
	if ok, err := l.TryLock(); err != nil {
		return nil, fmt.Errorf("lock %s: %w", dir, err)
	} else if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInUse, dir)
	}
	p := filepath.Join(dir, "reviews.jsonl")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		_ = l.Unlock()
		return nil, fmt.Errorf("open the reviews file: %w", err)
	}
	s := &Store{f: f, lock: l, byID: map[string]int{}}
	s.write = f.Write
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Review
		if err := json.Unmarshal(line, &r); err != nil || r.ID == "" {
			s.skipped++
			continue
		}
		s.index(r)
	}
	if err := sc.Err(); err != nil {
		_ = f.Close()
		_ = l.Unlock()
		return nil, fmt.Errorf("read the reviews file: %w", err)
	}
	// A crash can lose the newline of a line that is otherwise whole, which parses,
	// so a parse failure is not the test. Look at the last byte: the next append
	// must start a line.
	if endsMidLine(f) {
		if _, err := f.WriteString("\n"); err != nil {
			_ = f.Close()
			_ = l.Unlock()
			return nil, fmt.Errorf("repair the reviews file: %w", err)
		}
	}
	return s, nil
}

// log reports a fault that has no caller to return it to. It writes to stderr
// through the standard logger, as the notary's store faults do.
func (s *Store) log(msg string, err error) { stdlog.Printf("insights: %s: %v", msg, err) }

// endsMidLine reports whether the file is non-empty and does not end in a newline.
func endsMidLine(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false
	}
	return last[0] != '\n'
}

func (s *Store) index(r Review) {
	if _, dup := s.byID[r.ID]; dup {
		return
	}
	s.byID[r.ID] = len(s.reviews)
	s.reviews = append(s.reviews, r)
}

// ErrInUse is returned when another process holds the directory.
var ErrInUse = errors.New("insights directory is in use by another process")

// Close releases the file and the directory.
func (s *Store) Close() error {
	err := s.f.Close()
	if uerr := s.lock.Unlock(); err == nil {
		err = uerr
	}
	return err
}

// Skipped is how many lines of the file could not be read at start-up.
func (s *Store) Skipped() int { return s.skipped }

// Add validates and keeps a review, and returns it as kept. The same review sent
// twice is kept once: a CI job that retries must not double a repo's findings.
func (s *Store) Add(ctx context.Context, r Review) (Review, error) {
	if err := ctx.Err(); err != nil {
		return Review{}, err
	}
	r, err := clean(r)
	if err != nil {
		return Review{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, dup := s.byID[r.ID]; dup {
		return s.reviews[i], nil
	}
	line, err := json.Marshal(r)
	if err != nil {
		return Review{}, fmt.Errorf("encode the review: %w", err)
	}
	info, err := s.f.Stat()
	if err != nil {
		return Review{}, fmt.Errorf("stat the reviews file: %w", err)
	}
	if _, err := s.write(append(line, '\n')); err != nil {
		// A partial line would be joined by the next review's, and both would be
		// skipped at the next start. Cut the file back to where this write began.
		if terr := s.f.Truncate(info.Size()); terr != nil {
			s.log("could not remove a partial line after a failed write", terr)
		}
		return Review{}, fmt.Errorf("write the review: %w", err)
	}
	s.index(r)
	return r, nil
}

// ErrInvalid wraps every refusal of a review's content, so a handler can answer
// 400 for those and 500 for a store that failed.
var ErrInvalid = errors.New("invalid review")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func clean(r Review) (Review, error) {
	if r.Repo == "" || len(r.Repo) > maxRepo || hasControl(r.Repo) {
		return Review{}, invalid("repo must be 1 to %d printable characters", maxRepo)
	}
	// A little skew between a runner's clock and this one is normal. More is
	// refused because every query drops what is dated after now, so the review
	// would be accepted and then invisible.
	if r.At.After(time.Now().Add(maxSkew)) {
		return Review{}, invalid("at is more than %s in the future", maxSkew)
	}
	r.At = r.At.UTC()
	r.Ref, r.Commit, r.Result = limit(r.Ref, 100), limit(r.Commit, 64), limit(r.Result, 40)
	r.Source = limit(r.Source, 100)
	if hasControl(r.Ref) || hasControl(r.Commit) || hasControl(r.Result) {
		return Review{}, invalid("ref, commit and result must be printable")
	}
	if len(r.Findings) > MaxFindings {
		r.Truncated += len(r.Findings) - MaxFindings
		r.Findings = r.Findings[:MaxFindings]
	}
	out := make([]Finding, 0, len(r.Findings))
	for i, f := range r.Findings {
		if _, ok := severityWeight[f.Severity]; !ok {
			return Review{}, invalid("finding %d: severity %q is not one of %s", i, f.Severity, strings.Join(Severities, ", "))
		}
		p := path.Clean(strings.ReplaceAll(f.Path, "\\", "/"))
		if f.Path == "" || len(p) > maxPath || p == "." || strings.HasPrefix(p, "/") || p == ".." || strings.HasPrefix(p, "../") || hasControl(p) {
			return Review{}, invalid("finding %d: path %q is not a path inside the repository", i, f.Path)
		}
		f.Path = p
		f.Line = max(f.Line, 0)
		f.EndLine = max(f.EndLine, 0)
		f.Class, f.Category = limit(f.Class, 60), limit(f.Category, 60)
		f.Title = redact.Secrets(limit(f.Title, 300))
		f.Rationale = redact.Secrets(limit(f.Rationale, maxText))
		out = append(out, f)
	}
	r.Findings = out
	// The id is taken before a missing time is filled in, so a retry that carries
	// no time is the same review and is kept once.
	r.ID = digest(r)
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	return r, nil
}

// digest identifies a review by what it says, not by when it arrived.
func digest(r Review) string {
	c := r
	c.ID, c.Source = "", ""
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hasControl(s string) bool {
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return true
		}
	}
	return !utf8.ValidString(s)
}

func limit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Reviews lists reviews newest first.
func (s *Store) Reviews(f Filter) []Review {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Review
	for _, r := range s.reviews {
		if f.Repo != "" && r.Repo != f.Repo || r.At.Before(f.Since) {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Review returns one review by its id.
func (s *Store) Review(id string) (Review, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.byID[id]
	if !ok {
		return Review{}, false
	}
	return s.reviews[i], true
}
