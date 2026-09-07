package evals

import "github.com/jdziat/open-nitpick/internal/config"

// warningFixtures are the corpus's warning-level plants.
//
// Measured over AllFixtures() before these were written, the corpus planted 4
// critical, 8 error, 1 warning, 0 info and 1 nit across 15 fixtures. One plant
// was the entire resolution of every claim the reports made about this level:
// with a single warning, "the reviewer calibrates warnings" and "the reviewer
// got retry-no-backoff right" are the same sentence, and a reviewer that
// answers `error` to everything loses one plant out of fourteen for it.
//
// What would have been worthless is relabelling. Taking an existing error and
// dialling it down to fill this bucket produces a corpus that looks like
// evidence and is not: the plant's Why still describes a descriptor leak on
// every request, and the number beside it now says warning. Nothing here was
// moved. Every defect below is newly authored, and each is one a senior
// reviewer would rate warning on its own terms against the anchor the model is
// given: "`warning`, likely a bug, or a genuine hazard under
// plausible conditions."
//
// The property that decides this level, and the one every plant here is built
// around: NOTHING IS YET WRONG ON A NORMAL PATH. Each change below serves every
// request correctly today. The failure needs a condition that is plausible
// without being guaranteed, a cancelled context, a burst of traffic, a second
// user on the host, an attacker who can time a response. That is the whole
// distance to `error`, "a real bug that produces incorrect behavior on a
// reachable path", and the corpus already states it in multi-defect's own note:
// the descriptor leak there is an error because "every successful upload loses
// a descriptor, with no condition to be met". Remove the condition from any
// plant below and it becomes that; leave it and the plant is a hazard.
//
// The distance DOWNWARD is stated per plant too, because it is the easier one
// to get wrong. `info` is "a defensible concern the author should consciously
// accept or reject": a design choice with a cost the author may knowingly
// accept and no failing input to point at. Every plant here names a mechanism
// AND a failing input, so none of them is a matter of taste the author may
// decline.
//
// That argument deliberately does not quote the ladder's current info examples,
// which is a repair rather than a style choice. This comment used to name them
// , "widening an exported type, adding a dependency", and those two sentences
// were deleted from review.md for naming two plants; the quotation outlived
// them because it sat in an em-dash aside rather than in double quotes, where
// the sweep that fixed every other stale reference was looking. A comment keyed
// to prompt prose goes stale every time the prompt is edited, and the property
// this paragraph needs is a property of the LEVEL.
//
// TWO CLASSES, AND WHY NOT MORE. Every plant here is `security` or `resource`.
// That is not because warnings only occur there, the natural home for several
// warning-shaped defects, the anchor's own "retrying a non-idempotent request"
// among them, is `correctness`, but because
// TestSeverityIsConsistentWithinADefectClass requires a SeverityNote from EVERY
// member of a class that carries more than one severity, and correctness,
// concurrency, contract and data-loss are each planted in fixtures.go at a
// single level with no notes at all. A warning planted in correctness turns
// three green plants red in a file this change does not own; in the other three
// classes, one each. security and resource already carry two or three levels,
// every member already annotated, so a warning lands in them without reaching
// into anyone else's file. The cost is real and is recorded here rather than
// hidden: this level is now dominated by two classes, and a reviewer that
// learned "resource implies warning" would score better than it deserves.
// Closing that needs a correctness-class warning AND notes on the three
// correctness plants, in one change that owns both files.
//
// THIS FUNCTION IS NOT A CORPUS and nothing runs it as one. The five below are
// split across Fixtures() and HeldOutFixtures(), which name each of them
// directly; what this returns is the record of what was AUTHORED at this level,
// and TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus is what makes the two
// facts agree. Without it a fixture can be written, reviewed, merged and never
// wired into anything, passing every test in the tree while measuring nothing,
// which is the quietest way this corpus has to lose a plant.
//
// LANGUAGES. Eleven of the fifteen fixtures before these were Go, two Python,
// one TypeScript and one SQL migration, so a prompt tuned on that corpus can be
// Go-shaped without anyone noticing. Only one of the five below is Go. Two of
// the languages are new to the corpus: C# and shell.
//
// ONE MULTI-FILE FIXTURE, and what it does and does not exercise. Every other
// fixture in this corpus changes exactly one file, so the 6-file request cap,
// the 4-way concurrency and cross-batch triage have never been measured at all.
// ts-unbounded-memo-key changes SEVEN files. At the shipped
// max_files_per_request of 6 that is two batches, dispatched under the shipped
// concurrency of 4, whose findings are merged before triage, a path no fixture
// has ever taken. Be precise about the rest: the two files that carry the
// defect are deliberately in the SAME batch (git orders the diff by path, and
// cache.ts and search.ts are the first and sixth entries), because a defect
// split across batches would be one no reviewer could see, and a plant nothing
// can find scores as a prompt weakness forever. Cross-batch DEDUP is reached
// but not tested: nothing here reports the same defect twice, and a fixture
// that makes it do so is still owed.
func warningFixtures() []Fixture {
	return []Fixture{
		tsUnboundedMemoKeyFixture(),
		goCancelGoroutineLeakFixture(),
		pythonTimingUnsafeHMACFixture(),
		csharpClientPerRequestFixture(),
		bashFixedTempPathFixture(),
	}
}

