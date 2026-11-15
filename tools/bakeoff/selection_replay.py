#!/usr/bin/env python3
"""Replay selection rules on the stored drafts of deep-review arms, across corpora.

Two questions, answered from the judge's verdicts on each arm's drafted set and without a model call:

  signals   does a property of a draft (severity, confidence, agreement between agents, file kind,
            class, volume) separate the findings the judge matched from the rest, on every corpus?
  rules     what would a rule built from those properties have posted instead of today's floor,
            critique and cap, and what F1 would that give?

A draft's label is the verdict on the candidate it became, so F1 here is an estimate that ranks rules and
does not replace scoring the one chosen. Rules read the drafts only, never the critique's grade.

    python3 tools/bakeoff/selection_replay.py [signals|rules]
"""

import collections
import json
import re
import sys
from pathlib import Path

JUDGE = "openai-gpt-6.1-sol"
SEVERITY = {"critical": 4, "error": 3, "warning": 2, "info": 1, "nit": 0}
GOLDEN = {"tuning": 204, "held-out": 315, "fresh": 282}  # golden comments over three passes, every category
CORPORA = [
    ("tuning", "tuning D1c", "/tmp/bakeoff/runs/d1c-p{p}", f"/tmp/bakeoff/scores-d1c-{JUDGE}", "d1c-p{p}-drafted"),
    ("tuning", "tuning D3m", "/tmp/bakeoff/runs/d3m-p{p}", f"/tmp/bakeoff/scores-D3m-{JUDGE}", "d3m-p{p}-drafted"),
    ("held-out", "held-out D4a", "/tmp/bakeoff/heldout/runs/h-d3o-p{p}", f"/tmp/bakeoff/heldout/scores-h-d3o-{JUDGE}", "h-d3o-p{p}-drafted"),
    ("fresh", "fresh D1c", "/tmp/bakeoff/fresh/runs/d1c-p{p}", f"/tmp/bakeoff/fresh/scores-d1c-{JUDGE}", "d1c-p{p}-drafted"),
    ("fresh", "fresh D3m", "/tmp/bakeoff/fresh/runs/d3m-p{p}", f"/tmp/bakeoff/fresh/scores-d3m-{JUDGE}", "d3m-p{p}-drafted"),
]
TOKEN = re.compile(r"[a-z0-9_]+")


def jaccard(a, b):
    return len(a & b) / len(a | b) if a and b else 0.0


def words(text):
    return set(text.lower().split())


def token_overlap(a, b):
    ta, tb = set(TOKEN.findall(a.lower())), set(TOKEN.findall(b.lower()))
    if len(ta) < 8 or len(tb) < 8:
        return 0.0
    return jaccard(ta, tb)


def is_duplicate(a, b):
    """The engine's composite duplicate rule (internal/review/deepselect.go), ported."""
    title = jaccard(words(a["title"]), words(b["title"]))
    if a["path"] != b["path"]:
        return title >= 0.8 and jaccard(words(a["body"]), words(b["body"])) >= 0.5
    if a["line"] == b["line"] and a["class"] == b["class"]:
        return True
    near = abs(a["line"] - b["line"]) <= 3
    if a["class"] == b["class"] and near and token_overlap(a["title"] + " " + a["body"], b["title"] + " " + b["body"]) >= 0.25:
        return True
    if a["line"] == b["line"] and title >= 0.2:
        return True
    return title >= 0.6


def candidate_key(draft):
    """The scorer's rendering of a candidate, which is how a verdict names it."""
    return f'{draft["title"]}\n{draft["path"]}:{draft["line"]}\n{draft["body"]}'.strip()


def file_kind(path):
    p = path.lower()
    if re.search(r"(^|/)(tests?|spec|specs|__tests__)/|_test\.|\.test\.|\.spec\.|test_", p):
        return "test"
    if p.endswith((".md", ".rst", ".txt", ".adoc")) or "/docs/" in p:
        return "doc"
    if p.endswith((".yml", ".yaml", ".json", ".toml", ".xml", ".gradle", ".properties", ".lock", ".cfg", ".ini")):
        return "config"
    return "code"


def load_reviews(runs, scores, label):
    """Each review of an arm as a list of drafts, every draft carrying its verdict (None when merged)."""
    evaluations = json.loads((Path(scores) / "scores.json").read_text())["evaluations"]
    reviews = []
    for pass_number in (1, 2, 3):
        for case_dir in sorted(Path(runs.format(p=pass_number)).glob("*-r1")):
            verdict = evaluations.get(case_dir.name, {}).get(label.format(p=pass_number))
            if verdict is None:
                continue
            true = {c["matched_candidate"].strip() for c in verdict["true_positives"]}
            false = {c["candidate"].strip() for c in verdict["false_positives"]}
            result = json.loads((case_dir / "result.json").read_text())["result"]
            drafts = [dict(c, stage="posted") for c in result["comments"]]
            drafts += [dict(w, stage=w["reason"]) for w in result.get("withheld", [])]
            for draft in drafts:
                draft["line"] = int(draft["line"])
                key = candidate_key(draft)
                draft["hit"] = 1 if key in true else (0 if key in false else None)
            reviews.append(drafts)
    return reviews


