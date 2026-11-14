#!/usr/bin/env python3
"""Post-hoc selectors over stored bakeoff passes: a per-pull-request cap ranked
by the reviewer's confidence, and keep-if-found-in-another-pass agreement.

Both only remove comments, so each one is scored from the judge's existing
verdicts in scores.json and no new model call is made. The unfiltered row
reproduces the published counts, which is the check that the simulation
matches the scorer. Cap sizes and the line window are chosen after seeing the
data, so the numbers flatter the selector; they are a diagnostic, not a gate.

    python3 tools/bakeoff/selectors.py --root /tmp/bakeoff/heldout
"""
import argparse
import json
from pathlib import Path


def candidate(c):
    return f"{c['title']}\n{c['path']}:{c['line']}\n{c['body']}"


def covers(kept, matched):
    return any(k == matched or k in matched or matched in k for k in kept)


def load(scores, label, folder):
    """Per case: the pass's comments, the matched and unmatched candidate text, and the golden count."""
    out = {}
    for case_dir in sorted(Path(folder).glob("*-r1")):
        e = scores.get(case_dir.name, {}).get(label)
        if not e:
            continue
        comments = json.loads((case_dir / "result.json").read_text())["result"]["comments"]
        out[case_dir.name] = (
            comments,
            [t["matched_candidate"] for t in e["true_positives"]],
            [f["candidate"] for f in e["false_positives"]],
            e["tp"] + e["fn"],
        )
    return out


def score(passes, keep_of):
    """Pooled counts when keep_of(pass_index, case, comment) chooses what stays."""
    tp = fp = fn = 0
    for i, per in enumerate(passes):
        for case, (comments, matched, unmatched, gold) in per.items():
            kept = {candidate(c) for c in comments if keep_of(i, case, c)}
            hit = sum(1 for m in matched if covers(kept, m))
            tp += hit
            fp += sum(1 for f in unmatched if f in kept)
            fn += gold - hit
    p = tp / (tp + fp)
    r = tp / (tp + fn)
    return tp, fp, fn, p, r, 2 * p * r / (p + r), 5 * p * r / (4 * p + r)


def row(name, tag, counts):
    tp, fp, fn, p, r, f1, f2 = counts
    print(f"{name:17s} {tag:>8s} {tp:4d} {fp:4d} {fn:4d} {p * 100:5.1f} {r * 100:5.1f} {f1:.3f} {f2:.3f}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", type=Path, default=Path("/tmp/bakeoff/heldout"))
    ap.add_argument("--window", type=int, default=5, help="lines apart that still count as the same finding")
    args = ap.parse_args()
    scores = json.loads((args.root / "scores/scores.json").read_text())["evaluations"]
    arms = {
        "mira": ("h-mira", [args.root / f"mira-p{p}/mira/deepseek" for p in (1, 2, 3)]),
        "unverified": ("h-nit", [args.root / f"runs/nit-p{p}" for p in (1, 2, 3)]),
        "offline-verified": ("h-ver", [args.root / f"runs/ver-p{p}-confirmed" for p in (1, 2, 3)]),
    }
    print(f"{'arm':17s} {'keep':>8s} {'TP':>4s} {'FP':>4s} {'FN':>4s} {'prec':>5s} {'rec':>5s} {'F1':>5s} {'F2':>5s}")
    for name, (prefix, folders) in arms.items():
        passes = [load(scores, f"{prefix}-{p}", f) for p, f in enumerate(folders, 1)]
        row(name, "all", score(passes, lambda i, case, c: True))
        for n in (6, 4, 3, 2):
            # Ranking is per pull request and pass, so the keep set is rebuilt per case.
            tops = {}
            for i, per in enumerate(passes):
                for case, (comments, *_rest) in per.items():
                    ranked = sorted(comments, key=lambda c: float(c.get("confidence") or 0), reverse=True)[:n]
                    tops[(i, case)] = {id(c) for c in ranked}
            row(name, f"top {n}", score(passes, lambda i, case, c, tops=tops: id(c) in tops[(i, case)]))
        for need in (2, 3):
            def agrees(i, case, c, need=need, passes=passes):
                others = [per[case][0] for j, per in enumerate(passes) if j != i and case in per]
                seen = 1 + sum(any(o["path"] == c["path"] and abs(int(o["line"]) - int(c["line"])) <= args.window for o in os) for os in others)
                return seen >= need
            row(name, f"in {need}/3", score(passes, agrees))


if __name__ == "__main__":
    main()
