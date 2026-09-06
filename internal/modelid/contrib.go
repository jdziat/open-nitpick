package modelid

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The contributor experiment asks a different question from the corpus
// experiment: not "which of six models wrote this whole file", but "in a
// real repository, were the lines a commit added written by a person or by
// a tool", with the commit's co-author trailer as the label. The corpus is
// built by cmd/contrib-corpus and is not committed, being other people's
// code; its layout is <repo>/<label>/<language>/<NNNNN>.<ext>.txt with the
// number the commit's rank by date.

// contribName is the file shape the generator writes.
var contribName = regexp.MustCompile(`^\d{5}-\d+\.[a-z]+\.txt$`)

// LoadContrib reads a contributor corpus. Author is the label ("human" or
// "model-<tool>"), Task the commit's rank by date, and the first line (the
// generator's provenance comment) is dropped before measuring.
func LoadContrib(dir string) (map[string][]Sample, error) {
	out := map[string][]Sample{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 4 || !contribName.MatchString(parts[3]) {
			return nil
		}
		var seq int
		if _, err := fmt.Sscanf(parts[3][:5], "%d", &seq); err != nil {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		body := string(data)
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			body = body[i+1:]
		}
		out[parts[0]] = append(out[parts[0]], NewSample(parts[1], parts[2], seq, rel, body))
		return nil
	})
	return out, err
}

// ContribSplit chooses which samples train.
type ContribSplit int

const (
	// OlderHalf trains on the older half of the commits sampled and tests
	// on the newer: does a signature hold over time.
	OlderHalf ContribSplit = iota
	// Interleaved trains on every other commit by date: the control, with
	// no time between train and test, so a null here is a null of the
	// instrument and not of drift. It leaks: consecutive commits are often
	// one pull request touching one file, so a test sample can have its
	// near twin in training.
	Interleaved
	// Blocks trains on alternate runs of fifty consecutive commits: spans
	// the era like Interleaved, without a test commit's neighbours in
	// training.
	Blocks
)

// ContribExperiment runs, per repository and language, the split given
// under the method given. Labels are collapsed to "human" and "model" when
// binary is set, which is the question a reader of a pull request has;
// otherwise each tool is its own author.
func ContribExperiment(m Method, corpus map[string][]Sample, binary bool, split ContribSplit) []Result {
	var results []Result
	for repo, samples := range corpus {
		byLang := map[string][]Sample{}
		for _, s := range samples {
			if binary && strings.HasPrefix(s.Author, "model-") {
				s.Author = "model"
			}
			byLang[s.Language] = append(byLang[s.Language], s)
		}
		for lang, all := range byLang {
			seqs := make([]int, 0, len(all))
			for _, s := range all {
				seqs = append(seqs, s.Task)
			}
			sort.Ints(seqs)
			median := seqs[len(seqs)/2]
			var train, test []Sample
			for _, s := range all {
				trains := s.Task < median
				switch split {
				case Interleaved:
					trains = s.Task%2 == 0
				case Blocks:
					trains = (s.Task/50)%2 == 0
				}
				if trains {
					train = append(train, s)
				} else {
					test = append(test, s)
				}
			}
			if len(train) == 0 || len(test) == 0 {
				continue
			}
			r := score(m, lang, train, test)
			r.Language = repo + "/" + lang
			results = append(results, r)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Language < results[j].Language })
	return results
}

// ModelRecall reports, for a binary result, how many of the model-written
// samples were called model, and how many of the calls of "model" were
// right: the two numbers a reader of a pull request would want.
func ModelRecall(r Result) (recall, precision float64) {
	actualModel, calledModel, both := 0, 0, 0
	for actual, row := range r.Confusion {
		for predicted, n := range row {
			if actual == "model" {
				actualModel += n
			}
			if predicted == "model" {
				calledModel += n
			}
			if actual == "model" && predicted == "model" {
				both += n
			}
		}
	}
	return frac(both, actualModel), frac(both, calledModel)
}

// Balanced is the mean of the per-class recalls: the accuracy a reader
// should compare with chance (one over the number of classes) when the
// classes are as unequal as a repository's human and model commits are. A
// centroid classifier does not know the class sizes, so plain accuracy
// against the majority baseline punishes it for splitting its calls.
func Balanced(r Result) float64 {
	var sum float64
	n := 0
	for actual, row := range r.Confusion {
		total := 0
		for _, c := range row {
			total += c
		}
		if total == 0 {
			continue
		}
		sum += frac(row[actual], total)
		n++
	}
	return frac(int(sum*1000), n*1000)
}
