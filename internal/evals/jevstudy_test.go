//go:build eval

package evals

// Pre-registered 2026-11-27, before any model call. Do not edit the rules below
// after a run; add a dated note instead.
//
// QUESTION. When a reviewer files a finding, which method best tells a real
// defect from one that is not, and which part of what the decision model is shown
// carries the signal?
//
// DATA. The tuning and caller corpora (evals.Fixtures, CallerFixtures,
// InfoFixtures), held-out fixtures excluded and left unspent. Three reviewers,
// each run twice per fixture at temperature 0: deepseek/deepseek-v4.1-flash,
// qwen/qwen3.8-27b, z-ai/glm-5.3-flash. Every finding the engine files is one
// observation. It is real when ScoreRun matches it to a planted defect and not
// real otherwise, so every finding on a clean fixture is not real. Findings are
// scored from the Jev state the engine built for them, recorded by a Decider that
// forwards to the real client.
//
// METHODS, all scored on the same findings:
//   confidence   the reviewer's own confidence; a finding without one is scored 0.5
//   severity     the reviewer's severity as 0 to 1
//   jev          Jev's bug-plus-error probability on the full state
//   jev-claim    Jev on the claim, severity and cited code only
//   jev-nofile   Jev on the full state with the whole file removed
//   jev-nodiff   Jev on the full state with the diff removed
//   judge        a general model asked the same question on the full state
//   jev+conf     the mean of jev and confidence
//
// METRICS. Primary: the share of not-real findings withheld at the threshold that
// keeps at least 95% of the real findings (called W95). Secondary: AUC, and the
// result at jev's documented threshold of 0.3. Differences are paired over
// findings with 1000-draw bootstrap intervals; a difference counts only when its
// interval excludes zero.
//
// What each outcome changes. review.jev.threshold is off by default because Jev
// sends source to a third party, and no result here turns it on. A method that
// beats jev on W95 with an interval excluding zero is reported as a candidate and
// not shipped by this study. A part of the state whose removal moves W95 by less
// than its own interval is reported as not shown to matter. The study does not
// tune a threshold on the data it reports: the threshold is read off one half of
// the findings, split by fixture, and its W95 is reported on the other half.
//
// WHAT IT CANNOT SAY. The corpus is small and its defects are planted, so the
// not-real findings are the ones a reviewer invents on short fixtures. The
// reviewers are cheap models. The number of not-real findings bounds every
// interval and is printed first.
//
// NOTE 2026-11-27, after the first collection and before the second. The first
// arm ran with repository verification off, so every Jev state read "(the check
// made no lookups)"; the docs say Jev follows that check and is shown what it
// read. The label "real" is also wider than what Jev's bug-plus-error score
// measures: it includes planted nits and info observations, which are true and
// are scored low by design. Both are flaws in this design, not in Jev. The results
// above are reported as pre-registered. What follows is exploratory and is marked
// so wherever it is printed: Jev's full probability vector (support = 1 - noise),
// a second arm with review.verify on, the verification verdict itself as a method,
// and a stratum of fixtures whose plants are all warning or above.
//
// SPEND. One shared OpenRouter key; the test reads its usage before and after and
// stops a phase that has already passed the cap in NITPICK_JEV_STUDY_CAP_USD.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"
	"github.com/zalando/go-keyring"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/jev"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/review"
)

