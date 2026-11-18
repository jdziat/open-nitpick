import json

import review_census


def write_report(tmp_path, attempts, failed_stages=None):
    path = tmp_path / "review.json"
    path.write_text(json.dumps({
        "result": "clean",
        "complete": True,
        "failed_stages": failed_stages or [],
        "findings": [],
        "usage": {"attempts": attempts},
    }))
    return path


def attempt(usage):
    return {"usage": usage, "failed": False, "role": "review", "model": "fixture"}


def test_unknown_cost_does_not_become_zero_spend(tmp_path):
    path = write_report(tmp_path, [attempt({"cost": 2.5}), attempt(None), attempt({"prompt_tokens": 4})])
    result = review_census.census([path])
    assert result["reported_cost_usd_lower_bound"] == 2.5
    assert result["unknown_cost_attempts"] == 2
    assert result["cost_complete"] is False
    assert result["runs"][0]["missing_usage_attempts"] == 1


def test_clean_flag_does_not_erase_failed_stage(tmp_path):
    path = write_report(tmp_path, [attempt({"cost": 0})], ["triage"])
    row = review_census.summarize(path)
    assert row["declared_complete"] is True
    assert row["complete_without_failed_stages"] is False
    assert row["failed_stages"] == ["triage"]


def test_empty_artifact_does_not_disappear_from_the_census(tmp_path):
    path = tmp_path / "interrupted.json"
    path.write_text("")
    result = review_census.census([path])
    assert result["unreadable_artifacts"] == 1
    assert result["cost_complete"] is False
    assert len(result["runs"]) == 1
    assert "artifact_error" in result["runs"][0]


def test_receipt_span_is_not_presented_as_full_review_latency(tmp_path):
    rows = [
        {**attempt({"cost": 1}), "started_at": "2026-11-18T00:00:00Z", "duration_ms": 3000},
        {**attempt({"cost": 2}), "started_at": "2026-11-18T00:00:01Z", "duration_ms": 4000},
    ]
    path = write_report(tmp_path, rows)
    row = review_census.summarize(path)
    assert row["provider_activity_span_seconds"] == 5
    assert "review_latency_seconds" not in row
