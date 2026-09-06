package evals

import "github.com/jdziat/open-nitpick/v2/internal/config"

// dedupFixtures is the corpus's cross-batch DEDUP fixture, and it is here for a
// property no other fixture has: the same defect is reportable from more than
// one batch, so the merge that runs before triage has something to merge.
//
// WHAT WAS WRONG. ts-unbounded-memo-key made this project's first multi-batch
// review happen at all — seven files at the shipped max_files_per_request of 6
// — but it was authored to keep the plant and the files needed to see it in the
// SAME batch, because a defect split across requests is one no reviewer can
// find and a plant nothing can find scores as a prompt weakness forever. That
// is the right call for a scored plant, and its cost is that the second batch
// has nothing to say: only one batch ever reports the defect, so dedupe(),
// which triage() runs over the combined findings, has never had two reports of
// one defect to collapse. The engine's cross-batch merge — README calls it a
// headline capability — has therefore never merged anything under any
// measurement or any test. fixtures_warning.go says so in its own words: "a
// fixture that makes it do so is still owed". This is that fixture.
//
// WHY BOTH BATCHES REPORT IT. The defect is one bug with two faces, and each
// batch holds a complete, independently reportable face of it:
//
//   - platform/retry/retry.go:16 (batch 2) is the CAUSE. Replayable used to
//     admit only the read methods; it now admits everything except PATCH. A
//     reviewer holding that file alone can name the consequence without seeing
//     any caller: Do re-sends a POST the gateway may already have applied.
//   - billing/charge.go:20 (batch 1) is the EFFECT. Capture hands a payment
//     capture to that retry helper as a POST, with nothing on the request that
//     lets the gateway recognise a repeat. A reviewer holding that file alone
//     can name the consequence without seeing the helper: a capture that is
//     retried captures twice. "Retrying a non-idempotent request" is the review
//     prompt's own worked example of a warning, so this is not a defect a
//     reviewer has to be clever to see from either end.
//
// Neither half needs the other to be reportable, which is exactly the shape
// ts-unbounded-memo-key deliberately does not have: there, the defect is
// invisible from either file alone, so splitting it across batches would erase
// it. Here, splitting it across batches DUPLICATES it. That inversion is the
// whole fixture.
//
// WHY BOTH NAME THE SAME LINE. The two reports collapse only if they land on
// the same path and line, because Finding.Key is path, line and normalized
// title. review.md tells a reviewer to "anchor to the line where the problem
// is, not where its effect surfaces", and for this defect that line is
// retry.go:16 — the predicate that declares a POST replayable. The effect side
// reaches the same line from the other end: charge.go's own import names the
// helper, the helper is part of this same change, and filterAnchors validates a
// finding's path against the WHOLE change rather than against the batch that
// produced it, which is what lets a finding from batch 1 anchor there at all.
//
// THE RESIDUAL, STATED RATHER THAN HIDDEN: review.md also says "path must
// exactly match one of the file paths given below", and a reviewer that obeys
// that literally anchors in its own batch — charge.go:20 from batch 1,
// retry.go:16 from batch 2. Those are two keys, and dedupe cannot collapse
// them; only the triage model can. TestOneDefectAnchoredTwiceIsNotDeduped pins
// that boundary so nobody reads the test above as a claim the engine merges
// every cross-batch duplicate. It merges the ones that agree about where the
// problem is.
//
// DELIBERATELY IN NEITHER CORPUS, and this is the one thing about this file
// that has to be read before it is copied. Fixtures() and HeldOutFixtures() do
// not name it, so AllFixtures() does not contain it and NO ground-truth test in
// groundtruth_test.go touches it — which is precisely the failure
// TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus exists to catch for
// warningFixtures and nitFixtures. It is not an oversight here and the checks
// are not skipped: fixtures_dedup_test.go re-derives this fixture's line
// numbers out of Head by counting, checks the plant sits on a line the engine
// could publish a comment on, and checks the batch split, rather than trusting
// the paragraphs above. It stays out of the scored corpora because a fixture
// whose point is that ONE defect gets reported TWICE would be scored as one
// detection and one false positive by a reviewer that did exactly the right
// thing, which would make the noise column read a correct review as a sloppy
// one.
//
// IF IT IS EVER WIRED IN, two things have to move with it, and neither is free:
// score.go would need a notion of a defect with more than one acceptable
// anchor, and this plant is correctness at warning while every correctness
// plant in fixtures.go is error with no SeverityNote, so
// TestSeverityIsConsistentWithinADefectClass would turn three green plants red
// in a file this change does not own. SeverityNote below is written for that
// day; it changes nothing today.
//
// IT WAS RUN. Both states were extracted to a temp module and built, vetted,
// gofmt-ed and executed under `go test`. Head reproduces the plant — a gateway
// that applies a capture and then reports a timeout receives the same capture
// body twice for one order — and base does not, because base refuses to replay
// a POST at all. The filler files are exercised by the same run rather than
// eyeballed, which is what would have caught the second, unplanted,
// user-reachable defect ts-unbounded-memo-key shipped with: a Ledger that
// handed out its internal slice, or a Level whose String fell through, are both
// findings a reviewer would report and neither is planted.
func dedupFixtures() []Fixture {
	return []Fixture{crossBatchReplayFixture()}
}

