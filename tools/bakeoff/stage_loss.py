#!/usr/bin/env python3
"""Where each reviewer's true and false drafts go, stage by stage, under one judge.

For Mira and for each deep-review arm on the tuning set, this prints the scorer's own true and
false counts per pass at three points: drafted, after the confidence floor with the duplicate rule
and the cap, and posted. It then prints what the selection reads: the share of labelled drafts
that report a confidence under the floor, and the share of the survivors at 0.75 or more, which
is the confidence the keep rule asks of a finding its second stage could not prove.

It makes no model call. Counts are the scorer's, so a stage's true count is golden comments
matched, not drafts.

    python3 tools/bakeoff/stage_loss.py [label=runs-prefix=scores-directory ...]

An extra arm is named as `d6v=/tmp/bakeoff/runs/d6v=/tmp/bakeoff/scores-d6v-openai-gpt-6.1-sol`.
"""

import json
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from mira_funnel import draft_index  # noqa: E402

JUDGE = "openai-gpt-6.1-sol"
BAKEOFF = Path("/tmp/bakeoff")
FROZEN = Path(os.path.expanduser("~/.local/share/nitpick/bakeoff-frozen/2026-11-14"))
FLOOR = 0.7
SURE = 0.75
ARMS = [
    ("D1c", BAKEOFF / "runs/d1c", BAKEOFF / f"scores-d1c-{JUDGE}"),
    ("D3m", BAKEOFF / "runs/d3m", BAKEOFF / f"scores-D3m-{JUDGE}"),
]
FILTER = "the deep-review filter"
POSTED, FILTERED, SECOND = "posted", "held by the filter", "held by the second stage"


def key(comment):
    """The scorer's rendering of a candidate, which is how a verdict names it."""
    return f'{comment.get("title", "")}\n{comment.get("path")}:{comment.get("line")}\n{comment.get("body", "")}'.strip()


def evaluations(scores):
    return json.loads((Path(scores) / "scores.json").read_text())["evaluations"]


def stage_counts(evals, labels):
    """True and false counts over the labels, and how many of the labels were scored."""
    true = false = scored = 0
    for label in labels:
        seen = False
        for verdicts in evals.values():
            if label in verdicts:
                true += verdicts[label]["tp"]
                false += verdicts[label]["fp"]
                seen = True
        scored += seen
    return true, false, scored


def labelled(verdict, drafts):
    """The drafts the judge labelled, each as (confidence, where it ended, whether it was true)."""
    true = {c["matched_candidate"].strip() for c in verdict["true_positives"]}
    false = {c["candidate"].strip() for c in verdict["false_positives"]}
    return [(confidence, ended, key(comment) in true) for comment, confidence, ended in drafts if key(comment) in true | false]


def our_drafts(case_dir):
    """Every draft of one deep review with its confidence and where it ended."""
    result = json.loads((case_dir / "result.json").read_text())["result"]
    drafts = [(c, float(c.get("confidence") or 0), POSTED) for c in result["comments"]]
    for held in result.get("withheld", []):
        ended = FILTERED if held.get("withheld_by") == FILTER else SECOND
        drafts.append((held, float(held.get("confidence") or 0), ended))
    return drafts


def mira_drafts(case_dir):
    """Every draft of one Mira review, joined from its audit trail and its stored model calls."""
    result = json.loads((case_dir / "result.json").read_text())["result"]
    index = draft_index(case_dir)
    drafts = [(c, float(c.get("confidence") or 0), POSTED) for c in result["comments"]]
    for entry in result["audit"]:
        if entry.get("stage") not in ("noise_filter", "self_critique"):
            continue
        comment = index.get((entry["path"], entry["title"][:80]))
        if comment is not None:
            ended = FILTERED if entry["stage"] == "noise_filter" else SECOND
            drafts.append((comment, float(entry.get("confidence") or 0), ended))
    return drafts


def confidence_shares(rows):
    """Share of labelled drafts under the floor, and of those the floor passed that are at SURE or more."""
    under = sum(1 for confidence, _, _ in rows if confidence < FLOOR)
    passed = [confidence for confidence, ended, _ in rows if confidence >= FLOOR and ended != FILTERED]
    sure = sum(1 for confidence in passed if confidence >= SURE)
    return under, len(rows), sure, len(passed)


def posted_band(rows, low=SURE, high=0.85):
    """How far a mid-range confidence can be trusted once a finding is posted: true and total in the band."""
    band = [true for confidence, ended, true in rows if ended == POSTED and low <= confidence <= high]
    return sum(band), len(band)


def report(name, stages, rows):
    drafted, after, posted = stages
    print(f"\n== {name}")
    for title, (true, false, passes) in (("drafted", drafted), ("after the filter", after), ("posted", posted)):
        if passes:
            print(f"   {title:18} {true / passes:5.1f} true  {false / passes:5.1f} false a pass ({passes} passes)")
    if drafted[2] and after[2] and posted[2] and drafted[0] and after[1]:
        lost = 1 - (after[0] / after[2]) / (drafted[0] / drafted[2])
        removed = 1 - (posted[1] / posted[2]) / (after[1] / after[2])
        print(f"   the filter withholds {lost:.0%} of the true drafts; the second stage removes {removed:.0%} of the false ones it sees")
    under, total, sure, passed = confidence_shares(rows)
    if total and passed:
        print(f"   confidence under {FLOOR}: {under} of {total} labelled drafts ({under / total:.0%}); "
              f"at {SURE} or more among those the floor passed: {sure} of {passed} ({sure / passed:.0%})")
    true, total = posted_band(rows)
    if total:
        print(f"   posted at {SURE} to 0.85: {true} of {total} true ({true / total:.0%})")


def mira():
    evals = evaluations(BAKEOFF / f"rescore-{JUDGE}")
    rows = []
    for number, label in ((1, "a-md-1"), (2, "a-md-2")):
        for case_dir in sorted((FROZEN / f"mira/tuning/mira-pass{number}/mira/deepseek").glob("*-r1")):
            verdict = evals.get(case_dir.name, {}).get(label)
            if verdict is not None:
                rows += labelled(verdict, mira_drafts(case_dir))
    stages = [stage_counts(evals, labels) for labels in (["a-md-1", "a-md-2"], ["a-mn-1", "a-mn-2"], ["mira-A1", "mira-A2"])]
    report("Mira", stages, rows)


def arm(name, runs, scores):
    evals = evaluations(scores)
    label = Path(runs).name
    rows = []
    for number in (1, 2, 3):
        for case_dir in sorted(Path(f"{runs}-p{number}").glob("*-r1")):
            verdict = evals.get(case_dir.name, {}).get(f"{label}-p{number}-drafted")
            if verdict is not None and (case_dir / "result.json").exists():
                rows += labelled(verdict, our_drafts(case_dir))
    stages = [stage_counts(evals, [f"{label}-p{n}{suffix}" for n in (1, 2, 3)]) for suffix in ("-drafted", "-afterfilter", "")]
    report(name, stages, rows)


def main():
    mira()
    for name, runs, scores in ARMS:
        arm(name, runs, scores)
    for extra in sys.argv[1:]:
        name, runs, scores = extra.split("=")
        arm(name, runs, scores)


if __name__ == "__main__":
    main()
