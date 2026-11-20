#!/usr/bin/env python3
"""Build a review corpus from pull requests neither tool has seen.

Golden comments are real human review threads that a later commit answered: the thread's first
comment is by a person other than the pull request's author, and GitHub marks the thread
outdated because the lines it sat on changed. The reviewed state is the commit that carries the
most such threads, and its diff against the pull request's base is what a reviewer is given.

    python3 tools/bakeoff/fresh_corpus.py mine  --out /tmp/bakeoff/fresh/candidates.json
    python3 tools/bakeoff/fresh_corpus.py build --candidates /tmp/bakeoff/fresh/candidates.json \
        --out /tmp/bakeoff/fresh

`mine` needs `gh` signed in. `build` also needs OPENROUTER_API_KEY, for the model that gives each
golden comment a category, and writes the layout the Mira harness, build_repos.py and the upstream
scorer read: `inputs/` with a `selection.json`, and `bench-<sha>/offline/` with the golden data.

Selection is by rule and never by content: eligible pull requests are ordered by the SHA-256 of
`owner/repo#number`, and the first six per language are taken, at most two per repository.
"""

import argparse
import collections
import datetime as dt
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request
from pathlib import Path

LANGUAGES = ["TypeScript", "Python", "Go", "Java", "Ruby"]
SHORT = {"TypeScript": "ts", "Python": "py", "Go": "go", "Java": "java", "Ruby": "rb"}
MARTIAN = {"calcom/cal.com", "discourse/discourse", "grafana/grafana", "keycloak/keycloak", "getsentry/sentry"}
CORE = {"bug", "security", "concurrency", "data", "api", "perf", "test_gap", "doc_defect"}
CATEGORIES = CORE | {"style", "speculative"}
SEVERITIES = {"Low", "Medium", "High", "Critical"}
CLASSIFIER = "anthropic/claude-sonnet-5.5"
MIN_STARS = 200
MIN_FILES, MAX_FILES = 2, 40
MIN_LINES, MAX_LINES = 40, 1500
MIN_COMMENT = 30
MAX_COMMENT = 1500
PER_LANGUAGE, PER_REPO = 6, 2
MAX_ARCHIVE = 300 * 1024 * 1024

QUERY = """
query($q: String!, $first: Int!, $after: String) {
  rateLimit { cost remaining resetAt }
  search(query: $q, type: ISSUE, first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        number url title mergedAt additions deletions changedFiles baseRefOid headRefOid
        author { login __typename }
        repository { nameWithOwner stargazerCount primaryLanguage { name } }
        reviewThreads(first: 20) {
          nodes {
            isOutdated path originalLine
            comments(first: 1) {
              nodes { author { login __typename } body diffHunk createdAt originalCommit { oid } }
            }
          }
        }
      }
    }
  }
}
"""


def gh_graphql(variables):
    args = ["gh", "api", "graphql", "-f", f"query={QUERY}"]
    for key, value in variables.items():
        if value is None:
            continue
        args += ["-F" if isinstance(value, int) else "-f", f"{key}={value}"]
    for attempt in range(6):
        proc = subprocess.run(args, capture_output=True, text=True)
        if proc.returncode == 0:
            body = json.loads(proc.stdout)
            if "errors" not in body:
                return body["data"]
            message = json.dumps(body["errors"])[:300]
        else:
            message = proc.stderr[:300]
        if attempt < 5:
            # Rate limits, gateway errors and truncated responses all clear on their own.
            print(f"graphql attempt {attempt + 1} failed ({message[:40].strip()!r}); waiting", flush=True)
            time.sleep(15 * (attempt + 1))
            continue
        raise SystemExit(f"graphql failed: {message}")
    raise SystemExit("graphql kept failing")