// crossBatchReplayFixture widens a retry helper's "safe to repeat" predicate
// and, in the same change, sends a payment capture through it.
//
// THE BATCH SPLIT IS LOAD-BEARING AND IT IS ALPHABETICAL. git orders a diff by
// path, bundle.batch fills a request with up to max_files_per_request entries in
// that order, and exactly six of the eight changed paths sort before platform/:
// three under billing/, three under internal/. So batch 1 is billing/charge.go
// plus five files with nothing wrong with them, and batch 2 is
// platform/retry/retry.go plus platform/version/version.go. REMOVING one of
// those six is what breaks it: five paths before platform/ leaves room for
// retry.go in the first request, both halves arrive together, one reviewer sees
// the whole defect and reports it once, and the fixture silently stops testing
// anything while every test here still compiles. Adding one is survivable but
// not free — the split moves, and whichever filler lands beside retry.go is the
// file a reviewer of batch 2 has to ignore. TestTheDedupFixtureSplitsTheDefect
// reads the split back out of bundle.Assemble rather than trusting this
// paragraph, for the reason the corpus already learned once: two one-line edits
// were enough to falsify the same claim about ts-unbounded-memo-key while
// `go test ./...` printed ok.
//
// THE FILLER IS NOT PADDING AND IT IS NOT DECORATION. Six files carry no
// defect — five holding batch-1 slots and version.go riding along in batch 2 —
// and every one of them is a change a reviewer should wave through: a method
// that reports whether a customer left an address, a total over entries the
// ledger was already copying out defensively, a fixed clock for tests, a level
// comparison, a prefix trim, a version bump. A filler file with
// a defect in it would be a false positive charged to every reviewer that
// reported it, and a filler file with no added lines at all would be dropped by
// bundle for having nothing to comment on — which would shrink the change back
// to one batch.
//
// WHAT THE FIXTURE IS CAREFUL NOT TO INVITE. The head's predicate excludes
// PATCH and admits GET, HEAD, PUT and DELETE, all four of which genuinely are
// idempotent, so there is exactly one thing wrong with it: POST. An earlier
// draft excluded DELETE instead, which is idempotent, and that hands a reviewer
// a second true remark — "your exclusion is backwards" — in a fixture whose
// whole premise is that there is one defect to report twice. Do itself is
// byte-identical in both states for the same reason: a retry loop that is new
// code invites remarks about backoff and jitter, and the corpus already plants
// retry-no-backoff as a separate warning in the held-out set.
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
			// nameable, which is not something an author may simply decline.
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
