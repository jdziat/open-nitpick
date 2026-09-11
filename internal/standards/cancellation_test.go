package standards

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTreeWalkStopsWhenCancelledBetweenEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "package"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package", "app.go"), []byte("package p\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := ReadTreeContext(ctx, root, func(name string) bool {
		if name == "package" {
			cancel()
		}
		return false
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("walk continued after cancellation: %v", err)
	}
	files, err := ReadTreeContext(context.Background(), root, nil)
	if err != nil || len(files) != 1 {
		t.Fatalf("uncancelled walk: files=%v err=%v", files, err)
	}
}

func TestCancelledMeasurementReturnsNoSuccessfulReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	files := []File{{Path: "app.go", Src: []byte("package p\n")}}
	report, err := MeasureContext(ctx, files, Options{})
	if !errors.Is(err, context.Canceled) || len(report.Files) != 0 {
		t.Fatalf("measurement after cancellation: report=%+v err=%v", report, err)
	}
	report, err = MeasureContext(context.Background(), files, Options{})
	if err != nil || report.Files["go"] != 1 {
		t.Fatalf("uncancelled measurement: report=%+v err=%v", report, err)
	}
}
