package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// GitHubOIDCToken returns a source for GitHub Actions identity tokens minted
// for audience. It fails, rather than falling back to nothing, when the job
// was not granted id-token: write, because a missing token silently sent as an
// empty bearer reads as "the notary refused me".
func GitHubOIDCToken(audience string) func(context.Context) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ctx context.Context) (string, error) {
		base, bearer := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
		if base == "" || bearer == "" {
			return "", errors.New("no GitHub OIDC token is available: the job needs permissions: id-token: write")
		}
		u, err := url.Parse(base)
		if err != nil {
			return "", fmt.Errorf("parse the token request URL: %w", err)
		}
		q := u.Query()
		q.Set("audience", audience)
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("request an identity token: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("request an identity token: %s", resp.Status)
		}
		var out struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil || out.Value == "" {
			return "", errors.New("the identity token response held no token")
		}
		return out.Value, nil
	}
}
