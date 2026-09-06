package modelid

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// Both corpora travel with the binary so identify-model can train on them
// at run time: a few megabytes of text, and the only thing the classifier
// knows. The command trains on both generations, since the experiment
// measured the signature across them.
//
//go:embed corpus corpus2
var corpus embed.FS

// Corpus is the embedded corpora as samples.
func Corpus() ([]Sample, error) {
	var out []Sample
	for _, root := range []string{"corpus", "corpus2"} {
		err := fs.WalkDir(corpus, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			parts := strings.Split(strings.TrimPrefix(p, root+"/"), "/")
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
			out = append(out, NewSample(parts[0], parts[1], task, p, string(data)))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Supported lists the languages the experiment cleared for a product, by
// the rule in docs/findings.md: a margin over the majority baseline on
// both task splits and across generations, under the fingerprint. All
// three cleared it on 2026-09-05; a language outside the corpus answers
// "unknown", and says why.
var Supported = map[string]bool{"go": true, "python": true, "typescript": true}

// MinConfidence is the margin below which the answer is "unknown" rather
// than a name. Under the fingerprint the margin is the gap between the
// best and second-best cosine relative to the best, and it is small in
// absolute terms: on the second corpus, answering only above 0.05 was
// right 81 times in 89 (0.91) and abstained on 108 of 197; above 0.10 it
// was right 25 in 26 and abstained on 171; at the old floor of 0.20 it
// never answered. 0.05 is the point where an answer is worth giving.
const MinConfidence = 0.05

// Identify names the corpus author whose fingerprint the file is most
// similar to, or "unknown" with a reason. The fingerprint (character
// 3-grams and token bigrams) is the instrument that cleared every language
// in the experiment; the dense features remain in the report as the
// readable half.
func Identify(content, lang string) (author string, confidence float64, reason string, err error) {
	if !Supported[lang] {
		return "unknown", 0, fmt.Sprintf("the corpus in docs/findings.md has no %s: the classifier has nothing to compare the file with", lang), nil
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
	author, confidence = TrainFingerprints(train).Predict(Fingerprint(StripLicenseHeader(content)))
	if confidence < MinConfidence {
		return "unknown", confidence, fmt.Sprintf("nearest author %s, but the margin over the next is %.2f, below %.2f", author, confidence, MinConfidence), nil
	}
	return author, confidence, "", nil
}
