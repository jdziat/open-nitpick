#!/usr/bin/env python3
"""Does the reviewer's own confidence separate real findings from noise?

For every published comment carrying a confidence, the label is whether the
judge matched it to a golden comment. Prints the area under the ROC curve (the
chance a matched comment outranks an unmatched one) and, for a range of floors,
how many comments and matched comments a floor would keep. This is stage R1's
gate in docs/remediation-mira.md: an area of 0.70 or more to build the floor,
below 0.65 to abandon it.

    python3 tools/bakeoff/confidence_auc.py --scores /tmp/bakeoff/scores-R1/scores.json \\
        --run r1-p1=/tmp/bakeoff/nitpick-r1-p1 --run r1-p2=/tmp/bakeoff/nitpick-r1-p2
"""
import argparse
import json
from pathlib import Path


def candidate(c):
    return f"{c['title']}\n{c['path']}:{c['line']}\n{c['body']}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--scores", action="append", required=True)
    ap.add_argument("--run", action="append", required=True, help="label=run directory")
    args = ap.parse_args()
    evals = {}
    for p in args.scores:
        for case, per in json.loads(Path(p).read_text())["evaluations"].items():
            evals.setdefault(case, {}).update(per)
    points, missing = [], 0
    for spec in args.run:
        label, folder = spec.split("=", 1)
        for case_dir in sorted(Path(folder).glob("*-r1")):
            ev = evals.get(case_dir.name, {}).get(label)
            if ev is None:
                continue
            matched = [t["matched_candidate"] for t in ev["true_positives"]]
            for c in json.loads((case_dir / "result.json").read_text())["result"]["comments"]:
                conf = c.get("confidence")
                if conf is None:
                    missing += 1
                    continue
                cand = candidate(c)
                points.append((float(conf), any(cand == m or cand in m or m in cand for m in matched)))
    pos = [c for c, y in points if y]
    neg = [c for c, y in points if not y]
    print(f"comments with a confidence: {len(points)} ({len(pos)} matched), without: {missing}")
    if not pos or not neg:
        raise SystemExit("need both matched and unmatched comments")
    wins = sum((p > n) + 0.5 * (p == n) for p in pos for n in neg)
    auc = wins / (len(pos) * len(neg))
    verdict = "build the floor" if auc >= 0.70 else ("abandon the floor" if auc < 0.65 else "inconclusive")
    print(f"area under the ROC curve: {auc:.3f}  ({verdict})")
    print(f"{'floor':>6} {'kept':>5} {'matched kept':>13} {'precision':>9} {'share of matched kept':>22}")
    for floor in (0.0, 0.5, 0.6, 0.7, 0.75, 0.8, 0.85, 0.9, 0.95):
        kept = [(c, y) for c, y in points if c >= floor]
        mk = sum(y for _, y in kept)
        print(f"{floor:6.2f} {len(kept):5} {mk:13} {mk / len(kept) if kept else 0:9.1%} {mk / len(pos):22.1%}")


if __name__ == "__main__":
    main()
