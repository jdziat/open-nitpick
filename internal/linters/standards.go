package linters

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/standards"
)

// The analyzers, as a source of conformity observations.
//
// It lives here rather than in internal/standards because this package imports
// internal/review and internal/review imports internal/standards, so the arrow
// has to point this way. That is also the better shape: internal/standards owns
// the arithmetic and the interface and knows nothing about what a linter is.

// StandardsSource runs the configured analyzers over a whole tree.
type StandardsSource struct {
	cfg *config.Config
	log *slog.Logger
}

// NewStandardsSource builds a source from the operator's analyzer settings.
//
// The operator's, never the tree's. Analyzer configuration is policy: a
// repository able to point a linter at rules of its own would be choosing the
// conventions it is measured against, which is the seam config.BasePolicy holds
// one layer up.
func NewStandardsSource(cfg *config.Config, log *slog.Logger) *StandardsSource {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &StandardsSource{cfg: cfg, log: log}
}

// Name identifies this source in a rule name and a report.
func (s *StandardsSource) Name() string { return "linters" }

// Observe lints the tree and reports each finding as an observation.
//
// The denominator is read back from the analyzers' own statuses rather than
// taken from the tree; docs/findings.md has what that fixed.
func (s *StandardsSource) Observe(ctx context.Context, root string, files []standards.File) (
	[]standards.Observation, standards.Coverage, error) {

	if s.cfg == nil || s.cfg.Linters.Mode == config.LinterOff {
		return nil, standards.Coverage{Why: "linters.mode is off"}, nil
	}

	// Absolute, because the runners resolve modules and configuration against
	// it. A relative root found no Go module here and every analyzer reported
	// having run over nothing.
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, standards.Coverage{}, fmt.Errorf("resolve %s: %w", root, err)
	}

	// The conformity ruleset, not the review's. An operator who named their own
	// config keeps it: that is policy and this is not the place to override it.
	cfg := *s.cfg
	if cfg.Linters.GolangciConfig == "" {
		path, cleanup, err := WriteGolangciConventions(abs)
		if err != nil {
			return nil, standards.Coverage{Why: "conformity ruleset: " + err.Error()}, nil
		}
		defer cleanup()
		cfg.Linters.GolangciConfig = path
	}

	set := New(abs, &cfg, s.log)
	claims := claimedFiles(set, files)
	if len(claims) == 0 {
		return nil, standards.Coverage{Why: "no enabled analyzer reads any file in this tree"}, nil
	}

	parsed, err := treeDiff(abs, claims)
	if err != nil {
		return nil, standards.Coverage{}, err
	}

	found, runErr := set.Run(ctx, parsed)
	if runErr != nil {
		// A partial run is still a measurement, and saying so beats losing it.
		// Which analyzers managed it is read from the statuses below, so the
		// denominator narrows to them rather than the error being swallowed.
		s.log.Warn("an analyzer failed during the convention measurement", "error", runErr)
	}

	cov := s.coverage(abs, set, claims)
	if !cov.Ran {
		return nil, cov, nil
	}

	obs := make([]standards.Observation, 0, len(found))
	for _, f := range found {
		if _, ok := cov.Files[f.Path]; !ok {
			// Outside what this source says it read. Counting it would let a
			// share exceed one, which is the arithmetic reporting that the
			// denominator is wrong rather than the numerator.
			continue
		}
		obs = append(obs, standards.Observation{Rule: f.Source, Path: f.Path, Line: f.Line})
	}
	return obs, cov, nil
}

// coverage is the files belonging to analyzers that ran.
//
// An analyzer that failed or had nothing to read contributes none of its files,
// so a tool that is absent cannot enter its language as conforming. The reasons
// are collected into Why so a reader learns which tool went quiet.
func (s *StandardsSource) coverage(root string, set *Set, claims map[string][]string) standards.Coverage {
	cov := standards.Coverage{Files: map[string]int{}}

	var quiet []string
	for _, st := range set.Statuses() {
		if st.Outcome != review.LinterRan {
			quiet = append(quiet, fmt.Sprintf("%s (%s: %s)", st.Linter, st.Outcome, st.State))
			continue
		}
		cov.Ran = true
		for _, path := range claims[st.Linter] {
			if _, done := cov.Files[path]; done {
				continue
			}
			n, err := countLines(filepath.Join(root, path))
			if err != nil {
				// A file the walk named and the reader cannot open is a fact
				// about the tree, not a conforming file. Left out of the
				// denominator rather than entered as clean.
				s.log.Debug("skipping an unreadable file in the convention measurement",
					"path", path, "error", err)
				continue
			}
			cov.Files[path] = n
		}
	}

	sort.Strings(quiet)
	switch {
	case !cov.Ran && len(quiet) > 0:
		cov.Why = "no analyzer ran: " + strings.Join(quiet, ", ")
	case !cov.Ran:
		cov.Why = "no analyzer was enabled"
	case len(quiet) > 0:
		cov.Why = "some analyzers did not run: " + strings.Join(quiet, ", ")
	}
	return cov
}

// claimedFiles maps each runner to the files it reads.
//
// Catalog and Builtins are the same tables `nitpick linters` and Suggest read,
// so coverage here is decided by the table the roster is printed from and the
// two cannot disagree about which tool reads which file.
func claimedFiles(set *Set, files []standards.File) map[string][]string {
	described := map[string]CatalogEntry{}
	for _, e := range append(Catalog(), Builtins()...) {
		described[e.Name] = e
	}

	out := map[string][]string{}
	for _, r := range set.runners {
		e, ok := described[r.Name()]
		if !ok {
			continue
		}
		for _, f := range files {
			if matchesAny(e, []string{f.Path}) {
				out[r.Name()] = append(out[r.Name()], f.Path)
			}
		}
	}
	return out
}

// treeDiff builds the synthetic whole-file diff the analyzers are handed.
func treeDiff(root string, claims map[string][]string) (diff.Files, error) {
	seen := map[string]bool{}
	var paths []string
	for _, ps := range claims {
		for _, p := range ps {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, p := range paths {
		src, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		lines := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n",
			p, p, p, len(lines))
		for _, l := range lines {
			b.WriteString("+" + l + "\n")
		}
	}
	return diff.Parse([]byte(b.String()))
}

// countLines counts the lines in a file.
func countLines(path string) (int, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strings.Count(string(src), "\n") + 1, nil
}
