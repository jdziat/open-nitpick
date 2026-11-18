"""Summarize retained review receipts without contacting a model provider."""

import argparse
import collections
import datetime
import decimal
import hashlib
import json
from pathlib import Path


def summarize(path):
    raw = path.read_bytes()
    result = {"artifact": path.name, "sha256": hashlib.sha256(raw).hexdigest()}
    try:
        report = json.loads(raw)
        attempts = report["usage"]["attempts"]
    except (ValueError, KeyError, TypeError) as error:
        return {**result, "artifact_error": str(error), "cost_complete": False}
    costs = []
    unknown_cost = 0
    missing_usage = 0
    started = []
    ended = []
    roles = collections.Counter()
    for attempt in attempts:
        usage = attempt.get("usage")
        if usage is None:
            missing_usage += 1
        value = usage.get("cost") if usage else None
        if value is None:
            unknown_cost += 1
        else:
            cost = decimal.Decimal(str(value))
            if not cost.is_finite() or cost < 0:
                raise ValueError(f"{path}: invalid reported cost {value}")
            costs.append(cost)
        roles[(attempt["role"], attempt["model"])] += 1
        if attempt.get("started_at"):
            start = datetime.datetime.fromisoformat(attempt["started_at"].replace("Z", "+00:00"))
            started.append(start)
            ended.append(start + datetime.timedelta(milliseconds=attempt["duration_ms"]))
    failed = report.get("failed_stages", [])
    severity = collections.Counter(finding["severity"] for finding in report.get("findings", []))
    return {
        **result,
        "result": report.get("result"),
        "declared_complete": report.get("complete"),
        "failed_stages": failed,
        "complete_without_failed_stages": report.get("complete") is True and not failed,
        "severity_counts": dict(severity),
        "attempts": len(attempts),
        "failed_attempts": sum(attempt["failed"] for attempt in attempts),
        "missing_usage_attempts": missing_usage,
        "unknown_cost_attempts": unknown_cost,
        "reported_cost_usd_lower_bound": float(sum(costs, decimal.Decimal(0))),
        "cost_complete": unknown_cost == 0,
        "provider_activity_span_seconds": (max(ended) - min(started)).total_seconds() if started else None,
        "roles": [{"role": role, "model": model, "attempts": count} for (role, model), count in sorted(roles.items())],
    }


def census(paths):
    rows = [summarize(path) for path in paths]
    known = sum(decimal.Decimal(str(row.get("reported_cost_usd_lower_bound", 0))) for row in rows)
    return {
        "schema_version": 1,
        "scope": "historical self-review receipts; evolving source, not independent paired workloads",
        "notes": [
            "Reported costs exclude attempts whose provider reported no cost.",
            "Provider activity spans omit work before the first request and after the last response.",
            "No duplicate-purchase count can be inferred without original request identities.",
            "These artifacts do not supply blinded quality labels or operator-value evidence.",
        ],
        "reported_cost_usd_lower_bound": float(known),
        "cost_complete": all(row["cost_complete"] for row in rows),
        "unknown_cost_attempts": sum(row.get("unknown_cost_attempts", 0) for row in rows),
        "unreadable_artifacts": sum("artifact_error" in row for row in rows),
        "runs": rows,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reports", nargs="+", type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    result = census(args.reports)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.output.with_suffix(args.output.suffix + ".tmp")
    temporary.write_text(json.dumps(result, indent=2) + "\n")
    temporary.replace(args.output)


if __name__ == "__main__":
    main()
