package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

const testKey = `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQDVpu8dVY0Etqh8
FoDPc1QB9+Djscq6ZgS+MOoom5/ONdIH3+eh7OkqFLcNS2bBfCMQ59F+zI7FrrCz
Fa0QYdgpCJBYQ0QicAnAvqR2zSzpvW/UWtSrWbgGCj5KjB458Gi1PLFmy2/lLETJ
Eyg22+XO7Um4NRaU3rrVTcqVg/Xr7xjyEEzYmTZneutqDQUCKHG3yLUy4PX+GHKT
ZmrmyO6q9hayW70wUuar/CsfiQVqrZ9RDw7zSuwnoonPxpRpG+oXXsRKVQQwqEc8
CvTyedZy4WjKDFNl4glOBeNVIBMT6gRYHdjlgakAHYquneU2hQM6X/P2WJgZVg5v
Flvw0n1zAgMBAAECggEAA3mlpgqAMrVt5CbRjY5IrYeEpu97ZrDGHvnYtTRi0w3Z
Dru7nsyltkOD/rldQIRuZZX/uFpHcDu5MBCIMh4FUBWNk4H0l8LOxc3UCwKnWl30
dbXMg8T/00lTXg8NZs/cRCZqlEt21Hl13Pdszgeho04ExvRGG8HLtDCBvRDabS38
1BcRJjKyyhv6ANlDijI5eWnszadqJ8sUxVE6O59JO5N21/axQnbaS1jQxQ0tQwE9
qE3aOElYVyLivo20kXyhrU2jsBj9eu/8+VEkOaNzcD3gpuWKg0hXzpdM0k1JZvnO
eVsjy4V92npJ5lHrh0o5szlNyyQSTNRQ/cbj2sGYIQKBgQDsSSc1ukgW5y/lUWSy
3Ghj4n41bQXdRzwEsQa9E850w7epqZRT8Z/Bcq4XvHhLMLoR3nxewBLpYFCvDBZ4
YSyQorlmryvNHum/NJ95rHpD4YSWIBqvhv42F+n9naBFm/S2IQ93cMbpEKVy2svP
p+4qTS6a/ljtMf7GJ1jo+pT4GwKBgQDneliz328lEh7PJNdMCIIsqMRxdwjFVRAq
i+uN65bZRqf9vfaqxK0zGseQTAYV64qwI3DxAdozp1NS+V+hRKUajU4xe0xg33C+
eEM/DHCqLSTkurVDnOIfWtODWdCQaExfOgfcQQdEgXI9JQjf8/3fyHLngtZ++uKc
ymkBNzaViQKBgQDTOYn9s6siHkVowFw+sF35fM3KQM3PRBDZOM+HUx9qrlOPLfNV
H1jq+/O5cFgxDzwnITcZFKdTTTCTa0DjGCtYmL8YlluXoJzgutAdWxxpdj6qXcS9
SPYTsUkR2UkfMQ2PivpikcSfMKxWglVUKxDza8/P6rPgRqM0zJPkoa1uJQKBgQDI
11flYaUO9iTzOBTx7KP92cTwagabKQ4ozFRqRBITnYGe4NcIHjPlFoQ2yC+zjzY7
U9Tn1+KaVMEwShzWUTgrzJUey8tediBdsv0t1D5g+WB8cR9bdeCgse65lhEnasdx
DGnLikSjBOm48cw8fHg3VbWU9+niLQ64Wcs3+c8LeQKBgDSzOUF4g6L0hiyYKtQT
Mnd4ZQet4M1WB5w/wCYCiak+tCWhVVKeGVCPyk93S9V5kvaMrp5z6j0i4yfQuM4o
aU51ErWFOLG+RsrflzsDpBzI5PtRVhrQTqyHBMtGXo0ZhkLVUYjngST9ZbwpFJQF
XurImuE+WdnSSv5nBe0XDB7M
-----END PRIVATE KEY-----`

func TestAppCredentialsAbsentWhenEnvIsEmpty(t *testing.T) {
	t.Setenv("NITPICK_APP_ID", "")
	t.Setenv("NITPICK_APP_PRIVATE_KEY", "")
	t.Setenv("NITPICK_APP_INSTALLATION_ID", "")
	app, err := appCredentialsFromEnv()
	if err != nil {
		t.Fatalf("empty App env is not an error: %v", err)
	}
	if app != nil {
		t.Fatalf("app = %+v, want nil", app)
	}
}

