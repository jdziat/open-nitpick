// Package learnings loads reviewed repository references from a pinned base.
package learnings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/knowledge"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// ManifestPath is read only at the trusted policy revision.
const ManifestPath = ".nitpick/learnings/manifest.json"

const maxManifestBytes = 1 << 20
const maxEntries = 128

// Manifest holds operator-reviewed entries. Drafts are never loaded here.
type Manifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Entry is a cited reference, scoped to one repository and approved content.
type Entry struct {
	ID             string         `json:"id"`
	Repository     string         `json:"repository"`
	Title          string         `json:"title"`
	Body           string         `json:"body"`
	Paths          []string       `json:"paths"`
	Languages      []string       `json:"languages"`
	Classes        []config.Class `json:"classes"`
	SourceURL      string         `json:"source_url"`
	SourceRevision string         `json:"source_revision"`
	EvidenceDate   string         `json:"evidence_date"`
	Rationale      string         `json:"rationale"`
	Classification string         `json:"classification"`
	ApprovedBy     string         `json:"approved_by"`
	ContentHash    string         `json:"content_hash"`
	// AcceptanceRevision comes from the revision read by Load, never from a
	// declaration in the file that is trying to establish its own authority.
	AcceptanceRevision string `json:"-"`
}

// Hash binds every declared field, including scope, provenance, and approver.
func (e Entry) Hash() string {
	e.ContentHash = ""
	raw, _ := json.Marshal(e)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Overlay is an immutable set pinned for one review. A later review reloads
// the base, so deletion and revocation require no separate cache invalidation.
type Overlay struct {
	Revision string
	Digest   string
	entries  []Entry
}

// Load reads accepted references only from base. Missing base or manifest
// disables the overlay with a reason; unreadable or malformed policy is fatal.
func Load(ctx context.Context, repository, base string, read func(context.Context, string, string) ([]byte, error)) (*Overlay, string, error) {
	if strings.TrimSpace(base) == "" {
		return nil, "trusted base unavailable", nil
	}
	if strings.TrimSpace(repository) == "" {
		return nil, "repository identity unavailable", nil
	}
	raw, err := read(ctx, base, ManifestPath)
	if errors.Is(err, vcs.ErrNotFound) {
		return nil, "no accepted overlay", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read accepted learnings: %w", err)
	}
	if len(raw) > maxManifestBytes {
		return nil, "", fmt.Errorf("learnings manifest exceeds %d bytes", maxManifestBytes)
	}
	var manifest Manifest
	if err := Decode(raw, &manifest); err != nil {
		return nil, "", err
	}
	if manifest.Version != 1 || len(manifest.Entries) > maxEntries {
		return nil, "", errors.New("learnings manifest version or entry count is unsupported")
	}
	seen := map[string]bool{}
	for i := range manifest.Entries {
		e := &manifest.Entries[i]
		if err := validateEntry(*e, repository); err != nil {
			return nil, "", fmt.Errorf("learnings entry %d: %w", i+1, err)
		}
		if seen[e.ID] {
			return nil, "", errors.New("learnings manifest contains duplicate entry IDs")
		}
		seen[e.ID] = true
		e.AcceptanceRevision = base
	}
	slices.SortFunc(manifest.Entries, func(a, b Entry) int { return strings.Compare(a.ID, b.ID) })
	digest := sha256.Sum256(raw)
	overlay := &Overlay{Revision: base, Digest: hex.EncodeToString(digest[:]), entries: manifest.Entries}
	if len(manifest.Entries) == 0 {
		return overlay, "empty overlay", nil
	}
	return overlay, "loaded", nil
}

func validateEntry(e Entry, repository string) error {
	if e.Repository != repository {
		return errors.New("repository does not match this review")
	}
	for _, field := range []string{e.ID, e.Title, e.Body, e.Rationale, e.ApprovedBy, e.SourceRevision} {
		if strings.TrimSpace(field) == "" || len(field) > 8192 || strings.ContainsRune(field, '\x00') {
			return errors.New("required text is missing, oversized, or contains a NUL")
		}
	}
	if len(e.ID) > 128 || strings.ContainsAny(e.ID, "/\\\r\n") || len(e.Title) > 256 || len(e.ApprovedBy) > 256 {
		return errors.New("invalid identifier, title, or approver")
	}
	if e.ContentHash != e.Hash() {
		return errors.New("approved content hash does not match")
	}
	if !slices.Contains([]string{"convention", "false_positive", "accepted_risk", "fixed", "no_longer_relevant"}, e.Classification) {
		return errors.New("explicit feedback classification is required")
	}
	if _, err := time.Parse("2006-01-02", e.EvidenceDate); err != nil {
		return errors.New("evidence date must be YYYY-MM-DD")
	}
	u, err := url.Parse(e.SourceURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return errors.New("source must be an HTTPS citation without credentials or a query")
	}
	if len(e.Paths) == 0 || len(e.Paths) > 32 || len(e.Languages) == 0 || len(e.Languages) > 16 || len(e.Classes) == 0 {
		return errors.New("bounded path, language, and class scope is required")
	}
	for _, glob := range e.Paths {
		if glob == "" || len(glob) > 256 || strings.HasPrefix(glob, "/") || strings.ContainsAny(glob, "\\\x00") || path.Clean(glob) != glob || strings.HasPrefix(glob, "../") || glob == ".." || !doublestar.ValidatePattern(glob) {
			return errors.New("invalid repository-relative path scope")
		}
	}
	for _, language := range e.Languages {
		if !slices.Contains([]string{"any", "go", "python", "typescript", "javascript", "rust", "shell"}, language) {
			return errors.New("unknown language scope")
		}
	}
	for _, class := range e.Classes {
		if normalized, ok := class.Normalize(); !ok || normalized != class {
			return errors.New("unknown or noncanonical class scope")
		}
	}
	return nil
}

// ForPaths returns applicable entries without embeddings or cross-repository
// lookup. Resolved or fixed feedback is evidence history, not active policy.
func (o *Overlay) ForPaths(paths []string, classes map[config.Class]bool) []Entry {
	if o == nil {
		return nil
	}
	var out []Entry
	for _, e := range o.entries {
		if e.Classification == "fixed" || e.Classification == "no_longer_relevant" {
			continue
		}
		if !slices.ContainsFunc(e.Classes, func(class config.Class) bool { return classes[class] }) {
			continue
		}
		for _, p := range paths {
			langs := knowledge.LanguagesOf([]string{p})
			if !slices.ContainsFunc(e.Languages, func(language string) bool { return language == "any" || langs[language] }) {
				continue
			}
			if slices.ContainsFunc(e.Paths, func(glob string) bool { match, _ := doublestar.Match(glob, p); return match }) {
				out = append(out, e)
				break
			}
		}
	}
	return out
}
