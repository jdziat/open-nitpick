#!/usr/bin/env python3
"""Estimate what an evidence-graded critique pass would do to a run's findings.

Mira drops a draft comment after a critic grades how well the shown diff
supports it: proven, plausible or unsupported, with a keep rule held in code
(proven keeps; plausible keeps only at warning severity or worse; unsupported
drops). open-nitpick has no such pass, so this applies the same idea to a
finished run, offline, and writes a new run directory the scorer can read.

The critic prompt is a paraphrase of Mira's, not a copy. The grade is requested
in one batched call per pull request, with the diff section of each cited file.

    python3 tools/bakeoff/critique_filter.py --run /tmp/bakeoff/nitpick-pass1 \
        --inputs /tmp/bakeoff/inputs --out /tmp/bakeoff/nitpick-pass1-critique
"""
import argparse
import json
import os
import re
import shutil
import sys
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from run_nitpick import parse as parse_review  # noqa: E402

MODEL = "deepseek/deepseek-v4.1-flash-1017"
SEV_ORDER = {"critical": 0, "error": 1, "warning": 2, "info": 3, "nit": 4}


def diff_sections(diff: str):
    marks = list(re.finditer(r"^diff --git a/(.*?) b/(.*?)$", diff, re.M))
    out = {}
    for i, m in enumerate(marks):
        end = marks[i + 1].start() if i + 1 < len(marks) else len(diff)
        out[m.group(2)] = diff[m.start():end]
    return out


def window(section: str, line: int, radius=35, limit=3500):
    """The part of one file's diff nearest the cited line."""
    lines = section.splitlines()
    new = 0
    numbered = []
    for ln in lines:
        h = re.match(r"^@@ -\d+(?:,\d+)? \+(\d+)", ln)
        if h:
            new = int(h.group(1)) - 1
            numbered.append((None, ln))
            continue
        if ln.startswith("-") and not ln.startswith("---"):
            numbered.append((None, ln))
        elif ln.startswith("+++") or ln.startswith("---") or ln.startswith("diff ") or ln.startswith("index "):
            continue
        else:
            new += 1
            numbered.append((new, ln))
    keep = [t for n, t in numbered if n is None or abs(n - line) <= radius]
    text = "\n".join(keep)
    return text[:limit]


def call(prompt: str, key: str):
    body = json.dumps({
        "model": MODEL,
        "messages": [{"role": "user", "content": prompt}],
        "temperature": 0,
        "reasoning": {"enabled": False},
        "response_format": {"type": "json_object"},
        "max_tokens": 4096,
    }).encode()
    req = urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", data=body,
                                 headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
    last = None
    for _ in range(3):
        try:
            with urllib.request.urlopen(req, timeout=300) as r:
                data = json.load(r)
            return json.loads(data["choices"][0]["message"]["content"])
        except Exception as e:
            last = e
    raise last


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", type=Path, required=True)
    ap.add_argument("--inputs", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--keep-plausible-at", default="warning")
    ap.add_argument("--grades-from", type=Path, default=None,
                    help="reuse the grades a previous critique run stored, and only reapply the keep rule")
    args = ap.parse_args()
    key = os.environ.get("OPENROUTER_API_KEY", "")
    if args.out.exists():
        sys.exit(f"{args.out} exists")
    shutil.copytree(args.run, args.out)
    floor = SEV_ORDER[args.keep_plausible_at]
    totals = {"in": 0, "kept": 0}
    for case_dir in sorted(args.out.glob("*-r1")):
        case = case_dir.name[:-3]
        res = json.loads((case_dir / "result.json").read_text())
        comments = res["result"]["comments"]
        if not comments:
            continue
        # Severity is read back from the saved review text: the stored comments
        # predate it, and a keep rule that cannot see severity drops every
        # plausible finding.
        meta = parse_review((case_dir / "stdout.txt").read_text())
        if len(meta) == len(comments):
            for c, m in zip(comments, meta):
                c["severity"], c["class"] = m["severity"], m["class"]
        sections = diff_sections((args.inputs / case / "pr.diff").read_text(errors="replace"))
        entries = []
        for i, c in enumerate(comments):
            sec = sections.get(c["path"], "")
            entries.append(
                f"[{i}] {c['path']}:{c['line']} ({c.get('severity', '?')})\n    Title: {c['title']}\n"
                f"    Body: {c['body'][:600]}\n    Diff around the cited lines:\n{window(sec, c['line']) if sec else '(file not in the diff)'}\n"
            )
        prompt = (
            "You are grading draft pull request comments written by another reviewer. For each, grade how well the "
            "diff shown supports the claimed issue.\n\n"
            "- proven: the shown code demonstrates the issue and the reasoning is correct.\n"
            "- plausible: consistent with the shown code but depends on behaviour or code not shown. Real findings often "
            "land here; this is a valid grade.\n"
            "- unsupported: the shown code contradicts the claim, the reasoning about language semantics is wrong, or it is "
            "a style preference presented as a defect.\n\n"
            "Grade the evidence; do not construct counter-arguments. Grade the code fact, not the consequence.\n\n"
            'Reply with JSON: {"grades": [{"index": 0, "evidence": "proven|plausible|unsupported"}, ...]} with one entry '
            "per comment.\n\n## Draft comments\n\n" + "\n".join(entries)
        )
        try:
            if args.grades_from:
                stored = json.loads((args.grades_from / case_dir.name / "result.json").read_text())
                grades = {int(k): v for k, v in stored["critique"]["grades"].items()}
            else:
                grades = {g["index"]: g["evidence"] for g in call(prompt, key)["grades"]}
        except Exception as e:  # a failed critique keeps everything, and says so
            print(f"{case}: critique failed ({e}); keeping all", file=sys.stderr)
            grades = {i: "proven" for i in range(len(comments))}
        kept = []
        for i, c in enumerate(comments):
            g = grades.get(i, "proven")
            sev = SEV_ORDER.get(str(c.get("severity", "info")).lower(), 3)
            c["critique"] = g
            if g == "proven" or (g == "plausible" and sev <= floor):
                kept.append(c)
        res["result"]["comments"] = kept
        res["critique"] = {"model": MODEL, "before": len(comments), "after": len(kept),
                           "grades": {str(k): v for k, v in grades.items()}}
        (case_dir / "result.json").write_text(json.dumps(res, indent=2))
        totals["in"] += len(comments)
        totals["kept"] += len(kept)
        print(f"{case}: {len(comments)} -> {len(kept)}", flush=True)
    print(f"total {totals['in']} -> {totals['kept']}")


if __name__ == "__main__":
    main()
