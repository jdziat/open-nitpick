package review

import (
	"fmt"
	"sync"
)

// What retrieval did on a run, and why.
//
// Retrieval used to report itself by returning a nil retriever and writing a
// line to the log. That is enough for an operator reading a terminal and not
// enough for an evaluation: a run whose embedding provider refused every
// request reviewed without retrieval, and scoring it as a retrieval-on
// treatment measures the control twice and calls the difference noise. The
// status is carried on the report so a measurement can separate the arms it
// actually ran.

// KnowledgeState is the four things retrieval can have done.
type KnowledgeState string

const (
	// KnowledgeOff is retrieval not asked for. review.knowledge is false.
	KnowledgeOff KnowledgeState = "off"
	// KnowledgeActive is retrieval asked for, built, and answering.
	KnowledgeActive KnowledgeState = "active"
	// KnowledgeSkipped is retrieval asked for and not possible for a reason
	// the operator can fix: no models.embed, an empty corpus.
	KnowledgeSkipped KnowledgeState = "skipped"
	// KnowledgeFailed is retrieval asked for, possible, and broken: refused
	// credentials, an embedding error, a model or index mismatch.
	//
	// Separate from skipped because the two are different bugs. Skipped is a
	// configuration a person did not write; failed is a thing that should have
	// worked.
	KnowledgeFailed KnowledgeState = "failed"
)

// KnowledgeStatus is the state and the evidence for it.
type KnowledgeStatus struct {
	State  KnowledgeState
	Reason string

	// Model and Entries describe what was loaded, empty unless it was.
	Model   string
	Entries int

	// Queries and Failures count the batches retrieval was asked about and
	// the ones it could not answer. A run with failures is reported failed
	// even when some batches were served: an arm that retrieved for half its
	// batches is neither treatment nor control.
	Queries  int
	Failures int
}

// String is the one line a log or a report renders.
func (s KnowledgeStatus) String() string {
	out := string(s.State)
	if out == "" {
		out = string(KnowledgeOff)
	}
	if s.Reason != "" {
		out += ": " + s.Reason
	}
	if s.State == KnowledgeActive || s.Failures > 0 {
		out += fmt.Sprintf(" (%d entries, %s, %d queries, %d failed)", s.Entries, s.Model, s.Queries, s.Failures)
	}
	return out
}

// Retrieved reports whether every batch this run asked about was served.
//
// The question an evaluation has to ask before it scores a run, and the reason
// it is a method rather than a comparison against KnowledgeActive at each call
// site: "on" and "worked" are not the same claim.
func (s KnowledgeStatus) Retrieved() bool {
	return s.State == KnowledgeActive && s.Failures == 0 && s.Queries > 0
}

// counters accumulate what happened during a review. Held on the retriever
// because the engine reaches it from every batch goroutine.
type counters struct {
	mu       sync.Mutex
	queries  int
	failures int
}

func (c *counters) query()   { c.mu.Lock(); c.queries++; c.mu.Unlock() }
func (c *counters) failure() { c.mu.Lock(); c.failures++; c.mu.Unlock() }

func (c *counters) read() (queries, failures int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queries, c.failures
}
