package review

import (
	"encoding/json"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// The findings schema is hand-authored rather than derived from the Result
// struct by reflection.
//
// The SDK's SchemaFrom marks every exported field as required, with no way to
// opt out short of `json:"-"`. Applied to Finding that makes `suggestion`
// mandatory on every finding, while the review prompt tells the model
// suggestion is optional and should only be supplied when it can give exact
// replacement code. The wire contract wins that argument silently, so the
// model invents a suggestion for findings it has no fix for, and those get
// rendered as one-click ```suggestion blocks that replace real code with
// prose.
//
// Authoring the schema here also lets severity be a closed enum, which stops
// the model inventing severities that later degrade to info or, worse, to the
// "none" sentinel.
const (
	findingsSchemaName   = "review_findings"
	triageSchemaName     = "triage_result"
	validationSchemaName = "finding_validation"
)

// severityEnum is the closed set a model may return.
var severityEnum = []string{"nit", "info", "warning", "error", "critical"}

// findingProperties describes one finding. classes is the closed set the
// model may label a finding with: every class the configuration knows,
// less slop when the slop switch is off, so a model is never offered a
// class the operator did not ask for and FilterWith's relabelling of an
// unasked slop finding is a defence, not the ordinary path.
func findingProperties(classes []string) map[string]any {
	return map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "Repository-relative path, exactly as given in the diff.",
		},
		"line": map[string]any{
			"type":        "integer",
			"description": "Line number in the file AFTER the change, taken from the margin numbers shown. Prefer a line the diff added.",
		},
		"severity": map[string]any{
			"type":        "string",
			"enum":        severityEnum,
			"description": "How serious the defect is.",
		},
		"category": map[string]any{
			"type":        "string",
			"description": "Short human-readable grouping label shown to the reader.",
		},
		"class": map[string]any{
			"type":        "string",
			"enum":        classes,
			"description": "Which kind of problem this is. Pick the closest match; this drives which findings a repository publishes.",
		},
		"title": map[string]any{
			"type":        "string",
			"description": "One-line statement of the defect.",
		},
		"rationale": map[string]any{
			"type":        "string",
			"description": "The concrete consequence and the reasoning, in one to three sentences.",
		},
		"suggestion": map[string]any{
			"type": "string",
			"description": "OPTIONAL. Exact replacement code for the anchored line — or for lines line through fix_end_line when fix_end_line is given — with no placeholders and no prose. " +
				"Omit entirely unless the replacement is complete and correct as written.",
		},
		"fix_end_line": map[string]any{
			"type":        "integer",
			"description": "OPTIONAL. With suggestion: the last line, inclusive, that the suggestion replaces, when it replaces more than the anchored line. Every line from line to fix_end_line must be in the diff.",
		},
	}
}

// findingsSchema is the response schema for a review pass.
//
// Note what is absent from `required`: suggestion. That omission is the whole
// point of hand-authoring this.
func findingsSchema(classes []string) (json.RawMessage, error) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"findings": map[string]any{
				"type":        "array",
				"description": "Defects found. An empty array is a valid and common answer.",
				"items": map[string]any{
					"type":                 "object",
					"properties":           findingProperties(classes),
					"required":             []string{"path", "line", "severity", "category", "class", "title", "rationale"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"findings"},
		"additionalProperties": false,
	}

	return json.Marshal(schema)
}

