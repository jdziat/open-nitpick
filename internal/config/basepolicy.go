package config

import (
	"context"
	"strings"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

// BasePolicy resolves review policy from the revision a change is measured
// against, whenever the change modifies the configuration file itself.
//
// It is the only part of this defense that needs a forge, and it is separate
// for that reason: detection and resolution are pure and exercised without a
// repository, while this supplies the two things only a provider can answer,
// which revision counts as "before this change", and what a file contained
// there.
type BasePolicy struct {
	// RepoRoot is the checkout that diff paths are relative to.
	RepoRoot string

	// Loaded is the configuration read from the checkout. Its Source names the
	// file a change would have to modify to be supplying its own policy.
	Loaded *Config

	// Provider reads the base revision. One that cannot name a base revision is
	// not an error: policy falls back to defaults and the run reports why.
	Provider vcs.Provider
}

// ResolvePolicy reports whether the change under review modifies the
// configuration's own source, and returns the policy to apply when it does.
//
// pr is the pull request the caller already fetched, and may be nil. It is
// taken rather than looked up because the forge answer is expensive and can
// move: asking again costs a second unbounded API call, and a pull request
// synced between the two reads would hand back a base revision belonging to a
// different diff than the one being reviewed.
//
// modified is false, with a nil config, for the overwhelmingly common change
// that leaves the config file alone, so the ordinary run costs nothing: no
// forge round trip is made until a change has touched the file.
func (b *BasePolicy) ResolvePolicy(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, changed []string) (*Config, bool, error) {
	if b == nil || b.Loaded == nil {
		return nil, false, nil
	}

	// ResolvePolicy refuses an empty root, and its refusal is unreachable from
	// here: detection has to run first so an ordinary change costs no forge
	// round trip, and SelfModified with an empty root resolves it to the process
	// working directory, finds the config outside that, and answers "not
	// modified", the answer that reviews the change under its own policy. The
	// guard is repeated rather than relied upon.
	if strings.TrimSpace(b.RepoRoot) == "" {
		return nil, false, errNoRepoRoot
	}

	if !b.Loaded.SelfModified(b.RepoRoot, changed) {
		return nil, false, nil
	}

	req := PolicyRequest{RepoRoot: b.RepoRoot, Changed: changed}

	switch rev, err := b.baseRevision(ctx, ref, pr); {
	case err != nil:
		// Not fatal. An unnameable base revision means built-in defaults, which
		// ResolvePolicy records along with this reason; what it must never mean
		// is carrying on under the configuration the change supplied.
		req.BaseUnavailable = err
	default:
		req.BaseRev = rev
		req.ReadBase = func(ctx context.Context, path string) ([]byte, error) {
			return b.Provider.FileContent(ctx, ref.At(rev), path)
		}
	}

	cfg, err := ResolvePolicy(ctx, b.Loaded, req)
	if err != nil {
		return nil, true, err
	}
	return cfg, true, nil
}

// baseRevision names the revision the change is measured against, preferring
// the answer the caller already has.
//
// Only a SHA is taken from the pull request. A branch name moves, and the
// provider's own resolution knows things this cannot, Local computes the merge
// base, which is the revision its diff subtracted.
func (b *BasePolicy) baseRevision(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest) (string, error) {
	if pr != nil {
		if sha := strings.TrimSpace(pr.BaseSHA); sha != "" {
			return sha, nil
		}
	}
	if b.Provider == nil {
		return "", vcs.ErrNoBaseRevision
	}
	return vcs.BaseRevision(ctx, b.Provider, ref)
}
