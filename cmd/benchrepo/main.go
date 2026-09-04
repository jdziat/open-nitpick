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
	// The layout says nothing about what the code is for. `fixtures/` and a
	// pull request titled `fixture: …` told both reviewers they were reading
	// test scaffolding, and both said so in their walkthroughs.
	fixtureRoot  = "services"
	branchPrefix = "change/"
)

// pullRequests is how each fixture is presented: a title an engineer would
// write and a one-sentence body in the author's voice, naming the intent
// and never the defect. Fixtures missing here get a title from their name
// with the giveaway words stripped.
var pullRequests = map[string][2]string{
	"bash-fixed-temp-path":             {"Stage the export in a temp file before uploading", "Writes the export to a temp file first so a failed upload leaves nothing half-written."},
	"capacity-hint-nit":                {"Pre-size the sample window", "Reserves the slice up front so the append loop does not regrow it."},
	"clean-refactor":                   {"Tidy the config loader", "Splits the loader into smaller functions; no behaviour change."},
	"clean-sql-allowlist":              {"Allow sorting by more columns", "Adds the remaining sortable columns to the allowlist."},
	"contract-break":                   {"Make the payload tags consistent", "Aligns the JSON field names and adds an updated timestamp."},
	"cross-file-copy-nit":              {"Build the summary from a snapshot", "Summary now reads from a snapshot instead of live state."},
	"cross-file-sort-nit":              {"Render the roster", "Adds the roster view, listing members alphabetically."},
	"csharp-client-per-request":        {"Send alerts over HTTP", "The notifier posts each alert to the alerts service."},
	"data-loss-migration":              {"Backfill the plan column", "Gives legacy accounts an explicit plan and makes the column required."},
	"defensive-copy-nit":               {"Expose the request headers", "Returns the headers to callers that need them."},
	"duplicate-test-case-nit":          {"More slug test cases", "Extends the table with additional inputs."},
	"go-cache-get-unchecked":           {"Serve rendered pages from the cache", "Pages are rendered on a miss and cached for the next request."},
	"go-cancel-goroutine-leak":         {"Resolve with a context", "Lookups now honour cancellation."},
	"go-empty-filter-deletes-all":      {"Add the upload cleanup endpoint", "Operators can delete uploads by owner and age."},
	"go-empty-slug-path":               {"Readable post URLs", "Posts are published under a slug instead of a numeric id."},
	"go-hardcoded-secret":              {"Fall back to a default API key", "Local runs work without configuring a key."},
	"go-nil-deref":                     {"Return the body length from Fetch", "Fetch now reports how many bytes it read."},
	"go-package-singleton":             {"Read feature flags at startup", "Flags come from the environment once, at program start."},
	"go-query-without-deadline":        {"Monthly totals endpoint", "Adds the monthly report for an account."},
	"go-sql-injection":                 {"Search users by name", "Adds a name search to the user store."},
	"kotlin-widened-input":             {"Accept any collection of ids", "Callers no longer have to convert to a list first."},
	"multi-defect":                     {"Upload handler", "Accepts uploads and writes them to the uploads directory."},
	"php-forbidden-vs-404":             {"Hide documents the caller may not see", "Requests for another user's document no longer reveal it exists."},
	"python-clean-contract":            {"Retry balance reads", "The processor times out a few times a day; a balance read is safe to repeat."},
	"python-command-injection":         {"Archive uploads with tar", "Uploaded directories are packed into an archive on request."},
	"python-expired-token-accepted":    {"Resolve the current user from the bearer token", "Adds current_user for the API handlers."},
	"python-overwrite-through-package": {"Store attachments per user", "Uploads land under the user's own directory."},
	"python-retry-nonidempotent":       {"Retry card charges on timeout", "The processor times out a few times a day; rather than fail the order, try again."},
	"python-secret-to-audit-log":       {"Audit API key creation", "Key minting is recorded in the audit log."},
	"python-timing-unsafe-hmac":        {"Verify webhook signatures", "Rejects webhooks whose signature does not match."},
	"removed-guard":                    {"Simplify project deletion", "Drops a redundant check on the delete path."},
	"retry-no-backoff":                 {"Retry failed fetches", "Transient network errors no longer fail the whole job."},
	"ruby-default-page-size":           {"Larger pages by default", "Most clients page through everything; a bigger default saves round trips."},
	"ruby-mailer-in-transaction":       {"Send the receipt at checkout", "Customers get their receipt as soon as the order is placed."},
	"rust-crate-for-one-call":          {"Parse durations with a crate", "Replaces the hand-written parser."},
	"sorted-for-min-nit":               {"Find the coldest reading", "Reports the lowest temperature in a batch."},
	"style-only":                       {"Formatting", "gofmt and a few renames; no behaviour change."},
	"timezone-boundary":                {"Daily report boundaries", "Reports now cover a calendar day."},
	"ts-clean-contract":                {"Validate tip amounts", "Rejects malformed amounts with a 400."},
	"ts-client-per-request":            {"Quote endpoint", "Answers GET /quote/:sku with the current price."},
	"ts-duration-units-through-barrel": {"Delayed job registration", "Jobs can be registered to run after a delay such as 5m."},
	"ts-money-units":                   {"Cart totals", "Adds the total to charge for a cart."},
	"ts-unawaited-async":               {"Save all items", "saveAll stores every item in the batch."},
	"ts-unbounded-memo-key":            {"Memoize search results", "Repeated searches are common, so answer them from a table."},
}

