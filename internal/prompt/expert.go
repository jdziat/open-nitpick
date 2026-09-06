package prompt

import (
	"embed"
	"fmt"
	"strings"
	"unicode"

	"github.com/jdziat/open-nitpick/v2/internal/config"
)

// Expert prompts sit in their own directory, embedded like review.md and
// triage.md, so a specialist is a file of reviewable prose rather than a Go
// string literal — and so adding one is a new file rather than an edit to a
// switch statement.
//
//go:embed templates/experts/*.md
var expertTemplates embed.FS

// expertDir is where those prompts live inside expertTemplates.
const expertDir = "templates/experts"

// Expert is the specialist a finding is routed to for independent validation.
//
// It is deliberately narrow. The finding has already been reviewed by a
// generalist and survived triage, so a second generalist opinion is mostly the
// first opinion again — agreeing with yourself is not validation. What a
// specialist adds is the ability to say *why* a claim is wrong: that this
// driver binds the value, that this access is confined to one goroutine, that
// this identifier is public and authenticates nobody.
type Expert struct {
	// Key is the stable routing id. It appears in logs and tests and never
	// reaches a model.
	Key string

	// Name is the label a refuted finding is attributed to. Nothing is dropped
	// silently, so a reader sees "an application security engineer overruled
	// this" rather than watching a finding vanish.
	Name string

	// System is the expert's system prompt.
	System string
}

// Routing keys. They are stable because they are logged, and because a rename
// would orphan every route naming the old one.
const (
	keySQL         = "sql"
	keyAppSec      = "appsec"
	keySecrets     = "secrets"
	keyCrypto      = "crypto"
	keyAuthz       = "authz"
	keyConcurrency = "concurrency"
	keyResource    = "resource"
	keyDurability  = "durability"
	keyCorrectness = "correctness"
	keyAPI         = "api"
	keyTests       = "tests"
	keyDesign      = "design"
	keyStyle       = "style"
	keySlop        = "slop"
	keyGeneralist  = "generalist"
)

// expertRoster is every expert and the prompt it is built from.
//
// A slice rather than a map, so the roster has a reading order and so the
// loader can report which entry is broken.
var expertRoster = []struct{ key, name, file string }{
	{keySQL, "SQL and database expert", "sql.md"},
	{keyAppSec, "Application security engineer", "appsec.md"},
	{keySecrets, "Secret handling and rotation expert", "secrets.md"},
	{keyCrypto, "Applied cryptography engineer", "crypto.md"},
	{keyAuthz, "Authorization and access control engineer", "authz.md"},
	{keyConcurrency, "Concurrency and memory model expert", "concurrency.md"},
	{keyResource, "Resource lifetime and capacity engineer", "resource.md"},
	{keyDurability, "Data durability and migration expert", "durability.md"},
	{keyCorrectness, "Correctness and control flow reviewer", "correctness.md"},
	{keyAPI, "API compatibility expert", "api.md"},
	{keyTests, "Test design expert", "tests.md"},
	{keyDesign, "Software design and maintainability reviewer", "design.md"},
	{keyStyle, "Language idiom reviewer", "style.md"},
	{keySlop, "Generated-code reviewer", "slop.md"},
	{keyGeneralist, "Senior engineer", "generalist.md"},
}

// route sends a finding whose text carries any of these signals to one expert.
type route struct {
	expert  string
	signals []string
}

