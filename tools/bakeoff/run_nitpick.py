#!/usr/bin/env python3
"""Run `nitpick review` over the repositories built by build_repos.py.

Writes the layout the upstream scorer reads (the same one Mira's harness
writes), so both reviewers' runs can be scored together by one judge:

    <out>/run.json
    <out>/<case>-r1/status.json   {"exit_code": 0}   non-zero when the review failed
    <out>/<case>-r1/result.json   {"result": {"comments": [{title, path, line, body}]},
                                   "duration_seconds": s, "manifest": {...}}

The review is the shipped CLI at shipped defaults, with the model set through
LLM_MODEL and no analyzers, since the other reviewer runs none. Findings are
read from the dry-run text, which is the only machine-readable form `review`
prints; the raw output is kept beside the result so the parse can be audited.

    python3 tools/bakeoff/run_nitpick.py --repos /tmp/bakeoff/repos --inputs /tmp/bakeoff/inputs \
        --out /tmp/bakeoff/nitpick-pass1
"""
import argparse
import concurrent.futures
import html
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

LOCATION = re.compile(r"^(\S[^\n]*?):(\d+)$")
BOLD = re.compile(r"^\s*\*\*(.+)\*\*\s*$")


def parse(stdout: str):
    """Findings from a dry-run review: each starts at a `path:line` line."""
    lines = stdout.splitlines()
    starts = [i for i, ln in enumerate(lines) if LOCATION.match(ln) and "://" not in ln]
    comments = []
    for n, i in enumerate(starts):
        end = starts[n + 1] if n + 1 < len(starts) else len(lines)
        m = LOCATION.match(lines[i])
        path, line = m.group(1), int(m.group(2))
        # The severity row ("**🔴 Critical** · class") is not wholly bold, and a
        # bare one starts with a severity marker; the title is the next bold line.
        bolds = [
            (j, BOLD.match(lines[j]).group(1))
            for j in range(i + 1, end)
            if BOLD.match(lines[j]) and not BOLD.match(lines[j]).group(1).lstrip().startswith(("🔴", "🟠", "🟡", "🔵", "⚪"))
        ]
        if not bolds:
            continue
        title_at, title = bolds[0]
        body, fence = [], False
        for j in range(title_at + 1, end):
            raw = lines[j]
            text = raw.strip()
            if text.startswith("flagged by") or text.startswith("💬") or text.startswith("Reviewed "):
                break
            if text.startswith("**Suggested change**"):
                break
            if text.startswith("```"):
                fence = not fence
                continue
            if fence:
                continue
            body.append(text)
        comments.append({
            "title": html.unescape(title),
            "path": path,
            "line": line,
            "body": html.unescape("\n".join(x for x in body if x)).strip(),
        })
    return comments


def review(case, repos, out, model, timeout, config):
    repo = repos / case
    t0 = time.time()
    env = dict(os.environ, LLM_PROVIDER="openrouter", LLM_MODEL=model, NITPICK_NO_USER_CONFIG="1")
    dest = out / f"{case}-r1"
    dest.mkdir(parents=True, exist_ok=True)
    try:
        run = subprocess.run(
            ["nitpick", "review", "-repo", str(repo), "-base", "HEAD~1", "-head", "HEAD", "-dry-run", "-no-linters"]
            + (["-config", str(config)] if config else []),
            capture_output=True, text=True, env=env, timeout=timeout,
        )
        code, stdout, stderr = run.returncode, run.stdout, run.stderr
    except subprocess.TimeoutExpired as e:
        code, stdout, stderr = 124, (e.stdout or b"").decode() if isinstance(e.stdout, bytes) else (e.stdout or ""), "timeout"
    seconds = time.time() - t0
    (dest / "stdout.txt").write_text(stdout)
    (dest / "stderr.txt").write_text(stderr)
    # exit 1 is "findings at the gate"; only 2 and a timeout are failures.
    failed = code not in (0, 1) or "Reviewed " not in (stdout + stderr)
    status = {"exit_code": 1 if failed else 0, "nitpick_exit": code}
    comments = [] if failed else parse(stdout)
    result = {
        "result": {"comments": comments},
        "duration_seconds": seconds,
        "manifest": {"reviewer": "open-nitpick", "model": model, "version": nitpick_version(),
                     "config": str(config) if config else None},
    }
    (dest / "status.json").write_text(json.dumps(status))
    (dest / "result.json").write_text(json.dumps(result, indent=2))
    print(json.dumps({"case": case, "exit": code, "findings": len(comments), "seconds": round(seconds)}), flush=True)


def nitpick_version():
    return subprocess.run(["nitpick", "version"], capture_output=True, text=True).stdout.strip()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--repos", type=Path, required=True)
    ap.add_argument("--inputs", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--model", default="deepseek/deepseek-v4.1-flash-1017")
    ap.add_argument("--concurrency", type=int, default=2)
    ap.add_argument("--timeout", type=int, default=900)
    ap.add_argument("--only", default="")
    ap.add_argument("--config", type=Path, default=None, help="a .nitpick.yaml to review under")
    args = ap.parse_args()
    selection = json.loads((args.inputs / "selection.json").read_text())
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / "run.json").write_text(json.dumps({
        "selection": selection, "repeats": 1, "model": args.model, "reviewer": "open-nitpick",
        "version": nitpick_version(), "config": str(args.config) if args.config else None,
    }))
    only = {x for x in args.only.split(",") if x}
    cases = [c["case"] for c in selection["cases"] if not only or c["case"] in only]
    with concurrent.futures.ThreadPoolExecutor(args.concurrency) as pool:
        list(pool.map(lambda c: review(c, args.repos, args.out, args.model, args.timeout, args.config), cases))


if __name__ == "__main__":
    main()
