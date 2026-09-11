package vcs

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSnapshotDiffAndSourceContextIgnoreLaterCheckoutChanges(t *testing.T) {
	root := newRepo(t)
	content := map[string][]byte{"a.go": []byte("package original\n"), "sub/b.go": []byte("package b\n")}
	snapshot := NewSnapshot(NewLocal(root, nil), content)
	content["a.go"][0] = 'X'
	write(t, root, "a.go", "package changed\n")
	raw, err := snapshot.Diff(context.Background(), Ref{})
	if err != nil || !strings.Contains(string(raw), "+package original") || strings.Contains(string(raw), "package changed") {
		t.Fatalf("diff changed with checkout: %s %v", raw, err)
	}
	got, err := snapshot.FileContent(context.Background(), Ref{}, "a.go")
	if err != nil || string(got) != "package original\n" {
		t.Fatalf("snapshot read=%s %v", got, err)
	}
	entries, err := snapshot.ListDir(context.Background(), Ref{}, "")
	if err != nil || strings.Join(entries, ",") != "a.go,sub/" {
		t.Fatalf("entries=%v %v", entries, err)
	}
	if _, err := snapshot.FileContent(context.Background(), Ref{}, "new.go"); err == nil {
		t.Fatal("read outside snapshot")
	}
}

func TestCapturedTreeRetainsExactTextAndReportsBudgetOmissions(t *testing.T) {
	root := newRepo(t)
	tree := NewTree(NewLocal(root, nil), nil)
	tree.Capture = true
	if _, err := tree.Diff(context.Background(), Ref{}); err != nil {
		t.Fatal(err)
	}
	if len(tree.Snapshot) != 1 || len(tree.Snapshot["a.go"]) == 0 {
		t.Fatal("source bytes not captured")
	}
	write(t, root, "a.go", "package changed\n")
	raw, err := tree.Diff(context.Background(), Ref{})
	if err != nil || strings.Contains(string(raw), "package changed") {
		t.Fatalf("capture was not frozen: %v %s", err, raw)
	}
	limited := NewSnapshot(NewLocal(root, nil), map[string][]byte{"a.go": []byte("package a\n"), "b.go": []byte("package b\n")})
	limited.Budget, limited.Capture = 1, true
	if _, err := limited.Diff(context.Background(), Ref{}); err != nil {
		t.Fatal(err)
	}
	if body, err := limited.FileContent(context.Background(), Ref{}, "b.go"); err != nil || string(body) != "package b\n" {
		t.Fatalf("capture dropped budget-omitted source: %q %v", body, err)
	}
	if len(limited.Covered) != 1 || len(limited.Unbudgeted) != 1 {
		t.Fatalf("budget coverage=%v omitted=%v", limited.Covered, limited.Unbudgeted)
	}
}

func TestOversizedBinaryFilesRemainDistinctFromUnexaminedText(t *testing.T) {
	root := newRepo(t)
	write(t, root, "asset.png", "PNG"+strings.Repeat("\x00", 100))
	write(t, root, "large.go", strings.Repeat("package a\n", 20))
	tree := NewTree(NewLocal(root, nil), []string{"asset.png", "large.go"})
	tree.MaxBytes, tree.Capture = 16, true
	if _, err := tree.Diff(context.Background(), Ref{}); err != nil {
		t.Fatal(err)
	}
	if len(tree.Skipped) != 2 || len(tree.Covered) != 0 || len(tree.Snapshot) != 0 {
		t.Fatalf("unexpected oversized scope: %+v", tree)
	}
	reasons := map[string]string{}
	for _, skip := range tree.Skipped {
		reasons[skip.Path] = skip.Reason
	}
	if reasons["asset.png"] != "binary" || !strings.HasPrefix(reasons["large.go"], "larger than") {
		t.Fatalf("binary exclusion and text omission conflated: %v", reasons)
	}
	if raw, err := tree.Diff(t.Context(), Ref{}); err != nil || len(raw) != 0 || len(tree.Skipped) != 2 {
		t.Fatalf("empty capture lost its omissions: %s %v %+v", raw, err, tree.Skipped)
	}
}

func TestRepeatedCapturePreservesBudgetAndExclusionCoverage(t *testing.T) {
	for _, budget := range []int{0, 1} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			root := newRepo(t)
			write(t, root, "b.go", "package b\n")
			write(t, root, "asset.png", "PNG\x00")
			tree := NewTree(NewLocal(root, nil), nil)
			tree.Capture, tree.Budget = true, budget
			first, err := tree.Diff(t.Context(), Ref{})
			if err != nil {
				t.Fatal(err)
			}
			omitted, skipped := len(tree.Unbudgeted), len(tree.Skipped)
			if budget == 0 && skipped != 1 || budget == 1 && omitted != 2 {
				t.Fatalf("control did not omit/exclude expected files: %+v", tree)
			}
			second, err := tree.Diff(t.Context(), Ref{})
			if err != nil || !bytes.Equal(first, second) || len(tree.Unbudgeted) != omitted || len(tree.Skipped) != skipped {
				t.Fatalf("repeated capture erased scope: omitted=%v skipped=%v err=%v", tree.Unbudgeted, tree.Skipped, err)
			}
		})
	}
}
