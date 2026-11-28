package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// EnvInsightsToken carries the credential for the insights server, so it never
// appears in argv or a process listing.
const EnvInsightsToken = "NITPICK_INSIGHTS_TOKEN"

// insightsFlags are the client side of the insights component: where the server
// is and which repository this review is of.
type insightsFlags struct {
	url     string
	repo    string
	token   string
	timeout time.Duration
}

func (i *insightsFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&i.url, "insights", "", "nitpick-server URL: report this review's findings so the server can track hotspots over time (token in "+EnvInsightsToken+")")
	fs.StringVar(&i.repo, "insights-repo", "", "repository name to report under (default: the pull request's, then GITHUB_REPOSITORY)")
}

// check refuses a combination that would do nothing and say nothing, and takes
// the token from the environment.
func (i *insightsFlags) check() error {
	if i.url == "" {
		if i.repo != "" {
			return errors.New("-insights-repo needs -insights")
		}
		return nil
	}
	if _, err := url.ParseRequestURI(i.url); err != nil {
		return fmt.Errorf("-insights: %w", err)
	}
	i.token = strings.TrimSpace(os.Getenv(EnvInsightsToken))
	if i.token == "" {
		return fmt.Errorf("-insights needs a credential: set %s", EnvInsightsToken)
	}
	return nil
}

func (i *insightsFlags) repoName(ref vcs.Ref) string {
	switch {
	case i.repo != "":
		return i.repo
	case ref.Owner != "" && ref.Repo != "":
		return ref.Owner + "/" + ref.Repo
	}
	return os.Getenv("GITHUB_REPOSITORY")
}

// post sends the JSON report to the server. The review is already done and
// printed when this runs, so the caller treats a failure as a warning: losing a
// data point must not turn a clean review into a failed build.
func (i *insightsFlags) post(ctx context.Context, report []byte, ref vcs.Ref, at time.Time) error {
	timeout := i.timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	repo := i.repoName(ref)
	if repo == "" {
		return errors.New("no repository name: pass -insights-repo")
	}
	submission := struct {
		Repo   string          `json:"repo"`
		Ref    string          `json:"ref,omitempty"`
		Commit string          `json:"commit,omitempty"`
		At     time.Time       `json:"at"`
		Report json.RawMessage `json:"report"`
	}{Repo: repo, Commit: ref.Head, At: at.UTC(), Report: report}
	if ref.Number > 0 {
		submission.Ref = fmt.Sprintf("pull/%d", ref.Number)
	}
	body, err := json.Marshal(submission)
	if err != nil {
		return fmt.Errorf("encode the report: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(i.url, "/")+"/v1/reviews", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+i.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The error text of net/http names the URL, which holds no secret.
		return fmt.Errorf("insights: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("insights: the server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
