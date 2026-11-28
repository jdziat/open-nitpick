package upgrade

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func TestAnInterruptedTrustedRootFetchStopsAtOnceInsteadOfWaitingOnTheNetwork(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	f := &ctxFetcher{ctx: ctx, client: srv.Client()}
	done := make(chan error, 1)
	go func() {
		_, err := f.DownloadFile(srv.URL+"/root.json", 1<<20, 0)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the request reach the hanging server
	cancel()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want the cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ctrl-C did not stop the fetch: it kept waiting on the server")
	}
}

func TestAFetcherAnswersLikeTUFExpectsSoRootRotationStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("hello"))
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 100)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := &ctxFetcher{ctx: context.Background(), client: srv.Client()}

	if b, err := f.DownloadFile(srv.URL+"/ok", 10, 0); err != nil || string(b) != "hello" {
		t.Fatalf("a good file: %q %v", b, err)
	}
	// The TUF client looks for root.json versions until one is missing, and tells
	// that apart by this exact error type.
	var httpErr *metadata.ErrDownloadHTTP
	if _, err := f.DownloadFile(srv.URL+"/gone", 10, 0); !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("a 404 must be a *metadata.ErrDownloadHTTP with status 404, got %v", err)
	}
	if _, err := f.DownloadFile(srv.URL+"/big", 10, 0); !errors.Is(err, &metadata.ErrDownloadLengthMismatch{}) {
		t.Fatalf("a file over maxLength must be refused as a length mismatch, got %v", err)
	}
}

func TestACancelledContextRefusesTheFetchBeforeAnyRequest(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&ctxFetcher{ctx: ctx, client: srv.Client()}).DownloadFile(srv.URL, 10, 0)
	if !errors.Is(err, context.Canceled) || hits != 0 {
		t.Fatalf("err = %v, hits = %d: a cancelled command must not send a request", err, hits)
	}
}

func TestFetchTrustedRootGivesTheLibraryAFetcherBoundToTheCommandsContext(t *testing.T) {
	var got *tuf.Options
	old := fetchRoot
	defer func() { fetchRoot = old }()
	fetchRoot = func(opts *tuf.Options) (*root.TrustedRoot, error) {
		got = opts
		return nil, errors.New("stop here: no network in a test")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := FetchTrustedRoot(ctx); err == nil {
		t.Fatal("the stubbed fetch failed and the error was swallowed")
	}
	f, ok := got.Fetcher.(*ctxFetcher)
	if !ok {
		t.Fatalf("the library was given %T, not a fetcher bound to the context: Ctrl-C would wait on the network", got.Fetcher)
	}
	if f.ctx != ctx {
		t.Fatal("the fetcher holds a different context from the command's")
	}
}
