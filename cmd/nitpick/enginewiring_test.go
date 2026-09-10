package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	used := map[string]bool{}

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

		// The assignment form first, because it has no literal to find.
		// fullreview builds a policied engine through newEngine and then clears
		// the field, so a scan of literals alone returns before it ever looks.
		if nulled.MatchString(text) {
			if _, ok := exempt[path]; !ok {
				missing = append(missing, path+": clears Policy or Models after wiring them")
			}
			used[path] = true
			// And on into the literals below, rather than returning: a file
			// that clears one engine may also build another, and one `= nil`
			// anywhere in it must not be what stops this scan from reading the
			// rest.
		}
		if !strings.Contains(text, "review.Engine{") && !strings.Contains(text, "&Engine{") {
			return nil
		}
		if _, ok := exempt[path]; ok {
			used[path] = true
			return nil
		}

		// The literal and everything until its closing brace at the same
		// indentation, which is where the fields are.
		for _, block := range engineLiterals(text) {
			if !wired(block, "Policy") || !wired(block, "Models") {
				missing = append(missing, fmt.Sprintf("%s: an engine with no %s", path, missingField(block)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// A stale exemption is a claim nobody checks. This one was: it named a file
	// whose engine is built by a helper, so the scan returned before the map
	// was ever read and deleting the entry changed nothing.
	for path, reason := range exempt {
		if !used[path] {
			t.Errorf("%s is exempt for %q and the scan never reached it, so the exemption proves nothing",
				path, reason)
		}
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
	case !wired(block, "Policy") && !wired(block, "Models"):
		return "Policy and no Models"
	case !wired(block, "Policy"):
		return "Policy"
	default:
		return "Models"
	}
}

// wired reports whether a field is set to something. `Policy: nil` is the field
// present and saying nothing, which a Contains check reads as wired.
func wired(block, field string) bool {
	i := strings.Index(block, field+":")
	if i < 0 {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(block[i+len(field)+1:]), "nil")
}

// nulled matches an engine having its resolver cleared after construction.
//
// The literal spelling only. A field cleared through a variable holding nil,
// or through a helper, reads as wired here. That is the narrowing this guard
// accepts: it catches the form fullreview.go uses and the form somebody
// copying it would write, and it is a source scan rather than a type system.
var nulled = regexp.MustCompile(`\.(Policy|Models)\s*=\s*nil`)

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

	// Every decision after the resolution reads the resolved config, whatever
	// the key. A list of forbidden names is the thing this file's other test
	// argues against, and half of one here named keys respond never read.
	//
	// The config handed to runImprove is `cfg,`, not a selector, so it
	// survives. It resolves for itself; see the comment there.
	after := text[strings.Index(text, "respondPolicy(ctx,"):]
	if i := strings.Index(after, "cfg."); i >= 0 {
		line := after[i:]
		if end := strings.IndexByte(line, '\n'); end > 0 {
			line = line[:end]
		}
		t.Errorf("respond reads a decision off the file the change supplied, after resolving: %s", line)
	}

	// And the exception, asserted rather than assumed, so removing it on
	// purpose is a deliberate edit here.
	if !strings.Contains(text, "cfg.Review.Mention") {
		t.Error("the mention no longer comes from the checkout; if that is deliberate, say so here")
	}
}

// The mention that selects the command comes from the resolved policy.
//
// converse.Command reads the verb relative to the mention, so the mention is
// the parser's origin rather than a yes-or-no gate: a change setting
// `mention: please` turns a maintainer's "please fix all of these" into a fix
// that writes to the repository. The first parse is a pre-filter that may
// over-match and must not over-act.
func TestTheCommandIsParsedAgainstTheResolvedMention(t *testing.T) {
	body, err := os.ReadFile("respond.go")
	if err != nil {
		t.Fatalf("read respond.go: %v", err)
	}
	text := string(body)

	if !strings.Contains(text, "converse.Command(ev.Body, policy.Review.Mention)") {
		t.Error("the command is chosen by the mention the change supplied, including which command")
	}

	// After the resolution, not before it.
	resolve := strings.Index(text, "respondPolicy(ctx,")
	reparse := strings.Index(text, "converse.Command(ev.Body, policy.Review.Mention)")
	if resolve < 0 || reparse < 0 || reparse < resolve {
		t.Error("the second parse does not follow the resolution")
	}

	// And only when the handle came from the file. An explicit -mention is the
	// operator naming it out of band, which no config may override.
	if !strings.Contains(text, "if fromFile && policy.Review.Mention != mention {") {
		t.Error("a config file can override an operator's -mention flag")
	}
}
