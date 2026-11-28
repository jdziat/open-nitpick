package evals

// This study asks what review.max_design_tasks should be and how its tasks
// should be ranked. It makes no model call: a wide pull request is built by
// merging planted-defect Go fixtures into one module, the real planner and
// PackDesign run over it, and a defect counts as kept when its file is a source
// of a task that still has a request.
//
//	NITPICK_CAP_STUDY=1 go test -count=1 -run TestCapStudy -v ./internal/evals/
//
// What it cannot say: whether a real defect sits in a busy task. A fixture's own
// diff size is what makes a task busy here, so the numbers bound how the ranking
// behaves on this corpus, not on pull requests in general.

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/bundle"
	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/practices"
	"github.com/nitpick-oss/nitpick/internal/standards"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

const wideModule = "example.com/wide"

// capPool is the fixtures that can be merged: Go-only changes, outside the
// held-out corpus, every defect in a Go file.
func capPool() []Fixture {
	var all []Fixture
	all = append(all, Fixtures()...)
	all = append(all, CallerFixtures()...)
	all = append(all, MultiFileFixtures()...)
	all = append(all, InfoFixtures()...)
	all = append(all, KnowledgeFixtures()...)
	all = append(all, SlopFixtures()...)
	seen := map[string]bool{}
	var pool []Fixture
	for _, f := range all {
		if seen[f.Name] || HeldOut(f.Name) {
			continue
		}
		seen[f.Name] = true
		changedGo := false
		for path, content := range f.Head {
			if strings.HasSuffix(path, ".go") && f.Base[path] != content {
				changedGo = true
			}
		}
		ok := changedGo
		for _, d := range f.Defects {
			if !strings.HasSuffix(d.Path, ".go") {
				ok = false
			}
		}
		if ok {
			pool = append(pool, f)
		}
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Name < pool[j].Name })
	return pool
}

// mergeFixtures puts each fixture under its own directory of one module.
func mergeFixtures(parts []Fixture) Fixture {
	out := Fixture{Name: "wide", Base: map[string]string{"go.mod": "module " + wideModule + "\n\ngo 1.25\n"},
		Head: map[string]string{}}
	for i, f := range parts {
		prefix := fmt.Sprintf("fx%02d/", i)
		module := ""
		for _, files := range []map[string]string{f.Base, f.Head, f.Extra} {
			if mod, ok := files["go.mod"]; ok {
				for _, line := range strings.Split(mod, "\n") {
					if rest, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
						module = strings.TrimSpace(rest)
					}
				}
			}
		}
		rewrite := func(path, content string) string {
			if module == "" || !strings.HasSuffix(path, ".go") {
				return content
			}
			target := wideModule + "/" + strings.TrimSuffix(prefix, "/")
			content = strings.ReplaceAll(content, `"`+module+`/`, `"`+target+`/`)
			return strings.ReplaceAll(content, `"`+module+`"`, `"`+target+`"`)
		}
		copyGo := func(dst map[string]string, src map[string]string) {
			for path, content := range src {
				if strings.HasSuffix(path, ".go") {
					dst[prefix+path] = rewrite(path, content)
				}
			}
		}
		copyGo(out.Base, f.Extra)
		copyGo(out.Base, f.Base)
		copyGo(out.Head, f.Head)
		for _, d := range f.Defects {
			d.Path = prefix + d.Path
			out.Defects = append(out.Defects, d)
		}
	}
	return out
}

// holds reports the planted lines a design task's focus span contains. A package
// task has no span and holds every line of its sources.
func holds(task practices.DesignTask, defects map[string][]int) []string {
	var out []string
	if len(task.Focus) == 0 {
		for _, source := range task.Sources {
			for _, line := range defects[source.ID] {
				out = append(out, fmt.Sprintf("%s:%d", source.ID, line))
			}
		}
		return out
	}
	for _, span := range task.Focus {
		for _, line := range defects[span.Path] {
			if line >= span.Start-anchorTolerance && line <= span.End+anchorTolerance {
				out = append(out, fmt.Sprintf("%s:%d", span.Path, line))
			}
		}
	}
	return out
}

