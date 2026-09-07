//go:build eval

// Does attaching the change to the triage prompt produce a better review?
//
//	make grounding                                  # the tuning corpus
//	make grounding FIXTURES=clean-refactor,multi-defect
//
// Two questions, measured separately because they can disagree. Whether the
// walkthrough describes the change it claims to describe, and whether giving
// triage the diff changes which findings survive it.
package evals

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// contentWord is a word a walkthrough could only choose by knowing the subject.
//
// The first instrument here counted identifiers and file names and measured
// nothing: it scored 1.00 for both arms over six walkthroughs because the
// triage template forbids exactly those ("no bullet lists of files, no
// statistics, no restating the diff"), so neither arm produced a single
// checkable token. A metric that cannot separate a true walkthrough from an
// invented one is not a lenient metric, it is no metric.
//
// Content words can. A walkthrough describing a retry wrapper around an HTTP
// client, for a change that is a SQL migration, shares almost no vocabulary
// with its diff.
var contentWord = regexp.MustCompile(`(?i)\b[a-z][a-z0-9_]{3,}\b`)

// stopwords are the words any walkthrough of any change would use, so their
// presence says nothing about whether this one was shown its change.
var stopwords = map[string]bool{
	"this": true, "that": true, "with": true, "from": true, "into": true, "have": true,
	"which": true, "when": true, "then": true, "they": true, "them": true, "their": true,
	"there": true, "these": true, "those": true, "will": true, "would": true, "could": true,
	"should": true, "been": true, "being": true, "does": true, "each": true, "also": true,
	"only": true, "than": true, "more": true, "most": true, "some": true, "such": true,
	"change": true, "changes": true, "changed": true, "adds": true, "added": true,
	"removes": true, "removed": true, "code": true, "file": true, "files": true,
	"function": true, "functions": true, "review": true, "finding": true, "findings": true,
	"logic": true, "handling": true, "value": true, "values": true, "return": true,
	"returns": true, "instead": true, "before": true, "after": true, "where": true,
	"while": true, "still": true, "rather": true, "without": true, "because": true,
}

// grounding scores one walkthrough against the change it describes.
type grounding struct {
	Words    int      // content words in the walkthrough
	InChange int      // of those, how many appear in the diff
	Files    int      // changed files the walkthrough names
	AllFiles int      // changed files there were
	Invented []string // the words that do not
}

// Rate is the share of content words the change accounts for. A walkthrough
// that says nothing scores 0 rather than 1: silence is measured in the empty
// column, and scoring it as perfect accuracy would rank the arm that writes
// nothing above the arm that writes something true.
func (g grounding) Rate() float64 {
	if g.Words == 0 {
		return 0
	}
	return float64(g.InChange) / float64(g.Words)
}

func scoreGrounding(walkthrough, change string, files []string) grounding {
	g := grounding{AllFiles: len(files)}
	if strings.TrimSpace(walkthrough) == "" {
		return g
	}

	// The change's vocabulary, from the diff as the model would have seen it.
	vocab := map[string]bool{}
	for _, w := range contentWord.FindAllString(change, -1) {
		vocab[strings.ToLower(w)] = true
	}

	seen := map[string]bool{}
	for _, m := range contentWord.FindAllString(walkthrough, -1) {
		w := strings.ToLower(m)
		if stopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		g.Words++
		if vocab[w] {
			g.InChange++
		} else {
			g.Invented = append(g.Invented, w)
		}
	}

	lower := strings.ToLower(walkthrough)
	for _, f := range files {
		if strings.Contains(lower, strings.ToLower(f)) {
			g.Files++
		}
	}
	sort.Strings(g.Invented)
	return g
}

