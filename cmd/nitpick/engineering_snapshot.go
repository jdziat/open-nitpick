package main

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func readEngineeringTree(ctx context.Context, root string, policy config.PracticePolicy) ([]standards.File, []practices.Omission, []practices.Omission, error) {
	tree := vcs.NewTree(vcs.NewLocal(root, nil), nil)
	tree.MaxBytes, tree.Capture = policy.Review.MaxFileBytes, true
	if _, err := tree.Diff(ctx, vcs.Ref{}); err != nil {
		return nil, nil, nil, err
	}
	cfg := &config.Config{Review: policy.Review, Practices: policy.Practices}
	cfg.Review.Ignore = append(slices.Clone(cfg.Review.Ignore), policy.Practices.Ignore...)
	var files []standards.File
	var excluded, omitted []practices.Omission
	for name, data := range tree.Snapshot {
		if cfg.Ignored(name) {
			excluded = append(excluded, practices.Omission{Target: practices.Target{Kind: practices.FileTarget, ID: name}, Reason: "accepted policy exclusion"})
			continue
		}
		files = append(files, standards.File{Path: name, Src: data})
	}
	for _, skip := range tree.Skipped {
		entry := practices.Omission{Target: practices.Target{Kind: practices.FileTarget, ID: skip.Path}, Reason: skip.Reason}
		if cfg.Ignored(skip.Path) || skip.Reason == "binary" || skip.Reason == "empty" {
			excluded = append(excluded, entry)
		} else {
			omitted = append(omitted, entry)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].Target.ID < excluded[j].Target.ID })
	return files, excluded, omitted, nil
}

func snapshotCheck(files []standards.File, omissions []practices.Omission, reason string) practices.Check {
	c := practices.Check{ID: "snapshot", Version: "1", Instrument: practices.Deterministic, Required: true, State: practices.Completed}
	for _, file := range files {
		target := practices.Target{Kind: practices.FileTarget, ID: file.Path}
		c.Planned = append(c.Planned, target)
		c.Examined = append(c.Examined, target)
	}
	for _, omission := range omissions {
		c.Planned = append(c.Planned, omission.Target)
		c.Omitted = append(c.Omitted, omission)
	}
	if len(omissions) > 0 || reason != "" {
		c.State, c.Reason = practices.Partial, strings.TrimSpace("source snapshot incomplete: "+reason)
	}
	if len(c.Planned) == 0 {
		c.State, c.Reason = practices.NotApplicable, "no applicable source files"
	}
	return c
}
