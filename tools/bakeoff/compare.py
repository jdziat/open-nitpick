#!/usr/bin/env python3
"""Compare two arms of a bakeoff with a paired bootstrap over pull requests.

Each arm is one or more scored passes (labels in the upstream scorer's
scores.json). Counts are summed across an arm's passes per pull request, the
pull requests are resampled with replacement, and the difference in F1,
precision and recall between the arms is reported with a 95% interval. Pairing
by pull request matters: the pull requests differ far more from each other than
two runs of one reviewer do. Counts are the scorer's raw true, false and missed
comments over every golden category, so the rates differ slightly from the
Core-profile figures in scores.json.

    python3 tools/bakeoff/compare.py --scores /tmp/bakeoff/scores-ALL/scores.json \\
        --arm base=nitpick-A1,nitpick-A2 --arm new=r1-p1,r1-p2,r1-p3
"""
import argparse
import json
import random
from pathlib import Path


def load(paths):
    evals = {}
    for p in paths:
        for case, per in json.loads(Path(p).read_text())["evaluations"].items():
            evals.setdefault(case, {}).update(per)
    return evals


def counts(evals, labels):
    """Per pull request (tp, fp, fn) summed over the arm's passes."""
    out = {}
    for case, per in evals.items():
        if not all(label in per for label in labels):
            continue
        out[case] = tuple(sum(per[label][k] for label in labels) for k in ("tp", "fp", "fn"))
    return out


def rates(rows):
    tp = sum(r[0] for r in rows)
    fp = sum(r[1] for r in rows)
    fn = sum(r[2] for r in rows)
    p = tp / (tp + fp) if tp + fp else 0.0
    r = tp / (tp + fn) if tp + fn else 0.0
    f1 = 2 * p * r / (p + r) if p + r else 0.0
    return {"precision": p, "recall": r, "f1": f1}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--scores", action="append", required=True)
    ap.add_argument("--arm", action="append", required=True, help="name=label,label,...")
    ap.add_argument("--resamples", type=int, default=10000)
    ap.add_argument("--seed", type=int, default=20261113)
    args = ap.parse_args()
    if len(args.arm) != 2:
        ap.error("give exactly two --arm values")
    evals = load(args.scores)
    arms = []
    for spec in args.arm:
        name, labels = spec.split("=", 1)
        arms.append((name, labels.split(",")))
    a, b = (counts(evals, labels) for _, labels in arms)
    cases = sorted(set(a) & set(b))
    if not cases:
        raise SystemExit("no pull request is scored for both arms")
    print(f"{len(cases)} pull requests scored in both arms")
    for (name, labels), c in zip(arms, (a, b)):
        r = rates([c[k] for k in cases])
        tp, fp, fn = (sum(c[k][i] for k in cases) for i in range(3))
        print(f"  {name:10} passes {len(labels)}  TP {tp} FP {fp} FN {fn}  "
              f"P {r['precision']:.1%} R {r['recall']:.1%} F1 {r['f1']:.3f}")
    rng = random.Random(args.seed)
    diffs = {k: [] for k in ("precision", "recall", "f1")}
    for _ in range(args.resamples):
        sample = [rng.choice(cases) for _ in cases]
        ra, rb = rates([a[k] for k in sample]), rates([b[k] for k in sample])
        for k in diffs:
            diffs[k].append(rb[k] - ra[k])
    point_a, point_b = rates([a[k] for k in cases]), rates([b[k] for k in cases])
    print(f"{arms[1][0]} minus {arms[0][0]}, 95% interval over {args.resamples} resamples:")
    for k, values in diffs.items():
        values.sort()
        lo, hi = values[int(0.025 * len(values))], values[int(0.975 * len(values)) - 1]
        scale = 1 if k == "f1" else 100
        unit = "" if k == "f1" else " pts"
        print(f"  {k:9} {(point_b[k] - point_a[k]) * scale:+.3f}{unit}  [{lo * scale:+.3f}, {hi * scale:+.3f}]"
              f"{'  excludes zero' if lo > 0 or hi < 0 else ''}")


if __name__ == "__main__":
    main()
