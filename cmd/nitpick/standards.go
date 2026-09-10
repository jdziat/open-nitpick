package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// The measured half of the house style.
//
// `nitpick slop` scores prose against rules this tool ships. This scores code
// against rules the repository itself demonstrates, which is why nothing here
// calls a model: a convention is a count over named sites, and a count is
// either right or checkable. What a model could add is a nicer sentence for a
// rule that counting already established, and that is a later pass over the
// output rather than a step in the measurement.

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
	)
	fs := flag.NewFlagSet("standards", flag.ContinueOnError)
	fs.StringVar(&repo, "repo", ".", "repository root")
	fs.StringVar(&base, "base", "", "base revision: measure standards there and score the change against them")
	fs.StringVar(&configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.BoolVar(&asJSON, "json", false, "print the result as JSON")
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

	cfg, err := loadStandardsConfig(repo, configPath)
	if err != nil {
		return err
	}
	opts := standards.Options{
		Floor:    standards.Floor{MinShare: cfg.Standards.MinShare, MinSites: cfg.Standards.MinSites},
		Disabled: cfg.Standards.Disabled,
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

	baseFiles, err := readAtBase(ctx, local, base, files)
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

// readAtBase reads each probed file as it stood at the base revision.
//
// A file the base does not have is skipped rather than read from the working
// tree: a file this change created has no base version, and counting its
// contents into the base's own share lets the change vote on the standard it
// is about to be measured against.
func readAtBase(ctx context.Context, local *vcs.Local, base string, files []standards.File) ([]standards.File, error) {
	ref := vcs.Ref{Base: base, Head: base}
	out := make([]standards.File, 0, len(files))
	for _, f := range files {
		src, err := local.FileContent(ctx, ref, f.Path)
		if err != nil {
			continue
		}
		out = append(out, standards.File{Path: f.Path, Src: src})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no file readable at %s: the base revision has nothing to measure", base)
	}
	return out, nil
}

// skipStandardsDir names directories a measurement should not walk.
//
// The generated site and the release output are copies of files already
// counted once, and counting them again weights whatever they happen to
// contain.
func skipStandardsDir(name string) bool {
	return name == "website" || name == "dist" || name == "public" || name == ".website"
}

// loadStandardsConfig reads the repository's configuration for the standards
// block, tolerating its absence.
func loadStandardsConfig(repo, configPath string) (*config.Config, error) {
	if configPath == "" {
		configPath = filepath.Join(repo, ".nitpick.yaml")
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return nil, err
	}
	return cfg, nil
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
