package insights

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// Pages implements server.Component. Every page needs the insights role and only
// reads: the pages never change or delete a review.
func (c *Component) Pages() []server.Page {
	need := server.Role(RoleView)
	return []server.Page{
		{Path: "", Title: "Overview", Nav: true, Need: need, Body: overviewBody, Data: c.overviewPage},
		{Path: "repos", Title: "Repositories", Nav: true, Need: need, Body: reposBody, Data: c.reposPage},
		{Path: "hotspots", Title: "Hotspots", Nav: true, Need: need, Body: hotspotsBody, Data: c.hotspotsPage},
		{Path: "findings", Title: "Findings", Nav: true, Need: need, Body: findingsBody, Data: c.findingsPage},
		{Path: "reviews", Title: "Reviews", Nav: true, Need: need, Body: reviewsBody, Data: c.reviewsPage},
		{Path: "reviews/{id}", Title: "Review", Need: need, Body: reviewBody, Data: c.reviewPage, Heading: reviewHeading},
	}
}

// shared is the template every body can call: the filter bar, severity marks and
// the empty state that tells a person how to fill the page.
const shared = `{{define "scope"}}<div class="scope">
<nav class="seg" aria-label="Period">{{range .Periods}}<a href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}>{{.Label}}</a>{{end}}</nav>
{{if .Repos}}<form method="get" action="{{.Action}}" class="repo-pick">
<label>Repository <select name="repo"><option value="">All repositories</option>{{range .Repos}}<option value="{{.}}"{{if eq . $.Repo}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<input type="hidden" name="days" value="{{.Days}}">
<button type="submit" class="quiet">Apply</button>
</form>{{end}}
</div>{{end}}
{{define "sev"}}<span class="sev sev-{{.}}">{{.}}</span>{{end}}
{{define "none"}}<div class="empty"><p><strong>{{.Title}}</strong></p><p class="muted">{{.Hint}}</p>{{if .Cmd}}<pre>{{.Cmd}}</pre>{{end}}</div>{{end}}
{{define "spark"}}{{if .Empty}}<span class="muted">none</span>{{else}}<svg class="spark" viewBox="0 0 {{.W}} {{.H}}" width="{{.W}}" height="{{.H}}" role="img" aria-label="{{.Title}}" focusable="false"><title>{{.Title}}</title><polyline points="{{.Points}}" fill="none" class="spark-line"/><circle cx="{{.LastX}}" cy="{{.LastY}}" r="2.5" class="spark-dot"/></svg>{{end}}{{end}}
`

const postHint = "nitpick review -format json -insights https://HOST -repo OWNER/NAME"

type periodLink struct {
	Label   string
	Href    string
	Current bool
}

// scope is the filter bar every page shares.
type scope struct {
	Days    int
	Repo    string
	Repos   []string
	Periods []periodLink
	Action  string
}

func (c *Component) scope(r *http.Request, base string, q Query) scope {
	s := scope{Days: q.Days, Repo: q.Repo, Action: base}
	seen := map[string]bool{}
	for _, rv := range c.store.Reviews(Filter{}) {
		if !seen[rv.Repo] {
			seen[rv.Repo] = true
			s.Repos = append(s.Repos, rv.Repo)
		}
	}
	sort.Strings(s.Repos)
	for _, d := range []int{7, 30, 90} {
		v := url.Values{"days": {strconv.Itoa(d)}}
		if q.Repo != "" {
			v.Set("repo", q.Repo)
		}
		s.Periods = append(s.Periods, periodLink{Label: strconv.Itoa(d) + " days", Href: base + "?" + v.Encode(), Current: d == q.Days})
	}
	return s
}

func (c *Component) pageQuery(r *http.Request) (Query, error) {
	q, err := c.parseQuery(r)
	if err != nil {
		return q, fmt.Errorf("%w: %w", server.ErrNotFound, err)
	}
	return q, nil
}

type empty struct{ Title, Hint, Cmd string }

