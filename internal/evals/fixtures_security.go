package evals

import "github.com/jdziat/open-nitpick/internal/config"

// goIDORWrongPrincipalFixture: the handler checks that a session exists, then
// loads whatever ?id= names. Authz looks present; the principal is wrong.
//
// THE FALSE POSITIVE this INVITES is "add authentication" — the session check
// already proves a caller is signed in. Keywords require the reviewer to name
// the ownership mismatch, not the absence of a login gate. Bare "idor" is
// deliberately absent: it fires inside "corridor" (see groundtruth_test).
func goIDORWrongPrincipalFixture() Fixture {
	return Fixture{
		Name: "go-idor-wrong-principal",
		Base: map[string]string{
			"project.go": `package api

import (
	"context"
	"errors"
	"net/http"
)

// ErrUnauthorized is returned when there is no signed-in caller.
var ErrUnauthorized = errors.New("unauthorized")

// Store persists projects.
type Store interface {
	Project(ctx context.Context, id string) (Project, error)
}

// Project is a user's project.
type Project struct {
	ID      string
	OwnerID string
	Name    string
}

// Session is the signed-in caller.
type Session struct {
	UserID string
}

// Handler serves project reads.
type Handler struct{ store Store }

// GetProject returns the caller's project by id.
func (h *Handler) GetProject(ctx context.Context, s Session, projectID string) (Project, error) {
	if s.UserID == "" {
		return Project{}, ErrUnauthorized
	}
	p, err := h.store.Project(ctx, projectID)
	if err != nil {
		return Project{}, err
	}
	if p.OwnerID != s.UserID {
		return Project{}, ErrUnauthorized
	}
	return p, nil
}

// ServeHTTP is the HTTP entry for GET /projects?id=.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, s Session) {
	id := r.URL.Query().Get("id")
	p, err := h.GetProject(r.Context(), s, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	_, _ = w.Write([]byte(p.Name))
}
`,
		},
		Head: map[string]string{
			"project.go": `package api

import (
	"context"
	"errors"
	"net/http"
)

// ErrUnauthorized is returned when there is no signed-in caller.
var ErrUnauthorized = errors.New("unauthorized")

// Store persists projects.
type Store interface {
	Project(ctx context.Context, id string) (Project, error)
}

// Project is a user's project.
type Project struct {
	ID      string
	OwnerID string
	Name    string
}

// Session is the signed-in caller.
type Session struct {
	UserID string
}

// Handler serves project reads.
type Handler struct{ store Store }

// GetProject returns a project by id for any signed-in caller.
//
// The ownership read was removed: the store already 404s unknown ids, and
// every request already carries a session.
func (h *Handler) GetProject(ctx context.Context, s Session, projectID string) (Project, error) {
	if s.UserID == "" {
		return Project{}, ErrUnauthorized
	}
	return h.store.Project(ctx, projectID)
}

// ServeHTTP is the HTTP entry for GET /projects?id=.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, s Session) {
	id := r.URL.Query().Get("id")
	p, err := h.GetProject(r.Context(), s, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	_, _ = w.Write([]byte(p.Name))
}
`,
		},
		Defects: []Defect{{
			Path: "project.go",
			Line: 40, // return h.store.Project — no OwnerID check
			Keywords: []string{
				"wrong principal", "any project", "any user's", "another user's",
				"another project's", "does not check ownership", "without checking ownership",
				"ownership check", "horizontal privilege",
				"authorization bypass", "access other users", "other users' projects",
				"query parameter id", "loads by id", "trusts the id",
				"reads that project", "supplies another",
			},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityCritical,
			SeverityNote: "critical under \"a security breach\": any signed-in caller who can guess " +
				"an id reads another user's project. Above injection plants that need an untrusted " +
				"input the diff does not show, because the query id IS the demonstrated input. Same " +
				"band as removed-guard: the ownership check is what was deleted.",
			Why: "any signed-in user who supplies another project's id in ?id= reads that project because GetProject no longer checks ownership after confirming a session exists",
		}},
	}
}

