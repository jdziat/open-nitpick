#!/usr/bin/env python3
"""Ask TypeSafe's Jev decision model whether each comment's claim holds.

Each comment becomes one decision: the claim and the diff around the lines it
cites, asked as a single yes-or-no question. Jev answers with a probability,
stored beside the comment's identity so a threshold can be chosen afterwards
without another call. The request is the one cleatus's jev_parity.py makes: a
POST to OpenRouter's decision endpoint with model ~typesafe/jev-latest.

    python3 tools/bakeoff/jev_probe.py --run /tmp/bakeoff/grade/probe-run \\
        --inputs /tmp/bakeoff/heldout/inputs --out /tmp/bakeoff/grade/jev-probe.json --repeats 2
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from critique_filter import diff_sections, window  # noqa: E402

URL = "https://openrouter.ai/api/alpha/decisions"
MODEL = "~typesafe/jev-latest"
INSTRUCTIONS = (
    "Is the defect this review comment claims real, given the code shown? "
    "Answer true if the code shown supports the claim: the defect exists and can be reached. "
    "Answer false if the code shown contradicts the claim, already handles it, or does not support it."
)


def state_for(comment, sections):
    """The claim and the diff window around its cited lines, as one text state."""
    claim = (comment.get("body") or "").strip()[:900]
    section = sections.get(comment["path"], "")
    diff_text = window(section, comment["line"]) if section else "(file not in the diff)"
    return (
        f"Pull request review comment to check.\nFile: {comment['path']}:{comment['line']}\n"
        f"Title: {comment['title']}\nClaim: {claim}\nDiff around the cited lines:\n{diff_text}"
    )


def decide(state, key):
    """One decision: the probability that the claim holds, and what the call cost."""
    body = {
        "state": state,
        "model": MODEL,
        "questions": {"is_real": {"type": "noul", "instructions": INSTRUCTIONS}},
    }
    request = urllib.request.Request(
        URL,
        data=json.dumps(body).encode(),
        method="POST",
        headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"},
    )
    last: Exception | None = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=180) as response:
                reply = json.load(response)
            probability = float(reply["answers"]["is_real"]["noul"])
            cost = float((reply.get("usage") or {}).get("cost") or 0.0)
            return probability, cost
        except (urllib.error.URLError, KeyError, TypeError, ValueError) as error:
            last = error
            time.sleep(2 * (attempt + 1))
    raise RuntimeError(f"decision failed after 3 attempts: {last}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", type=Path, required=True, help="a run directory of <case>-r1 result files")
    ap.add_argument("--inputs", type=Path, required=True, help="prepared inputs, holding <case>/pr.diff")
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--repeats", type=int, default=1, help="decisions per comment, to measure stability")
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("--evidence", type=Path, default=None,
                    help="JSON from verify_filter --evidence-out: what the checker read, added to each decision")
    args = ap.parse_args()
    key = os.environ["OPENROUTER_API_KEY"]
    evidence = json.loads(args.evidence.read_text()) if args.evidence else {}

    jobs = []
    for case_dir in sorted(args.run.glob("*-r1")):
        case = case_dir.name[:-3]
        sections = diff_sections((args.inputs / case / "pr.diff").read_text(errors="replace"))
        for comment in json.loads((case_dir / "result.json").read_text())["result"]["comments"]:
            state = state_for(comment, sections)
            if args.evidence:
                found = evidence.get(f"{case}|{comment['path']}|{comment['line']}|{comment['title']}", "")
                state += "\n\nEvidence from a check of the repository:\n" + (found or "(the check made no lookups)")
            jobs.append((case, comment, state))

    def run_one(job):
        case, comment, state = job
        probabilities, spent = [], 0.0
        for _ in range(args.repeats):
            try:
                probability, cost = decide(state, key)
            except RuntimeError:
                probabilities.append(None)
                continue
            probabilities.append(probability)
            spent += cost
        return case, comment, probabilities, spent

    results, total = {}, 0.0
    with ThreadPoolExecutor(args.workers) as pool:
        for case, comment, probabilities, spent in pool.map(run_one, jobs):
            total += spent
            key_text = f"{case}|{comment['path']}|{comment['line']}|{comment['title']}"
            results[key_text] = {
                "case": case,
                "path": comment["path"],
                "line": comment["line"],
                "title": comment["title"],
                "probabilities": probabilities,
            }
    args.out.write_text(json.dumps(results, indent=1))
    failed = sum(p is None for r in results.values() for p in r["probabilities"])
    print(f"{len(results)} comments, {args.repeats} decision(s) each, {failed} failed, cost ${total:.4f}")


if __name__ == "__main__":
    main()
