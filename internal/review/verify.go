package review

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/prompt"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// The verification stage checks each model finding against the repository: a
// checker is shown the claim and the diff around the line it cites, may look up
// to verifyMaxLookups things with read_file and grep_repo, and then confirms,
// refutes or says it cannot tell. It exists because the stages that judge a
// finding from its text alone, severity, triage, expert validation and an
// evidence grade that could not look anything up, did not raise precision
// without losing recall. One that can look does (docs/remediation-mira.md, R2b).

// The verdicts a checker may submit. Confirmed and refuted are the words
// validate.go already uses for an expert; uncertain is this stage's own, and
// differs from an expert's "unresolved" in that it withholds under
// review.verify: confirmed.
const verdictUncertain = "uncertain"

const (
	// verifyMaxLookups bounds read_file and grep_repo calls per finding.
	verifyMaxLookups = 8

	// verifyWindow is how many diff lines either side of the cited one the
	// checker is shown first.
	verifyWindow = 30

	// verifyTimeout bounds one finding's whole check.
	verifyTimeout = 5 * time.Minute

	// verifyExpert is how a withheld finding is attributed to a reader.
	verifyExpert = "a repository check"

	// verifyEvidenceBytes bounds the lookups kept for a later stage. It is a
	// ceiling on what review.jev is sent about one finding, not on what the
	// checker may read.
	verifyEvidenceBytes = 9000
)

// verifyCallTimeout bounds one checker turn, and is a variable so a test can
// shorten it. OpenRouter answers a stalled DeepSeek request with HTTP 200 and a
// keepalive, so no status ever arrives and only a deadline of ours ends it. Held
// to the finding's five minutes, one stall delays the whole review by that long
// and fails the stage; held to this, it costs a turn.
var verifyCallTimeout = 90 * time.Second

// generateBounded asks for one turn under its own deadline and sends it again
// once if the turn timed out while the caller is still waiting: on that
// deadline, or on one the network reports, such as a DNS lookup that timed
// out. The SDK resends no DNS failure, and one ended a held-out deep review's
// agents on their first turn. A second timeout is returned as the error it is.
func generateBounded(ctx context.Context, llm interface {
	GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error)
}, msgs []llms.Message, opts []llms.CallOption, timeout time.Duration) (*llms.Response, error) {
	var err error
	for range 2 {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		var resp *llms.Response
		resp, err = llm.GenerateContent(callCtx, msgs, opts...)
		timedOut := callCtx.Err() != nil || networkTimeout(err)
		cancel()
		if err == nil || ctx.Err() != nil || !timedOut {
			return resp, err
		}
	}
	return nil, err
}

// networkTimeout reports an error the network stack marks as a timeout. A host
// that does not resolve is not one, and resending it would change nothing.
func networkTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// lookupTools are the two functions that read the repository, shared by the
// checker and the deep-review agents. grep_repo is offered only where a git
// checkout exists to search.
func lookupTools(canSearch bool) []llms.Tool {
	tools := []llms.Tool{toolDef("read_file",
		"Read a window of a file in the repository at the head of the pull request. Lines are numbered.",
		`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer"},"end_line":{"type":"integer"}},"required":["path"]}`)}
	if canSearch {
		tools = append(tools, toolDef("grep_repo",
			"Search the repository with an extended regular expression. Returns up to 30 matching lines as path:line:text.",
			`{"type":"object","properties":{"pattern":{"type":"string"},"path_glob":{"type":"string","description":"Optional pathspec such as '*.py' or 'src/'"}},"required":["pattern"]}`))
	}
	return tools
}

func toolDef(name, desc, params string) llms.Tool {
	return llms.Tool{Type: llms.ToolTypeFunction, Function: &llms.FunctionDefinition{
		Name: name, Description: desc, Parameters: json.RawMessage(params),
	}}
}

// verifyTools are the three functions the checker can call.
func verifyTools(canSearch bool) []llms.Tool {
	return append(lookupTools(canSearch), toolDef("submit_verdict",
		"Finish. confirmed: the code you read shows the claimed defect is real and reachable. refuted: the code you read contradicts the claim. uncertain: you could not establish either.",
		`{"type":"object","properties":{"verdict":{"type":"string","enum":["confirmed","refuted","uncertain"]},"reason":{"type":"string","description":"One sentence naming the file and line you decided on."}},"required":["verdict","reason"]}`))
}

// runLookup answers one read_file or grep_repo call. The second result is false
// for a name that is neither, so the caller decides what an unknown tool means.
func runLookup(ctx context.Context, tools *repoTools, name string, args map[string]any) (string, bool) {
	switch name {
	case "read_file":
		return tools.read(ctx, str(args["path"]), intArg(args["start_line"]), intArg(args["end_line"])), true
	case "grep_repo":
		return tools.grep(ctx, str(args["pattern"]), str(args["path_glob"])), true
	}
	return "", false
}

