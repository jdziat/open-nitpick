#!/usr/bin/env python3
"""Rebuild what Mira drafted at each stage of its pipeline from a stored run.

Mira's result carries an audit trail of every comment each stage removed (the
noise filter, then the self-critique) but not the removed comments' text. The
text is in the stored model calls: every draft was submitted as JSON in a
`submit_review` call. This joins the two, so the drafts, the drafts left after
the noise filter, and the posted comments can each be scored by the same judge:

    drafted     = posted + dropped by the noise filter + dropped by the critique
    afternoise  = posted + dropped by the critique

It writes one run directory per stage in the layout the upstream scorer reads,
so `score_snapshot_runs.py --run label=<dir>` works on them unchanged. The join
is exact: for every run checked, the rebuilt draft count equals the audit's.

    python3 tools/bakeoff/mira_funnel.py --run /tmp/bakeoff/heldout/mira-p1/mira/deepseek \\
        --out /tmp/bakeoff/heldout/mira-funnel/p1
"""

import argparse
import json
import shutil
from pathlib import Path


def draft_index(case_dir):
    """Every comment submitted in any call of one review, by (path, title[:80])."""
    index = {}
    for call_file in sorted(case_dir.glob("call-*.json")):
        call = json.loads(call_file.read_text())
        response = call.get("response")
        payloads = []
        if isinstance(response, str) and '"comments"' in response:
            payloads.append(response)
        elif isinstance(response, dict):
            for tool_call in response.get("tool_calls") or []:
                arguments = (tool_call.get("function") or {}).get("arguments")
                if arguments and '"comments"' in arguments:
                    payloads.append(arguments)
        for payload in payloads:
            try:
                data = json.loads(payload)
            except ValueError:
                continue
            for comment in data.get("comments") or []:
                if isinstance(comment, dict):
                    index[(comment.get("path"), (comment.get("title") or "")[:80])] = comment
    return index


def plain(comment):
    return {
        "title": comment.get("title", ""),
        "path": comment.get("path"),
        "line": comment.get("line"),
        "body": comment.get("body", ""),
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", type=Path, required=True, help="a Mira run directory holding run.json and <case>-r1")
    ap.add_argument("--out", type=Path, required=True, help="prefix; <out>-drafted and <out>-afternoise are written")
    args = ap.parse_args()
    missing = 0
    outputs = {}
    for variant in ("drafted", "afternoise"):
        target = Path(f"{args.out}-{variant}")
        if target.exists():
            raise SystemExit(f"{target} exists")
        target.mkdir(parents=True)
        shutil.copy(args.run / "run.json", target / "run.json")
        outputs[variant] = target
    for case_dir in sorted(args.run.glob("*-r1")):
        report = json.loads((case_dir / "result.json").read_text())
        audit = report["result"]["audit"]
        index = draft_index(case_dir)
        posted = [plain(c) for c in report["result"]["comments"]]

        def dropped(stage, index=index, audit=audit):
            nonlocal missing
            out = []
            for entry in (e for e in audit if e.get("stage") == stage):
                comment = index.get((entry["path"], entry["title"][:80]))
                if comment is None:
                    missing += 1
                else:
                    out.append(plain(comment))
            return out

        after_noise = posted + dropped("self_critique")
        drafted = after_noise + dropped("noise_filter")
        for variant, comments in (("drafted", drafted), ("afternoise", after_noise)):
            target = outputs[variant] / case_dir.name
            target.mkdir()
            shutil.copy(case_dir / "status.json", target / "status.json")
            body = {k: report.get(k) for k in ("manifest", "duration_seconds", "index_was_empty", "jit_needed", "source_reads", "model_usage")}
            body["result"] = {"comments": comments}
            (target / "result.json").write_text(json.dumps(body))
    print(f"wrote {outputs['drafted']} and {outputs['afternoise']}; {missing} dropped comments had no body")


if __name__ == "__main__":
    main()