// taskFeat is what a ranker may look at for one design task.
type taskFeat struct {
	ID          string
	Sources     []string
	Obligations int
	Added       int
	Removed     int
	SpanAdded   int // added lines inside the task's own focus spans
	SpanTouched int // lines inside the focus spans that the change added or removed next to
	Modified    bool
	Defective   bool // for the oracle only
}

func features(plan practices.DesignPlan, changes diff.Files, defects map[string][]int) []taskFeat {
	byPath := map[string]*diff.File{}
	for _, file := range changes {
		byPath[file.Path] = file
	}
	var out []taskFeat
	for _, task := range plan.Tasks {
		if task.SlopOnly {
			continue
		}
		feat := taskFeat{ID: task.ID, Obligations: len(task.Interactions), Modified: true}
		for _, source := range task.Sources {
			feat.Sources = append(feat.Sources, source.ID)
			file := byPath[source.ID]
			if file == nil {
				continue
			}
			if file.Kind != diff.ChangeModified {
				feat.Modified = false
			}
			for _, hunk := range file.Hunks {
				for _, line := range hunk.Lines {
					switch line.Kind {
					case diff.LineAdded:
						feat.Added++
					case diff.LineRemoved:
						feat.Removed++
					}
				}
			}
		}
		for _, span := range task.Focus {
			file := byPath[span.Path]
			if file == nil {
				continue
			}
			for _, hunk := range file.Hunks {
				for i, line := range hunk.Lines {
					if line.Kind == diff.LineAdded && line.NewLine >= span.Start && line.NewLine <= span.End {
						feat.SpanAdded++
					}
					// A removed line has no new line number; it belongs to the span of the
					// added or context line that follows it.
					if line.Kind == diff.LineRemoved {
						for _, next := range hunk.Lines[i+1:] {
							if next.NewLine > 0 {
								if next.NewLine >= span.Start && next.NewLine <= span.End {
									feat.SpanTouched++
								}
								break
							}
						}
					}
				}
			}
		}
		feat.SpanTouched += feat.SpanAdded
		feat.Defective = len(holds(task, defects)) > 0
		out = append(out, feat)
	}
	return out
}

func desc(a, b int) int { return b - a }

type ranker struct {
	name string
	less func(a, b taskFeat) int
}

func rankers() []ranker {
	byID := func(a, b taskFeat) int { return strings.Compare(a.ID, b.ID) }
	then := func(cs ...int) int {
		for _, c := range cs {
			if c != 0 {
				return c
			}
		}
		return 0
	}
	return []ranker{
		{"current", func(a, b taskFeat) int {
			return then(desc(a.SpanTouched, b.SpanTouched), desc(a.Obligations, b.Obligations), len(a.Sources)-len(b.Sources), byID(a, b))
		}},
		{"previous", func(a, b taskFeat) int {
			return then(desc(a.Obligations, b.Obligations), desc(a.Added, b.Added), len(a.Sources)-len(b.Sources), byID(a, b))
		}},
		{"added-only", func(a, b taskFeat) int { return then(desc(a.Added, b.Added), byID(a, b)) }},
		{"total-churn", func(a, b taskFeat) int { return then(desc(a.Added+a.Removed, b.Added+b.Removed), byID(a, b)) }},
		{"mira-like", func(a, b taskFeat) int {
			ma, mb := 1, 1
			if a.Modified {
				ma = 0
			}
			if b.Modified {
				mb = 0
			}
			return then(ma-mb, desc(a.Added+a.Removed, b.Added+b.Removed), byID(a, b))
		}},
		{"span-churn", func(a, b taskFeat) int { return then(desc(a.SpanTouched, b.SpanTouched), byID(a, b)) }},
		{"span-added", func(a, b taskFeat) int { return then(desc(a.SpanAdded, b.SpanAdded), byID(a, b)) }},
		{"obligations", func(a, b taskFeat) int { return then(desc(a.Obligations, b.Obligations), byID(a, b)) }},
		{"smallest-first", func(a, b taskFeat) int { return then(len(a.Sources)-len(b.Sources), byID(a, b)) }},
		{"largest-first", func(a, b taskFeat) int { return then(desc(len(a.Sources), len(b.Sources)), byID(a, b)) }},
		{"oracle", func(a, b taskFeat) int {
			da, db := 1, 1
			if a.Defective {
				da = 0
			}
			if b.Defective {
				db = 0
			}
			return then(da-db, byID(a, b))
		}},
	}
}

