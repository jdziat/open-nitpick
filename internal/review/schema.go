package review

import (
	"encoding/json"

	llms "github.com/nocturnium/llm-go-sdk"

	"github.com/jdziat/open-nitpick/internal/config"
)

// The findings schema is hand-authored rather than derived from the Result
// struct by reflection.
//
// The SDK's SchemaFrom marks EVERY exported field as required, with no way to
// opt out short of `json:"-"`. Applied to Finding that makes `suggestion`
// mandatory on every finding — while the review prompt tells the model
// suggestion is optional and should only be supplied when it can give exact
// replacement code. The wire contract wins that argument silently, so the model
// invents a suggestion for findings it has no fix for, and those get rendered
// as one-click ```suggestion blocks that replace real code with prose.
//
// Authoring the schema here also lets severity be a closed enum, which stops
// the model inventing severities that later degrade to info or, worse, to the
// "none" sentinel.
const (
	findingsSchemaName = "review_findings"
	triageSchemaName   = "triage_result"
)

// severityEnum is the closed set a model may return.
var severityEnum = []string{"nit", "info", "warning", "error", "critical"}

// findingProperties describes one finding.
func findingProperties() map[string]any {
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
			"enum":        config.ClassNames(),
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
			"description": "OPTIONAL. Exact replacement code for the single anchored line, with no placeholders and no prose. " +
				"Omit entirely unless you can replace that one line correctly.",
		},
	}
}

// findingsSchema is the response schema for a review pass.
//
// Note what is absent from `required`: suggestion. That omission is the whole
// point of hand-authoring this.
func findingsSchema() (json.RawMessage, error) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"findings": map[string]any{
				"type":        "array",
				"description": "Defects found. An empty array is a valid and common answer.",
				"items": map[string]any{
					"type":                 "object",
					"properties":           findingProperties(),
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
func triageSchema() (json.RawMessage, error) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"findings": map[string]any{
				"type":        "array",
				"description": "The findings that should be published, most severe first.",
				"items": map[string]any{
					"type":                 "object",
					"properties":           findingProperties(),
					"required":             []string{"path", "line", "severity", "category", "class", "title", "rationale"},
					"additionalProperties": false,
				},
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Short walkthrough of the change for the pull request description.",
			},
		},
		"required":             []string{"findings", "summary"},
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
