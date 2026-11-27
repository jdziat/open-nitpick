package insights

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// signInProvider signs in whoever the form names, with the groups it names. What
// is under test is what the pages do with a session, not a credential.
type signInProvider struct{}

func (signInProvider) ID() string               { return "fake" }
func (signInProvider) Label() string            { return "Fake" }
func (signInProvider) Form() server.LoginForm   { return server.FormCredentials }
func (signInProvider) CrossSitePosts() []string { return nil }
func (signInProvider) Mount(mux *http.ServeMux, h server.Host) {
	mux.HandleFunc("POST /auth/fake/login", func(w http.ResponseWriter, r *http.Request) {
		secret, ok := h.CSRF().PreSessionSecret(r)
		if !ok || h.CSRF().Token(secret) != r.PostFormValue(server.CSRFFormField) {
			http.Error(w, "csrf", http.StatusForbidden)
			return
		}
		h.SignIn(w, r, server.Identity{Provider: "fake", Subject: r.PostFormValue("username"), Groups: strings.Fields(r.PostFormValue("groups"))}, "/ui/")
	})
}

type ui struct {
	t  *testing.T
	ts *httptest.Server
	c  *http.Client
	co *Component
}

func newUI(t *testing.T) *ui {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	co := NewComponent(store, []Token{NewToken("ci", RoleWrite, writeSecret)}, nil)
	co.now = func() time.Time { return t0 }
	roles, err := server.NewRoleMap([]server.RoleRule{{Roles: []server.Role{server.Role(RoleView)}, Group: "eng"}})
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{
		"manifest.json":        {Data: []byte(`{"app.css":"app.0123456789ab.css","app.js":"app.0123456789ab.js"}`)},
		"app.0123456789ab.css": {Data: []byte("body{}")},
		"app.0123456789ab.js":  {Data: []byte("//")},
	}
	srv, err := server.New(server.Config{Components: []server.Component{co}, Providers: []server.Provider{signInProvider{}}, Roles: roles, Assets: assets, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ui{t: t, ts: ts, c: c, co: co}
}

func (u *ui) get(path string) (int, string) {
	u.t.Helper()
	resp, err := u.c.Get(u.ts.URL + path)
	if err != nil {
		u.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (u *ui) signIn(groups string) {
	u.t.Helper()
	_, page := u.get("/auth/login")
	i := strings.Index(page, `name="csrf_token" value="`) + len(`name="csrf_token" value="`)
	token := page[i : i+strings.Index(page[i:], `"`)]
	resp, err := u.c.PostForm(u.ts.URL+"/auth/fake/login", url.Values{"username": {"ana"}, "groups": {groups}, "csrf_token": {token}})
	if err != nil {
		u.t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		u.t.Fatalf("sign-in answered %d", resp.StatusCode)
	}
}

func (u *ui) seed() {
	u.t.Helper()
	day := 24 * time.Hour
	hostile := finding("pkg/<script>alert(1)</script>.go", "critical")
	hostile.Title = `<img src=x onerror=alert(1)> "quoted" & more`
	hostile.Rationale = `</details><script>alert(2)</script>`
	for _, r := range []Review{
		review("acme/api", t0.Add(-1*day), finding("hot.go", "error"), finding("hot.go", "warning"), hostile),
		review("acme/api", t0.Add(-3*day), finding("hot.go", "error")),
		review("acme/web", t0.Add(-2*day), finding("ui.go", "nit")),
		// The trend needs a review before the window to have a baseline.
		review("acme/api", t0.Add(-40*day), finding("hot.go", "nit")),
	} {
		if _, err := u.co.store.Add(context.Background(), r); err != nil {
			u.t.Fatal(err)
		}
	}
}

func TestEveryPageNeedsASessionWithTheInsightsRole(t *testing.T) {
	u := newUI(t)
	u.seed()
	paths := []string{"/ui/insights/", "/ui/insights/repos", "/ui/insights/hotspots", "/ui/insights/findings", "/ui/insights/reviews"}
	for _, p := range paths {
		if code, _ := u.get(p); code != http.StatusSeeOther {
			t.Errorf("%s signed out: %d, want a redirect to sign in", p, code)
		}
	}
	u.signIn("other")
	for _, p := range paths {
		if code, _ := u.get(p); code != http.StatusForbidden {
			t.Errorf("%s without the role: %d, want 403", p, code)
		}
	}
}

func TestPagesRenderTheSeededNumbersForARoleHolder(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	code, body := u.get("/ui/insights/?days=30")
	if code != 200 {
		t.Fatalf("%d %.200s", code, body)
	}
	for _, want := range []string{"Findings per day", "Where findings concentrate", `<code>hot.go</code>`, "By severity", "acme/web", `aria-current="true"`, "<svg class=\"chart\""} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	if !strings.Contains(body, `<td data-label="Findings" class="num">3</td>`) {
		t.Errorf("the overview's top hotspot must carry its 3 findings")
	}
	// 3 reviews, 5 findings.
	if !strings.Contains(body, `<div class="card-value">3</div>`) || !strings.Contains(body, `<div class="card-value">5</div>`) {
		t.Errorf("the cards do not carry 3 reviews and 5 findings")
	}
}

func TestHotspotsPageNamesTheTopFileFirstWithATrend(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	_, body := u.get("/ui/insights/hotspots?days=30")
	first := strings.Index(body, "hot.go")
	if first < 0 || first > strings.Index(body, "ui.go") {
		t.Fatalf("hot.go must be listed before ui.go")
	}
	if !strings.Contains(body, `<td data-label="Findings" class="num">3</td>`) {
		t.Fatalf("hot.go has 3 findings and the table must say 3")
	}
	if !strings.Contains(body, "trend-up") || !strings.Contains(body, "How the score works") {
		t.Fatalf("trend or the scoring note is missing")
	}
}

func TestAFindingsTextCannotInjectMarkupIntoAnyPage(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	for _, p := range []string{"/ui/insights/findings?days=30", "/ui/insights/hotspots?days=30", "/ui/insights/?days=30", "/ui/insights/reviews"} {
		_, body := u.get(p)
		for _, bad := range []string{"<script>alert", "<img src=x", "</details><script"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: unescaped %q", p, bad)
			}
		}
	}
	_, list := u.get("/ui/insights/reviews")
	const marker = "/ui/insights/reviews/"
	start := strings.Index(list, marker)
	if start < 0 {
		t.Fatal("the reviews list links to no review")
	}
	id, _, _ := strings.Cut(list[start+len(marker):], `"`)
	_, body := u.get("/ui/insights/reviews/" + id)
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") {
		t.Fatalf("the review page did not escape a finding")
	}
}

func TestAnEmptyStoreTellsThePersonHowToFillIt(t *testing.T) {
	u := newUI(t)
	u.signIn("eng")
	for _, p := range []string{"/ui/insights/", "/ui/insights/hotspots", "/ui/insights/findings", "/ui/insights/reviews", "/ui/insights/repos"} {
		code, body := u.get(p)
		if code != 200 || !strings.Contains(body, "No reviews in this period") || !strings.Contains(body, "-insights") {
			t.Errorf("%s: %d, no empty state with the command", p, code)
		}
	}
}

func TestAReviewThatDidNotFinishSaysSoInsteadOfLookingClean(t *testing.T) {
	u := newUI(t)
	r := review("acme/api", t0.Add(-time.Hour))
	r.Complete, r.Result = false, "error"
	kept, _ := u.co.store.Add(context.Background(), r)
	u.signIn("eng")
	_, body := u.get("/ui/insights/reviews/" + kept.ID)
	if !strings.Contains(body, `<span class="tag">did not finish</span>`) {
		t.Fatalf("the result line must carry the did-not-finish tag")
	}
	if !strings.Contains(body, "This review stopped early, so its findings may be incomplete.") || strings.Contains(body, "The review finished and reported no findings") {
		t.Fatalf("an incomplete review was shown as clean")
	}
	_, over := u.get("/ui/insights/")
	if !strings.Contains(over, "Did not finish: 1.") {
		t.Fatalf("the overview hides an incomplete review")
	}
}

func TestPagesRefuseABadWindowAndAnUnknownReview(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	for p, want := range map[string]int{"/ui/insights/?days=0": 404, "/ui/insights/?days=abc": 404, "/ui/insights/findings?min_severity=doom": 404, "/ui/insights/reviews/" + strings.Repeat("0", 64): 404} {
		if code, _ := u.get(p); code != want {
			t.Errorf("%s: %d, want %d", p, code, want)
		}
	}
}

func TestFindingsFilterNarrowsTheTableAndSaysHowManyMatched(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	_, body := u.get("/ui/insights/findings?days=30&min_severity=error&path=hot.go")
	if !strings.Contains(body, "2 findings.") || strings.Contains(body, "ui.go") {
		t.Fatalf("filter result wrong: %.600s", body)
	}
	_, none := u.get("/ui/insights/findings?days=30&path=nothing.go")
	for _, want := range []string{"No finding matches these filters in the last 30 days", "Widen to 90 days", "Clear filters", "so this is a result and not a gap"} {
		if !strings.Contains(none, want) {
			t.Fatalf("the empty filter result lacks %q", want)
		}
	}
}

func TestATrendIsColouredOnlyWhenItMovedAndHadSomethingToCompareWith(t *testing.T) {
	c := &Component{}
	for name, tc := range map[string]struct {
		h     Hotspot
		trend Trend
		text  string
	}{
		"up":          {Hotspot{Recent: 9, Prior: 3, Baseline: true, Trend: TrendUp}, TrendUp, "▲ +6 (was 3)"},
		"down":        {Hotspot{Recent: 1, Prior: 7, Baseline: true, Trend: TrendDown}, TrendDown, "▼ -6 (was 7)"},
		"steady":      {Hotspot{Recent: 4, Prior: 3, Baseline: true, Trend: TrendFlat}, TrendFlat, "steady (was 3)"},
		"no baseline": {Hotspot{Recent: 65, Prior: 0, Baseline: false, Trend: TrendFlat}, TrendFlat, "no earlier data"},
	} {
		row := c.hotspotRow(1, tc.h, 10, 30)
		if row.Trend != tc.trend || !strings.Contains(row.TrendText, tc.text) {
			t.Errorf("%s: %+v, want %s %q", name, row, tc.trend, tc.text)
		}
	}
}

func TestMoreReviewsIsNotColouredAsBadNewsButMoreFindingsIs(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	_, body := u.get("/ui/insights/?days=30")
	i := strings.Index(body, `<div class="card-label">Reviews</div>`)
	j := strings.Index(body, `<div class="card-label">Findings</div>`)
	if i < 0 || j < 0 {
		t.Fatal("cards missing")
	}
	if strings.Contains(body[max(0, i-80):i], "card-up") {
		t.Errorf("the Reviews card is coloured as bad news")
	}
	if !strings.Contains(body[max(0, j-80):j], "card-up") {
		t.Errorf("the Findings card, which rose, must be coloured")
	}
}

func TestTheHotspotTablesBusiestBucketSetsEveryRowsSparklineScale(t *testing.T) {
	small := Hotspot{Spark: []int{0, 3}, Findings: 3, Recent: 3}
	big := Hotspot{Spark: []int{0, 41}, Findings: 41, Recent: 41}
	if got := sparkScale([]Hotspot{small, big}); got != 41 {
		t.Fatalf("scale = %d, want the busiest bucket 41", got)
	}
	c := &Component{}
	alone := c.hotspotRow(1, small, 0, 30).Spark.LastY
	shared := c.hotspotRow(1, small, 41, 30).Spark.LastY
	if shared <= alone {
		t.Fatalf("a row given the table's scale must sit lower than one scaled to itself: %v vs %v", shared, alone)
	}
}

func TestAWindowWithNoEarlierReviewsSaysSoOnceInsteadOfOnEveryRow(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	for _, p := range []string{"/ui/insights/hotspots?days=90", "/ui/insights/?days=90"} {
		_, body := u.get(p)
		// Hotspots says it beside its table. Overview says it once, under the
		// cards, in different words, so the two pages never repeat it.
		want := 1
		if strings.HasPrefix(p, "/ui/insights/?") {
			want = 0
		}
		if n := strings.Count(body, "No earlier data: no reviews came before this period"); n != want {
			t.Errorf("%s: the note appears %d times, want %d", p, n, want)
		}
		if strings.Contains(body, `data-label="Trend"`) || strings.Contains(body, "no earlier data</span>") {
			t.Errorf("%s: a Trend column was drawn with nothing to compare", p)
		}
	}
	_, with := u.get("/ui/insights/hotspots?days=30")
	if !strings.Contains(with, `data-label="Trend"`) || strings.Contains(with, "No earlier data:") {
		t.Errorf("the 30 day view has a baseline and must show its trend column")
	}
}

func TestAListShowsOneStatusForAReviewThatDidNotFinish(t *testing.T) {
	u := newUI(t)
	r := review("acme/api", t0.Add(-time.Hour), finding("a.go", "error"))
	r.Complete, r.Result = false, "findings"
	_, _ = u.co.store.Add(context.Background(), r)
	u.signIn("eng")
	for _, p := range []string{"/ui/insights/reviews", "/ui/insights/"} {
		_, body := u.get(p)
		if !strings.Contains(body, `<td data-label="Result"><span class="tag">did not finish</span></td>`) {
			t.Errorf("%s: the chip must replace the result word, not follow it", p)
		}
	}
}

func TestAWindowWithNoEarlierReviewsDoesNotPromiseATrendOrRepeatItselfInTheCards(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	_, hot := u.get("/ui/insights/hotspots?days=90")
	if strings.Contains(hot, "Trend compares the period") {
		t.Errorf("the lede promises a trend the page then says it cannot show")
	}
	if strings.Index(hot, "No earlier data:") > strings.Index(hot, "<table") {
		t.Errorf("the note must come before the table, where it is read")
	}
	_, over := u.get("/ui/insights/?days=90")
	if strings.Contains(over, "No earlier data:") {
		t.Errorf("the overview says the same fact a second time, in the hotspots panel")
	}
	if n := strings.Count(over, "No reviews came before this period"); n != 1 {
		t.Errorf("the overview says it %d times, want once", n)
	}
	if strings.Contains(over, "days before</div>") && strings.Contains(over, "no reviews in the") {
		t.Errorf("a KPI card repeats the no-earlier-period note")
	}
	_, thirty := u.get("/ui/insights/hotspots?days=30")
	if !strings.Contains(thirty, "Trend compares the period") {
		t.Errorf("with a baseline the lede must explain the trend")
	}
}

func TestTheChartStatesItsScaleInTextBecauseTheAxisScrollsAwayOnAPhone(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	_, body := u.get("/ui/insights/?days=30")
	if !strings.Contains(body, "Scale: 0 to 4 a day") {
		t.Fatalf("the chart has no scale outside the scrolling svg")
	}
}

func TestClearFiltersIsOfferedOnceWhenNothingMatches(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	_, none := u.get("/ui/insights/findings?days=30&path=nothing.go")
	if n := strings.Count(none, "Clear filters"); n != 1 {
		t.Fatalf("Clear filters appears %d times, want once", n)
	}
	_, some := u.get("/ui/insights/findings?days=30&path=hot.go")
	if n := strings.Count(some, "Clear filters"); n != 1 {
		t.Fatalf("with results and a filter the form carries it once, got %d", n)
	}
}

func TestAReviewPageIsNamedByItsRepositoryAndPullRequest(t *testing.T) {
	u := newUI(t)
	r := review("acme/api", t0.Add(-time.Hour), finding("a.go", "error"))
	r.Ref = "pull/153"
	kept, _ := u.co.store.Add(context.Background(), r)
	u.signIn("eng")
	_, body := u.get("/ui/insights/reviews/" + kept.ID)
	if !strings.Contains(body, "<h1>acme/api · pull/153</h1>") {
		t.Fatalf("the review's heading is not its repository and pull request: %.300s", body)
	}
	if got := reviewHeading(reviewView{Review: Review{Repo: "acme/api", Commit: "abcdef0123456789"}}); got != "acme/api · abcdef01" {
		t.Fatalf("without a ref the heading uses the short commit: %q", got)
	}
}

func TestAFinishedReviewWithFindingsShowsADashNotTheWordFindings(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	_, body := u.get("/ui/insights/reviews")
	if strings.Contains(body, `class="quiet">findings`) {
		t.Errorf("the Result column repeats the word findings next to the count")
	}
	if !strings.Contains(body, `<span class="muted" title="Finished with findings">–</span>`) {
		t.Errorf("a finished review with findings shows no marker")
	}
}

func TestAHotspotsFindingsLinkKeepsThePeriodTheReaderWasLookingAt(t *testing.T) {
	c := &Component{}
	row := c.hotspotRow(1, Hotspot{Repo: "r", Path: "a.go"}, 0, 90)
	if !strings.Contains(row.FindingsHref, "days=90") {
		t.Fatalf("the link to the findings drops the period: %s", row.FindingsHref)
	}
}

func TestTheRepositoriesPageListsEveryRepositoryEvenWithAStrayRepoParameter(t *testing.T) {
	u := newUI(t)
	u.seed()
	u.signIn("eng")
	_, body := u.get("/ui/insights/repos?days=30&repo=acme/web")
	if !strings.Contains(body, "acme/api") || !strings.Contains(body, "acme/web") {
		t.Fatalf("a repo parameter hid the other repositories on the page that lists them all")
	}
}

func TestTheNoTrendNoteShowsOnlyWhenNoRowHasSomethingToCompareWith(t *testing.T) {
	u := newUI(t)
	day := 24 * time.Hour
	// Repo a has an earlier review, repo b has none. The top row is repo b's.
	for _, r := range []Review{
		review("a", t0.Add(-40*day), finding("old.go", "nit")),
		review("b", t0.Add(-1*day), finding("top.go", "critical"), finding("top.go", "critical")),
		review("a", t0.Add(-1*day), finding("low.go", "nit")),
	} {
		if _, err := u.co.store.Add(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	u.signIn("eng")
	_, body := u.get("/ui/insights/hotspots?days=30")
	if strings.Contains(body, "No earlier data:") || !strings.Contains(body, `data-label="Trend"`) {
		t.Fatalf("repo a's rows have a baseline, so the Trend column must stay: the top row's repo decided for everyone")
	}
}
