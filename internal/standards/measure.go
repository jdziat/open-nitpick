package standards

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
)

// The floor a count clears before it is written down as a standard.
//
// Two numbers rather than one. A share alone lies at small counts: three sites
// out of three is 100% and says nothing about what a repository decided.
// MinSites is where a share stops being an accident of the sample. Both are
// reachable from configuration, so a repository midway through adopting a
// convention can watch the number climb before any bar is asserted.
const (
	DefaultMinShare = 0.85
	DefaultMinSites = 12
)

// Floor is the evidence a probe needs before its rule is written down.
type Floor struct {
	MinShare float64
	MinSites int
}

// DefaultFloor is the floor applied when configuration names none.
func DefaultFloor() Floor { return Floor{MinShare: DefaultMinShare, MinSites: DefaultMinSites} }

// Standing is what the evidence supports saying about a probe.
type Standing string

const (
	// StandingStandard means the repository does this, by count.
	StandingStandard Standing = "standard"

	// StandingContested means the probe found sites and the repository is
	// divided about them. Reported, never written down as a rule: a
	// convention half the tree ignores is a proposal.
	StandingContested Standing = "contested"

	// StandingUnseen means the probe found nothing to have an opinion about,
	// which is not the same as a tree that conforms. See docs/measurement.md
	// Rule 10.
	StandingUnseen Standing = "unseen"
)

// Result is one probe's reading over a set of files.
type Result struct {
	ID         string   `json:"id"`
	Rule       string   `json:"rule"`
	Why        string   `json:"why"`
	Language   string   `json:"language"`
	Conforming int      `json:"conforming"`
	Total      int      `json:"total"`
	Standing   Standing `json:"standing"`

	// Off are the sites that do not conform, so a report can name them. Capped
	// by the caller rather than here: the count is the claim and the list is
	// the evidence for it, and truncating evidence silently is the failure
	// this package exists to avoid.
	Off []Site `json:"off,omitempty"`
}

// Share is the conforming fraction, and false when there is nothing to divide.
//
// The bool rather than a zero, for the reason CostRow.Recall returns one: zero
// is a number a reader will act on, and "no sites" and "no site conformed" are
// opposite readings of the same digit.
func (r Result) Share() (float64, bool) {
	if r.Total == 0 {
		return 0, false
	}
	return float64(r.Conforming) / float64(r.Total), true
}

// Report is a measurement over a tree, plus what it could not speak for.
type Report struct {
	Results []Result `json:"results"`

	// Files counted per language, including languages no probe reads. It is
	// what lets a reader see that 900 TypeScript files went unmeasured rather
	// than inferring silence.
	Files map[string]int `json:"files"`

	// Unprobed are the languages present in the tree that no probe reads,
	// sorted. Stated rather than left to be noticed.
	Unprobed []string `json:"unprobed,omitempty"`

	Floor Floor `json:"-"`
}

// Standards returns the results the floor supports writing down, best evidence
// first.
func (r Report) Standards() []Result {
	var out []Result
	for _, res := range r.Results {
		if res.Standing == StandingStandard {
			out = append(out, res)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, _ := out[i].Share()
		sj, _ := out[j].Share()
		if si != sj {
			return si > sj
		}
		return out[i].Total > out[j].Total
	})
	return out
}

// Options select which probes run and the evidence they need.
type Options struct {
	Floor Floor

	// Disabled are probe IDs to skip entirely. A disabled probe is absent from
	// the report rather than present at zero, so switching one off cannot look
	// like a tree that failed it.
	Disabled []string
}

// enabled reports whether a probe runs under these options.
func (o Options) enabled(p Probe) bool {
	for _, id := range o.Disabled {
		if id == p.ID {
			return false
		}
	}
	return true
}

// floor returns the configured floor, or the default when none was set.
func (o Options) floor() Floor {
	f := o.Floor
	if f.MinShare <= 0 {
		f.MinShare = DefaultMinShare
	}
	if f.MinSites <= 0 {
		f.MinSites = DefaultMinSites
	}
	return f
}

// File is one file to measure: its repository-relative path and its bytes.
type File struct {
	Path string
	Src  []byte
}