// studyAPIKey returns the OpenRouter key the study spends against, from the
// environment or the keystore. It is never printed or written.
func studyAPIKey() string {
	for _, name := range []string{EnvAPIKey, "LLM_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	key, _ := keyring.Get(llm.KeyringService, "openrouter")
	return strings.TrimSpace(key)
}

// keyUsageUSD is what the key has spent in total, from OpenRouter's key endpoint.
func keyUsageUSD(ctx context.Context, key string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/key", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Data struct {
			Usage float64 `json:"usage"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, err
	}
	return body.Data.Usage, nil
}

// observation is one filed finding with its label and the state Jev was shown.
type studyObservation struct {
	Model, Fixture string
	Run            int
	Path           string
	Line           int
	Title          string
	Real           bool
	Confidence     float64
	Severity       float64
	State          string
	Jev            map[string]float64 // bug plus error probability, by method name
	Support        map[string]float64 // 1 minus the noise probability, by method name
	Verify         string             // "", "kept" or "refuted" by the repository check
	Arm            string
}

// studyRecorder forwards to the real client and keeps every state it saw.
type studyRecorder struct {
	inner  review.Decider
	mu     sync.Mutex
	states []string
	scores []jev.Scores
}

func (r *studyRecorder) Classify(ctx context.Context, state string) (jev.Scores, error) {
	scores, err := r.inner.Classify(ctx, state)
	if err == nil {
		r.mu.Lock()
		r.states = append(r.states, state)
		r.scores = append(r.scores, scores)
		r.mu.Unlock()
	}
	return scores, err
}

// studyVerifyDone is the engine's line when the repository check finishes. A review
// with findings and no such line did not run the check, which is what zero refutations
// looks like too, so the study counts the lines rather than trusting the setting.
var studyVerifyDone = regexp.MustCompile(`verification finished\"? confirmed=(\d+) refuted=(\d+) uncertain=(\d+) unchecked=(\d+) withheld=(\d+)`)

type studyVerifyTally struct{ reviews, confirmed, refuted, uncertain, unchecked, withheld int }

func (v *studyVerifyTally) add(log string) bool {
	m := studyVerifyDone.FindStringSubmatch(log)
	if m == nil {
		return false
	}
	n := func(i int) int { x, _ := strconv.Atoi(m[i]); return x }
	v.reviews++
	v.confirmed += n(1)
	v.refuted += n(2)
	v.uncertain += n(3)
	v.unchecked += n(4)
	v.withheld += n(5)
	return true
}

var studyStateHead = regexp.MustCompile(`(?m)^File: (.+):(\d+)\nTitle: (.*)$`)

// section cuts a named part out of a Jev state. The headings are the ones jevState
// writes, so a change to them fails the study loudly rather than scoring a state
// with nothing removed.
func studyWithout(state, heading, next string) (string, bool) {
	start := strings.Index(state, heading)
	if start < 0 {
		return state, false
	}
	end := len(state)
	if next != "" {
		if i := strings.Index(state[start:], next); i >= 0 {
			end = start + i
		}
	}
	return state[:start] + heading + "(removed)\n\n" + state[end:], true
}

// studyID keys a finding by where it is and what it says, whitespace collapsed, which
// is the only thing the filed finding and the recorded state have in common.
func studyID(path string, line int, title string) string {
	return fmt.Sprintf("%s:%d:%s", path, line, strings.Join(strings.Fields(title), " "))
}

// overruledFindings are the findings the engine withheld, which are still scored.
func overruledFindings(r *review.Report) []review.Finding {
	var out []review.Finding
	for _, w := range r.Overruled {
		out = append(out, w.Finding)
	}
	return out
}

// verifyExpertName is how the engine attributes a finding the repository check
// withheld. It is the engine's own constant, restated because it is unexported, and
// TestTheStudyKeepsAFindingTheRepositoryCheckWithheld fails if the two drift.
const verifyExpertName = "a repository check"

func studySeverity(s string) float64 {
	switch strings.ToLower(s) {
	case "critical":
		return 1
	case "error":
		return 0.8
	case "warning":
		return 0.5
	case "info":
		return 0.3
	case "nit":
		return 0.1
	}
	return 0.5
}

func TestJevStudyCollect(t *testing.T) {
	if os.Getenv("NITPICK_JEV_STUDY") != "1" {
		t.Skip("set NITPICK_JEV_STUDY=1 to run the Jev study; it spends money")
	}
	key := studyAPIKey()
	if key == "" {
		t.Fatal("no OpenRouter key in the environment or the keystore")
	}
	capUSD := 40.0
	if v, err := strconv.ParseFloat(os.Getenv("NITPICK_JEV_STUDY_CAP_USD"), 64); err == nil && v > 0 {
		capUSD = v
	}
	// "plain" is the pre-registered arm. "verify" turns review.verify on, so the
	// decision model is shown what the repository check read.
	arm := os.Getenv("NITPICK_JEV_STUDY_ARM")
	if arm == "" {
		arm = "plain"
	}
	verifyOn := arm == "verify"
	ctx := t.Context()
	before, err := keyUsageUSD(ctx, key)
	if err != nil {
		t.Fatalf("read key usage: %v", err)
	}
	t.Logf("arm %s, key usage before: $%.4f, cap for this phase $%.2f", arm, before, capUSD)

	corpus := os.Getenv("NITPICK_JEV_STUDY_CORPUS")
	if err := studyGuardHeldOut(corpus, os.Getenv("NITPICK_JEV_STUDY_SPEND_HELDOUT"), os.Getenv("NITPICK_JEV_STUDY_FILE")); err != nil {
		t.Fatal(err)
	}
	fixtures, err := studyCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	models := []string{"deepseek/deepseek-v4.1-flash", "qwen/qwen3.8-27b", "z-ai/glm-5.3-flash"}
	runs := 2
	if v, err := strconv.Atoi(os.Getenv("NITPICK_JEV_STUDY_RUNS")); err == nil && v > 0 {
		runs = v
	}
	t.Logf("corpus=%q fixtures=%d models=%v runs=%d", corpus, len(fixtures), models, runs)

	client, err := jev.NewClient("", "", key)
	if err != nil {
		t.Fatal(err)
	}

	type job struct {
		model Model
		f     Fixture
		run   int
	}
	var jobs []job
	for _, m := range models {
		for _, f := range fixtures {
			for r := 0; r < runs; r++ {
				jobs = append(jobs, job{ParseModel(m), f, r})
			}
		}
	}

	var mu sync.Mutex
	var obs []studyObservation
	var tally studyVerifyTally
	skipped := 0
	failed := 0
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	stop := false
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			halted := stop
			mu.Unlock()
			if halted {
				return
			}
			rec := &studyRecorder{inner: client}
			var logged strings.Builder
			opts := Options{Timeout: 10 * time.Minute, Decider: rec, Log: slog.New(slog.NewTextHandler(&logged, nil)), Tune: studyTune(verifyOn)}
			result := Run(ctx, j.model, j.f, j.run, opts)
			if result.Err != nil || result.Report == nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			mu.Lock()
			ran := verifyOn && tally.add(logged.String())
			if verifyOn && !ran && len(result.Report.Findings)+len(overruledFindings(result.Report)) > 0 {
				skipped++
			}
			mu.Unlock()
			joined := studyJoin(j.model.ID, j.f, j.run, result, rec, arm, ran)
			mu.Lock()
			obs = append(obs, joined...)
			mu.Unlock()
			if spent, err := keyUsageUSD(ctx, key); err == nil && spent-before > capUSD {
				mu.Lock()
				stop = true
				mu.Unlock()
			}
		}(j)
	}
	wg.Wait()

	after, _ := keyUsageUSD(ctx, key)
	real := 0
	for _, o := range obs {
		if o.Real {
			real++
		}
	}
	t.Logf("collected %d findings (%d real, %d not real) from %d reviews, %d failed, stopped early: %v",
		len(obs), real, len(obs)-real, len(jobs), failed, stop)
	t.Logf("phase cost: $%.4f", after-before)
	if verifyOn {
		t.Logf("repository check ran in %d reviews: confirmed %d, refuted %d, uncertain %d, unchecked %d, withheld %d; %d reviews with findings did not run it",
			tally.reviews, tally.confirmed, tally.refuted, tally.uncertain, tally.unchecked, tally.withheld, skipped)
		if skipped > 0 {
			t.Errorf("%d reviews with findings never ran the repository check, so this arm is not the verify arm for them", skipped)
		}
	}

	raw, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(studyFile(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d bytes)", studyFile(), len(raw))
}

// studyCorpus names the fixtures a collection reviews. The default is the tuning, caller
// and info corpora with the held-out corpus left out; "heldout" is the held-out corpus
// alone, which can be spent once per question.
func studyCorpus(name string) ([]Fixture, error) {
	switch name {
	case "", "tuning":
		var out []Fixture
		seen := map[string]bool{}
		for _, f := range append(append(Fixtures(), CallerFixtures()...), InfoFixtures()...) {
			if seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			out = append(out, f)
		}
		return out, nil
	case "heldout":
		return HeldOutFixtures(), nil
	}
	return nil, fmt.Errorf("corpus %q is not tuning or heldout", name)
}

// studyGuardHeldOut refuses to start a collection on the held-out corpus unless the
// operator has said they mean to spend it and named a file that does not exist yet.
// Spent data cannot be un-spent, and a second run over the same fixtures would either
// overwrite the first or add a second look at data a result was already read from.
func studyGuardHeldOut(corpus, spend, file string) error {
	if corpus != "heldout" {
		return nil
	}
	if spend != "1" {
		return errors.New("the held-out corpus can be spent once: set NITPICK_JEV_STUDY_SPEND_HELDOUT=1 to say so")
	}
	if file == "" {
		return errors.New("a held-out collection needs NITPICK_JEV_STUDY_FILE, so it cannot overwrite the default file")
	}
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("%s already exists: the held-out corpus has been spent into it", file)
	}
	return nil
}