// tsUnboundedMemoKeyFixture feeds a process-lifetime memo table with request
// text, across two files.
//
// This is the shape the corpus has never had: the defect is invisible from
// either file alone. src/search.ts reads as ordinary caching, a repeated
// search is answered from a table, and src/cache.ts reads as an ordinary memo
// helper that says what it is, "a table of things that do not change", and
// states its one requirement: keys must come from a set the caller can
// enumerate. Neither is wrong. The defect is the pair: search.ts keys the table
// by trimmed request text, which is not a set anyone can enumerate, so the
// table gains an entry for every distinct string anyone ever searches for and
// releases none of them.
//
// src/plans.ts is in the same change and is the control: it memoizes on
// "plan:" + tier, three values, exactly the use cache.ts documents. A reviewer
// that objects to cache.ts ITSELF has to explain plans.ts, and a reviewer that
// reads only search.ts has nothing to object to. The remaining four files are
// ordinary PR filler, deliberately dull, and they are what pushes the change
// past the six-file batch ceiling.
//
// BOTH CALLERS NAMESPACE THEIR KEYS ("plan:" and "q:") and they must keep
// doing so. The first draft of this fixture had search.ts call remember(q)
// with the raw query, which shares one process-global Map with plans.ts's
// "plan:" + tier, so searching the literal text "plan:free" returned the
// cached Plan and `hits.slice` threw, and searching first made
// planLimits("team").seats undefined. Both were reproduced by running the head
// files under node. That was a SECOND, unplanted, user-reachable defect in a
// fixture whose whole premise is that the only cross-file finding available is
// the unbounded table: a reviewer reporting the collision was charged a false
// positive, and if it used the word "key" it was credited with the memory
// plant it had never mentioned. Disjoint prefixes remove it and cost the plant
// nothing, "q:" + q is exactly as unenumerable as q, and they make the
// remaining defect purely about CARDINALITY, which is what it was always
// supposed to be about.
//
// THE FALSE POSITIVE THIS INVITES is the staleness objection: a table that is
// never refreshed serves a document's old title forever. It is a real remark
// about a different consequence, and a reviewer that makes only it has not
// noticed that the process dies. The second is a rate-limit objection, which
// reaches for "unbounded" about the REQUEST rate. Both are why the keywords are
// about memory and growth and about what the key space IS, and why "unbounded",
// "cache", "key" and "held for the lifetime", that last phrase being cache.ts's
// own words, are not among them.
func tsUnboundedMemoKeyFixture() Fixture {
	return Fixture{
		Name: "ts-unbounded-memo-key",
		Base: map[string]string{
			"src/search.ts": `import type { Hit } from "./types";

export type Search = (query: string) => Promise<Hit[]>;

/** searchHandler answers GET /search?q=... */
export function searchHandler(search: Search) {
  return async (req: SearchRequest, res: SearchResponse): Promise<void> => {
    const q = (req.query.q ?? "").trim();
    const hits = await search(q);

    res.json({ query: q, hits });
  };
}

type SearchRequest = { query: { q?: string } };
type SearchResponse = { json: (body: unknown) => void };
`,
			"src/types.ts": `/** Hit is one search result. */
export interface Hit {
  id: string;
  title: string;
}
`,
		},
		Head: map[string]string{
			"src/cache.ts": `type Entry = { value: unknown };

const table = new Map<string, Entry>();

/**
 * remember memoizes load() under key.
 *
 * Entries are held for the lifetime of the process: this is a table of things
 * that do not change, not a cache with a replacement policy. Keys must come
 * from a set the caller can enumerate, such as a route name or a plan tier.
 */
export async function remember<T>(key: string, load: () => Promise<T>): Promise<T> {
  const hit = table.get(key);
  if (hit) {
    return hit.value as T;
  }

  const value = await load();
  table.set(key, { value });
  return value;
}
`,
			"src/errors.ts": `/** UpstreamError is thrown when a dependency answers with a non-2xx status. */
export class UpstreamError extends Error {
  readonly status: number;

  constructor(status: number) {
    super("upstream answered " + status);
    this.name = "UpstreamError";
    this.status = status;
  }
}
`,
			"src/index.ts": `export { remember } from "./cache";
export { UpstreamError } from "./errors";
export { MAX_HITS } from "./limits";
export { planLimits } from "./plans";
export { searchHandler } from "./search";
export type { Hit, Plan } from "./types";
`,
			"src/limits.ts": `/** MAX_HITS is the most results one search answers with. */
export const MAX_HITS = 50;
`,
			"src/plans.ts": `import { remember } from "./cache";
import type { Plan } from "./types";

/** Tier is a billing tier. Adding one is a deploy, not a request. */
export type Tier = "free" | "team" | "enterprise";

/**
 * planLimits reads a tier's limits. There are three tiers, so the memo table
 * holds at most three entries.
 */
export function planLimits(tier: Tier, load: (t: Tier) => Promise<Plan>): Promise<Plan> {
  return remember("plan:" + tier, () => load(tier));
}
`,
			"src/search.ts": `import { remember } from "./cache";
import { MAX_HITS } from "./limits";
import type { Hit } from "./types";

export type Search = (query: string) => Promise<Hit[]>;

/** searchHandler answers GET /search?q=... */
export function searchHandler(search: Search) {
  return async (req: SearchRequest, res: SearchResponse): Promise<void> => {
    const q = (req.query.q ?? "").trim();

    // Repeated searches are common, so answer them from the table.
    const hits = await remember("q:" + q, () => search(q));

    res.json({ query: q, hits: hits.slice(0, MAX_HITS) });
  };
}

type SearchRequest = { query: { q?: string } };
type SearchResponse = { json: (body: unknown) => void };
`,
			"src/types.ts": `/** Hit is one search result. */
export interface Hit {
  id: string;
  title: string;
}

/** Plan is what a billing tier allows. */
export interface Plan {
  seats: number;
  retentionDays: number;
}
`,
		},
		Extra: map[string]string{
			"tsconfig.json": "{\n  \"compilerOptions\": {\n    \"target\": \"ES2022\",\n    \"strict\": true\n  }\n}\n",
		},
		Defects: []Defect{{
			Path: "src/search.ts",
			Line: 13, // the remember() call keyed by request text
			// Anchored at the CALL, not at the table. cache.ts is correct as
			// documented and plans.ts uses it correctly; the line that violates
			// its one requirement is this one, and it is where the one-line fix
			// goes. A reviewer that anchors in cache.ts is scored as a miss on
			// purpose: it has objected to the helper without noticing which
			// caller broke it.
			//
			// "memory" and the growth phrases carry the detection because every
			// statement of this defect reaches for one of them and none of the
			// objections this change invites does. "unbounded" is deliberately
			// absent: the rate-limit objection ("any client can issue an
			// unbounded number of searches") would have collected full recall
			// for noticing nothing about the table.
			//
			// The bare stem "grow" WAS here and had to go. It is a substring of
			// "grow stale", which is ordinary English for the staleness
			// objection this fixture names as the false positive it invites,
			// so the one stem undid the care taken to exclude "unbounded",
			// "cache", "key" and "held for the lifetime". The phrases that
			// replace it say what grows, which is the whole distinction between
			// a table that grows and results that grow stale.
			Keywords: []string{
				"memory", "grows with", "grows without", "growing table",
				"one entry per", "an entry for every", "an entry for each",
				"never released", "never freed",
				"distinct quer", "unique quer", "every search term",
				"distinct key", "unbounded distinct", "until oom",
				"arbitrary key", "user-controlled key", "user-controlled quer",
				"user-controlled search", "attacker-controlled",
			},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"likely a bug, or a genuine hazard under plausible conditions\", the " +
				"same clause and the same level as retry-no-backoff, which shares this class. Every response " +
				"the endpoint serves is correct, today and after a million searches; the failure needs enough " +
				"DISTINCT query strings to matter, which one crawler supplies and a quiet week does not. That " +
				"condition is what keeps it below \"a real bug that produces incorrect behavior on a reachable " +
				"path\" — no reachable path returns a wrong answer — and it is the difference from " +
				"multi-defect's descriptor leak, an error because every successful upload loses a descriptor " +
				"with no condition to be met. It is above info because nothing here is a matter of taste: one " +
				"retained entry per distinct string, released never, in a process expected to run for weeks.",
			Why: "search.ts keys a memo table that never releases an entry by trimmed request text, so the table gains an entry per distinct query and the process grows until it is killed",
		}},
	}
}