// Measure runs every enabled probe over the files and returns the reading.
func Measure(files []File, opts Options) Report {
	floor := opts.floor()
	rep := Report{Files: map[string]int{}, Floor: floor}

	counts := make(map[string]*Result, len(Probes))
	order := make([]string, 0, len(Probes))
	for _, p := range Probes {
		if !opts.enabled(p) {
			continue
		}
		counts[p.ID] = &Result{ID: p.ID, Rule: p.Rule, Why: p.Why, Language: p.Language}
		order = append(order, p.ID)
	}

	probed := map[string]bool{}
	for _, l := range Languages() {
		probed[l] = true
	}

	for _, f := range files {
		lang := bundle.Language(f.Path)
		rep.Files[lang]++

		var s *source
		for _, p := range Probes {
			if p.Language != lang || !opts.enabled(p) {
				continue
			}
			if s == nil {
				s = newSource(f.Path, f.Src)
			}
			acc := counts[p.ID]
			for _, site := range p.sites(s) {
				acc.Total++
				if site.Conforms {
					acc.Conforming++
				} else {
					acc.Off = append(acc.Off, site)
				}
			}
		}
	}

	for lang := range rep.Files {
		if lang != "" && !probed[lang] {
			rep.Unprobed = append(rep.Unprobed, lang)
		}
	}
	sort.Strings(rep.Unprobed)

	for _, id := range order {
		res := *counts[id]
		res.Standing = floor.standing(res)
		rep.Results = append(rep.Results, res)
	}
	return rep
}

// standing reads a result against this floor.
func (f Floor) standing(r Result) Standing {
	share, ok := r.Share()
	switch {
	case !ok:
		return StandingUnseen
	case r.Total >= f.MinSites && share >= f.MinShare:
		return StandingStandard
	default:
		return StandingContested
	}
}

// Adherence is how a change sits against standards measured elsewhere.
type Adherence struct {
	Conforming int    `json:"conforming"`
	Total      int    `json:"total"`
	Off        []Site `json:"off,omitempty"`
}

// Share is the conforming fraction of the change's own sites.
func (a Adherence) Share() (float64, bool) {
	if a.Total == 0 {
		return 0, false
	}
	return float64(a.Conforming) / float64(a.Total), true
}

// Score reads a change against standards already measured on the base
// revision.
//
// Only the sites on lines the change touched are counted, which is the
// difference between asking an author about their own work and billing them
// for a convention that arrived after the file did. touched maps a path to the
// new-file lines the change added; a path absent from it contributes nothing.
//
// Only probes standing as a standard are scored. A contested probe is a
// proposal, and failing an author against a proposal is how a gate earns the
// reputation that gets it switched off.
func Score(files []File, base Report, touched map[string]map[int]bool, opts Options) map[string]Adherence {
	standing := map[string]bool{}
	for _, r := range base.Standards() {
		standing[r.ID] = true
	}

	out := map[string]Adherence{}
	for _, f := range files {
		lines := touched[f.Path]
		if len(lines) == 0 {
			continue
		}
		lang := bundle.Language(f.Path)

		var s *source
		for _, p := range Probes {
			if p.Language != lang || !standing[p.ID] || !opts.enabled(p) {
				continue
			}
			if s == nil {
				s = newSource(f.Path, f.Src)
			}
			a := out[p.ID]
			for _, site := range p.sites(s) {
				if !lines[site.Line] {
					continue
				}
				a.Total++
				if site.Conforms {
					a.Conforming++
				} else {
					a.Off = append(a.Off, site)
				}
			}
			if a.Total > 0 {
				out[p.ID] = a
			}
		}
	}
	return out
}

// ReadTree reads every file under root that some probe could read.
//
// It skips what a review skips and what no probe can speak for, so the
// denominator is files a probe was offered rather than files on disk.
func ReadTree(root string, skipDir func(name string) bool) ([]File, error) {
	probed := map[string]bool{}
	for _, l := range Languages() {
		probed[l] = true
	}

	var out []File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" ||
				(skipDir != nil && skipDir(name)) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !probed[bundle.Language(rel)] {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, File{Path: rel, Src: src})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// TouchedLines turns a set of changed-line lists into the lookup Score wants.
func TouchedLines(changed map[string][]int) map[string]map[int]bool {
	out := make(map[string]map[int]bool, len(changed))
	for path, lines := range changed {
		set := make(map[int]bool, len(lines))
		for _, l := range lines {
			set[l] = true
		}
		out[strings.TrimPrefix(path, "./")] = set
	}
	return out
}
