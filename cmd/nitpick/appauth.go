package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// App credentials let a long run mint its own installation tokens instead of
// holding one that expires: the private key is read once at startup, the
// tokens it mints are not, and each lives about an hour.
type appCredentials struct {
	appID         int64
	installation  int64
	privateKeyPEM []byte
}

// appCredentialsFromEnv reads the App configuration the workflows already
// carry. NITPICK_APP_ID gates the whole feature, matching the workflow step
// that only runs the App token mint when the ID is set; an empty ID means
// "no App configured", which is not an error.
func appCredentialsFromEnv() (*appCredentials, error) {
	id := strings.TrimSpace(os.Getenv("NITPICK_APP_ID"))
	if id == "" {
		return nil, nil
	}
	appID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("NITPICK_APP_ID %q is not an App ID: %w", id, err)
	}
	key := os.Getenv("NITPICK_APP_PRIVATE_KEY")
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("NITPICK_APP_ID is set but NITPICK_APP_PRIVATE_KEY is empty")
	}
	var installation int64
	if raw := strings.TrimSpace(os.Getenv("NITPICK_APP_INSTALLATION_ID")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("NITPICK_APP_INSTALLATION_ID %q is not an installation ID: %w", raw, err)
		}
		installation = parsed
	}
	return &appCredentials{appID: appID, installation: installation, privateKeyPEM: []byte(key)}, nil
}

// providerOptions builds the GitHub options for these credentials: a
// self-refreshing installation transport when the installation ID is known,
// otherwise the static token unchanged. Empty credentials return nil so the
// caller falls back to GITHUB_TOKEN without knowing this code exists.
func (a *appCredentials) providerOptions(baseURL, staticToken, botLogin string) (vcs.GitHubOptions, bool, error) {
	if a == nil {
		return vcs.GitHubOptions{Token: staticToken, BaseURL: baseURL, BotLogin: botLogin}, false, nil
	}
	if a.installation == 0 {
		return vcs.GitHubOptions{}, false, errors.New("NITPICK_APP_INSTALLATION_ID is required for self-refreshing tokens")
	}
	transport, err := ghinstallation.New(http.DefaultTransport, a.appID, a.installation, a.privateKeyPEM)
	if err != nil {
		return vcs.GitHubOptions{}, false, fmt.Errorf("github app transport: %w", err)
	}
	if strings.TrimSpace(baseURL) != "" {
		transport.BaseURL = strings.TrimRight(baseURL, "/") + "/"
	}
	return vcs.GitHubOptions{
		Installations: transport,
		BaseURL:       baseURL,
		BotLogin:      botLogin,
	}, true, nil
}
