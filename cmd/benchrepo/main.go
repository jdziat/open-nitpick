// Command benchrepo turns the eval corpora into a real repository, so the
// GitHub Action can be benchmarked end to end on real pull requests.
//
//	benchrepo init <dir>            write every fixture's base state under fixtures/<name>/ and commit it
//	benchrepo branches <dir>        create one branch per fixture with its head state committed
//	benchrepo prs <owner/repo>      open a pull request for every fixture branch
//	benchrepo trigger <owner/repo>  ask Incumbent's hosted app to review every fixture pull request
//	benchrepo score <owner/repo>    read every reviewer's comments back off the pull requests and score them
//
// The pull request bodies say nothing about what is planted: the description
// reaches the reviewer, and a description that names the defect measures
// reading comprehension rather than review.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/evals"
	"github.com/jdziat/open-nitpick/internal/review"
)

const (
	fixtureRoot  = "fixtures"
	branchPrefix = "fixture/"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: benchrepo init|branches <dir> | prs|score <owner/repo>")
		os.Exit(64)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = initRepo(os.Args[2])
	case "branches":
		err = branches(os.Args[2])
	case "prs":
		err = openPRs(os.Args[2])
	case "trigger":
		err = trigger(os.Args[2])
	case "score":
		err = score(os.Args[2])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchrepo:", err)
		os.Exit(1)
	}
}

