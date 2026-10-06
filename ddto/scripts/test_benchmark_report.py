"""Memory units, failed-run handling and integrity of imported benchmark data."""

import csv
import gzip
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import benchmark_report as report


class ReportTests(unittest.TestCase):
    def test_memory_cells_use_mib_and_both_signed_references(self):
        cell = {variant: {"peak_bytes_median": value * report.MIB}
                for variant, value in (("native", 2), ("orchestrion", 3), ("sdk", 1), ("mini", 4))}
        self.assertEqual(report.format_cell(cell, "mini", "peak_bytes"), "4.0 MiB (+33.3%; +100.0%)")
        self.assertEqual(report.format_cell(cell, "sdk", "peak_bytes"), "1.0 MiB (-66.7%; -50.0%)")

    def test_failed_reference_cannot_produce_a_percentage(self):
        cell = {"native": {"wall_s": {"median": 1}},
                "orchestrion": {"valid": False, "failed": 2, "attempted": 6},
                "mini": {"valid": True, "wall_s": {"median": 2}}}
        self.assertEqual(report.format_cell(cell, "orchestrion", "wall_s"), "FAIL 2/6")
        self.assertEqual(report.format_cell(cell, "mini", "wall_s"),
                         "2.000000 s (vs Orchestrion unavailable; +100.0% vs Native)")

    def test_memory_median_excludes_warmup_but_warmup_failure_invalidates_group(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / "runtime").mkdir()
            rows = [dict(case="fixture", cpus=4, variant="mini", warmup=warmup,
                         peak_bytes=peak, returncode=0, validated_contract=True)
                    for warmup, peak in ((True, 9999), (False, 100), (False, 300))]
            def save():
                with (directory / "runtime/observations.csv").open("w", newline="") as output:
                    writer = csv.DictWriter(output, fieldnames=rows[0])
                    writer.writeheader()
                    writer.writerows(rows)
            save()
            entry = dict(valid=True, attempted=3, valid_measured=2, peak_bytes_median=200)
            summary = {"fixture/4": {"mini": entry}}
            report.verify_runtime_memory(directory, summary)
            entry["peak_bytes_median"] = 100
            with self.assertRaisesRegex(ValueError, "memory mismatch"):
                report.verify_runtime_memory(directory, summary)
            rows[0]["returncode"] = 1
            save()
            with self.assertRaisesRegex(ValueError, "validation mismatch"):
                report.verify_runtime_memory(directory, summary)
            entry["valid"] = False
            report.verify_runtime_memory(directory, summary)

    def test_compressed_import_must_match_both_archived_and_original_hashes(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            original = b'{"peak_bytes": 1048576}\n'
            compressed = gzip.compress(original, mtime=0)
            (directory / "rows.json.gz").write_bytes(compressed)
            record = dict(sha256=hashlib.sha256(compressed).hexdigest(),
                          source_sha256=hashlib.sha256(original).hexdigest())
            archive = {"files": {"rows.json.gz": record}}
            def save():
                (directory / "archive.json").write_text(json.dumps(archive))
            save()
            self.assertEqual(report.verify_archive(directory), archive)
            record["source_sha256"] = "0" * 64
            save()
            with self.assertRaisesRegex(ValueError, "uncompressed SHA256"):
                report.verify_archive(directory)
            (directory / "rows.json.gz").write_bytes(b"changed")
            with self.assertRaisesRegex(ValueError, "archive SHA256"):
                report.verify_archive(directory)

    def test_continuous_parity_requires_the_recorded_balanced_execution_orders(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            for index in range(6):
                folder = directory / f"parity/cpu4/round{index}"
                folder.mkdir(parents=True)
                row = {"execution_order": ["sdk", "mini"] if index % 2 == 0 else ["mini", "sdk"],
                       "execution_block": {"sdk_wall_ns": 2_000_000_000, "mini_wall_ns": 1_000_000_000}}
                for name in ("parity", "parity-deferred"):
                    (folder / f"{name}.json").write_text(json.dumps(row))
            self.assertEqual(report.continuous_parity(directory, [4]).count("2.000000 s | 1.000000 s | -50.0%"), 2)
            row["execution_order"] = ["sdk", "mini"]
            (folder / "parity.json").write_text(json.dumps(row))
            with self.assertRaisesRegex(ValueError, "balanced"):
                report.continuous_parity(directory, [4])


if __name__ == "__main__":
    unittest.main()