type outcome struct{ kept, covered, tokens int }

type widePR struct {
	plan    practices.DesignPlan
	files   []standards.File
	changes diff.Files
	defects map[string][]int
	feats   []taskFeat
}

func buildWide(t *testing.T, parts []Fixture) (widePR, bool) {
	t.Helper()
	merged := mergeFixtures(parts)
	dir := t.TempDir()
	if err := buildRepo(dir, merged); err != nil {
		t.Fatalf("build repo: %v", err)
	}
	raw, err := vcs.NewLocal(dir, io.Discard).Diff(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	changes, err := diff.Parse(raw)
	if err != nil || len(changes) == 0 {
		return widePR{}, false
	}
	heads := map[string]string{}
	for path, content := range merged.Base {
		heads[path] = content
	}
	for path, content := range merged.Head {
		heads[path] = content
	}
	var files []standards.File
	for path, content := range heads {
		files = append(files, standards.File{Path: path, Src: []byte(content)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	inventory, _ := practices.InspectDesign(files, nil)
	var scope []string
	for _, file := range changes {
		scope = append(scope, file.Path)
	}
	plan := practices.PlanDesignInteractions(context.Background(), inventory, files, scope)
	defects := map[string][]int{}
	for _, d := range merged.Defects {
		defects[d.Path] = append(defects[d.Path], d.Line)
	}
	return widePR{plan: plan, files: files, changes: changes, defects: defects, feats: features(plan, changes, defects)}, true
}

// run packs the given tasks with no cap and counts what has a request.
func (w widePR) run(tasks []practices.DesignTask, cap int) (outcome, practices.DesignPacking) {
	cfg := config.Defaults()
	cfg.Review.MaxDesignTasks = cap
	design := w.plan
	design.Tasks = tasks
	packed := practices.PackDesign(context.Background(), cfg, design, w.files, w.changes, bundle.Reserve{})
	slop := map[string]bool{}
	for _, task := range packed.Design.Tasks {
		if task.SlopOnly {
			slop[task.ID] = true
		}
	}
	batched := map[string]bool{}
	var res outcome
	for _, batch := range packed.Plan.Batches {
		designed := false
		for _, id := range batch.DesignTaskIDs() {
			if !slop[id] {
				batched[id] = true
				designed = true
			}
		}
		if designed {
			res.tokens += batch.Tokens
		}
	}
	covered := map[string]bool{}
	for _, task := range packed.Design.Tasks {
		if task.SlopOnly || !batched[task.ID] {
			continue
		}
		res.kept++
		for _, held := range holds(task, w.defects) {
			covered[held] = true
		}
	}
	res.covered = len(covered)
	return res, packed
}

func (w widePR) tasksFor(ids map[string]bool) []practices.DesignTask {
	var out []practices.DesignTask
	for _, task := range w.plan.Tasks {
		if task.SlopOnly || ids[task.ID] {
			out = append(out, task)
		}
	}
	return out
}

func TestCapStudy(t *testing.T) {
	if os.Getenv("NITPICK_CAP_STUDY") != "1" {
		t.Skip("set NITPICK_CAP_STUDY=1 to run the design-task cap study")
	}
	pool := capPool()
	t.Logf("pool: %d Go fixtures, %d with planted defects", len(pool), countDefective(pool))
	rng := rand.New(rand.NewPCG(7, 11))
	sizes := []int{6, 10, 14}
	caps := []int{5, 8, 12, 16, 24, 32}
	perSize := 50
	rks := rankers()

	type key struct {
		size, cap int
		ranker    string
	}
	retention := map[key][]float64{}
	saving := map[key][]float64{}
	keptShare := map[key][]float64{}
	productionAgrees, productionTrials := 0, 0
	taskCounts := map[int][]float64{}

	for _, size := range sizes {
		done := 0
		for attempts := 0; done < perSize && attempts < perSize*4; attempts++ {
			order := rng.Perm(len(pool))
			var parts []Fixture
			defective := 0
			for _, i := range order[:size] {
				parts = append(parts, pool[i])
				if len(pool[i].Defects) > 0 {
					defective++
				}
			}
			// A pull request with nothing planted cannot separate the rankers.
			if defective < 2 {
				continue
			}
			w, ok := buildWide(t, parts)
			if !ok || len(w.feats) < 3 {
				continue
			}
			base, _ := w.run(w.plan.Tasks, 0)
			if base.covered == 0 {
				continue
			}
			done++
			taskCounts[size] = append(taskCounts[size], float64(len(w.feats)))
			for _, cap := range caps {
				if cap >= len(w.feats) {
					continue
				}
				for _, rk := range rks {
					var sel []taskFeat
					reps := 1
					if rk.name == "random" {
						reps = 1
					}
					_ = reps
					sel = slices.Clone(w.feats)
					slices.SortStableFunc(sel, rk.less)
					keep := map[string]bool{}
					for _, f := range sel[:cap] {
						keep[f.ID] = true
					}
					got, _ := w.run(w.tasksFor(keep), 0)
					k := key{size, cap, rk.name}
					retention[k] = append(retention[k], float64(got.covered)/float64(base.covered))
					saving[k] = append(saving[k], 1-float64(got.tokens)/float64(max(1, base.tokens)))
					keptShare[k] = append(keptShare[k], float64(cap)/float64(len(w.feats)))

					if rk.name == "current" {
						// The replica must keep what the shipped cap keeps.
						prod, packed := w.run(w.plan.Tasks, cap)
						productionTrials++
						var shipped []string
						for _, task := range packed.Design.Tasks {
							if !task.SlopOnly {
								shipped = append(shipped, task.ID)
							}
						}
						var mine []string
						for id := range keep {
							mine = append(mine, id)
						}
						slices.Sort(shipped)
						slices.Sort(mine)
						if slices.Equal(shipped, mine) && prod.covered == got.covered {
							productionAgrees++
						}
					}
				}
				// Random: twenty draws, averaged per pull request.
				var rr, rs float64
				for draw := 0; draw < 20; draw++ {
					perm := rng.Perm(len(w.feats))
					keep := map[string]bool{}
					for _, i := range perm[:cap] {
						keep[w.feats[i].ID] = true
					}
					got, _ := w.run(w.tasksFor(keep), 0)
					rr += float64(got.covered) / float64(base.covered)
					rs += 1 - float64(got.tokens)/float64(max(1, base.tokens))
				}
				k := key{size, cap, "random"}
				retention[k] = append(retention[k], rr/20)
				saving[k] = append(saving[k], rs/20)
				keptShare[k] = append(keptShare[k], float64(cap)/float64(len(w.feats)))
			}
		}
		sort.Float64s(taskCounts[size])
		tc := taskCounts[size]
		t.Logf("size %d: %d pull requests, design tasks median %.0f, min %.0f, max %.0f", size, done, tc[len(tc)/2], tc[0], tc[len(tc)-1])
	}
	t.Logf("shipped cap agrees with the replica of 'current' on %d of %d trials", productionAgrees, productionTrials)

	names := []string{"oracle", "current", "previous", "added-only", "total-churn", "mira-like", "span-churn", "span-added", "obligations", "smallest-first", "random"}
	var b strings.Builder
	for _, size := range sizes {
		fmt.Fprintf(&b, "\nPR of %d merged fixtures: mean share of planted defects still reviewed (mean token saving)\n", size)
		fmt.Fprintf(&b, "%-15s", "ranker")
		for _, cap := range caps {
			fmt.Fprintf(&b, "  cap=%-9d", cap)
		}
		b.WriteString("\n")
		for _, name := range names {
			fmt.Fprintf(&b, "%-15s", name)
			for _, cap := range caps {
				k := key{size, cap, name}
				if len(retention[k]) == 0 {
					fmt.Fprintf(&b, "  %-13s", "-")
					continue
				}
				fmt.Fprintf(&b, "  %.2f (%.2f)  ", meanOf(retention[k]), meanOf(saving[k]))
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%-15s", "n")
		for _, cap := range caps {
			fmt.Fprintf(&b, "  %-13d", len(retention[key{size, cap, "current"}]))
		}
		b.WriteString("\n")
	}
	t.Log(b.String())

	// The cap as a share of the tasks a pull request has, for the shipped ranking: the
	// default has to work whatever the size of the pull request, so the cap is read
	// against how many tasks there were, pooled over every size.
	b.Reset()
	b.WriteString("\nshipped ranking, pooled over sizes: share of tasks kept -> defects still reviewed, tokens saved\n")
	type bucket struct{ ret, sav []float64 }
	buckets := map[int]*bucket{}
	for _, size := range sizes {
		for _, cap := range caps {
			k := key{size, cap, "current"}
			for i := range retention[k] {
				share := int(keptShare[k][i]*10 + 0.5)
				if buckets[share] == nil {
					buckets[share] = &bucket{}
				}
				buckets[share].ret = append(buckets[share].ret, retention[k][i])
				buckets[share].sav = append(buckets[share].sav, saving[k][i])
			}
		}
	}
	var shares []int
	for share := range buckets {
		shares = append(shares, share)
	}
	sort.Ints(shares)
	for _, share := range shares {
		bk := buckets[share]
		lo, _ := bootstrap(bk.ret, rng)
		fmt.Fprintf(&b, "  keep ~%3d%% of tasks: defects reviewed %.3f (5th pct of mean %.3f), tokens saved %.3f, n=%d\n",
			share*10, meanOf(bk.ret), lo, meanOf(bk.sav), len(bk.ret))
	}
	t.Log(b.String())

	// Paired differences against the shipped ranking, with bootstrap intervals.
	b.Reset()
	b.WriteString("\ncurrent minus other, mean retention difference over all sizes and caps (95% bootstrap interval)\n")
	for _, other := range names {
		if other == "current" {
			continue
		}
		var diffs []float64
		for _, size := range sizes {
			for _, cap := range caps {
				a, c := retention[key{size, cap, "current"}], retention[key{size, cap, other}]
				for i := range min(len(a), len(c)) {
					diffs = append(diffs, a[i]-c[i])
				}
			}
		}
		lo, hi := bootstrap(diffs, rng)
		fmt.Fprintf(&b, "  vs %-15s %+.3f  [%+.3f, %+.3f]  n=%d\n", other, meanOf(diffs), lo, hi, len(diffs))
	}
	t.Log(b.String())
}

func countDefective(pool []Fixture) int {
	n := 0
	for _, f := range pool {
		if len(f.Defects) > 0 {
			n++
		}
	}
	return n
}

func meanOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	total := 0.0
	for _, x := range xs {
		total += x
	}
	return total / float64(len(xs))
}

func bootstrap(xs []float64, rng *rand.Rand) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	means := make([]float64, 1000)
	for i := range means {
		total := 0.0
		for range xs {
			total += xs[rng.IntN(len(xs))]
		}
		means[i] = total / float64(len(xs))
	}
	sort.Float64s(means)
	return means[25], means[974]
}
