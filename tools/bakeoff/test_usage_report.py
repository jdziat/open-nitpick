"""Guard against undercounted reviews and prices invented for missing usage."""

import json
from pathlib import Path
import tempfile
import unittest

from usage_report import summarize_usage


class UsageReportTests(unittest.TestCase):
    def create_run(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.run = Path(self.temp.name)

    def write_case(self, name, attempts=None, failed=False, usage=None):
        case = self.run / f"{name}-r1"
        case.mkdir()
        (case / "status.json").write_text(json.dumps({"exit_code": int(failed)}))
        if attempts is not None:
            usage = {"schema_version": 1, "metered": True, "attempts": attempts}
        (case / "result.json").write_text(json.dumps({"usage": usage}))
        return case

    @staticmethod
    def attempt(role="review", usage=None, failed=False):
        return {"provider": "fixture", "model": "shared", "role": role,
                "failed": failed, "duration_ms": 12, "usage": usage}

    def test_empty_run_reports_no_completed_reviews(self):
        self.create_run()
        got = summarize_usage(self.run)
        self.assertFalse(got["tokens_complete"])
        self.assertEqual(got["reason"], "no completed reviews")
        self.assertIsNone(got["reported_cost_usd"])

    def test_metered_zero_calls_keeps_the_positive_denominator(self):
        self.create_run()
        self.write_case("clean", attempts=[])
        got = summarize_usage(self.run)
        self.assertTrue(got["tokens_complete"])
        self.assertEqual(got["completed_reviews"], 1)
        self.assertEqual(got["prompt_tokens_per_review"], 0)
        self.assertEqual(got["reported_cost_usd"], 0)

    def test_shared_model_roles_and_failed_reviews_are_retained(self):
        self.create_run()
        usage = {"prompt_tokens": 12, "completion_tokens": 7, "cache_read_tokens": 3,
                 "reasoning_tokens": 4, "cost": 0.25}
        self.write_case("done", attempts=[self.attempt(usage=usage), self.attempt("triage", usage=usage)])
        self.write_case("failed", failed=True, attempts=[self.attempt("verification", failed=True)])
        got = summarize_usage(self.run)
        self.assertEqual(got["completed_reviews"], 2)
        self.assertEqual(got["failed_reviews"], 1)
        self.assertEqual(len(got["stages"]), 3)
        self.assertEqual(got["known_totals"]["completion_tokens"], 14)
        self.assertEqual(got["known_totals"]["reasoning_tokens"], 8)
        self.assertEqual(got["known_totals"]["failed_attempts"], 1)
        self.assertEqual(got["known_totals"]["reported_cost_usd"], 0.5)
        self.assertFalse(got["tokens_complete"])
        self.assertIsNone(got["prompt_tokens_per_review"])
        self.assertIsNone(got["reported_cost_usd"])
        self.assertEqual(len(got["artifacts_sha256"]), 4)

    def test_historical_partial_and_malformed_reviews_are_not_free(self):
        self.create_run()
        self.write_case("historical")
        self.write_case("invalid", attempts=[{"role": "review"}])
        (self.run / "pending-r1").mkdir()
        got = summarize_usage(self.run)
        self.assertEqual(got["completed_reviews"], 2)
        self.assertEqual(got["pending_reviews"], 1)
        self.assertEqual(got["metered_reviews"], 0)
        self.assertEqual(len(got["malformed_cases"]), 1)
        self.assertFalse(got["tokens_complete"])
        self.assertIsNone(got["reported_cost_usd"])

    def test_missing_prices_do_not_erase_reported_tokens(self):
        self.create_run()
        self.write_case("unpriced", attempts=[self.attempt("fix", usage={"prompt_tokens": 12})])
        got = summarize_usage(self.run)
        self.assertTrue(got["tokens_complete"])
        self.assertIsNone(got["reported_cost_usd"])
        self.assertEqual(got["purposes"]["fix"]["prompt_tokens"], 12)


if __name__ == "__main__":
    unittest.main()
