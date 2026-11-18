"""Keep unknown labels and generic passing tests out of the refutation ceiling."""

import unittest

from execution_feasibility import feasibility_report, human_view, precision_ceiling, sample_pool


class ExecutionFeasibilityTests(unittest.TestCase):
    def sample(self):
        rows = [{"id": str(i), "stratum": "unmatched_reference" if i < 4 else "matched_reference", "finding": {"body": "claim"}, "source": "hidden"} for i in range(6)]
        return sample_pool(rows, 2, 42)

    def test_reference_mismatch_is_not_a_false_label(self):
        sample = self.sample()
        view = human_view(sample)
        self.assertTrue(all("stratum" not in row and "source" not in row for row in view))
        report = feasibility_report(sample, view)
        self.assertEqual(report["sample_counts"]["unknown"], 4)
        self.assertEqual(report["sample_counts"]["false"], 0)
        self.assertIsNone(report["adjudicated_precision_ceiling"])
        self.assertEqual(report["reason"], "awaiting human adjudication")

    def test_model_labels_and_generic_tests_cannot_certify_refutations(self):
        sample = self.sample()
        view = human_view(sample)
        view[0].update(label="confirmed_false", human_approved=True, reviewer="model:judge", check_kind="existing_test", experiment="suite passed", refutation_implication="")
        view[1].update(label="confirmed_false", human_approved=True, reviewer="operator", check_kind="existing_test", experiment="suite passed", refutation_implication="")
        view[2].update(label="confirmed_true", human_approved=True, reviewer="operator")
        report = feasibility_report(sample, view)
        self.assertEqual(report["sample_counts"]["false"], 1)
        self.assertEqual(report["sample_counts"]["true"], 1)
        self.assertEqual(report["sample_counts"]["unknown"], 2)
        self.assertEqual(report["sample_counts"]["refutable_false"], 0)

    def test_refutation_requires_a_claim_specific_human_mapping(self):
        sample = self.sample()
        view = human_view(sample)
        view[0].update(label="confirmed_false", human_approved=True, reviewer="operator", check_kind="isolated_reproducer", experiment="Run the cited input with the exact preconditions", refutation_implication="Claim predicts panic; observing the expected output refutes that prediction")
        report = feasibility_report(sample, view)
        self.assertEqual(report["sample_counts"]["refutable_false"], 1)
        self.assertIsNone(precision_ceiling(0, 1, 1))
        self.assertEqual(precision_ceiling(3, 4, 2), 0.6)
        with self.assertRaises(ValueError):
            precision_ceiling(3, 2, 3)


if __name__ == "__main__":
    unittest.main()
