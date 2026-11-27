#!/usr/bin/env python3
"""Benchmark notary against non-notary reviews with a real model.

Runs the real `nitpick review` against a real `nitpick-server` on real changes
from this repository's history. Each change gets its arms interleaved: reviews
with no notary, a first review with one, replays of it, and a -fresh review.
Usage and cost are read from what the provider reported, never estimated, and a
hard budget aborts the run before it can overspend.

    scripts/bench-notary-real.py --probe          # one review, to check the setup
    scripts/bench-notary-real.py --budget 2.00    # the benchmark

Needs an OpenRouter key in the keystore (nitpick auth set openrouter), a
nitpick and a nitpick-server binary, and spends real money.
"""
import argparse
import hashlib
import json
import os
import subprocess
import sys
import tempfile
import time

MODEL = "deepseek/deepseek-v4.1-flash"
CHANGES = [("small", "fc0ec89"), ("medium", "765e711")]


def run(cmd, env=None, timeout=1200):
    t0 = time.time()
    p = subprocess.run(cmd, capture_output=True, text=True, env=env, timeout=timeout)
    return p, time.time() - t0


def parse(stdout):
    # A dry run prints the review text, then one JSON object.
    i = stdout.rfind("\n{")
    return json.loads(stdout[i + 1:] if i >= 0 else stdout)


def summarize(out, wall, rc):
    u = out["usage"]
    attempts = u["attempts"]
    toks = {"prompt": 0, "completion": 0}
    unknown = 0
    for a in attempts:
        us = a.get("usage")
        if not us:
            unknown += 1
            continue
        toks["prompt"] += us.get("prompt_tokens", us.get("PromptTokens", 0)) or 0
        toks["completion"] += us.get("completion_tokens", us.get("CompletionTokens", 0)) or 0
    n = out.get("notary") or {}
    finds = out["findings"]
    return {
        "rc": rc, "result": out["result"], "complete": out["complete"], "wall_s": round(wall, 1),
        "requests": len(attempts), "failed_requests": sum(1 for a in attempts if a["failed"]),
        "prompt_tokens": toks["prompt"], "completion_tokens": toks["completion"],
        "attempts_without_usage": unknown, "reported_cost_usd": u["reported_cost_usd"],
        "notary_mode": n.get("Mode", ""), "replayed": n.get("Replayed", 0), "live": n.get("Live", 0),
        "findings": len(finds),
        "set_hash": hashlib.sha256(json.dumps(
            sorted([f["path"], f["line"], f["severity"], f["title"]] for f in finds)).encode()).hexdigest()[:10],
        "keys_path_line": sorted({f"{f['path']}:{f['line']}" for f in finds}),
        "keys_path_class": sorted({f"{f['path']}|{f.get('class') or f.get('category')}" for f in finds}),
    }


def jaccard(a, b):
    a, b = set(a), set(b)
    return 1.0 if not (a | b) else len(a & b) / len(a | b)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--nitpick", default="/tmp/nb/nitpick")
    ap.add_argument("--notary", default="/tmp/nb/nitpick-server")
    ap.add_argument("--repo", default=os.getcwd())
    ap.add_argument("--budget", type=float, default=2.0, help="abort once reported spend passes this many USD")
    ap.add_argument("--probe", action="store_true")
    ap.add_argument("--out", default="/tmp/notary-real.json")
    a = ap.parse_args()

    tmp = tempfile.mkdtemp(prefix="notary-real-")
    cfg = os.path.join(tmp, "bench.yaml")
    open(cfg, "w").write(f"""models:
  default: &m
    provider: openrouter
    model: {MODEL}
  review: *m
  triage: *m
review:
  incremental: false
""")
    wt = os.path.join(tmp, "wt")
    subprocess.run(["git", "-C", a.repo, "worktree", "add", "--detach", wt, "main"], check=True, capture_output=True)
    env = dict(os.environ, GITHUB_ACTIONS="", GITHUB_REPOSITORY="", GITHUB_REF="", PR_NUMBER="", NITPICK_NOTARY_TOKEN="bench-secret")

    notary = None
    port = 18490
    try:
        notary = subprocess.Popen([a.notary, "-addr", f"127.0.0.1:{port}", "-data", os.path.join(tmp, "records"),
                                   "-token", "ci:authoritative:bench-secret"], stderr=subprocess.DEVNULL)
        time.sleep(1.0)
        url = f"http://127.0.0.1:{port}"
        spent = 0.0

        def review(sha, *extra):
            nonlocal spent
            cmd = [a.nitpick, "review", "-repo", wt, "-config", cfg, "-base", sha + "^", "-head", sha,
                   "-dry-run", "-format", "json", "-no-linters", "-fail-on", "none", *extra]
            p, wall = run(cmd, env=env)
            try:
                out = parse(p.stdout)
            except Exception:
                sys.exit(f"no JSON from review (rc={p.returncode}):\n{p.stderr[-800:]}")
            s = summarize(out, wall, p.returncode)
            if s["reported_cost_usd"] is not None:
                spent += s["reported_cost_usd"]
            elif s["requests"] and not a.probe:
                # Cost not reported: spend cannot be bounded in dollars, so refuse to go on.
                sys.exit("the provider reported no cost, so the budget cannot be enforced; stopping")
            if spent > a.budget:
                json.dump({"aborted": "budget", "spent": spent}, open(a.out, "w"))
                sys.exit(f"budget {a.budget} USD exceeded after {spent:.4f}; stopped")
            return s

        if a.probe:
            s = review(CHANGES[0][1])
            print(json.dumps({k: v for k, v in s.items() if not k.startswith("keys_")}, indent=2))
            return

        results = {}
        for name, sha in CHANGES:
            rows = []
            plan = ["none", "first", "none", "replay", "none", "replay", "fresh", "replay"]
            for arm in plan:
                if arm == "none":
                    s = review(sha)
                elif arm == "first":
                    s = review(sha, "-notary", url)
                elif arm == "replay":
                    s = review(sha, "-notary", url)
                else:
                    s = review(sha, "-notary", url, "-fresh", "-clear-by", "bench")
                s["arm"] = arm
                rows.append(s)
                print(f"{name:7} {arm:7} wall={s['wall_s']:6}s req={s['requests']:3} cost={s['reported_cost_usd']} "
                      f"find={s['findings']:2} mode={s['notary_mode'] or '-':9} replayed={s['replayed']:3} set={s['set_hash']}",
                      flush=True)
            # A change whose reviews find nothing cannot show that a replay says
            # what the review said, or that reviews drift: empty equals empty.
            vacuous = all(r["findings"] == 0 for r in rows if r["arm"] != "replay")
            if vacuous:
                print(f"{name}: no live review found anything, so its equality and drift results are void", flush=True)
            results[name] = {"sha": sha, "vacuous": vacuous, "runs": rows}
        results["spent_usd"] = spent
        json.dump(results, open(a.out, "w"), indent=1)
        print(f"\nspent {spent:.4f} USD; wrote {a.out}")
    finally:
        if notary:
            notary.terminate()
        subprocess.run(["git", "-C", a.repo, "worktree", "remove", "--force", wt], capture_output=True)


if __name__ == "__main__":
    main()
