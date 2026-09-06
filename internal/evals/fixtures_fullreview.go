package evals

// The full-review fixture: a small repository reviewed whole by
// `nitpick full-review`, with a planted bug, a planted secret, a dependency
// with a known advisory, a file of slop for the class section 2 of
// notes/plan-full-review.md defines, and a clean control. It is not a change:
// every file is what it is, and the acceptance is that the whole-tree review
// finds the bug and the secret, says nothing about the control, lists the
// advisory when the scanner is installed, and puts the secret first in the
// remediation plan.

// FullReviewFixture is the tree, path to content.
var FullReviewFixture = map[string]string{
	"go.mod": `module example.com/svc

go 1.22

require golang.org/x/text v0.3.0
`,
	"internal/fetch/fetch.go": `package fetch

import (
	"io"
	"net/http"
)

// Body returns the body of url.
func Body(url string) ([]byte, error) {
	resp, err := http.Get(url)
	defer resp.Body.Close()
	if err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}
`,
	"internal/cfg/cfg.go": `package cfg

// Credentials for the billing gateway.
const (
	awsAccessKeyID = "AKIAIOSFODNN7EXAMPLE"
	awsSecretKey   = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

// Gateway returns the credentials the billing client signs with.
func Gateway() (string, string) { return awsAccessKeyID, awsSecretKey }
`,
	"internal/clean/clean.go": `package clean

import "strings"

// Slug lowercases s and joins its words with hyphens.
func Slug(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), "-")
}
`,
	"internal/slop/slop.go": `package slop

import "fmt"

// Sure! Here's the helper function that processes the data.
// This function will process the data and return the result.
func ProcessData(data []int) []int {
	// Create a result slice to hold the result.
	result := []int{}
	// Loop over the data.
	for _, d := range data {
		// Check if d is not nil (it cannot be nil, it is an int).
		if d != 0 || d == 0 {
			// Append d to the result.
			result = append(result, d)
		}
	}
	// Return the result.
	return result
}

// Helper2 is a helper.
func Helper2(x int) int {
	defer func() {
		if r := recover(); r != nil {
			// Note that we ignore the error here.
			fmt.Println("recovered")
		}
	}()
	return x
}
`,
}

// FullReviewPlants are the defects the fixture plants, in the form the
// scorer credits: the file, the line, and words a finding that saw the
// defect uses.
var FullReviewPlants = []Defect{
	{
		Path: "internal/fetch/fetch.go", Line: 11, // defer resp.Body.Close() before the error check
		Keywords: []string{"nil pointer", "nil resp", "resp is nil", "before the error", "before checking", "nil dereference", "resp.Body.Close on a nil"},
		Class:    "correctness", WantSeverity: "error",
		Why: "resp is nil when http.Get fails, and the deferred Close dereferences it",
	},
	{
		Path: "internal/cfg/cfg.go", Line: 5, // awsAccessKeyID
		// The AWS documentation's own example pair, which every scanner
		// recognises and no account has ever held. Keywords name what a
		// finding that read the file says, not the word "secret".
		Keywords: []string{"hard-coded", "hardcoded", "committed to", "in source control", "in the source", "checked in", "AKIA", "awsSecretKey", "awsAccessKeyID", "Gateway()"},
		Class:    "security", WantSeverity: "critical",
		Why: "credentials are committed in source",
	},
}

// FullReviewClean is the file the review must say nothing about.
const FullReviewClean = "internal/clean/clean.go"
