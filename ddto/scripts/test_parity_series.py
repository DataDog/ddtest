"""Keep the whole-run comparison paired, balanced and tied to the same inputs."""

import unittest

import parity_series
import test_parity_report


class ParitySeriesTests(unittest.TestCase):
    def setUp(self):
        self.fixture = test_parity_report.ParityReportTests()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.root = self.fixture.path.parent
        self.fixture.report.update(schema_version=3, execution_block=self.fixture.timing,
                                   execution_order=["sdk", "mini"])
        self.fixture.render()

    def report_pair(self):
        import json
        second = self.root / "second.json"
        for source in list(self.root.glob("parity*.json")):
            target = self.root / source.name.replace("parity", "second", 1)
            target.write_bytes(source.read_bytes())
        value = json.loads(second.read_text())
        value["execution_order"] = ["mini", "sdk"]
        value["execution_block"] = dict(value["execution_block"], sdk_wall_ns=400_000_000, mini_wall_ns=200_000_000)
        second.write_text(json.dumps(value))
        return [self.fixture.path, second]

    def test_uses_continuous_blocks_not_the_sum_of_cases(self):
        summary = parity_series.summarize(self.report_pair())
        self.assertAlmostEqual(summary["statistics"]["sdk"]["median_seconds"], 0.3)
        self.assertAlmostEqual(summary["statistics"]["mini"]["median_seconds"], 0.15)
        self.assertAlmostEqual(summary["median_delta_percent"], -50)
        self.assertEqual(len(summary["samples"]), 2)
        self.assertIn("SDK block (s)", parity_series.markdown(summary))

    def test_rejects_duplicate_unbalanced_or_changed_inputs(self):
        import json
        paths = self.report_pair()
        with self.assertRaisesRegex(ValueError, "distinct reports"):
            parity_series.summarize([paths[0], paths[0]])
        value = json.loads(paths[1].read_text())
        value["execution_order"] = ["sdk", "mini"]
        paths[1].write_text(json.dumps(value))
        with self.assertRaisesRegex(ValueError, "balanced"):
            parity_series.summarize(paths)
        value["execution_order"] = ["mini", "sdk"]
        value["sdk_commit"] = "b" * 40
        paths[1].write_text(json.dumps(value))
        with self.assertRaisesRegex(ValueError, "changed between rounds"):
            parity_series.summarize(paths)


if __name__ == "__main__":
    unittest.main()