func noReviews() empty {
	return empty{Title: "No reviews in this period.", Hint: "A review shows up here once nitpick posts its report to this server, with a write token in NITPICK_INSIGHTS_TOKEN.", Cmd: postHint}
}

// ---- overview ----

type card struct {
	Label, Value, Note string
	Dir                string // "up", "down" or "": for a count where more is worse
}

type bar struct {
	Label string
	N     int
	W     float64 // 0 to 100
	Class string
}

type overviewView struct {
	Scope      scope
	Empty      bool
	Cards      []card
	Chart      Chart
	Points     []Point
	Severities []bar
	Classes    []bar
	Hotspots   []hotspotRow
	NoTrend    bool
	NoPrior    bool
	Recent     []reviewRow
	Incomplete int
	Empties    empty
}

func delta(cur, prior int, priorHeld bool, days int) (note, dir string) {
	if !priorHeld {
		return "", ""
	}
	switch d := cur - prior; {
	case d > 0:
		return fmt.Sprintf("▲ %d more than the %d days before", d, days), "up"
	case d < 0:
		return fmt.Sprintf("▼ %d fewer than the %d days before", -d, days), "down"
	}
	return "same as the " + strconv.Itoa(days) + " days before", ""
}

func (c *Component) overviewPage(r *http.Request) (any, error) {
	q, err := c.pageQuery(r)
	if err != nil {
		return nil, err
	}
	o := c.store.Overview(q)
	v := overviewView{Scope: c.scope(r, "/ui/insights/", q), Empty: o.Empty, Empties: noReviews(), Incomplete: o.Incomplete}
	if o.Empty {
		return v, nil
	}
	prior := c.store.Overview(Query{Now: q.Now.AddDate(0, 0, -q.Days), Days: q.Days, Repo: q.Repo})
	// worse says whether more of this is bad. More reviews is activity, not a
	// problem, so it is never coloured.
	mk := func(label string, cur, was int, worse bool) card {
		n, d := delta(cur, was, !prior.Empty, q.Days)
		if !worse {
			d = ""
		}
		return card{Label: label, Value: strconv.Itoa(cur), Note: n, Dir: d}
	}
	v.Cards = []card{
		mk("Reviews", o.Reviews, prior.Reviews, false),
		mk("Findings", o.Findings, prior.Findings, true),
		mk("Error or critical", o.Errors, prior.Errors, true),
		{Label: "Repositories", Value: strconv.Itoa(o.Repos), Note: "reviewed in the period"},
		{Label: "Files with findings", Value: strconv.Itoa(o.Files), Note: "distinct, per repository"},
	}
	v.NoPrior = prior.Empty
	v.Points = c.store.Series(q)
	v.Chart = dailyChart(v.Points)
	most := 0
	for _, s := range Severities {
		most = max(most, o.BySeverity[s])
	}
	for i := len(Severities) - 1; i >= 0; i-- {
		s := Severities[i]
		v.Severities = append(v.Severities, bar{Label: s, N: o.BySeverity[s], W: pct(o.BySeverity[s], most), Class: "sev-" + s})
	}
	top := 0
	for _, k := range o.ByClass {
		top = max(top, k.N)
	}
	for i, k := range o.ByClass {
		if i == 6 {
			break
		}
		v.Classes = append(v.Classes, bar{Label: k.Label, N: k.N, W: pct(k.N, top), Class: "series-1"})
	}
	hs := c.store.Hotspots(q)
	if len(hs) > 5 {
		hs = hs[:5]
	}
	for i, h := range hs {
		v.Hotspots = append(v.Hotspots, c.hotspotRow(i+1, h, sparkScale(hs), q.Days))
	}
	v.NoTrend = noBaseline(hs)
	for i, rv := range c.store.Reviews(Filter{Repo: q.Repo, Until: q.Now}) {
		if i == 5 {
			break
		}
		v.Recent = append(v.Recent, c.reviewRow(rv))
	}
	return v, nil
}

func pct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return float64(n) / float64(of) * 100
}

