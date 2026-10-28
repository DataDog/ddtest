"""Keep recorded timings distinct from missing or invalid observations."""

import copy
import json
from pathlib import Path
import tempfile
import unittest

import parity_report


class ParityReportTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.path = Path(temporary.name) / "parity.json"
        counts = dict.fromkeys(parity_report.COUNT_FIELDS, 1)
        self.timing = {"scope": "prebuilt binary", "sdk_wall_ns": 200_000_000, "mini_wall_ns": 100_000_000}
        self.report = {
            "schema_version": 2, "sdk_instrumentation": "orchestrion",
            "sdk_commit": "a" * 40, "sdk_version": "fixture",
            "os": "windows", "architecture": "amd64", "go": "fixture",
            "scenarios": [{"scenario": f"case-{i}", "features": ["pass"],
                           "status": "passed", "sdk": counts, "mini": counts,
                           "sdk_exit": 0, "mini_exit": 0, "timing": self.timing}
                          for i in range(65)],
        }
        self.evidence = {}
        for name in ("manual", "spans", "packages", "fuzz", "telemetry", "testify"):
            item = {"timing": self.timing, "status": "passed", "sdk": counts, "mini": counts, "scope": name}
            if name == "telemetry":
                item.update(semantic_counts_equal=True, request_counts_match_http=True)
            if name == "testify":
                item.update(status="gap", sdk_with_orchestrion=counts, sdk_with_poc=counts, reason="known gap")
            self.evidence[name] = item

    def render(self):
        self.path.write_text(json.dumps(self.report))
        for name, item in self.evidence.items():
            self.path.with_name(f"parity-{name}.json").write_text(json.dumps(item))
        return parity_report.render(self.path)

    def test_fuzz_examples_requires_every_reference_and_delivery_combination(self):
        report = {"sdk_commit": parity_report.FUZZ_EXAMPLE_COMMIT, "scenarios": [
            {"scenario": scenario, "mode": mode, "deferred": deferred,
             "status": "passed", "sdk": self.report["scenarios"][0]["sdk"],
             "mini": self.report["scenarios"][0]["mini"], "timing": self.timing}
            for scenario in parity_report.FUZZ_EXAMPLE_SCENARIOS
            for mode in ("manual", "orchestrion") for deferred in (False, True)]}
        parity_report.validate_fuzz_examples(report)
        for change, message in (
                (lambda value: value["scenarios"].pop(), "missing fuzz/example"),
                (lambda value: value["scenarios"].append(value["scenarios"][0]), "duplicated fuzz/example"),
                (lambda value: value.update(sdk_commit="a" * 40), "unexpected fuzz/example SDK"),
                (lambda value: value["scenarios"][0].update(status="failed"), "failed fuzz/example"),
                (lambda value: value["scenarios"][0].update(mini=dict(value["scenarios"][0]["mini"], tests=99)), "failed fuzz/example")):
            altered = copy.deepcopy(report)
            change(altered)
            with self.assertRaisesRegex(ValueError, message):
                parity_report.validate_fuzz_examples(altered)

    def test_current_schema_requires_feature_matrix(self):
        self.report.update(schema_version=4)
        self.report["scenarios"] = self.report["scenarios"][:64]
        with self.assertRaises(FileNotFoundError):
            self.render()

    def current_feature_evidence(self):
        self.report.update(schema_version=4)
        self.report["scenarios"] = self.report["scenarios"][:64]
        self.evidence["fuzz"]["sdk_commit"] = parity_report.FUZZ_EXAMPLE_COMMIT
        rows = [
            {"scenario": scenario, "mode": mode, "deferred": deferred,
             "status": "passed", "sdk": self.report["scenarios"][0]["sdk"],
             "mini": self.report["scenarios"][0]["mini"], "timing": self.timing}
            for scenario in sorted(parity_report.FUZZ_EXAMPLE_SCENARIOS)
            for mode in ("manual", "orchestrion") for deferred in (False, True)]
        common = {"sdk_commit": parity_report.FUZZ_EXAMPLE_COMMIT, "sdk_version": "fixture"}
        self.evidence["fuzz-examples"] = dict(common, scenarios=rows)
        covered = {"pass", "seed-lifecycle", "test-management", "active-fuzz",
                   "skip-lifecycle", "parallel-duration", "filtered"}
        self.evidence["fuzz-examples-coverage"] = dict(common, scenarios=[
            row for row in rows if row["mode"] == "orchestrion" and row["scenario"] in covered])

    def test_current_report_renders_complete_feature_and_coverage_tables(self):
        self.current_feature_evidence()
        text = self.render()
        self.assertIn("Matrix: 64 passing scenarios", text)
        self.assertIn("## Go Fuzz and executable Examples", text)
        self.assertIn("### Coverage combinations", text)
        self.assertIn("| corpus-lifecycle | manual | True |", text)
        self.assertIn("| filtered | False |", text)

    def test_current_report_rejects_missing_or_failing_coverage(self):
        self.current_feature_evidence()
        covered = self.evidence["fuzz-examples-coverage"]
        valid = copy.deepcopy(covered["scenarios"])
        for rows, message in (
                (valid[:-1], "missing fuzz/example"),
                (valid + [valid[0]], "duplicated fuzz/example"),
                ([dict(valid[0], status="failed")] + valid[1:], "failed fuzz/example")):
            covered["scenarios"] = rows
            with self.assertRaisesRegex(ValueError, message):
                self.render()

    def test_current_report_rejects_a_different_campaign_reference(self):
        self.current_feature_evidence()
        self.evidence["fuzz"]["sdk_commit"] = "a" * 40
        with self.assertRaisesRegex(ValueError, "fuzz campaign reference differs"):
            self.render()

    def test_testify_passed_contract_and_counts(self):
        item = self.evidence["testify"]
        item["status"] = "passed"
        item["scenarios"] = self.report["scenarios"][:25]
        self.assertIn("Testify parity passes", self.render())
        item["sdk_with_poc"] = dict(item["sdk_with_poc"], suites=2)
        with self.assertRaisesRegex(ValueError, "Testify event counts differ"):
            self.render()

    def test_failed_testify_case_is_rejected(self):
        self.evidence["testify"]["status"] = "passed"
        self.evidence["testify"]["scenarios"] = [dict(self.report["scenarios"][0], status="failed")] + self.report["scenarios"][1:25]
        with self.assertRaisesRegex(ValueError, "failed Testify scenario"):
            self.render()

    def test_incomplete_testify_evidence_is_rejected(self):
        self.evidence["testify"].update(status="passed", scenarios=[])
        with self.assertRaisesRegex(ValueError, "missing or duplicated Testify"):
            self.render()

    def test_external_testify_claim_requires_case(self):
        item = self.evidence["testify"]
        item["status"] = "passed"
        item["scenarios"] = self.report["scenarios"][:25]
        item["external_callers"] = True
        with self.assertRaisesRegex(ValueError, "missing external-module"):
            self.render()
        row = dict(item["scenarios"][0], scenario="external-module-helper")
        item["scenarios"] = item["scenarios"] + [row]
        self.assertIn("external-module callers covered", self.render())

    def test_reports_seconds_delta_and_measured_scope(self):
        text = self.render()
        self.assertIn("0.200000 | 0.100000 | -50.0%", text)
        self.assertIn("SDK column uses full Orchestrion reference", text)
        self.assertIn("Complete feature parity: **no**", text)
        slower = dict(self.timing, mini_wall_ns=300_000_000)
        self.assertEqual(parity_report.timing_cells({"timing": slower}, True)[2], "+50.0%")

    def test_grouped_report_keeps_continuous_walltime_and_actual_order(self):
        self.report.update(schema_version=3, execution_block=dict(self.timing, scope="all scenarios"),
                           execution_order=["mini", "sdk"])
        text = self.render()
        self.assertIn("Execution order: mini then sdk.", text)
        self.assertIn("Continuous walltime | 0.200000 | 0.100000 | -50.0%", text)
        self.report["execution_order"] = ["sdk", "sdk"]
        with self.assertRaisesRegex(ValueError, "invalid grouped execution order"):
            self.render()
        self.report["execution_order"] = ["sdk", "mini"]
        self.report["execution_block"]["sdk_wall_ns"] = 0
        with self.assertRaisesRegex(ValueError, "positive integer"):
            self.render()

    def test_historical_reports_do_not_fabricate_timings(self):
        self.report.pop("schema_version")
        self.report["scenarios"] = copy.deepcopy(self.report["scenarios"])
        for row in self.report["scenarios"]:
            row.pop("timing")
        for item in self.evidence.values():
            item.pop("timing")
        self.assertIn("Not recorded", self.render())

    def test_current_report_requires_every_timing(self):
        self.report["scenarios"][0] = dict(self.report["scenarios"][0])
        self.report["scenarios"][0].pop("timing")
        with self.assertRaisesRegex(ValueError, "missing SDK/Mini timing"):
            self.render()
        self.report["scenarios"][0]["timing"] = self.timing
        self.evidence["telemetry"].pop("timing")
        with self.assertRaisesRegex(ValueError, "missing SDK/Mini timing"):
            self.render()

    def test_rejects_invalid_duration_or_missing_scope(self):
        for value in (0, -1, True, 1.5, float("nan"), float("inf")):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "positive integer"):
                parity_report.timing_cells({"timing": dict(self.timing, sdk_wall_ns=value)}, True)
        with self.assertRaisesRegex(ValueError, "missing timing scope"):
            parity_report.timing_cells({"timing": dict(self.timing, scope="")}, True)


if __name__ == "__main__":
    unittest.main()
