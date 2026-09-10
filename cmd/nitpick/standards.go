package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
	"gopkg.in/yaml.v3"
)

// The measured half of the house style.
//
// `nitpick slop` scores prose against rules this tool ships. This scores code
// against rules the repository demonstrates. Nothing here calls a model. A
// convention is a count over named sites, and a count is either right or
// checkable. A model could phrase a rule that counting already established;
// that would be a later pass over this output, not a step in the measurement.

// StandardsResult is what nitpick standards returns.
type StandardsResult struct {
	Report standards.Report `json:"report"`

	// Adherence is keyed by probe ID and present only when a base revision was
	// given. Absent means no change was scored, which is not the same as a
	// change that conformed.
	Adherence map[string]standards.Adherence `json:"adherence,omitempty"`

	Recommendations []string `json:"recommendations,omitempty"`

	// Base is the revision the standards were measured against, empty when the
	// working tree was measured. It is in the output because a share is only
	// as meaningful as the tree it was counted over.
	Base string `json:"base,omitempty"`
}

func runStandards(ctx context.Context, args []string) error {
	var (
		repo       string
		base       string
		configPath string
		asJSON     bool
		agentsOut  string
	)
	fs := flag.NewFlagSet("standards", flag.ContinueOnError)
	fs.StringVar(&repo, "repo", ".", "repository root")
	fs.StringVar(&base, "base", "", "base revision: measure standards there and score the change against them")
	fs.StringVar(&configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.BoolVar(&asJSON, "json", false, "print the result as JSON")
	fs.StringVar(&agentsOut, "agents", "", "write the measured standards into this agent instructions file (e.g. AGENTS.md)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick standards [flags]\n\n"+
			"Measures the conventions this repository demonstrates, as counts over named sites,\n"+
			"and says which of them the evidence supports writing down. With -base, also scores\n"+
			"the change's own lines against the standards measured at that base revision.\n\n"+
			"No model is called and no credentials are read.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	// -agents writes the measurement to a file, and with -base the measurement
	// is of the base revision. Writing the base's standards into the working
	// tree's AGENTS.md would publish yesterday's conventions and pass the drift
	// gate while doing it. -json has nowhere to go once the output is a file.
	if agentsOut != "" {
		switch {
		case base != "":
			return errors.New("-agents writes what the working tree demonstrates, and -base measures the " +
				"base revision instead; run them separately")
		case asJSON:
			return errors.New("-agents writes a file and -json writes stdout; pick one")
		}
	}

	cfg, err := loadStandardsConfig(repo, configPath)
	if err != nil {
		return err
	}
	opts := standards.Options{
		Floor:    standards.Floor{MinShare: cfg.MinShare, MinSites: cfg.MinSites},
		Disabled: cfg.Disabled,
	}
	// A disabled ID that names no probe disables nothing, and the report it
	// produces looks exactly like the one the author wanted. internal/config
	// cannot check this for itself without importing the probes it would have
	// to know, so the check is here, where they are.
	if err := checkDisabled(opts.Disabled); err != nil {
		return err
	}

	result, err := measureStandards(ctx, repo, base, opts)
	if err != nil {
		return err
	}

	if agentsOut != "" {
		return writeAgents(agentsOut, result.Report)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Print(result.Report.Text())
	if result.Adherence != nil {
		fmt.Print("\n" + standards.AdherenceText(result.Adherence))
	}
	if len(result.Recommendations) > 0 {
		fmt.Println("\nRecommendations, most to fix first:")
		for i, r := range result.Recommendations {
			fmt.Printf("  %d. %s\n", i+1, r)
		}
	}
	return nil
}

// measureStandards reads the tree, measures it, and scores the change when a
// base revision was named.
func measureStandards(ctx context.Context, repo, base string, opts standards.Options) (*StandardsResult, error) {
	files, err := standards.ReadTree(repo, skipStandardsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", repo, err)
	}

	out := &StandardsResult{Report: standards.Measure(files, opts), Base: base}
	if base == "" {
		return out, nil
	}

	// The standards come from the base revision, so a change cannot supply the
	// convention it is measured against. This is the seam config.BasePolicy
	// exists for, applied to a measurement rather than to a policy: without
	// it, a branch that rewrites a package's style is scored as the repository
	// having always written it that way.
	local := vcs.NewLocal(repo, os.Stderr)
	raw, err := local.Diff(ctx, vcs.Ref{Base: base})
	if err != nil {
		return nil, fmt.Errorf("diff against %s: %w", base, err)
	}
	changed, err := diff.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse the diff against %s: %w", base, err)
	}

	baseFiles, err := readAtBase(ctx, local, base)
	if err != nil {
		return nil, err
	}
	out.Report = standards.Measure(baseFiles, opts)

	touched := map[string][]int{}
	for _, f := range changed {
		if lines := f.ChangedLines(); len(lines) > 0 {
			touched[f.Path] = lines
		}
	}
	out.Adherence = standards.Score(files, out.Report, standards.TouchedLines(touched), opts)
	out.Recommendations = standards.Recommendations(out.Adherence)
	return out, nil
}

// baseReader is the part of a provider readAtBase needs, as an interface so
// the failure path has a guard.
type baseReader interface {
	Tree(ctx context.Context, rev string) ([]string, error)
	FileContent(ctx context.Context, ref vcs.Ref, path string) ([]byte, error)
}

// readAtBase reads the base revision's own file list at the base revision.
//
// The list comes from `git ls-tree`, so the base is enumerated from the base.
// Walking the working tree made the base a function of the change; see
// docs/findings.md. Every path here is one the base holds, so a failed read is
// a failure rather than an absence and it stops the command.
func readAtBase(ctx context.Context, local baseReader, base string) ([]standards.File, error) {
	paths, err := local.Tree(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", base, err)
	}

	probed := map[string]bool{}
	for _, l := range standards.Languages() {
		probed[l] = true
	}

	ref := vcs.Ref{Base: base, Head: base}
	out := make([]standards.File, 0, len(paths))
	for _, p := range paths {
		if skipStandardsPath(p) {
			continue
		}
		lang := bundle.Language(p)
		if lang == "" {
			continue
		}
		if !probed[lang] {
			// In the census, and there is nothing at the base worth fetching.
			out = append(out, standards.File{Path: p})
			continue
		}
		src, err := local.FileContent(ctx, ref, p)
		if err != nil {
			return nil, fmt.Errorf("read %s at %s: %w", p, base, err)
		}
		out = append(out, standards.File{Path: p, Src: src})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no file any probe reads", base)
	}
	return out, nil
}

// skipStandardsPath reports whether a base path lies under a skipped directory.
//
// ReadTree prunes these while walking; a flat list from git has to check each
// path's segments for itself, and the two have to agree or the base and the
// working tree are measured over different sets.
func skipStandardsPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if skipStandardsDir(seg) {
			return true
		}
	}
	return false
}