// studyTune is the configuration of an arm. Jev is enabled and withholds nothing, so
// every finding is scored and kept; the verify arm also turns the repository check on,
// so the decision model is shown what the check read.
func studyTune(verifyOn bool) func(*config.Config) {
	return func(c *config.Config) {
		c.Review.Jev.Threshold = 1e-9
		if verifyOn {
			c.Review.Verify = config.VerifyNotRefuted
		}
	}
}

// studyJoin turns one finished review into observations: every finding the engine
// filed or withheld that the decision model was asked about, labelled real or not by
// ScoreRun and carrying the state it was shown. A finding with no recorded state is
// left out, and the count of such findings is the caller's to check against the
// recorded states, since a silent drop here would shrink the sample with nothing to
// say so.
func studyJoin(model string, f Fixture, run int, result RunResult, rec *studyRecorder, arm string, verified bool) []studyObservation {
	all := append(append([]review.Finding{}, result.Report.Findings...), overruledFindings(result.Report)...)
	// ScoreRun reads only the findings the report published, so a finding the engine
	// withheld would never appear among its unmatched ones and would be labelled real.
	// Scoring a copy of the report that holds every finding gives each one a label.
	everything := result
	whole := *result.Report
	whole.Findings = all
	everything.Report = &whole
	score := ScoreRun(everything, f)
	unmatched := map[string]bool{}
	for _, u := range score.Unmatched {
		unmatched[studyID(u.Path, u.Line, u.Title)] = true
	}
	byState := map[string]studyObservation{}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for i, state := range rec.states {
		m := studyStateHead.FindStringSubmatch(state)
		if m == nil {
			continue
		}
		line, _ := strconv.Atoi(m[2])
		byState[studyID(m[1], line, m[3])] = studyObservation{State: state,
			Jev:     map[string]float64{"jev": rec.scores[i].Defect()},
			Support: map[string]float64{"jev": 1 - rec.scores[i][jev.Noise]}}
	}
	refuted := map[string]bool{}
	for _, w := range result.Report.Overruled {
		if w.Expert == verifyExpertName {
			refuted[studyID(w.Finding.Path, w.Finding.Line, w.Finding.Title)] = true
		}
	}
	var out []studyObservation
	for _, finding := range all {
		// The harness turns linters off, so none reaches here and this guard is not
		// exercised by a test; it stays so that turning them on does not send an
		// analyzer finding to a decision model that was never asked about it.
		if finding.FromAnalyzer {
			continue
		}
		id := studyID(finding.Path, finding.Line, finding.Title)
		o, found := byState[id]
		switch {
		case found && verified:
			o.Verify = "kept"
		case refuted[id]:
			// The repository check withheld it before the decision model saw it, so it
			// has no Jev score and still has a label and a verdict.
			o = studyObservation{Jev: map[string]float64{}, Support: map[string]float64{}, Verify: "refuted"}
		case !found:
			continue
		}
		o.Model, o.Fixture, o.Run, o.Arm = model, f.Name, run, arm
		o.Path, o.Line, o.Title = finding.Path, finding.Line, finding.Title
		o.Real = !unmatched[id]
		o.Confidence = 0.5
		if finding.Confidence != nil {
			o.Confidence = *finding.Confidence
		}
		o.Severity = studySeverity(finding.Severity)
		out = append(out, o)
	}
	return out
}

