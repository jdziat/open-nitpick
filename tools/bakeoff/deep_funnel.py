#!/usr/bin/env python3
"""Rebuild what a deep review drafted from a stored run, so each stage can be scored.

A deep review withholds findings below its confidence floor, over its cap on
low-priority findings, or graded unsupported by its critique, and `-format json`
lists them under `withheld` with the stage that held them. run_nitpick.py keeps
that list beside the posted comments. This writes a run directory whose comments
are the drafts (posted plus withheld) and one whose comments are what passed the
filter (posted plus what the critique withheld), in the layout the upstream
scorer reads:

    python3 tools/bakeoff/deep_funnel.py --run /tmp/bakeoff/runs/d1-p1 --out /tmp/bakeoff/runs/d1-p1
    # writes /tmp/bakeoff/runs/d1-p1-drafted and /tmp/bakeoff/runs/d1-p1-afterfilter
"""

import argparse
import json
import shutil
from pathlib import Path

CRITIQUE = "the deep-review critique"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True, help="prefix; <out>-drafted and <out>-afterfilter are written")
    args = ap.parse_args()
    unfinished = [c.name for c in sorted(args.run.glob("*-r1")) if not (c / "result.json").exists()]
    if unfinished:
        raise SystemExit(f"{args.run} has reviews with no result yet, so no stage was written: {', '.join(unfinished)}")
    stages = {"drafted": lambda w: True, "afterfilter": lambda w: w.get("withheld_by") == CRITIQUE}
    counts = {}
    for stage, keep in stages.items():
        target = Path(f"{args.out}-{stage}")
        if target.exists():
            raise SystemExit(f"{target} exists")
        target.mkdir(parents=True)
        shutil.copy(args.run / "run.json", target / "run.json")
        total = 0
        for case_dir in sorted(args.run.glob("*-r1")):
            body = json.loads((case_dir / "result.json").read_text())
            result = body["result"]
            comments = result["comments"] + [w for w in result.get("withheld", []) if keep(w)]
            total += len(comments)
            body["result"] = {"comments": comments}
            (target / case_dir.name).mkdir()
            shutil.copy(case_dir / "status.json", target / case_dir.name / "status.json")
            (target / case_dir.name / "result.json").write_text(json.dumps(body))
        counts[stage] = total
    print(f"{args.run}: {counts['drafted']} drafted, {counts['afterfilter']} after the filter")


if __name__ == "__main__":
    main()