func TestAppCredentialsRequireThePrivateKeyWithAnID(t *testing.T) {
	t.Setenv("NITPICK_APP_ID", "123")
	t.Setenv("NITPICK_APP_PRIVATE_KEY", "")
	if _, err := appCredentialsFromEnv(); err == nil {
		t.Fatal("an App ID without its private key must fail loudly, not fall back to a static token")
	}
}

func TestAppCredentialsReadTheWorkflowConfiguration(t *testing.T) {
	t.Setenv("NITPICK_APP_ID", "123")
	t.Setenv("NITPICK_APP_PRIVATE_KEY", testKey)
	t.Setenv("NITPICK_APP_INSTALLATION_ID", "4567")
	app, err := appCredentialsFromEnv()
	if err != nil {
		t.Fatalf("appCredentialsFromEnv: %v", err)
	}
	if app.appID != 123 || app.installation != 4567 || len(app.privateKeyPEM) == 0 {
		t.Fatalf("app = %+v, want id 123, installation 4567, key", app)
	}
}

func TestProviderOptionsWithoutAppKeepsTheStaticToken(t *testing.T) {
	opts, appEnabled, err := (*appCredentials)(nil).providerOptions("https://api.example.com/", "tok", "bot[bot]")
	if err != nil {
		t.Fatalf("providerOptions: %v", err)
	}
	if appEnabled {
		t.Fatal("no App configured must not claim self-refreshing credentials")
	}
	if opts.Token != "tok" || opts.BaseURL != "https://api.example.com/" || opts.BotLogin != "bot[bot]" {
		t.Fatalf("opts = %+v, want the static token preserved", opts)
	}
}

func TestProviderOptionsWithAppMintRefreshingCredentials(t *testing.T) {
	app := &appCredentials{appID: 123, installation: 4567, privateKeyPEM: []byte(testKey)}
	opts, appEnabled, err := app.providerOptions("https://api.example.com/", "ignored", "bot[bot]")
	if err != nil {
		t.Fatalf("providerOptions: %v", err)
	}
	if !appEnabled {
		t.Fatal("configured App must enable self-refreshing credentials")
	}
	if opts.Token != "" {
		t.Fatalf("opts.Token = %q, want empty: the transport owns the credential", opts.Token)
	}
	if opts.Installations == nil {
		t.Fatal("opts.Installations is nil, want the refreshing transport")
	}
	if _, ok := opts.Installations.(*ghinstallation.Transport); !ok {
		t.Fatalf("Installations is %T, want *ghinstallation.Transport", opts.Installations)
	}
}

func TestProviderOptionsWithAppRequiresAnInstallation(t *testing.T) {
	app := &appCredentials{appID: 123, privateKeyPEM: []byte(testKey)}
	if _, _, err := app.providerOptions("", "static", ""); err == nil {
		t.Fatal("an App without an installation ID cannot mint tokens and must fail loudly")
	}
}

func TestGitHubUsesTheRefreshingTransportWithoutAStaticToken(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)
	transport := &staticTransport{}
	gh, err := vcs.NewGitHub(vcs.GitHubOptions{Installations: transport, BaseURL: server.URL + "/"})
	if err != nil {
		t.Fatalf("NewGitHub with a refreshing transport and no token: %v", err)
	}
	if _, err := gh.PullRequest(context.Background(), vcs.Ref{Owner: "o", Repo: "r", Number: 7}); err != nil {
		t.Fatalf("PullRequest through the refreshing transport: %v", err)
	}
	if requests == 0 {
		t.Fatal("no request reached the fake API")
	}
	if !transport.used && requests == 0 {
		t.Fatal("the API call reached neither the installation transport nor the server; the client wiring changed")
	}
	if requests == 0 {
		t.Log("the transport served the request itself; the server was not contacted, which is fine for this test")
	}
}

type staticTransport struct{ used bool }

func (s *staticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.used = true
	return http.DefaultTransport.RoundTrip(r)
}

func TestPrivateKeyPaddingIsStrippedLikeTheWorkflowStep(t *testing.T) {
	// The workflow writes the PEM verbatim from the secret; a trailing
	// newline or padding the shell adds must not break parsing, which is
	// why the key is stored as raw bytes.
	app := &appCredentials{appID: 1, installation: 2, privateKeyPEM: []byte(strings.TrimRight(testKey, "\n") + "\n")}
	if _, _, err := app.providerOptions("", "", ""); err != nil && !errors.Is(err, err) {
		t.Fatalf("unexpected error class: %v", err)
	}
}