// decisiveRoutes are phrases that name a domain regardless of the class the
// finding was filed under.
//
// They run BEFORE the class because the class is model-authored and routinely
// wrong: a data race filed as correctness is the common case, and the expert
// who can refute it is the one who thinks in happens-before edges.
//
// The cost of letting text override the class is bounded only because every
// expert is told, in the shared validation contract, that being the wrong
// specialist refutes nothing and re-rates nothing. Without that clause a
// misroute is not a less informed opinion, it is a deletion: each prompt's
// refutation list is a set of domain-membership tests, and "this is not a
// credential" is a named reason. So a signal belongs here only if it names a
// domain rather than merely appearing in findings about one.
//
// Order is the routing rule, not decoration: the narrowest vocabulary comes
// first, so "SQL injection" reaches the SQL expert rather than the generic
// security engineer who would nod at the word "injection".
var decisiveRoutes = []route{
	// Concurrency vocabulary is used almost nowhere else, and these findings
	// are the ones most often filed under another class.
	{keyConcurrency, []string{
		"race", "races", "data race", "race condition", "racy",
		"happens before", "memory model", "memory visibility",
		"deadlock", "deadlocks", "livelock", "lock ordering", "lock order",
		"double checked locking", "check then act",
		"mutex", "rwmutex", "sync mutex", "critical section",
		"waitgroup", "sync waitgroup", "sync once", "errgroup",
		"goroutine", "goroutines", "concurrent map", "concurrent access",
		"thread safe", "thread safety", "not thread safe",
		"unsynchronized", "unsynchronised", "synchronization", "synchronisation",
		"compare and swap", "sync atomic", "atomic load", "atomic store",
		"torn read", "torn write", "lost wakeup",
	}},

	{keySQL, []string{
		"sql", "sqli", "sqlx", "sqlc", "orm", "gorm", "ent",
		"postgres", "postgresql", "mysql", "sqlite", "mariadb", "pgx",
		"prepared statement", "prepared statements",
		"parameterized", "parameterised", "parameterization", "parameterisation",
		"bind parameter", "bind parameters", "bind variable",
		"query builder", "where clause", "order by", "database driver",
		"second order injection",
	}},

	// Primitives only. The subject nouns a cryptography finding shares with
	// every other kind — "encrypted", "tls", "certificate" — are class-scoped
	// further down, because on their own they capture the finding rather than
	// describe it: "the encrypted archive is overwritten in place" is a
	// durability claim and "TLS connections are never closed" is a resource
	// claim, and neither has an anchor on this expert's severity scale.
	{keyCrypto, []string{
		"md5", "sha1", "sha256", "sha512", "des", "rc4", "ecb", "cbc", "gcm",
		"aes", "rsa", "ecdsa", "ed25519", "hmac", "x509", "cipher", "ciphertext",
		// signalText splits on punctuation, so the way these are usually
		// written — SHA-1, SHA-256, X.509 — arrives as separate tokens. A
		// signal that only matches the unpunctuated spelling is a route that
		// exists for half the findings that need it.
		"sha 1", "sha 256", "sha 512", "x 509",
		"nonce", "initialization vector", "initialisation vector",
		"bcrypt", "scrypt", "argon2", "argon2id", "pbkdf2", "key derivation",
		"constant time", "timing attack", "timing side channel",
		"math rand", "crypto rand", "insecure random", "predictable random",
		"insecureskipverify",
		"signature verification", "verify the signature",
	}},

	{keySecrets, []string{
		"credential", "credentials", "password", "passwords", "passphrase",
		"secret", "secrets", "api key", "api keys", "apikey", "access key",
		"secret key", "client secret", "private key", "service account key",
		"connection string",
		// A bearer token is a credential. Claims about *validating* one are
		// authorization's, and they say so in those words further down.
		"bearer token", "access token", "session token", "refresh token",
		// Qualified, because "hardcoded" on its own is ordinary English used by
		// findings of every class. A hardcoded timeout routed here met an
		// expert whose prompt offers "the value is a public identifier and
		// authenticates nothing" as a refutation — true of a timeout, and
		// nothing to do with the claim.
		"hardcoded credential", "hardcoded credentials", "hardcoded password",
		"hardcoded secret", "hardcoded api key", "hardcoded token",
		"hard coded credential", "hard coded password", "hard coded secret",
		"committed secret", "leaked secret",
		"key rotation", "secret rotation", "rotate the credential",
		"vault", "dotenv", "env file", "keychain", "keyring",
	}},

	{keyAppSec, []string{
		"path traversal", "directory traversal", "zip slip", "arbitrary file",
		"command injection", "shell injection", "argument injection",
		"xss", "cross site scripting", "html escaping", "template injection",
		"ssrf", "server side request forgery",
		"csrf", "cross site request forgery",
		"open redirect", "header injection", "response splitting", "crlf",
		"deserialization", "deserialisation", "unmarshalling untrusted",
		"xxe", "xml external entity", "attacker controlled", "untrusted input",
		// Bare "injection" lands here only because the SQL route ran first and
		// declined it; what is left is a shell, a template, or a header.
		"injection", "injected",
	}},

	{keyAuthz, []string{
		"authorization", "authorisation", "authz", "access control",
		"idor", "insecure direct object reference",
		"privilege escalation", "escalate privileges", "elevated privileges",
		"tenant isolation", "cross tenant", "multi tenant",
		"permission check", "missing permission", "unauthorized access",
		"authentication", "authn", "unauthenticated", "auth bypass",
		"session fixation", "jwt", "oauth",
		"token validation", "validate the token", "verify the token",
	}},

	{keyDurability, []string{
		"data loss", "data corruption", "lose data", "loses data", "lost data",
		"irrecoverable", "unrecoverable", "irreversible",
		"migration", "migrations", "drop column", "drop table", "truncate",
		"backfill", "schema change", "destructive", "overwrites the file",
		"partial write", "durability", "fsync",
	}},

	{keyResource, []string{
		"memory leak", "leaks memory", "goroutine leak", "connection leak",
		"file descriptor", "descriptor leak", "fd leak", "handle leak",
		"unbounded", "unbounded growth", "grows without bound", "never closed",
		"not closed", "missing close", "missing defer", "resource exhaustion",
		"connection pool", "out of memory",
	}},

	// Signals about the test itself, not about a gap in coverage. "untested",
	// "no tests", "missing test" and "test coverage" are how a reviewer
	// describes the RISK attached to a real defect — "the new branch is
	// untested, so a 500 storms the upstream" — and routing on them sent
	// correctness findings to an expert whose severity scale rates test gaps
	// ("rarely critical and rarely error"). A genuine test-design finding
	// carries class tests and reaches this expert through the class.
	{keyTests, []string{
		"asserts nothing", "does not assert", "tautological",
		"flaky", "flakiness", "table test", "table driven test",
	}},

	{keyAPI, []string{
		"breaking change", "breaks callers", "breaks existing callers",
		"backward compatibility", "backwards compatibility",
		"backward compatible", "backwards compatible", "backward incompatible",
		"semver", "semantic versioning", "public api", "exported api",
		"wire format", "api contract",
	}},
}

