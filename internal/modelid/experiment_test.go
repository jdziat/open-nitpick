package modelid

import (
	"os"
	"strings"
	"testing"
)

func TestFeaturesMeasureShapeNotLength(t *testing.T) {
	short := "package a\n\n// Add adds.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	long := strings.Repeat(short, 20)
	fs, fl := Extract(short, "go"), Extract(long, "go")
	for i := range featureCount {
		if i == 12 || i == 19 { // a maximum, and a ratio over a fixed token window
			continue
		}
		if d := fs[i] - fl[i]; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s: %v for one copy, %v for twenty", FeatureNames[i], fs[i], fl[i])
		}
	}
	slop := "package a\n\n// Sure! Here's the add function.\n// Add the numbers.\nfunc Add(a, b int) int {\n\t// return the sum\n\treturn a + b\n}\n"
	f := Extract(slop, "go")
	if f[4] == 0 || f[3] == 0 {
		t.Errorf("chat phrases %v and restating comments %v should both be nonzero", f[4], f[3])
	}
}

func TestClassifierSeparatesDistinctStyles(t *testing.T) {
	var samples []Sample
	for task := range 12 {
		terse := "package a\n\nfunc F(x int) int {\n\tif x > 0 {\n\t\treturn x\n\t}\n\treturn -x\n}\n"
		chatty := "package a\n\n// Sure! Here's a function that returns the absolute value.\n// Note that this handles negatives.\nfunc absoluteValue(inputValue int) int {\n\t// Check if the input value is positive.\n\tif inputValue > 0 {\n\t\t// Return the input value.\n\t\treturn inputValue\n\t}\n\t// Return the negated value.\n\treturn -inputValue\n}\n"
		samples = append(samples,
			Sample{Author: "terse", Language: "go", Task: task, Features: Extract(strings.Repeat(terse, 1+task%3), "go")},
			Sample{Author: "chatty", Language: "go", Task: task, Features: Extract(strings.Repeat(chatty, 1+task%2), "go")},
		)
	}
	results := Experiment(samples, func(task int) bool { return task%2 == 0 })
	if len(results) != 1 || results[0].Accuracy() != 1 {
		t.Fatalf("results = %s", Render(results))
	}
}

// TestModelIdentificationExperiment runs the experiment on the committed
// corpus and logs the matrices; it fails only when the corpus is malformed.
// The go/no-go reads the two splits' margins over the majority baseline.
func TestModelIdentificationExperiment(t *testing.T) {
	const dir = "corpus"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no corpus")
	}
	samples, err := LoadCorpus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) == 0 {
		t.Skip("empty corpus")
	}
	for _, m := range Methods {
		for name, split := range map[string]func(int) bool{
			"even tasks train": func(task int) bool { return task%2 == 0 },
			"first half train": func(task int) bool { return task < len(Tasks)/2 },
		} {
			results := ExperimentWith(m, samples, split)
			t.Logf("method %s, split %s:%s", m.Name, name, Render(results))
			for _, r := range results {
				t.Logf("%s, %s: margin over majority %.2f (%s)", r.Language, r.Method, r.Accuracy()-r.Baseline(), Verdict(r))
			}
		}
	}
	for _, lang := range Languages {
		t.Logf("idioms, %s:%s", lang.Name, RenderIdioms(Idioms(samples, lang.Name, 4, 8)))
	}
}

func TestFingerprintSeparatesDistinctStyles(t *testing.T) {
	var samples []Sample
	for task := range 12 {
		tabs := "package a\n\nfunc F(x int) int {\n\tif x > 0 {\n\t\treturn x\n\t}\n\treturn -x\n}\n"
		spaces := "package a\n\n// Edge case: negatives.\nfunc absoluteValue(v int) int {\n    if v > 0 {\n        return v\n    }\n    return -v\n}\n"
		for i, body := range []string{tabs, spaces} {
			author := []string{"tabs", "spaces"}[i]
			content := strings.Repeat(body, 1+task%3)
			samples = append(samples, Sample{Author: author, Language: "go", Task: task, Features: Extract(content, "go"), Profile: Fingerprint(content)})
		}
	}
	for _, m := range Methods[1:] {
		results := ExperimentWith(m, samples, func(task int) bool { return task%2 == 0 })
		if len(results) != 1 || results[0].Accuracy() != 1 {
			t.Errorf("%s: %s", m.Name, Render(results))
		}
	}
	found := false
	for _, id := range Idioms(samples, "go", 3, 50)["spaces"] {
		if id.Gram == "Edge case" {
			found = true
		}
	}
	if !found {
		t.Errorf("the spaces author's idiom was not mined: %+v", Idioms(samples, "go", 3, 50)["spaces"])
	}
}

