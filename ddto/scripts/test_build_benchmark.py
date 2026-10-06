"""Contracts for retained evidence and signed compile-time comparisons."""

import csv
import json
from pathlib import Path
import tempfile
import unittest

import build_benchmark as benchmark


class ReportTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.manifest = {"schema_version": 1, "source_head": "example", "status": "verified",
                         "cpus": [4], "cases": [{"id": "fixture", "label": "Fixture",
                                                "flags": [], "series": "test"}],
                         "repetitions": {"test": {s: 3 for s in benchmark.SCENARIOS}}}
        (self.directory / "manifest.json").write_text(json.dumps(self.manifest))
        (self.directory / "methodology.md").write_text("# Recorded fixture\n")
        self.rows = []
        timings = {"native": [10, 20, 30], "orchestrion": [20, 30, 40],
                   "sdk": [10, 15, 30], "mini": [20, 40, 100]}
        for scenario in benchmark.SCENARIOS:
            for variant, values in timings.items():
                for index, wall in enumerate(values):
                    row = dict.fromkeys(benchmark.FIELDS, 0)
                    row.update(series="test", project="fixture", cpus=4, variant=variant,
                               scenario=scenario, iteration=index, validation_only=False,
                               wall_s=wall, cpu_s=wall, peak_bytes=100,
                               process_tree_drained=True, cold_output_removed=scenario == "cold",
                               command=json.dumps(["go", "test", "-c", "-o", "output/", "./..."]))
                    self.rows.append(row)
        self.save_rows()

    def save_rows(self):
        with (self.directory / "observations.csv").open("w", newline="") as output:
            writer = csv.DictWriter(output, fieldnames=benchmark.FIELDS)
            writer.writeheader()
            writer.writerows(self.rows)

    def test_medians_keep_outliers_and_report_actual_positive_changes(self):
        report, summary = benchmark.render(self.directory)
        expected = "| Fixture | `none` | 4 | 20.000 s | 30.000 s | 15.000 s (-50.0%; -25.0%) | 40.000 s (+33.3%; +100.0%) |"
        self.assertEqual(report.count(expected), 5)
        mini = summary["statistics"]["fixture/4/cold"]["mini"]["wall_s"]
        self.assertEqual((mini["n"], mini["median"], mini["min"], mini["max"]), (3, 40, 20, 100))
        self.assertEqual(benchmark.render(self.directory), (report, summary))

    def test_readme_links_the_reports_without_duplicating_tables(self):
        _, summary = benchmark.render(self.directory)
        self.manifest.update(toolchain="go example", sdk_version="sdk-example")
        excerpt = benchmark.overview_lines(self.manifest, summary, "docs/results/example")
        self.assertIn("docs/results/example/README.md", excerpt)
        self.assertIn("docs/benchmarks.md", excerpt)
        self.assertIn("`example`", excerpt)
        self.assertIn("60 comparative observations", excerpt)
        self.assertNotIn("|", excerpt)
        self.assertNotIn("40.000 s", excerpt)

    def test_controls_do_not_enter_comparative_medians(self):
        row = dict(self.rows[0], scenario="control-link", validation_only=True, wall_s=1000)
        self.rows.append(row)
        self.save_rows()
        _, summary = benchmark.render(self.directory)
        self.assertEqual(summary["completed_commands"], 61)
        self.assertEqual(summary["measured_observations"], 60)
        self.assertEqual(summary["controls_and_trace_checks"], 1)
        self.assertEqual(summary["statistics"]["fixture/4/cold"]["native"]["wall_s"]["median"], 20)

    def test_incomplete_or_duplicate_rounds_are_rejected(self):
        original = list(self.rows)
        for invalid in (original[:-1], original + [original[0]]):
            with self.subTest(count=len(invalid)):
                self.rows = invalid
                self.save_rows()
                with self.assertRaisesRegex(ValueError, "incomplete|duplicate"):
                    benchmark.render(self.directory)

    def test_failure_paths_cannot_be_summarized_as_success(self):
        for field, value in (("returncode", 1), ("process_tree_drained", False),
                             ("cold_output_removed", False), ("wall_s", "nan"),
                             ("command", json.dumps(["go", "test", "./..."]))):
            with self.subTest(field=field):
                original = self.rows[0][field]
                self.rows[0][field] = value
                self.save_rows()
                with self.assertRaises(ValueError):
                    benchmark.render(self.directory)
                self.rows[0][field] = original

    def test_unqualified_run_is_rejected_even_with_complete_timings(self):
        self.manifest["status"] = "partial"
        (self.directory / "manifest.json").write_text(json.dumps(self.manifest))
        with self.assertRaisesRegex(ValueError, "qualification"):
            benchmark.render(self.directory)

    def test_csv_content_hash_detects_modified_evidence(self):
        self.manifest["raw_csv_sha256"] = benchmark.digest(self.directory / "observations.csv")
        (self.directory / "manifest.json").write_text(json.dumps(self.manifest))
        self.rows[0]["wall_s"] = 11
        self.save_rows()
        with self.assertRaisesRegex(ValueError, "SHA256"):
            benchmark.render(self.directory)


if __name__ == "__main__":
    unittest.main()
