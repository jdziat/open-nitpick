package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

// A change may not supply the policy it is reviewed under.
//
// open-nitpick reviews a pull request under a .nitpick.yaml that the same pull
// request can edit, and nearly every key in that file decides whether a finding
// is ever produced or ever published: review.ignore skips files outright,
// min_severity and fail_on delete findings and un-gate the build,
// persona.nitpick filters whole classes away, the budgets decide how much of the
// diff is read at all, validation.enabled switches on the one pass whose purpose
// is removing findings, and instructions[].prompt is free text rendered at
// column 0 under "Repository instructions for this path:", the position the
// model is told to trust.
//
// sanitize() answers a narrower question and stays: a config the change did not
// touch can still name an endpoint or a credential variable. But scrubbing is
// per key, so its list grows with every knob a feature adds and the next knob
// gets missed the same way instructions[] was. This file is the invariant
// instead. When the change modifies the config file, that file's policy is not
// authoritative for this review; policy comes from the version the maintainers
// already accepted, and from built-in defaults when that cannot be read. Never
// from the change.

// PolicyOrigin names where the configuration governing a review came from.
type PolicyOrigin string

// Policy origins, in the preference order ResolvePolicy applies.
const (
	// OriginCheckout is the ordinary case: the config file as the checkout has
	// it, which this change did not modify.
	OriginCheckout PolicyOrigin = "checkout"

	// OriginBase means the change modifies the config file, so policy was read
	// from the base revision, the version maintainers already accepted, which
	// preserves every legitimate setting while ignoring what the change added.
	OriginBase PolicyOrigin = "base"

	// OriginDefaults means the change modifies the config file and no accepted
	// version could be read, so none of the repository's configuration is in
	// force. It is the fallback, never a preference.
	OriginDefaults PolicyOrigin = "defaults"
)

// Policy records which configuration decided a review's behavior.
//
// It exists for the maintainer legitimately editing .nitpick.yaml: their new
// ignore rule does nothing in the very pull request that adds it, and without
// this they have no way to find out why. It is surfaced the way Dropped is,
// logged on every run and printed by explain-config.
type Policy struct {
	// Origin says which of the three sources applied.
	Origin PolicyOrigin

	// Path is the config file the policy came from: a filesystem path for
	// OriginCheckout, repository-relative for OriginBase, empty when no file
	// supplied anything.
	Path string

	// Ref is the revision Path was read at. Set only for OriginBase.
	Ref string

	// Modified is the repository-relative path of the config file the change
	// edits, the file whose policy was withheld. Set whenever the policy was
	// substituted, including for OriginDefaults, where Path is empty because no
	// file supplied anything.
	//
	// It is separate from Path because a reader needs both halves of the
	// sentence: which file to go and look at, and where the policy that ran
	// instead came from. Deriving it later from Source cannot work, Source is
	// absolute, so in CI it carries the runner's workspace path, and it is empty
	// altogether when the change ADDS or DELETES the file.
	Modified string

	// Reason states why the checkout's config was set aside, in one sentence a
	// maintainer can act on. Empty for OriginCheckout.
	//
	// One sentence means one LINE: it is published inside a blockquote on a pull
	// request, and it carries forge and parser text, errors.Join separates its
	// messages with newlines, so it is flattened where it is built rather than
	// at each of the places that print it.
	Reason string
}

// Substituted reports whether the config file in the checkout was set aside,
// which is the condition worth warning about.
func (p Policy) Substituted() bool {
	return p.Origin == OriginBase || p.Origin == OriginDefaults
}

// String renders the policy as one line for a log or explain-config.
func (p Policy) String() string {
	switch p.Origin {
	case OriginBase:
		return fmt.Sprintf("%s at base revision %s (%s)", p.Path, p.Ref, p.Reason)
	case OriginDefaults:
		return fmt.Sprintf("built-in defaults (%s)", p.Reason)
	}

	// OriginCheckout, and the zero value of a Config that never went through
	// resolution, are both governed by the file they were loaded from.
	if p.Path == "" {
		return "built-in defaults and environment (no config file)"
	}
	return p.Path
}

// BaseReader reads a repository-relative path as it exists at the base
// revision.
//
// Its error is not classified by the caller. Any failure to obtain the accepted
// version means defaults, because the one alternative, the version under
// review, is what the base revision exists to keep out.
type BaseReader func(ctx context.Context, path string) ([]byte, error)

