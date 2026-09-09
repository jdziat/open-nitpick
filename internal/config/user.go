package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The user-level configuration file, and why it is trusted where the
// repository's own file is not.
//
// Configuration was per repository: a .nitpick.yaml at the root, or -config.
// Someone running the CLI across many checkouts had to repeat themselves in
// every one of them, or pass -config on every invocation.
//
// The user-level file has the opposite property from the repository's. The
// person running the tool wrote it, it lives outside every checkout, and no
// pull request can reach it. So it is trusted for exactly the keys a
// repository's file is not, with no NITPICK_TRUST_CONFIG_ENDPOINTS needed:
// base_url, api_key_env, extra, allow_private_endpoint, and persona.custom.
//
// That trust is the whole risk of this feature, because merging two documents
// is where an untrusted value could be laundered into a trusted position. The
// order below is what prevents it, and it is the reason the repository's
// document is pruned as a DOCUMENT rather than scrubbed as a struct after the
// merge:
//
//  1. built-in defaults
//  2. the user-level file, whole
//  3. the repository's file, with the trusted keys deleted from it first
//  4. the environment, for what neither file said
//
// Scrubbing after the merge would clear the user's own base_url along with the
// repository's, since by then nothing records which document supplied it.
// Pruning first means the repository's value is gone before any merge can see
// it, and step 3 can never write into a key step 2 owns.

// UserFile is the user-level configuration file, under a nitpick directory in
// the user's configuration home.
const UserFile = "config.yaml"

// EnvUserConfig names a user-level config file explicitly, overriding the
// search below and applying wherever it is set, CI included.
//
// It is how an operator opts a runner in deliberately, and how this package's
// tests reach a file without a home directory. Like
// NITPICK_TRUST_CONFIG_ENDPOINTS it is an environment variable rather than a
// config key, because a file must not be able to nominate itself.
const EnvUserConfig = "NITPICK_USER_CONFIG"

// EnvNoUserConfig switches the user-level file off for one run.
const EnvNoUserConfig = "NITPICK_NO_USER_CONFIG"

// UserConfigPath resolves the user-level configuration file for this run.
//
// found is false when no user-level file applies at all, which is a different
// answer from "the file is missing": in CI nothing is looked for, and the path
// returned is empty rather than a path on a runner nobody configured.
//
// CI is excluded on purpose. A user-level file is a statement by the person at
// the keyboard, and on a runner there is no such person: $HOME belongs to the
// job, not to anyone, and a review whose endpoint depended on whether a file
// happened to exist in it would be a review nobody could reproduce. An
// operator who does want one on a runner names it with NITPICK_USER_CONFIG,
// which says so out loud.
func UserConfigPath(getenv func(string) string) (path string, found bool) {
	if getenv == nil {
		getenv = os.Getenv
	}

	if off, err := strconv.ParseBool(strings.TrimSpace(getenv(EnvNoUserConfig))); err == nil && off {
		return "", false
	}

	if explicit := strings.TrimSpace(getenv(EnvUserConfig)); explicit != "" {
		return explicit, true
	}

	if inCI(getenv) {
		return "", false
	}

	if xdg := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "nitpick", UserFile), true
	}

	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		// Windows, and any environment that does not export HOME.
		var err error
		if home, err = os.UserHomeDir(); err != nil || strings.TrimSpace(home) == "" {
			return "", false
		}
	}
	return filepath.Join(home, ".config", "nitpick", UserFile), true
}

// inCI reports whether this looks like an automated runner.
//
// The two variables are the ones every forge sets and nothing else does. A
// false negative here means a user-level file is read on a runner that has
// one, which is the same outcome as NITPICK_USER_CONFIG; a false positive
// means it is not read locally, which the run reports rather than hides.
func inCI(getenv func(string) string) bool {
	for _, name := range []string{"CI", "GITHUB_ACTIONS"} {
		if v, err := strconv.ParseBool(strings.TrimSpace(getenv(name))); err == nil && v {
			return true
		}
	}
	return false
}

