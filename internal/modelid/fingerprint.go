package modelid

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Profile is a file's fingerprint: sparse, length-normalised frequencies of
// character 3-grams and of token bigrams. Character n-grams are the standard
// instrument of authorship attribution and read spacing, punctuation and
// casing habits without being told what to look for; token bigrams read the
// idioms, the two-word phrases an author reaches for. Both are sublinear
// (1 + log count) and unit-length, so a long file is not a louder one.
type Profile struct {
	Char map[string]float64
	Tok  map[string]float64
}

// Fingerprint measures one file.
func Fingerprint(content string) Profile {
	return Profile{Char: charGrams(content, 3), Tok: tokenGrams(content, 2)}
}

// charGrams counts n-grams over the text with runs of spaces collapsed, so
// alignment padding does not become the signature, and tabs kept, since
// tab versus spaces is one.
func charGrams(content string, n int) map[string]float64 {
	var b strings.Builder
	space := false
	for _, r := range strings.ReplaceAll(content, "\r\n", "\n") {
		if r == ' ' {
			if !space {
				b.WriteRune(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	rs := []rune(b.String())
	counts := map[string]float64{}
	for i := 0; i+n <= len(rs); i++ {
		counts[string(rs[i:i+n])]++
	}
	return normalise(counts)
}

// tokenGrams counts n-grams over the token stream. Identifiers keep their
// case, since casing is a habit; numbers collapse to one token, since a
// literal is the task's, not the author's.
func tokenGrams(content string, n int) map[string]float64 {
	toks := tokenRe.FindAllString(content, -1)
	for i, t := range toks {
		if t[0] >= '0' && t[0] <= '9' {
			toks[i] = "0"
		}
	}
	counts := map[string]float64{}
	for i := 0; i+n <= len(toks); i++ {
		counts[strings.Join(toks[i:i+n], " ")]++
	}
	return normalise(counts)
}

// normalise applies sublinear scaling and unit length.
func normalise(counts map[string]float64) map[string]float64 {
	var norm float64
	for k, c := range counts {
		v := 1 + math.Log(c)
		counts[k] = v
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return counts
	}
	for k := range counts {
		counts[k] /= norm
	}
	return counts
}

func cosine(a, b map[string]float64) float64 {
	if len(a) > len(b) {
		a, b = b, a
	}
	var dot float64
	for k, v := range a {
		dot += v * b[k]
	}
	return dot
}

// Fingerprinter is a centroid classifier over profiles: each author's
// centroid is the mean of their unit vectors, restricted to the grams seen
// in at least minDF training files, and a sample is scored by cosine
// similarity to each.
type Fingerprinter struct {
	char, tok map[string]map[string]float64
}

const minDF = 2

// TrainFingerprints fits on the samples given.
func TrainFingerprints(samples []Sample) *Fingerprinter {
	f := &Fingerprinter{char: map[string]map[string]float64{}, tok: map[string]map[string]float64{}}
	f.char = centroids(samples, func(s Sample) map[string]float64 { return s.Profile.Char })
	f.tok = centroids(samples, func(s Sample) map[string]float64 { return s.Profile.Tok })
	return f
}

func centroids(samples []Sample, of func(Sample) map[string]float64) map[string]map[string]float64 {
	df := map[string]int{}
	for _, s := range samples {
		for k := range of(s) {
			df[k]++
		}
	}
	sums := map[string]map[string]float64{}
	counts := map[string]int{}
	for _, s := range samples {
		if sums[s.Author] == nil {
			sums[s.Author] = map[string]float64{}
		}
		for k, v := range of(s) {
			if df[k] >= minDF {
				sums[s.Author][k] += v
			}
		}
		counts[s.Author]++
	}
	for author, sum := range sums {
		for k := range sum {
			sum[k] /= float64(counts[author])
		}
		var norm float64
		for _, v := range sum {
			norm += v * v
		}
		norm = math.Sqrt(norm)
		for k := range sum {
			sum[k] /= norm
		}
	}
	return sums
}

// Similarities scores a profile against every author: the mean of the
// character and token cosines, in [0, 1].
func (f *Fingerprinter) Similarities(p Profile) map[string]float64 {
	out := map[string]float64{}
	for author := range f.char {
		out[author] = (cosine(p.Char, f.char[author]) + cosine(p.Tok, f.tok[author])) / 2
	}
	return out
}

// Predict returns the most similar author and a confidence: the margin
// between the best and second-best similarity relative to the best, the
// same convention Classifier.Predict uses.
func (f *Fingerprinter) Predict(p Profile) (string, float64) {
	return best(f.Similarities(p))
}

func best(sims map[string]float64) (string, float64) {
	type cand struct {
		author string
		sim    float64
	}
	var cands []cand
	for a, s := range sims {
		cands = append(cands, cand{a, s})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].sim != cands[j].sim {
			return cands[i].sim > cands[j].sim
		}
		return cands[i].author < cands[j].author
	})
	switch {
	case len(cands) == 0:
		return "unknown", 0
	case len(cands) == 1 || cands[0].sim == 0:
		return cands[0].author, 1
	}
	return cands[0].author, math.Min(1, (cands[0].sim-cands[1].sim)/cands[0].sim)
}

// Combined scores a sample by fingerprint similarity and by the dense
// features together: the fingerprint's cosine, plus the feature
// classifier's distance turned into a similarity in (0, 1], averaged.
type Combined struct {
	features *Classifier
	prints   *Fingerprinter
}

// TrainCombined fits both instruments.
func TrainCombined(samples []Sample) *Combined {
	return &Combined{features: Train(samples), prints: TrainFingerprints(samples)}
}

// Predict returns the best author under the combined score.
func (c *Combined) Predict(s Sample) (string, float64) {
	sims := c.prints.Similarities(s.Profile)
	z := c.features.standardise(s.Features)
	for author, centroid := range c.features.centroids {
		d := 0.0
		for i := range featureCount {
			d += (z[i] - centroid[i]) * (z[i] - centroid[i])
		}
		// Distances in standardised units run from about 2 to 10 here;
		// 1/(1+d/5) puts them on the cosine's scale without a tuned weight.
		sims[author] = (sims[author] + 1/(1+math.Sqrt(d)/5)) / 2
	}
	return best(sims)
}

// Method is one way of deciding who wrote a sample, so the experiment can
// run each over the same splits.
type Method struct {
	Name  string
	Train func([]Sample) func(Sample) (string, float64)
}

// Methods are the instruments the experiment compares: the dense features
// alone (the original instrument), the fingerprint alone, and both.
var Methods = []Method{
	{"features", func(train []Sample) func(Sample) (string, float64) {
		c := Train(train)
		return func(s Sample) (string, float64) { return c.Predict(s.Features) }
	}},
	{"fingerprint", func(train []Sample) func(Sample) (string, float64) {
		f := TrainFingerprints(train)
		return func(s Sample) (string, float64) { return f.Predict(s.Profile) }
	}},
	{"combined", func(train []Sample) func(Sample) (string, float64) {
		c := TrainCombined(train)
		return c.Predict
	}},
	{"bayes", func(train []Sample) func(Sample) (string, float64) {
		b := TrainBayes(train)
		return func(s Sample) (string, float64) { return b.Predict(s.Profile) }
	}},
}

// Idiom is a token bigram one author uses far more than the others.
type Idiom struct {
	Gram  string
	Files int     // of the author's files it appears in
	Ratio float64 // the author's document frequency over everyone else's, smoothed
}

// Idioms mines, per author, the token bigrams most characteristic of them
// in one language: present in at least minFiles of the author's files and
// rare in everyone else's. It is the readable half of the fingerprint, so a
// reader can check a claimed signature against words on a page.
func Idioms(samples []Sample, lang string, minFiles, top int) map[string][]Idiom {
	byAuthor := map[string][]Sample{}
	total := 0
	for _, s := range samples {
		if s.Language == lang {
			byAuthor[s.Author] = append(byAuthor[s.Author], s)
			total++
		}
	}
	df := map[string]map[string]int{} // author -> gram -> files
	all := map[string]int{}
	for author, ss := range byAuthor {
		df[author] = map[string]int{}
		for _, s := range ss {
			for g := range s.Profile.Tok {
				df[author][g]++
				all[g]++
			}
		}
	}
	out := map[string][]Idiom{}
	for author, grams := range df {
		n := float64(len(byAuthor[author]))
		rest := float64(total) - n
		var idioms []Idiom
		for g, files := range grams {
			if files < minFiles {
				continue
			}
			others := float64(all[g] - files)
			ratio := (float64(files)/n + 0.01) / (others/max(rest, 1) + 0.01)
			if ratio < 3 {
				continue
			}
			idioms = append(idioms, Idiom{Gram: g, Files: files, Ratio: ratio})
		}
		sort.Slice(idioms, func(i, j int) bool {
			if idioms[i].Ratio != idioms[j].Ratio {
				return idioms[i].Ratio > idioms[j].Ratio
			}
			if idioms[i].Files != idioms[j].Files {
				return idioms[i].Files > idioms[j].Files
			}
			return idioms[i].Gram < idioms[j].Gram
		})
		if len(idioms) > top {
			idioms = idioms[:top]
		}
		out[author] = idioms
	}
	return out
}

// RenderIdioms prints mined idioms, one author per line.
func RenderIdioms(idioms map[string][]Idiom) string {
	var authors []string
	for a := range idioms {
		authors = append(authors, a)
	}
	sort.Strings(authors)
	var b strings.Builder
	for _, a := range authors {
		fmt.Fprintf(&b, "\n  %-28s", short(a))
		for _, id := range idioms[a] {
			fmt.Fprintf(&b, " %q(%d)", id.Gram, id.Files)
		}
	}
	b.WriteString("\n")
	return b.String()
}

// Bayes is a Bernoulli naive Bayes classifier over the presence of grams,
// with equal class priors and Laplace smoothing: the stronger of the two
// simple instruments for a two-class question with unequal classes, where
// a centroid splits its calls down the middle whatever the classes' sizes.
// It scores the log-odds of each author over the grams a sample has, so an
// author with many samples does not win by having a fuller centroid.
type Bayes struct {
	authors []string
	docs    map[string]float64            // per author, files
	present map[string]map[string]float64 // per author, per gram, files with it
	vocab   map[string]bool
}

// TrainBayes fits on the samples given, over both gram channels.
func TrainBayes(samples []Sample) *Bayes {
	b := &Bayes{docs: map[string]float64{}, present: map[string]map[string]float64{}, vocab: map[string]bool{}}
	df := map[string]int{}
	for _, s := range samples {
		for g := range grams(s.Profile) {
			df[g]++
		}
	}
	for _, s := range samples {
		if b.present[s.Author] == nil {
			b.present[s.Author] = map[string]float64{}
			b.authors = append(b.authors, s.Author)
		}
		b.docs[s.Author]++
		for g := range grams(s.Profile) {
			if df[g] >= minDF {
				b.present[s.Author][g]++
				b.vocab[g] = true
			}
		}
	}
	sort.Strings(b.authors)
	return b
}

// grams merges the two channels, prefixed so they cannot collide.
func grams(p Profile) map[string]bool {
	out := make(map[string]bool, len(p.Char)+len(p.Tok))
	for g := range p.Char {
		out["c:"+g] = true
	}
	for g := range p.Tok {
		out["t:"+g] = true
	}
	return out
}

// Predict returns the author with the highest posterior and the margin of
// that posterior over the runner-up, in probability, as the confidence.
func (b *Bayes) Predict(p Profile) (string, float64) {
	has := grams(p)
	scores := map[string]float64{}
	for _, a := range b.authors {
		n := b.docs[a]
		var ll float64
		for g := range b.vocab {
			pr := (b.present[a][g] + 1) / (n + 2)
			if has[g] {
				ll += math.Log(pr)
			} else {
				ll += math.Log(1 - pr)
			}
		}
		scores[a] = ll
	}
	// Softmax to probabilities, for a margin a reader can read.
	best, second := math.Inf(-1), math.Inf(-1)
	for _, v := range scores {
		if v > best {
			second, best = best, v
		} else if v > second {
			second = v
		}
	}
	var z float64
	for a, v := range scores {
		scores[a] = math.Exp(v - best)
		z += scores[a]
	}
	for a := range scores {
		scores[a] /= z
	}
	author, _ := best2(scores)
	if len(scores) < 2 {
		return author, 1
	}
	return author, scores[author] - math.Exp(second-best)/z
}

func best2(sims map[string]float64) (string, float64) { return best(sims) }