// PolicyRequest describes the change under review and where a copy of the
// configuration that the change cannot have edited may be read from.
type PolicyRequest struct {
	// RepoRoot is the checkout Changed's paths are relative to. Required:
	// without it there is no way to tell an in-repo config from one passed with
	// -config from outside, and guessing either way is wrong.
	RepoRoot string

	// Changed lists every repository-relative path the diff touches. Callers
	// should include a rename's old path alongside its new one, so a change
	// that renames a file onto the config path is still caught.
	Changed []string

	// BaseRev names the revision ReadBase reads at. It is reported, never
	// parsed.
	BaseRev string

	// ReadBase reads a path at BaseRev. Nil when the provider cannot name a
	// base revision at all, which forces the defaults fallback rather than an
	// invented revision.
	ReadBase BaseReader

	// BaseUnavailable is why no base revision could be named, when that is why
	// ReadBase is nil. It is reported, never acted on: "your configuration did
	// not apply" is a dead end for a maintainer without the reason attached.
	BaseUnavailable error
}

// ResolvePolicy returns the configuration that governs this review.
//
// cfg is configuration as loaded from the checkout. When the change under
// review does not modify the file it came from, cfg is authoritative and comes
// back with nothing but its Policy stated. When the change does modify that
// file, the file describes policy its author wrote for their own review, so it
// is set aside in favor of the version at the base revision, and of built-in
// defaults when that cannot be read.
//
// The returned Config always carries a Policy saying which of the three
// applied, so a run can always answer "which policy was this reviewed under".
func ResolvePolicy(ctx context.Context, cfg *Config, req PolicyRequest) (*Config, error) {
	if cfg == nil {
		return nil, errors.New("config: no configuration to resolve policy from")
	}
	if strings.TrimSpace(req.RepoRoot) == "" {
		return nil, errNoRepoRoot
	}

	rel, self := cfg.selfModified(req.RepoRoot, req.Changed)
	if !self {
		cfg.Policy = Policy{Origin: OriginCheckout, Path: cfg.Source}
		return cfg, nil
	}

	if req.ReadBase == nil || strings.TrimSpace(req.BaseRev) == "" {
		reason := fmt.Sprintf(
			"%s is modified by this change and no base revision is available to read the accepted version from",
			rel)
		if req.BaseUnavailable != nil {
			reason += ": " + oneLine(req.BaseUnavailable.Error())
		}
		return defaultsPolicy(rel, reason)
	}

	data, err := req.ReadBase(ctx, rel)
	if err != nil {
		return defaultsPolicy(rel, baseUnreadable(rel, req.BaseRev, err))
	}

	base, err := loadBytes(data, rel+"@"+req.BaseRev)
	if err != nil {
		return defaultsPolicy(rel, fmt.Sprintf(
			"%s is modified by this change and the version at %s is not usable: %s",
			rel, req.BaseRev, oneLine(err.Error())))
	}

	base.Policy = Policy{
		Origin:   OriginBase,
		Path:     rel,
		Ref:      req.BaseRev,
		Modified: rel,
		Reason:   "this change modifies it, so the version already accepted at the base revision is in force",
	}
	return base, nil
}

// errNoRepoRoot is shared with BasePolicy, which has to make the same refusal
// before it reaches this function.
//
// Defaulting to the working directory would quietly place most configs "outside
// the repository", and outside means trusted. A defense that fails open when a
// caller forgets an argument is not one.
var errNoRepoRoot = errors.New("config: a repository root is required to resolve review policy")

// baseUnreadable words the fallback for a base revision that yielded no
// configuration.
//
// "Not found" is separated from every other failure because it is the ordinary
// and benign case, the pull request that ADDS the config file, or moves another
// file onto its path, and reporting that as "no version of it could be read:
// vcs: not found" describes a normal event in the vocabulary of an
// infrastructure fault. The decision is identical either way, and deliberately
// so; only the sentence a contributor reads differs.
func baseUnreadable(rel, rev string, err error) string {
	if errors.Is(err, vcs.ErrNotFound) {
		return fmt.Sprintf(
			"%s is added by this change and the base revision %s carries no configuration, "+
				"so there is no accepted version to review under", rel, rev)
	}
	return fmt.Sprintf("%s is modified by this change and no version of it could be read at %s: %s",
		rel, rev, oneLine(err.Error()))
}

// defaultsPolicy is the last resort: nothing the change supplied, and no
// accepted version to fall back to either. modified names the file that was set
// aside, which is all a reader has left to go on once no file supplies policy.
func defaultsPolicy(modified, reason string) (*Config, error) {
	cfg, err := defaultConfig()
	if err != nil {
		// Defaults name no model, so a repository that names its own only
		// inside .nitpick.yaml has nothing left to run on, which is most of
		// them, in the pull request that adds that file. Failing loudly here is
		// the point: the only way to carry on would be to review under policy
		// the change wrote for its own review. The caller publishes this
		// sentence on the pull request rather than leaving it in a CI log,
		// because the person who needs it is the one who wrote the file.
		return nil, fmt.Errorf(
			"%s, and built-in defaults cannot run on their own: %w "+
				"(set %s and %s in the environment, or pass -config a file outside the repository)",
			reason, err, EnvProvider, EnvModel)
	}

	cfg.Policy = Policy{Origin: OriginDefaults, Modified: modified, Reason: reason}
	return cfg, nil
}

