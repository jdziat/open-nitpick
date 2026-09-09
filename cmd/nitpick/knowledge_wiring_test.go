package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// `review.knowledge: true` used to mean four different things depending on
// which command read it: review built a retriever, and full-review, slop, the
// MCP tools and improve silently did not. Nothing failed, so nothing said so,
// and a measurement taken through the wrong command would have compared the
// control against itself.
//
// The guard is structural rather than behavioural because the failure was
// structural: a new command that builds its own engine is exactly how this
// came back, and no test of the four that exist today would have caught it.
func TestEveryEngineWiresRetrieval(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse the command package: %v", err)
	}

	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if !buildsAnEngine(fn) {
					continue
				}
				if !wiresKnowledge(fn) {
					t.Errorf("%s builds a review.Engine and never sets Knowledge (%s). "+
						"Build it through newEngine, or set Knowledge from review.BuildKnowledge: "+
						"an engine without it reviews with retrieval off however the config reads.",
						fn.Name.Name, fset.Position(fn.Pos()))
				}
			}
		}
	}
}

// buildsAnEngine reports whether fn contains a review.Engine composite literal.
func buildsAnEngine(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Engine" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "review" {
			found = true
		}
		return true
	})
	return found
}

// wiresKnowledge reports whether fn names Knowledge at all, in the literal or
// in an assignment after it.
func wiresKnowledge(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.KeyValueExpr:
			if k, ok := v.Key.(*ast.Ident); ok && k.Name == "Knowledge" {
				found = true
			}
		case *ast.SelectorExpr:
			if v.Sel.Name == "Knowledge" {
				found = true
			}
		}
		return true
	})
	return found
}

// And the behaviour behind the guard: an operator who asked for retrieval and
// misconfigured it is told, rather than handed a review that quietly did less.
func TestNewEngineRefusesAMisconfiguredEmbedder(t *testing.T) {
	t.Setenv("SYNTHETIC_API_KEY", "syn_test")

	cfg := config.Defaults()
	cfg.Review.Knowledge = true
	cfg.Models.Embed = &config.ModelSpec{Provider: "synthetic", Model: "hf:not-the-indexed-model"}

	_, err := newEngine(context.Background(), &reviewFlags{}, t.TempDir(), cfg,
		vcs.NewLocal(t.TempDir(), io.Discard), slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("an index built by a different embedding model was accepted; the run would report retrieval on and retrieve nothing")
	}
	if !strings.Contains(err.Error(), "knowledge retrieval") {
		t.Errorf("error %q does not say retrieval is what failed", err)
	}
}

// Retrieval off is the default and must stay free: no embedder, no index read,
// no error.
func TestNewEngineLeavesRetrievalOffByDefault(t *testing.T) {
	cfg := config.Defaults()

	engine, err := newEngine(context.Background(), &reviewFlags{}, t.TempDir(), cfg,
		vcs.NewLocal(t.TempDir(), io.Discard), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newEngine with retrieval off: %v", err)
	}
	if engine.Knowledge != nil {
		t.Error("retrieval is off by default and a retriever was built anyway")
	}
}