func studyFile() string {
	if p := os.Getenv("NITPICK_JEV_STUDY_FILE"); p != "" {
		return p
	}
	return os.TempDir() + "/nitpick-jev-study.json"
}

func studyLoad(t *testing.T) []studyObservation {
	t.Helper()
	raw, err := os.ReadFile(studyFile())
	if err != nil {
		t.Fatalf("read %s: %v (run TestJevStudyCollect first)", studyFile(), err)
	}
	var obs []studyObservation
	if err := json.Unmarshal(raw, &obs); err != nil {
		t.Fatal(err)
	}
	return obs
}

// studyJudgeSystem asks a general model the question Jev is asked, over the same state.
const studyJudgeSystem = `You check a code review comment against the code it is about.
"defect" is how likely the comment's claim is a real defect that breaks something for some caller, given the code shown, from 0 to 1.
Answer low when the code contradicts the claim, already handles it, or does not support it.`

type studyJudgeAnswer struct {
	Defect float64 `json:"defect"`
}

func studyJudgeSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["defect"],"properties":{"defect":{"type":"number","minimum":0,"maximum":1}}}`)
}

func TestJevStudyScore(t *testing.T) {
	if os.Getenv("NITPICK_JEV_STUDY") != "1" {
		t.Skip("set NITPICK_JEV_STUDY=1 to run the Jev study; it spends money")
	}
	key := studyAPIKey()
	capUSD := 25.0
	if v, err := strconv.ParseFloat(os.Getenv("NITPICK_JEV_STUDY_CAP_USD"), 64); err == nil && v > 0 {
		capUSD = v
	}
	ctx := t.Context()
	before, err := keyUsageUSD(ctx, key)
	if err != nil {
		t.Fatalf("read key usage: %v", err)
	}
	obs := studyLoad(t)
	client, err := jev.NewClient("", "", key)
	if err != nil {
		t.Fatal(err)
	}
	judgeModel := os.Getenv("NITPICK_JEV_STUDY_JUDGE")
	if judgeModel == "" {
		judgeModel = "deepseek/deepseek-v4.1-flash"
	}
	judge, err := llm.Build(config.ModelSpec{
		Provider: llm.ProviderOpenRouter, Model: judgeModel,
		Temperature: floatPtr(0), MaxTokens: 2048, Timeout: 3 * time.Minute, StructuredOutput: config.StructuredAuto,
	})
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}

	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	var mu sync.Mutex
	stop := false
	missing := map[string]int{}
	for i := range obs {
		wg.Add(1)
		go func(o *studyObservation) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			halted := stop
			mu.Unlock()
			if halted {
				return
			}
			if o.State == "" {
				return // withheld by the repository check, never shown to the decision model
			}
			variants := map[string]string{}
			if v, ok := studyWithout(o.State, "Whole file at the head commit:\n", ""); ok {
				variants["jev-nofile"] = v
			} else {
				mu.Lock()
				missing["file"]++
				mu.Unlock()
			}
			if v, ok := studyWithout(o.State, "Diff of the cited file:\n", "Whole file at the head commit:"); ok {
				variants["jev-nodiff"] = v
			} else {
				mu.Lock()
				missing["diff"]++
				mu.Unlock()
			}
			if cut, end := strings.Index(o.State, "Review comment to check."), strings.Index(o.State, "What an investigator found"); cut >= 0 && end > cut {
				variants["jev-claim"] = o.State[cut:end]
			} else {
				mu.Lock()
				missing["claim"]++
				mu.Unlock()
			}
			for name, state := range variants {
				scores, err := client.Classify(ctx, state)
				if err != nil {
					continue
				}
				mu.Lock()
				o.Jev[name] = scores.Defect()
				o.Support[name] = 1 - scores[jev.Noise]
				mu.Unlock()
			}
			if spent, err := keyUsageUSD(ctx, key); err == nil && spent-before > capUSD {
				mu.Lock()
				stop = true
				mu.Unlock()
			}
		}(&obs[i])
	}
	wg.Wait()
	for name, n := range missing {
		if n > 0 {
			t.Logf("WARNING: %d states had no %q section to remove; those findings are missing that variant", n, name)
		}
	}

	// The general model, scored on the full state.
	judged := 0
	for i := range obs {
		wg.Add(1)
		go func(o *studyObservation) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			halted := stop
			mu.Unlock()
			if halted {
				return
			}
			if o.State == "" {
				return
			}
			msgs := []llms.Message{
				{Role: llms.RoleSystem, Content: studyJudgeSystem},
				{Role: llms.RoleUser, Content: o.State},
			}
			got, err := llm.Extract[studyJudgeAnswer](ctx, judge, msgs, llms.WithJSONSchema("verdict", studyJudgeSchema(), true))
			if err != nil {
				return
			}
			mu.Lock()
			o.Jev["judge"] = got.Defect
			judged++
			mu.Unlock()
		}(&obs[i])
	}
	wg.Wait()
	t.Logf("judge %s scored %d of %d findings", judgeModel, judged, len(obs))

	after, _ := keyUsageUSD(ctx, key)
	t.Logf("phase cost: $%.4f, stopped early: %v", after-before, stop)
	raw, _ := json.Marshal(obs)
	if err := os.WriteFile(studyFile(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---- analysis, offline ----

type studyScored struct {
	real  bool
	score float64
	group string // fixture, for the split
}

func studyAUC(xs []studyScored) float64 {
	var pos, neg []float64
	for _, x := range xs {
		if x.real {
			pos = append(pos, x.score)
		} else {
			neg = append(neg, x.score)
		}
	}
	if len(pos) == 0 || len(neg) == 0 {
		return 0.5
	}
	wins := 0.0
	for _, p := range pos {
		for _, n := range neg {
			switch {
			case p > n:
				wins++
			case p == n:
				wins += 0.5
			}
		}
	}
	return wins / float64(len(pos)*len(neg))
}

// threshold95 is the highest threshold that keeps at least 95% of the real findings.
func studyThreshold95(xs []studyScored) float64 {
	var pos []float64
	for _, x := range xs {
		if x.real {
			pos = append(pos, x.score)
		}
	}
	if len(pos) == 0 {
		return 0
	}
	sort.Float64s(pos)
	cut := int(float64(len(pos)) * 0.05)
	return pos[cut]
}

// withheld is the share of not-real findings under a threshold, and the share of real
// findings kept at it.
func studyWithheldAt(xs []studyScored, threshold float64) (wh, kept float64) {
	var neg, negUnder, pos, posKept float64
	for _, x := range xs {
		if x.real {
			pos++
			if x.score >= threshold {
				posKept++
			}
		} else {
			neg++
			if x.score < threshold {
				negUnder++
			}
		}
	}
	if neg > 0 {
		wh = negUnder / neg
	}
	if pos > 0 {
		kept = posKept / pos
	}
	return wh, kept
}

// studySerious is the set of fixtures whose planted defects are all warning or above:
// the ones where Jev's bug-plus-error score and the label mean the same thing.
func studySerious() map[string]bool {
	out := map[string]bool{}
	var all []Fixture
	all = append(append(append(all, Fixtures()...), CallerFixtures()...), InfoFixtures()...)
	for _, f := range all {
		serious := len(f.Defects) > 0
		for _, d := range f.Defects {
			switch d.WantSeverity {
			case config.SeverityNit, config.SeverityInfo:
				serious = false
			}
		}
		// A clean fixture has no plant to be wrong about, so its findings are all not real
		// and it joins the stratum, which keeps the not-real count from shrinking to nothing.
		if serious || len(f.Defects) == 0 {
			out[f.Name] = true
		}
	}
	return out
}

// studyValue is the score a method gives one observation, and whether it gave one. The
// score is rounded to six places: Jev's bug plus error is a sum of two probabilities, so
// 0.62 and 0.32 give 0.9400000000000001, and a threshold written as 0.94 would otherwise keep
// some findings scored 0.94 and withhold others.
func studyValue(o studyObservation, m string) (float64, bool) {
	v, ok := studyRawValue(o, m)
	return math.Round(v*1e6) / 1e6, ok
}

func studyRawValue(o studyObservation, m string) (float64, bool) {
	switch m {
	case "confidence":
		return o.Confidence, true
	case "severity":
		return o.Severity, true
	case "verify":
		switch o.Verify {
		case "kept":
			return 1, true
		case "refuted":
			return 0, true
		}
		return 0, false
	case "jev+conf":
		v, ok := o.Jev["jev"]
		return (v + o.Confidence) / 2, ok
	case "support:jev", "support:jev-claim", "support:jev-nofile", "support:jev-nodiff":
		v, ok := o.Support[strings.TrimPrefix(m, "support:")]
		return v, ok
	case "support+conf":
		v, ok := o.Support["jev"]
		return (v + o.Confidence) / 2, ok
	}
	v, ok := o.Jev[m]
	return v, ok
}

func studyTable(t *testing.T, title string, obs []studyObservation, methods []string, rng *rand.Rand) {
	t.Helper()
	real := 0
	for _, o := range obs {
		if o.Real {
			real++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s: %d findings, %d real, %d not real\n", title, len(obs), real, len(obs)-real)
	if len(obs)-real < 10 {
		b.WriteString("fewer than 10 not-real findings: every interval here is wider than the effects it could show\n")
	}
	b.WriteString("method               n    AUC    [95% interval]    @0.3 withheld/kept   W95 (all findings)\n")
	for _, m := range methods {
		var all []studyScored
		for _, o := range obs {
			if v, ok := studyValue(o, m); ok {
				all = append(all, studyScored{o.Real, v, o.Fixture})
			}
		}
		if len(all) == 0 {
			fmt.Fprintf(&b, "%-18s %4d  (no scores)\n", m, 0)
			continue
		}
		var aucs []float64
		for i := 0; i < 300; i++ {
			draw := make([]studyScored, len(all))
			for j := range draw {
				draw[j] = all[rng.IntN(len(all))]
			}
			aucs = append(aucs, studyAUC(draw))
		}
		sort.Float64s(aucs)
		wh3, kept3 := studyWithheldAt(all, 0.3)
		wh95, _ := studyWithheldAt(all, studyThreshold95(all))
		fmt.Fprintf(&b, "%-18s %4d  %.3f  [%.3f, %.3f]     %.3f / %.3f          %.3f\n", m, len(all), studyAUC(all), aucs[7], aucs[292], wh3, kept3, wh95)
	}
	t.Log(b.String())
}

func TestJevStudyAnalyseArms(t *testing.T) {
	if os.Getenv("NITPICK_JEV_STUDY") != "1" {
		t.Skip("set NITPICK_JEV_STUDY=1 to run the Jev study")
	}
	rng := rand.New(rand.NewPCG(9, 13))
	serious := studySerious()
	load := func(arm string) []studyObservation {
		raw, err := os.ReadFile(fmt.Sprintf("%s/jev-%s.json", os.Getenv("NITPICK_JEV_STUDY_DIR"), arm))
		if err != nil {
			t.Logf("arm %s: %v", arm, err)
			return nil
		}
		var obs []studyObservation
		if err := json.Unmarshal(raw, &obs); err != nil {
			t.Fatal(err)
		}
		return obs
	}
	plain, verify := load("plain"), load("verify")

	// A reviewer whose reviews all failed under verification leaves the verify arm with
	// fewer reviewers than the plain arm, and the arms are only comparable on the ones they
	// share. The plain arm is also shown restricted to them.
	shared := map[string]bool{}
	for _, o := range verify {
		shared[o.Model] = true
	}
	var plainShared []studyObservation
	for _, o := range plain {
		if shared[o.Model] {
			plainShared = append(plainShared, o)
		}
	}
	for _, o := range plain {
		if !shared[o.Model] {
			t.Logf("model %s has findings in the plain arm and none in the verify arm", o.Model)
			break
		}
	}

	basic := []string{"confidence", "severity", "jev", "support:jev", "support:jev-claim", "support:jev-nofile", "support:jev-nodiff", "judge", "support+conf", "jev+conf"}
	for _, set := range []struct {
		name string
		obs  []studyObservation
		with []string
	}{
		{"plain arm, three reviewers", plain, basic},
		{"plain arm, only the reviewers the verify arm kept", plainShared, basic},
		{"verify arm", verify, append([]string{"verify"}, basic...)},
	} {
		studyTable(t, "EXPLORATORY "+set.name+", all fixtures", set.obs, set.with, rng)
		var strata []studyObservation
		for _, o := range set.obs {
			if serious[o.Fixture] {
				strata = append(strata, o)
			}
		}
		studyTable(t, "EXPLORATORY "+set.name+", fixtures whose plants are all warning or above, and clean ones", strata, set.with, rng)
	}
}

func TestJevStudyAnalyse(t *testing.T) {
	if os.Getenv("NITPICK_JEV_STUDY") != "1" {
		t.Skip("set NITPICK_JEV_STUDY=1 to run the Jev study")
	}
	obs := studyLoad(t)
	rng := rand.New(rand.NewPCG(3, 5))
	real := 0
	for _, o := range obs {
		if o.Real {
			real++
		}
	}
	t.Logf("%d findings: %d real, %d not real", len(obs), real, len(obs)-real)
	if len(obs)-real < 10 {
		t.Logf("fewer than 10 not-real findings: every interval below is wider than the effects it could show")
	}

	methods := []string{"confidence", "severity", "jev", "jev-claim", "jev-nofile", "jev-nodiff", "judge", "jev+conf"}
	value := func(o studyObservation, m string) (float64, bool) {
		switch m {
		case "confidence":
			return o.Confidence, true
		case "severity":
			return o.Severity, true
		case "jev+conf":
			v, ok := o.Jev["jev"]
			return (v + o.Confidence) / 2, ok
		}
		v, ok := o.Jev[m]
		return v, ok
	}

	// Fixture-split: threshold from one half, W95 on the other.
	fixtures := map[string]bool{}
	for _, o := range obs {
		fixtures[o.Fixture] = true
	}
	names := make([]string, 0, len(fixtures))
	for f := range fixtures {
		names = append(names, f)
	}
	sort.Strings(names)
	half := map[string]bool{}
	for i, f := range rng.Perm(len(names)) {
		if i < len(names)/2 {
			half[names[f]] = true
		}
	}

	var b strings.Builder
	b.WriteString("\nmethod        n    AUC    thr95(train)  W95(test)  kept(test)  @0.3 withheld/kept\n")
	perMethod := map[string][]studyScored{}
	for _, m := range methods {
		var all, train, test []studyScored
		for _, o := range obs {
			v, ok := value(o, m)
			if !ok {
				continue
			}
			s := studyScored{o.Real, v, o.Fixture}
			all = append(all, s)
			if half[o.Fixture] {
				train = append(train, s)
			} else {
				test = append(test, s)
			}
		}
		perMethod[m] = all
		thr := studyThreshold95(train)
		wh, kept := studyWithheldAt(test, thr)
		wh3, kept3 := studyWithheldAt(all, 0.3)
		fmt.Fprintf(&b, "%-12s %4d  %.3f  %.3f         %.3f      %.3f       %.3f / %.3f\n", m, len(all), studyAUC(all), thr, wh, kept, wh3, kept3)
	}
	t.Log(b.String())

	// Paired AUC difference against jev, over findings both methods scored.
	b.Reset()
	b.WriteString("\nAUC(jev) minus AUC(method), paired bootstrap over findings\n")
	for _, m := range methods {
		if m == "jev" {
			continue
		}
		var pairs [][2]studyScored
		for _, o := range obs {
			a, okA := value(o, "jev")
			c, okC := value(o, m)
			if okA && okC {
				pairs = append(pairs, [2]studyScored{{o.Real, a, o.Fixture}, {o.Real, c, o.Fixture}})
			}
		}
		draw := func() float64 {
			var xs, ys []studyScored
			for range pairs {
				p := pairs[rng.IntN(len(pairs))]
				xs = append(xs, p[0])
				ys = append(ys, p[1])
			}
			return studyAUC(xs) - studyAUC(ys)
		}
		var diffs []float64
		for i := 0; i < 400; i++ {
			diffs = append(diffs, draw())
		}
		sort.Float64s(diffs)
		var xs, ys []studyScored
		for _, p := range pairs {
			xs = append(xs, p[0])
			ys = append(ys, p[1])
		}
		fmt.Fprintf(&b, "  vs %-12s %+.3f  [%+.3f, %+.3f]  n=%d\n", m, studyAUC(xs)-studyAUC(ys), diffs[10], diffs[389], len(pairs))
	}
	t.Log(b.String())
}
