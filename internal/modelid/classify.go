package modelid

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Sample is one file of the corpus: who wrote it, in what language, for
// which task, and its features.
type Sample struct {
	Author   string // a model id slug, or "human"
	Language string
	Task     int
	Path     string
	Features Features
}

// LoadCorpus reads <dir>/<author>/<language>/<NN>.<ext>.txt; the .txt keeps
// the corpus from being read as source by anything but this package.
func LoadCorpus(dir string) ([]Sample, error) {
	var out []Sample
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 3 || !corpusName.MatchString(parts[2]) {
			return nil
		}
		var task int
		if _, err := fmt.Sscanf(parts[2][:2], "%d", &task); err != nil {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, Sample{Author: parts[0], Language: parts[1], Task: task, Path: rel, Features: Extract(string(data), parts[1])})
		return nil
	})
	return out, err
}

// corpusName is the file shape the generator writes: NN.<ext>.txt.
var corpusName = regexp.MustCompile(`^\d{2}\.[a-z]+\.txt$`)

// Classifier is a nearest-centroid classifier over standardised features,
// one per language. It is the simplest thing that can find a signature if
// there is one, and it cannot memorise a task, which is the failure the
// experiment splits train and test to rule out.
type Classifier struct {
	mean, std Features
	centroids map[string]Features
}

// Train fits on the samples given.
func Train(samples []Sample) *Classifier {
	c := &Classifier{centroids: map[string]Features{}}
	if len(samples) == 0 {
		return c
	}
	for i := range featureCount {
		var xs []float64
		for _, s := range samples {
			xs = append(xs, s.Features[i])
		}
		c.mean[i], c.std[i] = meanStd(xs)
		if c.std[i] == 0 {
			c.std[i] = 1
		}
	}
	sums := map[string]*Features{}
	counts := map[string]int{}
	for _, s := range samples {
		z := c.standardise(s.Features)
		if sums[s.Author] == nil {
			sums[s.Author] = &Features{}
		}
		for i := range featureCount {
			sums[s.Author][i] += z[i]
		}
		counts[s.Author]++
	}
	for author, sum := range sums {
		var centroid Features
		for i := range featureCount {
			centroid[i] = sum[i] / float64(counts[author])
		}
		c.centroids[author] = centroid
	}
	return c
}

func (c *Classifier) standardise(f Features) Features {
	var z Features
	for i := range featureCount {
		z[i] = (f[i] - c.mean[i]) / c.std[i]
	}
	return z
}

// Predict returns the nearest author and a confidence in (0, 1]: the margin
// between the nearest and second-nearest centroid, relative to the nearest
// distance, so a sample equidistant from two authors reports near zero.
func (c *Classifier) Predict(f Features) (author string, confidence float64) {
	z := c.standardise(f)
	type cand struct {
		author string
		dist   float64
	}
	var cands []cand
	for a, centroid := range c.centroids {
		d := 0.0
		for i := range featureCount {
			d += (z[i] - centroid[i]) * (z[i] - centroid[i])
		}
		cands = append(cands, cand{a, math.Sqrt(d)})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].author < cands[j].author
	})
	if len(cands) == 0 {
		return "unknown", 0
	}
	if len(cands) == 1 {
		return cands[0].author, 1
	}
	if cands[0].dist == 0 {
		return cands[0].author, 1
	}
	return cands[0].author, math.Min(1, (cands[1].dist-cands[0].dist)/cands[0].dist)
}

// Result is one language's held-out outcome.
type Result struct {
	Language  string
	Authors   []string
	Train     int
	Test      int
	Correct   int
	Majority  int // what always guessing the commonest author gets right
	Confusion map[string]map[string]int
}

// Accuracy and Baseline are fractions of the test set.
func (r Result) Accuracy() float64 { return frac(r.Correct, r.Test) }

// Baseline is the majority-class accuracy.
func (r Result) Baseline() float64 { return frac(r.Majority, r.Test) }