// oneLine collapses text onto a single line. Reason and the errors it carries
// are published inside a markdown blockquote, and validate joins its messages
// with newlines.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// SelfModified reports whether the change under review modifies the config file
// this configuration was loaded from.
//
// changed holds repository-relative diff paths; repoRoot is the checkout they
// are relative to. A config supplied with -config from outside the repository is
// never self-modified: the change cannot reach it, so a file of the same name
// in the diff is a different file.
func (c *Config) SelfModified(repoRoot string, changed []string) bool {
	_, self := c.selfModified(repoRoot, changed)
	return self
}

// RepoRelative returns the repository-relative path of the file this
// configuration came from, or would have come from had it been there.
//
// ok is false when no file is involved and when the file lies outside
// repoRoot, the two cases no change under review can reach. Callers describing
// policy to an operator have to tell those from an in-repo config, and an
// absolute path out of a CI runner's workspace tells a reader nothing.
func (c *Config) RepoRelative(repoRoot string) (string, bool) {
	paths := c.configPaths(repoRoot)
	if len(paths) == 0 {
		return "", false
	}
	return paths[0], true
}

// selfModified also returns the config's repository-relative path, which is
// what reading the base version needs.
func (c *Config) selfModified(repoRoot string, changed []string) (string, bool) {
	candidates := c.configPaths(repoRoot)
	if len(candidates) == 0 {
		return "", false
	}

	for _, path := range changed {
		normalized := normalizePath(path)
		for _, candidate := range candidates {
			// Compared case-insensitively because the filesystems this runs on
			// are: on a macOS or Windows runner, os.ReadFile(root/.nitpick.yaml)
			// happily reads a file committed as .NITPICK.YAML, so a byte-exact
			// comparison answers "not self-modified" for a change that renamed
			// the config and rewrote it. Over-matching on a case-sensitive
			// checkout costs one base read and a notice naming a file the
			// contributor did edit; under-matching costs the whole defense.
			if !strings.EqualFold(normalized, candidate) {
				continue
			}
			// The candidate rather than the diff's spelling: the base revision
			// has to be read at the name this configuration resolves
			// to, not at whatever case or alias the change chose to write.
			return candidate, true
		}
	}
	return "", false
}

// configPaths returns every repository-relative name that refers to this
// configuration's own file.
//
// There is more than one because a file can be reached by more than one name,
// and a change only has to edit it under ONE of them to be supplying its own
// policy.
func (c *Config) configPaths(repoRoot string) []string {
	if c == nil {
		return nil
	}

	// Source is empty when no file was read, and a change that DELETES the
	// config file, or renames it away, or replaces it with a dangling symlink
	//, produces exactly that checkout. Skipping it there would let a change
	// swap the repository's accepted policy for built-in defaults by removal
	// rather than by edit, with nothing reported: the weakest policy available,
	// chosen by the change, silently.
	source := strings.TrimSpace(c.Source)
	if source == "" {
		source = strings.TrimSpace(c.Missing)
	}
	if source == "" {
		return nil
	}

	var out []string
	add := func(p string) {
		// Source is a filesystem path and diff paths are repository-relative,
		// so comparing the two as they stand never matches. The check would
		// report "not self-modified" for every change ever made, leaving the
		// hole open while looking closed.
		rel, ok := repoRelative(repoRoot, p)
		if !ok || slices.Contains(out, rel) {
			return
		}
		out = append(out, rel)
	}

	add(source)

	// os.ReadFile follows symlinks, so a committed `.nitpick.yaml` pointing at
	// `ci/nitpick.yaml` puts the bytes governing the review in ci/nitpick.yaml.
	// A change editing the target names only the target in its diff, never the
	// link, so matching the link's own name alone lets that change supply the
	// entire policy while the run reports nothing unusual.
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		add(resolved)
	}

	return out
}

// repoRelative converts a filesystem path to the repository-relative,
// slash-separated form diff paths are written in. ok is false when the path
// lies outside root.
//
// Directories are resolved through symlinks before comparing. A checkout
// reached through a symlinked parent, /tmp on macOS, /home on several CI
// images, spells the same file two different ways, and filepath.Rel then
// reports an in-repo config as being OUTSIDE the repository, which is the
// answer that trusts it. The final component is deliberately left alone: the
// name in the diff is the name of the file the change edits.
func repoRelative(root, source string) (string, bool) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return "", false
	}
	sourceAbs = filepath.Join(evalSymlinks(filepath.Dir(sourceAbs)), filepath.Base(sourceAbs))

	rel, err := filepath.Rel(evalSymlinks(rootAbs), sourceAbs)
	if err != nil {
		return "", false
	}

	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// evalSymlinks resolves p, falling back to p itself: a path that cannot be
// resolved is still worth comparing literally.
func evalSymlinks(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}
