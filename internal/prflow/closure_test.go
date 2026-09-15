package prflow

import (
	"context"
	"strings"
	"testing"
)

func TestAnalyzeResolvesImmediateDeferredAndGoroutineClosures(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path:         "p.go",
		ChangedLines: []int{2},
		Content: []byte(`package p
func Changed() {
		(func() { immediate() })()
		defer func() { deferred() }()
		go func() { goroutine() }()
}
func immediate() {}
func deferred() {}
func goroutine() {}
`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, node := range result.Nodes {
		labels[node.Label] = true
	}
	for _, want := range []string{"p.immediate", "p.deferred", "p.goroutine"} {
		if !labels[want] {
			t.Fatalf("closure target %q missing: nodes=%+v edges=%+v", want, result.Nodes, result.Edges)
		}
	}
	closureEdges := map[string]bool{}
	for _, edge := range result.Edges {
		if strings.Contains(edge.From, "<closure@") {
			closureEdges[edge.To] = true
		}
	}
	for _, want := range []string{"p.immediate", "p.deferred", "p.goroutine"} {
		if !closureEdges[want] {
			t.Fatalf("closure body edge to %q missing: edges=%+v", want, result.Edges)
		}
	}
	var immediate, deferred, goroutine bool
	for _, edge := range result.Edges {
		if !strings.Contains(edge.To, "<closure@") {
			continue
		}
		switch edge.Kind {
		case "closure":
			immediate = immediate || edge.Resolution == ResolutionResolved
		case "defer":
			deferred = deferred || edge.Resolution == ResolutionResolved
		case "go":
			goroutine = goroutine || edge.Resolution == ResolutionResolved
		}
	}
	if !immediate || !deferred || !goroutine {
		t.Fatalf("closure scheduling edges immediate=%v deferred=%v goroutine=%v: %+v", immediate, deferred, goroutine, result.Edges)
	}
}

func TestAnalyzeRetainsCallbackLiteralClosureChain(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path:         "p.go",
		ChangedLines: []int{2},
		Content: []byte(`package p
func Changed() { register(func() { callback() }) }
func register(fn func()) { fn() }
func callback() {}
`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var callbackEdge, bodyEdge bool
	for _, edge := range result.Edges {
		if edge.Kind == "callback" && edge.Resolution == ResolutionInferred && strings.Contains(edge.Reason, "callback_literal") {
			callbackEdge = true
		}
		if strings.Contains(edge.From, "<closure@") && edge.To == "p.callback" && edge.Resolution == ResolutionResolved {
			bodyEdge = true
		}
	}
	if !callbackEdge || !bodyEdge {
		t.Fatalf("callback closure chain callback=%v body=%v: nodes=%+v edges=%+v", callbackEdge, bodyEdge, result.Nodes, result.Edges)
	}
}
