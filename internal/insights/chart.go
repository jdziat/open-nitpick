package insights

import (
	"fmt"
	"strconv"
	"strings"
)

// Charts are drawn here, as numbers, and the page's template turns them into
// SVG. The page's Content-Security-Policy refuses inline script and style, so a
// chart cannot be drawn in the browser; every position is an attribute the
// server computed, and every colour is a class.

// Chart geometry, in the SVG's own units.
const (
	chartW   = 720.0
	chartH   = 220.0
	chartTop = 12.0
	chartBot = 196.0 // the baseline; below it sit the day labels
	chartL   = 40.0  // room for the y labels
	chartR   = 8.0
)

// Bar is one day: an errors segment on the baseline and the rest above it, with a
// gap of at least a pixel between neighbours.
type Bar struct {
	X, W           float64
	ErrY, ErrH     float64
	OtherY, OtherH float64
	// Label is the hover text, a sentence so it reads aloud as well as it shows.
	Label string
}

// Tick is a gridline with its label.
type Tick struct {
	Y     float64
	Label string
}

// XLabel is a day label under the plot.
type XLabel struct {
	X    float64
	Text string
}

// Chart is the daily findings chart.
type Chart struct {
	W, H, Top, Base, Left, Right float64
	Bars                         []Bar
	Ticks                        []Tick
	XLabels                      []XLabel
}

// niceMax is the top of the y axis: the smallest of 4, 8, 10, 20, 40, 100, 200,
// 400 ... that holds n. Round tops put the gridlines on round numbers.
func niceMax(n int) int {
	if n <= 4 {
		return 4
	}
	if n <= 8 {
		return 8
	}
	scale := 1
	for scale*10 < n {
		scale *= 10
	}
	for _, m := range []int{1, 2, 4, 10} {
		if m*scale >= n {
			return m * scale
		}
	}
	return 10 * scale
}

func dailyChart(pts []Point) Chart {
	most := 0
	for _, p := range pts {
		most = max(most, p.Findings)
	}
	top := niceMax(most)
	c := Chart{W: chartW, H: chartH, Top: chartTop, Base: chartBot, Left: chartL, Right: chartW - chartR}
	plot := c.Right - c.Left
	slot := plot / float64(max(len(pts), 1))
	gap := 2.0
	if slot < 5 {
		gap = 1
	}
	h := func(n int) float64 { return float64(n) / float64(top) * (c.Base - c.Top) }
	for i, p := range pts {
		other := p.Findings - p.Errors
		b := Bar{X: c.Left + float64(i)*slot + gap/2, W: slot - gap}
		b.ErrH, b.OtherH = h(p.Errors), h(other)
		b.ErrY = c.Base - b.ErrH
		b.OtherY = b.ErrY - b.OtherH
		b.Label = fmt.Sprintf("%s: %d %s, %d error or critical", p.Day.Format("Jan 2"), p.Findings, plural(p.Findings, "finding"), p.Errors)
		c.Bars = append(c.Bars, b)
	}
	for _, v := range []int{0, top / 2, top} {
		c.Ticks = append(c.Ticks, Tick{Y: c.Base - h(v), Label: strconv.Itoa(v)})
	}
	// About five labels, always including the first day and the last.
	step := max(len(pts)/5, 1)
	for i := 0; i < len(pts); i += step {
		if len(pts)-1-i < step/2 && i != 0 {
			break
		}
		c.XLabels = append(c.XLabels, XLabel{X: c.Left + (float64(i)+0.5)*slot, Text: pts[i].Day.Format("Jan 2")})
	}
	return c
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// Spark is a small trend line.
type Spark struct {
	Points string
	// Empty is true when every value is zero: there is no trend to draw.
	Empty bool
	W, H  float64
	// LastX, LastY place the dot on the newest point.
	LastX, LastY float64
}

const (
	sparkW = 96.0
	sparkH = 24.0
)

func sparkline(vals []int) Spark {
	s := Spark{W: sparkW, H: sparkH, Empty: true}
	most := 0
	for _, v := range vals {
		most = max(most, v)
	}
	if most == 0 || len(vals) < 2 {
		return s
	}
	s.Empty = false
	var b strings.Builder
	for i, v := range vals {
		x := 2 + float64(i)/float64(len(vals)-1)*(sparkW-4)
		y := sparkH - 3 - float64(v)/float64(most)*(sparkH-6)
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", x, y)
		s.LastX, s.LastY = x, y
	}
	s.Points = b.String()
	return s
}
