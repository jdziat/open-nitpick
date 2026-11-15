#!/usr/bin/env python3
"""Per-arm breakdown on the fresh corpus: matches and misses by golden category, comments per review.

    python3 tools/bakeoff/fresh_breakdown.py [judge] [fresh directory]
"""
import glob, json, collections, statistics, sys
J = sys.argv[1] if len(sys.argv) > 1 else "openai-gpt-6.1-sol"
F = sys.argv[2] if len(sys.argv) > 2 else "/tmp/bakeoff/fresh"
cats = {}
for e in json.load(open(glob.glob(f"{F}/fresh-benchmark-*/offline/golden_comments/fresh.json")[0])):
    for g in e["comments"]:
        cats[g["comment"]] = g["category"]
order = ["bug", "security", "concurrency", "data", "api", "perf", "test_gap", "doc_defect", "style", "speculative"]
for arm in ("mira", "d1c", "d3m"):
    path = f"{F}/scores-{arm}-{J}/scores.json"
    try:
        ev = json.load(open(path))["evaluations"]
    except FileNotFoundError:
        print(arm, "not scored"); continue
    hit, miss = collections.Counter(), collections.Counter()
    fp = 0; posted = []
    for case, per in ev.items():
        for p in (1, 2, 3):
            v = per[f"{arm}-p{p}"]
            fp += v["fp"]; posted.append(v["tp"] + v["fp"])
            for t in v["true_positives"]: hit[cats.get(t["golden_comment"], "?")] += 1
            for f in v["false_negatives"]: miss[cats.get(f["golden_comment"], "?")] += 1
    print(f"{arm}: matched {sum(hit.values())}, missed {sum(miss.values())}, false positives {fp}, "
          f"candidates per review {statistics.mean(posted):.2f}")
    print("   " + "  ".join(f"{c} {hit[c]}/{hit[c] + miss[c]}" for c in order if hit[c] + miss[c]))