// lookupTurn joins reads from the leading lookup group before the caller
// appends their answers. A submission remains a boundary: later calls cannot
// run before its verdict or budget decision.
func lookupTurn(ctx context.Context, tools *repoTools, calls []llms.ToolCall, limit int) []string {
	n := 0
	for _, call := range calls {
		if n >= limit || call.Function == nil ||
			(call.Function.Name != "read_file" && call.Function.Name != "grep_repo") {
			break
		}
		n++
	}
	answers := make([]string, n)
	var workers sync.WaitGroup
	next := make(chan int, n)
	for i := range n {
		next <- i
	}
	close(next)
	for range min(4, n) {
		workers.Go(func() {
			for i := range next {
				args := map[string]any{}
				_ = json.Unmarshal([]byte(calls[i].Function.Arguments), &args)
				answers[i], _ = runLookup(ctx, tools, calls[i].Function.Name, args)
			}
		})
	}
	workers.Wait()
	return answers
}

// keepAfterVerify is the rule that turns a verdict into a decision. It lives
// here, in code, so moving the dial is a change a test can see. A verdict
// outside the vocabulary has not checked anything, so it keeps the finding.
func keepAfterVerify(mode config.VerifyMode, verdict string) bool {
	switch verdict {
	case verdictConfirmed:
		return true
	case verdictRefuted:
		return false
	case verdictUncertain:
		return mode != config.VerifyConfirmed
	}
	return true
}

type verifyOutcome struct {
	verdict string
	reason  string
	failed  string // set when the check could not run; the finding is kept
	// submitted requires a supported verdict; malformed replies must be retried.
	submitted bool
	lookups   []verdictLookup
	complete  bool

	// evidence is what the checker looked up, each call followed by its result,
	// for a later stage that judges the finding from the same facts. Empty when
	// it looked nothing up.
	evidence string
}

// verifyFindings checks the model findings in the list and returns what is
// kept, what was withheld with its reason, what the checker looked up for each
// finding it checked (keyed by findingKey), and a stage status when a check
// could not run.
//
// A check that errors keeps its finding: an outage costs precision, which the
// stage status shows, where dropping the finding would cost recall nobody is
// told about.
func (e *Engine) verifyFindings(ctx context.Context, ref vcs.Ref, findings []Finding, plan *bundle.Plan) ([]Finding, []Overruled, map[string]string, []StageStatus) {
	mode := e.Config.Review.Verify
	if mode == "" || len(findings) == 0 {
		return findings, nil, nil, nil
	}
	client := e.Roles.Validator()
	system, err := prompt.Verify()
	if client == nil || err != nil {
		e.log().Error("verification unavailable; publishing unchecked", "error", err)
		return findings, nil, nil, []StageStatus{{Stage: "verify", Reason: "unavailable"}}
	}
	tools := e.repositoryTools(ref)
	if !tools.canSearch() {
		e.log().Warn("verification has no git checkout to search; the checker can read files but not grep")
	}
	files := filesByPath(plan)

	var todo []int
	for i, f := range findings {
		if !f.FromAnalyzer {
			todo = append(todo, i)
		}
	}
	e.log().Info("verifying against the repository", "findings", len(todo), "of", len(findings))

	outcomes := make([]verifyOutcome, len(findings))
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(1, e.Config.Review.Concurrency))
	for _, i := range todo {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				outcomes[i] = verifyOutcome{failed: errorKind(ctx.Err())}
				return
			}
			outcomes[i] = e.verifyCached(ctx, ref, system, tools, findings[i], files[findings[i].Path])
			outcomes[i] = e.executeClaim(ctx, ref, findings[i], outcomes[i])
		}(i)
	}
	wg.Wait()

	var (
		kept       []Finding
		withheld   []Overruled
		unchecked  int
		failure    string
		confirmed  int
		refutedN   int
		uncertainN int
	)
	evidence := map[string]string{}
	for i, f := range findings {
		o := outcomes[i]
		if o.evidence != "" {
			evidence[findingKey(f)] = o.evidence
		}
		switch {
		case f.FromAnalyzer:
			kept = append(kept, f)
		case o.failed != "":
			unchecked++
			failure = o.failed
			kept = append(kept, f)
		default:
			switch o.verdict {
			case verdictConfirmed:
				confirmed++
			case verdictRefuted:
				refutedN++
			case verdictUncertain:
				uncertainN++
			}
			if keepAfterVerify(mode, o.verdict) {
				kept = append(kept, f)
				continue
			}
			reason := strings.TrimSpace(o.reason)
			if reason == "" {
				reason = "the repository does not show the claimed defect"
			}
			if o.verdict == verdictUncertain {
				reason = "could not be confirmed against the repository: " + reason
			}
			withheld = append(withheld, Overruled{Finding: f, Expert: verifyExpert, Reason: reason})
		}
	}
	e.log().Info("verification finished", "confirmed", confirmed, "refuted", refutedN,
		"uncertain", uncertainN, "unchecked", unchecked, "withheld", len(withheld))

	var stages []StageStatus
	if failure != "" {
		stages = append(stages, StageStatus{Stage: "verify", Reason: failure})
	}
	return kept, withheld, evidence, stages
}