func presentation(f evals.Fixture) (title, body string) {
	if p, ok := pullRequests[f.Name]; ok {
		return p[0], p[1]
	}
	title = strings.NewReplacer("-nit", "", "-", " ").Replace(f.Name)
	return strings.ToUpper(title[:1]) + title[1:], "Small change; see the diff."
}

// manifestPath is where `prs` records which pull request holds which fixture,
// on THIS side, so the benchmark repository never carries the answer key.
func manifestPath(repo string) string {
	return filepath.Join("cmd", "benchrepo", "manifests", strings.ReplaceAll(repo, "/", "__")+".json")
}

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

Every directory under ` + "`services/`" + ` is one small service in its base state. Every pull request
changes one of them. Some changes carry a defect and some are clean; which is which is recorded
outside this repository, so that nothing here tells a reviewer what to find.

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

func branchFor(i int) string { return fmt.Sprintf("%s%03d", branchPrefix, i+1) }

func branches(dir string) error {
	for i, f := range corpus() {
		branch := branchFor(i)
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
		title, _ := presentation(f)
		if _, err := git(dir, "commit", "-qm", title); err != nil {
			return err
		}
		fmt.Println("branch", branch, "=", f.Name)
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
	manifest := map[string]string{} // branch -> fixture
	for i, f := range corpus() {
		branch := branchFor(i)
		manifest[branch] = f.Name
		existing, err := gh("pr", "list", "--repo", repo, "--head", branch, "--state", "all", "--json", "number", "--jq", "length")
		if err != nil {
			return err
		}
		if existing != "0" {
			fmt.Println("exists", branch)
			continue
		}
		title, body := presentation(f)
		out, err := gh("pr", "create", "--repo", repo, "--head", branch, "--base", "main", "--title", title, "--body", body)
		if err != nil {
			return err
		}
		fmt.Println("opened", out, "=", f.Name)
	}
	blob, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.MkdirAll(filepath.Dir(manifestPath(repo)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(manifestPath(repo), append(blob, '\n'), 0o644)
}

// fixtureBranches reads the manifest `prs` wrote: fixture -> branch.
func fixtureBranches(repo string) (map[string]string, error) {
	data, err := os.ReadFile(manifestPath(repo))
	if err != nil {
		return nil, fmt.Errorf("no manifest for %s (run `benchrepo prs` first): %w", repo, err)
	}
	var byBranch map[string]string
	if err := json.Unmarshal(data, &byBranch); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for branch, fixture := range byBranch {
		out[fixture] = branch
	}
	return out, nil
}

// trigger asks Incumbent to review every fixture pull request. The app only
// reviews on events it sees after installation, so pull requests opened
// before it was installed need to be asked; a comment is the documented way.
func trigger(repo string) error {
	numbers, err := prNumbers(repo)
	if err != nil {
		return err
	}
	branchOf, err := fixtureBranches(repo)
	if err != nil {
		return err
	}
	for _, f := range corpus() {
		number, ok := numbers[branchOf[f.Name]]
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
	branchOf, err := fixtureBranches(repo)
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
		number, ok := numbers[branchOf[f.Name]]
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
			if d.NearMisses > 0 {
				cell += fmt.Sprintf(" ~%d", d.NearMisses)
			}
			fmt.Printf("  %-20s", cell)
			for _, t := range []*tally{totals[r], langTally(byLang[r], lang)} {
				t.reviews++
				t.plants += len(f.Defects)
				t.located += d.Matched
				t.noise += d.Noise()
				t.near += d.NearMisses
				t.comments += len(findings[r])
			}
		}
		fmt.Println()
	}

	fmt.Printf("\n%-12s", "language")
	for _, r := range reviewers {
		fmt.Printf("  %-32s", r)
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
			fmt.Printf("  %-32s", fmt.Sprintf("R %d/%d N %d/%d ~%d C %d", t.located, t.plants, t.noise, t.reviews, t.near, t.comments))
		}
		fmt.Println()
	}
	fmt.Println("\nR = located/plants, N = noise findings/pull requests, ~ = near misses (a plant's keywords in its file, beyond the anchor tolerance; counted in N), C = inline comments scored.")
	return nil
}

type tally struct{ reviews, plants, located, noise, near, comments int }

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
