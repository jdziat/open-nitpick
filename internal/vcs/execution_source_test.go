package vcs

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionListingsRefuseUnsupportedOrTruncatedForgeTrees(t *testing.T) {
	for _, kind := range []string{"regular", "symlink", "submodule", "unknown", "saturated"} {
		t.Run(kind, func(t *testing.T) {
			gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, strings.Repeat("a", 40)) {
					t.Error("directory read was not pinned")
				}
				entries := []map[string]any{{"path": "main.go", "type": "blob", "mode": "100644"}}
				switch kind {
				case "symlink":
					entries[0]["mode"] = "120000"
				case "submodule":
					entries[0]["mode"] = "160000"
					entries[0]["type"] = "commit"
				case "unknown":
					entries[0]["type"] = "other"
				case "saturated":
					for len(entries) < 1000 {
						entries = append(entries, map[string]any{"path": "file.go", "type": "blob", "mode": "100644"})
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"tree": entries, "truncated": kind == "saturated"})
			})
			entries, err := gh.ListSnapshotDir(t.Context(), Ref{Owner: "o", Repo: "r", Head: strings.Repeat("a", 40)}, "")
			if kind == "regular" {
				if err != nil || len(entries) != 1 {
					t.Fatalf("positive listing unavailable: %v %v", entries, err)
				}
			} else if err == nil {
				t.Fatalf("incomplete %s listing accepted", kind)
			}
		})
	}
}

func TestExecutionListingsRefuseLocalSymlinksAndGitlinks(t *testing.T) {
	for _, kind := range []string{"regular", "symlink", "gitlink"} {
		t.Run(kind, func(t *testing.T) {
			root := newRepo(t)
			switch kind {
			case "symlink":
				if err := os.Symlink("a.go", filepath.Join(root, "linked.go")); err != nil {
					t.Fatal(err)
				}
				gitIn(t, root, "add", "linked.go")
				gitIn(t, root, "commit", "-qm", "link")
			case "gitlink":
				head := headOf(t, root)
				gitIn(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",submodule")
				gitIn(t, root, "commit", "-qm", "gitlink")
			}
			entries, err := NewLocal(root, nil).ListSnapshotDir(t.Context(), Ref{Head: headOf(t, root)}, "")
			if kind == "regular" {
				if err != nil || len(entries) != 1 {
					t.Fatalf("regular source unavailable: %v %v", entries, err)
				}
			} else if err == nil {
				t.Fatalf("unsupported %s source accepted", kind)
			}
		})
	}
}

func TestNestedExecutionTreeCannotHideTruncation(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		name := "complete"
		if truncated {
			name = "truncated"
		}
		t.Run(name, func(t *testing.T) {
			gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/git/trees/"+strings.Repeat("a", 40)) {
					_ = json.NewEncoder(w).Encode(map[string]any{"tree": []any{map[string]any{"path": "src", "type": "tree", "mode": "040000", "sha": strings.Repeat("b", 40)}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"truncated": truncated, "tree": []any{map[string]any{"path": "file.go", "type": "blob", "mode": "100644"}}})
			})
			got, err := gh.ListSnapshotDir(t.Context(), Ref{Owner: "o", Repo: "r", Head: strings.Repeat("a", 40)}, "src")
			if truncated && err == nil {
				t.Fatal("nested truncated tree accepted")
			}
			if !truncated && (err != nil || len(got) != 1 || got[0] != "file.go") {
				t.Fatalf("complete nested tree rejected: %v %v", got, err)
			}
		})
	}
}
