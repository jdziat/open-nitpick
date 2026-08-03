package evals

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/jdziat/open-nitpick/internal/review"
)

// EnvDump names a file to write per-finding diagnostics into, as JSON Lines.
//
// The reports say how many findings were inflated or misclassified. They cannot
// say WHICH, and closing those gaps means reading the offending finding beside
// the code it was about and the judge's reasoning for the call. Unset, this
// costs nothing: OpenDump returns a nil *Dump and every method on a nil *Dump is
// a no-op, so no call site carries a conditional.
const EnvDump = "NITPICK_EVAL_DUMP"

// DumpRecord is one judged finding with everything needed to re-derive any
// number in the reports.
//
// One line per finding rather than per sample, because this file is read by a
// program that groups and filters rather than by a human who scrolls. The
// consequence is that a sample whose reviewer said nothing contributes no
// lines: misses live in the judge's `missed` list and in Score.Detected, not
// here. A finding credited with reporting two planted defects gets one line per
// defect, so grouping by defect_why counts each located defect once.
type DumpRecord struct {
	Model   string `json:"model"`
	Fixture string `json:"fixture"`
	Run     int    `json:"run"`

	// HeldOut marks a record produced against the held-out corpus.
	//
	// The dump is written to a path the operator chooses and every experiment
	// reuses those paths, so a held-out run can land in the file the tuner has
	// been iterating against — carrying defect_why, which is the planted
	// defect's own prose. Nothing else in the record distinguishes the two, and
	// "read the dump, edit the prompt" is the documented workflow, so the
	// distinction has to be in the data rather than in the filename.
	HeldOut bool `json:"held_out,omitempty"`

	// Variant names the persona configuration under test, empty for the model
	// benchmark where the persona is held constant.
	//
	// The nitpick axis derives every level from ONE review, so a finding that
	// survives several levels' filters is written once per level. Group by
	// variant before counting anything, or the same finding is counted four
	// times.
	Variant string `json:"variant,omitempty"`

	// Index is the finding's position in the list submitted to the judge, which
	// is what Verdict.Index refers to.
	Index int `json:"index"`

	Path      string `json:"path"`
	Line      int    `json:"line"`
	EndLine   int    `json:"end_line,omitempty"`
	Severity  string `json:"severity"`
	Class     string `json:"class"`
	Title     string `json:"title"`
	Rationale string `json:"rationale,omitempty"`

	// Verdict is the judge's assessment, absent when the judge returned none
	// for this index. Absent is not "the judge approved it" — a judge that
	// returns the wrong number of verdicts is already reported as suspect, and
	// a reader of this file must be able to tell the two apart.
	Verdict *Verdict `json:"verdict,omitempty"`

	// Matched reports whether this finding is the one CREDITED with reporting a
	// planted defect — the same finding recall counted and the same one the
	// severity columns graded. The fields below are populated only when it did:
	// nothing planted says what an unmatched finding's severity should have
	// been.
	//
	// It is deliberately the scorer's own association rather than a second
	// answer to the same question. Re-deriving it here let the dump disagree
	// with the table it exists to explain, which is worse than not having it.
	// A finding that also mentions a defect but was not the one credited
	// carries no ground truth, so grouping this file by defect_why counts each
	// located defect once.
	Matched      bool   `json:"matched"`
	WantSeverity string `json:"want_severity,omitempty"`

	// SeverityDelta is the objective verdict — accurate, inflated or
	// understated — against WantSeverity. Precomputed so a reader does not
	// reimplement the severity ordering to recover it.
	SeverityDelta string `json:"severity_delta,omitempty"`

	// DefectWhy is the planted defect's own description, so a line of this file
	// is legible without the fixture source beside it.
	DefectWhy string `json:"defect_why,omitempty"`
}

// DumpSample is one judged review: what was reviewed, by whom, and what the
// judge said about it.
type DumpSample struct {
	Model   string
	Variant string
	Run     int

	Fixture  Fixture
	Findings []review.Finding

	// Judged assesses exactly these findings, so Verdict.Index is a position in
	// Findings. The persona path filters a shared corpus per level and must
	// re-index its verdicts before handing them over.
	Judged *JudgeResult
}

// Dump writes DumpRecords as JSON Lines. A nil *Dump is a disabled dump.
type Dump struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// OpenDump opens the dump named by EnvDump, returning nil when it is unset.
func OpenDump() (*Dump, error) {
	path := strings.TrimSpace(os.Getenv(EnvDump))
	if path == "" {
		return nil, nil
	}
	return NewDump(path)
}

// NewDump writes records to path.
//
// It truncates rather than appends: the records are keyed by (model, fixture,
// run) and every experiment reuses those keys, so an accumulating file cannot
// be told apart from one run that produced twice the findings.
func NewDump(path string) (*Dump, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Dump{file: f, enc: json.NewEncoder(f)}, nil
}

// Record writes one line per finding in the sample, and one line per credited
// defect for a finding that reported more than one.
//
// The duplicate line is what makes the file groupable both ways: by
// (model, fixture, run, index) it is still one comment, and by defect_why every
// located defect is counted exactly once even when a reviewer folded two of
// them into a single comment. Emitting one line and dropping the second defect
// would make a merged comment look like a miss.
//
// It takes its own lock rather than relying on the caller's: every experiment
// here fans reviews out across goroutines into one shared sink, and a sink that
// serializes itself is the only version that stays correct when a call site
// later moves out from under the mutex it happened to be written beneath.
func (d *Dump) Record(s DumpSample) error {
	if d == nil {
		return nil
	}

	byIndex := map[int]Verdict{}
	if s.Judged != nil {
		for _, v := range s.Judged.Verdicts {
			byIndex[v.Index] = v
		}
	}

	// The scorer's own calls, keyed by the finding it credited, so this file
	// reports exactly what the tables counted rather than a second answer to
	// the same question that is free to disagree with them.
	calls := map[int][]SeverityCall{}
	for _, c := range ScoreSeverity(s.Fixture, s.Findings).Calls {
		calls[c.FindingIndex] = append(calls[c.FindingIndex], c)
	}

	heldOut := HeldOut(s.Fixture.Name)

	d.mu.Lock()
	defer d.mu.Unlock()

	for i, f := range s.Findings {
		rec := DumpRecord{
			Model:     s.Model,
			Fixture:   s.Fixture.Name,
			HeldOut:   heldOut,
			Run:       s.Run,
			Variant:   s.Variant,
			Index:     i,
			Path:      f.Path,
			Line:      f.Line,
			EndLine:   f.EndLine,
			Severity:  f.Severity,
			Class:     f.Class,
			Title:     f.Title,
			Rationale: f.Rationale,
		}

		if v, ok := byIndex[i]; ok {
			rec.Verdict = &v
		}

		credited := calls[i]
		if len(credited) == 0 {
			if err := d.enc.Encode(rec); err != nil {
				return err
			}
			continue
		}

		for _, c := range credited {
			rec.Matched = true
			rec.WantSeverity = c.Defect.WantSeverity.String()
			rec.SeverityDelta = c.Verdict
			rec.DefectWhy = c.Defect.Why

			if err := d.enc.Encode(rec); err != nil {
				return err
			}
		}
	}

	return nil
}

// Close releases the file.
func (d *Dump) Close() error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	return d.file.Close()
}