def threads_of(pr):
    """The review threads that qualify as golden, grouped by the commit they were made on."""
    author = (pr.get("author") or {}).get("login")
    by_commit = collections.defaultdict(list)
    for thread in pr["reviewThreads"]["nodes"]:
        if not thread["isOutdated"] or not thread["path"] or not thread["comments"]["nodes"]:
            continue
        first = thread["comments"]["nodes"][0]
        who = first.get("author") or {}
        body = (first.get("body") or "").strip()
        commit = (first.get("originalCommit") or {}).get("oid")
        if who.get("__typename") != "User" or who.get("login") == author or len(body) < MIN_COMMENT or not commit:
            continue
        by_commit[commit].append(
            {
                "path": thread["path"],
                "line": thread["originalLine"],
                "comment": body[:MAX_COMMENT],
                "diff_hunk": (first.get("diffHunk") or "")[-1500:],
                "reviewer": who.get("login"),
                "created_at": first["createdAt"],
                "commit": commit,
            }
        )
    return by_commit


def reviewed_state(by_commit):
    """The commit with the most golden threads; ties go to the earliest."""
    return sorted(by_commit, key=lambda c: (-len(by_commit[c]), min(t["created_at"] for t in by_commit[c])))[0]


def eligible(pr):
    """The candidate record for a pull request, or None when a mechanical rule rejects it."""
    repo = pr["repository"]
    language = (repo.get("primaryLanguage") or {}).get("name")
    author = pr.get("author") or {}
    if (
        language not in LANGUAGES
        or repo["nameWithOwner"].lower() in MARTIAN
        or repo["stargazerCount"] < MIN_STARS
        or author.get("__typename") != "User"
        or not MIN_FILES <= pr["changedFiles"] <= MAX_FILES
        or not MIN_LINES <= pr["additions"] + pr["deletions"] <= MAX_LINES
    ):
        return None
    by_commit = threads_of(pr)
    if not by_commit:
        return None
    commit = reviewed_state(by_commit)
    return {
        "repo": repo["nameWithOwner"],
        "stars": repo["stargazerCount"],
        "language": language,
        "number": pr["number"],
        "url": pr["url"],
        "title": pr["title"],
        "merged_at": pr["mergedAt"],
        "base_oid": pr["baseRefOid"],
        "head_oid": pr["headRefOid"],
        "reviewed_commit": commit,
        "threads": by_commit[commit],
    }


def mine(args):
    start = dt.date.fromisoformat(args.start)
    end = dt.date.fromisoformat(args.end)
    out = {}
    if args.out.exists():
        out = {c["url"]: c for c in json.loads(args.out.read_text())["candidates"]}
    seen = scanned = 0
    day = start
    while day <= end:
        window_end = min(day + dt.timedelta(days=args.window - 1), end)
        for language in LANGUAGES:
            query = (
                f"is:pr is:merged language:{language} merged:{day}..{window_end} "
                f"comments:{args.min_comments}..{args.max_comments} sort:comments-desc"
            )
            after = None
            for _ in range(args.pages):
                data = gh_graphql({"q": query, "first": 25, "after": after})
                scanned += len(data["search"]["nodes"])
                for pr in data["search"]["nodes"]:
                    if not pr:
                        continue
                    seen += 1
                    candidate = eligible(pr)
                    if candidate:
                        out[candidate["url"]] = candidate
                left = data["rateLimit"]
                if left["remaining"] < 400:
                    # The quota is per hour; wait for it to refill rather than fail a query.
                    reset = dt.datetime.fromisoformat(left["resetAt"].replace("Z", "+00:00"))
                    wait = max(30.0, (reset - dt.datetime.now(dt.timezone.utc)).total_seconds() + 5)
                    print(f"{left['remaining']} points left; waiting {wait:.0f}s for the quota", flush=True)
                    time.sleep(wait)
                info = data["search"]["pageInfo"]
                if not info["hasNextPage"]:
                    break
                after = info["endCursor"]
            print(f"{day}..{window_end} {language}: {len(out)} candidates so far, {scanned} scanned, "
                  f"{data['rateLimit']['remaining']} points left", flush=True)
            args.out.parent.mkdir(parents=True, exist_ok=True)
            args.out.write_text(json.dumps({"query_window": [args.start, args.end], "candidates": list(out.values())}, indent=1))
        day = window_end + dt.timedelta(days=1)
    print(f"{len(out)} candidates from {scanned} pull requests scanned", flush=True)