// goCancelGoroutineLeakFixture sends a lookup result on an unbuffered channel
// that nobody is left to receive.
//
// The added function is the standard shape for putting a context around a
// blocking call that has no context-aware form, and it is right in every
// respect but one: done is unbuffered. On the normal path the select takes the
// receive and the goroutine finishes. When ctx is done first, ResolveContext
// returns, nothing ever receives, and the send blocks for the life of the
// process, holding the goroutine, the connection Lookup is using, and whatever
// its result references. A one-character fix, make(chan result, 1), removes it.
//
// THE FALSE POSITIVE THIS INVITES is the design objection: "Directory.Lookup
// should take a context so the work can be cancelled". It is a fair
// remark and it is not this defect. It is about the upstream interface, and a
// reviewer making only it has not noticed that this goroutine never exits even
// after Lookup returns. The keywords therefore never mention context,
// cancellation or the interface: every one of them is a word only a reviewer
// reasoning about the CHANNEL would reach for, and "unbuffered" and "buffered"
// appear nowhere in the change, so neither can be earned by quoting it.
func goCancelGoroutineLeakFixture() Fixture {
	return Fixture{
		Name: "go-cancel-goroutine-leak",
		Base: map[string]string{
			"resolve.go": `package lookup

// Directory is the upstream name directory.
type Directory interface {
	Lookup(name string) (string, error)
}

// Resolve looks name up. It blocks until the directory answers.
func Resolve(d Directory, name string) (string, error) {
	return d.Lookup(name)
}
`,
		},
		Head: map[string]string{
			"resolve.go": `package lookup

import "context"

// Directory is the upstream name directory.
type Directory interface {
	Lookup(name string) (string, error)
}

// Resolve looks name up. It blocks until the directory answers.
func Resolve(d Directory, name string) (string, error) {
	return d.Lookup(name)
}

// result carries one Lookup outcome back to the select below.
type result struct {
	addr string
	err  error
}

// ResolveContext looks name up and gives up when ctx is done. Directory has no
// context-aware method, so the lookup runs in its own goroutine.
func ResolveContext(ctx context.Context, d Directory, name string) (string, error) {
	done := make(chan result)

	go func() {
		addr, err := d.Lookup(name)
		done <- result{addr: addr, err: err}
	}()

	select {
	case r := <-done:
		return r.addr, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
`,
		},
		Defects: []Defect{{
			Path: "resolve.go",
			Line: 24, // done := make(chan result)
			// The channel, not the send: this is the line the one-line fix
			// replaces, and review.md asks for the line the problem is on. The
			// send is four lines below, inside the scorer's tolerance, so a
			// reviewer that anchors there is still credited.
			Keywords: []string{
				"goroutine leak", "leaks a goroutine", "leaks the goroutine",
				"leaked goroutine", "leaking a goroutine", "goroutine is leaked",
				"blocks forever", "block forever", "blocked forever",
				"never receives", "no receiver", "nobody receives",
				"unbuffered", "buffered channel", "buffer of 1",
			},
			Class: config.ClassResource,
			// Classed by what is consumed, which is the convention
			// retry-no-backoff set for this class: the mechanism is a blocked
			// send and would sit as well in `concurrency`, but the reported
			// consequence is a goroutine that is never reclaimed, and `resource`
			// is "leaks and unbounded growth". Recording the tension rather than
			// picking silently, because Class is author-declared and nothing
			// scores against it.
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"a genuine hazard under plausible conditions\", alongside " +
				"retry-no-backoff in the same class and against the same clause. The leak needs ctx to be " +
				"done BEFORE the directory answers; on the normal path the receive happens and nothing is " +
				"left behind, which is exactly the condition multi-defect's descriptor leak does NOT need — " +
				"that note says so itself, \"every successful upload loses a descriptor, with no condition to " +
				"be met\", and that is why it is an error and this is not. It is not info either: the " +
				"cancellation path is the only reason this function was written, so the hazard is on the " +
				"feature's own reason for existing rather than a concern the author may decline.",
			Why: "done is unbuffered, so when ctx is done first nothing ever receives and the sending goroutine blocks for the life of the process",
		}},
	}
}

