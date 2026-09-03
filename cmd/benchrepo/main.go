// Command benchrepo turns the eval corpora into a real repository, so the
// GitHub Action can be benchmarked end to end on real pull requests.
//
//	benchrepo init <dir>            write every fixture's base state under fixtures/<name>/ and commit it
//	benchrepo branches <dir>        create one branch per fixture with its head state committed
//	benchrepo prs <owner/repo>      open a pull request for every fixture branch
//	benchrepo score <owner/repo>    read the reviews back off the pull requests and score them
//
// The pull request bodies say nothing about what is planted: the description
// reaches the reviewer, and a description that names the defect measures
// reading comprehension rather than review.
package main

import (
	"context"
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

// score reads every fixture pull request's review comments and scores them
// against the plants, with the same deterministic scorer the harness uses.
func score(repo string) error {
	type comment struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	type pr struct {
		Number  int    `json:"number"`
		HeadRef string `json:"headRefName"`
	}

	raw, err := gh("pr", "list", "--repo", repo, "--state", "all", "--limit", "200", "--json", "number,headRefName")
	if err != nil {
		return err
	}
	var prs []pr
	if err := json.Unmarshal([]byte(raw), &prs); err != nil {
		return err
	}
	byBranch := map[string]int{}
	for _, p := range prs {
		byBranch[p.HeadRef] = p.Number
	}

	fmt.Printf("%-36s %8s %8s %8s\n", "fixture", "located", "noise", "comments")
	totPlants, totLocated, totNoise, reviews := 0, 0, 0, 0
	for _, f := range corpus() {
		number, ok := byBranch[branchPrefix+f.Name]
		if !ok {
			fmt.Printf("%-36s %8s\n", f.Name, "no PR")
			continue
		}
		raw, err := gh("api", "--paginate", fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number))
		if err != nil {
			return err
		}
		var comments []comment
		// --paginate concatenates arrays; decode each.
		dec := json.NewDecoder(strings.NewReader(raw))
		for dec.More() {
			var page []comment
			if err := dec.Decode(&page); err != nil {
				return err
			}
			comments = append(comments, page...)
		}

		prefix := fixtureRoot + "/" + f.Name + "/"
		var findings []review.Finding
		for _, c := range comments {
			if !strings.Contains(c.Body, "open-nitpick") {
				continue
			}
			title, rationale, _ := strings.Cut(strings.TrimSpace(c.Body), "\n")
			findings = append(findings, review.Finding{
				Path: strings.TrimPrefix(c.Path, prefix), Line: c.Line,
				Title: title, Rationale: rationale,
			})
		}
		if len(comments) == 0 {
			// No review yet, or a clean review; the summary would say which,
			// and a summary-only review posts no comments.
		}
		d := evals.ScoreDetection(f, findings)
		reviews++
		totPlants += len(f.Defects)
		totLocated += d.Matched
		totNoise += d.Noise()
		fmt.Printf("%-36s %8s %8d %8d\n", f.Name, strconv.Itoa(d.Matched)+"/"+strconv.Itoa(len(f.Defects)), d.Noise(), len(findings))
	}
	if reviews > 0 {
		fmt.Printf("\nlocated %d/%d, noise %d over %d pull requests\n", totLocated, totPlants, totNoise, reviews)
	}
	_ = context.Background
	return nil
}