def order_key(candidate):
    return hashlib.sha256(f"{candidate['repo']}#{candidate['number']}".encode()).hexdigest()


CLASSIFY_SYSTEM = (
    "You label code review comments for a benchmark. For each numbered review comment, read the comment "
    "and the diff hunk it was left on, and give it one category and a severity. Judge only from what is "
    "shown; add nothing. Categories: bug (wrong behaviour, a crash, a wrong result, a regression), "
    "security (a vulnerability, an authorization gap, injection, an exposed secret), concurrency (a race, "
    "a deadlock, missing synchronization, misuse of async), data (data loss or corruption, a wrong "
    "migration or schema, wrong persistence), api (a broken or wrong public contract, compatibility), "
    "perf (a performance regression, unbounded work, an N+1), test_gap (a missing or wrong test of the "
    "changed behaviour), doc_defect (wrong or misleading documentation, comment or message), style "
    "(naming, formatting or readability with no behavioural effect), speculative (a question, a "
    "preference or a hypothetical without a concrete defect). Severity is Low, Medium, High or Critical "
    'by the harm if the point is right. Reply with JSON only: {"labels":[{"i":0,"category":"bug","severity":"High"}]}.'
)


def call_model(system, user):
    key = os.environ.get("OPENROUTER_API_KEY")
    if not key:
        raise SystemExit("OPENROUTER_API_KEY is not set")
    body = json.dumps(
        {"model": CLASSIFIER, "max_tokens": 2000, "messages": [{"role": "system", "content": system}, {"role": "user", "content": user}]}
    ).encode()
    for attempt in range(4):
        req = urllib.request.Request(
            "https://openrouter.ai/api/v1/chat/completions",
            data=body,
            headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
        )
        try:
            with urllib.request.urlopen(req, timeout=180) as resp:
                return json.load(resp)["choices"][0]["message"]["content"]
        except urllib.error.HTTPError as exc:
            if exc.code in (429, 500, 502, 503, 504):
                time.sleep(15 * (attempt + 1))
                continue
            raise SystemExit(f"classifier request failed: HTTP {exc.code}")
        except (urllib.error.URLError, TimeoutError):
            time.sleep(15 * (attempt + 1))
    raise SystemExit("classifier kept failing")


def classify(candidate, cache):
    key = f"{candidate['url']}@{candidate['reviewed_commit']}"
    if key in cache:
        return cache[key]
    items = [
        {"i": i, "path": t["path"], "line": t["line"], "comment": t["comment"], "diff_hunk": t["diff_hunk"]}
        for i, t in enumerate(candidate["threads"])
    ]
    for _ in range(3):
        reply = call_model(CLASSIFY_SYSTEM, json.dumps({"repository": candidate["repo"], "comments": items}))
        match = re.search(r"\{.*\}", reply, re.S)
        try:
            labels = {x["i"]: x for x in json.loads(match.group(0))["labels"]}
        except (AttributeError, ValueError, KeyError, TypeError):
            continue
        if all(i in labels and labels[i].get("category") in CATEGORIES for i in range(len(items))):
            result = [
                {"category": labels[i]["category"], "severity": labels[i].get("severity") if labels[i].get("severity") in SEVERITIES else "Medium"}
                for i in range(len(items))
            ]
            cache[key] = result
            return result
    cache[key] = None
    return None


def request(url, accept=None, limit=None):
    headers = {"User-Agent": "open-nitpick-fresh-corpus"}
    if accept:
        headers["Accept"] = accept
    token = subprocess.run(["gh", "auth", "token"], capture_output=True, text=True).stdout.strip()
    if token and "api.github.com" in url:
        headers["Authorization"] = f"Bearer {token}"
    for attempt in range(5):
        req = urllib.request.Request(url, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=300) as resp:
                data = resp.read(limit + 1) if limit else resp.read()
                if limit and len(data) > limit:
                    raise ValueError("too large")
                return data
        except urllib.error.HTTPError as exc:
            if exc.code in (403, 429, 500, 502, 503):
                time.sleep(20 * (attempt + 1))
                continue
            raise
        except (urllib.error.URLError, TimeoutError):
            time.sleep(10 * (attempt + 1))
    raise OSError(f"giving up on {url}")


