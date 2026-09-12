// Package practices records engineering checks without conflating execution,
// findings, and evidence of conformity.
package practices

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
)

// State describes execution, independently of the findings a check produced.
type State string

// Check execution states.
const (
	Completed     State = "completed"
	Partial       State = "partial"
	Unavailable   State = "unavailable"
	Failed        State = "failed"
	NotSelected   State = "not_selected"
	NotApplicable State = "not_applicable"
)

// Instrument identifies how evidence was obtained.
type Instrument string

// Supported evidence instruments.
const (
	Deterministic Instrument = "deterministic"
	Model         Instrument = "model"
)

// TargetKind prevents commit and architectural findings from inventing file lines.
type TargetKind string

// Supported target kinds.
const (
	FileTarget   TargetKind = "file"
	CommitTarget TargetKind = "commit"
	TitleTarget  TargetKind = "pr_title"
	UnitTarget   TargetKind = "architecture_unit"
	SiteTarget   TargetKind = "declaration_site"
)

// Target identifies one unit of coverage or the subject of a finding.
type Target struct {
	Kind TargetKind `json:"kind"`
	ID   string     `json:"id"`
	Line int        `json:"line,omitempty"`
}

// Omission explains why a planned target was not examined.
type Omission struct {
	Target Target `json:"target"`
	Reason string `json:"reason"`
}

// Finding retains evidence and the policy decision independently of execution.
type Finding struct {
	Rule        string   `json:"rule"`
	Target      Target   `json:"target"`
	AlsoAt      []Target `json:"also_at,omitempty"`
	Title       string   `json:"title"`
	Rationale   string   `json:"rationale,omitempty"`
	Remedy      string   `json:"remedy,omitempty"`
	Severity    string   `json:"severity,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Blocking    bool     `json:"blocking"`
	Exception   string   `json:"exception,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Uncertainty string   `json:"uncertainty,omitempty"`
}

// ContextSpan records the exact supporting source supplied for a task.
type ContextSpan struct {
	Path string `json:"path"`
	bundle.SourceSpan
}

// DesignInteraction names a caller obligation assigned to a review task.
type DesignInteraction struct {
	Caller Target `json:"caller"`
	Callee Target `json:"callee"`
}

// DesignTask links a bounded design assessment to its source and context.
type DesignTask struct {
	ID string `json:"id"`
	// Package identifies the inventory unit, not a claim of package completion.
	Package      string              `json:"package,omitempty"`
	Interactions []DesignInteraction `json:"interactions,omitempty"`
	// Focus narrows the design question; whole Sources remain available for slop.
	Focus   []ContextSpan `json:"focus,omitempty"`
	Source  Target        `json:"source"`
	Purpose string        `json:"purpose"`
	Context []Target      `json:"context,omitempty"`
	// ContextSpans narrows named context files; absent paths are supplied whole.
	ContextSpans []ContextSpan `json:"context_spans,omitempty"`
	// Sources lists the primary source files intended for this task.
	Sources []Target `json:"sources,omitempty"`
	// Omitted records required source or graph context unavailable to the task.
	Omitted []Omission `json:"omitted,omitempty"`
	// SourceDigest is an opaque, builder-specific binding, not a model cache key.
	// PlanDesign hashes task metadata and source bytes; the legacy file adapter
	// hashes its rendered request. Compare only within the same builder.
	SourceDigest string `json:"source_digest,omitempty"`
}

// Decision retains a claim an expert withheld, together with the stated reason.
type Decision struct {
	Finding Finding `json:"finding"`
	Expert  string  `json:"expert"`
	Reason  string  `json:"reason"`
}

// ModelRun identifies the models selected for one batch and any fallback.
type ModelRun struct {
	Files    []string `json:"files"`
	Primary  string   `json:"primary"`
	Ensemble []string `json:"ensemble,omitempty"`
	Fallback string   `json:"fallback,omitempty"`
}

