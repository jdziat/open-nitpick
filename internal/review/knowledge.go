package review

import (
	"context"
	"fmt"
	"strings"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/gomod"
	"github.com/jdziat/open-nitpick/internal/knowledge"
)

// Retrieved knowledge, attached to a batch.
//
// Per batch rather than per file: the entries are about the change, and
// repeating the same five under every file in a batch would spend the token
// budget on duplication and teach the model that the section is boilerplate.

// knowledgeSection renders the entries a batch should be judged against.
//
// The heading says what these are and what they are not. A model handed
// reference material beside a diff will otherwise report the reference as a
// finding, which is the failure this feature has to avoid to be worth having:
// a reviewer that invents defects out of a style guide is worse than one that
// misses them.
// knowledgeTokens estimates what a rendered section costs, with its framing.
//
// The heading and the disclaimer are counted, not just the entries: they are
// most of a one-entry section, and a budget that ignored them would let a
// section overrun the number an operator wrote by the size of the words that
// make it safe to read.
func knowledgeTokens(section string) int {
	if section == "" {
		return 0
	}
	return llms.DefaultTokenEstimator().EstimateTokens(section)
}

// fitKnowledge drops entries until the rendered section fits a token budget,
// least relevant first.
//
// Zero is unbounded, which is what shipped. The entries arrive best-first, so
// dropping from the end drops the least relevant, and a budget too small for
// even one entry yields no section rather than a heading with nothing under
// it: a disclaimer about reference material with no reference material is
// tokens spent on nothing.
func fitKnowledge(hits []knowledge.Hit, budget int) []knowledge.Hit {
	if budget <= 0 || len(hits) == 0 {
		return hits
	}
	for n := len(hits); n > 0; n-- {
		if knowledgeTokens(knowledgeSection(hits[:n])) <= budget {
			return hits[:n]
		}
	}
	return nil
}

func knowledgeSection(hits []knowledge.Hit) string {
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n#### Known patterns this change may touch\n\n")
	b.WriteString("Reference material, not findings. None of this was written about this " +
		"change: it is here because the change resembles it. Judge whether each applies " +
		"before reporting anything from it, and report nothing on the strength of this " +
		"section alone.\n\n")
	for _, h := range hits {
		fmt.Fprintf(&b, "##### %s\n\n%s\n\n",
			bundle.PromptSafe(h.Entry.Title),
			bundle.PromptSafe(h.Entry.Body))
		// Applicability where the entry states it. Rendered rather than
		// filtered on: nothing here knows the versions the change runs under,
		// and a filter fed a guess would silence an entry on the strength of
		// it. The model is already asked to judge whether an entry applies;
		// this is the sentence it judges with.
		if v := strings.TrimSpace(h.Entry.Versions); v != "" {
			fmt.Fprintf(&b, "Applies to: %s.\n\n", bundle.PromptSafe(v))
		}
		if len(h.Entry.Frameworks) > 0 {
			fmt.Fprintf(&b, "Frameworks: %s.\n\n", bundle.PromptSafe(strings.Join(h.Entry.Frameworks, ", ")))
		}
		fmt.Fprintf(&b, "Source: %s, read %s.\n\n",
			bundle.PromptSafe(h.Entry.Source),
			h.Entry.Checked.Format("2006-01-02"))
	}
	return b.String()
}

// retrieveKnowledge returns the entries closest to a batch, or nothing.
//
// Every failure here is nothing rather than an error. Retrieval is an addition
// to a review that worked without it, so an embedding provider that is down
// should cost the run its extra context and not the review, and the log says
// which happened.
func (e *Engine) retrieveKnowledge(ctx context.Context, b bundle.Batch, style bool) []knowledge.Hit {
	if e.Knowledge == nil {
		return nil
	}
	// The style pass used to retrieve nothing at all, because the corpus was
	// entirely about correctness and a second embedding call per batch bought
	// a style reviewer a set of defect rules. Now the classes decide: the
	// style pass gets style and maintainability entries and no others, and
	// declines only when the corpus has none, which is the corpus's answer
	// rather than a rule in the engine.
	started := time.Now()
	hits, err := e.Knowledge.ForBatch(ctx, b, e.knowledgeClasses(style),
		strings.EqualFold(strings.TrimSpace(e.Config.Review.KnowledgeQuery), "file"))
	if err != nil {
		// Still nothing rather than an error: the review worked before
		// retrieval existed and must survive its provider. The count is what
		// changed, so the report can say the arm did not run clean instead of
		// the log saying it once into a file nobody scores.
		e.log().Warn("knowledge retrieval failed; reviewing without it", "error", err)
		return nil
	}

	// What was retrieved and what it cost, per batch. #81 owns the in-flight
	// progress work and this does not compete with it: it is the existing
	// logger and the fields a results table needs, not a second mechanism.
	hits = fitKnowledge(hits, e.Config.Review.KnowledgeTokens)

	if len(hits) > 0 {
		// A different message from the Info record the caller writes. Two
		// records sharing one message and carrying different fields is a log
		// nobody can filter.
		e.log().Debug("knowledge retrieval detail",
			"entries", ids(hits),
			"scores", scores(hits),
			"stage", stage(style),
			"latency", time.Since(started),
			"tokens", knowledgeTokens(knowledgeSection(hits)))
	}
	return hits
}