def prepare(candidate, folder):
    """Download one case's metadata, diff and source tree, or return why it cannot be used."""
    repo, number, commit = candidate["repo"], candidate["number"], candidate["reviewed_commit"]
    folder.mkdir(parents=True, exist_ok=True)
    try:
        meta = json.loads(request(f"https://api.github.com/repos/{repo}/pulls/{number}"))
        diff = request(
            f"https://api.github.com/repos/{repo}/compare/{candidate['base_oid']}...{commit}", accept="application/vnd.github.diff"
        ).decode("utf-8", "replace")
    except (urllib.error.HTTPError, OSError, ValueError) as exc:
        return f"metadata or diff unavailable: {exc}"
    files = re.findall(r"^diff --git ", diff, re.M)
    changed = sum(1 for line in diff.splitlines() if line[:1] in "+-" and line[:3] not in ("+++", "---"))
    if not MIN_FILES <= len(files) <= MAX_FILES or not MIN_LINES <= changed <= MAX_LINES:
        return f"the reviewed state has {len(files)} files and {changed} changed lines, outside the size rule"
    meta["head"]["sha"] = commit
    meta["base"]["sha"] = candidate["base_oid"]
    (folder / "metadata.json").write_text(json.dumps(meta))
    (folder / "pr.diff").write_text(diff)
    try:
        archive = request(f"https://codeload.github.com/{repo}/tar.gz/{commit}", limit=MAX_ARCHIVE)
    except (urllib.error.HTTPError, OSError, ValueError) as exc:
        return f"source tree unavailable or over {MAX_ARCHIVE >> 20} MB: {exc}"
    (folder / "tree.tar.gz").write_bytes(archive)
    temp = folder / "extracted"
    shutil.rmtree(temp, ignore_errors=True)
    temp.mkdir()
    with tarfile.open(folder / "tree.tar.gz") as tar:
        tar.extractall(temp, filter="data")
    roots = list(temp.iterdir())
    if len(roots) != 1:
        return "the archive did not extract to a single root"
    shutil.rmtree(folder / "snapshot", ignore_errors=True)
    roots[0].rename(folder / "snapshot")
    temp.rmdir()
    check = subprocess.run(
        ["git", "apply", "-R", "--check", "--whitespace=nowarn", str((folder / "pr.diff").resolve())],
        cwd=folder / "snapshot",
        capture_output=True,
        text=True,
    )
    if check.returncode != 0:
        return f"the diff does not reverse-apply to the reviewed tree: {check.stderr.strip()[:200]}"
    (folder / "input.json").write_text(
        json.dumps(
            {
                "snapshot": str((folder / "snapshot").resolve()),
                "metadata": str((folder / "metadata.json").resolve()),
                "diff": str((folder / "pr.diff").resolve()),
                "tree_archive": str((folder / "tree.tar.gz").resolve()),
            },
            indent=2,
        )
    )
    return None


