package insights

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// listHub is a hub with enough reviews to page: 25 reviews of two repositories,
// each with findings spread over files, severities and classes.
func listHub(t *testing.T) *httptest.Server {
	t.Helper()
	ts, c := newHub(t)
	day := 24 * time.Hour
	classes := []string{"bug", "security", "style"}
	sevs := []string{"nit", "info", "warning", "error", "critical"}
	for i := 0; i < 25; i++ {
		repo := "acme/api"
		if i%5 == 0 {
			repo = "acme/web"
		}
		var fs []Finding
		for j := 0; j < 1+i%4; j++ {
			fs = append(fs, Finding{
				Path: fmt.Sprintf("pkg/f%d.go", (i+j)%6), Line: 10 + j, Severity: sevs[(i+j)%5],
				Class: classes[(i+j)%3], Title: fmt.Sprintf("finding %d.%d about %s", i, j, classes[(i+j)%3]),
				Rationale: "the value is used before it is checked",
			})
		}
		r := Review{Repo: repo, Commit: fmt.Sprintf("c%03d", i), Ref: fmt.Sprintf("pull/%d", 100+i), At: t0.Add(-time.Duration(i) * day / 2), Result: "findings", Complete: i != 7, Findings: fs}
		if _, err := c.store.Add(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	return ts
}

type envelope struct {
	Items    []map[string]any `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Pages    int              `json:"pages"`
}

func getList(t *testing.T, ts *httptest.Server, path string, q url.Values) envelope {
	t.Helper()
	code, body := do(t, "GET", ts.URL+path+"?"+q.Encode(), readSecret, "")
	if code != 200 {
		t.Fatalf("GET %s?%s = %d %s", path, q.Encode(), code, body)
	}
	var e envelope
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, body)
	}
	return e
}

func ids(e envelope, key string) []string {
	var out []string
	for _, it := range e.Items {
		out = append(out, fmt.Sprint(it[key]))
	}
	return out
}

func TestEveryListRouteSharesOneEnvelopeAndPagesWithoutLosingOrRepeatingARow(t *testing.T) {
	ts := listHub(t)
	for _, path := range []string{"/v1/reviews", "/v1/insights/findings", "/v1/insights/hotspots", "/v1/insights/repos"} {
		first := getList(t, ts, path, url.Values{"days": {"30"}, "page_size": {"7"}, "page": {"1"}})
		if first.PageSize != 7 || first.Page != 1 || first.Total == 0 {
			t.Fatalf("%s: envelope = %+v", path, first)
		}
		if want := (first.Total + 6) / 7; first.Pages != want {
			t.Errorf("%s: pages = %d, want %d for %d rows of 7", path, first.Pages, want, first.Total)
		}
		var all []string
		for p := 1; p <= first.Pages; p++ {
			page := getList(t, ts, path, url.Values{"days": {"30"}, "page_size": {"7"}, "page": {strconv.Itoa(p)}})
			if p < first.Pages && len(page.Items) != 7 {
				t.Errorf("%s page %d holds %d, want a full page of 7", path, p, len(page.Items))
			}
			if page.Total != first.Total {
				t.Errorf("%s page %d total = %d, want %d", path, p, page.Total, first.Total)
			}
			for _, it := range page.Items {
				b, _ := json.Marshal(it)
				all = append(all, string(b))
			}
		}
		if len(all) != first.Total {
			t.Errorf("%s: the pages hold %d rows, the total says %d", path, len(all), first.Total)
		}
		seen := map[string]bool{}
		for _, row := range all {
			if seen[row] && path != "/v1/insights/findings" {
				t.Errorf("%s: a row appears on two pages: %.80s", path, row)
			}
			seen[row] = true
		}
	}
}

func TestAPagePastTheEndIsEmptyWithTheRightTotalNotAnError(t *testing.T) {
	ts := listHub(t)
	e := getList(t, ts, "/v1/reviews", url.Values{"days": {"30"}, "page_size": {"10"}, "page": {"99"}})
	if len(e.Items) != 0 || e.Total != 25 || e.Page != 99 {
		t.Fatalf("a page past the end = %+v", e)
	}
	if e.Items == nil {
		t.Fatal("items must be [] and not null, or the app cannot map over it")
	}
}

func TestSortingOrdersTheWholeSetNotJustThePage(t *testing.T) {
	ts := listHub(t)
	asc := getList(t, ts, "/v1/reviews", url.Values{"days": {"30"}, "sort": {"findings"}, "order": {"asc"}, "page_size": {"100"}})
	prev := -1.0
	for _, it := range asc.Items {
		n := it["findings"].(float64)
		if n < prev {
			t.Fatalf("not ascending by findings: %v after %v", n, prev)
		}
		prev = n
	}
	desc := getList(t, ts, "/v1/reviews", url.Values{"days": {"30"}, "sort": {"findings"}, "order": {"desc"}, "page_size": {"5"}})
	if desc.Items[0]["findings"].(float64) != 4 {
		t.Fatalf("the first of a descending sort has %v findings, want the maximum 4: it sorted the page and not the set", desc.Items[0]["findings"])
	}
	byRepo := getList(t, ts, "/v1/reviews", url.Values{"days": {"30"}, "sort": {"repo"}, "order": {"asc"}, "page_size": {"100"}})
	if r := ids(byRepo, "repo"); r[0] != "acme/api" || r[len(r)-1] != "acme/web" {
		t.Fatalf("sorted by repo: %v ... %v", r[0], r[len(r)-1])
	}
}

func TestSearchNarrowsTheSetAndTheTotalCountsOnlyWhatMatched(t *testing.T) {
	ts := listHub(t)
	e := getList(t, ts, "/v1/reviews", url.Values{"days": {"30"}, "q": {"PULL/107"}})
	if e.Total != 1 || ids(e, "ref")[0] != "pull/107" {
		t.Fatalf("a case-insensitive search for a ref = %+v", e)
	}
	e = getList(t, ts, "/v1/reviews", url.Values{"days": {"30"}, "q": {"acme/web"}})
	if e.Total != 5 {
		t.Fatalf("a search for a repository matched %d, want the 5 reviews of acme/web", e.Total)
	}
	e = getList(t, ts, "/v1/insights/findings", url.Values{"days": {"30"}, "q": {"about security"}})
	if e.Total == 0 {
		t.Fatal("a search of finding titles found nothing")
	}
	for _, it := range e.Items {
		if it["class"] != "security" {
			t.Errorf("a result does not match the search: %v", it)
		}
	}
	if none := getList(t, ts, "/v1/insights/findings", url.Values{"days": {"30"}, "q": {"zzz-no-such-text"}}); none.Total != 0 || len(none.Items) != 0 || none.Pages != 0 {
		t.Fatalf("a search with no match = %+v", none)
	}
}

func TestFiltersCombineWithSearchAndSorting(t *testing.T) {
	ts := listHub(t)
	all := getList(t, ts, "/v1/insights/findings", url.Values{"days": {"30"}, "page_size": {"100"}})
	errs := getList(t, ts, "/v1/insights/findings", url.Values{"days": {"30"}, "min_severity": {"error"}, "page_size": {"100"}})
	if errs.Total == 0 || errs.Total >= all.Total {
		t.Fatalf("a severity floor kept %d of %d", errs.Total, all.Total)
	}
	for _, it := range errs.Items {
		if s := it["severity"]; s != "error" && s != "critical" {
			t.Errorf("a finding below the floor: %v", s)
		}
	}
	both := getList(t, ts, "/v1/insights/findings", url.Values{"days": {"30"}, "min_severity": {"error"}, "class": {"bug"}, "sort": {"severity"}, "order": {"desc"}, "page_size": {"100"}})
	if both.Total == 0 || both.Total >= errs.Total {
		t.Fatalf("a class filter on top of the floor kept %d of %d", both.Total, errs.Total)
	}
	if both.Items[0]["severity"] != "critical" {
		t.Errorf("sorted by severity descending, the first is %v", both.Items[0]["severity"])
	}
	inc := getList(t, ts, "/v1/reviews", url.Values{"days": {"30"}, "complete": {"false"}})
	if inc.Total != 1 || inc.Items[0]["complete"] != false {
		t.Fatalf("the incomplete filter = %+v, want the one review that did not finish", inc)
	}
	hot := getList(t, ts, "/v1/insights/hotspots", url.Values{"days": {"30"}, "q": {"F3"}})
	for _, it := range hot.Items {
		if it["path"] != "pkg/f3.go" {
			t.Errorf("a hotspot search for f3 returned %v", it["path"])
		}
	}
}

func TestListParametersAreValidatedNotIgnored(t *testing.T) {
	ts := listHub(t)
	for _, q := range []string{
		"/v1/reviews?page=0", "/v1/reviews?page=-1", "/v1/reviews?page=abc", "/v1/reviews?page_size=0", "/v1/reviews?page_size=101",
		"/v1/reviews?sort=password", "/v1/reviews?order=sideways", "/v1/reviews?complete=maybe",
		"/v1/insights/hotspots?sort=nope", "/v1/insights/hotspots?trend=sideways", "/v1/insights/findings?sort=nope",
		"/v1/insights/repos?sort=nope", "/v1/reviews?q=" + strings.Repeat("a", 300),
	} {
		if code, _ := do(t, "GET", ts.URL+q, readSecret, ""); code != 400 {
			t.Errorf("%.60s: %d, want 400", q, code)
		}
	}
}

func TestEveryListRouteSortsAByEachOfItsDocumentedFieldsWithoutAnError(t *testing.T) {
	ts := listHub(t)
	for path, fields := range map[string][]string{
		"/v1/reviews":           {"at", "repo", "findings", "errors", "result"},
		"/v1/insights/repos":    {"repo", "reviews", "findings", "errors", "last"},
		"/v1/insights/hotspots": {"score", "findings", "path", "repo", "delta", "worst", "last"},
		"/v1/insights/findings": {"at", "severity", "path", "repo", "class", "title"},
	} {
		for _, f := range fields {
			for _, order := range []string{"asc", "desc"} {
				if code, body := do(t, "GET", ts.URL+path+"?days=30&sort="+f+"&order="+order, readSecret, ""); code != 200 {
					t.Errorf("%s sort=%s order=%s: %d %.80s", path, f, order, code, body)
				}
			}
		}
	}
}

func TestTheFacetsListTheValuesAFilterCanOfferOnlyFromWhatWasReported(t *testing.T) {
	ts := listHub(t)
	code, body := do(t, "GET", ts.URL+"/v1/insights/facets?days=30", readSecret, "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var f struct {
		Repos      []string `json:"repos"`
		Classes    []string `json:"classes"`
		Severities []string `json:"severities"`
	}
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Repos) != 2 || f.Repos[0] != "acme/api" || len(f.Classes) != 3 || f.Classes[0] != "bug" || len(f.Severities) != 5 || f.Severities[0] != "nit" {
		t.Fatalf("facets = %+v", f)
	}
	if code, _ := do(t, "GET", ts.URL+"/v1/insights/facets", "", ""); code != 401 {
		t.Fatalf("facets without a token = %d, want 401", code)
	}
}

func TestTheOverviewsPriorPeriodIsTheWindowTheHotspotTrendsCompareWith(t *testing.T) {
	ts, c := newHub(t)
	day := 24 * time.Hour
	q := Query{Now: t0, Days: 30}
	from, edge := q.window()
	for i, r := range []Review{
		review("r", edge.Add(time.Hour), finding("a.go", "nit")),
		review("r", from.Add(-time.Hour), finding("a.go", "nit"), finding("a.go", "nit")), // the last partial day before the period
		review("r", t0.Add(-day), finding("a.go", "error"), finding("a.go", "error"), finding("a.go", "error"), finding("a.go", "error"), finding("a.go", "error")),
	} {
		r.Commit = strconv.Itoa(i)
		if _, err := c.store.Add(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	_, body := do(t, "GET", ts.URL+"/v1/insights/overview?days=30", readSecret, "")
	var ov struct {
		Overview struct{ Findings int } `json:"overview"`
		Prior    struct{ Findings int } `json:"prior"`
	}
	if err := json.Unmarshal([]byte(body), &ov); err != nil {
		t.Fatal(err)
	}
	hot := getList(t, ts, "/v1/insights/hotspots", url.Values{"days": {"30"}})
	if ov.Overview.Findings != 5 || ov.Prior.Findings != 3 || hot.Items[0]["prior"].(float64) != 3 {
		t.Fatalf("overview %d now, %d before; hotspot prior %v: the cards and the trends must compare with the same 3", ov.Overview.Findings, ov.Prior.Findings, hot.Items[0]["prior"])
	}
	if hot.Items[0]["delta"].(float64) != 2 {
		t.Errorf("delta = %v, want 5 recent minus 3 prior", hot.Items[0]["delta"])
	}
}

func TestHotspotsFilterByWorstSeverityAndByTrend(t *testing.T) {
	ts := listHub(t)
	all := getList(t, ts, "/v1/insights/hotspots", url.Values{"days": {"30"}, "page_size": {"100"}})
	crit := getList(t, ts, "/v1/insights/hotspots", url.Values{"days": {"30"}, "min_severity": {"critical"}, "page_size": {"100"}})
	if crit.Total == 0 || crit.Total >= all.Total {
		t.Fatalf("a critical floor kept %d of %d hotspots", crit.Total, all.Total)
	}
	for _, it := range crit.Items {
		if it["worst"] != "critical" {
			t.Errorf("a hotspot below the floor: %v", it["worst"])
		}
	}
	for _, trend := range []string{"up", "down", "flat"} {
		got := getList(t, ts, "/v1/insights/hotspots", url.Values{"days": {"30"}, "trend": {trend}, "page_size": {"100"}})
		for _, it := range got.Items {
			if it["trend"] != trend {
				t.Errorf("trend=%s returned a %v hotspot", trend, it["trend"])
			}
		}
		if got.Total > all.Total {
			t.Errorf("trend=%s returned more than all of them", trend)
		}
	}
	// Rule 10: the filter, not an empty data set, is why the lists differ.
	up := getList(t, ts, "/v1/insights/hotspots", url.Values{"days": {"30"}, "trend": {"up"}, "page_size": {"100"}})
	flat := getList(t, ts, "/v1/insights/hotspots", url.Values{"days": {"30"}, "trend": {"flat"}, "page_size": {"100"}})
	if up.Total+flat.Total == 0 || up.Total+flat.Total > all.Total {
		t.Fatalf("up %d + flat %d of %d: the trend filter is not splitting the set", up.Total, flat.Total, all.Total)
	}
}