// storedVerdict is the part of a check worth keeping: what the checker
// decided and the facts it looked up.
type storedVerdict struct {
	Verdict  string          `json:"verdict"`
	Reason   string          `json:"reason"`
	Evidence string          `json:"evidence,omitempty"`
	Lookups  []verdictLookup `json:"lookups,omitempty"`
	Complete bool            `json:"complete"`
}

type verdictLookup struct {
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Digest    string `json:"digest"`
}

func lookupDigest(result string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(bound(result))))
}

func (stored storedVerdict) matches(ctx context.Context, tools *repoTools) bool {
	if !stored.Complete || !validVerdict(stored.Verdict) {
		return false
	}
	failures := tools.failures.Load()
	for _, lookup := range stored.Lookups {
		args := map[string]any{}
		if json.Unmarshal([]byte(lookup.Arguments), &args) != nil {
			return false
		}
		result, ok := runLookup(ctx, tools, lookup.Tool, args)
		if !ok || lookupDigest(result) != lookup.Digest {
			return false
		}
	}
	return ctx.Err() == nil && failures == tools.failures.Load()
}

// verifyCached answers from an earlier run's check when the same finding is
// put to the same checker at the same head, and otherwise runs it. Only a
// check that ended in a submitted verdict is kept: a failure, or a checker that
// stopped without one, is exactly what a retry exists to repeat.
func (e *Engine) verifyCached(ctx context.Context, ref vcs.Ref, system string, tools *repoTools, f Finding, file *diff.File) verifyOutcome {
	client := e.Roles.Validator()
	identity, err := json.Marshal([]any{ref, tools.ref, tools.canSearch(), verifyRequest(f, file)})
	key := ""
	if err == nil {
		key = e.stageKeyFor("verify", client, system, string(identity), []string{f.Path}, verifyOptions(client, tools)...)
	}
	stored, reused := loadStageChecked[storedVerdict](e.progress, key, rankVerify, func(s storedVerdict) bool {
		return s.matches(ctx, tools)
	})
	if reused {
		e.log().Info("reusing completed verification", "path", f.Path, "line", f.Line)
		return verifyOutcome{verdict: stored.Verdict, reason: stored.Reason, evidence: stored.Evidence, submitted: true, complete: true, lookups: stored.Lookups}
	}
	outcome := e.verifyOne(ctx, system, tools, f, file)
	if outcome.failed == "" && outcome.submitted && outcome.complete {
		saveStage(e.progress, key, rankVerify, storedVerdict{Verdict: outcome.verdict, Reason: outcome.reason, Evidence: outcome.evidence, Lookups: outcome.lookups, Complete: true})
	}
	return outcome
}

func verifyOptions(client *llm.Client, tools *repoTools) []llms.CallOption {
	return append(client.NoReasoningOptions(),
		llms.WithTools(verifyTools(tools.canSearch())), llms.WithToolChoiceAuto())
}

func validVerdict(v string) bool {
	return v == verdictConfirmed || v == verdictRefuted || v == verdictUncertain
}

// findingKey names a finding across stages that reorder and drop them.
func findingKey(f Finding) string { return fmt.Sprintf("%s|%d|%s", f.Path, f.Line, f.Title) }