// TestTriageGrounding runs each fixture twice, with review.ground_triage off
// and on, and reports both the walkthrough's groundedness and the detection
// score, so a gain in one that costs the other is visible rather than averaged
// away.
func TestTriageGrounding(t *testing.T) {
	requireGit(t)

	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	fixtures := opts.Fixtures
	if len(fixtures) == 0 {
		fixtures = Fixtures()
	}
	models := opts.Models
	if len(models) == 0 {
		t.Fatal("set MODELS")
	}
	model := models[0]

	var rows []groundingRow

	for _, f := range fixtures {
		for _, arm := range []struct {
			name   string
			ground bool
		}{{"ungrounded", false}, {"grounded", true}} {
			dir := t.TempDir()
			if err := buildRepo(dir, f); err != nil {
				t.Fatalf("%s: build repo: %v", f.Name, err)
			}

			cfg := evalConfig(model)
			cfg.Review.Summary = true
			cfg.Review.GroundTriage = arm.ground

			client, err := llm.Build(cfg.Models.Default)
			if err != nil {
				t.Fatalf("build model: %v", err)
			}
			local := vcs.NewLocal(dir, io.Discard)
			provider := &captureProvider{Local: local}

			raw, err := local.Diff(context.Background(), vcs.Ref{})
			if err != nil {
				t.Fatalf("%s: diff: %v", f.Name, err)
			}

			engine := &review.Engine{
				Config:   cfg,
				Roles:    &llm.Roles{Review: client, Triage: client},
				Provider: provider,
				Log:      slog.New(slog.DiscardHandler),
			}

			report, err := engine.Review(context.Background(), vcs.Ref{})
			if err != nil {
				t.Errorf("%s/%s: review: %v", f.Name, arm.name, err)
				continue
			}

			var changed []string
			for p := range headFiles(f) {
				changed = append(changed, p)
			}
			sort.Strings(changed)

			rows = append(rows, groundingRow{
				Fixture: f.Name,
				Arm:     arm.name,
				Ground:  scoreGrounding(report.Summary, string(raw), changed),
				Detect:  ScoreDetection(f, report.Findings),
				Summary: report.Summary,
				Words:   len(strings.Fields(report.Summary)),
			})
		}
	}

	reportGrounding(t, rows)
}

// groundingRow is one fixture under one arm.
type groundingRow struct {
	Fixture string
	Arm     string
	Ground  grounding
	Detect  DetectionScore
	Summary string
	Words   int
}

// reportGrounding prints the per-fixture table and the two aggregates the
// decision turns on, then the invented tokens themselves, since a rate without
// the words behind it is not evidence anybody can check.
func reportGrounding(t *testing.T, rows []groundingRow) {
	t.Helper()

	var b strings.Builder
	b.WriteString("\n| fixture | arm | grounded | words | in change | files named | words | found | planted |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")

	agg := map[string]*struct {
		tokens, inChange, words, runs, found, planted, silent, noise int
	}{}
	for _, r := range rows {
		a, ok := agg[r.Arm]
		if !ok {
			a = &struct{ tokens, inChange, words, runs, found, planted, silent, noise int }{}
			agg[r.Arm] = a
		}
		a.tokens += r.Ground.Words
		a.inChange += r.Ground.InChange
		a.words += r.Words
		a.runs++
		a.found += r.Detect.Matched
		a.planted += len(r.Detect.Detected)
		if strings.TrimSpace(r.Summary) == "" {
			a.silent++
		}
		a.noise += len(r.Detect.Unmatched)

		fmt.Fprintf(&b, "| %s | %s | %.2f | %d | %d | %d/%d | %d | %d | %d |\n",
			r.Fixture, r.Arm, r.Ground.Rate(), r.Ground.Words, r.Ground.InChange,
			r.Ground.Files, r.Ground.AllFiles, r.Words, r.Detect.Matched, len(r.Detect.Detected))
	}

	b.WriteString("\n| arm | groundedness | content words | not in change | walkthrough words | empty | recall | unmatched findings |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, arm := range []string{"ungrounded", "grounded"} {
		a, ok := agg[arm]
		if !ok {
			continue
		}
		rate := 1.0
		if a.tokens > 0 {
			rate = float64(a.inChange) / float64(a.tokens)
		}
		recall := 0.0
		if a.planted > 0 {
			recall = float64(a.found) / float64(a.planted)
		}
		fmt.Fprintf(&b, "| %s | %.2f | %d | %d | %d | %d/%d | %.2f (%d/%d) | %d |\n",
			arm, rate, a.tokens, a.tokens-a.inChange, a.words, a.silent, a.runs,
			recall, a.found, a.planted, a.noise)
	}

	b.WriteString("\nWords the walkthrough used that its diff does not contain:\n")
	for _, r := range rows {
		if len(r.Ground.Invented) > 0 {
			fmt.Fprintf(&b, "  %s/%s: %s\n", r.Fixture, r.Arm, strings.Join(r.Ground.Invented, " "))
		}
	}

	t.Log(b.String())
}

// headFiles is the set of paths the fixture changes.
func headFiles(f Fixture) map[string]bool {
	out := map[string]bool{}
	for p, head := range f.Head {
		if f.Base[p] != head {
			out[p] = true
		}
	}
	for p := range f.Base {
		if _, ok := f.Head[p]; !ok {
			out[p] = true
		}
	}
	return out
}

func report(t *testing.T, rows []any) {}
