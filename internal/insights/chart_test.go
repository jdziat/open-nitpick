package insights

import (
	"strings"
	"testing"
	"time"
)

func TestNiceMaxRoundsUpToAReadableTick(t *testing.T) {
	for in, want := range map[int]int{0: 4, 1: 4, 4: 4, 5: 8, 9: 10, 11: 20, 37: 40, 101: 200, 960: 1000} {
		if got := niceMax(in); got != want {
			t.Errorf("niceMax(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestDailyChartDrawsAZeroDayAsNoBarAndKeepsItsSlot(t *testing.T) {
	pts := []Point{{Day: t0, Findings: 0}, {Day: t0.AddDate(0, 0, 1), Findings: 6, Errors: 2}, {Day: t0.AddDate(0, 0, 2), Findings: 3, Errors: 3}}
	c := dailyChart(pts)
	if len(c.Bars) != 3 {
		t.Fatalf("%d bars", len(c.Bars))
	}
	if c.Bars[0].OtherH != 0 || c.Bars[0].ErrH != 0 {
		t.Fatalf("a zero day drew %+v", c.Bars[0])
	}
	b := c.Bars[1]
	if b.ErrH <= 0 || b.OtherH <= 0 || b.ErrH >= b.OtherH+b.ErrH {
		t.Fatalf("a day with 2 errors of 6 must draw both segments: %+v", b)
	}
	if b.ErrY+b.ErrH != c.Base || b.OtherY+b.OtherH != b.ErrY {
		t.Fatalf("segments must stack from the baseline: %+v base %v", b, c.Base)
	}
	if got := c.Bars[2]; got.OtherH != 0 || got.ErrH == 0 {
		t.Fatalf("a day of only errors draws only the error segment: %+v", got)
	}
	if !strings.Contains(c.Bars[1].Label, "6 findings") || !strings.Contains(c.Bars[1].Label, "2 error") {
		t.Fatalf("the hover text must carry the numbers: %q", c.Bars[1].Label)
	}
}

func TestDailyChartHeightScalesToTheBusiestDayNotTheRoundedAxis(t *testing.T) {
	c := dailyChart([]Point{{Day: t0, Findings: 10, Errors: 0}})
	top := c.Bars[0].OtherH
	if c.Ticks[len(c.Ticks)-1].Label != "10" {
		t.Fatalf("ticks = %+v", c.Ticks)
	}
	if want := c.Base - c.Top; top != want {
		t.Fatalf("a bar at the axis maximum must reach the top: h %v want %v", top, want)
	}
}

func TestSparklinePointsRiseWithTheCountsAndStayInTheBox(t *testing.T) {
	s := sparkline([]int{0, 2, 4, 0})
	if s.Empty || len(s.Points) == 0 {
		t.Fatalf("%+v", s)
	}
	if !sparkline([]int{0, 0, 0}).Empty {
		t.Fatalf("a flat zero line must say it is empty, not draw one")
	}
}

func TestAxisLabelsAreSparseAndAnchoredToRealDays(t *testing.T) {
	var pts []Point
	for i := 0; i < 30; i++ {
		pts = append(pts, Point{Day: time.Date(2026, 10, 1+i, 0, 0, 0, 0, time.UTC)})
	}
	c := dailyChart(pts)
	if n := len(c.XLabels); n < 3 || n > 7 {
		t.Fatalf("%d x labels for 30 days", n)
	}
	if c.XLabels[0].Text != "Oct 1" {
		t.Fatalf("first label %q", c.XLabels[0].Text)
	}
}
