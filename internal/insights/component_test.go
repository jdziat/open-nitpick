package insights

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nitpick-oss/nitpick/internal/server"
)

const (
	writeSecret = "write-secret-value"
	readSecret  = "read-secret-value"
)

func newHub(t *testing.T) (*httptest.Server, *Component) {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	c := NewComponent(store, []Token{NewToken("ci", RoleWrite, writeSecret), NewToken("agent", RoleRead, readSecret)}, nil)
	c.now = func() time.Time { return t0 }
	h, err := server.New(server.Config{Components: []server.Component{c}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, c
}

func do(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const goodSubmission = `{"repo":"acme/api","commit":"abc1234","at":"2026-10-31T10:00:00Z",
 "report":{"result":"findings","complete":true,"findings":[
  {"path":"pkg/a.go","line":7,"severity":"error","class":"bug","title":"nil deref","rationale":"x may be nil","suggestion":"ignored field","confidence":0.9}],
  "withheld":[{"path":"b.go"}],"usage":{"ignored":1}}}`

func TestPostingAReviewNeedsAWriteToken(t *testing.T) {
	ts, _ := newHub(t)
	for name, tc := range map[string]struct {
		token string
		want  int
	}{"no token": {"", 401}, "wrong token": {"nope", 401}, "read token": {readSecret, 403}, "write token": {writeSecret, 201}} {
		if got, body := do(t, "POST", ts.URL+"/v1/reviews", tc.token, goodSubmission); got != tc.want {
			t.Errorf("%s: status %d (%s), want %d", name, got, body, tc.want)
		}
	}
}

func TestReadRoutesAcceptEitherTokenAndRefuseNone(t *testing.T) {
	ts, _ := newHub(t)
	_, _ = do(t, "POST", ts.URL+"/v1/reviews", writeSecret, goodSubmission)
	for _, path := range []string{"/v1/reviews", "/v1/insights/overview", "/v1/insights/hotspots", "/v1/insights/findings"} {
		if got, _ := do(t, "GET", ts.URL+path, "", ""); got != 401 {
			t.Errorf("%s without a token: %d", path, got)
		}
		if got, body := do(t, "GET", ts.URL+path, readSecret, ""); got != 200 {
			t.Errorf("%s with a read token: %d %s", path, got, body)
		}
	}
}

func TestPostedReviewIsReadableBackWithItsFindingAndWithheldCount(t *testing.T) {
	ts, _ := newHub(t)
	_, body := do(t, "POST", ts.URL+"/v1/reviews", writeSecret, goodSubmission)
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &made); err != nil || made.ID == "" {
		t.Fatalf("no id in %q", body)
	}
	code, got := do(t, "GET", ts.URL+"/v1/reviews/"+made.ID, readSecret, "")
	var rv Review
	if code != 200 || json.Unmarshal([]byte(got), &rv) != nil {
		t.Fatalf("%d %s", code, got)
	}
	if rv.Repo != "acme/api" || len(rv.Findings) != 1 || rv.Findings[0].Path != "pkg/a.go" || rv.Withheld != 1 || rv.Source != "ci" {
		t.Fatalf("review = %+v", rv)
	}
	if code, _ := do(t, "GET", ts.URL+"/v1/reviews/"+strings.Repeat("0", 64), readSecret, ""); code != 404 {
		t.Fatalf("an unknown id answered %d", code)
	}
}

func TestPostRefusesBadInputWithTheRightStatus(t *testing.T) {
	ts, _ := newHub(t)
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"not json":     {`{`, 400},
		"no repo":      {`{"report":{"result":"clean","complete":true}}`, 400},
		"bad severity": {`{"repo":"r","report":{"findings":[{"path":"a.go","severity":"doom"}]}}`, 400},
		"oversize":     {`{"repo":"r","report":{"result":"` + strings.Repeat("x", maxBody) + `"}}`, 413},
	} {
		if got, body := do(t, "POST", ts.URL+"/v1/reviews", writeSecret, tc.body); got != tc.want {
			t.Errorf("%s: %d (%.80s), want %d", name, got, body, tc.want)
		}
	}
}

func TestQueryParametersAreValidatedNotIgnored(t *testing.T) {
	ts, _ := newHub(t)
	for _, q := range []string{"/v1/insights/hotspots?days=0", "/v1/insights/hotspots?days=9999", "/v1/insights/hotspots?days=x", "/v1/insights/findings?limit=0", "/v1/insights/findings?min_severity=doom"} {
		if got, _ := do(t, "GET", ts.URL+q, readSecret, ""); got != 400 {
			t.Errorf("%s: %d, want 400", q, got)
		}
	}
}

func TestHotspotsRouteRanksWhatWasPosted(t *testing.T) {
	ts, _ := newHub(t)
	_, _ = do(t, "POST", ts.URL+"/v1/reviews", writeSecret, goodSubmission)
	_, body := do(t, "GET", ts.URL+"/v1/insights/hotspots", readSecret, "")
	var out struct {
		Hotspots []Hotspot `json:"hotspots"`
		Total    int       `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Total != 1 || out.Hotspots[0].Path != "pkg/a.go" {
		t.Fatalf("%s", body)
	}
}

func connect(t *testing.T, ts *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).
		Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tool := range res.Tools {
		out[tool.Name] = true
	}
	return out
}

func TestMCPOffersSubmitOnlyToAWriteToken(t *testing.T) {
	ts, _ := newHub(t)
	if names := toolNames(t, connect(t, ts, readSecret)); names["submit_review"] || !names["list_hotspots"] {
		t.Fatalf("read token tools = %v", names)
	}
	if names := toolNames(t, connect(t, ts, writeSecret)); !names["submit_review"] || !names["list_hotspots"] {
		t.Fatalf("write token tools = %v", names)
	}
}

func TestMCPRefusesACallerWithoutAToken(t *testing.T) {
	ts, _ := newHub(t)
	if code, _ := do(t, "POST", ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 401 {
		t.Fatalf("status %d", code)
	}
	if code, _ := do(t, "POST", ts.URL+"/mcp", "wrong", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 401 {
		t.Fatalf("status %d", code)
	}
}

func TestAnAgentCanSubmitAReviewAndThenFindItsHotspot(t *testing.T) {
	ts, _ := newHub(t)
	cs := connect(t, ts, writeSecret)
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "submit_review", Arguments: map[string]any{
		"repo": "acme/api", "at": "2026-10-31T10:00:00Z",
		"report": map[string]any{"result": "findings", "complete": true, "findings": []map[string]any{{"path": "pkg/a.go", "line": 3, "severity": "critical", "class": "security", "title": "sql built from input"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_hotspots", Arguments: map[string]any{"days": 30}})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, `"path":"pkg/a.go"`) || !strings.Contains(text, `"worst":"critical"`) {
		t.Fatalf("hotspots = %s", text)
	}
}

func TestMCPToolErrorsAreReportedToTheAgentNotSwallowed(t *testing.T) {
	ts, _ := newHub(t)
	cs := connect(t, ts, readSecret)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_findings", Arguments: map[string]any{"min_severity": "doom"}})
	if err != nil {
		return // a protocol-level refusal is also a refusal
	}
	if !res.IsError {
		t.Fatalf("a bad severity was answered as success: %+v", res)
	}
	if res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_review", Arguments: map[string]any{"id": "nope"}}); err == nil && !res.IsError {
		t.Fatalf("an unknown review was answered as success")
	}
}

func TestTheJSONRoutesUseLowercaseSnakeCaseNamesThroughout(t *testing.T) {
	ts, _ := newHub(t)
	_, _ = do(t, "POST", ts.URL+"/v1/reviews", writeSecret, goodSubmission)
	for _, path := range []string{"/v1/insights/hotspots", "/v1/insights/findings", "/v1/insights/overview", "/v1/reviews"} {
		_, body := do(t, "GET", ts.URL+path, readSecret, "")
		var v any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatal(err)
		}
		var walk func(any)
		walk = func(x any) {
			switch x := x.(type) {
			case map[string]any:
				for k, child := range x {
					if k != strings.ToLower(k) {
						t.Errorf("%s: field %q is not lower case", path, k)
					}
					walk(child)
				}
			case []any:
				for _, c := range x {
					walk(c)
				}
			}
		}
		walk(v)
	}
}

func TestTheReviewListSaysHowManyThereAreWhenItIsCutShort(t *testing.T) {
	ts, c := newHub(t)
	for i := 0; i < 205; i++ {
		_, err := c.store.Add(context.Background(), Review{Repo: "r", Commit: strconv.Itoa(i), At: t0.Add(-time.Duration(i) * time.Minute), Result: "clean", Complete: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, body := do(t, "GET", ts.URL+"/v1/reviews?days=30", readSecret, "")
	var out struct {
		Reviews   []json.RawMessage `json:"reviews"`
		Total     int               `json:"total"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Reviews) != 200 || out.Total != 205 || !out.Truncated {
		t.Fatalf("%d reviews, total %d, truncated %v: a cut-short list must say so", len(out.Reviews), out.Total, out.Truncated)
	}
}

func TestTheReviewListLeavesOutAReviewDatedAheadThatEveryAnalyticExcludes(t *testing.T) {
	ts, c := newHub(t)
	// The store accepts a little clock skew; the list must agree with the counts.
	c.now = time.Now
	if _, err := c.store.Add(context.Background(), Review{Repo: "r", Commit: "now", At: time.Now().Add(-time.Minute), Result: "clean", Complete: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.Add(context.Background(), Review{Repo: "r", Commit: "ahead", At: time.Now().Add(3 * time.Minute), Result: "clean", Complete: true}); err != nil {
		t.Fatalf("a review 3 minutes ahead is within the skew and must be accepted: %v", err)
	}
	_, body := do(t, "GET", ts.URL+"/v1/reviews?days=7", readSecret, "")
	var out struct {
		Reviews []struct{ Commit string } `json:"reviews"`
		Total   int                       `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 1 || len(out.Reviews) != 1 || out.Reviews[0].Commit != "now" {
		t.Fatalf("the list holds %+v: a review dated ahead is in the list but in no count", out)
	}
	_, over := do(t, "GET", ts.URL+"/v1/insights/overview?days=7", readSecret, "")
	var ov struct {
		Overview struct{ Reviews int } `json:"overview"`
	}
	_ = json.Unmarshal([]byte(over), &ov)
	if ov.Overview.Reviews != out.Total {
		t.Fatalf("overview counts %d reviews, the list %d", ov.Overview.Reviews, out.Total)
	}
}
