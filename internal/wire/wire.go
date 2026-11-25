// Package wire defines the review record the notary stores and the pinned
// input hash that keys it. A record keeps the raw model answer per batch, so
// one mechanism gives both replay (every batch present, rerun the gate) and
// resume (some batches present, ask only for the rest).
package wire

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Schema versions the record layout and the gate's behaviour. Bump it when
// either changes: old records then hash differently and are never replayed.
const Schema = 3

// Inputs is everything that decides what a review says. A change to any field
// is a different review and gets a different hash.
type Inputs struct {
	Tool     string // tool version
	Provider string
	Model    string
	System   string   // the system prompt
	Settings []string // name=value pairs that affect the output, in a fixed order
	Batches  []string // the packed prompts, in order
}

// Hash returns the pinned input hash. Fields are length-prefixed so two
// different splits of the same bytes cannot collide.
func (in Inputs) Hash() string {
	h := sha256.New()
	put := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	put("schema")
	put(strconv.Itoa(Schema))
	for _, s := range []string{in.Tool, in.Provider, in.Model, in.System} {
		put(s)
	}
	// Each list is prefixed with its length. A delimiter word alone cannot
	// separate Settings from Batches, because a setting may equal it.
	put(strconv.Itoa(len(in.Settings)))
	for _, s := range in.Settings {
		put(s)
	}
	put(strconv.Itoa(len(in.Batches)))
	for _, b := range in.Batches {
		put(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BatchKey names one batch inside a record.
func BatchKey(batch string) string {
	sum := sha256.Sum256([]byte(batch))
	return hex.EncodeToString(sum[:8])
}

// Record is one stored review.
type Record struct {
	Schema   int               `json:"schema"`
	Hash     string            `json:"hash"`
	Tool     string            `json:"tool"`
	Provider string            `json:"provider"`
	Model    string            `json:"model"`
	Created  time.Time         `json:"created"`
	Answers  map[string]string `json:"answers"` // BatchKey to raw model text
	Batches  int               `json:"batches"` // how many answers make it complete
	Complete bool              `json:"complete"`
	Source   string            `json:"source"` // who wrote it: "ci", a token name
	// Effort is the reasoning level the answers were produced at, when a
	// decision model chose it. It is stored rather than hashed so a replay
	// uses the level that made the record, whatever the chooser says today.
	Effort    string   `json:"effort,omitempty"`
	Clears    int      `json:"clears"`
	ClearedBy []string `json:"cleared_by,omitempty"`
}

// Store keeps records. Implementations must be safe for concurrent use.
type Store interface {
	// Get returns the record for a hash, or ok=false when there is none.
	Get(ctx context.Context, hash string) (rec Record, ok bool, err error)
	// Put stores a record. A complete record is never replaced by an
	// incomplete one.
	Put(ctx context.Context, rec Record) error
	// Clear retires the record, keeps it as history and counts the clear.
	Clear(ctx context.Context, hash, by string) error
}

// ErrUnreachable marks a store that could not be contacted. A review treats it
// as "no notary" and carries on.
var ErrUnreachable = errors.New("notary unreachable")

// ErrForbidden marks a store that refused the caller's token.
var ErrForbidden = errors.New("notary refused the token")
