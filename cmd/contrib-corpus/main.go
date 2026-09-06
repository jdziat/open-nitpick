// Command contrib-corpus builds a corpus for the contributor experiment of
// docs/findings.md ("Contributors"): from a repository whose history labels
// model-written commits with a co-author trailer, it writes the lines each
// commit added to each file, labelled by who the trailer says wrote them.
//
// The corpus is third-party code and is not committed; the test that reads
// it skips when it is absent. Output layout, which internal/modelid reads:
//
//	<out>/<repo>/<label>/<language>/<NNNNN>-<k>.<ext>.txt
//
// where label is "human" or "model-<tool>", NNNNN is the commit's rank by
// date, so an experiment can split by time, and k is the file's index
// within the commit.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// trailers name the tools whose co-author lines label a commit. The match
// is on the trailer, not on the word: "copilot" in a message about the
// Copilot feature is not a Copilot commit.
var trailers = []struct {
	tool string
	re   *regexp.Regexp
}{
	{"claude", regexp.MustCompile(`(?im)^co-authored-by:\s*claude\b|generated with \[?claude code`)},
	{"copilot", regexp.MustCompile(`(?im)^co-authored-by:\s*(copilot|copilot-swe-agent)`)},
	{"codex", regexp.MustCompile(`(?im)^co-authored-by:\s*(codex|chatgpt|openai)`)},
	{"cursor", regexp.MustCompile(`(?im)^co-authored-by:\s*cursor`)},
	{"aider", regexp.MustCompile(`(?im)^co-authored-by:\s*aider`)},
	{"devin", regexp.MustCompile(`(?im)^co-authored-by:\s*devin`)},
}

var exts = map[string]string{"go": ".go", "python": ".py", "typescript": ".ts"}

func main() {
	var (
		repo     = flag.String("repo", "", "repository URL or owner/name")
		out      = flag.String("out", "contrib", "output directory")
		clones   = flag.String("clones", "", "directory to keep clones in (default: a temporary directory)")
		lang     = flag.String("lang", "go", "language: go, python or typescript")
		perClass = flag.Int("per-class", 300, "at most this many samples per label")
		minLines = flag.Int("min-lines", 30, "a file's added lines in one commit must be at least this many")
		maxLines = flag.Int("max-lines", 600, "and at most this many")
		since    = flag.String("since", "", "only commits after this date (the default is the first model-labelled commit, so both classes share an era)")
	)
	flag.Parse()
	if *repo == "" || exts[*lang] == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*repo, *out, *clones, *lang, *perClass, *minLines, *maxLines, *since); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type commit struct {
	sha, date, label string
	seq              int
}

