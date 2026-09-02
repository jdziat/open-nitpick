package evals

import (
	"strings"
	"testing"
)

func TestParseContenderReadsCommentsAndKeepsTheWord(t *testing.T) {
	out := []byte("Reviewing 3 files...\n" + `{
  "summary": "Adds a cleanup endpoint.",
  "comments": [
    {"id": "c1", "path": "api/admin.go", "startLine": 37, "endLine": 38, "severity": "high",
     "title": "Empty filter deletes every row", "body": "DeleteWhere with a zero Filter matches everything."},
    {"id": "c2", "path": "api/admin.go", "line": 12, "severity": "unheard-of", "body": "Consider a doc comment. It helps."}
  ]
}`)
	findings, err := parseContender(out)
	if err != nil {
		t.Fatalf("parseContender: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(findings))
	}
	f := findings[0]
	if f.Path != "api/admin.go" || f.Line != 37 || f.EndLine != 38 || f.Severity != "error" || f.RawSeverity != "high" || !f.SeverityTranslated {
		t.Errorf("first finding = %+v", f)
	}
	if findings[1].Title != "Consider a doc comment." || findings[1].Severity != "warning" || findings[1].Line != 12 {
		t.Errorf("second finding = %+v; a missing title takes the first sentence, an unknown word lands on warning", findings[1])
	}
}

func TestParseContenderRefusesADocumentWithoutComments(t *testing.T) {
	for _, in := range []string{"", "no json here", `{"summary": "ok"}`, `{"comments": "nope"}`} {
		if _, err := parseContender([]byte(in)); err == nil {
			t.Errorf("parseContender(%q) = nil error; a document this parser cannot read must not score as a clean review", in)
		}
	}
	if _, err := parseContender([]byte(`{"comments": []}`)); err != nil {
		t.Errorf("an empty comments array is a clean review, got %v", err)
	}
}

func TestContenderSeverityIsAlwaysTranslated(t *testing.T) {
	for _, w := range []string{"critical", "HIGH", "medium", "low", "nit", "", "whatever"} {
		if _, translated := contenderSeverity(w); !translated {
			t.Errorf("%q: Contender's vocabulary is not ours, so every word is a translation", w)
		}
	}
	if s, _ := contenderSeverity("critical"); s != "critical" {
		t.Errorf("critical mapped to %q", s)
	}
	if !strings.Contains(strings.ToLower(ContenderModel), "contender") {
		t.Error("model id should name the reviewer")
	}
}
