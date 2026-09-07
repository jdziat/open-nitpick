package review

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/prompt"
)

// Routing.
//
// A review is a set of batches, and until now every batch went to the same
// model. The eval battery says models differ by what a change is: one is the
// quietest on cross-file contract changes and blind to info-level plants,
// another is strongest on single-file security defects and expensive. A route
// sends a batch to the model that measured best for it, by language, by file
// count, and (when a router is configured), by what the change does. An
// ensemble sends a batch to several models and lets triage merge and rerank
// the pool.

// RouteDecision records where one batch went, for the report.
type RouteDecision struct {
	Files     []string
	Languages []string
	Kinds     []string
	Route     string   // route name, or "" for the default
	Reviewer  string   // primary model
	Ensemble  []string // additional models
}

// reviewers is the set of clients that review one batch.
type reviewers struct {
	primary  *llm.Client
	extra    []*llm.Client
	decision RouteDecision
}

// reviewersFor chooses the clients for a batch.
func (e *Engine) reviewersFor(ctx context.Context, b bundle.Batch) (reviewers, error) {
	paths := b.Paths()
	d := RouteDecision{Files: paths, Languages: bundle.Languages(paths)}
	models := e.Config.Models

	var route *config.Route
	if len(models.Routes) > 0 {
		if models.NeedsRouter() {
			kinds, err := e.classify(ctx, b)
			if err != nil {
				// A router failure is not a batch failure. Every route runs
				// against an empty kind set and the report discloses it.
				e.log().Warn("router failed; batch reviewed unclassified", "files", paths, "error", err)
			}
			d.Kinds = kinds
		}
		for i := range models.Routes {
			if models.Routes[i].Match.Matches(d.Languages, d.Kinds, len(paths)) {
				route = &models.Routes[i]
				break
			}
		}
	}

	out := reviewers{primary: e.Roles.Review}
	if route != nil {
		d.Route = route.Name
		if d.Route == "" {
			d.Route = fmt.Sprintf("route %d", indexOfRoute(models.Routes, route))
		}
		c, err := e.Roles.For(models.ResolveRoute(*route))
		if err != nil {
			return out, fmt.Errorf("%s: %w", d.Route, err)
		}
		out.primary = c
	}
	for _, spec := range models.ResolveEnsemble(route) {
		c, err := e.Roles.For(spec)
		if err != nil {
			return out, fmt.Errorf("ensemble: %w", err)
		}
		if c == out.primary {
			continue
		}
		out.extra = append(out.extra, c)
		d.Ensemble = append(d.Ensemble, c.String())
	}
	d.Reviewer = out.primary.String()
	out.decision = d
	return out, nil
}

func indexOfRoute(routes []config.Route, r *config.Route) int {
	for i := range routes {
		if &routes[i] == r {
			return i
		}
	}
	return -1
}

// classification is the router's answer.
type classification struct {
	Kinds []string `json:"kinds"`
}

// classify asks the router what a batch does. The router sees the diff only
// (not the full files, not the related context), because the question is what
// the change is, and the diff is the change.
func (e *Engine) classify(ctx context.Context, b bundle.Batch) ([]string, error) {
	if e.Roles.Router == nil {
		return nil, fmt.Errorf("routes match on kinds and no router is configured")
	}
	p, err := prompt.Build(prompt.NameRoute, prompt.Options{})
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	body.WriteString("Classify the following change.\n\n")
	for _, entry := range b.Entries {
		body.WriteString(bundle.RenderDiffOnly(entry))
		body.WriteString("\n")
	}
	msgs := []llms.Message{
		{Role: llms.RoleSystem, Content: p.String()},
		{Role: llms.RoleUser, Content: body.String()},
	}
	schema, err := schemaOption("classification", func() (json.RawMessage, error) { return json.RawMessage(classificationSchema), nil })
	if err != nil {
		return nil, err
	}
	result, err := llm.Extract[classification](ctx, e.Roles.Router, msgs, schema)
	if err != nil {
		return nil, err
	}
	var kinds []string
	for _, k := range result.Kinds {
		k = strings.ToLower(strings.TrimSpace(k))
		for _, known := range config.Kinds() {
			if k == known {
				kinds = append(kinds, k)
				break
			}
		}
	}
	sort.Strings(kinds)
	return kinds, nil
}

const classificationSchema = `{
  "type": "object",
  "properties": {
    "kinds": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["kinds"],
  "additionalProperties": false
}`

// reviewWith runs every reviewer over a batch and pools their findings.
// Reviewers run concurrently; one failing does not lose the others, and the
// batch fails only when every reviewer did.
func (e *Engine) reviewWith(ctx context.Context, r reviewers, prContext string, b bundle.Batch) ([]Finding, error) {
	clients := append([]*llm.Client{r.primary}, r.extra...)

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		findings []Finding
		errs     []error
	)
	for _, c := range clients {
		wg.Add(1)
		go func(c *llm.Client) {
			defer wg.Done()
			base, err := e.reviewPromptFor(c)
			if err == nil {
				var out []Finding
				out, err = e.analyzeBatchWith(ctx, c, base, prContext, b)
				mu.Lock()
				findings = append(findings, out...)
				mu.Unlock()
			}
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", c, err))
				mu.Unlock()
				if len(clients) > 1 {
					e.log().Warn("one reviewer of an ensemble failed", "model", c.String(), "files", b.Paths(), "error", err)
				}
			}
		}(c)
	}
	wg.Wait()

	if len(errs) == len(clients) {
		if len(errs) == 1 {
			return nil, errs[0]
		}
		return nil, fmt.Errorf("every reviewer failed: %v", errs)
	}
	return findings, nil
}