// classRoutes pick a specialist within a class, once no decisive phrase has
// claimed the finding. These signals are too ordinary to route on their own —
// "query" and "lock" mean different things in different classes — so they are
// only consulted where the class has already narrowed the meaning.
var classRoutes = map[config.Class][]route{
	config.ClassSecurity: {
		// Escaping and sanitization are not listed here: unqualified, they are
		// as much a shell or a template as a query, and appsec is the default
		// that catches them.
		{keySQL, []string{"query", "queries", "database", "statement", "table", "column"}},
		// The subject nouns the decisive crypto route cannot carry. Inside a
		// security finding "encrypted" and "certificate" mean what the
		// cryptographer thinks they mean; in a data-loss or resource finding
		// they are scenery.
		{keyCrypto, []string{"encrypt", "encrypted", "encryption", "decrypt",
			"decrypted", "decryption", "tls", "certificate", "certificates",
			"plaintext", "at rest"}},
		{keyAuthz, []string{"permission", "permissions", "role", "roles", "admin",
			"owner", "tenant", "session", "identity", "scope", "scopes", "login",
			// An identifier the client chose, naming a subject: who the caller
			// claims to be is this expert's first question.
			"user id", "account id", "customer id", "org id"}},
		// "key" on its own is not here: a cache key is not a credential, and
		// this route would otherwise claim every security finding that mentions
		// one.
		{keySecrets, []string{"secret", "secrets", "token", "tokens",
			"logged", "logging", "redact", "redacted"}},
	},

	config.ClassCorrectness: {
		{keyConcurrency, []string{"concurrent", "concurrently", "parallel", "lock",
			"locked", "locking", "channel", "channels", "shared state", "worker",
			"workers", "thread", "threads", "context cancellation"}},
		{keySQL, []string{"query", "queries", "database", "transaction", "rows"}},
		{keyDurability, []string{"persisted", "commit", "committed", "rollback",
			"disk", "flush", "flushed"}},
	},

	config.ClassConcurrency: {
		// Isolation and locking questions inside a database are the database
		// expert's, not the memory model's: no amount of happens-before
		// reasoning settles what SELECT FOR UPDATE takes.
		{keySQL, []string{"isolation level", "serializable", "phantom read",
			"select for update", "row lock", "table lock", "transaction"}},
	},

	config.ClassResource: {
		{keySQL, []string{"rows", "cursor", "transaction", "database", "statement"}},
		{keyConcurrency, []string{"channel", "channels", "waitgroup", "worker",
			"workers", "context", "ticker", "timer"}},
	},

	config.ClassContract: {
		{keyDurability, []string{"schema", "column", "migration", "stored data"}},
	},
}

