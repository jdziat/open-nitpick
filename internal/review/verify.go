package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
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
)

// verifyCallTimeout bounds one checker turn, and is a variable so a test can
// shorten it. OpenRouter answers a stalled DeepSeek request with HTTP 200 and a
// keepalive, so no status ever arrives and only a deadline of ours ends it. Held
// to the finding's five minutes, one stall delays the whole review by that long
// and fails the stage; held to this, it costs a turn.
var verifyCallTimeout = 90 * time.Second

// generateBounded asks for one turn under its own deadline and sends it again
// once if that deadline, not the caller's, ended it. A second stall is
// returned as the error it is.
func generateBounded(ctx context.Context, llm interface {
	GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error)
}, msgs []llms.Message, opts []llms.CallOption, timeout time.Duration) (*llms.Response, error) {
	var err error
	for range 2 {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		var resp *llms.Response
		resp, err = llm.GenerateContent(callCtx, msgs, opts...)
		stalled := callCtx.Err() != nil && ctx.Err() == nil
		cancel()
		if err == nil || !stalled {
			return resp, err
		}
	}
	return nil, err
}

// lookupTools are the two functions that read the repository, shared by the
// checker and by a reviewer that investigates. grep_repo is offered only where a
// git checkout exists to search.
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

// runLookup answers one read_file or grep_repo call, counting it against the
// budget; finish names the tool a spent budget points the model at. The second
// result is false for a name that is neither, so the caller decides what an
// unknown tool means.
func runLookup(ctx context.Context, tools *repoTools, name string, args map[string]any, lookups *int, budget int, finish string) (string, bool) {
	if name != "read_file" && name != "grep_repo" {
		return "", false
	}
	*lookups++
	switch {
	case *lookups > budget:
		return "the lookup budget is spent: call " + finish + " now", true
	case name == "read_file":
		return tools.read(ctx, str(args["path"]), intArg(args["start_line"]), intArg(args["end_line"])), true
	}
	return tools.grep(ctx, str(args["pattern"]), str(args["path_glob"])), true
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
}

// verifyFindings checks the model findings in the list and returns what is
// kept, what was withheld with its reason, and a stage status when a check
// could not run.
//
// A check that errors keeps its finding: an outage costs precision, which the
// stage status shows, where dropping the finding would cost recall nobody is
// told about.
func (e *Engine) verifyFindings(ctx context.Context, ref vcs.Ref, findings []Finding, plan *bundle.Plan) ([]Finding, []Overruled, []StageStatus) {
	mode := e.Config.Review.Verify
	if mode == "" || len(findings) == 0 {
		return findings, nil, nil
	}
	client := e.Roles.Validator()
	system, err := prompt.Verify()
	if client == nil || err != nil {
		e.log().Error("verification unavailable; publishing unchecked", "error", err)
		return findings, nil, []StageStatus{{Stage: "verify", Reason: "unavailable"}}
	}
	tools := newRepoTools(e.RepoRoot, e.Provider, ref)
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
			outcomes[i] = e.verifyOne(ctx, system, tools, findings[i], files[findings[i].Path])
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
	for i, f := range findings {
		o := outcomes[i]
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
	return kept, withheld, stages
}

// verifyOne runs one finding's tool loop.
func (e *Engine) verifyOne(ctx context.Context, system string, tools *repoTools, f Finding, file *diff.File) verifyOutcome {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	client := e.Roles.Validator()
	opts := append(client.NoReasoningOptions(),
		llms.WithTools(verifyTools(tools.canSearch())), llms.WithToolChoiceAuto())

	msgs := []llms.Message{
		{Role: llms.RoleSystem, Content: system},
		{Role: llms.RoleUser, Content: verifyRequest(f, file)},
	}
	lookups := 0
	for range verifyMaxLookups + 3 {
		resp, err := generateBounded(ctx, client.LLM, msgs, opts, verifyCallTimeout)
		if err != nil {
			e.log().Warn("verification failed; keeping the finding", "path", f.Path, "line", f.Line, "error", err)
			return verifyOutcome{failed: errorKind(err)}
		}
		if len(resp.ToolCalls) == 0 {
			return verifyOutcome{verdict: verdictUncertain, reason: "the checker stopped without a verdict"}
		}
		msgs = append(msgs, llms.Message{Role: llms.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, call := range resp.ToolCalls {
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
				if v != verdictConfirmed && v != verdictRefuted && v != verdictUncertain {
					v = verdictUncertain
				}
				return verifyOutcome{verdict: v, reason: oneLine(reason)}
			default:
				var ok bool
				if result, ok = runLookup(ctx, tools, name, args, &lookups, verifyMaxLookups, "submit_verdict"); !ok {
					result = "unknown tool " + name
				}
			}
			msgs = append(msgs, llms.Message{Role: llms.RoleTool, ToolCallID: call.ID, Content: bound(result)})
		}
	}
	return verifyOutcome{verdict: verdictUncertain, reason: "the checker never submitted a verdict"}
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
