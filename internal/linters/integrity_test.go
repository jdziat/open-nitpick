package linters

import (
	"testing"
)

func TestPylintCrashMustNotBeClean(t *testing.T) {
	f, e := pylintSpec().parse(invocation{}, nil, 32)
	if e == nil {
		t.Fatalf("pylint usage/crash exit 32 accepted as clean: findings=%v", f)
	}
}
func TestSQLParseFailureMustNotBeClean(t *testing.T) {
	f, e := sqlfluffSpec().parse(invocation{}, []byte(`[{"filepath":"migration.sql","violations":[{"start_line_no":1,"code":"PRS","description":"Unable to parse"}]}]`), 1)
	if e == nil && len(f) == 0 {
		t.Fatal("unparsed SQL accepted as zero findings and successful analysis")
	}
}

func TestCleanAnalyzerReportsRemainSuccessful(t *testing.T) {
	for _, spec := range []toolSpec{pylintSpec(), sqlfluffSpec()} {
		findings, err := spec.parse(invocation{}, []byte(`[]`), 0)
		if err != nil || len(findings) != 0 {
			t.Fatalf("%s clean report: %v %v", spec.name, findings, err)
		}
	}
}
