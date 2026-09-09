package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every engine that reviews a pull request resolves the policy it reviews
// under, and the two that do not are named here with their reason.
//
// TestTheReviewEngineIsWiredAgainstTheChangesOwnPolicy guards one constructor
// and says in its own header that dropping the wiring elsewhere is a clean
// build and a green suite. It was right: a second constructor had neither
// field, so `@open-nitpick improve` ran a whole review under configuration the
// change supplied, and internal/review's nil branch said nothing about it.
//
// A scan rather than a list, for the reason internal/config's modelRoleKeys is
// reflective: a list is what somebody forgets to extend, and the thing being
// guarded is precisely somebody adding a caller and forgetting.
func TestEveryReviewingEngineResolvesItsPolicy(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	// The two that review no pull request. A tree review changes every file by
	// construction and its operator is the policy's author; the eval harness
	// reviews fixtures with no forge behind them.
	exempt := map[string]string{
		filepath.Join(root, "cmd", "nitpick", "fullreview.go"): "a tree review, whose operator wrote the policy",
		filepath.Join(root, "internal", "evals", "harness.go"): "fixtures, with no forge and no base revision",
	}

	var missing []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			if name := d.Name(); name == ".git" || name == "website" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		if !strings.Contains(text, "review.Engine{") && !strings.Contains(text, "&Engine{") {
			return nil
		}
		if _, ok := exempt[path]; ok {
			return nil
		}

		// The literal and everything until its closing brace at the same
		// indentation, which is where the fields are.
		for _, block := range engineLiterals(text) {
			if !strings.Contains(block, "Policy:") || !strings.Contains(block, "Models:") {
				missing = append(missing, fmt.Sprintf("%s: an engine with no %s", path, missingField(block)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(missing) > 0 {
		t.Errorf("these review a pull request under whatever configuration they were handed:\n  %s\n\n"+
			"Wire Policy and Models as cmd/nitpick/review.go does, or add the file to exempt with the reason.",
			strings.Join(missing, "\n  "))
	}
}

// engineLiterals returns the text of each Engine composite literal in a file.
func engineLiterals(text string) []string {
	var out []string
	for _, opener := range []string{"review.Engine{", "&Engine{"} {
		rest := text
		for {
			i := strings.Index(rest, opener)
			if i < 0 {
				break
			}
			rest = rest[i+len(opener):]
			depth := 1
			end := len(rest)
			for j, r := range rest {
				switch r {
				case '{':
					depth++
				case '}':
					if depth--; depth == 0 {
						end = j
					}
				}
				if depth == 0 {
					break
				}
			}
			out = append(out, rest[:end])
		}
	}
	return out
}

func missingField(block string) string {
	switch {
	case !strings.Contains(block, "Policy:") && !strings.Contains(block, "Models:"):
		return "Policy and no Models"
	case !strings.Contains(block, "Policy:"):
		return "Policy"
	default:
		return "Models"
	}
}

// respond resolves the policy, and reads no decision off the file on disk
// afterwards.
//
// The behavioural guards in respond_policy_test.go call respondPolicy
// directly, so they hold whatever runRespond does with it: dropping the call
// leaves them green. That is the same shape as an engine built without a
// resolver, and the same answer, a check on the source.
//
// The mention is the one deliberate exception. It decides only whether the
// comment addresses us, resolving needs a forge call, and paying for that on
// every comment in the repository is a cost nobody asked for.
func TestRespondResolvesBeforeItDecidesAnything(t *testing.T) {
	body, err := os.ReadFile("respond.go")
	if err != nil {
		t.Fatalf("read respond.go: %v", err)
	}
	text := string(body)

	if !strings.Contains(text, "respondPolicy(ctx,") {
		t.Fatal("runRespond does not resolve a policy: a change that edits .nitpick.yaml is " +
			"answered, and its findings applied, under the configuration it wrote for itself")
	}

	// Every decision reads the resolved config. cfg is the file on disk.
	for _, forbidden := range []string{
		"cfg.Review.Respond",
		"cfg.Models",
		"cfg.Review.Approve",
		"cfg.Validation",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("respond reads %s off the file the change supplied; read it from the resolved policy", forbidden)
		}
	}

	// And the exception, asserted rather than assumed, so removing it on
	// purpose is a deliberate edit here.
	if !strings.Contains(text, "cfg.Review.Mention") {
		t.Error("the mention no longer comes from the checkout; if that is deliberate, say so here")
	}
}
