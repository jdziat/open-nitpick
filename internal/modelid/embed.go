package modelid

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// The corpus travels with the binary so identify-model can train on it at
// run time; it is a few hundred kilobytes of text and it is the only thing
// the classifier knows.
//
//go:embed corpus
var corpus embed.FS

// Corpus is the embedded corpus as samples.
func Corpus() ([]Sample, error) {
	var out []Sample
	err := fs.WalkDir(corpus, "corpus", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		parts := strings.Split(strings.TrimPrefix(p, "corpus/"), "/")
		if len(parts) != 3 || !corpusName.MatchString(parts[2]) {
			return nil
		}
		var task int
		if _, err := fmt.Sscanf(parts[2][:2], "%d", &task); err != nil {
			return nil
		}
		data, err := corpus.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, Sample{Author: parts[0], Language: parts[1], Task: task, Path: p, Features: Extract(string(data), parts[1])})
		return nil
	})
	return out, err
}

// Supported lists the languages the experiment cleared for a product, by
// the rule in docs/findings.md: a margin over the majority baseline on
// both task splits. The others answer "unknown", and say why.
var Supported = map[string]bool{"typescript": true}

// MinConfidence is the classifier confidence below which the answer is
// "unknown" rather than a name.
const MinConfidence = 0.2

// Identify names the corpus author whose style the file is most similar
// to, or "unknown" with a reason.
func Identify(content, lang string) (author string, confidence float64, reason string, err error) {
	if !Supported[lang] {
		return "unknown", 0, fmt.Sprintf("the experiment in docs/findings.md did not clear %s: the classifier could not hold a margin over guessing the commonest author on both task splits", lang), nil
	}
	samples, err := Corpus()
	if err != nil {
		return "", 0, "", err
	}
	var train []Sample
	for _, s := range samples {
		if s.Language == lang {
			train = append(train, s)
		}
	}
	author, confidence = Train(train).Predict(Extract(content, lang))
	if confidence < MinConfidence {
		return "unknown", confidence, fmt.Sprintf("nearest author %s, but the margin over the next is %.2f, below %.2f", author, confidence, MinConfidence), nil
	}
	return author, confidence, "", nil
}