// userDocument reads the user-level configuration document.
//
// A missing file is not an error and not a warning: not having one is the
// ordinary case. A file that exists and cannot be read IS an error, because
// the alternative is reviewing under settings the operator believes are in
// force and are not.
func userDocument(getenv func(string) string) (path string, data []byte, err error) {
	path, found := UserConfigPath(getenv)
	if !found {
		return "", nil, nil
	}

	switch data, err = os.ReadFile(path); {
	case errors.Is(err, os.ErrNotExist):
		return "", nil, nil
	case err != nil:
		return "", nil, fmt.Errorf("read user config %s: %w", path, err)
	}
	return path, data, nil
}

// overlay merges the user-level document, then the repository's, onto the
// receiver, and reports what each supplied.
//
// repo is pruned before it is merged, so a repository document cannot reach
// any key the user-level file is trusted for. Ordering here is the invariant;
// see the note at the top of this file.
func (c *Config) overlay(user, repo []byte, trusted bool) (dropped, userKeys, overridden, unknown []string, err error) {
	tolerate := ignoreUnknownKeys(nil)

	userNode, err := documentNode(user)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("parse user config: %w", err)
	}
	repoNode, err := documentNode(repo)
	if err != nil {
		// Refused rather than merged, whether or not the file is trusted.
		// Untrusted, merging would hand the decoder a document with nothing
		// pruned from it, so one this parser rejects and the decoder accepts
		// would carry every key the prune exists to remove. Trusted, the
		// decoder is about to reject the same bytes anyway, and reporting it
		// here says which parse failed.
		return nil, nil, nil, nil, fmt.Errorf("parse config: %w", err)
	}

	if len(user) > 0 {
		ignored, err := c.merge(user, tolerate)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		unknown = append(unknown, ignored...)
		userKeys = keyPaths(userNode)
	}

	if !trusted && repoNode != nil {
		dropped = pruneUntrusted(repoNode)
		if repo, err = yaml.Marshal(repoNode); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("rewrite config without the keys it may not supply: %w", err)
		}
	}

	ignored, err := c.merge(repo, tolerate)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	unknown = append(unknown, ignored...)

	if len(userKeys) > 0 && repoNode != nil {
		overridden = intersect(userKeys, keyPaths(repoNode))
	}
	return dropped, userKeys, overridden, unknown, nil
}

// documentNode parses a document down to its root mapping, or nil when the
// document is empty or is not a mapping. A document that is neither is left
// for merge to reject in its own words.
func documentNode(data []byte) (*yaml.Node, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil
	}
	if root := resolveNode(doc.Content[0]); root != nil && root.Kind == yaml.MappingNode {
		return root, nil
	}
	return nil, nil
}

// keyPaths lists the dotted paths a document sets, deepest key only, so
// explain-config can say which settings a file supplied.
//
// A sequence is a leaf. Its entries are not addressable settings on their own:
// merge replaces a sequence wholesale rather than combining it element by
// element, so "review.ignore" is the thing the file supplied.
func keyPaths(root *yaml.Node) []string {
	var out []string

	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		n = resolveNode(n)
		if n == nil || n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i].Value, resolveNode(n.Content[i+1])
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			if value != nil && value.Kind == yaml.MappingNode && len(value.Content) > 0 {
				walk(value, path)
				continue
			}
			out = append(out, path)
		}
	}

	walk(root, "")
	return out
}

// intersect returns the members of a that also appear in b, in a's order.
func intersect(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if in[s] {
			out = append(out, s)
		}
	}
	return out
}

// resolveNode follows an alias to the node it names.
//
// An alias is followed rather than skipped because skipping is how a value
// smuggles itself past a check: a model spec written as *anchor is the same
// mapping as the one the anchor named, and pruning has to reach it. Following
// it also means pruning the anchor prunes every use of it, which is the
// direction an error here should fail in.
func resolveNode(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < 100; i++ {
		n = n.Alias
	}
	return n
}