// verifyOne runs one finding's tool loop.
func (e *Engine) verifyOne(ctx context.Context, system string, tools *repoTools, f Finding, file *diff.File) verifyOutcome {
	ctx = llm.WithCallRole(ctx, "verification")
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	client := e.Roles.Validator()
	opts := verifyOptions(client, tools)

	msgs := []llms.Message{
		{Role: llms.RoleSystem, Content: system},
		{Role: llms.RoleUser, Content: verifyRequest(f, file)},
	}
	lookups := 0
	failures := tools.failures.Load()
	var trace []verdictLookup
	var seen strings.Builder
	// found is what each return carries, so no outcome can forget the lookups.
	found := func(o verifyOutcome) verifyOutcome {
		o.evidence = seen.String()
		o.lookups = trace
		o.complete = failures == tools.failures.Load() && ctx.Err() == nil
		return o
	}
	for range verifyMaxLookups + 3 {
		resp, err := generateBounded(ctx, client.LLM, msgs, opts, verifyCallTimeout)
		if err != nil {
			e.log().Warn("verification failed; keeping the finding", "path", f.Path, "line", f.Line, "error", err)
			return found(verifyOutcome{failed: errorKind(err)})
		}
		if len(resp.ToolCalls) == 0 {
			return found(verifyOutcome{verdict: verdictUncertain, reason: "the checker stopped without a verdict"})
		}
		msgs = append(msgs, llms.Message{Role: llms.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		answers := lookupTurn(ctx, tools, resp.ToolCalls, max(0, verifyMaxLookups-lookups))
		for i, call := range resp.ToolCalls {
			name, args := "", map[string]any{}
			if call.Function != nil {
				name = call.Function.Name
				_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
			}
			var result string
			switch name {
			case "submit_verdict":
				v, _ := args["verdict"].(string)
				reason, _ := args["reason"].(string)
				submitted := validVerdict(v)
				if !submitted {
					v = verdictUncertain
				}
				return found(verifyOutcome{verdict: v, reason: oneLine(reason), submitted: submitted})
			case "read_file", "grep_repo":
				lookups++
				if lookups > verifyMaxLookups {
					result = "the lookup budget is spent: call submit_verdict now"
				} else {
					if i < len(answers) {
						result = answers[i]
					} else {
						result, _ = runLookup(ctx, tools, name, args)
					}
					recordLookup(&seen, name, call.Function.Arguments, result)
					trace = append(trace, verdictLookup{Tool: name, Arguments: call.Function.Arguments, Digest: lookupDigest(result)})
				}
			default:
				result = "unknown tool " + name
			}
			msgs = append(msgs, llms.Message{Role: llms.RoleTool, ToolCallID: call.ID, Content: bound(result)})
		}
	}
	return found(verifyOutcome{verdict: verdictUncertain, reason: "the checker never submitted a verdict"})
}

// recordLookup appends one lookup and its result to the evidence and cuts the
// evidence back to what a later stage will read. The cut is from the end, so the
// first lookups, which are the ones that located the code, are the ones kept.
func recordLookup(b *strings.Builder, name, args, result string) {
	fmt.Fprintf(b, "%s %s\n%s\n\n", name, args, result)
	if b.Len() > verifyEvidenceBytes {
		cut := strings.ToValidUTF8(b.String()[:verifyEvidenceBytes], "")
		b.Reset()
		b.WriteString(cut)
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func intArg(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// verifyRequest shows the checker one finding and the diff around its line.
func verifyRequest(f Finding, file *diff.File) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Comment: %s:%d (%s)\nTitle: %s\nClaim: %s\n", f.Path, f.Line, f.Sev(),
		oneLineTitle(f.Title), defang(strings.TrimSpace(f.Rationale)))
	if c := strings.TrimSpace(f.Citation); c != "" {
		fmt.Fprintf(&b, "Cites: %s\n", defang(c))
	}
	if w := diffWindow(file, f.Line, verifyWindow); w != "" {
		fmt.Fprintf(&b, "Diff around the cited line, new-file line numbers in the margin:\n```\n%s```\n", defang(w))
	} else {
		b.WriteString("The diff for this file is not available; read it from the repository.\n")
	}
	return b.String()
}

// filesByPath indexes the plan's changed files for window lookup.
func filesByPath(plan *bundle.Plan) map[string]*diff.File {
	out := map[string]*diff.File{}
	if plan == nil {
		return out
	}
	for _, b := range plan.Batches {
		for _, entry := range b.Entries {
			if entry.File != nil {
				out[entry.File.Path] = entry.File
			}
		}
	}
	return out
}

// diffWindow renders the lines of a file's diff within radius lines of the one
// cited, removed lines included, numbered as the new file numbers them.
func diffWindow(file *diff.File, line, radius int) string {
	if file == nil {
		return ""
	}
	type row struct {
		new  int
		text string
	}
	var rows []row
	nearest, best := -1, int(^uint(0)>>1)
	for _, h := range file.Hunks {
		for _, l := range h.Lines {
			mark := " "
			switch l.Kind {
			case diff.LineAdded:
				mark = "+"
			case diff.LineRemoved:
				mark = "-"
			}
			rows = append(rows, row{new: l.NewLine, text: mark + l.Content})
			if l.NewLine > 0 {
				if d := abs(l.NewLine - line); d < best {
					best, nearest = d, len(rows)-1
				}
			}
		}
	}
	if nearest < 0 {
		return ""
	}
	lo, hi := max(0, nearest-radius), min(len(rows), nearest+radius+1)
	var b strings.Builder
	for _, r := range rows[lo:hi] {
		if r.new > 0 {
			fmt.Fprintf(&b, "%5d %s\n", r.new, r.text)
		} else {
			fmt.Fprintf(&b, "      %s\n", r.text)
		}
	}
	return b.String()
}