// TestModelIdentificationAcrossGenerations trains on the first corpus and
// tests on a second, independent generation of the same tasks by the same
// models (corpus2, from a later run of cmd/modelid-corpus). Every task is in
// both, so what this measures is whether the signature holds from one
// sampling to the next, which the task split cannot ask.
func TestModelIdentificationAcrossGenerations(t *testing.T) {
	if _, err := os.Stat("corpus2"); err != nil {
		t.Skip("no second corpus")
	}
	first, err := LoadCorpus("corpus")
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadCorpus("corpus2")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) == 0 {
		t.Skip("empty second corpus")
	}
	for _, m := range Methods {
		results := CrossExperimentWith(m, first, second)
		t.Logf("method %s, train on corpus, test on corpus2:%s", m.Name, Render(results))
		for _, r := range results {
			t.Logf("%s, %s: margin over majority %.2f (%s)", r.Language, r.Method, r.Accuracy()-r.Baseline(), Verdict(r))
		}
	}
}

func TestLicenseHeaderIsStrippedBeforeMeasuring(t *testing.T) {
	src := "// Copyright 2014 The Go Authors. All rights reserved.\n// Use of this source code is governed by a BSD-style\n// license that can be found in the LICENSE file.\n\npackage a\n\n// F does a thing.\nfunc F() {}\n"
	if got := StripLicenseHeader(src); !strings.HasPrefix(got, "package a") || !strings.Contains(got, "// F does") {
		t.Errorf("stripped = %q", got)
	}
	plain := "package a\n\n// F does a thing.\nfunc F() {}\n"
	if got := StripLicenseHeader(plain); got != plain {
		t.Errorf("a doc comment was stripped: %q", got)
	}
	if _, ok := NewSample("human", "go", 0, "x", src).Profile.Tok["All rights"]; ok {
		t.Error("the copyright line reached the fingerprint")
	}
}

// TestContributorExperiment reads the corpus cmd/contrib-corpus builds
// (NITPICK_CONTRIB_CORPUS, default ./contrib), which is not committed, and
// logs a time split per repository: were the lines a commit added written
// by a person or by the tool its trailer names.
func TestContributorExperiment(t *testing.T) {
	dir := os.Getenv("NITPICK_CONTRIB_CORPUS")
	if dir == "" {
		dir = "contrib"
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no contributor corpus")
	}
	corpus, err := LoadContrib(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus) == 0 {
		t.Skip("empty contributor corpus")
	}
	for _, split := range []struct {
		name string
		s    ContribSplit
	}{{"older half trains", OlderHalf}, {"blocks of fifty alternate", Blocks}, {"interleaved by date", Interleaved}} {
		for _, m := range Methods {
			results := ContribExperiment(m, corpus, true, split.s)
			t.Logf("method %s, human against model, %s:%s", m.Name, split.name, Render(results))
			for _, r := range results {
				recall, precision := ModelRecall(r)
				t.Logf("%s, %s, %s: balanced accuracy %.2f against chance 0.50; model recall %.2f, precision %.2f", r.Language, r.Method, split.name, Balanced(r), recall, precision)
			}
		}
	}
	results := ContribExperiment(Methods[1], corpus, false, Interleaved)
	t.Logf("fingerprint, by tool, interleaved:%s", Render(results))
}

// A sample with nothing in common with any author is unknown at zero
// confidence, not the alphabetically first author at one.
func TestFingerprintWithNoSharedGramsIsUnknown(t *testing.T) {
	train := []Sample{
		{Author: "a", Language: "go", Profile: Fingerprint("package a\n\nfunc A() {}\n")},
		{Author: "b", Language: "go", Profile: Fingerprint("package b\n\nfunc B() {}\n")},
	}
	f := TrainFingerprints(train)
	if author, conf := f.Predict(Fingerprint("")); author != "unknown" || conf != 0 {
		t.Errorf("empty file = %s at %.2f, want unknown at 0", author, conf)
	}
	if author, conf := f.Predict(Fingerprint("\u4e2d\u6587\u7684\u6587\u672c")); author != "unknown" || conf != 0 {
		t.Errorf("file with no shared grams = %s at %.2f, want unknown at 0", author, conf)
	}
}