// classExpert is the expert a class falls back to.
//
// Every class has one, including ClassUnknown, which is where an unrecognized
// or empty class lands. A finding that routed nowhere would either crash or
// skip validation, and skipping silently is exactly the failure this package is
// built to avoid: class.go and severity.go make the same call for the same
// reason.
var classExpert = map[config.Class]string{
	config.ClassCorrectness:     keyCorrectness,
	config.ClassConcurrency:     keyConcurrency,
	config.ClassSecurity:        keyAppSec,
	config.ClassResource:        keyResource,
	config.ClassDataLoss:        keyDurability,
	config.ClassContract:        keyAPI,
	config.ClassTests:           keyTests,
	config.ClassMaintainability: keyDesign,
	config.ClassStyle:           keyStyle,
	config.ClassSlop:            keySlop,
	config.ClassUnknown:         keyGeneralist,
}

// ExpertFor picks the expert that validates one finding.
//
// Routing has two levels. The class is the coarse route; the finding's own
// words pick the specialist within it, because "security" covers both a SQL
// injection and a leaked credential and the two are refuted by entirely
// different knowledge.
//
// It never returns a zero Expert. Every class resolves, including ClassUnknown
// and an empty class, and the roster is checked at startup so a mistyped route
// cannot produce an expert with no expertise.
func ExpertFor(class, title, rationale string) Expert {
	text := signalText(title + " " + rationale)

	for _, r := range decisiveRoutes {
		if r.matches(text) {
			return experts[r.expert]
		}
	}

	// Normalize so a model's synonym ("vulnerability", "race", "performance")
	// routes like the class it means, and anything unrecognized lands on
	// ClassUnknown rather than matching nothing.
	cls, _ := config.Class(class).Normalize()

	for _, r := range classRoutes[cls] {
		if r.matches(text) {
			return experts[r.expert]
		}
	}

	if key, ok := classExpert[cls]; ok {
		return experts[key]
	}

	// Unreachable while classExpert covers every class, which a test enforces.
	// It stays because the alternative to a backstop here is a zero Expert: a
	// class added to config and forgotten in this file would otherwise skip
	// validation for that whole class without saying anything.
	return experts[keyGeneralist]
}

// matches reports whether any of the route's signals appears in text as a whole
// word or phrase.
func (r route) matches(text string) bool {
	for _, s := range r.signals {
		if strings.Contains(text, " "+s+" ") {
			return true
		}
	}
	return false
}

// signalText reduces a finding's own words to lowercase tokens separated by
// single spaces, padded at both ends.
//
// Signals then match on word boundaries with a plain substring test. That is
// what stops "sql" from firing on "postgresql", and it lets a signal be written
// the way a person says it — "prepared statement", "db query" — instead of as a
// regexp. Every signal must already be in this form; a signal that is not can
// never match, which is a route that silently does not exist, so the tests
// check it.
func signalText(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte(' ')

	space := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}

	if !space {
		b.WriteByte(' ')
	}
	return b.String()
}

// experts is the roster, keyed by Expert.Key.
var experts = loadExperts()

// loadExperts reads every prompt and checks that the routing tables only name
// experts that exist.
//
// It panics on failure, which is the point: a missing prompt or a mistyped
// route key would otherwise yield an Expert with an empty system prompt — a
// specialist with no speciality, quietly judging findings in production. Both
// are edits to this file, so the panic is unreachable without a code change and
// the tests reach it first.
func loadExperts() map[string]Expert {
	out := make(map[string]Expert, len(expertRoster))

	for _, e := range expertRoster {
		raw, err := expertTemplates.ReadFile(expertDir + "/" + e.file)
		if err != nil {
			panic(fmt.Sprintf("expert %q: %v", e.key, err))
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			panic(fmt.Sprintf("expert %q: prompt %s is empty", e.key, e.file))
		}
		out[e.key] = Expert{Key: e.key, Name: e.name, System: text}
	}

	check := func(where, key string) {
		if _, ok := out[key]; !ok {
			panic(fmt.Sprintf("%s routes to unknown expert %q", where, key))
		}
	}
	for _, r := range decisiveRoutes {
		check("decisiveRoutes", r.expert)
	}
	for cls, rs := range classRoutes {
		for _, r := range rs {
			check("classRoutes["+string(cls)+"]", r.expert)
		}
	}
	for cls, key := range classExpert {
		check("classExpert["+string(cls)+"]", key)
	}

	return out
}