// phpClean404OnForbiddenFixture is the silence twin of php-forbidden-vs-404:
// membership failure returns 404 by documented design, so the correct security
// review is silence. A model that always flags "use 403" becomes measurable NOISE.
func phpClean404OnForbiddenFixture() Fixture {
	return Fixture{
		Name: "php-clean-404-on-forbidden",
		Base: map[string]string{
			"src/Http/ProjectController.php": `<?php

declare(strict_types=1);

namespace App\Http;

/** ProjectController answers the project endpoints. */
final class ProjectController
{
    public function __construct(private ProjectRepository $projects)
    {
    }

    /** GET /projects/{id}, for the signed-in viewer. */
    public function show(string $projectId, string $viewerId): Response
    {
        $project = $this->projects->find($projectId);
        if ($project === null) {
            return new Response(404, ['error' => 'not_found']);
        }

        return new Response(200, $project->toArray());
    }
}
`,
		},
		Head: map[string]string{
			"src/Http/ProjectController.php": `<?php

declare(strict_types=1);

namespace App\Http;

/**
 * ProjectController answers the project endpoints.
 *
 * Non-members MUST receive the same 404 as a missing id so project existence
 * is not disclosed. Do not return 403 from show().
 */
final class ProjectController
{
    public function __construct(private ProjectRepository $projects)
    {
    }

    /** GET /projects/{id}, for the signed-in viewer. */
    public function show(string $projectId, string $viewerId): Response
    {
        $project = $this->projects->find($projectId);
        if ($project === null) {
            return new Response(404, ['error' => 'not_found']);
        }

        if (!$this->projects->isMember($projectId, $viewerId)) {
            return new Response(404, ['error' => 'not_found']);
        }

        return new Response(200, $project->toArray());
    }
}
`,
		},
		// Clean: correct review under the security persona is silence.
	}
}

// pythonHMACUnboundCompareFixture: verify uses compare_digest (looks correct)
// but the MAC covers only the body. The untouched contract requires binding
// the X-Timestamp header into the signed payload.
func pythonHMACUnboundCompareFixture() Fixture {
	contract := `"""Webhook authentication contract.

Every signed request carries an X-Timestamp header (unix seconds) and an
X-Signature hex HMAC. The MAC MUST be computed over the concatenation

    timestamp + "." + body

so a captured body cannot be replayed under a fresh timestamp. verify() is
responsible for that binding; callers only pass the raw header values through.
"""
`
	return Fixture{
		Name: "python-hmac-unbound-compare",
		Base: map[string]string{
			"app/__init__.py": "",
			"app/contract.py": contract,
			"app/webhook.py": `import hashlib
import hmac
import os

import app.contract  # binding rules for signed requests

SECRET = os.environ["WEBHOOK_SECRET"].encode()


def signature(body: bytes) -> str:
    """Return the hex signature for body alone (legacy)."""
    return hmac.new(SECRET, body, hashlib.sha256).hexdigest()
`,
		},
		Head: map[string]string{
			"app/__init__.py": "",
			"app/contract.py": contract,
			"app/webhook.py": `import hashlib
import hmac
import os

import app.contract  # binding rules for signed requests

SECRET = os.environ["WEBHOOK_SECRET"].encode()


def signature(body: bytes) -> str:
    """Return the hex signature for body alone (legacy)."""
    return hmac.new(SECRET, body, hashlib.sha256).hexdigest()


def verify(body: bytes, provided: str, ts: str) -> bool:
    """Report whether provided matches the signature for this request.

    ts is accepted for API compatibility with app.contract; production senders
    still emit the legacy MAC that covers only the request body.
    """
    _ = ts
    expected = signature(body)
    return hmac.compare_digest(expected, provided)
`,
		},
		Defects: []Defect{{
			Path: "app/webhook.py",
			Line: 23, // return hmac.compare_digest
			Keywords: []string{
				"not bound", "unbound", "binding", "anti-replay",
				"does not include", "ignores ts", "unused ts", "replay",
				"mac over body", "signed payload", "concatenat",
				"dot-separated", "contract requires",
			},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityError,
			SeverityNote: "error: compare_digest makes the comparison timing-safe, so the timing plant's " +
				"warning does not apply — the bug is that the contract's required timestamp binding is " +
				"ignored, so a captured body verifies forever under any timestamp. Below critical because " +
				"the attacker already holds a previously signed body (calibration rule 1).",
			Why: "verify uses compare_digest on a MAC that never mixes ts into the signed payload, so the contract's anti-replay binding is absent despite the secure-looking comparison",
		}},
	}
}

