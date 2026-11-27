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
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nitpick-oss/nitpick/internal/redact"
)

// Limits on what one review may bring. They bound memory and the width of a page
// more than they judge the review.
const (
	MaxFindings = 500
	maxRepo     = 200
	maxPath     = 500
	maxText     = 4000
)

// severityWeight is what a finding adds to a file's hotspot score. The spacing is
// deliberate: one critical outranks several nits, but not a pile of errors.
var severityWeight = map[string]float64{"nit": 0.25, "info": 0.5, "warning": 1, "error": 3, "critical": 8}

// Severities lists the accepted values, least severe first.
var Severities = []string{"nit", "info", "warning", "error", "critical"}

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
	p := filepath.Join(dir, "reviews.jsonl")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the reviews file: %w", err)
	}
	s := &Store{f: f, byID: map[string]int{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	var torn bool
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Review
		if err := json.Unmarshal(line, &r); err != nil || r.ID == "" {
			s.skipped++
			torn = true
			continue
		}
		s.index(r)
	}
	if err := sc.Err(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("read the reviews file: %w", err)
	}
	if torn {
		// A torn tail has no newline; end it so the next append starts a line.
		if _, err := f.WriteString("\n"); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("repair the reviews file: %w", err)
		}
	}
	return s, nil
}

func (s *Store) index(r Review) {
	if _, dup := s.byID[r.ID]; dup {
		return
	}
	s.byID[r.ID] = len(s.reviews)
	s.reviews = append(s.reviews, r)
}

// Close releases the file.
func (s *Store) Close() error { return s.f.Close() }

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
	if _, err := s.f.Write(append(line, '\n')); err != nil {
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
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	if r.At.After(time.Now().Add(24 * time.Hour)) {
		return Review{}, invalid("at is in the future")
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
	r.ID = digest(r)
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