// skipStandardsDir names directories a measurement should not walk.
//
// The generated site and the release output are copies of files already
// counted once, and counting them again weights whatever they happen to
// contain. testdata is worse than a copy: Go's own convention is that it holds
// code written to be wrong, so measuring it asks whether a repository's
// fixtures follow its conventions, which is a question nobody has.
func skipStandardsDir(name string) bool {
	switch name {
	case "website", "dist", "public", ".website", "testdata":
		return true
	}
	return false
}

// loadStandardsConfig reads the standards block, and only that block.
//
// The full loader validates models, providers and every other key, and this
// command reads none of them. Refusing to count a repository's conventions
// because its model configuration names a provider that is not a router would
// make "no model is called and no credentials are read" false in the way that
// matters, which is whether the command runs at all.
//
// The repository's own file, not the user-level one. A convention belongs to a
// codebase rather than to whoever is looking at it, and a floor set in a home
// directory would silently change what a shared report says.
func loadStandardsConfig(repo, configPath string) (config.Standards, error) {
	if configPath == "" {
		configPath = filepath.Join(repo, config.FileName)
	}
	raw, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return config.Standards{}, nil
	}
	if err != nil {
		return config.Standards{}, fmt.Errorf("read %s: %w", configPath, err)
	}

	var file struct {
		Standards config.Standards `yaml:"standards"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return config.Standards{}, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if errs := file.Standards.Validate(); errs != nil {
		return config.Standards{}, fmt.Errorf("invalid standards block in %s: %w", configPath, errs)
	}
	return file.Standards, nil
}

// checkDisabled refuses a disabled entry that names no probe.
func checkDisabled(ids []string) error {
	var unknown []string
	for _, id := range ids {
		if _, ok := standards.Find(id); !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	known := make([]string, 0, len(standards.Probes))
	for _, p := range standards.Probes {
		known = append(known, p.ID)
	}
	return fmt.Errorf("standards.disabled names no probe: %s; the probes are %s",
		strings.Join(unknown, ", "), strings.Join(known, ", "))
}

// writeAgents regenerates the managed block of an agent instructions file.
//
// The file is read first and rewritten whole, so everything outside the
// markers survives byte for byte. A missing file is written from scratch; a
// file with a broken marker pair is refused rather than guessed at, since
// guessing where a block ended would overwrite prose nobody can recover.
func writeAgents(path string, rep standards.Report) error {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	out, err := standards.Render(string(existing), rep)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if string(existing) == out {
		return nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
