package evals

import "github.com/jdziat/open-nitpick/internal/config"

// dedupFixtures is the corpus's cross-batch DEDUP fixture, and it is here for
// a
// property no other fixture has: the same defect is reportable from more than
// one batch, so the merge that runs before triage has something to merge.
//
// The note behind it is in docs/harness-notes.md#dedupfixtures.
func dedupFixtures() []Fixture {
	return []Fixture{crossBatchReplayFixture()}
}

// crossBatchReplayFixture widens a retry helper's "safe to repeat" predicate
// and, in the same change, sends a payment capture through it.
//
// The note behind it is in docs/harness-notes.md#crossbatchreplayfixture.
func crossBatchReplayFixture() Fixture {
	return Fixture{
		Name: "cross-batch-replay",

		// go.mod is Extra, so it is written into the repository and never
		// reaches a prompt: Assemble only fetches content for files the diff
		// names, and a file byte-identical in both states is in no diff. It is
		// here because it is what makes the tree a Go module that builds, which
		// is what let this fixture be executed before it was believed.
		Extra: map[string]string{
			"go.mod": "module example.com/pay\n\ngo 1.25\n",
		},

		Base: map[string]string{
			"billing/customer.go": `package billing

// Customer is who an order belongs to.
type Customer struct {
	ID    string
	Email string
}

// Label renders a customer for a receipt.
func (c Customer) Label() string {
	if c.Email == "" {
		return c.ID
	}
	return c.ID + " <" + c.Email + ">"
}
`,
			"billing/ledger.go": `package billing

// Entry is one line of the ledger.
type Entry struct {
	OrderID string
	Cents   int
}

// Ledger accumulates entries in the order they were recorded.
type Ledger struct {
	entries []Entry
}

// Record appends an entry.
func (l *Ledger) Record(e Entry) {
	l.entries = append(l.entries, e)
}

// Entries returns a copy of what has been recorded.
func (l *Ledger) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}
`,
			"internal/clock/clock.go": `// Package clock is the time source the billing code reads.
package clock

import "time"

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

// System reads the machine clock.
type System struct{}

// Now returns the current time.
func (System) Now() time.Time { return time.Now() }
`,
			"internal/logging/logging.go": `// Package logging names the levels the service logs at.
package logging

// Level is how important a log record is.
type Level int

// The levels, least important first.
const (
	Debug Level = iota
	Info
	Warn
	Error
)

// String renders a level for a log line.
func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "unknown"
}
`,
			"internal/orderid/orderid.go": `// Package orderid formats and checks order identifiers.
package orderid

import "strings"

// Prefix is what every order identifier starts with.
const Prefix = "ord_"

// Valid reports whether s is an order identifier.
func Valid(s string) bool {
	return strings.HasPrefix(s, Prefix) && len(s) > len(Prefix)
}
`,
			"platform/retry/retry.go": `// Package retry re-runs an operation that failed against the payment gateway.
package retry

import "net/http"

// Attempts is how many times Do runs an operation before giving up.
const Attempts = 3

// Replayable reports whether an operation using this HTTP method can be run a
// second time without repeating work the gateway has already done.
//
// Only the read methods qualify: a GET that fails halfway has changed nothing
// on the far side, so asking again costs nothing but time.
func Replayable(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// Do runs op, re-running it while it fails and the method is one Replayable
// allows to be repeated.
func Do(method string, op func() error) error {
	var err error
	for range Attempts {
		if err = op(); err == nil {
			return nil
		}
		if !Replayable(method) {
			return err
		}
	}
	return err
}
`,
			"platform/version/version.go": `// Package version reports which build of the service is running.
package version

// Number is the released version of the service.
const Number = "4.2.0"

// String renders the version for the health endpoint.
func String() string { return "pay/" + Number }
`,
		},
		Head: map[string]string{
			"billing/charge.go": `// Package billing turns an authorised order into money.
package billing

import (
	"fmt"
	"net/http"

	"example.com/pay/platform/retry"
)

// Gateway sends one request to the payment provider.
type Gateway interface {
	Send(method, path, body string) error
}

// Capture takes the payment an order has already authorised.
func Capture(gw Gateway, orderID string, cents int) error {
	body := fmt.Sprintf("{\"order\":%q,\"amount_cents\":%d}", orderID, cents)

	return retry.Do(http.MethodPost, func() error {
		return gw.Send(http.MethodPost, "/v1/captures", body)
	})
}
`,
			"billing/customer.go": `package billing

// Customer is who an order belongs to.
type Customer struct {
	ID    string
	Email string
}

// Label renders a customer for a receipt.
func (c Customer) Label() string {
	if c.Email == "" {
		return c.ID
	}
	return c.ID + " <" + c.Email + ">"
}

// Anonymous reports whether the customer left no contact address.
func (c Customer) Anonymous() bool { return c.Email == "" }
`,
			"billing/ledger.go": `package billing

// Entry is one line of the ledger.
type Entry struct {
	OrderID string
	Cents   int
}

// Ledger accumulates entries in the order they were recorded.
type Ledger struct {
	entries []Entry
}

// Record appends an entry.
func (l *Ledger) Record(e Entry) {
	l.entries = append(l.entries, e)
}

// Entries returns a copy of what has been recorded.
func (l *Ledger) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Total sums every entry recorded so far.
func (l *Ledger) Total() int {
	total := 0
	for _, e := range l.entries {
		total += e.Cents
	}
	return total
}
`,
			"internal/clock/clock.go": `// Package clock is the time source the billing code reads.
package clock

import "time"

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

// System reads the machine clock.
type System struct{}

// Now returns the current time.
func (System) Now() time.Time { return time.Now() }

// Fixed reports one instant however often it is asked, for tests.
type Fixed struct {
	At time.Time
}

// Now returns the fixed instant.
func (f Fixed) Now() time.Time { return f.At }
`,
			"internal/logging/logging.go": `// Package logging names the levels the service logs at.
package logging

// Level is how important a log record is.
type Level int

// The levels, least important first.
const (
	Debug Level = iota
	Info
	Warn
	Error
)

// String renders a level for a log line.
func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "unknown"
}

// AtLeast reports whether l is at least as important as floor.
func (l Level) AtLeast(floor Level) bool { return l >= floor }
`,
			"internal/orderid/orderid.go": `// Package orderid formats and checks order identifiers.
package orderid

import "strings"

// Prefix is what every order identifier starts with.
const Prefix = "ord_"

// Valid reports whether s is an order identifier.
func Valid(s string) bool {
	return strings.HasPrefix(s, Prefix) && len(s) > len(Prefix)
}

// Suffix returns the part after the prefix, or s unchanged when it carries no
// prefix.
func Suffix(s string) string { return strings.TrimPrefix(s, Prefix) }
`,
			"platform/retry/retry.go": `// Package retry re-runs an operation that failed against the payment gateway.
package retry

import "net/http"

// Attempts is how many times Do runs an operation before giving up.
const Attempts = 3

// Replayable reports whether an operation using this HTTP method can be run a
// second time without repeating work the gateway has already done.
//
// Every idempotent method qualifies: applying one of them twice leaves the
// gateway in the state a single application would have left it in. PATCH is
// the exception, because a patch document may describe a relative change.
func Replayable(method string) bool {
	return method != http.MethodPatch
}

// Do runs op, re-running it while it fails and the method is one Replayable
// allows to be repeated.
func Do(method string, op func() error) error {
	var err error
	for range Attempts {
		if err = op(); err == nil {
			return nil
		}
		if !Replayable(method) {
			return err
		}
	}
	return err
}
`,
			"platform/version/version.go": `// Package version reports which build of the service is running.
package version

// Number is the released version of the service.
const Number = "4.3.0"

// Commit is the revision this build came from, set at link time.
var Commit = "unknown"

// String renders the version for the health endpoint.
func String() string { return "pay/" + Number + "+" + Commit }
`,
		},

		Defects: []Defect{{
			Path: "platform/retry/retry.go",
			Line: 16,

			// Keywords describe the CONSEQUENCE, and none of them is a word a
			// reviewer types merely by quoting the change. "replayable",
			// "idempotent", "retry" and "post" are all in the diff, so any of
			// them would pay full recall to a comment that had noticed nothing.
			// "twice" and "duplicate" are what a reviewer says only after
			// working out that the gateway can apply the same capture again.
			Keywords: []string{"twice", "duplicate", "double", "again"},

			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityWarning,

			// Named for the day this is wired into a scored corpus; see
			// dedupFixtures. review.md's warning anchor is "likely a bug, or a
			// genuine hazard under plausible conditions", and its worked
			// example is literally "retrying a non-idempotent request". WARNING
			// rather than error because nothing is wrong on a normal path: the
			// capture is sent once and succeeds once until an attempt fails
			// after the gateway has already applied it. WARNING rather than
			// info because the mechanism and the failing input are both
			// nameable, which is not something an author may decline.
			SeverityNote: "warning, not error: every capture is correct until an " +
				"attempt fails after the gateway applied it, which is a plausible " +
				"condition rather than a guaranteed one",

			Why: "Replayable admitted only GET and HEAD; it now admits every method " +
				"except PATCH, so retry.Do re-sends a POST the gateway may already " +
				"have applied. billing/charge.go:20 hands a payment capture to it as " +
				"a POST with nothing that lets the gateway recognise a repeat, so a " +
				"capture that times out after being applied is applied a second time. " +
				"The two files are in different batches on purpose: this is the only " +
				"fixture where one defect is reportable from each side of the split.",
		}},
	}
}
