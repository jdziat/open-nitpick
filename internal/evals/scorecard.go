package evals

import (
	"fmt"
	"time"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/review"
)

// SampleMeasurement retains the outcome even when no findings could be produced.
type SampleMeasurement struct {
	SampleID    string        `json:"sample_id"`
	Failure     string        `json:"failure,omitempty"`
	Duration    time.Duration `json:"duration_ns"`
	Usage       TokenUsage    `json:"usage"`
	Cost        Cost          `json:"cost"`
	CacheHits   int64         `json:"cache_hits"`
	CacheMisses int64         `json:"cache_misses"`
}

// productionSeverity is the floor the published reviewer applies. The benchmark
// publishes at nit so precision can be measured; ProductionLocated re-scores the
// same findings at this floor without a second dispatch.
const productionSeverity = config.SeverityInfo

type evaluationCell struct {
	// persona fixes the scorer for the whole cell. Unset, the cell reads the
	// caller's environment, which is right for a live run and wrong for replay.
	persona                                         *bool
	productionLocated                               int
	reviews, lost, plants, located, findings, noise int
	anchor, near, priced, timed, reason, complete   int
	metered, failedAttempts, unreported             int
	usd, seconds                                    float64
	byBand                                          map[config.Severity][2]int
	perFix                                          map[string]string
}

func (r *evaluationCell) record(f Fixture, findings []review.Finding, runErr error, cost Cost, res RunResult) {
	if r.byBand == nil {
		r.byBand = map[config.Severity][2]int{}
		r.perFix = map[string]string{}
	}
	r.reviews++
	if res.Duration > 0 {
		r.timed++
		r.seconds += res.Duration.Seconds()
	}
	r.failedAttempts += res.Usage.Failed
	r.unreported += res.Usage.Unreported
	r.reason += res.Usage.Reasoning()
	r.complete += res.Usage.Completion()
	if res.Usage.Complete() {
		r.metered++
	}
	r.usd += cost.USD
	if cost.Known {
		r.priced++
	}
	if runErr != nil {
		r.lost++
		// Partial findings are retained in the artifact but cannot make an
		// incomplete review count as a successful detection.
		findings = nil
	}
	security := securityPersonaActive()
	if r.persona != nil {
		security = *r.persona
	}
	d := ScoreDetectionForPersona(f, findings, security)
	plants := PersonaDefectsFor(f, security)
	r.productionLocated += ScoreDetectionForPersona(f, atProductionVisibility(findings), security).Matched
	r.plants += len(plants)
	r.located += d.Matched
	r.findings += len(findings)
	r.noise += d.Noise()
	r.anchor = max(r.anchor, d.WidestAnchor)
	r.near += d.NearMisses
	for _, defect := range plants {
		band := r.byBand[defect.WantSeverity]
		band[1]++
		if d.Detected[defect.Why] {
			band[0]++
		}
		r.byBand[defect.WantSeverity] = band
	}
	cell := fmt.Sprintf("%d/%d", d.Matched, len(plants))
	if runErr != nil {
		cell += " lost"
	} else if n := d.Noise(); n > 0 {
		cell += fmt.Sprintf(" +%dn", n)
	}
	if previous := r.perFix[f.Name]; previous != "" {
		cell = previous + " " + cell
	}
	r.perFix[f.Name] = cell
}

// atProductionVisibility keeps the findings the published reviewer would show.
// An unrecognised severity is kept: dropping it would silently improve noise.
func atProductionVisibility(findings []review.Finding) []review.Finding {
	var kept []review.Finding
	for _, finding := range findings {
		severity, ok := config.Severity(finding.Severity).Normalize()
		if !ok || severity.AtLeast(productionSeverity) {
			kept = append(kept, finding)
		}
	}
	return kept
}

// personaForType reports whether a manifest type is scored under the security
// persona. slop is a review with review.slop on and keeps the default plants.
func personaForType(typ string) bool { return typ == TypeSecurity }