const overviewBody = shared + `{{template "scope" .Data.Scope}}
{{if .Data.Empty}}{{template "none" .Data.Empties}}{{else}}
{{if .Data.Incomplete}}<p class="notice" role="note"><strong>Did not finish: {{.Data.Incomplete}}.</strong> A review that stopped early reports few or no findings, which is not a clean result. <a href="/ui/insights/reviews?days={{.Data.Scope.Days}}{{if .Data.Scope.Repo}}&amp;repo={{.Data.Scope.Repo}}{{end}}">See the reviews</a></p>{{end}}
<section aria-label="Totals" class="cards">{{range .Data.Cards}}<div class="card{{if .Dir}} card-{{.Dir}}{{end}}"><div class="card-label">{{.Label}}</div><div class="card-value">{{.Value}}</div><div class="card-note">{{.Note}}</div></div>{{end}}</section>
{{if .Data.NoPrior}}<p class="muted">No reviews came before this period, so there is nothing to compare these totals with.</p>{{end}}

<section class="panel" aria-labelledby="daily">
<h2 id="daily">Findings per day</h2>
<p class="legend"><span class="key key-err"></span>Error or critical <span class="key key-other"></span>Other findings <span class="muted">Scale: 0 to {{.Data.Chart.Max}} a day</span> <span class="muted only-sm">Scrolls sideways</span></p>
<div class="chart-scroll" tabindex="0" role="group" aria-label="Findings per day, scrollable"><svg class="chart" viewBox="0 0 {{.Data.Chart.W}} {{.Data.Chart.H}}" role="img" aria-labelledby="daily" preserveAspectRatio="xMidYMid meet">
{{range .Data.Chart.Ticks}}<line class="grid" x1="{{$.Data.Chart.Left}}" x2="{{$.Data.Chart.Right}}" y1="{{.Y}}" y2="{{.Y}}"/><text class="axis" x="{{$.Data.Chart.Left}}" dx="-6" y="{{.Y}}" dy="4" text-anchor="end">{{.Label}}</text>{{end}}
{{range .Data.Chart.Bars}}<g>{{if .OtherH}}<rect class="bar-other" x="{{.X}}" y="{{.OtherY}}" width="{{.W}}" height="{{.OtherH}}" rx="2"/>{{end}}{{if .ErrH}}<rect class="bar-err" x="{{.X}}" y="{{.ErrY}}" width="{{.W}}" height="{{.ErrH}}" rx="2"/>{{end}}<title>{{.Label}}</title></g>{{end}}
{{range .Data.Chart.XLabels}}<text class="axis" x="{{.X}}" y="214" text-anchor="middle">{{.Text}}</text>{{end}}
</svg></div>
<details class="data"><summary>Show as a table</summary>
<table class="records"><thead><tr><th>Day</th><th>Reviews</th><th>Findings</th><th>Error or critical</th></tr></thead><tbody>
{{range .Data.Points}}<tr><td>{{.Day.Format "Jan 2"}}</td><td>{{.Reviews}}</td><td>{{.Findings}}</td><td>{{.Errors}}</td></tr>{{end}}</tbody></table></details>
</section>

<div class="split">
<section class="panel" aria-labelledby="sevs"><h2 id="sevs">By severity</h2>
<ul class="bars">{{range .Data.Severities}}<li><span class="bar-label">{{.Label}}</span><svg class="bar-track" viewBox="0 0 100 8" preserveAspectRatio="none" aria-hidden="true"><rect class="track" width="100" height="8" rx="2"/>{{if .W}}<rect class="{{.Class}}" width="{{.W}}" height="8" rx="2"/>{{end}}</svg><span class="bar-n">{{.N}}</span></li>{{end}}</ul></section>
<section class="panel" aria-labelledby="cls"><h2 id="cls">By class</h2>
{{if .Data.Classes}}<ul class="bars">{{range .Data.Classes}}<li><span class="bar-label">{{.Label}}</span><svg class="bar-track" viewBox="0 0 100 8" preserveAspectRatio="none" aria-hidden="true"><rect class="track" width="100" height="8" rx="2"/><rect class="{{.Class}}" width="{{.W}}" height="8" rx="2"/></svg><span class="bar-n">{{.N}}</span></li>{{end}}</ul>{{else}}<p class="muted">No findings in this period.</p>{{end}}</section>
</div>

<section class="panel" aria-labelledby="hot"><h2 id="hot">Where findings concentrate</h2>
{{if .Data.Hotspots}}<table class="records"><thead><tr><th>File</th><th class="num">Findings</th><th>Worst</th>{{if not .Data.NoTrend}}<th>Trend</th>{{end}}<th>Over the period</th></tr></thead><tbody>
{{range .Data.Hotspots}}<tr><td data-label="File"><a href="{{.FindingsHref}}"><code>{{.Path}}</code></a>{{if .Repo}}<div class="muted">{{.Repo}}</div>{{end}}</td><td data-label="Findings" class="num">{{.Findings}}</td><td data-label="Worst">{{template "sev" .Worst}}</td>{{if not $.Data.NoTrend}}<td data-label="Trend"><span class="trend trend-{{.Trend}}">{{.TrendText}}</span></td>{{end}}<td data-label="Over the period">{{template "spark" .Spark}}</td></tr>{{end}}</tbody></table>
<p><a href="/ui/insights/hotspots?days={{.Data.Scope.Days}}{{if .Data.Scope.Repo}}&amp;repo={{.Data.Scope.Repo}}{{end}}">All hotspots</a></p>
{{else}}<p class="muted">No findings in this period.</p>{{end}}</section>

<section class="panel" aria-labelledby="rec"><h2 id="rec">Latest reviews</h2>
<table class="records"><thead><tr><th>When</th><th>Repository</th><th>Commit</th><th>Result</th><th class="num">Findings</th></tr></thead><tbody>
{{range .Data.Recent}}<tr><td data-label="When"><a href="{{.Href}}"><time datetime="{{.At}}">{{.At}}</time></a></td><td data-label="Repository">{{.Repo}}</td><td data-label="Commit"><code>{{.Short}}</code></td><td data-label="Result"{{if .Complete}} class="quiet"{{end}}>{{if not .Complete}}<span class="tag">did not finish</span>{{else if eq .Result "findings"}}<span class="muted" title="Finished with findings">–</span>{{else}}{{.Result}}{{end}}</td><td data-label="Findings" class="num">{{.Findings}}</td></tr>{{end}}</tbody></table>
<p><a href="/ui/insights/reviews?days={{.Data.Scope.Days}}{{if .Data.Scope.Repo}}&amp;repo={{.Data.Scope.Repo}}{{end}}">All reviews</a></p></section>
{{end}}`