func pythonHMACBoundCleanFixture() Fixture {
	contract := `"""Webhook authentication contract.

Every signed request carries an X-Timestamp header (unix seconds) and an
X-Signature hex HMAC. The MAC MUST be computed over the concatenation

    timestamp + "." + body

so a captured body cannot be replayed under a fresh timestamp. verify() is
responsible for that binding; callers only pass the raw header values through.
"""
`
	return Fixture{
		Name: "python-hmac-bound-clean",
		Base: map[string]string{
			"app/__init__.py": "",
			"app/contract.py": contract,
			"app/webhook.py": `import hashlib
import hmac
import os

import app.contract  # binding rules for signed requests

SECRET = os.environ["WEBHOOK_SECRET"].encode()


def signature(body: bytes) -> str:
    """Return the hex signature for body alone (legacy)."""
    return hmac.new(SECRET, body, hashlib.sha256).hexdigest()
`,
		},
		Head: map[string]string{
			"app/__init__.py": "",
			"app/contract.py": contract,
			"app/webhook.py": `import hashlib
import hmac
import os

import app.contract  # binding rules for signed requests

SECRET = os.environ["WEBHOOK_SECRET"].encode()


def signature(payload: bytes) -> str:
    """Return the hex HMAC for the signed payload."""
    return hmac.new(SECRET, payload, hashlib.sha256).hexdigest()


def verify(body: bytes, provided: str, ts: str) -> bool:
    """Report whether provided matches the contract-bound signature."""
    payload = ts.encode() + b"." + body
    expected = signature(payload)
    return hmac.compare_digest(expected, provided)
`,
		},
	}
}

// securityBakeOffNames is the make eval-security set, in report order.
// Spent global held-out fixtures are in this list on purpose; they are tuning
// for the security persona and must not be labeled a generalization spend.
func securityBakeOffNames() []string {
	return []string{
		"go-sql-injection", "go-hardcoded-secret", "python-command-injection",
		"python-timing-unsafe-hmac", "python-secret-to-audit-log", "multi-defect",
		"bash-fixed-temp-path", "removed-guard", "php-forbidden-vs-404",
		"go-idor-wrong-principal",
		"clean-refactor", "style-only", "clean-sql-allowlist", "php-clean-404-on-forbidden",
	}
}

func securityHeldOutNames() []string {
	names := make([]string, 0, len(SecurityHeldOutFixtures()))
	for _, f := range SecurityHeldOutFixtures() {
		names = append(names, f.Name)
	}
	return names
}

// fixtureSetMatches reports whether fixtures is exactly the named set, ignoring
// order. A subset is not a match: a shorter run must not inherit the label of
// the corpus it sampled.
func fixtureSetMatches(fixtures []Fixture, names []string) bool {
	if len(fixtures) != len(names) {
		return false
	}
	want := make(map[string]int, len(names))
	for _, n := range names {
		want[n]++
	}
	for _, f := range fixtures {
		want[f.Name]--
		if want[f.Name] < 0 {
			return false
		}
	}
	for _, n := range want {
		if n != 0 {
			return false
		}
	}
	return true
}

// securityCorpusToken is the dump-name token for a security-persona battery,
// or "" when the selection is not that battery. Global HeldOut membership
// would otherwise call the tuning set mixed, because three spent fixtures
// still live in HeldOutFixtures.
func securityCorpusToken(fixtures []Fixture) string {
	switch {
	case fixtureSetMatches(fixtures, securityBakeOffNames()):
		return "security"
	case fixtureSetMatches(fixtures, securityHeldOutNames()):
		return "security-heldout"
	default:
		return ""
	}
}

// SecurityTuningFixtures are security-persona plants and silence twins that
// live outside Fixtures()/HeldOutFixtures so the AllFixtures severity census
// stays stable. Selected by make eval-security via EveryFixture name lookup.
func SecurityTuningFixtures() []Fixture {
	return []Fixture{
		goIDORWrongPrincipalFixture(),
		phpClean404OnForbiddenFixture(),
	}
}

// SecurityHeldOutFixtures is the security-persona held-out set: never mixed into
// SECURITY tuning, spent once via make eval-security-heldout. Distinct from
// HeldOutFixtures() / HELD_OUT (global prompt held-out).
func SecurityHeldOutFixtures() []Fixture {
	return []Fixture{
		pythonExpiredTokenAcceptedFixture(),
		pythonHMACUnboundCompareFixture(),
		pythonHMACBoundCleanFixture(),
	}
}
