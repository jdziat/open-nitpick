package insights

import (
	"context"
	"testing"
	"time"
)

func seeded(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	add := func(r Review) {
		t.Helper()
		if _, err := s.Add(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	// hot.go: three errors this week. old.go: five errors, all two months ago.
	add(review("acme/api", t0.Add(-1*day), finding("hot.go", "error"), finding("hot.go", "error"), finding("quiet.go", "nit")))
	add(review("acme/api", t0.Add(-2*day), finding("hot.go", "error")))
	for i := 0; i < 5; i++ {
		add(Review{Repo: "acme/api", Commit: string(rune('a' + i)), At: t0.Add(-60 * day), Result: "findings", Complete: true,
			Findings: []Finding{finding("old.go", "error")}})
	}
	return s
}

func TestHotspotsRankRecentSevereFilesAboveOldOnes(t *testing.T) {
	s := seeded(t)
	hs := s.Hotspots(Query{Now: t0, Days: 90})
	if len(hs) < 2 || hs[0].Path != "hot.go" {
		t.Fatalf("ranking = %+v, want hot.go first", hs)
	}
	var old Hotspot
	for _, h := range hs {
		if h.Path == "old.go" {
			old = h
		}
	}
	if old.Findings != 5 || old.Score >= hs[0].Score {
		t.Fatalf("old.go has 5 findings but must score below hot.go's 3 recent ones: old=%+v top=%+v", old, hs[0])
	}
}

func TestHotspotTrendComparesThisPeriodWithThePrior(t *testing.T) {
	s := seeded(t)
	for _, h := range s.Hotspots(Query{Now: t0, Days: 30}) {
		if h.Path == "hot.go" && (h.Trend != TrendUp || h.Recent != 3 || h.Prior != 0) {
			t.Fatalf("hot.go = %+v, want up, 3 recent, 0 prior", h)
		}
	}
	for _, h := range s.Hotspots(Query{Now: t0, Days: 30}) {
		if h.Path == "old.go" {
			t.Fatalf("old.go is outside a 30-day window and must not be listed: %+v", h)
		}
	}
}

func TestHotspotTrendIsDownWhenAFileCalmsDown(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	day := 24 * time.Hour
	for i := 0; i < 4; i++ { // four findings 40 days ago, one 5 days ago
		_, _ = s.Add(context.Background(), Review{Repo: "r", Commit: string(rune('a' + i)), At: t0.Add(-40 * day), Result: "findings", Complete: true, Findings: []Finding{finding("calm.go", "error")}})
	}
	_, _ = s.Add(context.Background(), review("r", t0.Add(-5*day), finding("calm.go", "error")))
	hs := s.Hotspots(Query{Now: t0, Days: 30})
	if len(hs) != 1 || hs[0].Trend != TrendDown || hs[0].Recent != 1 || hs[0].Prior != 4 {
		t.Fatalf("calm.go = %+v, want down from 4 to 1", hs)
	}
}

func TestHotspotsAreScopedToARepo(t *testing.T) {
	s := seeded(t)
	_, _ = s.Add(context.Background(), review("acme/web", t0.Add(-time.Hour), finding("ui.go", "critical")))
	hs := s.Hotspots(Query{Now: t0, Days: 90, Repo: "acme/web"})
	if len(hs) != 1 || hs[0].Path != "ui.go" {
		t.Fatalf("repo scope leaked: %+v", hs)
	}
}

func TestSeriesHasOneBucketPerDayIncludingQuietOnes(t *testing.T) {
	s := seeded(t)
	pts := s.Series(Query{Now: t0, Days: 7})
	if len(pts) != 7 {
		t.Fatalf("got %d points, want 7: a quiet day is a zero, not a gap", len(pts))
	}
	var total int
	for _, p := range pts {
		total += p.Findings
	}
	if total != 4 {
		t.Fatalf("findings in the week = %d, want 4", total)
	}
}

func TestOverviewCountsOnlyTheWindowAndSaysWhenNothingIsHeld(t *testing.T) {
	s := seeded(t)
	o := s.Overview(Query{Now: t0, Days: 7})
	if o.Reviews != 2 || o.Findings != 4 || o.Repos != 1 {
		t.Fatalf("overview = %+v", o)
	}
	empty, _ := OpenStore(t.TempDir())
	defer func() { _ = empty.Close() }()
	if o := empty.Overview(Query{Now: t0, Days: 7}); o.Reviews != 0 || o.Empty != true {
		t.Fatalf("an empty store must say it is empty: %+v", o)
	}
}

func TestIncompleteReviewsAreCountedSoACleanRunIsNotMistakenForNoRun(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	r := review("acme/api", t0.Add(-time.Hour))
	r.Complete = false
	r.Result = "error"
	_, _ = s.Add(context.Background(), r)
	o := s.Overview(Query{Now: t0, Days: 7})
	if o.Incomplete != 1 || o.Findings != 0 {
		t.Fatalf("overview = %+v: zero findings from a review that did not finish must show as incomplete", o)
	}
}

func TestFindingsFilterBySeverityPathAndClassAndNameTheirReview(t *testing.T) {
	s := seeded(t)
	rows, total := s.Findings(FindingQuery{Query: Query{Now: t0, Days: 90}, Path: "hot.go", MinSeverity: "error", Limit: 10})
	if total != 3 || len(rows) != 3 {
		t.Fatalf("hot.go errors = %d rows of %d", len(rows), total)
	}
	if rows[0].ReviewID == "" || rows[0].Repo != "acme/api" || !rows[0].At.After(rows[2].At.Add(-1)) {
		t.Fatalf("rows must be newest first and name their review: %+v", rows)
	}
	if rows, _ := s.Findings(FindingQuery{Query: Query{Now: t0, Days: 90}, Path: "quiet.go", MinSeverity: "error", Limit: 10}); len(rows) != 0 {
		t.Fatalf("a nit passed an error floor: %+v", rows)
	}
}

func TestFindingsLimitReportsTheTotalSoATruncatedListIsNotTakenForAll(t *testing.T) {
	s := seeded(t)
	rows, total := s.Findings(FindingQuery{Query: Query{Now: t0, Days: 90}, Limit: 2})
	if len(rows) != 2 || total != 9 {
		t.Fatalf("got %d rows of %d, want 2 of 9", len(rows), total)
	}
}

func TestHotspotSparklineBucketsFindingsAcrossTheWindowOldestFirst(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	day := 24 * time.Hour
	_, _ = s.Add(context.Background(), review("r", t0.Add(-29*day), finding("f.go", "error")))
	_, _ = s.Add(context.Background(), review("r", t0.Add(-1*day), finding("f.go", "error"), finding("f.go", "nit")))
	hs := s.Hotspots(Query{Now: t0, Days: 30})
	if len(hs) != 1 || len(hs[0].Spark) != SparkBuckets {
		t.Fatalf("hotspots = %+v", hs)
	}
	sp := hs[0].Spark
	if sp[0] != 1 || sp[SparkBuckets-1] != 2 {
		t.Fatalf("spark = %v, want the oldest finding first and the two newest last", sp)
	}
	var sum int
	for _, n := range sp {
		sum += n
	}
	if sum != hs[0].Findings {
		t.Fatalf("spark sums to %d, findings %d: every finding in the window falls in a bucket", sum, hs[0].Findings)
	}
}

func TestHotspotBaselineIsKnownOnlyWhenTheEarlierWindowHeldAReview(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	day := 24 * time.Hour
	_, _ = s.Add(context.Background(), review("r", t0.Add(-2*day), finding("a.go", "error")))
	if hs := s.Hotspots(Query{Now: t0, Days: 30}); len(hs) != 1 || hs[0].Baseline {
		t.Fatalf("no review came before the window, so there is no baseline: %+v", hs)
	}
	_, _ = s.Add(context.Background(), review("r", t0.Add(-40*day), finding("other.go", "nit")))
	if hs := s.Hotspots(Query{Now: t0, Days: 30}); len(hs) != 1 || !hs[0].Baseline || hs[0].Prior != 0 {
		t.Fatalf("a review in the earlier window makes 0 a real count: %+v", hs)
	}
}

func TestAFindingOneHalfLifeOldScoresHalfAsMuchAsAFreshOne(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	_, _ = s.Add(context.Background(), review("old", t0.Add(-21*24*time.Hour), finding("f.go", "error")))
	_, _ = s.Add(context.Background(), review("new", t0, finding("f.go", "error")))
	byRepo := map[string]float64{}
	for _, h := range s.Hotspots(Query{Now: t0, Days: 60}) {
		byRepo[h.Repo] = h.Score
	}
	if byRepo["new"] < 2.99 || byRepo["new"] > 3.01 {
		t.Fatalf("a fresh error scores %v, want its weight 3", byRepo["new"])
	}
	if byRepo["old"] < 1.49 || byRepo["old"] > 1.51 {
		t.Fatalf("an error 21 days old scores %v, want half its weight, 1.5", byRepo["old"])
	}
}

func TestThePriorWindowIsExactlyOnePeriodLong(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	day := 24 * time.Hour
	_, _ = s.Add(context.Background(), review("r", t0.Add(-59*day), finding("f.go", "error")))                                                                                   // in the prior 30 days
	_, _ = s.Add(context.Background(), Review{Repo: "r", Commit: "z", At: t0.Add(-61 * day), Result: "findings", Complete: true, Findings: []Finding{finding("f.go", "error")}}) // before it
	_, _ = s.Add(context.Background(), review("r", t0.Add(-59*day-12*time.Hour), finding("g.go", "error")))                                                                      // just inside the far edge
	_, _ = s.Add(context.Background(), review("r", t0.Add(-day), finding("f.go", "error")))
	hs := s.Hotspots(Query{Now: t0, Days: 30})
	if len(hs) != 1 || hs[0].Prior != 1 {
		t.Fatalf("prior = %+v, want only the finding 59 days back: the one 61 days back is outside the window before", hs)
	}
}

func TestAFileIsComparedWithItsOwnRepositorysEarlierReviewsNotAnothers(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	day := 24 * time.Hour
	_, _ = s.Add(context.Background(), review("b", t0.Add(-40*day), finding("x.go", "nit")))
	_, _ = s.Add(context.Background(), review("a", t0.Add(-day), finding("new.go", "error")))
	for _, h := range s.Hotspots(Query{Now: t0, Days: 30}) {
		if h.Repo == "a" && h.Baseline {
			t.Fatalf("repo a had no review before the window, but a review in repo b gave it a baseline: %+v", h)
		}
	}
}

func TestTheEdgeOfThePriorWindowIsDrawnToTheMinute(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	day, near := 24*time.Hour, 10*time.Minute
	// The window before a 30 day period starts 60 days back.
	_, _ = s.Add(context.Background(), review("r", t0.Add(-60*day+near), finding("in.go", "error")))
	_, _ = s.Add(context.Background(), review("r", t0.Add(-60*day-near), finding("out.go", "error")))
	_, _ = s.Add(context.Background(), review("r", t0.Add(-day), finding("in.go", "error"), finding("out.go", "error")))
	prior := map[string]int{}
	for _, h := range s.Hotspots(Query{Now: t0, Days: 30}) {
		prior[h.Path] = h.Prior
	}
	if len(prior) != 2 {
		t.Fatalf("both files have a finding in the window and must be listed: %v", prior)
	}
	if prior["in.go"] != 1 || prior["out.go"] != 0 {
		t.Fatalf("prior = %v: a finding ten minutes inside the edge counts and one ten minutes outside does not", prior)
	}
}

func TestReposHonoursTheRepositoryFilterLikeEveryOtherQuery(t *testing.T) {
	s := seeded(t)
	_, _ = s.Add(context.Background(), review("acme/web", t0.Add(-time.Hour), finding("ui.go", "nit")))
	if rows := s.Repos(Query{Now: t0, Days: 90, Repo: "acme/web"}); len(rows) != 1 || rows[0].Repo != "acme/web" {
		t.Fatalf("a repo filter returned %+v", rows)
	}
	if rows := s.Repos(Query{Now: t0, Days: 90}); len(rows) != 2 {
		t.Fatalf("no filter must list every repo: %+v", rows)
	}
}

func TestHotspotsOfEqualScoreAreOrderedByRepoThenPathSoARefreshDoesNotShuffleThem(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	defer func() { _ = s.Close() }()
	for _, repo := range []string{"b", "a", "c"} {
		_, _ = s.Add(context.Background(), review(repo, t0, finding("same.go", "error")))
	}
	for i := 0; i < 20; i++ {
		hs := s.Hotspots(Query{Now: t0, Days: 30})
		if len(hs) != 3 || hs[0].Repo != "a" || hs[1].Repo != "b" || hs[2].Repo != "c" {
			t.Fatalf("equal scores came back as %v %v %v", hs[0].Repo, hs[1].Repo, hs[2].Repo)
		}
	}
}

func TestEverySeverityTheStoreAcceptsHasAWeightAndAnOrder(t *testing.T) {
	for i, sev := range Severities {
		if w, ok := severityWeight[sev]; !ok || w <= 0 {
			t.Errorf("%s has no weight", sev)
		}
		if i > 0 && severityWeight[sev] <= severityWeight[Severities[i-1]] {
			t.Errorf("%s must weigh more than %s", sev, Severities[i-1])
		}
	}
	if len(severityWeight) != len(Severities) {
		t.Errorf("%d weights for %d severities: the two lists have drifted", len(severityWeight), len(Severities))
	}
}
