package review

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// PolicyResolver decides which configuration a review runs under.
//
// A change may not supply the policy it is reviewed under. open-nitpick
// reviews pull requests, and a pull request can edit the .nitpick.yaml it is
// reviewed under, so when the change modifies that file, the file in the
// change is not authoritative for this review and policy comes from a revision
// the change cannot write.
//
// This is deliberately one seam rather than a growing list of scrubbed keys.
// config.sanitize withholds base_url, api_key_env, extra, allow_private_endpoint
// and persona.custom one key at a time, which is exactly how instructions[],
// review.ignore, the severity gates, persona.nitpick, validation.enabled and the
// budgets were all missed: every feature that adds a knob adds another key to
// remember. The invariant does not grow with the schema.
type PolicyResolver interface {
	// ResolvePolicy reports whether the change under review modifies the
	// configuration's own source, and returns the policy to apply when it does.
	//
	// changed carries every path in the change, taken from the parsed diff
	// before any policy has narrowed it. Deciding this against an
	// already-filtered set would let a hostile ignore rule hide the very edit
	// that installed it.
	//
	// pr is the pull request the engine already fetched, so a resolver needing
	// the base revision does not have to buy it a second time from the forge.
	// It may be nil.
	ResolvePolicy(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, changed []string) (cfg *config.Config, modified bool, err error)
}

// Policy records which configuration a review ran under.
type Policy struct {
	// Config is the configuration the run used. It is the engine's own unless
	// Replaced, and never nil in a report the engine produced.
	Config *config.Config

	// Replaced reports that the change under review edits the configuration
	// source, so the configuration carried BY THE CHANGE was not applied.
	Replaced bool

	// Modified names the configuration source the change edits, empty unless
	// Replaced. It is recorded rather than derived at render time because by
	// then the engine holds only the policy that replaced it, and a notice that
	// cannot name the file leaves a contributor with nothing to look at.
	Modified string
}

// Source names the policy that governed the review, in the words the published
// summary prints.
//
// A substituted policy is described by config itself, because it knows things
// this package cannot reconstruct: which revision the accepted version was
// read at, and (when even that failed), why nothing but built-in defaults was
// left. "Defaults" alone would leave a maintainer unable to tell a base
// revision that carried no configuration from one that could not be
// read.
func (p Policy) Source() string {
	if p.Config == nil {
		return "built-in defaults"
	}
	if p.Config.Policy.Substituted() {
		return p.Config.Policy.String()
	}
	if strings.TrimSpace(p.Config.Source) == "" {
		return "built-in defaults"
	}
	return p.Config.Source
}

// resolvePolicy decides which configuration this review runs under, and returns
// it together with what was set aside to get there.
//
// Failure is fatal on purpose. A resolver that errors has not told us whether
// the change edits the policy, and continuing under the change's own
// configuration on a maybe is the outcome this exists to prevent, a run that
// reports success having read nothing is worse than a run that fails.
func (e *Engine) resolvePolicy(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, files diff.Files) (Policy, error) {
	current := Policy{Config: e.Config}

	if e.Policy == nil {
		// Said out loud. An engine reviewing a pull request without a resolver
		// runs under whatever configuration it was handed, and this branch was
		// silent, so the one command that had forgotten ran that way with
		// nothing anywhere recording it. Two callers reach here on purpose, a
		// tree review and the eval harness, and neither reviews a pull request.
		e.log().Debug("no policy resolver; reviewing under the configuration this engine was given")
		return current, nil
	}

	changed := make([]string, 0, len(files))
	for _, f := range files {
		changed = append(changed, f.Path)
		// A rename is still an edit to the file it moved. Sending only the new
		// path would let `git mv .nitpick.yaml x && write .nitpick.yaml` read as
		// a change that touches no configuration.
		if f.OldPath != "" && f.OldPath != f.Path {
			changed = append(changed, f.OldPath)
		}
	}

	resolved, modified, err := e.Policy.ResolvePolicy(ctx, ref, pr, changed)
	if err != nil {
		return Policy{}, fmt.Errorf("resolve review policy: %w", err)
	}
	if !modified {
		return current, nil
	}
	if resolved == nil {
		// The resolver said the change edits the configuration and then gave
		// nothing to use instead. Falling back to e.Config here would apply the
		// change's own policy at the one moment it must not be applied.
		return Policy{}, errors.New("resolve review policy: the change modifies the configuration " +
			"but no replacement policy was returned")
	}

	policy := Policy{Config: resolved, Replaced: true, Modified: modifiedName(resolved, e.Config)}

	e.log().Warn("the change under review modifies the configuration; the configuration in the change was not applied",
		"modified", policy.Modified, "policy", policy.Source())

	return policy, nil
}

// modifiedName is the file a contributor has to look at, in a form worth
// printing on a pull request.
//
// The resolver's own answer is the repository-relative path it matched, so it
// names a config below the root in full and survives a change adding or
// deleting the file. The fallbacks cover a resolver that records none:
// loaded.Source is absolute and in CI carries the runner's workspace path,
// while its basename reports tools/ci/.nitpick.yaml as `.nitpick.yaml`, which
// does not exist in the repository.
func modifiedName(resolved, loaded *config.Config) string {
	if resolved != nil && strings.TrimSpace(resolved.Policy.Modified) != "" {
		return resolved.Policy.Modified
	}
	if loaded == nil || strings.TrimSpace(loaded.Source) == "" {
		return config.FileName
	}
	return filepath.Base(loaded.Source)
}
