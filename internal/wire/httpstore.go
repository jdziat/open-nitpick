package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPStore talks to a notary server.
type HTTPStore struct {
	base  string
	token string
	// source, when set, supplies the token per request. A CI identity token
	// lives for minutes and a review can outlast it.
	source func(context.Context) (string, error)
	http   *http.Client
}

// NewHTTPStore builds a client. The base URL and token are both required.
func NewHTTPStore(base, token string) (*HTTPStore, error) {
	if base == "" || token == "" {
		return nil, errors.New("notary needs a URL and a token")
	}
	return &HTTPStore{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

// NewHTTPStoreWithSource builds a client whose token is fetched for every
// request, for credentials that expire.
func NewHTTPStoreWithSource(base string, source func(context.Context) (string, error)) (*HTTPStore, error) {
	if base == "" || source == nil {
		return nil, errors.New("notary needs a URL and a token source")
	}
	return &HTTPStore{base: strings.TrimRight(base, "/"), source: source, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

func (s *HTTPStore) do(ctx context.Context, method, hash string, body []byte, query string) (*http.Response, error) {
	u := s.base + "/v1/records/" + hash + query
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build notary request: %w", err)
	}
	token := s.token
	if s.source != nil {
		var err error
		if token, err = s.source(ctx); err != nil {
			return nil, fmt.Errorf("notary token: %w", err)
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		// A cancelled or expired context is the caller stopping, not the
		// notary being down. ErrUnreachable means "carry on without records",
		// which an interrupted review must not do.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("notary request: %w", ctxErr)
		}
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return resp, nil
}

// Get implements Store.
func (s *HTTPStore) Get(ctx context.Context, hash string) (Record, bool, error) {
	resp, err := s.do(ctx, http.MethodGet, hash, nil, "")
	if err != nil {
		return Record{}, false, err
	}
	defer closeBody(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		var r Record
		if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&r); err != nil {
			return Record{}, false, fmt.Errorf("decode notary record: %w", err)
		}
		return r, true, nil
	case http.StatusNotFound:
		return Record{}, false, nil
	}
	return Record{}, false, statusErr(resp)
}

// Put implements Store.
func (s *HTTPStore) Put(ctx context.Context, rec Record) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	resp, err := s.do(ctx, http.MethodPut, rec.Hash, b, "")
	if err != nil {
		return err
	}
	defer closeBody(resp)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return statusErr(resp)
}

// Clear implements Store.
func (s *HTTPStore) Clear(ctx context.Context, hash, by string) error {
	resp, err := s.do(ctx, http.MethodDelete, hash, nil, "?"+url.Values{"by": {by}}.Encode())
	if err != nil {
		return err
	}
	defer closeBody(resp)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return statusErr(resp)
}

func statusErr(resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w (status %d): %s", ErrForbidden, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w (status %d)", ErrUnreachable, resp.StatusCode)
	}
	return fmt.Errorf("notary returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}

// closeBody drops the unread remainder of a response. A close error here
// changes nothing the caller can act on.
func closeBody(resp *http.Response) { _ = resp.Body.Close() }
