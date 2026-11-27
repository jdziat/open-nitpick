#!/usr/bin/env python3
"""Count the calls in a stored run that lost their reasoning at the output cap.

A reasoning model that spends the whole output budget thinking answers with
nothing, and both tools then send the call again with reasoning off. The
review that comes back is a review written without reasoning, so a run where
most calls took that path measures a different reviewer from the one named.
nitpick logs the retry as "hit the output cap" (a review or triage call)
or "a forced submission made no tool call" (the R10 tool loop); Mira logs
"output budget before finishing" in each review's execution.log.

    python3 tools/bakeoff/fallbacks.py /tmp/bakeoff/runs/r1-p1 /tmp/bakeoff/mira-pass1/mira/deepseek
"""

import argparse
from pathlib import Path


def ours(run):
    """Batches, review-call fallbacks, triage-call fallbacks and empty forced submissions."""
    batches = review = triage = forced = 0
    for log in sorted(run.glob("*-r1/stderr.txt")):
        stage = "review"
        for line in log.read_text(errors="replace").splitlines():
            if '"batch started"' in line:
                batches += 1
            elif "msg=triaging" in line:
                stage = "triage"
            elif "hit the output cap" in line:
                if stage == "triage":
                    triage += 1
                else:
                    review += 1
            elif "forced submission made no tool call" in line:
                forced += 1
    return batches, review, triage, forced


def mira(run):
    """Reviews, and how many of them had a call that ran out of budget thinking."""
    logs = sorted(run.glob("*-r1/execution.log"))
    hit = [log for log in logs if "output budget before finishing" in log.read_text(errors="replace")]
    return len(logs), len(hit)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("runs", nargs="+", type=Path, help="run directories holding <case>-r1 results")
    args = ap.parse_args()
    for run in args.runs:
        if any(run.glob("*-r1/execution.log")):
            reviews, hit = mira(run)
            print(f"{run}: Mira, {reviews} reviews, {hit} with a call sent again with thinking off")
            continue
        batches, review, triage, forced = ours(run)
        if batches == 0:
            print(f"{run}: no review logs found")
            continue
        print(f"{run}: {batches} batches, {review} review calls and {triage} triage calls sent again "
              f"with reasoning off, {forced} empty forced submissions")


if __name__ == "__main__":
    main()