func frac(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// Experiment trains on the tasks in train and tests on the rest, per
// language. Splitting by task, not by file, is what stops the classifier
// scoring by recognising the program rather than the author.
func Experiment(samples []Sample, trainTask func(task int) bool) []Result {
	byLang := map[string][]Sample{}
	for _, s := range samples {
		byLang[s.Language] = append(byLang[s.Language], s)
	}
	var results []Result
	for lang, all := range byLang {
		var train, test []Sample
		for _, s := range all {
			if trainTask(s.Task) {
				train = append(train, s)
			} else {
				test = append(test, s)
			}
		}
		results = append(results, score(lang, train, test))
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Language < results[j].Language })
	return results
}

// score trains on train and tests on test for one language.
func score(lang string, train, test []Sample) Result {
	c := Train(train)
	r := Result{Language: lang, Train: len(train), Test: len(test), Confusion: map[string]map[string]int{}}
	counts := map[string]int{}
	authors := map[string]bool{}
	for _, s := range train {
		authors[s.Author] = true
	}
	for _, s := range test {
		got, _ := c.Predict(s.Features)
		if r.Confusion[s.Author] == nil {
			r.Confusion[s.Author] = map[string]int{}
		}
		r.Confusion[s.Author][got]++
		if got == s.Author {
			r.Correct++
		}
		counts[s.Author]++
		authors[s.Author] = true
		authors[got] = true
	}
	// Every author that appears as an actual or a prediction gets a
	// column, so a prediction of an author absent from the test set is
	// shown rather than dropped.
	for a := range authors {
		r.Authors = append(r.Authors, a)
	}
	for _, n := range counts {
		if n > r.Majority {
			r.Majority = n
		}
	}
	sort.Strings(r.Authors)
	return r
}

// Render prints the results as the findings document will carry them.
func Render(results []Result) string {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "\n%s: %d train, %d test, %d authors; accuracy %.2f against a majority baseline of %.2f (chance %.2f)\n",
			r.Language, r.Train, r.Test, len(r.Authors), r.Accuracy(), r.Baseline(), frac(1, len(r.Authors)))
		fmt.Fprintf(&b, "  %-28s", "actual \\ predicted")
		for _, a := range r.Authors {
			fmt.Fprintf(&b, " %8s", short(a))
		}
		b.WriteString("\n")
		for _, actual := range r.Authors {
			fmt.Fprintf(&b, "  %-28s", short(actual))
			for _, predicted := range r.Authors {
				fmt.Fprintf(&b, " %8d", r.Confusion[actual][predicted])
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func short(author string) string {
	if i := strings.LastIndex(author, "_"); i >= 0 && i < len(author)-1 {
		author = author[i+1:]
	}
	if len(author) > 8 {
		return author[:8]
	}
	return author
}

// GoMargin is the accuracy over the majority baseline a language must clear
// before "which model wrote this" is offered as a product at all.
const GoMargin = 0.15

// Verdict is the go/no-go for one result.
func Verdict(r Result) string {
	if r.Accuracy()-r.Baseline() >= GoMargin {
		return "go"
	}
	return "no-go"
}

// CrossExperiment trains on one corpus and tests on another, per language:
// the second corpus is the same tasks generated again, so a signature that
// holds here holds across samplings.
func CrossExperiment(train, test []Sample) []Result {
	byLang := map[string][2][]Sample{}
	for _, s := range train {
		e := byLang[s.Language]
		e[0] = append(e[0], s)
		byLang[s.Language] = e
	}
	for _, s := range test {
		e := byLang[s.Language]
		e[1] = append(e[1], s)
		byLang[s.Language] = e
	}
	var results []Result
	for lang, e := range byLang {
		if len(e[0]) == 0 || len(e[1]) == 0 {
			continue
		}
		results = append(results, score(lang, e[0], e[1]))
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Language < results[j].Language })
	return results
}
