package evals

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// The two prompts the engine sends, identified by text the templates open with.
// Matching on the prompt rather than on call order is what lets the two review
// batches run concurrently without the script becoming order-dependent.
const (
	triageMarker = "triaging findings"
	reviewMarker = "Review the following changes"
)

// The fixture's two faces, by path and line. Both are re-derived from Head by
// TestTheDedupFixturePlantIsWhereItSaysItIs rather than trusted from here.
const (
	causePath  = "platform/retry/retry.go"
	causeLine  = 16
	effectPath = "billing/charge.go"
	effectLine = 20
)

// batchLLM answers each review batch with whatever the test scripted for the
// file that batch contains, and answers triage by ECHOING BACK the findings it
// was shown.
//
// The echo is the point, and it is what stops this being another guard that
// passes against the bug it names. Triage is the pass README credits with
// merging duplicates across batches, so a scripted triage that returns one
// finding would publish one finding whatever dedupe did, and the test would go
// green with the merge deleted. Deriving the answer from renderForTriage's own
// listing makes the published count exactly the count dedupe handed to triage:
// the script cannot merge, because it can only repeat.
type batchLLM struct {
	mu sync.Mutex

	// byFile maps a path that identifies a batch to the JSON that batch returns.
	byFile map[string]string

	// failTriage makes the triage call return unparseable output, which is the
	// path where the engine publishes locally deduped findings instead.
	failTriage bool

	triagePrompt string
	reviewCalls  int
	triageCalls  int
}

func (b *batchLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	var joined strings.Builder
	for _, m := range msgs {
		joined.WriteString(m.Content)
	}
	text := joined.String()

	b.mu.Lock()
	defer b.mu.Unlock()

	if strings.Contains(text, triageMarker) {
		b.triageCalls++
		b.triagePrompt = text
		if b.failTriage {
			return nil, errors.New("simulated triage failure")
		}
		return &llms.Response{Content: echoTriage(text)}, nil
	}

	if !strings.Contains(text, reviewMarker) {
		return nil, fmt.Errorf("unexpected prompt: %.120s", text)
	}
	b.reviewCalls++

	for file, response := range b.byFile {
		// bundle.Render heads every entry with this line, so it identifies the
		// batch a prompt is for without depending on call order.
		if strings.Contains(text, "### File: "+file) {
			return &llms.Response{Content: response}, nil
		}
	}
	// A batch the test did not script found nothing. The five filler files are
	// deliberately clean, so this is the honest answer for them.
	return &llms.Response{Content: `{"findings":[]}`}, nil
}