// pythonTimingUnsafeHMACFixture compares a webhook signature with ==.
//
// The signature is computed correctly: right secret, right body, right hash.
// Only the comparison is wrong. CPython's string equality returns as soon as
// two bytes differ, so how long verify() takes depends on how much of the
// signature a caller got right, and a caller that can measure that recovers a
// valid signature one hex digit at a time rather than searching 2^256.
//
// THE FALSE POSITIVE THIS INVITES is the module-level secret: SECRET is read at
// import, so a missing WEBHOOK_SECRET raises KeyError at import time rather
// than at first use. That is a real remark and a different one. The second is
// replay: verify says nothing about a timestamp, so a captured request can be
// sent again. Neither reaches for a word about TIME TAKEN, which is what every
// keyword here is about, and "compare_digest", the fix, appears nowhere in
// the change, so it cannot be typed by anyone who has not identified the
// defect. Bare "compare" is excluded on purpose: it would credit the wholly
// different objection that hex digests should be compared case-insensitively.
func pythonTimingUnsafeHMACFixture() Fixture {
	return Fixture{
		Name: "python-timing-unsafe-hmac",
		Base: map[string]string{
			"webhook.py": `import hashlib
import hmac
import os

SECRET = os.environ["WEBHOOK_SECRET"].encode()


def signature(body: bytes) -> str:
    """Return the hex signature we expect for body."""
    return hmac.new(SECRET, body, hashlib.sha256).hexdigest()
`,
		},
		Head: map[string]string{
			"webhook.py": `import hashlib
import hmac
import os

SECRET = os.environ["WEBHOOK_SECRET"].encode()


def signature(body: bytes) -> str:
    """Return the hex signature we expect for body."""
    return hmac.new(SECRET, body, hashlib.sha256).hexdigest()


def verify(body: bytes, provided: str) -> bool:
    """Report whether provided is the signature the sender should have sent."""
    return signature(body) == provided
`,
		},
		Extra: map[string]string{
			"pyproject.toml": "[project]\nname = \"webhook\"\nversion = \"0.1.0\"\n",
		},
		Defects: []Defect{{
			Path: "webhook.py",
			Line: 15, // the == comparison
			Keywords: []string{
				"constant-time", "constant time", "timing attack", "timing side",
				"side channel", "side-channel", "timing-safe", "timing safe",
				"compare_digest", "byte-by-byte", "byte by byte", "how long the comparison",
			},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"a genuine hazard under plausible conditions\", the lowest level in " +
				"this class and on the same reading its other notes use: what the DIFF demonstrates. Every " +
				"signature is accepted or rejected correctly, so there is no \"incorrect behavior on a " +
				"reachable path\" to make it an error, and the breach the class's criticals show is not here " +
				"either — recovering a signature needs an attacker who can measure a remote timing difference " +
				"across many requests, which calibration rule 1, \"rate the demonstrated consequence, not the " +
				"worst imaginable one\", declines to promote. Rule 2 takes the lower of what is left. Above " +
				"info because the mechanism and the one-line fix are both specific, not a concern the author " +
				"may simply accept.",
			Why: "the expected signature is compared with ==, which returns at the first differing byte, so response time leaks how much of a forged signature is correct",
		}},
	}
}

