#!/usr/bin/env python3
"""Reconcile retained per-attempt usage, including failed reviews and retries."""

import argparse
import hashlib
import json
import math
from pathlib import Path


def empty_stage():
    return {"attempts": 0, "failed_attempts": 0, "unknown_usage_attempts": 0,
            "prompt_tokens": 0, "completion_tokens": 0, "cache_read_tokens": 0,
            "cache_creation_tokens": 0, "reasoning_tokens": 0,
            "duration_ms": 0, "reported_cost_usd": 0.0, "unknown_cost_attempts": 0}


def add_attempt(stage, attempt):
    stage["attempts"] += 1
    stage["failed_attempts"] += bool(attempt["failed"])
    stage["duration_ms"] += attempt["duration_ms"]
    usage = attempt.get("usage")
    if usage is None:
        stage["unknown_usage_attempts"] += 1
        stage["unknown_cost_attempts"] += 1
        return
    for key in ("prompt_tokens", "completion_tokens", "cache_read_tokens", "cache_creation_tokens", "reasoning_tokens"):
        value = usage.get(key, 0)
        if not isinstance(value, int) or isinstance(value, bool) or value < 0:
            raise ValueError(f"invalid usage {key}: {value!r}")
        stage[key] += value
    cost = usage.get("cost")
    if cost is None:
        stage["unknown_cost_attempts"] += 1
    elif not isinstance(cost, (int, float)) or isinstance(cost, bool) or not math.isfinite(cost) or cost < 0:
        raise ValueError(f"invalid reported cost: {cost!r}")
    else:
        stage["reported_cost_usd"] += cost


def purpose(role):
    if role == "embedding":
        return "embedding"
    if role.startswith("judge"):
        return "judge"
    if role.startswith("fix"):
        return "fix"
    if role.startswith("execution"):
        return "execution"
    return "review"


def summarize_usage(run):
    stages = {}
    purposes = {}
    hashes = {}
    completed = failed = pending = metered = 0
    malformed = []
    for case in sorted(run.glob("*-r*")):
        if not case.is_dir():
            continue
        for name in ("status.json", "result.json", "nitpick.json", "stdout.txt", "stderr.txt"):
            path = case / name
            if path.is_file():
                with path.open("rb") as stream:
                    hashes[str(path.relative_to(run))] = hashlib.file_digest(stream, "sha256").hexdigest()
        if not (case / "status.json").exists():
            pending += 1
            continue
        completed += 1
        try:
            status = json.loads((case / "status.json").read_text())
            # Failed reviews may have bought responses; skipping them hides
            # spend and turns missing usage into apparent zero-cost work.
            failed += status["exit_code"] != 0
            result = json.loads((case / "result.json").read_text())
            usage = result.get("usage")
            if usage is None and (case / "nitpick.json").exists():
                usage = json.loads((case / "nitpick.json").read_text()).get("usage")
            if not usage or not usage.get("metered"):
                continue
            if usage.get("schema_version") != 1 or not isinstance(usage.get("attempts"), list):
                raise ValueError("unsupported or incomplete usage schema")
            # Validate before banking a case, so a malformed attempt cannot
            # leave a partially accumulated case looking fully metered.
            for attempt in usage["attempts"]:
                for key in ("provider", "model", "role"):
                    if not isinstance(attempt.get(key), str) or not attempt[key]:
                        raise ValueError(f"missing attempt {key}")
                if not isinstance(attempt.get("failed"), bool) or not isinstance(attempt.get("duration_ms"), int) or attempt["duration_ms"] < 0:
                    raise ValueError("invalid attempt outcome or duration")
                add_attempt(empty_stage(), attempt)
            metered += 1
            for attempt in usage["attempts"]:
                key = f'{attempt["provider"]}/{attempt["model"]}:{attempt["role"]}'
                add_attempt(stages.setdefault(key, empty_stage()), attempt)
                add_attempt(purposes.setdefault(purpose(attempt["role"]), empty_stage()), attempt)
        except (OSError, ValueError, KeyError, TypeError) as exc:
            malformed.append({"case": case.name, "error": str(exc)})
    total = empty_stage()
    for stage in stages.values():
        for key in total:
            total[key] += stage[key]
    coverage_complete = completed > 0 and metered == completed and not pending and not malformed
    tokens_complete = coverage_complete and total["unknown_usage_attempts"] == 0
    cost_complete = coverage_complete and total["unknown_cost_attempts"] == 0
    if not completed:
        reason = "no completed reviews"
    elif not coverage_complete:
        reason = "unmetered, pending, or malformed reviews"
    elif not tokens_complete:
        reason = "provider attempts with unknown usage"
    else:
        reason = "all completed reviews reconciled"
    return {"schema_version": 1, "run": str(run), "completed_reviews": completed,
            "failed_reviews": failed, "pending_reviews": pending, "metered_reviews": metered,
            "tokens_complete": tokens_complete, "reason": reason,
            "known_totals": total, "stages": stages, "purposes": purposes,
            "reported_cost_usd": total["reported_cost_usd"] if cost_complete else None,
            "prompt_tokens_per_review": total["prompt_tokens"] / completed if tokens_complete else None,
            "completion_tokens_per_review": total["completion_tokens"] / completed if tokens_complete else None,
            "malformed_cases": malformed, "artifacts_sha256": hashes,
            "token_semantics": "reasoning is included in completion; cache tokens are separate input categories",
            "review_semantics": "completed means terminal, including failed reviews; paid attempts from failed reviews stay in the totals"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("runs", nargs="+", type=Path)
    parser.add_argument("--output", type=Path, help="write a retained accounting artifact")
    args = parser.parse_args()
    text = json.dumps([summarize_usage(run) for run in args.runs], indent=2, allow_nan=False) + "\n"
    if args.output:
        args.output.write_text(text)
    else:
        print(text, end="")


if __name__ == "__main__":
    main()