func (b *batchLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (b *batchLLM) Provider() llms.Provider { return "batched" }
func (b *batchLLM) Model() string           { return "batched" }

func (b *batchLLM) counts() (reviews, triages int, prompt string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reviewCalls, b.triageCalls, b.triagePrompt
}

// triageListing matches one entry of renderForTriage's numbered list:
//
//  1. [warning] platform/retry/retry.go:16 — A POST is replayable
var triageListing = regexp.MustCompile(`(?m)^\d+\. \[([a-z]+)\] (\S+):(\d+) — (.*)$`)

// echoTriage answers a triage prompt with exactly the findings it lists.
func echoTriage(prompt string) string {
	var out []string
	for _, m := range triageListing.FindAllStringSubmatch(prompt, -1) {
		line, err := strconv.Atoi(m[3])
		if err != nil {
			continue
		}
		out = append(out, fmt.Sprintf(
			`{"path":%q,"line":%d,"severity":%q,"category":"correctness","class":"correctness","title":%q,"rationale":"Echoed by the scripted triage."}`,
			m[2], line, m[1], m[4]))
	}
	return `{"findings":[` + strings.Join(out, ",") + `],"summary":"Walkthrough."}`
}

// finding renders one review-pass response.
func finding(path string, line int, title string) string {
	return fmt.Sprintf(
		`{"findings":[{"path":%q,"line":%d,"severity":"warning","category":"correctness","class":"correctness","title":%q,"rationale":"A capture the gateway already applied is applied a second time."}]}`,
		path, line, title)
}

// reviewFixture drives the real engine over a fixture with a scripted model and
// no network: a real git repository, a real diff, real batching, and the
// published review the provider was handed.
func reviewFixture(t *testing.T, f Fixture, model *batchLLM) (*review.Report, *vcs.Review) {
	t.Helper()

	dir := t.TempDir()
	if err := buildRepo(dir, f); err != nil {
		t.Fatalf("build repo: %v", err)
	}

	cfg := evalConfig(Model{ID: "dedup-probe"})
	client := llm.NewClientForTest(model, cfg.Models.Default)
	provider := &captureProvider{Local: vcs.NewLocal(dir, io.Discard)}

	engine := &review.Engine{
		Config:   cfg,
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: provider,
		Log:      slog.New(slog.DiscardHandler),
	}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if provider.review == nil {
		t.Fatal("nothing was published, so this test proves nothing about what a reader sees")
	}
	return report, provider.review
}

// requireGit skips when git is missing, since every test here builds a real
// repository and reads a real diff out of it.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// assemble runs the shipped bundler over a fixture and returns the plan plus
// which batch each path landed in.
func assemble(t *testing.T, f Fixture) (*bundle.Plan, map[string]int) {
	t.Helper()

	dir := t.TempDir()
	if err := buildRepo(dir, f); err != nil {
		t.Fatalf("build repo: %v", err)
	}

	raw, err := vcs.NewLocal(dir, io.Discard).Diff(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	files, err := diff.Parse(raw)
	if err != nil {
		t.Fatalf("parse diff: %v", err)
	}

	plan, err := bundle.Assemble(context.Background(), evalConfig(Model{ID: "assembly-probe"}), files,
		func(_ context.Context, path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	batchOf := map[string]int{}
	for i, b := range plan.Batches {
		for _, p := range b.Paths() {
			batchOf[p] = i
		}
	}
	return plan, batchOf
}

// TestTheDedupFixtureSplitsTheDefect reads the batch split back out of
// bundle.Assemble instead of trusting the paragraph that describes it.
//
// The fixture's entire value is that its two halves land in DIFFERENT requests:
// that is what gives dedupe two reports of one defect to merge. It is the exact
// inverse of the invariant the tuning corpus needs — ts-unbounded-memo-key must
// keep its halves TOGETHER, because there the defect is invisible from either
// file alone — and it turns on the same fragile fact, alphabetical position at
// a six-file boundary. One added path sorting before platform/ re-splits the
// change and this fixture quietly stops testing anything, which is precisely
// how two one-line edits were once enough to falsify the corpus's other
// batching claim while the suite stayed green.
func TestTheDedupFixtureSplitsTheDefect(t *testing.T) {
	requireGit(t)

	for _, f := range dedupFixtures() {
		plan, batchOf := assemble(t, f)

		if len(plan.Batches) < 2 {
			t.Fatalf("%s: assembled into %d batch(es); with one request there is no cross-batch "+
				"duplicate and dedupe has nothing to merge", f.Name, len(plan.Batches))
		}

		cause, okCause := batchOf[causePath]
		effect, okEffect := batchOf[effectPath]
		if !okCause || !okEffect {
			t.Fatalf("%s: the plan is missing a half of the defect: %s=%v %s=%v (plan: %v)",
				f.Name, causePath, okCause, effectPath, okEffect, batchOf)
		}
		if cause == effect {
			t.Errorf("%s: %s and %s are both in batch %d, so ONE request sees the whole defect and "+
				"reports it once. This fixture exists to have them apart; move a filler file or "+
				"rename a path so six changed paths sort before platform/",
				f.Name, effectPath, causePath, cause+1)
		}

		t.Logf("%s: %d batches, %s in batch %d, %s in batch %d",
			f.Name, len(plan.Batches), effectPath, effect+1, causePath, cause+1)
	}
}

// TestTheDedupFixturePlantIsWhereItSaysItIs re-derives the plant's line by
// counting lines out of Head, and checks the engine could publish a comment
// there.
//
// This fixture is in neither scored corpus, so not one ground-truth test in
// groundtruth_test.go touches it: no line check, no reportability check. Those
// checks caught four wrong line numbers in the corpus's first eight fixtures,
// and a defect anchored one line off would make the tests below assert
// something a real reviewer could never produce.
func TestTheDedupFixturePlantIsWhereItSaysItIs(t *testing.T) {
	requireGit(t)

	// What each anchor must actually contain, read out of Head rather than
	// asserted about it. The cause is the widened predicate; the effect is the
	// capture handed to the retry helper as a POST.
	want := map[string]string{
		causePath:  "return method != http.MethodPatch",
		effectPath: "return retry.Do(http.MethodPost",
	}
	at := map[string]int{causePath: causeLine, effectPath: effectLine}

	for _, f := range dedupFixtures() {
		for path, substr := range want {
			body, ok := f.Head[path]
			if !ok {
				t.Fatalf("%s: Head has no %s", f.Name, path)
			}
			lines := strings.Split(body, "\n")
			n := at[path]
			if n > len(lines) {
				t.Fatalf("%s: %s has %d lines, so line %d does not exist", f.Name, path, len(lines), n)
			}
			if got := lines[n-1]; !strings.Contains(got, substr) {
				t.Errorf("%s: %s:%d is %q, want it to contain %q", f.Name, path, n, got, substr)
			}
		}

		// Declared ground truth has to agree with the file, or the fixture
		// documents a defect that is not there.
		for _, d := range f.Defects {
			if d.Path != causePath || d.Line != causeLine {
				t.Errorf("%s: Defect anchors %s:%d; the constants this file's tests script are %s:%d",
					f.Name, d.Path, d.Line, causePath, causeLine)
			}
		}

		// Reportable: a finding on a line the diff did not add is dropped
		// before publication, which would make every assertion below vacuous.
		dir := t.TempDir()
		if err := buildRepo(dir, f); err != nil {
			t.Fatalf("build repo: %v", err)
		}
		raw, err := vcs.NewLocal(dir, io.Discard).Diff(context.Background(), vcs.Ref{})
		if err != nil {
			t.Fatalf("diff: %v", err)
		}
		files, err := diff.Parse(raw)
		if err != nil {
			t.Fatalf("parse diff: %v", err)
		}
		for path, n := range at {
			file := files.Find(path)
			if file == nil {
				t.Fatalf("%s: the diff does not contain %s", f.Name, path)
			}
			if !file.IsChangedLine(n) {
				t.Errorf("%s: %s:%d is not an added line, so a finding anchored there is dropped "+
					"and the review publishes nothing", f.Name, path, n)
			}
		}
	}
}

// TestACrossBatchDuplicateIsPublishedOnce is the test the merge path has never
// had: two review requests, both reporting the same defect at the same line,
// and one comment on the pull request.
//
// The fixture is what makes the input honest. Its defect has a face in each
// batch — the predicate that declares a POST replayable, and the capture handed
// to the retry helper — and review.md tells a reviewer to "anchor to the line
// where the problem is, not where its effect surfaces", which names the
// predicate from either side. A duplicate the model would not naturally produce
// would test nothing.
//
// WHY IT FAILS WITH DEDUP OFF, in both arms. Delete the dedupe call at the top
// of triage() and the two findings survive as two: the echoing arm publishes
// two because renderForTriage lists two and the script can only repeat what it
// is shown, and the failing arm publishes two because that path returns the
// locally deduped findings directly. Verified by making that edit and watching
// both arms go red, not by reading the code.
func TestACrossBatchDuplicateIsPublishedOnce(t *testing.T) {
	requireGit(t)

	const title = "A POST is replayable, so a capture can be applied twice"

	for _, arm := range []struct {
		name       string
		failTriage bool
	}{
		// The ordinary path: triage runs and merges nothing, which is the
		// honest worst case for a real triage model.
		{name: "triage echoes what it was shown"},
		// The degraded path the engine documents: "when triage fails the run
		// continues with locally deduped findings". Here dedupe is the only
		// thing between two reports and two comments.
		{name: "triage fails", failTriage: true},
	} {
		t.Run(arm.name, func(t *testing.T) {
			for _, f := range dedupFixtures() {
				model := &batchLLM{
					failTriage: arm.failTriage,
					byFile: map[string]string{
						// Both batches report the SAME defect at the SAME line.
						effectPath: finding(causePath, causeLine, title),
						causePath:  finding(causePath, causeLine, title),
					},
				}

				report, published := reviewFixture(t, f, model)
				reviews, triages, prompt := model.counts()

				// Without two reports there is no duplicate, and everything
				// below would pass against an engine that never merged.
				if reviews < 2 {
					t.Fatalf("%s: the model was asked to review %d batch(es); this test needs two "+
						"batches to both report the defect", f.Name, reviews)
				}
				if triages != 1 {
					t.Fatalf("%s: triage was called %d times, want 1", f.Name, triages)
				}

				anchor := fmt.Sprintf("%s:%d", causePath, causeLine)
				if listed := strings.Count(prompt, anchor); listed != 1 {
					t.Errorf("%s: triage was shown %s %d times, want 1. The two batches reported one "+
						"defect and the merge before triage did not collapse it:\n%s",
						f.Name, anchor, listed, listing(prompt))
				}

				if len(report.Findings) != 1 {
					t.Errorf("%s: report carries %d findings, want 1: %+v",
						f.Name, len(report.Findings), report.Findings)
				}

				var comments []string
				for _, c := range published.Comments {
					comments = append(comments, fmt.Sprintf("%s:%d", c.Path, c.Line))
				}
				if len(comments) != 1 || comments[0] != anchor {
					t.Errorf("%s: published %d comment(s) at %v, want exactly one at %s. Two batches "+
						"reporting one defect must not put two comments on the same line",
						f.Name, len(comments), comments, anchor)
				}
			}
		})
	}
}

// TestOneDefectAnchoredTwiceIsNotDeduped pins the boundary of the merge above,
// so nothing reads that test as a claim the engine collapses every cross-batch
// duplicate.
//
// Finding.Key is path, line and normalized title, so two reports of one defect
// merge only when they agree about WHERE the problem is. review.md pulls a
// reviewer both ways here: "anchor to the line where the problem is" points at
// the predicate from either batch, while "path must exactly match one of the
// file paths given below" points each batch at its own file. When the second
// instruction wins, the same defect arrives as two keys and reaches the pull
// request as two comments, and only the triage model can merge it.
//
// This is recorded, not endorsed. It is the residual after the fixture and it
// is the case a reader should know about before quoting the engine's dedup as
// unconditional.
func TestOneDefectAnchoredTwiceIsNotDeduped(t *testing.T) {
	requireGit(t)

	for _, f := range dedupFixtures() {
		model := &batchLLM{
			// The failing arm on purpose: it takes the triage model out of the
			// answer entirely, so two comments here is a statement about
			// dedupe and not about what a script chose to echo.
			failTriage: true,
			byFile: map[string]string{
				// Each batch anchors in the file it was actually given.
				effectPath: finding(effectPath, effectLine, "A capture is retried, so it can be applied twice"),
				causePath:  finding(causePath, causeLine, "A POST is replayable, so a capture can be applied twice"),
			},
		}

		report, published := reviewFixture(t, f, model)

		if len(report.Findings) != 2 || len(published.Comments) != 2 {
			t.Errorf("%s: two anchors for one defect produced %d finding(s) and %d comment(s), want 2 "+
				"and 2. If the engine has learned to merge findings that disagree about where the "+
				"defect is, this test is the record that it did not use to, and the claim in "+
				"dedupFixtures about the residual needs rewriting", f.Name,
				len(report.Findings), len(published.Comments))
		}
	}
}

// TestTheDedupFixtureIsNotInEitherScoredCorpus makes the omission explicit.
//
// A fixture in neither Fixtures() nor HeldOutFixtures() is invisible to every
// ground-truth test that iterates AllFixtures, which is the quietest way this
// corpus has to lose a plant and the reason
// TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus exists. That test
// enumerates warningFixtures and nitFixtures by name, so it cannot see this
// file at all. The omission here is deliberate — a fixture whose point is that
// one defect is reported twice would be scored as one detection and one false
// positive — and stating it as an assertion is what stops the next reader
// wiring it in for tidiness and moving a published noise number.
func TestTheDedupFixtureIsNotInEitherScoredCorpus(t *testing.T) {
	scored := map[string]string{}
	for _, f := range Fixtures() {
		scored[f.Name] = "Fixtures()"
	}
	for _, f := range HeldOutFixtures() {
		scored[f.Name] = "HeldOutFixtures()"
	}

	for _, f := range dedupFixtures() {
		if where, ok := scored[f.Name]; ok {
			t.Errorf("%s is in %s. It is a dedup harness, not a plant: one defect reported from two "+
				"batches scores as one detection and one false positive, so it would make a correct "+
				"review read as a noisy one. Wiring it in needs score.go to admit a defect with more "+
				"than one acceptable anchor first", f.Name, where)
		}
	}
}

// listing extracts the numbered finding lines from a triage prompt so a failure
// prints those rather than the whole system prompt.
func listing(prompt string) string {
	var out []string
	for _, m := range triageListing.FindAllString(prompt, -1) {
		out = append(out, strings.TrimSpace(m))
	}
	return strings.Join(out, "\n")
}