// Check pairs evidence with the exact targets an instrument examined.
type Check struct {
	ID            string       `json:"id"`
	Version       string       `json:"version"`
	Revision      string       `json:"revision,omitempty"`
	BaseRevision  string       `json:"base_revision,omitempty"`
	Instrument    Instrument   `json:"instrument"`
	Required      bool         `json:"required"`
	State         State        `json:"state"`
	Reason        string       `json:"reason,omitempty"`
	Planned       []Target     `json:"planned"`
	Examined      []Target     `json:"examined"`
	Omitted       []Omission   `json:"omitted,omitempty"`
	Findings      []Finding    `json:"findings"`
	Signals       []Finding    `json:"signals,omitempty"`
	Decisions     []Decision   `json:"decisions,omitempty"`
	FailedStages  []string     `json:"failed_stages,omitempty"`
	Limitations   []string     `json:"limitations,omitempty"`
	Tool          string       `json:"tool,omitempty"`
	PromptVersion string       `json:"prompt_version,omitempty"`
	ModelRuns     []ModelRun   `json:"model_runs,omitempty"`
	Tasks         []DesignTask `json:"tasks,omitempty"`
	Context       []Target     `json:"context,omitempty"`
	DurationMS    int64        `json:"duration_ms,omitempty"`
}

// ModelUsage records provider-reported tokens, a lower bound when SDK retries
// consume tokens without returning usage. It does not estimate a monetary cost.
type ModelUsage struct {
	Model            string `json:"model"`
	ReportedCalls    int    `json:"reported_calls"`
	UnreportedCalls  int    `json:"unreported_calls"`
	FailedCalls      int    `json:"failed_calls"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	CacheReadTokens  int    `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
	ReasoningTokens  int    `json:"reasoning_tokens"`
}

// SchemaVersion identifies reports whose design coverage requires bound source.
const SchemaVersion = 2

// Report is the versioned evidence for a selected engineering policy.
type Report struct {
	SchemaVersion int              `json:"schema_version"`
	Profile       string           `json:"profile"`
	Revision      string           `json:"revision"`
	PolicySource  string           `json:"policy_source"`
	PolicyDigest  string           `json:"policy_digest"`
	Checks        []Check          `json:"checks"`
	ModelUsage    []ModelUsage     `json:"model_usage,omitempty"`
	Design        *DesignInventory `json:"design,omitempty"`
	Excluded      []Omission       `json:"excluded,omitempty"`
}

