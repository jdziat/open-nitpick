#!/usr/bin/env python3
"""Per-arm breakdown on the fresh corpus: matches and misses by golden category, comments per review.

    python3 tools/bakeoff/fresh_breakdown.py [judge] [fresh directory]
"""

import collections
import json
import statistics
import sys
from pathlib import Path

JUDGE = sys.argv[1] if len(sys.argv) > 1 else "openai-gpt-6.1-sol"
FRESH = Path(sys.argv[2] if len(sys.argv) > 2 else "/tmp/bakeoff/fresh")
ORDER = ["bug", "security", "concurrency", "data", "api", "perf", "test_gap", "doc_defect", "style", "speculative"]


def golden_categories():
    """Golden comment text to its category."""
    golden = next(FRESH.glob("fresh-benchmark-*/offline/golden_comments/fresh.json"))
    return {g["comment"]: g["category"] for e in json.loads(golden.read_text()) for g in e["comments"]}


def main():
    categories = golden_categories()
    for arm in ("mira", "d1c", "d3m"):
        path = FRESH / f"scores-{arm}-{JUDGE}" / "scores.json"
        if not path.exists():
            print(arm, "not scored")
            continue
        hit, miss = collections.Counter(), collections.Counter()
        false_positives, candidates = 0, []
        for per_label in json.loads(path.read_text())["evaluations"].values():
            for pass_number in (1, 2, 3):
                verdict = per_label[f"{arm}-p{pass_number}"]
                false_positives += verdict["fp"]
                candidates.append(verdict["tp"] + verdict["fp"])
                for t in verdict["true_positives"]:
                    hit[categories.get(t["golden_comment"], "?")] += 1
                for f in verdict["false_negatives"]:
                    miss[categories.get(f["golden_comment"], "?")] += 1
        print(f"{arm}: matched {sum(hit.values())}, missed {sum(miss.values())}, false positives {false_positives}, "
              f"candidates per review {statistics.mean(candidates):.2f}")
        print("   " + "  ".join(f"{c} {hit[c]}/{hit[c] + miss[c]}" for c in ORDER if hit[c] + miss[c]))


if __name__ == "__main__":
    main()
