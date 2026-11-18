#!/usr/bin/env python3
"""On the fresh corpus: how often a drafted finding is a true positive, by the stage that held it back.

Labels come from the judge's verdicts on the drafted sets. This is a diagnostic, not the registered
measure.

    python3 tools/bakeoff/fresh_stages.py [judge] [fresh directory]
"""

import collections
import json
import sys
from pathlib import Path

JUDGE = sys.argv[1] if len(sys.argv) > 1 else "openai-gpt-6.1-sol"
FRESH = Path(sys.argv[2] if len(sys.argv) > 2 else "/tmp/bakeoff/fresh")


def key(comment):
    """The scorer's rendering of a candidate, which is how a verdict names it."""
    return f'{comment["title"]}\n{comment["path"]}:{comment["line"]}\n{comment["body"]}'.strip()


def stage_of(reason):
    if reason == "posted":
        return "posted"
    if "below the floor" in reason:
        return "held by the floor"
    if "repeats" in reason:
        return "held as a duplicate"
    if "unsupported" in reason:
        return "held by the critique (unsupported)"
    if "plausible" in reason:
        return "held by the critique (plausible)"
    return "other: " + reason[:30]


def band(confidence):
    if confidence >= 0.9:
        return "0.9+"
    if confidence >= 0.8:
        return "0.8"
    if confidence >= 0.7:
        return "0.7"
    return "<0.7"


def labelled_drafts(arm, evaluations):
    """Every draft of an arm with the judge's verdict on it, and how many the scorer merged away."""
    drafts, merged = [], 0
    for pass_number in (1, 2, 3):
        for case_dir in sorted((FRESH / "runs" / f"{arm}-p{pass_number}").glob("*-r1")):
            verdict = evaluations.get(case_dir.name, {}).get(f"{arm}-p{pass_number}-drafted")
            if verdict is None:
                continue
            true = {c["matched_candidate"].strip() for c in verdict["true_positives"]}
            false = {c["candidate"].strip() for c in verdict["false_positives"]}
            result = json.loads((case_dir / "result.json").read_text())["result"]
            held = [(w, w["reason"]) for w in result.get("withheld", [])]
            for comment, reason in [(c, "posted") for c in result["comments"]] + held:
                if key(comment) in true:
                    drafts.append((comment, reason, 1))
                elif key(comment) in false:
                    drafts.append((comment, reason, 0))
                else:
                    merged += 1
    return drafts, merged


def main():
    for arm in ("d1c", "d3m"):
        path = FRESH / f"scores-{arm}-{JUDGE}" / "scores.json"
        drafts, merged = labelled_drafts(arm, json.loads(path.read_text())["evaluations"])
        by_stage = collections.defaultdict(lambda: [0, 0])
        by_band = collections.defaultdict(lambda: [0, 0])
        for comment, reason, hit in drafts:
            for table, name in ((by_stage, stage_of(reason)), (by_band, band(float(comment.get("confidence") or 0)))):
                table[name][0] += hit
                table[name][1] += 1
        hits = sum(h for h, _ in by_stage.values())
        print(f"{arm}: {len(drafts)} labelled drafts, {hits} true ({merged} merged by the scorer's own dedup)")
        for name, (h, n) in sorted(by_stage.items(), key=lambda item: -item[1][1]):
            print(f"   {name:38} {h:3}/{n:3} true = {h / n:.0%}")
        rates = ", ".join(f"{b} {by_band[b][0]}/{by_band[b][1]} ({by_band[b][0] / by_band[b][1]:.0%})"
                          for b in ("<0.7", "0.7", "0.8", "0.9+") if by_band[b][1])
        print(f"   by confidence: {rates}")


if __name__ == "__main__":
    main()
