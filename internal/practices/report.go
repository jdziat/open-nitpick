// Package practices records engineering checks without conflating execution,
// findings, and evidence of conformity.
package practices

import (
	"fmt"
	"strings"
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

// DesignTask links a bounded design assessment to its source and context.
type DesignTask struct {
	ID      string   `json:"id"`
	Source  Target   `json:"source"`
	Purpose string   `json:"purpose"`
	Context []Target `json:"context,omitempty"`
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
	if r.SchemaVersion != 1 || r.Profile == "" || r.Revision == "" || r.PolicySource == "" || r.PolicyDigest == "" {
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
		for _, task := range c.Tasks {
			if !task.Source.valid() || task.Purpose == "" || !planned[Target{Kind: UnitTarget, ID: task.ID}] {
				bad("design task lacks a planned unit, source or purpose")
			}
			if read[Target{Kind: UnitTarget, ID: task.ID}] {
				evidence[normalize(task.Source)] = true
				for _, target := range task.Context {
					evidence[normalize(target)] = true
				}
			}
		}
		for _, finding := range c.Findings {
			for _, target := range append([]Target{finding.Target}, finding.AlsoAt...) {
				if !target.valid() || (c.State == Completed && !evidence[normalize(target)]) {
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
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "  %s: %s; %d/%d targets examined, %d findings\n", c.ID, c.State, len(c.Examined), len(c.Planned), len(c.Findings))
		if c.Reason != "" {
			fmt.Fprintf(&b, "    %s\n", c.Reason)
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
