#!/usr/bin/env python3
"""Summarize what deep reviews did and cost, from the logs of stored runs.

Each deep-review agent logs one "deep-review agent finished" line with its
turns, lookups, whether it was forced to submit, how many times it was sent
again without reasoning, and its tokens; a failed agent logs "a deep-review
agent failed"; the critique logs its tokens. This prints, for each run, the
validity counts the D1 registration names (failed agents and reasoning
fallbacks as a share of agent runs) beside calls, tokens, spend and review time.

    python3 tools/bakeoff/deep_stats.py /tmp/bakeoff/runs/d1-p1 /tmp/bakeoff/runs/d1-p2
"""

import argparse
import json
import re
import statistics
from pathlib import Path

# OpenRouter's price for deepseek/deepseek-v4.1-flash-1017 on 2026-11-14, dollars per million tokens.
# A run on another model passes its own with --price-in and --price-out.
PRICE_IN, PRICE_OUT = 0.038, 0.50

FIELD = re.compile(r'(\w+)=("[^"]*"|\S+)')


def fields(line):
    return {k: v.strip('"') for k, v in FIELD.findall(line)}


def summarize(run, price_in=PRICE_IN, price_out=PRICE_OUT):
    agents = failed = forced = reasoning_low = reasoning_off = turns = lookups = 0
    prompt = completion = 0
    times = []
    reviews = pending = 0
    for case in sorted(run.glob("*-r1")):
        if not (case / "result.json").exists():
            pending += 1
            continue
        reviews += 1
        result = json.loads((case / "result.json").read_text())
        times.append(result.get("duration_seconds") or 0)
        for line in (case / "stderr.txt").read_text(errors="replace").splitlines():
            if "deep-review agent finished" in line:
                f = fields(line)
                agents += 1
                turns += int(f.get("turns", 0))
                lookups += int(f.get("lookups", 0))
                forced += f.get("forced") == "true"
                reasoning_low += int(f.get("reasoning_low", 0))
                reasoning_off += int(f.get("reasoning_off", 0))
                prompt += int(f.get("prompt_tokens", 0))
                completion += int(f.get("completion_tokens", 0))
            elif "a deep-review agent failed" in line:
                failed += 1
            elif "deep-review critique answered" in line:
                f = fields(line)
                prompt += int(f.get("prompt_tokens", 0))
                completion += int(f.get("completion_tokens", 0))
    if reviews == 0:
        return f"{run}: no finished reviews ({pending} in progress)"
    started = agents + failed
    spend = prompt / 1e6 * price_in + completion / 1e6 * price_out
    return (
        f"{run}: {reviews} reviews, {started} agent runs, {failed} failed ({failed / max(started, 1):.1%}), "
        f"{forced} forced, {reasoning_low} stepped down to low effort, "
        f"{reasoning_off} sent again without reasoning ({reasoning_off / max(started, 1):.1%} of agent runs, "
        f"{reasoning_off / max(forced, 1):.1%} of forced submissions); "
        f"per review {turns / reviews:.1f} turns, {lookups / reviews:.1f} lookups, "
        f"{prompt / reviews / 1000:.0f}k prompt and {completion / reviews / 1000:.0f}k completion tokens, "
        f"${spend / reviews:.4f}; median {statistics.median(times):.0f}s"
        + (f"; {pending} still in progress" if pending else "")
    )


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("runs", nargs="+", type=Path)
    ap.add_argument("--price-in", type=float, default=PRICE_IN, help="dollars per million prompt tokens")
    ap.add_argument("--price-out", type=float, default=PRICE_OUT, help="dollars per million completion tokens")
    args = ap.parse_args()
    for run in args.runs:
        print(summarize(run, args.price_in, args.price_out))


if __name__ == "__main__":
    main()
