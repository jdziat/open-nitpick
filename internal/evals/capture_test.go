package evals

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedSampleIsRetainedWithoutClaimingSilence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	dump, err := NewDump(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := Fixtures()[0]
	measurement := &SampleMeasurement{SampleID: "sample-1", Failure: "provider interrupted"}
	if err := dump.Record(DumpSample{Model: "test", Fixture: fixture, Measurement: measurement}); err != nil {
		t.Fatal(err)
	}
	if err := dump.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record DumpRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Silent || record.Measurement == nil || record.Measurement.Failure == "" || record.FixtureHash == "" {
		t.Fatalf("failure became successful silence: %+v", record)
	}
	if _, _, err := GroupDump([]DumpRecord{record}); err == nil {
		t.Fatal("failed sample was eligible for rejudging as silence")
	}
}

func TestSampleCaptureRetainsFailuresAndRejectsOverwrite(t *testing.T) {
	dir := t.TempDir()
	f := Fixtures()[0]
	result := RunResult{Model: "test/model", Fixture: f.Name, Run: 1, Err: errors.New("interrupted")}
	if err := CaptureSample(dir, "", f, result); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*", "*", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("capture files %v: %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var artifact SampleArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Failure != "interrupted" || artifact.FixtureHash == "" {
		t.Fatalf("incomplete artifact: %s", data)
	}
	if err := CaptureSample(dir, "", f, result); err == nil || !strings.Contains(err.Error(), "exist") {
		t.Fatalf("existing evidence was overwritten: %v", err)
	}
}
