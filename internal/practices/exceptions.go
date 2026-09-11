package practices

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

// ApplyExceptions fingerprints evidence before applying accepted, bounded policy.
// Missing source is never eligible for a file exception.
func ApplyExceptions(report *Report, policy []config.PracticeException, files []standards.File, now time.Time) {
	sources := map[string][]byte{}
	for _, file := range files {
		sources[file.Path] = file.Src
	}
	for i := range report.Checks {
		check := &report.Checks[i]
		for j := range check.Findings {
			finding := &check.Findings[j]
			finding.Exception, finding.Fingerprint = "", ""
			evidence := finding.Rationale
			if finding.Target.Kind == FileTarget || finding.Target.Kind == SiteTarget {
				source, ok := sources[finding.Target.ID]
				if !ok {
					continue
				}
				// Include the full file so changes to an enclosing contract or
				// control flow invalidate an exception on an unchanged line.
				evidence = string(source)
				complete := true
				for _, other := range finding.AlsoAt {
					data, ok := sources[other.ID]
					if !ok {
						complete = false
						break
					}
					evidence += "\x00" + other.ID + "\x00" + string(data)
				}
				if !complete {
					continue
				}
			}
			payload, _ := json.Marshal([]string{check.ID, check.Version, finding.Rule, string(finding.Target.Kind), finding.Target.ID, strconv.Itoa(finding.Target.Line), finding.Title, evidence})
			digest := sha256.Sum256(payload)
			finding.Fingerprint = hex.EncodeToString(digest[:])
			for _, exception := range policy {
				if exception.Rule != finding.Rule || exception.Target != finding.Target.ID || !strings.EqualFold(exception.Fingerprint, finding.Fingerprint) {
					continue
				}
				if exception.Expires != "" {
					expiry, err := time.Parse(time.DateOnly, exception.Expires)
					if err != nil || !now.Before(expiry) {
						continue
					}
				}
				if strings.TrimSpace(exception.Reason) != "" {
					finding.Exception = exception.Reason
				}
			}
		}
	}
}