// corpus is every fixture, in a stable order.
func corpus() []evals.Fixture {
	fx := evals.EveryFixture()
	sort.Slice(fx, func(i, j int) bool { return fx[i].Name < fx[j].Name })
	return fx
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=benchrepo", "GIT_AUTHOR_EMAIL=bench@example.com",
		"GIT_COMMITTER_NAME=benchrepo", "GIT_COMMITTER_EMAIL=bench@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func writeTree(dir, prefix string, files map[string]string) error {
	for p, content := range files {
		full := filepath.Join(dir, prefix, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func initRepo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := git(dir, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	for _, f := range corpus() {
		prefix := filepath.Join(fixtureRoot, f.Name)
		if err := writeTree(dir, prefix, f.Extra); err != nil {
			return err
		}
		if err := writeTree(dir, prefix, f.Base); err != nil {
			return err
		}
	}
	if err := writeTree(dir, "", scaffold()); err != nil {
		return err
	}
	if _, err := git(dir, "add", "-A"); err != nil {
		return err
	}
	_, err := git(dir, "commit", "-qm", "benchmark corpus: base state of every fixture")
	return err
}

// scaffold is the repository's own files: the workflow, the reviewer's
// configuration, and a README that explains what the place is.
func scaffold() map[string]string {
	return map[string]string{
		"README.md": `# nitpick-bench

A private benchmark repository for [open-nitpick](https://github.com/jdziat/open-nitpick).

Every directory under ` + "`fixtures/`" + ` is one fixture from open-nitpick's eval corpora in its
BASE state. Every pull request applies one fixture's HEAD state. Some plant a defect and some
are clean; which is which is recorded in open-nitpick's ` + "`internal/evals`" + ` and nowhere in this
repository, so that nothing here tells a reviewer what to find.

To score the reviews posted here against the plants:

    go run ./cmd/benchrepo score <owner>/<repo>

from an open-nitpick checkout.
`,
		".nitpick.yaml": `models:
  default:
    provider: openrouter
    model: anthropic/claude-sonnet-4.6
  triage:
    provider: openrouter
    model: z-ai/glm-5.3-flash
    temperature: 0

review:
  fail_on: none
  min_severity: nit
  related_context: true
  token_budget_per_request: 120000

linters:
  mode: auto
`,
		".github/workflows/review.yml": `name: Review
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]

permissions:
  contents: read
  pull-requests: write

concurrency:
  group: nitpick-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: jdziat/open-nitpick@v1
        with:
          provider: openrouter
          model: anthropic/claude-sonnet-4.6
          api-key: ${{ secrets.OPENROUTER_API_KEY }}
          fail-on: none
`,
		".gitignore": "dist/\n",
	}
}

func branches(dir string) error {
	for _, f := range corpus() {
		branch := branchPrefix + f.Name
		if _, err := git(dir, "checkout", "-q", "-B", branch, "main"); err != nil {
			return err
		}
		prefix := filepath.Join(fixtureRoot, f.Name)
		// Files the head state no longer has are deleted; buildRepo in the
		// harness leaves them, but a real pull request would not.
		for p := range f.Base {
			if _, keep := f.Head[p]; !keep {
				_ = os.Remove(filepath.Join(dir, prefix, filepath.FromSlash(p)))
			}
		}
		if err := writeTree(dir, prefix, f.Head); err != nil {
			return err
		}
		if _, err := git(dir, "add", "-A"); err != nil {
			return err
		}
		if _, err := git(dir, "commit", "-qm", "fixture "+f.Name); err != nil {
			return err
		}
		fmt.Println("branch", branch)
	}
	_, err := git(dir, "checkout", "-q", "main")
	return err
}

func gh(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func openPRs(repo string) error {
	for _, f := range corpus() {
		branch := branchPrefix + f.Name
		// One pull request per branch; skip the ones already open.
		existing, err := gh("pr", "list", "--repo", repo, "--head", branch, "--state", "all", "--json", "number", "--jq", "length")
		if err != nil {
			return err
		}
		if existing != "0" {
			fmt.Println("exists", branch)
			continue
		}
		body := fmt.Sprintf("Change under review: `%s`, one fixture of the open-nitpick eval corpus applied as a pull request.", f.Name)
		out, err := gh("pr", "create", "--repo", repo, "--head", branch, "--base", "main",
			"--title", "fixture: "+f.Name, "--body", body)
		if err != nil {
			return err
		}
		fmt.Println("opened", out)
	}
	return nil
}

// trigger asks Incumbent to review every fixture pull request. The app only
// reviews on events it sees after installation, so pull requests opened
// before it was installed need to be asked; a comment is the documented way.
func trigger(repo string) error {
	numbers, err := prNumbers(repo)
	if err != nil {
		return err
	}
	for _, f := range corpus() {
		number, ok := numbers[branchPrefix+f.Name]
		if !ok {
			continue
		}
		if _, err := gh("pr", "comment", "--repo", repo, strconv.Itoa(number), "--body", "@incumbentai full review"); err != nil {
			return err
		}
		fmt.Println("asked", f.Name)
	}
	return nil
}

func prNumbers(repo string) (map[string]int, error) {
	raw, err := gh("pr", "list", "--repo", repo, "--state", "all", "--limit", "200", "--json", "number,headRefName")
	if err != nil {
		return nil, err
	}
	var prs []struct {
		Number  int    `json:"number"`
		HeadRef string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(raw), &prs); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, p := range prs {
		out[p.HeadRef] = p.Number
	}
	return out, nil
}

// reviewers are the contenders a comment can belong to, told apart by what
// posted it: open-nitpick by the marker it leaves in every comment,
// Incumbent by its app account. Anything else is a human and is not scored.
var reviewers = []string{"open-nitpick", "incumbent"}

func reviewerOf(login, body string) string {
	switch {
	case strings.Contains(body, "<!-- open-nitpick"):
		return "open-nitpick"
	case strings.HasPrefix(login, "incumbentai"):
		return "incumbent"
	}
	return ""
}

// score reads every fixture pull request's review comments and scores each
// reviewer's against the plants, with the same deterministic scorer the
// harness uses. Only inline comments are scored: Incumbent folds what it
// calls nitpicks into the review body, and those are neither anchored nor
// counted here — for it or against it.
func score(repo string) error {
	type comment struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		StartLine int    `json:"start_line"`
		Body      string `json:"body"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	numbers, err := prNumbers(repo)
	if err != nil {
		return err
	}

	totals := map[string]*tally{}
	byLang := map[string]map[string]*tally{}
	for _, r := range reviewers {
		totals[r] = &tally{}
		byLang[r] = map[string]*tally{}
	}

	fmt.Printf("%-36s", "fixture")
	for _, r := range reviewers {
		fmt.Printf("  %-20s", r)
	}
	fmt.Println()
	for _, f := range corpus() {
		number, ok := numbers[branchPrefix+f.Name]
		if !ok {
			fmt.Printf("%-36s  no PR\n", f.Name)
			continue
		}
		raw, err := gh("api", "--paginate", fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number))
		if err != nil {
			return err
		}
		var comments []comment
		dec := json.NewDecoder(strings.NewReader(raw))
		for dec.More() {
			var page []comment
			if err := dec.Decode(&page); err != nil {
				return err
			}
			comments = append(comments, page...)
		}

		prefix := fixtureRoot + "/" + f.Name + "/"
		findings := map[string][]review.Finding{}
		for _, c := range comments {
			r := reviewerOf(c.User.Login, c.Body)
			if r == "" {
				continue
			}
			title, rationale, _ := strings.Cut(strings.TrimSpace(c.Body), "\n")
			fnd := review.Finding{Path: strings.TrimPrefix(c.Path, prefix), Line: c.Line, Title: title, Rationale: rationale}
			if c.StartLine > 0 && c.StartLine < c.Line {
				fnd.Line, fnd.EndLine = c.StartLine, c.Line
			}
			findings[r] = append(findings[r], fnd)
		}

		lang := fixtureLanguage(f)
		fmt.Printf("%-36s", f.Name)
		for _, r := range reviewers {
			d := evals.ScoreDetection(f, findings[r])
			cell := fmt.Sprintf("%d/%d", d.Matched, len(f.Defects))
			if n := d.Noise(); n > 0 {
				cell += fmt.Sprintf(" +%dn", n)
			}
			fmt.Printf("  %-20s", cell)
			for _, t := range []*tally{totals[r], langTally(byLang[r], lang)} {
				t.reviews++
				t.plants += len(f.Defects)
				t.located += d.Matched
				t.noise += d.Noise()
				t.comments += len(findings[r])
			}
		}
		fmt.Println()
	}

	fmt.Printf("\n%-12s", "language")
	for _, r := range reviewers {
		fmt.Printf("  %-28s", r)
	}
	fmt.Println()
	langs := map[string]bool{}
	for _, r := range reviewers {
		for l := range byLang[r] {
			langs[l] = true
		}
	}
	var names []string
	for l := range langs {
		names = append(names, l)
	}
	sort.Strings(names)
	for _, l := range append(names, "all") {
		fmt.Printf("%-12s", l)
		for _, r := range reviewers {
			t := totals[r]
			if l != "all" {
				t = langTally(byLang[r], l)
			}
			fmt.Printf("  %-28s", fmt.Sprintf("R %d/%d N %d/%d C %d", t.located, t.plants, t.noise, t.reviews, t.comments))
		}
		fmt.Println()
	}
	fmt.Println("\nR = located/plants, N = noise findings/pull requests, C = inline comments scored.")
	return nil
}

type tally struct{ reviews, plants, located, noise, comments int }

func langTally(m map[string]*tally, lang string) *tally {
	if m[lang] == nil {
		m[lang] = &tally{}
	}
	return m[lang]
}

// fixtureLanguage names a fixture's language from the extension of the files
// its change touches, majority wins; the same rule as the harness report.
func fixtureLanguage(f evals.Fixture) string {
	counts := map[string]int{}
	for p, head := range f.Head {
		if base, ok := f.Base[p]; ok && base == head {
			continue
		}
		ext := strings.TrimPrefix(filepath.Ext(p), ".")
		switch ext {
		case "tsx":
			ext = "ts"
		case "kts":
			ext = "kt"
		case "cc", "cpp":
			ext = "c"
		}
		if ext != "" {
			counts[ext]++
		}
	}
	best, n := "other", 0
	for ext, c := range counts {
		if c > n || (c == n && ext < best) {
			best, n = ext, c
		}
	}
	return best
}