func stage(style bool) string {
	if style {
		return "style"
	}
	return "defect"
}

// scores names how close each hit was, rounded, so a log line stays readable
// and a run that retrieved nothing relevant is visible as a row of small
// numbers rather than as five ids.
func scores(hits []knowledge.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, fmt.Sprintf("%.3f", h.Score))
	}
	return out
}

// KnowledgeRetriever is what the engine holds, so internal/review does not
// depend on how retrieval is configured.
type KnowledgeRetriever struct {
	R *knowledge.Retriever

	// RepoRoot is where the go.mod behind an entry's `applies:` clauses is
	// read from, resolved per batch to the module that owns the batch's files.
	// Empty reads none, and none keeps every entry.
	RepoRoot string

	// status is what construction settled: the model, the entry count and,
	// when retrieval never got as far as answering, why.
	status KnowledgeStatus
	counts counters
}

// Status is what retrieval did, for the report.
//
// A nil retriever is off rather than a missing answer: the review ran, and
// nobody asked retrieval for anything.
func (k *KnowledgeRetriever) Status() KnowledgeStatus {
	if k == nil {
		return KnowledgeStatus{State: KnowledgeOff}
	}
	out := k.status
	out.Queries, out.Failures = k.counts.read()
	if out.Failures > 0 && out.State == KnowledgeActive {
		out.State = KnowledgeFailed
		out.Reason = "the embedder refused one or more batches"
	}
	return out
}

// ForBatch retrieves against a batch's diffs.
//
// The query is the diff text rather than the whole file: a file is mostly
// unchanged code, and embedding it retrieves entries about the parts nobody
// touched.
func (k *KnowledgeRetriever) ForBatch(ctx context.Context, b bundle.Batch, classes map[config.Class]bool, perFile bool) ([]knowledge.Hit, error) {
	if k == nil || k.R == nil {
		return nil, nil
	}
	if perFile {
		return k.perFile(ctx, b, classes)
	}

	var q strings.Builder
	paths := make([]string, 0, len(b.Entries))
	for _, e := range b.Entries {
		if e.File == nil {
			continue
		}
		paths = append(paths, e.File.Path)
		q.WriteString(changedLines(e.File))
	}
	if q.Len() == 0 {
		return nil, nil
	}
	return k.retrieve(ctx, q.String(), knowledge.LanguagesOf(paths), classes, paths, "")
}

// knowledgeCorpus is every entry a targeted validation can cite, by id.
//
// The retriever's own entries rather than a fresh read of the corpus: those
// are the entries this run could have put in front of a reviewer, so an
// evidence id that resolves here resolves to the text the reviewer saw. Nil
// when retrieval is off or targeted validation is not asked for, which makes
// Validator.cited a no-op.
func (e *Engine) knowledgeCorpus() map[string]knowledge.Entry {
	if !e.Config.Validation.Targeted || e.Knowledge == nil || e.Knowledge.R == nil {
		return nil
	}
	out := make(map[string]knowledge.Entry, len(e.Knowledge.R.Entries))
	for _, entry := range e.Knowledge.R.Entries {
		out[entry.ID] = entry
	}
	return out
}

