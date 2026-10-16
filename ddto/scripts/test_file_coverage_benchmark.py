import unittest

import file_coverage_benchmark as benchmark


class CompareTests(unittest.TestCase):
    def test_metrics_and_missing_files(self):
        reference = {"a_test.go/TestA": ["a.go", "a_test.go"], "b_test.go/TestB": ["b.go", "b_test.go"]}
        rows = {
            "counters": reference,
            "marks": {"a_test.go/TestA": ["a.go", "c.go"], "b_test.go/TestB": ["c.go"]},
        }
        got = benchmark.compare(rows, reference)
        self.assertEqual(2, got["counters"]["tests"])
        self.assertEqual(1, got["counters"]["files_per_test"])
        self.assertEqual(0, got["counters"]["missing_tests"])
        # Each of a.go, b.go and c.go: counters select 1, 1 and 0 tests.
        self.assertEqual(0.7, got["counters"]["tests_per_changed_file"])
        self.assertEqual(1.5, got["marks"]["files_per_test"])
        self.assertEqual({"b_test.go/TestB": ["b.go"]}, got["marks"]["missing"])

    def test_a_test_without_rows_misses_its_files(self):
        reference = {"T/A": ["a.go"], "T/B": ["b.go"]}
        got = benchmark.compare({"counters": reference, "marks": {"T/A": ["a.go"]}}, reference)["marks"]
        self.assertEqual((2, 1, 1), (got["tests"], got["tests_without_rows"], got["missing_tests"]))
        self.assertEqual({"T/B": ["b.go"]}, got["missing"])

    def test_tests_that_only_another_mode_reports_do_not_count(self):
        rows = {"counters": {"T/A": ["a.go"]}, "marks": {"T/A": ["a.go"], "T/B": ["b.go"]}}
        self.assertEqual(1, benchmark.compare(rows, rows["counters"])["marks"]["tests"])

    def test_a_fallback_is_rejected(self):
        with self.assertRaises(SystemExit):
            benchmark.check_mode(["ddto", "test"], "TestOptimization.build v0.1.1 WARN: --experimental-file-coverage=context: x; tests run one at a time\n")
        with self.assertRaises(SystemExit):
            benchmark.check_mode(["ddto", "test"], "TestOptimization.build v0.1.1 WARN: toolchain in GOMODCACHE without a mirror - running go test without CI Visibility\n")
        benchmark.check_mode(["ddto", "test"], "ok\n")


if __name__ == "__main__":
    unittest.main()