// ---- hotspots ----

type hotspotRow struct {
	Rank         int
	Repo, Path   string
	Score        string
	Findings     int
	Recent       int
	Prior        int
	Worst        string
	Trend        Trend
	TrendText    string
	Spark        Spark
	FindingsHref string
	Last         string
}

func (c *Component) hotspotRow(rank int, h Hotspot, scale, days int) hotspotRow {
	v := url.Values{"path": {h.Path}, "repo": {h.Repo}, "days": {strconv.Itoa(days)}}
	trend, d, text := h.Trend, h.Recent-h.Prior, ""
	switch {
	case !h.Baseline:
		text = "no earlier data"
	case trend == TrendUp:
		text = fmt.Sprintf("▲ +%d (was %d)", d, h.Prior)
	case trend == TrendDown:
		text = fmt.Sprintf("▼ %d (was %d)", d, h.Prior)
	default:
		text = fmt.Sprintf("● steady (was %d)", h.Prior)
	}
	return hotspotRow{Rank: rank, Repo: h.Repo, Path: h.Path, Score: strconv.FormatFloat(h.Score, 'f', 1, 64), Findings: h.Findings, Recent: h.Recent, Prior: h.Prior,
		Worst: h.Worst, Trend: trend, TrendText: text, Spark: sparkline(h.Spark, scale), FindingsHref: "/ui/insights/findings?" + v.Encode(), Last: h.Last.Format(time.RFC3339)}
}

