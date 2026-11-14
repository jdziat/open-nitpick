#!/usr/bin/env python3
"""Build a two-commit git repository for each benchmark snapshot.

open-nitpick reviews a checkout, so the comparison gives it one: the snapshot's
head tree as the working tree, with the pull request as the last commit.

    base commit  = head tree with the diff reverse-applied
    head commit  = head tree

    python3 tools/bakeoff/build_repos.py --inputs /tmp/bakeoff/inputs --out /tmp/bakeoff/repos [--only calcom-4]

A case whose diff does not reverse-apply cleanly is reported and left out; it is
never reviewed against a guessed base.
"""
import argparse
import json
import shutil
import subprocess
import sys
from pathlib import Path

GIT = ["git", "-c", "user.name=bakeoff", "-c", "user.email=bakeoff@example.invalid", "-c", "commit.gpgsign=false"]


def run(args, cwd, **kw):
    return subprocess.run(args, cwd=cwd, capture_output=True, text=True, **kw)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--inputs", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--only", default="")
    args = ap.parse_args()
    selection = json.loads((args.inputs / "selection.json").read_text())
    only = {x for x in args.only.split(",") if x}
    failed = 0
    for case in selection["cases"]:
        name = case["case"]
        if only and name not in only:
            continue
        dest = args.out / name
        if (dest / ".git").exists():
            print(f"{name}: exists", file=sys.stderr)
            continue
        snap = args.inputs / name / "snapshot"
        roots = [p for p in snap.iterdir() if p.is_dir()]
        tree = roots[0] if len(roots) == 1 else snap
        if dest.exists():
            shutil.rmtree(dest)
        shutil.copytree(tree, dest, symlinks=True)
        diff = (args.inputs / name / "pr.diff").resolve()
        steps = [
            (["git", "init", "-q"], "init"),
            (["git", "apply", "-R", "--whitespace=nowarn", str(diff)], "reverse apply"),
            (["git", "add", "-A", "-f"], "stage base"),
            (GIT + ["commit", "-q", "--no-verify", "-m", "base"], "commit base"),
            (["git", "apply", "--whitespace=nowarn", str(diff)], "apply"),
            (["git", "add", "-A", "-f"], "stage head"),
            (GIT + ["commit", "-q", "--no-verify", "-m", "head"], "commit head"),
        ]
        ok = True
        for cmd, label in steps:
            r = run(cmd, dest)
            if r.returncode != 0:
                print(f"{name}: {label} failed: {r.stderr.strip()[:300]}", file=sys.stderr)
                ok = False
                break
        if not ok:
            failed += 1
            shutil.rmtree(dest, ignore_errors=True)
            continue
        print(f"{name}: ok", file=sys.stderr)
    print(f"failed cases: {failed}", file=sys.stderr)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
