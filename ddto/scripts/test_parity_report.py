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