// noBaseline is true when there are rows and none has an earlier period to be
// compared with. One row with a baseline keeps the Trend column.
func noBaseline(hs []Hotspot) bool {
	for _, h := range hs {
		if h.Baseline {
			return false
		}
	}
	return len(hs) > 0
}

// sparkScale is the busiest bucket of any file in the rows.
func sparkScale(hs []Hotspot) int {
	most := 0
	for _, h := range hs {
		for _, n := range h.Spark {
			most = max(most, n)
		}
	}
	return most
}

type hotspotsView struct {
	NoTrend bool
	Scope   scope
	Rows    []hotspotRow
	Total   int
	Empties empty
	Empty   bool
}

func (c *Component) hotspotsPage(r *http.Request) (any, error) {
	q, err := c.pageQuery(r)
	if err != nil {
		return nil, err
	}
	hs := c.store.Hotspots(q)
	v := hotspotsView{NoTrend: noBaseline(hs), Scope: c.scope(r, "/ui/insights/hotspots", q), Total: len(hs), Empties: noReviews(), Empty: c.store.Overview(q).Empty}
	shown := hs[:min(len(hs), 100)]
	for i, h := range shown {
		v.Rows = append(v.Rows, c.hotspotRow(i+1, h, sparkScale(shown), q.Days))
	}
	return v, nil
}

const hotspotsBody = shared + `{{template "scope" .Data.Scope}}
{{if .Data.Empty}}{{template "none" .Data.Empties}}{{else}}
<p class="lede">Files ranked by the findings that reviews reported in the last {{.Data.Scope.Days}} days. Each review counts, so a finding left unfixed through several pushes counts each time.{{if not .Data.NoTrend}} Trend compares the period with the {{.Data.Scope.Days}} days before it.{{end}}</p>
{{if .Data.NoTrend}}<p class="muted">No earlier data: no reviews came before this period, so there is no comparison with the period before it.</p>{{end}}
<details class="data"><summary>How the score works</summary>
<p>Each finding adds a weight by severity (nit 0.25, info 0.5, warning 1, error 3, critical 8), halved for every 21 days since it was reported. A file with many old findings ranks below one with a few recent severe ones.</p></details>
{{if .Data.Rows}}<table class="records"><thead><tr><th class="num">#</th><th>File</th><th class="num">Score</th><th class="num">Findings</th><th>Worst</th>{{if not .Data.NoTrend}}<th>Trend</th>{{end}}<th>Over the period</th></tr></thead><tbody>
{{range .Data.Rows}}<tr><td data-label="Rank" class="num">{{.Rank}}</td><td data-label="File"><a href="{{.FindingsHref}}"><code>{{.Path}}</code></a><div class="muted">{{.Repo}}</div></td><td data-label="Score" class="num">{{.Score}}</td><td data-label="Findings" class="num">{{.Findings}}</td><td data-label="Worst">{{template "sev" .Worst}}</td>{{if not $.Data.NoTrend}}<td data-label="Trend"><span class="trend trend-{{.Trend}}">{{.TrendText}}</span></td>{{end}}<td data-label="Over the period">{{template "spark" .Spark}}</td></tr>{{end}}</tbody></table>
{{if gt .Data.Total (len .Data.Rows)}}<p class="muted">Showing the top {{len .Data.Rows}} of {{.Data.Total}} files.</p>{{end}}
{{else}}<p>Reviews ran in this period and reported no findings.</p>{{end}}{{end}}`

// ---- repositories ----

type reposView struct {
	Scope   scope
	Rows    []repoRow
	Empties empty
}

type repoRow struct {
	RepoRow
	Href string
	Last string
}

