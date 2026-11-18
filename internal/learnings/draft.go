package learnings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Feedback is evidence for a proposed reference. Classification describes
// the operator's rationale; a resolved thread alone supplies no classification.
type Feedback struct {
	Repository     string `json:"repository"`
	SourceURL      string `json:"source_url"`
	SourceRevision string `json:"source_revision"`
	EvidenceDate   string `json:"evidence_date"`
	Classification string `json:"classification"`
	Rationale      string `json:"rationale"`
	BotGenerated   bool   `json:"bot_generated"`
	Text           string `json:"text"`
}

// Draft retains provenance without retaining the original thread body.
// It cannot be loaded as an active Manifest.
type Draft struct {
	Kind              string `json:"kind"`
	Entry             Entry  `json:"entry"`
	SourceExcerptHash string `json:"source_excerpt_hash"`
}

// Propose invokes a text-only generator after explicit provider consent and
// redaction. The caller provides no shell or forge-write capabilities.
func Propose(ctx context.Context, feedback Feedback, consent bool, redact func(string) string, generate func(context.Context, string) (Entry, error)) (Draft, error) {
	if !consent {
		return Draft{}, errors.New("provider consent is required before drafting from feedback")
	}
	if feedback.BotGenerated {
		return Draft{}, errors.New("bot-generated events are not independent feedback")
	}
	if strings.TrimSpace(feedback.Text) == "" || len(feedback.Text) > 16*1024 {
		return Draft{}, errors.New("feedback must contain from 1 to 16384 bytes")
	}
	if redact == nil {
		return Draft{}, errors.New("feedback redaction is required")
	}
	text := redact(feedback.Text)
	entry, err := generate(ctx, text)
	if err != nil {
		return Draft{}, err
	}
	entry.Repository = feedback.Repository
	entry.SourceURL = feedback.SourceURL
	entry.SourceRevision = feedback.SourceRevision
	entry.EvidenceDate = feedback.EvidenceDate
	entry.Classification = feedback.Classification
	entry.Rationale = redact(feedback.Rationale)
	entry.Body = redact(entry.Body)
	entry.Title = redact(entry.Title)
	entry.ApprovedBy = "draft"
	entry.ContentHash = entry.Hash()
	if err := validateEntry(entry, feedback.Repository); err != nil {
		return Draft{}, err
	}
	entry.ApprovedBy = ""
	entry.ContentHash = entry.Hash()
	excerptHash := sha256.Sum256([]byte(text))
	return Draft{Kind: "draft", Entry: entry, SourceExcerptHash: hex.EncodeToString(excerptHash[:])}, nil
}

// Accept binds an individually reviewed draft to the authenticated operator.
// The returned entry becomes active only after landing in the trusted base.
func Accept(ctx context.Context, draft Draft, reviewedHash string, authorize func(context.Context, string) (string, error)) (Entry, error) {
	if draft.Kind != "draft" || reviewedHash == "" || reviewedHash != draft.Entry.Hash() || reviewedHash != draft.Entry.ContentHash {
		return Entry{}, errors.New("reviewed hash does not match this draft")
	}
	if authorize == nil {
		return Entry{}, errors.New("authenticated operator is required")
	}
	identity, err := authorize(ctx, draft.Entry.Repository)
	if err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(identity) == "" {
		return Entry{}, errors.New("authenticated operator identity is missing")
	}
	entry := draft.Entry
	entry.ApprovedBy = identity
	entry.ContentHash = entry.Hash()
	if err := validateEntry(entry, entry.Repository); err != nil {
		return Entry{}, err
	}
	return entry, nil
}
