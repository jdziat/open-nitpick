package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// The wider pass, on `@open-nitpick improve`.
//
// config.GenerationLevel is normal because four eval runs found that asking
// one pass for defects and style together produced more findings, lower
// precision and more missed defects. Style therefore comes from a second
// generation pass, which internal/review/engine.go runs when the configured
// level is pedantic.
//
// Setting `nitpick: pedantic` in a repository's config would reach that pass,
// and would also put style findings on every push, which is the outcome the
// measurement argues against. This command reaches it for ONE pull request,
// when a person asks, and leaves the configured level alone.

// improveMaxListed bounds the summary block. A pedantic pass over a large
// change is the widest call pattern this tool has, and a comment nobody
// scrolls to the end of is a comment nobody reads.
const improveMaxListed = 40

// runImprove reviews the change again at pedantic scope with slop on, and
// posts what it found as one comment.
// repo is the checkout the diff's paths are relative to. Taken rather than read
// off gh.Checkout, which is set only when the root holds a .git. Empty, the
// resolver refuses and this pass fails loudly. Divergent and non-empty is the
// case worth avoiding: SelfModified would find no config under that root,
// answer false, and review under the change's own configuration.
func runImprove(ctx context.Context, gh *vcs.GitHub, repo string, cfg *config.Config, ref vcs.Ref, ev *converse.Event, log *slog.Logger) error {
	// No permission gate beyond the association check the caller already ran.
	// This command reads and comments; runFix refuses more because it writes.

	// A copy, mutated for this run only. Every field set here is a scalar, so
	// the shallow copy is sound, and the caller's config is what the next
	// ordinary review will use.
	icfg := *cfg
	applyImproveScope(&icfg)
	icfg.Review.ResolveSuperseded = false

	// This pass answers with one comment and no disposition. reviewEvent reads
	// the operator's config and fires for every publish, so leaving
	// review.approve on here would post an approval beside that comment on a
	// change clean at pedantic scope, a verdict this command has never given.
	icfg.Review.Approve.Enabled = false

	roles, err := llm.BuildRoles(&icfg)
	if err != nil {
		return err
	}

	// The wrapper implements Provider and nothing else, which is three
	// decisions rather than a convenience: without IncrementalDiffer the
	// engine reads the whole change instead of the increment, without
	// PriorReviewer it does not narrow to what moved, and without
	// ThreadResolver it closes nothing. A person asking for the wider pass
	// wants the whole change looked at, and wants no threads touched.
	held := &heldReview{inner: gh}

	// Retrieval, on the defect pass only. The style pass declines it inside
	// the engine: it re-reviews the same batches with a style prompt, and a
	// corpus about correctness defects handed to a style reviewer buys a
	// second embedding call per batch and nothing else.
	//
	// Fatal on a construction error, matching every other command: the
	// operator asked for retrieval, so a broken embedder is a configuration
	// answer rather than a review that quietly did less than it said.
	k, status, err := review.BuildKnowledge(ctx, &icfg, gh.Checkout, log)
	if err != nil {
		return fmt.Errorf("knowledge retrieval (%s): %w", status.Reason, err)
	}

	// The wider pass reads the same measured conventions, and gets the style
	// half of them. improve resolves its own policy just below, so the base
	// revision here is the one that resolution used.
	std, stdStatus := review.BuildStandards(ctx, &icfg, gh.Checkout,
		baseRevisionFor(ctx, gh, ref, log), log)

	engine := &review.Engine{
		Standards:       std,
		StandardsStatus: stdStatus,

		Config:    &icfg,
		Roles:     roles,
		Provider:  held,
		Log:       log,
		Knowledge: k,

		// Both fields, as newEngine wires them. Policy alone would record a
		// substitution and then review under the change's own models, because
		// withPolicy rebuilds the roles through Models and leaves the engine
		// untouched when it is nil.
		//
		// This pass is the widest of the three a mention can start. It is a
		// whole review, so review.ignore, min_severity, validation, the budgets
		// and instructions[].prompt all reach it, and no permission gate stands
		// in front of it.
		//
		// The scope is applied by the resolver rather than here, because
		// withPolicy replaces Config wholesale with whatever the resolver
		// returned. Scoping only the roles would leave a substituted policy
		// reviewing at the ordinary scope, which is a pass that says it was
		// pedantic and was not.
		Policy: improveScoped{&config.BasePolicy{RepoRoot: repo, Loaded: &icfg, Provider: gh}},
		Models: func(policy *config.Config) (*llm.Roles, error) { return llm.BuildRoles(policy) },
		// Linters stays nil. The analyzers are deterministic and the ordinary
		// review already ran them; a second run would spend time to publish
		// what is already on the pull request.
	}

	report, err := engine.Review(ctx, ref)
	if err != nil {
		return err
	}

	findings := report.Findings

	// An inline `improve` is about the file its thread is on.
	if ev.Inline && ev.Path != "" {
		var scoped []review.Finding
		for _, f := range findings {
			if f.Path == ev.Path {
				scoped = append(scoped, f)
			}
		}
		findings = scoped
	}

	// A failure to read what is already published is not a failure of the
	// pass: the worst outcome is repeating something already said, and losing
	// the whole answer to avoid that is the worse trade.
	prior, err := gh.PriorReview(ctx, ref)
	if err != nil {
		log.Warn("could not read the prior review, so this pass may repeat a published finding", "error", err)
	}
	findings = dropPublished(findings, prior)

	body := improveComment(findings, ev, len(report.Findings))
	if err := gh.CommentOnPullRequest(ctx, ref, body+"\n\n"+vcs.AnswerMarker); err != nil {
		return err
	}
	return gh.React(ctx, ref, ev.CommentID, ev.Inline, "+1")
}

