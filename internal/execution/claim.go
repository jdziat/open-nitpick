package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"
)

// FindingDigest binds human claim mapping to one complete finding at one head.
func FindingDigest(head, file string, line int, title, body string) string {
	raw, _ := json.Marshal([]any{head, file, line, title, body})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// RefutesBuildFailure covers only the claim that this selected build fails.
// A successful build cannot refute a behavioral, test-only or tagged-build claim.
func RefutesBuildFailure(kind, file string, result Result) bool {
	return kind == "go_build_failure" && result.Check.Kind == "go_build" && result.Status == Passed && strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") && path.Dir(file) == result.Check.Package
}