func run(repo, out, clones, lang string, perClass, minLines, maxLines int, since string) error {
	url := repo
	if !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@") {
		url = "https://github.com/" + repo + ".git"
	}
	slug := strings.NewReplacer("/", "_", ".git", "").Replace(strings.TrimPrefix(strings.TrimPrefix(repo, "https://github.com/"), "git@github.com:"))
	if clones == "" {
		tmp, err := os.MkdirTemp("", "contrib-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		clones = tmp
	}
	dir := filepath.Join(clones, slug)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		fmt.Fprintf(os.Stderr, "cloning %s\n", url)
		if err := git(clones, "clone", "-q", "--no-tags", "--single-branch", url, dir); err != nil {
			return err
		}
	}

	// The trailer is in the body, so the whole message is read.
	args := []string{"log", "--format=%H%x00%cI%x00%B%x1e", "--no-merges"}
	if since != "" {
		args = append(args, "--since="+since)
	}
	raw, err := gitOut(dir, args...)
	if err != nil {
		return err
	}
	var commits []commit
	for rec := range strings.SplitSeq(raw, "\x1e") {
		parts := strings.SplitN(strings.TrimSpace(rec), "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		c := commit{sha: parts[0], date: parts[1], label: "human"}
		for _, t := range trailers {
			if t.re.MatchString(parts[2]) {
				c.label = "model-" + t.tool
				break
			}
		}
		commits = append(commits, c)
	}
	// The dates are ISO 8601 with the committer's offset, so a string
	// order would sort by local time; parse, and compare instants.
	when := func(c commit) time.Time {
		t, err := time.Parse(time.RFC3339, c.date)
		if err != nil {
			return time.Time{}
		}
		return t
	}
	sort.Slice(commits, func(i, j int) bool { return when(commits[i]).Before(when(commits[j])) })
	// Both classes come from the same era: nothing before the first
	// model-labelled commit is sampled, so a time split cannot separate
	// the classes by date alone, and neither can the classifier by the
	// repository's own drift.
	for i, c := range commits {
		if c.label != "human" {
			commits = commits[i:]
			break
		}
	}
	for i := range commits {
		commits[i].seq = i
	}
	counts := map[string]int{}
	for _, c := range commits {
		counts[c.label]++
	}
	fmt.Fprintf(os.Stderr, "%d commits: %v\n", len(commits), counts)

	// Per label, commits are drawn in a seeded random order across the
	// whole era, so a time split cannot tell the classes apart by date.
	// The first attempt took the newest human commits against model
	// commits spread over a year; the second walked each label from the
	// oldest with a stride and stopped at the quota, which put every human
	// sample before every model sample in a repository with many human
	// commits. Both made the older-half split a date test, not an author
	// test.
	byLabel := map[string][]commit{}
	for _, c := range commits {
		byLabel[c.label] = append(byLabel[c.label], c)
	}
	written := map[string]int{}
	unreadable := 0
	ext := exts[lang]
	for label, cs := range byLabel {
		r := rand.New(rand.NewPCG(2026, uint64(len(cs))))
		r.Shuffle(len(cs), func(i, j int) { cs[i], cs[j] = cs[j], cs[i] })
		for i := 0; i < len(cs) && written[label] < perClass; i++ {
			c := cs[i]
			show, err := gitOut(dir, "show", "--format=", "--unified=0", "--no-color", "--diff-filter=AM", c.sha, "--", "*"+ext)
			if err != nil {
				// Counted and reported, so a short quota can be told from
				// a clone that cannot show its own commits.
				unreadable++
				fmt.Fprintf(os.Stderr, "skipping %s: %v\n", c.sha[:12], err)
				continue
			}
			k := 0
			for _, path := range sortedKeys(addedLines(show)) {
				added := addedLines(show)[path]
				if written[label] >= perClass || !strings.HasSuffix(path, ext) || strings.HasSuffix(path, "_test"+ext) || strings.Contains(path, "vendor/") || strings.Contains(path, "testdata/") {
					continue
				}
				if len(added) < minLines || len(added) > maxLines {
					continue
				}
				target := filepath.Join(out, slug, label, lang)
				if err := os.MkdirAll(target, 0o755); err != nil {
					return err
				}
				name := fmt.Sprintf("%05d-%d.%s.txt", c.seq, k, strings.TrimPrefix(ext, "."))
				k++
				body := fmt.Sprintf("// commit %s %s %s\n", c.sha[:12], c.date, path) + strings.Join(added, "\n") + "\n"
				if lang == "python" {
					body = "#" + body[2:]
				}
				if err := os.WriteFile(filepath.Join(target, name), []byte(body), 0o644); err != nil {
					return err
				}
				written[label]++
			}
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %v under %s (%d commit(s) unreadable)\n", written, filepath.Join(out, slug), unreadable)
	return nil
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// addedLines returns, per file, the lines a diff added.
func addedLines(diff string) map[string][]string {
	out := map[string][]string{}
	var path string
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if path != "" {
				out[path] = append(out[path], line[1:])
			}
		}
	}
	return out
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var b bytes.Buffer
	cmd.Stdout = &b
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args[:min(2, len(args))], " "), err)
	}
	return b.String(), nil
}
