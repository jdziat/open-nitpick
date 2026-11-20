package evals

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/jdziat/open-nitpick/internal/review"
)

// QualityCell reports scheduled review quality without hiding missing attempts.
type QualityCell struct {
	Type   string `json:"type,omitempty"`
	Corpus string `json:"corpus,omitempty"`
	// ProductionLocated is Located re-scored at the published severity floor,
	// from the same stored findings. It is a measurement, not a policy change.
	ProductionLocated int               `json:"production_located"`
	Model             string            `json:"model"`
	Variant           string            `json:"variant"`
	Scheduled         int               `json:"scheduled"`
	Failed            int               `json:"failed"`
	Plants            int               `json:"plants"`
	Located           int               `json:"located"`
	Recall            float64           `json:"recall"`
	Noise             int               `json:"scored_noise"`
	Anchor            int               `json:"widest_anchor"`
	CostUSD           float64           `json:"cost_usd_lower_bound"`
	Priced            int               `json:"priced_samples"`
	MeanSeconds       float64           `json:"mean_seconds"`
	P95Seconds        float64           `json:"p95_seconds"`
	Timed             int               `json:"timed_samples"`
	CacheHits         int64             `json:"cache_hits"`
	CacheMisses       int64             `json:"cache_misses"`
	Completion        int               `json:"completion_tokens"`
	PerFixture        map[string]string `json:"per_fixture"`
}

type auditSample struct {
	model, variant, fixture string
	run                     int
}

// RebuildQuality refuses missing evidence while retaining it in the denominator.
func RebuildQuality(manifest ExperimentManifest, records []DumpRecord, fixtures []Fixture) ([]QualityCell, error) {
	if (manifest.Version != manifestVersionOriginal && manifest.Version != manifestVersionCurrent) ||
		manifest.Runs <= 0 || len(manifest.Models) == 0 || len(manifest.Variants) == 0 {
		return nil, errors.New("invalid experiment manifest")
	}
	known := map[string]Fixture{}
	for _, fixture := range fixtures {
		known[fixture.Name] = fixture
	}
	groups := map[auditSample][]DumpRecord{}
	for _, record := range records {
		if record.Model == IncumbentModel {
			continue
		}
		key := auditSample{record.Model, record.Variant, record.Fixture, record.Run}
		groups[key] = append(groups[key], record)
	}
	var cells []QualityCell
	var gaps []error
	for _, model := range manifest.Models {
		for variant := range manifest.Variants {
			var cell evaluationCell
			security := personaForType(manifest.Type)
			if manifest.Version == manifestVersionOriginal {
				// Version 1 predates the type; the dump recorded the persona.
				for _, record := range records {
					if record.SecurityPersona {
						security = true
						break
					}
				}
			}
			cell.persona = &security
			var durations []float64
			var cacheHits, cacheMisses int64
			for name, expectedHash := range manifest.FixtureHashes {
				fixture, ok := known[name]
				if !ok || fixtureFingerprint(fixture) != expectedHash {
					return nil, fmt.Errorf("fixture %s is absent or changed since registration", name)
				}
				for run := manifest.SampleOffset; run < manifest.SampleOffset+manifest.Runs; run++ {
					key := auditSample{model.ID, variant, name, run}
					sample := groups[key]
					delete(groups, key)
					var runErr error
					var findings []review.Finding
					var cost Cost
					var result RunResult
					if len(sample) == 0 {
						runErr = fmt.Errorf("missing sample %s/%s/%s/%d", model.ID, variant, name, run)
						gaps = append(gaps, runErr)
					} else {
						seen := map[int]DumpRecord{}
						for _, record := range sample {
							if record.FixtureHash != expectedHash {
								return nil, fmt.Errorf("sample fixture hash mismatch: %s", name)
							}
							if record.Index >= 0 {
								if previous, ok := seen[record.Index]; ok {
									if !sameFinding(previous, record) {
										return nil, fmt.Errorf("contradictory finding index in %s", name)
									}
								} else {
									seen[record.Index] = record
									findings = append(findings, findingFromRecord(record))
								}
							}
						}
						if sample[0].Findings > 0 && sample[0].Findings != len(seen) {
							return nil, fmt.Errorf("missing finding records in %s", name)
						}
						if len(seen) == 0 && !sample[0].Silent &&
							(sample[0].Measurement == nil || sample[0].Measurement.Failure == "") {
							return nil, fmt.Errorf("sample %s has no proof of successful silence or failure", name)
						}
						if m := sample[0].Measurement; m != nil {
							result.Duration, result.Usage, cost = m.Duration, m.Usage, m.Cost
							cacheHits += m.CacheHits
							cacheMisses += m.CacheMisses
							if m.Failure != "" {
								runErr = errors.New(m.Failure)
							}
						}
					}
					cell.record(fixture, findings, runErr, cost, result)
					if result.Duration > 0 {
						durations = append(durations, result.Duration.Seconds())
					}
				}
			}
			sort.Float64s(durations)
			var mean, p95 float64
			if len(durations) > 0 {
				mean = cell.seconds / float64(len(durations))
				p95 = durations[(95*len(durations)+99)/100-1]
			}
			recall := 0.0
			if cell.plants > 0 {
				recall = float64(cell.located) / float64(cell.plants)
			}
			cells = append(cells, QualityCell{Type: manifest.Type, Corpus: manifest.Corpus, ProductionLocated: cell.productionLocated, Model: model.ID, Variant: variant, Scheduled: cell.reviews, Failed: cell.lost,
				Plants: cell.plants, Located: cell.located, Recall: recall, Noise: cell.noise, Anchor: cell.anchor,
				CostUSD: cell.usd, Priced: cell.priced, MeanSeconds: mean, P95Seconds: p95,
				Timed: cell.timed, Completion: cell.complete, PerFixture: cell.perFix})
			cells[len(cells)-1].CacheHits = cacheHits
			cells[len(cells)-1].CacheMisses = cacheMisses
		}
	}
	if len(groups) > 0 {
		gaps = append(gaps, errors.New("retained samples outside the registered experiment"))
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i].Model+cells[i].Variant < cells[j].Model+cells[j].Variant })
	return cells, errors.Join(gaps...)
}

// FinalizeQualityDump reconstructs the receipt from persisted inputs and outputs.
func FinalizeQualityDump(dump *Dump, path string, fixtures []Fixture) error {
	if err := dump.Close(); err != nil {
		return err
	}
	manifest, err := ReadExperimentManifest(path + ".manifest.json")
	if err != nil {
		return err
	}
	records, err := ReadDump(path)
	if err != nil {
		return err
	}
	cells, auditErr := RebuildQuality(manifest, records, fixtures)
	data, err := json.MarshalIndent(cells, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".scorecard.json", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return auditErr
}
