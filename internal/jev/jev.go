// Package jev asks TypeSafe's Jev decision model, through OpenRouter's decisions
// endpoint, which kind of finding a review comment is.
//
// It is an optional second opinion on a finding that has already been written,
// and the question it asks is a single five-way choice, given the
// comment, the code it rests on and what a checker found.
// The package knows nothing about reviews. It sends a state and returns the
// probabilities, and the caller decides what a probability is worth.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/llm"
)

// DefaultEndpoint is OpenRouter's decisions endpoint.
const DefaultEndpoint = "https://openrouter.ai/api/alpha/decisions"

// DefaultModel resolves to the current Jev release.
const DefaultModel = "~typesafe/jev-latest"

// Category names, as the question offers them and the answer keys them.
const (
	Bug     = "bug"
	Failure = "error"
	Warning = "warning"
	Info    = "info"
	Noise   = "noise"
)

// category is one answer the question offers.
type category struct{ name, meaning string }

// categories are offered in this order. The order is part of what was measured:
// the criteria are an ordered list to the model, and a JSON object built from a
// Go map would reorder them alphabetically.
var categories = []category{
	{Bug, "a real defect that is reachable on a path the code takes and should be fixed"},
	{Failure, "a real failure that breaks behaviour, data or a contract for some caller"},
	{Warning, "a real concern that is risky or fragile but does not break current behaviour"},
	{Info, "a true observation that needs no change"},
	{Noise, "wrong, already handled elsewhere in the code shown, speculative, or not supported by the code"},
}

const instructions = "Which best describes the review comment's claim, given the code shown? " +
	"Pick the category the code supports; pick noise if the code contradicts or does not support the claim."

// questionName keys the one question in a request and its answer.
const questionName = "verdict"

// maxResponseBytes bounds what is read back. The answer is a few hundred bytes,
// and the endpoint is a third party's.
const maxResponseBytes = 1 << 20

// Scores are the probabilities Jev gave each category, keyed by category name.
type Scores map[string]float64

// Defect is the probability that the finding is a defect that breaks something:
// bug plus error. It is the score that ranked agent
// verdicts best, and it deliberately leaves out warning, which is a real
// concern that breaks nothing yet.
func (s Scores) Defect() float64 { return s[Bug] + s[Failure] }

// Leaning names the category with the most probability. Ties go to the one the
// question lists first, so the same scores always give the same word.
func (s Scores) Leaning() string {
	best, name := math.Inf(-1), ""
	for _, c := range categories {
		if p := s[c.name]; p > best {
			best, name = p, c.name
		}
	}
	return name
}

// Client calls the decisions endpoint. Build one with NewClient.
type Client struct {
	endpoint string
	model    string
	key      string
	http     *http.Client
	attempts int
	pause    func(context.Context, time.Duration) error
	observe  func(llm.CallRecord)
}

// ObserveCalls installs an attempt observer before the client is used.
// The decisions endpoint has no normalized token usage contract; its usage
// stays unknown instead of being inferred from the size of the request.
func (c *Client) ObserveCalls(fn func(llm.CallRecord)) { c.observe = fn }

// NewClient returns a client for an endpoint, a model and an OpenRouter key.
// An empty key is an error rather than a client that fails on first use: the
// caller learns before it has built a request that nothing could authorise.
func NewClient(endpoint, model, key string) (*Client, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("jev needs an OpenRouter API key")
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	return &Client{
		endpoint: endpoint,
		model:    model,
		key:      strings.TrimSpace(key),
		http: &http.Client{
			Timeout: 90 * time.Second,
			// A decisions endpoint has no reason to redirect, and a redirect is
			// where a request carrying a credential and source code would be
			// sent somewhere nobody configured.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		attempts: 3,
		pause:    sleep,
	}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// orderedCriteria marshals the category list as a JSON object in list order.
type orderedCriteria []category

func (o orderedCriteria) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, c := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(c.name)
		meaning, _ := json.Marshal(c.meaning)
		b.Write(name)
		b.WriteByte(':')
		b.Write(meaning)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

type question struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     orderedCriteria `json:"criteria"`
}

type request struct {
	State     string              `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]question `json:"questions"`
}

type response struct {
	Answers map[string]struct {
		Probabilities map[string]float64 `json:"probabilities"`
	} `json:"answers"`
}

// retryable marks an error worth another attempt: the endpoint was busy or
// unreachable, as opposed to having refused the request.
type retryable struct{ error }

func (r retryable) Unwrap() error { return r.error }

// Classify asks which category the state describes and returns the probabilities.
//
// A busy or unreachable endpoint is tried up to three times. A refusal, a
// malformed answer or a cancelled context is returned at once, since asking
// again would not change it.
func (c *Client) Classify(ctx context.Context, state string) (Scores, error) {
	body, err := json.Marshal(request{
		State: state,
		Model: c.model,
		Questions: map[string]question{
			questionName: {Type: "choice", Instructions: instructions, Criteria: orderedCriteria(categories)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode jev request: %w", err)
	}
	var last error
	for attempt := range c.attempts {
		if attempt > 0 {
			if err := c.pause(ctx, time.Duration(attempt)*time.Second); err != nil {
				return nil, err
			}
		}
		started := time.Now().UTC()
		scores, err := c.once(ctx, body)
		if c.observe != nil {
			c.observe(llm.CallRecord{Provider: "openrouter", Model: c.model, Role: "jev",
				StartedAt: started, DurationMS: time.Since(started).Milliseconds(), Failed: err != nil})
		}
		if err == nil {
			return scores, nil
		}
		last = err
		if !errors.As(err, new(retryable)) {
			break
		}
	}
	return nil, fmt.Errorf("jev decision: %w", last)
}

func (c *Client) once(ctx context.Context, body []byte) (Scores, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// The transport's error can quote the URL but not the header, so the
		// key stays out of it. A cancelled context is the caller's, not ours.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, retryable{fmt.Errorf("send request: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	// The status alone is reported. The body of an error is a third party's
	// text on its way into a log and, from there, a pull request comment.
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, retryable{fmt.Errorf("endpoint answered HTTP %d", resp.StatusCode)}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("endpoint answered HTTP %d", resp.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read answer: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return nil, errors.New("answer exceeds response byte limit")
	}
	var out response
	if err := json.Unmarshal(responseBody, &out); err != nil {
		return nil, fmt.Errorf("decode answer: %w", err)
	}
	return scoresFrom(out)
}

// scoresFrom checks an answer before it is believed. A missing category would
// read as probability zero, and a finding would then be scored as certainly not
// a defect on the strength of an answer that said nothing about it.
func scoresFrom(r response) (Scores, error) {
	a, ok := r.Answers[questionName]
	if !ok {
		return nil, errors.New("answer has no verdict")
	}
	scores := make(Scores, len(categories))
	for _, c := range categories {
		p, ok := a.Probabilities[c.name]
		if !ok {
			return nil, fmt.Errorf("answer has no probability for %q", c.name)
		}
		if math.IsNaN(p) || p < 0 || p > 1 {
			return nil, fmt.Errorf("probability for %q is %v, outside 0 to 1", c.name, p)
		}
		scores[c.name] = p
	}
	return scores, nil
}