// Problems lists incomplete or internally inconsistent evidence, preserving findings.
func (r Report) Problems() []string {
	var problems []string
	if r.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("unsupported report schema version %d; expected %d", r.SchemaVersion, SchemaVersion))
	}
	if r.Profile == "" || r.Revision == "" || r.PolicySource == "" || r.PolicyDigest == "" {
		problems = append(problems, "report lacks supported schema, scope or policy provenance")
	}
	seen := map[string]bool{}
	examined := 0
	for _, c := range r.Checks {
		bad := func(reason string) { problems = append(problems, c.ID+": "+reason) }
		if c.ID == "" || c.Version == "" || seen[c.ID] {
			bad("missing identity or duplicate check")
		}
		seen[c.ID] = true
		if c.Instrument != Deterministic && c.Instrument != Model {
			bad("unknown instrument")
		}
		planned := map[Target]bool{}
		for _, target := range c.Planned {
			if !target.valid() || planned[target] {
				bad("invalid or duplicate planned target")
			}
			planned[target] = true
		}
		read := map[Target]bool{}
		for _, target := range c.Examined {
			if !planned[target] || read[target] {
				bad("unplanned or duplicate examined target")
			}
			read[target] = true
		}
		evidence := map[Target]bool{}
		normalize := func(target Target) Target { target.Line = 0; return target }
		for target := range read {
			evidence[normalize(target)] = true
		}
		for _, target := range c.Context {
			evidence[normalize(target)] = true
		}
		spanEvidence := map[Target][]bundle.SourceSpan{}
		taskIDs := map[string]bool{}
		for _, task := range c.Tasks {
			if taskIDs[task.ID] {
				bad("duplicate design task")
			}
			taskIDs[task.ID] = true
			ranges, rangeErr := task.contextRanges()
			if rangeErr != nil {
				bad(rangeErr.Error())
			}
			sourceTargets := map[Target]bool{}
			for _, source := range task.Sources {
				if source.Kind != FileTarget || !source.valid() || sourceTargets[source] {
					bad("design task has invalid or duplicate source")
				}
				sourceTargets[source] = true
			}
			for _, focus := range task.Focus {
				if !sourceTargets[Target{Kind: FileTarget, ID: focus.Path}] || focus.Start < 1 || focus.End < focus.Start {
					bad("design focus lies outside its primary source")
				}
			}
			seenInteractions := map[DesignInteraction]bool{}
			for _, relation := range task.Interactions {
				caller := normalize(relation.Caller)
				if relation.Caller.Kind != FileTarget || !relation.Caller.valid() || !sourceTargets[caller] || !relation.Callee.valid() || (relation.Callee.Kind != FileTarget && relation.Callee.Kind != UnitTarget) || seenInteractions[relation] {
					bad("design task has invalid or duplicate caller obligation")
				}
				if relation.Callee.Kind == FileTarget && !sourceTargets[normalize(relation.Callee)] && !slices.Contains(task.Context, normalize(relation.Callee)) {
					bad("design task omits its declared callee context")
				}
				seenInteractions[relation] = true
			}
			if len(task.Sources) == 0 {
				bad("design task has no source scope")
			}
			if len(task.Sources) > 0 && !sourceTargets[task.Source] {
				bad("design task primary source is outside its source scope")
			}
			if !task.Source.valid() || task.Purpose == "" || !planned[Target{Kind: UnitTarget, ID: task.ID}] {
				bad("design task lacks a planned unit, source or purpose")
			}
			seenOmissions := map[Omission]bool{}
			for _, omission := range task.Omitted {
				if seenOmissions[omission] {
					bad("design task repeats an omission")
				}
				seenOmissions[omission] = true
				if !omission.Target.valid() || (omission.Target.Kind != FileTarget && omission.Target.Kind != UnitTarget) || strings.TrimSpace(omission.Reason) == "" {
					bad("design task has invalid omission target or reason")
				}
				if omission.Target.Kind == FileTarget && !slices.Contains(task.Sources, omission.Target) && !slices.Contains(task.Context, omission.Target) {
					bad("design task omits source outside its declared scope")
				}
			}
			if read[Target{Kind: UnitTarget, ID: task.ID}] {
				if len(task.Omitted) > 0 {
					bad("examined design task has omitted context")
				}
				if len(task.Sources) > 0 {
					digest, err := hex.DecodeString(task.SourceDigest)
					if err != nil || len(digest) != sha256.Size {
						bad("examined design task lacks a source digest")
					}
				}
				for _, source := range task.Sources {
					evidence[normalize(source)] = true
				}
				evidence[normalize(task.Source)] = true
				for _, target := range task.Context {
					if spans := ranges[target.ID]; len(spans) > 0 {
						key := normalize(target)
						spanEvidence[key] = append(spanEvidence[key], spans...)
						continue
					}
					evidence[normalize(target)] = true
				}
			}
		}
		for _, finding := range c.Findings {
			for _, target := range append([]Target{finding.Target}, finding.AlsoAt...) {
				if !target.valid() || (c.State == Completed && !evidence[normalize(target)] && !slices.ContainsFunc(spanEvidence[normalize(target)], func(span bundle.SourceSpan) bool { return target.Line >= span.Start && target.Line <= span.End })) {
					bad("finding has an invalid target or lacks examined evidence")
				}
			}
		}
		omitted := map[Target]bool{}
		for _, omission := range c.Omitted {
			if !planned[omission.Target] || read[omission.Target] || omitted[omission.Target] || strings.TrimSpace(omission.Reason) == "" {
				bad("invalid, duplicate or unexplained omission")
			}
			omitted[omission.Target] = true
		}
		switch c.State {
		case Completed:
			if len(read) == 0 || len(read) != len(planned) || len(c.Omitted) > 0 {
				bad("completed check did not examine its entire nonempty scope")
			}
			if c.ID != "snapshot" {
				examined += len(read)
			}
		case NotApplicable:
			if c.Reason == "" || len(planned) != 0 || len(read) != 0 || len(c.Findings) != 0 {
				bad("inapplicability requires an empty scope and an explanation")
			}
		case Partial, Unavailable, Failed, NotSelected:
			if c.Reason == "" {
				bad("unfinished check has no explanation")
			}
			if c.Required {
				bad("required check is " + string(c.State))
			}
		default:
			bad("unknown execution state")
		}
		if len(c.FailedStages) > 0 && (c.Required || c.State == Completed) {
			bad("stages did not complete: " + strings.Join(c.FailedStages, ", "))
		}
	}
	if examined == 0 {
		problems = append(problems, "no completed check examined an applicable target")
	}
	return problems
}