func (c *Component) reposPage(r *http.Request) (any, error) {
	q, err := c.pageQuery(r)
	if err != nil {
		return nil, err
	}
	v := reposView{Scope: c.scope(r, "/ui/insights/repos", q), Empties: noReviews()}
	v.Scope.Repos = nil // this page lists every repository; a filter would hide the rest
	listAll := q
	listAll.Repo = "" // this page lists every repository, so a stray ?repo= must not hide the rest
	for _, row := range c.store.Repos(listAll) {
		v.Rows = append(v.Rows, repoRow{RepoRow: row, Href: "/ui/insights/?days=" + strconv.Itoa(q.Days) + "&repo=" + url.QueryEscape(row.Repo), Last: row.Last.Format(time.RFC3339)})
	}
	return v, nil
}

const reposBody = shared + `{{template "scope" .Data.Scope}}
{{if .Data.Rows}}<table class="records"><thead><tr><th>Repository</th><th class="num">Reviews</th><th class="num">Findings</th><th class="num">Error or critical</th><th>Last review</th></tr></thead><tbody>
{{range .Data.Rows}}<tr><td data-label="Repository"><a href="{{.Href}}">{{.Repo}}</a></td><td data-label="Reviews" class="num">{{.Reviews}}</td><td data-label="Findings" class="num">{{.Findings}}</td><td data-label="Error or critical" class="num">{{.Errors}}</td><td data-label="Last review"><time datetime="{{.Last}}">{{.Last}}</time></td></tr>{{end}}</tbody></table>
{{else}}{{template "none" .Data.Empties}}{{end}}`

// ---- findings ----

type findingRowView struct {
	FindingRow
	When    string
	Href    string
	Where   string
	Short   string
	HotHref string
}

type findingsView struct {
	Scope       scope
	Rows        []findingRowView
	Total       int
	Path, Class string
	MinSeverity string
	Severities  []string
	Classes     []string
	Repos       []string
	Repo        string
	ClearHref   string
	WidenHref   string
	Filtered    bool
	Empties     empty
	Empty       bool
}