// PoolSize is how many entries this batch could have reached.
//
// The languages a batch resolves to, which is what the per-batch query uses.
// A per-file run asks smaller questions and so has smaller pools; this reports
// the widest one, which is the one that says whether Keep can bind at all.
func (k *KnowledgeRetriever) PoolSize(b bundle.Batch, classes map[config.Class]bool) int {
	if k == nil || k.R == nil {
		return 0
	}
	paths := make([]string, 0, len(b.Entries))
	for _, e := range b.Entries {
		if e.File != nil {
			paths = append(paths, e.File.Path)
		}
	}
	return k.R.PoolSize(knowledge.LanguagesOf(paths), classes, gomod.VersionsFor(k.RepoRoot, paths))
}

// perFile queries once per changed file and merges the results.
//
// One query per file rather than one per batch, because a batch's query is
// dominated by whichever file changed most: a two-line edit that is the whole
// reason retrieval would have helped contributes two lines to a query of four
// hundred, and the entry that would have caught it never reaches the
// candidates. The cost is one embedding call per file instead of one per
// batch, which is why it is not the default until something measures it.
func (k *KnowledgeRetriever) perFile(ctx context.Context, b bundle.Batch, classes map[config.Class]bool) ([]knowledge.Hit, error) {
	var (
		sets     [][]knowledge.Hit
		firstErr error
	)
	for _, e := range b.Entries {
		if e.File == nil {
			continue
		}
		q := changedLines(e.File)
		if q == "" {
			continue
		}
		hits, err := k.retrieve(ctx, q, knowledge.LanguagesOf([]string{e.File.Path}), classes,
			[]string{e.File.Path}, e.File.Path)
		if err != nil {
			// One file's failure is not the batch's. The others still have
			// something to say, and the counters already record that this run
			// did not retrieve clean.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sets = append(sets, hits)
	}
	if len(sets) == 0 {
		return nil, firstErr
	}
	return knowledge.Merge(sets, k.R.Keep), nil
}

// retrieve is one query, counted.
func (k *KnowledgeRetriever) retrieve(ctx context.Context, q string, langs map[string]bool, classes map[config.Class]bool, paths []string, path string) ([]knowledge.Hit, error) {
	k.counts.query()
	hits, err := k.R.Retrieve(ctx, q, langs, classes, gomod.VersionsFor(k.RepoRoot, paths))
	if err != nil {
		k.counts.failure()
		return nil, err
	}
	for i := range hits {
		hits[i].Path = path
	}
	return hits, nil
}

// changedLines is the query text for one file: what the change added or
// removed.
//
// The changed lines only. A hunk's context lines are code nobody touched, and
// embedding them retrieves entries about the parts of the file the change left
// alone.
func changedLines(f *diff.File) string {
	var q strings.Builder
	for i := range f.Hunks {
		for _, l := range f.Hunks[i].Lines {
			if l.Kind == diff.LineAdded || l.Kind == diff.LineRemoved {
				q.WriteString(l.Content)
				q.WriteString("\n")
			}
		}
	}
	return q.String()
}

// ids names the retrieved entries for the log, so a review that consulted the
// corpus can be checked against what it was shown.
func ids(hits []knowledge.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Entry.ID)
	}
	return out
}

// knowledgeClasses names the entries a pass can act on.
//
// The defect pass takes everything the review taxonomy calls a defect, plus
// contract and tests. It does NOT take style: a style rule handed to the
// defect reviewer is the dilution config.GenerationLevel exists to prevent,
// arriving as reference material instead of as a prompt.
//
// Slop rides with the defect pass and only when review.slop is on, because
// that is when slop is a class the reviewer may publish. Retrieving entries
// for a class the filter will drop spends a slot in the prompt on a finding
// that cannot survive.
func (e *Engine) knowledgeClasses(style bool) map[config.Class]bool {
	if style {
		return map[config.Class]bool{
			config.ClassStyle:           true,
			config.ClassMaintainability: true,
		}
	}
	out := map[config.Class]bool{
		config.ClassCorrectness: true,
		config.ClassConcurrency: true,
		config.ClassSecurity:    true,
		config.ClassResource:    true,
		config.ClassDataLoss:    true,
		config.ClassContract:    true,
		config.ClassTests:       true,
		// Maintainability is in both. It is the one class the defect pass and
		// the style pass both publish, so an entry about it is useful to
		// either and belongs to neither alone.
		config.ClassMaintainability: true,
	}
	if e.Config != nil && e.Config.Review.Slop {
		out[config.ClassSlop] = true
	}
	return out
}
