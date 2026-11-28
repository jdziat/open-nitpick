package insights

import (
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// halfLife is how long a finding counts for half as much as it did when it was
// reported. A file that was bad once and was fixed should leave the top of the
// list; a file that is bad now should be on it.
const halfLife = 21 * 24 * time.Hour

// Query scopes the analytics. Now is passed in so a result does not depend on
// the clock.
type Query struct {
	Now  time.Time
	Days int
	Repo string
}

// Trend compares a file's findings in the window with the window before it.
type Trend string

// The trends. Flat means the same count.
const (
	TrendUp   Trend = "up"
	TrendDown Trend = "down"
	TrendFlat Trend = "flat"
)

// Hotspot is a file ranked by how much, how severely and how recently it has
// produced findings.
type Hotspot struct {
	Repo     string  `json:"repo"`
	Path     string  `json:"path"`
	Score    float64 `json:"score"`
	Findings int     `json:"findings"` // in the window
	Recent   int     `json:"recent"`   // same as Findings: the current window
	Prior    int     `json:"prior"`    // the window before
	Worst    string  `json:"worst"`
	Trend    Trend   `json:"trend"`
	// Baseline is true when a review ran in the window before, so a Prior of 0 is
	// a count and not the absence of data.
	Baseline bool `json:"baseline"`
	// Delta is Recent minus Prior: how many more findings than the window before.
	Delta int       `json:"delta"`
	Last  time.Time `json:"last"`
	// Spark is findings per bucket across the window, oldest first.
	Spark []int `json:"spark"`
}

// flatBand is how few findings of change still read as steady. A file that went
// from 3 to 4 findings is not an alarm, and colouring it as one teaches the reader
// to ignore the colour.
const flatBand = 1

// trendOf is the one rule for a file's direction: with nothing earlier to compare
// with, or a change within flatBand, it is flat.
func trendOf(recent, prior int, baseline bool) Trend {
	switch d := recent - prior; {
	case !baseline:
		return TrendFlat
	case d > flatBand:
		return TrendUp
	case d < -flatBand:
		return TrendDown
	}
	return TrendFlat
}

// SparkBuckets is how many points a hotspot's trend line has.
const SparkBuckets = 12

type fileKey struct{ repo, path string }

// window is the whole UTC days the period covers: Days calendar days ending with
// today, so the cards and the chart, which draws one bar per day, count the same
// reviews. The window before it is the Days days before that.
func (q Query) window() (from, priorFrom time.Time) {
	days := max(q.Days, 1)
	today := q.Now.UTC().Truncate(24 * time.Hour)
	from = today.AddDate(0, 0, -(days - 1))
	return from, from.AddDate(0, 0, -days)
}

func rank(sev string) int {
	for i, s := range Severities {
		if s == sev {
			return i
		}
	}
	return -1
}

// Hotspots ranks files by score. Only findings inside the window score and are
// listed; the window before it only decides the trend.
func (s *View) Hotspots(q Query) []Hotspot {
	from, priorFrom := q.window()
	byFile := map[fileKey]*Hotspot{}
	prior := map[fileKey]int{}
	baseline := map[string]bool{} // by repository: another repository's history is no baseline
	for _, r := range s.reviews {
		if q.Repo != "" && r.Repo != q.Repo || r.At.After(q.Now) || r.At.Before(priorFrom) {
			continue
		}
		if r.At.Before(from) {
			baseline[r.Repo] = true
		}
		for _, f := range r.Findings {
			k := fileKey{r.Repo, f.Path}
			if r.At.Before(from) {
				prior[k]++
				continue
			}
			h := byFile[k]
			if h == nil {
				h = &Hotspot{Repo: r.Repo, Path: f.Path, Worst: f.Severity, Spark: make([]int, SparkBuckets)}
				byFile[k] = h
			}
			h.Findings++
			h.Recent++
			h.Spark[min(int(r.At.Sub(from)*SparkBuckets/q.Now.Sub(from)), SparkBuckets-1)]++
			h.Score += severityWeight[f.Severity] * math.Pow(0.5, float64(q.Now.Sub(r.At))/float64(halfLife))
			if rank(f.Severity) > rank(h.Worst) {
				h.Worst = f.Severity
			}
			if r.At.After(h.Last) {
				h.Last = r.At
			}
		}
	}
	out := make([]Hotspot, 0, len(byFile))
	for k, h := range byFile {
		h.Prior = prior[k]
		h.Baseline = baseline[h.Repo]
		h.Trend = trendOf(h.Recent, h.Prior, h.Baseline)
		h.Delta = h.Recent - h.Prior
		out = append(out, *h)
	}
	// Files that fell out of the window entirely are not hotspots, but one that
	// had findings before and none now is the good news a team wants to see; it is
	// listed with Trend down only when it still has a finding in the window.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Point is one day of the series.
type Point struct {
	Day      time.Time `json:"day"`
	Reviews  int       `json:"reviews"`
	Findings int       `json:"findings"`
	Errors   int       `json:"errors"` // findings of error or critical severity
}

// Series has one point per day for the window, quiet days included as zeros, so
// a chart is not drawn across a gap that means "nothing happened".
func (s *View) Series(q Query) []Point {
	days := max(q.Days, 1)
	end := q.Now.UTC().Truncate(24 * time.Hour)
	pts := make([]Point, days)
	for i := range pts {
		pts[i].Day = end.AddDate(0, 0, -(days - 1 - i))
	}
	for _, r := range s.reviews {
		if q.Repo != "" && r.Repo != q.Repo || r.At.After(q.Now) {
			continue
		}
		i := days - 1 - int(end.Sub(r.At.UTC().Truncate(24*time.Hour))/(24*time.Hour))
		if i < 0 || i >= days {
			continue
		}
		pts[i].Reviews++
		pts[i].Findings += len(r.Findings)
		for _, f := range r.Findings {
			if rank(f.Severity) >= rank("error") {
				pts[i].Errors++
			}
		}
	}
	return pts
}

// Overview is the headline numbers of a window.
type Overview struct {
	Reviews  int `json:"reviews"`
	Findings int `json:"findings"`
	Errors   int `json:"errors"`
	Repos    int `json:"repos"`
	Files    int `json:"files"`
	// Incomplete counts reviews that did not finish. Their zero findings are not
	// a clean result, and the page says so.
	Incomplete int `json:"incomplete"`
	// Empty is true when the store holds no review at all in the window.
	Empty bool `json:"empty"`
	// BySeverity and ByClass count the window's findings.
	BySeverity map[string]int `json:"by_severity"`
	ByClass    []Count        `json:"by_class"`
}

// Count is one labelled number.
type Count struct {
	Label string `json:"label"`
	N     int    `json:"n"`
}

// Overview summarises the window.
func (s *View) Overview(q Query) Overview {
	from, _ := q.window()
	return s.overviewBetween(q.Repo, from, q.Now)
}

// PriorOverview summarises the window before the period, the same one the
// hotspot trends compare with, so a card and a trend never disagree about it.
func (s *View) PriorOverview(q Query) Overview {
	from, priorFrom := q.window()
	return s.overviewBetween(q.Repo, priorFrom, from.Add(-time.Nanosecond))
}

// overviewBetween counts reviews dated from the first time to the second, both
// inclusive.
func (s *View) overviewBetween(repo string, from, until time.Time) Overview {
	o := Overview{BySeverity: map[string]int{}}
	repos, files := map[string]bool{}, map[fileKey]bool{}
	classes := map[string]int{}
	for _, r := range s.reviews {
		if repo != "" && r.Repo != repo || r.At.Before(from) || r.At.After(until) {
			continue
		}
		o.Reviews++
		repos[r.Repo] = true
		if !r.Complete {
			o.Incomplete++
		}
		for _, f := range r.Findings {
			o.Findings++
			o.BySeverity[f.Severity]++
			if rank(f.Severity) >= rank("error") {
				o.Errors++
			}
			files[fileKey{r.Repo, f.Path}] = true
			classes[f.Class]++
		}
	}
	o.Repos, o.Files, o.Empty = len(repos), len(files), o.Reviews == 0
	for c, n := range classes {
		o.ByClass = append(o.ByClass, Count{c, n})
	}
	sort.Slice(o.ByClass, func(i, j int) bool {
		if o.ByClass[i].N != o.ByClass[j].N {
			return o.ByClass[i].N > o.ByClass[j].N
		}
		return o.ByClass[i].Label < o.ByClass[j].Label
	})
	return o
}

// RepoRow is one repo's numbers in the window.
type RepoRow struct {
	Repo     string    `json:"repo"`
	Reviews  int       `json:"reviews"`
	Findings int       `json:"findings"`
	Errors   int       `json:"errors"`
	Last     time.Time `json:"last"`
}

// Repos summarises each repo in the window, busiest first.
func (s *View) Repos(q Query) []RepoRow {
	from, _ := q.window()
	by := map[string]*RepoRow{}
	for _, r := range s.reviews {
		if q.Repo != "" && r.Repo != q.Repo || r.At.Before(from) || r.At.After(q.Now) {
			continue
		}
		row := by[r.Repo]
		if row == nil {
			row = &RepoRow{Repo: r.Repo}
			by[r.Repo] = row
		}
		row.Reviews++
		row.Findings += len(r.Findings)
		for _, f := range r.Findings {
			if rank(f.Severity) >= rank("error") {
				row.Errors++
			}
		}
		if r.At.After(row.Last) {
			row.Last = r.At
		}
	}
	out := make([]RepoRow, 0, len(by))
	for _, row := range by {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Findings != out[j].Findings {
			return out[i].Findings > out[j].Findings
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// FindingQuery filters the findings across reviews. Path matches a file exactly,
// or a directory when it ends in "/". Q is a text search over a finding's title,
// rationale, path and class, ignoring case.
type FindingQuery struct {
	Query
	Path        string
	Class       string
	MinSeverity string
	Q           string
	Limit       int
}

// FindingRow is a finding with where and when it was reported.
type FindingRow struct {
	Finding
	Repo     string    `json:"repo"`
	ReviewID string    `json:"review_id"`
	Commit   string    `json:"commit,omitempty"`
	At       time.Time `json:"at"`
}

// AllFindings returns every matching finding, newest first and, within one
// review, most severe first. It is what a page is cut from, so the whole set is
// sorted before any of it is shown.
func (s *View) AllFindings(q FindingQuery) []FindingRow {
	from, _ := q.window()
	floor := rank(q.MinSeverity)
	var rows []FindingRow
	for _, r := range s.reviews {
		if q.Repo != "" && r.Repo != q.Repo || r.At.Before(from) || r.At.After(q.Now) {
			continue
		}
		for _, f := range r.Findings {
			if q.Path != "" && !matchPath(q.Path, f.Path) {
				continue
			}
			if q.Class != "" && f.Class != q.Class || rank(f.Severity) < floor {
				continue
			}
			if !contains(q.Q, f.Title, f.Rationale, f.Path, f.Class) {
				continue
			}
			rows = append(rows, FindingRow{Finding: f, Repo: r.Repo, ReviewID: r.ID, Commit: r.Commit, At: r.At})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].At.Equal(rows[j].At) {
			return rows[i].At.After(rows[j].At)
		}
		return rank(rows[i].Severity) > rank(rows[j].Severity)
	})
	return rows
}

// Findings returns the newest matching findings, at most Limit, and how many
// matched, so a caller that was cut short can say so.
func (s *View) Findings(q FindingQuery) (rows []FindingRow, total int) {
	rows = s.AllFindings(q)
	total = len(rows)
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, total
}

// Facets are the values a filter can offer, from what was reported in the period.
type Facets struct {
	Repos      []string `json:"repos"`
	Classes    []string `json:"classes"`
	Severities []string `json:"severities"`
}

// Facets lists the repositories and classes seen in the period, sorted. The
// severities are the fixed scale, least severe first, whether or not each was seen.
func (s *View) Facets(q Query) Facets {
	from, _ := q.window()
	repos, classes := map[string]bool{}, map[string]bool{}
	for _, r := range s.reviews {
		if r.At.Before(from) || r.At.After(q.Now) {
			continue
		}
		repos[r.Repo] = true
		for _, f := range r.Findings {
			if f.Class != "" {
				classes[f.Class] = true
			}
		}
	}
	return Facets{Repos: sortedKeys(repos), Classes: sortedKeys(classes), Severities: slices.Clone(Severities)}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// matchPath is a file exactly, or everything under a directory that ends in "/".
func matchPath(want, got string) bool {
	return got == want || strings.HasSuffix(want, "/") && strings.HasPrefix(got, want)
}