def build(args):
    pool = json.loads(args.candidates.read_text())["candidates"]
    inputs = args.out / "inputs"
    inputs.mkdir(parents=True, exist_ok=True)
    cache_path = args.out / "classify-cache.json"
    cache = json.loads(cache_path.read_text()) if cache_path.exists() else {}
    cases, skipped = [], []
    for language in [x for x in LANGUAGES if x in args.languages.split(",")]:
        taken, per_repo = 0, collections.Counter()
        for candidate in sorted((c for c in pool if c["language"] == language), key=order_key):
            if taken == args.per_language:
                break
            if per_repo[candidate["repo"]] >= PER_REPO:
                continue
            labels = classify(candidate, cache)
            cache_path.write_text(json.dumps(cache, indent=1))
            if not labels:
                skipped.append({"url": candidate["url"], "why": "the classifier gave no usable labels"})
                continue
            if not any(label["category"] in CORE for label in labels):
                skipped.append({"url": candidate["url"], "why": "no golden comment in a core category"})
                continue
            name = f"{SHORT[language]}-{taken + 1}"
            reason = prepare(candidate, inputs / name)
            if reason:
                shutil.rmtree(inputs / name, ignore_errors=True)
                skipped.append({"url": candidate["url"], "why": reason})
                print(f"skip {candidate['url']}: {reason}", flush=True)
                continue
            taken += 1
            per_repo[candidate["repo"]] += 1
            golden = [
                {
                    "comment": f"{t['path']}:{t['line']}: {t['comment']}" if t["line"] else f"{t['path']}: {t['comment']}",
                    "severity": label["severity"],
                    "category": label["category"],
                }
                for t, label in zip(candidate["threads"], labels)
            ]
            cases.append({"case": name, "url": candidate["url"], "language": language.lower(), "split": "fresh", "repo": candidate["repo"],
                          "title": candidate["title"], "reviewed_commit": candidate["reviewed_commit"], "golden": golden})
            print(f"{name}: {candidate['url']} ({len(golden)} golden comments)", flush=True)
        print(f"{language}: {taken} of {args.per_language}", flush=True)
    rule = (
        "Merged pull requests of 2026-09-01 to 2026-11-08 in public repositories with 200 stars or more, in TypeScript, Python, Go, "
        "Java or Ruby, outside the five Martian repositories; ordered by SHA-256 of owner/repo#number, the first six per language, "
        "at most two per repository. Fixed before reviews; no selection by findings or by any tool's performance."
    )
    digest = hashlib.sha1(json.dumps([[c["url"], c["reviewed_commit"]] for c in cases]).encode()).hexdigest()
    selection = {
        "rule": rule,
        "benchmark_commit": digest,
        "cases": [{k: c[k] for k in ("case", "url", "language", "split")} for c in cases],
    }
    (inputs / "selection.json").write_text(json.dumps(selection, indent=2))
    (args.out / "skipped.json").write_text(json.dumps(skipped, indent=1))
    write_bench(args, cases, digest)
    print(f"{len(cases)} cases written to {args.out}; {len(skipped)} candidates skipped", flush=True)


def write_bench(args, cases, digest):
    """The directory the upstream scorer reads in place of the pinned Martian checkout."""
    real = Path(args.martian) / "offline"
    root = args.out / f"fresh-benchmark-{digest}" / "offline"
    shutil.rmtree(root.parent, ignore_errors=True)
    (root / "results").mkdir(parents=True)
    (root / "golden_comments").mkdir()
    for name in ("code_review_benchmark", "analysis"):
        (root / name).symlink_to((real / name).resolve())
    entries = [
        {"pr_title": c["title"], "url": c["url"], "comments": c["golden"]} for c in cases
    ]
    (root / "golden_comments" / "fresh.json").write_text(json.dumps(entries, indent=1))
    data = {c["url"]: {"pr_title": c["title"], "golden_comments": c["golden"], "source_repo": c["repo"]} for c in cases}
    (root / "results" / "benchmark_data.json").write_text(json.dumps(data, indent=1))
    judge = root / "results" / "anthropic_claude-sonnet-4-5-20250929"
    judge.mkdir()
    (judge / "evaluations.json").write_text(json.dumps({c["url"]: {} for c in cases}))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="command", required=True)
    m = sub.add_parser("mine")
    m.add_argument("--out", type=Path, required=True)
    m.add_argument("--start", default="2026-09-01")
    m.add_argument("--end", default="2026-11-08")
    m.add_argument("--window", type=int, default=7, help="days per search window")
    m.add_argument("--pages", type=int, default=3, help="pages of 25 per window and language")
    m.add_argument("--min-comments", type=int, default=3)
    m.add_argument("--max-comments", type=int, default=25)
    b = sub.add_parser("build")
    b.add_argument("--candidates", type=Path, required=True)
    b.add_argument("--out", type=Path, required=True)
    b.add_argument("--martian", default="/tmp/bakeoff/bench", help="the pinned Martian checkout, for the scorer's code")
    b.add_argument("--per-language", type=int, default=PER_LANGUAGE)
    b.add_argument("--languages", default=",".join(LANGUAGES))
    args = ap.parse_args()
    {"mine": mine, "build": build}[args.command](args)


if __name__ == "__main__":
    sys.exit(main())
