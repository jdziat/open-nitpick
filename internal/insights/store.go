// Package insights keeps the reviews that nitpick runs report to a server, and
// answers the questions a team asks of them: how many findings, in which repos,
// and which files keep producing them.
//
// It is a component of the nitpick server. Reviews are posted by `nitpick
// review -insights`, kept in a SQL database, and analysed in memory over the window a
// request asks about (see View).
package insights

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm/clause"

	"github.com/nitpick-oss/nitpick/internal/database"
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
	// Until, when set, leaves out what is dated after it. The analytics ignore a
	// review dated after now, so a list built for the same period must too.
	Until time.Time
}

// storedReview is one stored review. The body is the review as JSON and is the source
// of truth; the other columns are copies a query narrows by.
type storedReview struct {
	ID       string    `gorm:"primaryKey;size:64"`
	Repo     string    `gorm:"size:200;not null;index:idx_insights_repo_at,priority:1"`
	At       time.Time `gorm:"precision:6;not null;index:idx_insights_repo_at,priority:2;index:idx_insights_at"`
	Complete bool      `gorm:"not null"`
	Body     []byte
}

func (storedReview) TableName() string { return "insights_reviews" }

// Models are the tables the insights component keeps, for the migration.
func Models() []any { return []any{&storedReview{}} }

// Store holds reviews in a SQL database that any number of servers may write.
type Store struct{ db *database.DB }

// NewStore returns a store over db. The tables must have been migrated with Models.
func NewStore(db *database.DB) *Store { return &Store{db: db} }

// Add validates and keeps a review, and returns it as kept. The same review sent
// twice is kept once, by any server: a CI job that retries must not double a
// repo's findings, and which replica it reached must not matter.
func (s *Store) Add(ctx context.Context, r Review) (Review, error) {
	if err := ctx.Err(); err != nil {
		return Review{}, err
	}
	r, err := clean(r)
	if err != nil {
		return Review{}, err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return Review{}, fmt.Errorf("encode the review: %w", err)
	}
	row := storedReview{ID: r.ID, Repo: r.Repo, At: r.At, Complete: r.Complete, Body: body}
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return Review{}, fmt.Errorf("store the review: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		kept, ok, err := s.Review(ctx, r.ID)
		if err != nil {
			return Review{}, err
		}
		if !ok {
			return Review{}, errors.New("store the review: a duplicate was reported and none was found")
		}
		return kept, nil
	}
	return r, nil
}

// Review returns one review by its id.
func (s *Store) Review(ctx context.Context, id string) (Review, bool, error) {
	var row storedReview
	res := s.db.WithContext(ctx).Where("id = ?", id).Limit(1).Find(&row)
	if res.Error != nil {
		return Review{}, false, fmt.Errorf("read the review: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return Review{}, false, nil
	}
	r, err := decodeReview(row)
	return r, err == nil, err
}

func decodeReview(row storedReview) (Review, error) {
	var r Review
	if err := json.Unmarshal(row.Body, &r); err != nil || r.ID != row.ID {
		return Review{}, fmt.Errorf("the stored review %s cannot be decoded", row.ID)
	}
	return r, nil
}

// load reads the reviews dated within a filter's Since and Until, newest first. A review that cannot be
// decoded fails the read: dropping it would show a smaller dashboard with no word
// that something is missing.
func (s *Store) load(ctx context.Context, f Filter) ([]Review, error) {
	tx := s.db.WithContext(ctx).Model(&storedReview{})
	if !f.Since.IsZero() {
		tx = tx.Where("at >= ?", f.Since.UTC())
	}
	if !f.Until.IsZero() {
		tx = tx.Where("at <= ?", f.Until.UTC())
	}
	var rows []storedReview
	if err := tx.Order("at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read the reviews: %w", err)
	}
	out := make([]Review, 0, len(rows))
	for _, row := range rows {
		r, err := decodeReview(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
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
