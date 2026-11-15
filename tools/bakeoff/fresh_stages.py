#!/usr/bin/env python3
"""On the fresh corpus: how often a drafted finding is a true positive, by the stage that held it back.
Labels come from the judge's verdicts on the drafted sets; a diagnostic, not the registered measure.

    python3 tools/bakeoff/fresh_stages.py [judge] [fresh directory]
"""
import json, glob, sys, collections
from pathlib import Path
F = sys.argv[2] if len(sys.argv) > 2 else "/tmp/bakeoff/fresh"
J = sys.argv[1] if len(sys.argv) > 1 else "openai-gpt-6.1-sol"
def key(c): return f'{c["title"]}\n{c["path"]}:{c["line"]}\n{c["body"]}'.strip()
def kind(stage):
    if stage == "posted": return "posted"
    if "below the floor" in stage: return "held by the floor"
    if "repeats" in stage: return "held as a duplicate"
    if "unsupported" in stage: return "held by the critique (unsupported)"
    if "plausible" in stage: return "held by the critique (plausible)"
    return "other: " + stage[:30]
for arm in ("d1c", "d3m"):
    ev = json.loads(Path(f"{F}/scores-{arm}-{J}/scores.json").read_text())["evaluations"]
    tally = collections.defaultdict(lambda: [0, 0]); conf = collections.defaultdict(lambda: [0, 0]); merged = 0
    for p in (1, 2, 3):
        for case_dir in sorted(Path(f"{F}/runs/{arm}-p{p}").glob("*-r1")):
            v = ev.get(case_dir.name, {}).get(f"{arm}-p{p}-drafted")
            if v is None: continue
            tp = {c["matched_candidate"].strip() for c in v["true_positives"]}
            fp = {c["candidate"].strip() for c in v["false_positives"]}
            r = json.loads((case_dir / "result.json").read_text())["result"]
            drafts = [(c, "posted") for c in r["comments"]] + [(w, w["reason"]) for w in r.get("withheld", [])]
            for c, why in drafts:
                k = key(c)
                if k in tp: hit = 1
                elif k in fp: hit = 0
                else: merged += 1; continue
                tally[kind(why)][0] += hit; tally[kind(why)][1] += 1
                b = "0.9+" if float(c.get("confidence") or 0) >= 0.9 else ("0.8" if float(c.get("confidence") or 0) >= 0.8 else ("0.7" if float(c.get("confidence") or 0) >= 0.7 else "<0.7"))
                conf[b][0] += hit; conf[b][1] += 1
    tot = sum(v[1] for v in tally.values()); hits = sum(v[0] for v in tally.values())
    print(f"{arm}: {tot} labelled drafts, {hits} true ({merged} merged by the scorer's own dedup)")
    for k, (h, n) in sorted(tally.items(), key=lambda kv: -kv[1][1]): print(f"   {k:38} {h:3}/{n:3} true = {h/n:.0%}")
    print("   by confidence: " + ", ".join(f"{b} {conf[b][0]}/{conf[b][1]} ({conf[b][0]/conf[b][1]:.0%})" for b in ("<0.7", "0.7", "0.8", "0.9+") if conf[b][1]))