def label_rows(reviews):
    """Labelled drafts with the properties a rule could read, agreement counted over every draft."""
    rows = []
    for drafts in reviews:
        for draft in drafts:
            if draft["hit"] is not None:
                twins = sum(1 for other in drafts if other is not draft and is_duplicate(draft, other))
                rows.append(dict(draft, twins=twins, volume=len(drafts)))
    return rows


def clusters(reviews):
    """Each review's drafts grouped by the duplicate rule, as the engine groups them; size is the agreement."""
    out = []
    for drafts in reviews:
        left = list(range(len(drafts)))
        order = sorted(left, key=lambda i: (-SEVERITY[drafts[i]["severity"]], -float(drafts[i].get("confidence") or 0)))
        for i in order:
            if i not in left:
                continue
            members = [j for j in left if j == i or is_duplicate(drafts[i], drafts[j])]
            left = [j for j in left if j not in members]
            labelled = [drafts[j]["hit"] for j in members if drafts[j]["hit"] is not None]
            if labelled:
                out.append({
                    "size": len(members),
                    "hit": max(labelled),
                    "confidence": max(float(drafts[j].get("confidence") or 0) for j in members),
                    "severity": max(SEVERITY[drafts[j]["severity"]] for j in members),
                    "posted": any(drafts[j]["stage"] == "posted" for j in members),
                })
    return out


def confidence_band(draft):
    x = float(draft.get("confidence") or 0)
    for floor, name in ((0.9, "0.9+"), (0.8, "0.8"), (0.7, "0.7"), (0.6, "0.6")):
        if x >= floor:
            return name
    return "<0.6"


SIGNALS = [
    ("severity", lambda d: "error+" if SEVERITY[d["severity"]] >= 3 else d["severity"], ["error+", "warning", "info", "nit"]),
    ("confidence", confidence_band, ["<0.6", "0.6", "0.7", "0.8", "0.9+"]),
    ("twin agents", lambda d: ["0 twins", "1 twin"][min(d["twins"], 1)] if d["twins"] < 2 else "2+ twins", ["0 twins", "1 twin", "2+ twins"]),
    ("file kind", lambda d: file_kind(d["path"]), ["code", "test", "doc", "config"]),
    ("drafts in the review", lambda d: "1-2" if d["volume"] <= 2 else ("3-5" if d["volume"] <= 5 else "6+"), ["1-2", "3-5", "6+"]),
]

RULES = {
    "posted today (floor .7, critique, cap)": lambda c: c["posted"],
    "floor .7, no critique": lambda c: c["confidence"] >= 0.7,
    "agreed (2+) or conf .9+": lambda c: c["size"] >= 2 or c["confidence"] >= 0.9,
    "agreed (2+), any confidence": lambda c: c["size"] >= 2,
    "agreed (2+) or error+ at .8": lambda c: c["size"] >= 2 or (c["severity"] >= 3 and c["confidence"] >= 0.8),
    "agreed (2+) or conf .8+": lambda c: c["size"] >= 2 or c["confidence"] >= 0.8,
}


def estimated_f1(selected, golden):
    true = sum(c["hit"] for c in selected)
    precision = true / len(selected) if selected else 0.0
    recall = true / golden
    return 2 * precision * recall / (precision + recall) if precision + recall else 0.0


def show_signals():
    for _, name, runs, scores, label in CORPORA:
        rows = label_rows(load_reviews(runs, scores, label))
        print(f"\n== {name}: {len(rows)} labelled drafts, {sum(r['hit'] for r in rows)} true")
        for signal, feature, order in SIGNALS:
            groups = collections.defaultdict(lambda: [0, 0])
            for row in rows:
                groups[feature(row)][0] += row["hit"]
                groups[feature(row)][1] += 1
            cells = [f"{k}: {groups[k][0]}/{groups[k][1]} ({groups[k][0] / groups[k][1]:.0%})" for k in order if groups[k][1]]
            print(f"   {signal:22} " + "  ".join(cells))


def show_rules():
    names = [c[1] for c in CORPORA]
    print(f"{'rule':42}" + "".join(f"{n:>16}" for n in names))
    results = {}
    for corpus, name, runs, scores, label in CORPORA:
        grouped = clusters(load_reviews(runs, scores, label))
        for rule_name, rule in RULES.items():
            selected = [c for c in grouped if rule(c)]
            results[(name, rule_name)] = (estimated_f1(selected, GOLDEN[corpus]), len(selected))
    for rule_name in RULES:
        print(f"{rule_name:42}" + "".join(f"{results[(n, rule_name)][0]:>9.3f} ({results[(n, rule_name)][1]:>3})" for n in names))


if __name__ == "__main__":
    which = sys.argv[1] if len(sys.argv) > 1 else "both"
    if which in ("signals", "both"):
        show_signals()
    if which in ("rules", "both"):
        print()
        show_rules()
