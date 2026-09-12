package review

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestTriageAcknowledgementRetainsOriginalEvidenceInDecisions(t *testing.T) {
	original := Finding{Path: "service.go", Line: 7, Severity: "nit", Class: "correctness", Title: "No defect found in records package", Rationale: "Register returns the encoder error from save, so the documented persistence contract is met.", Source: "review-model", Evidence: []string{"service.go:7"}}
	reply := TriageResult{Acknowledgements: []Acknowledgement{{Number: 1, Quote: original.Rationale, Reason: "The title and rationale report success without alleging a problem."}}}
	model := &scriptedLLM{byPrompt: map[string]string{"triaging findings": mustJSON(t, reply)}}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)
	_, kept, decisions, err := engine.triage(context.Background(), &vcs.PullRequest{}, []Finding{original})
	if err != nil {
		t.Fatal(err)
	}
	if model.callCount() != 1 {
		t.Fatalf("triage calls = %d, want 1", model.callCount())
	}
	if len(kept) != 0 || len(decisions) != 1 {
		t.Fatalf("kept=%+v decisions=%+v", kept, decisions)
	}
	if !reflect.DeepEqual(decisions[0].Finding, original) {
		t.Fatalf("original evidence changed: %+v", decisions[0])
	}
	rendered := overruledNotes(&Report{Overruled: decisions})
	for _, want := range []string{original.Title, original.Rationale, reply.Acknowledgements[0].Reason, "triage ("} {
		if !strings.Contains(rendered, want) {
			t.Errorf("decision missing %q: %s", want, rendered)
		}
	}
}

func TestTriageAcknowledgementRejectsUnsafeOrAmbiguousDecisions(t *testing.T) {
	for _, name := range []string{"analyzer", "warning", "patch", "blank reason", "blank quote", "partial quote", "invented quote", "out of range", "zero number", "duplicate decision", "verdict conflict", "merge source", "merge survivor", "invalid merge", "silent omission"} {
		t.Run(name, func(t *testing.T) {
			original := Finding{Path: "service.go", Line: 7, Severity: "nit", Class: "correctness", Title: "No defect found", Rationale: "No defect found. The function preserves its documented contract."}
			a := Acknowledgement{Number: 1, Quote: original.Rationale, Reason: "Reports successful assessment."}
			reply := TriageResult{}
			switch name {
			case "analyzer":
				original.FromAnalyzer = true
			case "warning":
				original.Severity = "warning"
			case "patch":
				original.Suggestion = "return err"
			case "blank reason":
				a.Reason = " "
			case "blank quote":
				a.Quote = " "
			case "partial quote":
				a.Quote = "No defect found."
			case "invented quote":
				a.Quote = "This was never said."
			case "out of range":
				a.Number = 3
			case "zero number":
				a.Number = 0
			case "verdict conflict":
				reply.Verdicts = []Verdict{{Number: 1, Severity: "nit", Class: original.Class}}
			case "merge source":
				reply.Dropped = []Drop{{Number: 1, DuplicateOf: 2, Reason: "same claim"}}
			case "merge survivor":
				reply.Dropped = []Drop{{Number: 2, DuplicateOf: 1, Reason: "same claim"}}
			case "invalid merge":
				reply.Dropped = []Drop{{Number: 1, DuplicateOf: 99, Reason: "invalid"}}
			case "silent omission":
				original.Title = "A caller might pass nil"
				original.Rationale = "A caller outside this excerpt might pass nil and panic."
			}
			reply.Acknowledgements = []Acknowledgement{a}
			if name == "duplicate decision" {
				reply.Acknowledgements = append(reply.Acknowledgements, a)
			}
			if name == "silent omission" {
				reply.Acknowledgements = nil
			}
			before := []Finding{original}
			merging := name == "merge source" || name == "merge survivor"
			if merging {
				before = append(before, gosecFinding(9, "Analyzer defect"))
			}
			kept, decisions := triageWith(t, before, reply)
			if len(kept) != 1 {
				t.Fatalf("kept=%+v decisions=%+v", kept, decisions)
			}
			if merging {
				if !kept[0].FromAnalyzer || len(decisions) != 1 {
					t.Fatalf("merge lost analyzer evidence: %+v, %+v", kept, decisions)
				}
			} else if kept[0].Title != original.Title || kept[0].Rationale != original.Rationale || len(decisions) != 0 {
				t.Fatalf("original was not preserved: %+v, %+v", kept, decisions)
			}
			for _, d := range decisions {
				if strings.Contains(d.Reason, "assessment acknowledgement:") {
					t.Fatalf("unsafe acknowledgement accepted: %+v", d)
				}
			}
		})
	}
}

func TestAcknowledgementWireContractAllowsAuditableDecisions(t *testing.T) {
	raw, err := triageSchema(offeredClasses(false))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string
		Properties map[string]struct {
			Items struct {
				Properties map[string]any
				Required   []string
			}
		}
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(schema.Required, "acknowledgements") {
		t.Fatal("optional acknowledgements can be omitted despite excluding those entries from findings")
	}
	item, ok := schema.Properties["acknowledgements"]
	if !ok {
		t.Fatal("strict provider cannot return acknowledgement decisions")
	}
	for _, field := range []string{"number", "quote", "reason"} {
		if _, ok := item.Items.Properties[field]; !ok {
			t.Errorf("schema excludes %s", field)
		}
	}
	var reply TriageResult
	if err := json.Unmarshal([]byte(`{"findings":[],"summary":"","dropped":[],"acknowledgements":[{"number":1,"quote":"All paths return errors.","reason":"Reports success."}]}`), &reply); err != nil {
		t.Fatal(err)
	}
	original := Finding{Path: "service.go", Line: 7, Severity: "nit", Title: "No issue found", Rationale: "All paths return errors."}
	kept, decisions := triageWith(t, []Finding{original}, reply)
	if len(kept) != 0 || len(decisions) != 1 {
		t.Fatalf("wire decision did not run: %+v %+v", kept, decisions)
	}
}