// csharpClientPerRequestFixture constructs and disposes an HttpClient per call.
//
// The change is a plausible refactor with a plausible reason. The field goes
// away, the class becomes stateless, and everything else about it is right:
// the method awaits before the using scopes close, so nothing is disposed out
// from under the request, and the response is disposed too. What it costs is
// invisible until there is traffic. Each HttpClient brings its own handler and
// connection pool, disposing one leaves its sockets in TIME_WAIT for minutes,
// and a service alerting steadily runs out of ephemeral ports.
//
// It is also the corpus's first C# file.
//
// THE FALSE POSITIVE THIS INVITES is the timeout objection: a reviewer that
// sees a bare new HttpClient often asks for an explicit timeout, and here that
// is close to wrong. The default is 100 seconds and the change did not alter
// it. The second is disposal ("EnsureSuccessStatusCode throws, so the response
// is leaked"), which the using already handles. Neither reaches for a word
// about SOCKETS or REUSE, which is what the keywords require, and none of those
// words appears in the change.
func csharpClientPerRequestFixture() Fixture {
	return Fixture{
		Name: "csharp-client-per-request",
		Base: map[string]string{
			"src/Notifier.cs": `using System.Net.Http;
using System.Threading.Tasks;

namespace Alerts;

/// <summary>Notifier posts alerts to the on-call webhook.</summary>
public sealed class Notifier
{
    private readonly HttpClient _http;

    public Notifier(HttpClient http) => _http = http;

    public async Task NotifyAsync(string message)
    {
        using var body = new StringContent(message);

        using var response = await _http.PostAsync("/alerts", body);
        response.EnsureSuccessStatusCode();
    }
}
`,
		},
		Head: map[string]string{
			"src/Notifier.cs": `using System;
using System.Net.Http;
using System.Threading.Tasks;

namespace Alerts;

/// <summary>Notifier posts alerts to the on-call webhook.</summary>
public sealed class Notifier
{
    private readonly Uri _baseAddress;

    public Notifier(Uri baseAddress) => _baseAddress = baseAddress;

    public async Task NotifyAsync(string message)
    {
        // One client per call keeps Notifier free of shared state.
        using var http = new HttpClient { BaseAddress = _baseAddress };
        using var body = new StringContent(message);

        using var response = await http.PostAsync("/alerts", body);
        response.EnsureSuccessStatusCode();
    }
}
`,
		},
		Extra: map[string]string{
			"src/Alerts.csproj": "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <TargetFramework>net8.0</TargetFramework>\n    <Nullable>enable</Nullable>\n  </PropertyGroup>\n</Project>\n",
		},
		Defects: []Defect{{
			Path: "src/Notifier.cs",
			Line: 17, // the per-call new HttpClient
			// The bare token "port" WAS here and had to go. mentionsAny matches
			// case-insensitive SUBSTRINGS, and "port" is inside "important",
			// "support" and "reports", three words a reviewer reaches for
			// without having noticed anything. It credited BOTH false positives
			// this fixture names: "it is important to set an explicit timeout"
			// and "if the webhook reports a non-2xx status". This is the same
			// failure Defect.Keywords already records fixing once, where
			// "subprocess" credited a comment about tar's exit status with
			// finding a command injection. The discriminating members were
			// always socket/exhaust/pool/reuse; the two-word forms below keep
			// the port vocabulary without the substring.
			Keywords: []string{
				"socket", "exhaust", "time_wait", "time wait",
				"connection pool", "ephemeral port", "port exhaustion",
				"out of ports", "reuse", "reused", "shared instance",
				"singleton", "static client", "httpclientfactory",
			},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"a genuine hazard under plausible conditions\", with the other two " +
				"hazards in this class. Every alert this sends is delivered correctly, so nothing meets " +
				"\"incorrect behavior on a reachable path\"; ports run out only under sustained alerting, " +
				"which is plausible on a busy day and absent on a quiet one. That condition is the whole " +
				"distance from multi-defect's descriptor leak, an error because it loses a descriptor on " +
				"every single request with no condition at all. Not info: the sockets outlive the process's " +
				"control of them for minutes, which is a named failure and not a design preference.",
			Why: "a new HttpClient is created and disposed per call, so its sockets sit in TIME_WAIT and a steady alert rate exhausts ephemeral ports",
		}},
	}
}

