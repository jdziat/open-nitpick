package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type callerKey struct{}

// mcpHandler serves the insights tools over streamable HTTP, so an agent can
// ask the server where the bugs are and report a review it ran.
//
// It is stateless on purpose. Each request is authenticated by its own bearer
// token and gets a server built for that caller, so what a caller may do never
// depends on state left by an earlier request, and a read token has no
// submit_review tool to find.
func (c *Component) mcpHandler() http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		who, _ := r.Context().Value(callerKey{}).(caller)
		return c.mcpServer(who)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := c.need(w, r, false)
		if !ok {
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, who)))
	})
}

// Window is what every read tool takes.
type Window struct {
	Repo string `json:"repo,omitempty" jsonschema:"limit to one repository, as the repo field of a review names it"`
	Days int    `json:"days,omitempty" jsonschema:"how many days back to look, 1 to 365; default 30"`
}

// HotspotsInput is the input of list_hotspots.
type HotspotsInput struct {
	Window
	Limit int `json:"limit,omitempty" jsonschema:"most files to return, 1 to 500; default 20"`
}

// FindingsInput is the input of list_findings.
type FindingsInput struct {
	Window
	Path        string `json:"path,omitempty" jsonschema:"a file, or a directory when it ends in /"`
	Class       string `json:"class,omitempty" jsonschema:"only findings of this class, such as bug or security"`
	MinSeverity string `json:"min_severity,omitempty" jsonschema:"nit, info, warning, error or critical"`
	Limit       int    `json:"limit,omitempty" jsonschema:"most findings to return, 1 to 500; default 50"`
}

// ReviewInput is the input of get_review.
type ReviewInput struct {
	ID string `json:"id" jsonschema:"a review id from list_findings"`
}

func (c *Component) mcpServer(who caller) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "nitpick-insights", Version: "1"}, &mcp.ServerOptions{
		Instructions: "Findings that nitpick reviews reported, by repository and file. Use list_hotspots to find where bugs keep appearing, list_findings to read them, and get_review for one run.",
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_overview", Description: "Counts of reviews, findings and repositories in a window, and how many reviews did not finish."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in Window) (*mcp.CallToolResult, any, error) {
			q, err := c.window(in)
			if err != nil {
				return nil, nil, err
			}
			v, err := c.store.View(ctx, q)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(map[string]any{"overview": v.Overview(q), "series": v.Series(q)})
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_hotspots", Description: "Files ranked by how many findings, how severe and how recent. Trend compares the window with the one before it."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in HotspotsInput) (*mcp.CallToolResult, any, error) {
			q, err := c.window(in.Window)
			if err != nil {
				return nil, nil, err
			}
			n, err := capLimit(in.Limit, 20)
			if err != nil {
				return nil, nil, err
			}
			v, err := c.store.View(ctx, q)
			if err != nil {
				return nil, nil, err
			}
			hs := v.Hotspots(q)
			total := len(hs)
			if len(hs) > n {
				hs = hs[:n]
			}
			return jsonResult(map[string]any{"hotspots": hs, "total": total})
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_findings", Description: "Findings from reviews, newest first. total says how many matched, so a cut-short list is not mistaken for all of them."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in FindingsInput) (*mcp.CallToolResult, any, error) {
			q, err := c.window(in.Window)
			if err != nil {
				return nil, nil, err
			}
			n, err := capLimit(in.Limit, 50)
			if err != nil {
				return nil, nil, err
			}
			if in.MinSeverity != "" && rank(in.MinSeverity) < 0 {
				return nil, nil, fmt.Errorf("min_severity must be one of %s", strings.Join(Severities, ", "))
			}
			v, err := c.store.View(ctx, q)
			if err != nil {
				return nil, nil, err
			}
			rows, total := v.Findings(FindingQuery{Query: q, Path: in.Path, Class: in.Class, MinSeverity: in.MinSeverity, Limit: n})
			return jsonResult(map[string]any{"findings": rows, "total": total})
		})
	mcp.AddTool(s, &mcp.Tool{Name: "get_review", Description: "One review with all its findings."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ReviewInput) (*mcp.CallToolResult, any, error) {
			rv, ok, err := c.store.Review(ctx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				return nil, nil, errors.New("no such review")
			}
			return jsonResult(rv)
		})
	if who.Write {
		mcp.AddTool(s, &mcp.Tool{Name: "submit_review", Description: "Report a review that ran. The same review sent twice is kept once."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in Submission) (*mcp.CallToolResult, any, error) {
				kept, err := c.store.Add(ctx, in.Review(who.Name))
				if err != nil {
					return nil, nil, err
				}
				return jsonResult(map[string]any{"id": kept.ID, "findings": len(kept.Findings), "truncated": kept.Truncated})
			})
	}
	return s
}

func (c *Component) window(in Window) (Query, error) {
	q := Query{Now: c.now().UTC(), Days: 30, Repo: in.Repo}
	if in.Days != 0 {
		if in.Days < 1 || in.Days > MaxDays {
			return q, fmt.Errorf("days must be a whole number from 1 to %d", MaxDays)
		}
		q.Days = in.Days
	}
	return q, nil
}

func capLimit(n, def int) (int, error) {
	switch {
	case n == 0:
		return def, nil
	case n < 1 || n > 500:
		return 0, errors.New("limit must be a whole number from 1 to 500")
	}
	return n, nil
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("encode the result: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}
