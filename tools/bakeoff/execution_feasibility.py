#!/usr/bin/env python3
"""Sample retained findings for blinded human feasibility adjudication, without execution."""

import argparse
from collections import Counter, defaultdict
import hashlib
import json
from pathlib import Path
import random


def finding_key(comment):
    return f'{comment.get("title", "")}\n{comment.get("path")}:{comment.get("line")}\n{comment.get("body", "")}'.strip()


def retained_pool(runs, evaluations):
    pool = []
    for run in runs:
        for case in sorted(run.glob("*-r*")):
            path = case / "result.json"
            if not path.exists():
                raise ValueError(f"retained result missing: {case}")
            raw = path.read_bytes()
            digest = hashlib.sha256(raw).hexdigest()
            result = json.loads(raw)["result"]
            if "withheld" not in result:
                raise ValueError(f"withheld coverage unavailable: {case}")
            scored = evaluations.get(case.name, {}).get(run.name + "-drafted", {})
            matched = {entry["matched_candidate"].strip() for entry in scored.get("true_positives", [])}
            unmatched = {entry["candidate"].strip() for entry in scored.get("false_positives", [])}
            for index, comment in enumerate(result.get("comments", []) + result["withheld"]):
                key = finding_key(comment)
                stratum = "matched_reference" if key in matched else "unmatched_reference" if key in unmatched else "reference_match_unknown"
                identity = hashlib.sha256(f"{run.name}:{case.name}:{digest}:{index}".encode()).hexdigest()
                # Stage, confidence, and judge labels stay out of the human view.
                finding = {key: comment.get(key) for key in ("title", "path", "line", "body")}
                pool.append({"id": identity, "stratum": stratum, "finding": finding,
                             "source": str(path), "source_sha256": digest, "case": case.name})
    return pool


def sample_pool(pool, per_stratum, seed):
    if per_stratum < 1:
        raise ValueError("per-stratum must be positive")
    groups = defaultdict(list)
    for row in pool:
        groups[row["stratum"]].append(row)
    generator = random.Random(seed)
    selected = []
    for stratum, rows in sorted(groups.items()):
        count = min(len(rows), per_stratum)
        for row in generator.sample(rows, count):
            selected.append({**row, "sampling_fraction": count / len(rows)})
    generator.shuffle(selected)
    return {"version": 1, "seed": seed, "population_by_stratum": dict(Counter(row["stratum"] for row in pool)),
            "coverage": "posted and retained withheld candidates; coverage of raw model drafts is not established",
            "selected": selected}


def human_view(sample):
    return [{"id": row["id"], "finding": row["finding"], "label": "unknown", "human_approved": False,
             "reviewer": "", "check_kind": "none", "experiment": "", "refutation_implication": ""}
            for row in sample["selected"]]


def precision_ceiling(true, false, refutable):
    if min(true, false, refutable) < 0 or refutable > false:
        raise ValueError("refutable findings must be a subset of confirmed false findings")
    denominator = true + false - refutable
    return true / denominator if denominator else None


def feasibility_report(sample, adjudications):
    by_id = {}
    known_ids = {row["id"] for row in sample["selected"]}
    for label in adjudications:
        if label["id"] in by_id or label["id"] not in known_ids:
            raise ValueError("duplicate or foreign adjudication")
        by_id[label["id"]] = label
    counts = Counter({"true": 0, "false": 0, "refutable_false": 0, "unknown": 0})
    weighted = dict.fromkeys(counts, 0.0)
    for row in sample["selected"]:
        label = by_id.get(row["id"], {})
        approved = label.get("human_approved") is True and bool(label.get("reviewer"))
        if label.get("reviewer", "").startswith("model:"):
            approved = False
        kind = {"confirmed_true": "true", "confirmed_false": "false"}.get(label.get("label"), "unknown") if approved else "unknown"
        fraction = row["sampling_fraction"]
        if not 0 < fraction <= 1:
            raise ValueError("invalid sampling fraction")
        counts[kind] += 1
        weighted[kind] += 1 / fraction
        can_refute = kind == "false" and label.get("check_kind") in ("analyzer", "existing_test", "isolated_reproducer") and bool(label.get("experiment", "").strip()) and bool(label.get("refutation_implication", "").strip())
        if can_refute:
            counts["refutable_false"] += 1
            weighted["refutable_false"] += 1 / fraction
    true, false, refutable, unknown = (weighted[key] for key in ("true", "false", "refutable_false", "unknown"))
    known_ceiling = precision_ceiling(true, false, refutable)
    pessimistic = precision_ceiling(true, false + unknown, refutable)
    optimistic = precision_ceiling(true + unknown, false, refutable)
    return {"purpose": "diagnostic feasibility only; not a promotion result", "sample_counts": dict(counts),
            "weighted_population_estimates": weighted, "adjudicated_precision_ceiling": known_ceiling,
            "unknown_label_sensitivity": {"all_false_unrefutable": pessimistic, "all_true": optimistic},
            "reason": "awaiting human adjudication" if not counts["true"] and not counts["false"] else "optimistic reach estimate; no code executed",
            "promotion": "not evaluated"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subs = parser.add_subparsers(dest="command", required=True)
    sample = subs.add_parser("sample")
    sample.add_argument("--runs", nargs="+", type=Path, required=True)
    sample.add_argument("--scores", type=Path)
    sample.add_argument("--per-stratum", type=int, required=True)
    sample.add_argument("--seed", type=int, required=True)
    sample.add_argument("--output", type=Path, required=True)
    report = subs.add_parser("report")
    report.add_argument("--sample", type=Path, required=True)
    report.add_argument("--adjudications", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "sample":
        evaluations = json.loads(args.scores.read_text())["evaluations"] if args.scores else {}
        sample = sample_pool(retained_pool(args.runs, evaluations), args.per_stratum, args.seed)
        args.output.mkdir(parents=True, exist_ok=False)
        (args.output / "sample-manifest.json").write_text(json.dumps(sample, indent=2) + "\n")
        (args.output / "human-adjudication.json").write_text(json.dumps(human_view(sample), indent=2) + "\n")
    else:
        result = feasibility_report(json.loads(args.sample.read_text()), json.loads(args.adjudications.read_text()))
        print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