// bashFixedTempPathFixture writes a fetched payload to a fixed path in /tmp.
//
// The script needs the response twice, so it stops piping and saves a copy,
// the right instinct, spent on a path any other account on the host can create
// first. A pre-created symlink at that path turns the redirect into a write to
// whatever it points at, under this script's identity; a pre-created regular
// file that the attacker keeps writing turns the two jq reads into content this
// script did not fetch. On a shared CI runner both are ordinary. mktemp is the
// one-line fix and it costs nothing here, because the path never leaves the
// script.
//
// It is also the corpus's first shell file.
//
// THE FALSE POSITIVE THIS INVITES is cleanup: "nothing removes the file; add a
// trap". True, weaker, and about a different line's worth of consequence. The
// second is collision, two runs of the script clobbering each other, which is
// a real hazard from the same fixed path but a different mechanism, and the
// keywords admit it only when it arrives with the fix ("mktemp") or the
// attacker ("another user", "symlink") attached. That boundary is deliberate:
// this plant is about who else can reach the path, not about how tidy the
// script is.
func bashFixedTempPathFixture() Fixture {
	return Fixture{
		Name: "bash-fixed-temp-path",
		Base: map[string]string{
			"scripts/release-notes.sh": `#!/usr/bin/env bash
# Print the body of the latest release.
set -euo pipefail

API=${API:-https://api.example.com}

curl -sSf "$API/releases/latest" | jq -r '.body'
`,
		},
		Head: map[string]string{
			"scripts/release-notes.sh": `#!/usr/bin/env bash
# Print the body and the tag of the latest release.
set -euo pipefail

API=${API:-https://api.example.com}

# One fetch, two reads.
OUT=/tmp/release-notes.json
curl -sSf "$API/releases/latest" > "$OUT"

jq -r '.body' "$OUT"
jq -r '.tag_name' "$OUT"
`,
		},
		Defects: []Defect{{
			Path: "scripts/release-notes.sh",
			Line: 8, // OUT=/tmp/release-notes.json
			// The path, not the redirect: choosing the name is the defect and
			// OUT=$(mktemp) is the single-line replacement. The redirect one
			// line below is inside the scorer's tolerance either way.
			// "mktemp" WAS here and had to go. It is the fix, and it is the fix
			// for the WRONG finding too: "nothing removes the file, use
			// OUT=$(mktemp) and trap rm EXIT" is the cleanup objection this
			// fixture names as the false positive it invites, and it arrives
			// carrying the word. The collision argument below was written about
			// the collision objection and never covered that one. What is left
			// is consequence-shaped, which is what Defect.Keywords asks for: a
			// finding that only says "use mktemp" has named no consequence and
			// does not clear the bar review.md sets for reporting at all.
			Keywords: []string{
				"symlink", "predictable", "world-writable", "world writable",
				"any local user", "another user", "other users", "unprivileged user",
				"hijack", "toctou", "insecure temporary", "pre-create", "pre-created",
			},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"a genuine hazard under plausible conditions\", below the " +
				"error-level injection plants and far below the class's criticals. Nothing is wrong when the " +
				"script runs alone, which is most of the time; the failure needs a second account on the host " +
				"that gets there first, and calibration rule 1 refuses to rate it as the breach it would be " +
				"if that were shown — the attacker must already be able to run code on the runner. It is not " +
				"info, whose examples are design choices with no named failure: this one has an attacker, a " +
				"mechanism and a one-word fix.",
			Why: "the payload is written to a fixed path in a world-writable directory, so another account on the host can pre-create it as a symlink and redirect the write, or feed the two reads content this script never fetched",
		}},
	}
}