// triageSchema is the response schema for the triage pass, which additionally
// returns the walkthrough summary.
func triageSchema(classes []string) (json.RawMessage, error) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			// Verdicts against the numbered list, not findings. Triage names
			// each one by the number it was shown, and the engine applies the
			// verdict to the finding it already holds. The finding never makes
			// the round trip, so its class, source, evidence and severity
			// provenance cannot be lost on the way back, which is what
			// re-emitting whole findings cost whenever triage used its
			// permission to reword or move a line.
			"findings": map[string]any{
				"type":        "array",
				"description": "Your verdict on each finding you are publishing, most severe first, naming it by its number in the list you were given.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"number":     map[string]any{"type": "integer", "description": "The finding's number in the list you were given. This is how it is identified; do not renumber."},
						"severity":   map[string]any{"type": "string", "enum": severityEnum, "description": "The level this finding should publish at."},
						"class":      map[string]any{"type": "string", "enum": classes, "description": "The kind of defect. The reviewer's own class is kept where it named one."},
						"line":       map[string]any{"type": "integer", "description": "Optional. The clearer line for this finding, when a merge moved the anchor. Omit to keep the reviewer's."},
						"title":      map[string]any{"type": "string", "description": "Optional. A clearer title. Omit to keep the reviewer's."},
						"rationale":  map[string]any{"type": "string", "description": "Optional. A clearer rationale. Omit to keep the reviewer's."},
						"suggestion": map[string]any{"type": "string", "description": "Optional. A replacement suggestion. Omit to keep the reviewer's."},
					},
					"required":             []string{"number", "severity", "class"},
					"additionalProperties": false,
				},
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Short walkthrough of the change for the pull request description.",
			},
			"acknowledgements": map[string]any{
				"type":        "array",
				"description": "Original model nits that only report successful assessment and allege no defect. Never analyzer findings, weak claims, verdicts, or merge participants. Retained as visible decisions.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"number": map[string]any{"type": "integer"},
						"quote":  map[string]any{"type": "string", "description": "Copy the entire original rationale exactly, not an excerpt."},
						"reason": map[string]any{"type": "string", "description": "Explain why the original title and rationale allege no defect, risk, missing coverage, or requested change."},
					},
					"required":             []string{"number", "quote", "reason"},
					"additionalProperties": false,
				},
			},
			"dropped": map[string]any{
				"type":        "array",
				"description": "Duplicate findings merged into another numbered entry. An entry without a valid verdict, merge or acknowledgement decision is restored unchanged.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"number":       map[string]any{"type": "integer", "description": "The finding's number in the list you were given."},
						"duplicate_of": map[string]any{"type": "integer", "description": "The number of the finding this one was merged into. Required for a duplicate merge."},
						"reason":       map[string]any{"type": "string", "description": "Why it duplicates a finding you kept. Unsupported or uncertain claims must be retained."},
					},
					// duplicate_of is NOT required, so a drop with no merge
					// target stays legal and rule 2 restores that finding. It
					// was absent from this schema entirely, under strict
					// enforcement, so the model could not emit it and every
					// Drop decoded with DuplicateOf zero: merging has never
					// run. Every test of it constructed the reply in Go and
					// skipped the schema.
					"required":             []string{"number", "reason"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"findings", "summary", "dropped", "acknowledgements"},
		"additionalProperties": false,
	}

	return json.Marshal(schema)
}

// validationSchema is the response schema for one expert validation.
//
// Hand-authored for the same reason as the two above, and this is the sharpest
// case of it: SchemaFrom marks every exported field required, which would make
// `revised_severity` mandatory on every verdict. A model forced to fill that
// field for a finding whose severity is already right invents a level, and the
// engine would then apply it, turning a schema convenience into silent
// severity churn on findings nobody disputed. Only verdict and reason are
// required.
//
// `reason` is required on every verdict, not just refutations, because it is
// the one field that makes a verdict auditable in the log. The engine still
// checks it rather than trusting the schema: JSON-mode providers do not enforce
// required either, and an empty reason on a refutation must not delete a
// finding.
func validationSchema() (json.RawMessage, error) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verdict": map[string]any{
				"type": "string",
				"enum": verdictEnum,
				"description": "confirmed when the claim holds, refuted when you can name why it is wrong, " +
					"severity when the defect is real but rated wrong, unresolved when you cannot " +
					"decide from what you were shown and can name what is missing.",
			},
			"reason": map[string]any{
				"type": "string",
				"description": "One or two sentences. For a refutation this is the specific reason the claim " +
					"is wrong; uncertainty is not a reason.",
			},
			"revised_severity": map[string]any{
				"type":        "string",
				"enum":        severityEnum,
				"description": "OPTIONAL. Only for the severity verdict: the level the demonstrated consequence supports.",
			},
			"cited": map[string]any{
				"type": "string",
				"description": "OPTIONAL. The bracketed id of the reference entry that decided your verdict, " +
					"when one did. Leave it empty otherwise; do not name an entry you were not shown.",
			},
		},
		"required":             []string{"verdict", "reason"},
		"additionalProperties": false,
	}

	return json.Marshal(schema)
}

// schemaOption renders a schema as a call option, or returns nil when the
// schema cannot be built (which would be a programming error here).
func schemaOption(name string, build func() (json.RawMessage, error)) (llms.CallOption, error) {
	schema, err := build()
	if err != nil {
		return nil, err
	}
	return llms.WithJSONSchema(name, schema, true), nil
}

// offeredClasses is the class enum for a configuration: every class, less
// slop unless review.slop is on.
func offeredClasses(slop bool) []string {
	all := config.ClassNames()
	if slop {
		return all
	}
	out := make([]string, 0, len(all))
	for _, c := range all {
		if c != string(config.ClassSlop) {
			out = append(out, c)
		}
	}
	return out
}