func (t Target) valid() bool {
	if t.ID == "" || t.Line < 0 {
		return false
	}
	switch t.Kind {
	case FileTarget, SiteTarget:
		return true
	case CommitTarget, TitleTarget, UnitTarget:
		return t.Line == 0
	default:
		return false
	}
}

// ExitCode prioritizes incomplete evidence while retaining all policy violations.
func (r Report) ExitCode() int {
	if len(r.Problems()) > 0 {
		return 2
	}
	for _, c := range r.Checks {
		for _, f := range c.Findings {
			if f.Blocking && f.Exception == "" && f.Uncertainty == "" {
				return 1
			}
		}
	}
	return 0
}

// Text renders coverage next to findings rather than turning silence into a grade.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Engineering practices (%s):\n", r.Profile)
	if r.PolicySource != "" {
		fmt.Fprintf(&b, "  Policy: %s\n", r.PolicySource)
	}
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "  %s: %s; %d/%d targets examined, %d findings\n", c.ID, c.State, len(c.Examined), len(c.Planned), len(c.Findings))
		if c.Reason != "" {
			fmt.Fprintf(&b, "    %s\n", c.Reason)
		}
		for _, limitation := range c.Limitations {
			fmt.Fprintf(&b, "    Scope limit: %s\n", limitation)
		}
		for _, signal := range c.Signals {
			fmt.Fprintf(&b, "    Advisory model signal (%s): %s\n", signal.Rule, signal.Title)
		}
		for _, decision := range c.Decisions {
			fmt.Fprintf(&b, "    Withheld by %s: %s (%s)\n", decision.Expert, decision.Finding.Title, decision.Reason)
		}
		for _, f := range c.Findings {
			disposition := "advisory"
			if f.Blocking && f.Uncertainty == "" {
				disposition = "policy violation"
			}
			if f.Exception != "" {
				disposition = "accepted exception: " + f.Exception
			}
			location := f.Target.ID
			if f.Target.Line > 0 {
				location += fmt.Sprintf(":%d", f.Target.Line)
			}
			fmt.Fprintf(&b, "    %s [%s; %s] %s: %s\n", f.Rule, f.Target.Kind, disposition, location, f.Title)
			if f.Uncertainty != "" {
				fmt.Fprintf(&b, "      Unresolved: %s\n", f.Uncertainty)
			}
			if f.Rationale != "" {
				fmt.Fprintf(&b, "      %s\n", f.Rationale)
			}
			if f.Remedy != "" {
				fmt.Fprintf(&b, "      Suggested correction: %s\n", f.Remedy)
			}
		}
	}
	if len(r.ModelUsage) > 0 {
		b.WriteString("Model usage is a provider-reported lower bound; SDK retries may add usage and no monetary tariff is assumed.\n")
		for _, usage := range r.ModelUsage {
			fmt.Fprintf(&b, "  %s: %d prompt tokens, %d completion tokens; %d calls with usage, %d without usage, %d failed\n", usage.Model, usage.PromptTokens, usage.CompletionTokens, usage.ReportedCalls, usage.UnreportedCalls, usage.FailedCalls)
		}
	}
	if r.Design != nil {
		for _, limitation := range r.Design.Limitations {
			fmt.Fprintf(&b, "Design scope: %s\n", limitation)
		}
		for _, problem := range r.Design.Errors {
			fmt.Fprintf(&b, "Design inventory unavailable: %s\n", problem)
		}
	}
	for _, excluded := range r.Excluded {
		fmt.Fprintf(&b, "Excluded: %s (%s)\n", excluded.Target.ID, excluded.Reason)
	}
	for _, reason := range r.Problems() {
		fmt.Fprintf(&b, "Incomplete: %s\n", reason)
	}
	return b.String()
}