func (c *Component) findingsPage(r *http.Request) (any, error) {
	q, err := c.pageQuery(r)
	if err != nil {
		return nil, err
	}
	fq := FindingQuery{Query: q, Path: r.URL.Query().Get("path"), Class: r.URL.Query().Get("class"), MinSeverity: r.URL.Query().Get("min_severity"), Limit: 100}
	if fq.MinSeverity != "" && rank(fq.MinSeverity) < 0 {
		return nil, fmt.Errorf("%w: unknown severity", server.ErrNotFound)
	}
	rows, total := c.store.Findings(fq)
	v := findingsView{Scope: c.scope(r, "/ui/insights/findings", q), Total: total, Path: fq.Path, Class: fq.Class, MinSeverity: fq.MinSeverity,
		Severities: Severities, Filtered: fq.Path != "" || fq.Class != "" || fq.MinSeverity != "", Empties: noReviews(), Empty: c.store.Overview(q).Empty}
	v.Repo, v.Repos, v.Scope.Repos = q.Repo, v.Scope.Repos, nil // the form below holds the repository choice
	for _, k := range c.store.Overview(Query{Now: q.Now, Days: MaxDays, Repo: q.Repo}).ByClass {
		v.Classes = append(v.Classes, k.Label)
	}
	v.ClearHref = "/ui/insights/findings?" + url.Values{"days": {strconv.Itoa(q.Days)}}.Encode()
	if q.Days < 90 {
		w := url.Values{"days": {"90"}, "path": {fq.Path}, "class": {fq.Class}, "min_severity": {fq.MinSeverity}, "repo": {q.Repo}}
		v.WidenHref = "/ui/insights/findings?" + w.Encode()
	}
	for _, row := range rows {
		where := row.Path
		if row.Line > 0 {
			where += ":" + strconv.Itoa(row.Line)
		}
		v.Rows = append(v.Rows, findingRowView{FindingRow: row, When: row.At.Format(time.RFC3339), Href: "/ui/insights/reviews/" + row.ReviewID, Where: where, Short: short(row.Commit)})
	}
	return v, nil
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

const findingsBody = shared + `{{template "scope" .Data.Scope}}
{{if .Data.Empty}}{{template "none" .Data.Empties}}{{else}}
<form class="filters grid" method="get" action="/ui/insights/findings">
<label>Repository <select name="repo"><option value="">All repositories</option>{{range .Data.Repos}}<option value="{{.}}"{{if eq . $.Data.Repo}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<label>File or directory <input name="path" value="{{.Data.Path}}" placeholder="pkg/auth/ or pkg/a.go"></label>
<label class="half">Class <select name="class"><option value="">any class</option>{{range .Data.Classes}}<option value="{{.}}"{{if eq . $.Data.Class}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<label class="half">At least <select name="min_severity"><option value="">any severity</option>{{range .Data.Severities}}<option value="{{.}}"{{if eq . $.Data.MinSeverity}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<input type="hidden" name="days" value="{{.Data.Scope.Days}}">
<button type="submit">Filter</button>{{if and .Data.Filtered .Data.Rows}} <a href="{{.Data.ClearHref}}">Clear filters</a>{{end}}
</form>
{{if .Data.Rows}}<p class="muted">{{if gt .Data.Total (len .Data.Rows)}}Showing the newest {{len .Data.Rows}} of {{.Data.Total}} findings. Narrow the filters to reach the rest.{{else}}{{.Data.Total}} finding{{if ne .Data.Total 1}}s{{end}}.{{end}}</p>
<table class="records findings"><thead><tr><th>Severity</th><th>Finding</th><th>Repository</th><th>When</th></tr></thead><tbody>
{{range .Data.Rows}}<tr><td data-label="Severity">{{template "sev" .Severity}}</td><td data-label="Finding" class="wide"><strong>{{.Title}}</strong><div><code>{{.Where}}</code>{{if .Class}} <span class="muted">· {{.Class}}</span>{{end}}</div>{{if .Rationale}}<details class="data"><summary>Why</summary><p>{{.Rationale}}</p></details>{{end}}</td><td data-label="Repository">{{.Repo}}<div class="muted"><a href="{{.Href}}"><code>{{.Short}}</code></a></div></td><td data-label="When"><time datetime="{{.When}}">{{.When}}</time></td></tr>{{end}}</tbody></table>
{{else if .Data.Filtered}}<div class="empty"><p><strong>No finding matches these filters in the last {{.Data.Scope.Days}} days.</strong></p>
<p class="muted">The period had reviews, so this is a result and not a gap.</p>
<p>{{if .Data.WidenHref}}<a class="button" href="{{.Data.WidenHref}}">Widen to 90 days</a> {{end}}<a href="{{.Data.ClearHref}}">Clear filters</a></p></div>
{{else}}<p>Reviews ran in this period and reported no findings.</p>{{end}}{{end}}`

// ---- reviews ----

type reviewRow struct {
	Review
	Href     string
	Short    string
	Findings int
	At       string
}

func (c *Component) reviewRow(rv Review) reviewRow {
	return reviewRow{Review: rv, Href: "/ui/insights/reviews/" + rv.ID, Short: short(rv.Commit), Findings: len(rv.Findings), At: rv.At.Format(time.RFC3339)}
}

type reviewsView struct {
	Scope   scope
	Rows    []reviewRow
	Total   int
	Empties empty
}

func (c *Component) reviewsPage(r *http.Request) (any, error) {
	q, err := c.pageQuery(r)
	if err != nil {
		return nil, err
	}
	from, _ := q.window()
	all := c.store.Reviews(Filter{Repo: q.Repo, Since: from, Until: q.Now})
	v := reviewsView{Scope: c.scope(r, "/ui/insights/reviews", q), Total: len(all), Empties: noReviews()}
	for i, rv := range all {
		if i == 100 {
			break
		}
		v.Rows = append(v.Rows, c.reviewRow(rv))
	}
	return v, nil
}

const reviewsBody = shared + `{{template "scope" .Data.Scope}}
{{if .Data.Rows}}<table class="records"><thead><tr><th>When</th><th>Repository</th><th>Ref</th><th>Commit</th><th>Result</th><th class="num">Findings</th></tr></thead><tbody>
{{range .Data.Rows}}<tr><td data-label="When"><a href="{{.Href}}"><time datetime="{{.At}}">{{.At}}</time></a></td><td data-label="Repository">{{.Repo}}</td><td data-label="Ref">{{.Ref}}</td><td data-label="Commit"><code>{{.Short}}</code></td><td data-label="Result"{{if .Complete}} class="quiet"{{end}}>{{if not .Complete}}<span class="tag">did not finish</span>{{else if eq .Result "findings"}}<span class="muted" title="Finished with findings">–</span>{{else}}{{.Result}}{{end}}</td><td data-label="Findings" class="num">{{.Findings}}</td></tr>{{end}}</tbody></table>
{{if gt .Data.Total (len .Data.Rows)}}<p class="muted">Showing the newest {{len .Data.Rows}} of {{.Data.Total}} reviews.</p>{{end}}
{{else}}{{template "none" .Data.Empties}}{{end}}`

type reviewView struct {
	Review
	At       string
	Findings []Finding
	Count    int
}

// reviewHeading names a review by what it reviewed: the repository and the pull
// request, or the commit when there is none.
func reviewHeading(data any) string {
	v, ok := data.(reviewView)
	if !ok {
		return ""
	}
	what := v.Ref
	if what == "" {
		what = short(v.Commit)
	}
	if what == "" {
		return v.Repo
	}
	return v.Repo + " · " + what
}

func (c *Component) reviewPage(r *http.Request) (any, error) {
	rv, ok := c.store.Review(r.PathValue("id"))
	if !ok {
		return nil, server.ErrNotFound
	}
	return reviewView{Review: rv, At: rv.At.Format(time.RFC3339), Findings: rv.Findings, Count: len(rv.Findings)}, nil
}

const reviewBody = shared + `<p><a href="/ui/insights/reviews">All reviews</a></p>
<dl class="facts">
<dt>Repository</dt><dd>{{.Data.Repo}}</dd>
{{if .Data.Ref}}<dt>Ref</dt><dd>{{.Data.Ref}}</dd>{{end}}
{{if .Data.Commit}}<dt>Commit</dt><dd><code>{{.Data.Commit}}</code></dd>{{end}}
<dt>Reported</dt><dd><time datetime="{{.Data.At}}">{{.Data.At}}</time></dd>
<dt>Result</dt><dd>{{if .Data.Complete}}{{.Data.Result}}{{else}}<span class="tag">did not finish</span>{{end}}</dd>
<dt>Findings</dt><dd>{{.Data.Count}}{{if .Data.Truncated}}, and {{.Data.Truncated}} more that were not kept{{end}}</dd>
{{if .Data.Withheld}}<dt>Withheld</dt><dd>{{.Data.Withheld}} drafted and held back by a later stage</dd>{{end}}
{{if .Data.Source}}<dt>Posted by</dt><dd>{{.Data.Source}}</dd>{{end}}
</dl>
{{if not .Data.Complete}}<p class="error" role="note">This review stopped early, so its findings may be incomplete.</p>{{end}}
<h2>Findings</h2>
{{range .Data.Findings}}<section class="answer"><h3>{{template "sev" .Severity}} {{.Title}}</h3><p><code>{{.Path}}{{if .Line}}:{{.Line}}{{end}}</code>{{if .Class}} <span class="muted">· {{.Class}}</span>{{end}}</p>{{if .Rationale}}<p>{{.Rationale}}</p>{{end}}</section>
{{else}}<p>{{if .Data.Complete}}The review finished and reported no findings.{{else}}No findings were reported before the review stopped, which is not a clean result.{{end}}</p>{{end}}`
