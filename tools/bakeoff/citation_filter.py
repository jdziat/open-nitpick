#!/usr/bin/env python3
"""Does requiring a verbatim citation separate real findings from noise?

Mira discards a comment whose quoted code is not in the diff. This applies the same
rule to nitpick's own `citation` field on stored runs: keep a comment only
when its citation is present and appears, whitespace-normalised, in the pull
request's diff. Scored from the judge's existing verdicts, so no model call is made.

    python3 tools/bakeoff/citation_filter.py
"""

import json
import re
from pathlib import Path


def norm(s):
    return re.sub(r"\s+", " ", s).strip()


def cand(c):
    return f"{c['title']}\n{c['path']}:{c['line']}\n{c['body']}"


def covers(kept, m):
    return any(k == m or k in m or m in k for k in kept)


def run(scores, label_dirs, inputs):
    ev = json.loads(Path(scores).read_text())["evaluations"]
    tot = {"all": [0, 0, 0], "cited": [0, 0, 0]}
    n = {"all": 0, "cited": 0, "nocit": 0}
    for label, folder in label_dirs:
        for d in sorted(Path(folder).glob("*-r1")):
            e = ev.get(d.name, {}).get(label)
            if not e:
                continue
            diff = norm(
                (Path(inputs) / d.name[:-3] / "pr.diff").read_text(errors="replace")
            )
            cs = json.loads((d / "result.json").read_text())["result"]["comments"]
            tpm = [t["matched_candidate"] for t in e["true_positives"]]
            fpm = [f["candidate"] for f in e["false_positives"]]
            gold = e["tp"] + e["fn"]
            for mode in ("all", "cited"):
                keep = set()
                for c in cs:
                    cit = norm(c.get("citation") or "")
                    ok = mode == "all" or (cit and cit in diff)
                    if ok:
                        keep.add(cand(c))
                tp = sum(1 for m in tpm if covers(keep, m))
                fp = sum(1 for f in fpm if f in keep)
                tot[mode][0] += tp
                tot[mode][1] += fp
                tot[mode][2] += gold - tp
                n[mode] += len(keep)
            n["nocit"] += sum(1 for c in cs if not norm(c.get("citation") or ""))
    for mode, (tp, fp, fn) in tot.items():
        p = tp / (tp + fp)
        r = tp / (tp + fn)
        print(
            f"  {mode:6s} comments {n[mode]:4d}  TP {tp:3d} FP {fp:3d} FN {fn:3d}  P {p * 100:5.1f} R {r * 100:5.1f} F1 {2 * p * r / (p + r):.3f}"
        )
    print("  comments with no citation at all:", n["nocit"])


H = "/tmp/bakeoff/heldout"
print("held-out, unverified R1-build passes")
run(
    H + "/scores/scores.json",
    [(f"h-nit-{p}", f"{H}/runs/nit-p{p}") for p in (1, 2, 3)],
    H + "/inputs",
)
print("tuning set, R1 passes")
run(
    "/tmp/bakeoff/scores-R1/scores.json",
    [(f"r1-p{p}", f"/tmp/bakeoff/runs/r1-p{p}") for p in (1, 2, 3)],
    "/tmp/bakeoff/inputs",
)
