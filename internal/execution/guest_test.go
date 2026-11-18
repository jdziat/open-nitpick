package execution

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
)

func TestGuestRunsOnlyThePinnedProfileAfterDependencyPreflight(t *testing.T) {
	var calls [][]string
	code := runGuest(t.Context(), fixtureSnapshot(t), Check{Kind: "go_test", Package: "internal/http"}, t.TempDir(), io.Discard, func(_ context.Context, args []string, _ string, out io.Writer) error {
		calls = append(calls, slices.Clone(args))
		if len(calls) == 2 {
			_, err := io.WriteString(out, "{\"Dir\":\"/work\"}\n")
			return err
		}
		_, err := io.WriteString(out, "{\"Action\":\"pass\",\"Test\":\"TestControl\"}\n")
		return err
	})
	expected := [][]string{{"list", "-m", "all"}, {"list", "-e", "-json", "-deps", "-test", "./internal/http"}, {"test", "-json", "-count=1", "./internal/http"}}
	if code != 0 || !slices.EqualFunc(calls, expected, slices.Equal[[]string]) {
		t.Fatalf("unexpected sequence: code=%d %v", code, calls)
	}
}

func TestGuestRequiresTestsToRunBeforeClaimingChecksPassed(t *testing.T) {
	for _, output := range []string{"", "{\"Action\":\"pass\",\"Package\":\"empty\"}\n", "{\"Action\":\"skip\",\"Test\":\"TestSkipped\"}\n", "{\"Action\":\"output\",\"Output\":\"{\\\"Action\\\":\\\"pass\\\",\\\"Test\\\":\\\"TestForged\\\"}\"}\n"} {
		code := runGuest(t.Context(), fixtureSnapshot(t), Check{Kind: "go_test", Package: "."}, t.TempDir(), io.Discard, func(_ context.Context, args []string, _ string, out io.Writer) error {
			if args[0] == "list" && args[1] == "-e" {
				_, err := io.WriteString(out, "{\"Dir\":\"/work\"}\n")
				return err
			}
			_, err := io.WriteString(out, output)
			return err
		})
		if code != 75 {
			t.Fatalf("no test ran but code=%d for %q", code, output)
		}
	}
}

func TestGuestSeparatesUnavailableDependenciesFromFailingChecks(t *testing.T) {
	for _, failAt := range []int{1, 2, 3} {
		calls := 0
		code := runGuest(t.Context(), fixtureSnapshot(t), Check{Kind: "go_test", Package: "."}, t.TempDir(), io.Discard, func(_ context.Context, _ []string, _ string, out io.Writer) error {
			calls++
			if calls == failAt {
				return errors.New("fixture failure")
			}
			if calls == 2 {
				_, err := io.WriteString(out, "{\"Dir\":\"/work\"}\n")
				return err
			}
			return nil
		})
		want := 75
		if failAt == 3 {
			want = 1
		}
		if code != want {
			t.Fatalf("failure at step %d gave %d, want %d", failAt, code, want)
		}
	}
}

func TestGuestTreatsUnstagedImportsAsUnavailableEvidence(t *testing.T) {
	code := runGuest(t.Context(), fixtureSnapshot(t), Check{Kind: "go_build", Package: "."}, t.TempDir(), io.Discard, func(_ context.Context, args []string, _ string, out io.Writer) error {
		if args[0] == "list" && args[1] == "-e" {
			_, err := io.WriteString(out, "{\"ImportPath\":\"private.example/missing\",\"Error\":{\"Err\":\"unavailable\"}}\n")
			return err
		}
		if args[0] == "build" {
			t.Fatal("build ran with missing dependencies")
		}
		return nil
	})
	if code != 75 {
		t.Fatalf("missing import reported as check outcome %d", code)
	}
}
