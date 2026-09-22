package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/fence"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/prompt"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// residualJudgment is the triage model's answer for near-clean approve.
type residualJudgment struct {
	Approve bool   `json:"approve"`
	Reason  string `json:"reason"`
}

const residualApproveSchema = `{
  "type": "object",
  "properties": {
    "approve": {"type": "boolean"},
    "reason": {"type": "string"}
  },
  "required": ["approve", "reason"],
  "additionalProperties": false
}`

// judgeResidualApprove asks the triage model whether residual findings still
// allow APPROVE. Failures leave ResidualApprove false (comment).
func (e *Engine) judgeResidualApprove(ctx context.Context, report *Report) {
	if report == nil || e.Config == nil || !residualJudgeEligible(report, e.Config) {
		return
	}
	// Standing threads need a ThreadResolver after a yes; without one the
	// judge cannot earn APPROVE, so skip the model call.
	if report.PriorComments > len(report.Superseded) {
		if _, ok := e.Provider.(vcs.ThreadResolver); !ok {
			return
		}
	}
	if e.Roles == nil || e.Roles.Triage == nil {
		return
	}

	p, err := prompt.Build(prompt.NameApproveResidual, prompt.Options{
		PersonaText: prompt.Persona(e.Config.Persona),
	})
	if err != nil {
		e.log().Warn("residual approve prompt failed; publishing as comment", "error", err)
		return
	}
	msgs := []llms.Message{
		{Role: llms.RoleSystem, Content: p.String()},
		{Role: llms.RoleUser, Content: renderForResidualJudge(e.Config, report.Findings)},
	}
	schema, err := schemaOption("residual_approve", func() (json.RawMessage, error) {
		return json.RawMessage(residualApproveSchema), nil
	})
	if err != nil {
		e.log().Warn("residual approve schema failed; publishing as comment", "error", err)
		return
	}
	result, err := llm.Extract[residualJudgment](ctx, e.Roles.Triage, msgs, schema)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		e.log().Warn("residual approve judge failed; publishing as comment", "error", err)
		return
	}
	if !result.Approve {
		e.log().Info("residual approve refused", "reason", result.Reason)
		return
	}
	report.ResidualApprove = true
	report.ResidualReason = strings.TrimSpace(result.Reason)
	e.log().Info("residual approve granted", "reason", report.ResidualReason, "findings", len(report.Findings))
}

func renderForResidualJudge(cfg *config.Config, findings []Finding) string {
	var b strings.Builder
	nitpick := config.NitpickNormal
	if cfg != nil {
		nitpick = cfg.Persona.Nitpick
	}
	max := config.ResidualMaxSeverityDefault
	if cfg != nil {
		max = residualMaxSeverity(cfg)
	}
	fmt.Fprintf(&b, "Nitpick level: %s\nResidual max severity: %s\n\nFindings:\n", nitpick, max)
	for i, f := range findings {
		fmt.Fprintf(&b, "%d. [%s/%s] %s:%d  %s\n", i+1, residualJudgeField(f.Severity), residualJudgeField(f.Class), residualJudgeField(f.Path), f.Line, residualJudgeField(f.Title))
		if r := strings.TrimSpace(f.Rationale); r != "" {
			runes := []rune(r)
			if len(runes) > 240 {
				r = string(runes[:240]) + "..."
			}
			fmt.Fprintf(&b, "   %s\n", residualJudgeField(r))
		}
	}
	return b.String()
}

func residualJudgeField(s string) string {
	return fence.Defang(strings.ReplaceAll(strings.TrimSpace(s), "\n", " "))
}