// dropPublished removes findings the pull request already carries, by the
// fingerprint its comments are marked with.
//
// A nil prior review drops nothing, which is what a failed read should cost.
func dropPublished(findings []review.Finding, prior *vcs.PriorReview) []review.Finding {
	if prior == nil || len(prior.Comments) == 0 {
		return findings
	}
	seen := make(map[string]bool, len(prior.Comments))
	for _, c := range prior.Comments {
		seen[c.Fingerprint] = true
	}
	var out []review.Finding
	for _, f := range findings {
		if !seen[review.Fingerprint(f)] {
			out = append(out, f)
		}
	}
	return out
}

// improveComment renders the pass as one block.
//
// One comment rather than a thread per finding, and that is the point rather
// than a saving: a wall of nit threads beside the review's correctness
// findings is the dilution the generation scope exists to prevent, arriving
// one layer up.
func improveComment(findings []review.Finding, ev *converse.Event, generated int) string {
	var b strings.Builder

	scope := "this change"
	if ev.Inline && ev.Path != "" {
		scope = "`" + ev.Path + "`"
	}

	fmt.Fprintf(&b, "@%s here is the wider pass over %s: naming, documentation, structure, idiom and slop, ", ev.Author, scope)
	b.WriteString("the classes an ordinary review does not ask for.\n\n")

	if len(findings) == 0 {
		if generated > 0 {
			b.WriteString("Nothing new. Everything this pass found is already on the pull request.\n")
		} else {
			b.WriteString("Nothing to report.\n")
		}
		return b.String()
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Line < findings[j].Line
	})

	listed := findings
	if len(listed) > improveMaxListed {
		listed = listed[:improveMaxListed]
	}
	for _, f := range listed {
		fmt.Fprintf(&b, "- `%s:%d` **%s** (%s, %s)\n", f.Path, f.Line, f.Title, f.Severity, f.Class)
	}
	if omitted := len(findings) - len(listed); omitted > 0 {
		fmt.Fprintf(&b, "\n%d more not listed.\n", omitted)
	}

	b.WriteString("\nThese are advisory and are not on any thread, so nothing here blocks a merge ")
	b.WriteString("or has to be resolved. The next ordinary review is unchanged by this pass.\n")
	return b.String()
}

// heldReview is a Provider that answers reads from the forge and keeps the
// review instead of publishing it.
//
// It implements Provider and no optional interface, which is what makes the
// improve pass read the whole change and resolve nothing. See runImprove.
type heldReview struct {
	inner *vcs.GitHub
	held  vcs.Review
}

func (h *heldReview) PullRequest(ctx context.Context, ref vcs.Ref) (*vcs.PullRequest, error) {
	return h.inner.PullRequest(ctx, ref)
}

func (h *heldReview) Diff(ctx context.Context, ref vcs.Ref) ([]byte, error) {
	return h.inner.Diff(ctx, ref)
}

func (h *heldReview) FileContent(ctx context.Context, ref vcs.Ref, path string) ([]byte, error) {
	return h.inner.FileContent(ctx, ref, path)
}

func (h *heldReview) PublishReview(_ context.Context, _ vcs.Ref, r vcs.Review) error {
	h.held = r
	return nil
}

func (h *heldReview) Name() string { return h.inner.Name() + " (held)" }

// applyImproveScope widens a configuration to the pass improve asks for.
//
// Shared by the comment command above and `nitpick improve`, so the two
// cannot drift into meaning different things. The CLI overrides the level and
// the slop switch afterwards from its flags; everything here is what both
// forms agree on.
func applyImproveScope(cfg *config.Config) {
	cfg.Persona.Nitpick = config.NitpickPedantic
	cfg.Review.Slop = true

	// The floor, not the configured one: most of what this pass finds is a
	// nit, and a repository publishing at warning would otherwise get silence
	// back from a command it typed on purpose.
	cfg.Review.MinSeverity = config.SeverityNit
}

// runImproveCLI is the local form: the same pass on a checkout this tool does
// not post to. internal/evals drives the engine directly, so a pass reachable
// only by commenting on a pull request could not be scored against the corpus
// that argues for it existing separately.
func runImproveCLI(ctx context.Context, args []string) error {
	return reviewWithScope(ctx, "improve", args, applyImproveScope)
}

// improveScoped applies the improve scope to whatever policy is resolved.
//
// The engine replaces its whole Config with the resolver's answer, so a scope
// applied anywhere else is lost the moment a substitution happens, and the
// substitution happens on exactly the change a maintainer is most likely to
// type `improve` on: the one editing the configuration.
type improveScoped struct{ inner review.PolicyResolver }

func (i improveScoped) ResolvePolicy(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest,
	changed []string) (*config.Config, bool, error) {

	cfg, modified, err := i.inner.ResolvePolicy(ctx, ref, pr, changed)
	if cfg == nil {
		return cfg, modified, err
	}
	scoped := *cfg
	applyImproveScope(&scoped)
	scoped.Review.ResolveSuperseded = false
	scoped.Review.Approve.Enabled = false
	return &scoped, modified, err
}
