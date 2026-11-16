#!/usr/bin/env python3
"""Rebuild what a deep review drafted from a stored run, so each stage can be scored.

A deep review withholds findings below its confidence floor, over its cap on
low-priority findings, or graded unsupported by its critique, and `-format json`
lists them under `withheld` with the stage that held them. run_nitpick.py keeps
that list beside the posted comments. This writes a run directory whose comments
are the drafts (posted plus withheld) and one whose comments are what passed the
filter (posted plus what the critique or the repository check withheld), in the
layout the upstream scorer reads:

    python3 tools/bakeoff/deep_funnel.py --run /tmp/bakeoff/runs/d1-p1 --out /tmp/bakeoff/runs/d1-p1
    # writes /tmp/bakeoff/runs/d1-p1-drafted and /tmp/bakeoff/runs/d1-p1-afterfilter

With `--stages notrefuted` it writes what `review.verify: not_refuted` would have
posted from a run made under `confirmed`: the posted comments plus the ones the
check could not settle either way.
"""

import argparse
import json
import shutil
from pathlib import Path

SECOND_STAGE = {"the deep-review critique", "a repository check"}
CHECK = "a repository check"
UNSETTLED = "could not be confirmed against the repository"
STAGES = {
    "drafted": lambda w: True,
    "afterfilter": lambda w: w.get("withheld_by") in SECOND_STAGE,
    "notrefuted": lambda w: w.get("withheld_by") == CHECK and w.get("reason", "").startswith(UNSETTLED),
}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True, help="prefix; <out>-<stage> is written for each stage")
    ap.add_argument("--stages", default="drafted,afterfilter", help="comma-separated: drafted, afterfilter, notrefuted")
    args = ap.parse_args()
    unfinished = [c.name for c in sorted(args.run.glob("*-r1")) if not (c / "result.json").exists()]
    if unfinished:
        raise SystemExit(f"{args.run} has reviews with no result yet, so no stage was written: {', '.join(unfinished)}")
    unknown = [name for name in args.stages.split(",") if name not in STAGES]
    if unknown:
        raise SystemExit(f"no such stage: {', '.join(unknown)}")
    stages = {name: STAGES[name] for name in args.stages.split(",")}
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
    print(f"{args.run}: " + ", ".join(f"{total} {stage}" for stage, total in counts.items()))


if __name__ == "__main__":
    main()
