package fullreview

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// A repository score is three numbers per language, each reported with the
// count and the denominator beside it so that none is read alone: slop, bugs
// and security findings, weighted by severity, per thousand lines reviewed.
// Rule 1 of docs/measurement.md applies: judge-free, computed from findings
// the keyword rule can credit on a fixture, and per language because a
// reviewer's recall is not the same in every language.

// SlopThreshold is the weighted slop findings per thousand lines above which
// a repository reads as generated and left unread. It is set where the
// fixture repository with its slop file scores above and without it below;
// see TestRepoScoreFixture.
const SlopThreshold = 2.0

// weight is the severity weighting: a critical counts eight nits.
func weight(severity string) float64 {
	switch config.Severity(severity) {
	case config.SeverityCritical:
		return 8
	case config.SeverityError:
		return 4
	case config.SeverityWarning:
		return 2
	case config.SeverityNit:
		return 0.5
	default:
		// Info, and any vocabulary the scale does not know, which the
		// severity ranking reads as info too.
		return 1
	}
}

// LanguageScore is one language's row.
type LanguageScore struct {
	Language string
	Files    int
	Lines    int
	// Counts and weighted sums, by section.
	Slop, Bugs, Security                         int
	SlopWeighted, BugsWeighted, SecurityWeighted float64
}

// PerKLOC is a weighted sum per thousand lines, zero when there are no lines.
func (s LanguageScore) PerKLOC(weighted float64) float64 {
	if s.Lines == 0 {
		return 0
	}
	return weighted / float64(s.Lines) * 1000
}

// Scorecard is every language's row plus the total.
type Scorecard struct {
	Languages []LanguageScore
	Total     LanguageScore
}

// Score computes the scorecard from a whole-tree review and the tree it read.
func Score(report *review.Report, tree *vcs.Tree) Scorecard {
	rows := map[string]*LanguageScore{}
	row := func(lang string) *LanguageScore {
		r, ok := rows[lang]
		if !ok {
			r = &LanguageScore{Language: lang}
			rows[lang] = r
		}
		return r
	}
	for _, p := range tree.Covered {
		r := row(languageOf(p))
		r.Files++
		r.Lines += tree.Lines[p]
	}
	for _, f := range report.Findings {
		r := row(languageOf(f.Path))
		w := weight(f.Severity)
		switch {
		case f.FromAnalyzer && advisoryID.MatchString(f.Source):
			// An advisory is a fact about a dependency, not about the code;
			// it is listed by Sections and not scored.
		case f.Class == string(config.ClassSlop):
			r.Slop++
			r.SlopWeighted += w
		case f.Class == string(config.ClassSecurity):
			r.Security++
			r.SecurityWeighted += w
		default:
			r.Bugs++
			r.BugsWeighted += w
		}
	}
	var card Scorecard
	for _, r := range rows {
		card.Languages = append(card.Languages, *r)
		card.Total.Files += r.Files
		card.Total.Lines += r.Lines
		card.Total.Slop += r.Slop
		card.Total.Bugs += r.Bugs
		card.Total.Security += r.Security
		card.Total.SlopWeighted += r.SlopWeighted
		card.Total.BugsWeighted += r.BugsWeighted
		card.Total.SecurityWeighted += r.SecurityWeighted
	}
	card.Total.Language = "all"
	sort.Slice(card.Languages, func(i, j int) bool {
		if card.Languages[i].Lines != card.Languages[j].Lines {
			return card.Languages[i].Lines > card.Languages[j].Lines
		}
		return card.Languages[i].Language < card.Languages[j].Language
	})
	return card
}

// String renders the scorecard as a table, denominators beside every rate.
func (c Scorecard) String() string {
	var b strings.Builder
	b.WriteString("\nRepository score (weighted findings per thousand lines reviewed; critical 8, error 4, warning 2, info 1, nit 0.5):\n")
	fmt.Fprintf(&b, "  %-12s %6s %8s   %-22s %-22s %-22s\n", "language", "files", "lines", "slop", "bugs", "security")
	rowText := func(r LanguageScore) string {
		cell := func(n int, w float64) string {
			return fmt.Sprintf("%5.2f (%d, %.1f wt)", r.PerKLOC(w), n, w)
		}
		return fmt.Sprintf("  %-12s %6d %8d   %-22s %-22s %-22s\n", r.Language, r.Files, r.Lines,
			cell(r.Slop, r.SlopWeighted), cell(r.Bugs, r.BugsWeighted), cell(r.Security, r.SecurityWeighted))
	}
	for _, r := range c.Languages {
		b.WriteString(rowText(r))
	}
	b.WriteString(rowText(c.Total))
	verdict := "below"
	if c.Total.PerKLOC(c.Total.SlopWeighted) > SlopThreshold {
		verdict = "above"
	}
	fmt.Fprintf(&b, "  slop per thousand lines is %s the threshold of %.1f.\n", verdict, SlopThreshold)
	return b.String()
}

// languageOf names a file's language by extension, "other" when none is known.
func languageOf(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp":
		return "c/c++"
	case ".php":
		return "php"
	case ".sql":
		return "sql"
	case ".sh", ".bash":
		return "shell"
	case ".yaml", ".yml", ".json", ".toml", ".md", ".mod", ".sum", ".lock", ".txt":
		return "config/docs"
	default:
		return "other"
	}
}
