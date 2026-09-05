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
	for name, split := range map[string]func(int) bool{
		"even tasks train": func(task int) bool { return task%2 == 0 },
		"first half train": func(task int) bool { return task < len(Tasks)/2 },
	} {
		results := Experiment(samples, split)
		t.Logf("split %s:%s", name, Render(results))
		for _, r := range results {
			t.Logf("%s: margin over majority %.2f (%s)", r.Language, r.Accuracy()-r.Baseline(), Verdict(r))
		}
	}
}
