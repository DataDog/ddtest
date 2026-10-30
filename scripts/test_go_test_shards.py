"""Keep CI partitioning exhaustive and coverage aggregation lossless."""

from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import go_test_shards as shards


class ShardTests(unittest.TestCase):
    def test_every_test_example_and_fuzz_seed_runs_on_exactly_one_shard(self):
        names = [f"TestCase{i}" for i in range(200)] + ["Example", "ExampleDecode", "FuzzRoundTrip"]
        for total in range(1, 7):
            groups = [shards.selected_tests(names, i, total) for i in range(1, total + 1)]
            self.assertCountEqual([name for group in groups for name in group], names)
            self.assertEqual(groups, [shards.selected_tests(reversed(names), i, total) for i in range(1, total + 1)])

    def test_discovery_uses_go_build_selection_and_ignores_diagnostics_and_benchmarks(self):
        result = subprocess.CompletedProcess([], 0, "TestCase\nTestCase\nExample\nFuzzSeed\nBenchmarkOther\nok\tpkg\n")
        with patch.object(subprocess, "run", return_value=result) as run:
            self.assertEqual(shards.discover_tests(), ["Example", "FuzzSeed", "TestCase"])
        self.assertEqual(run.call_args.args[0], ["go", "test", "-race", "-list", ".", "./..."])

    def test_failed_or_empty_discovery_cannot_silently_pass(self):
        with patch.object(subprocess, "run", side_effect=subprocess.CalledProcessError(1, ["go"])):
            with self.assertRaises(subprocess.CalledProcessError):
                shards.discover_tests()
        with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "ok\tpkg\n")):
            with self.assertRaisesRegex(ValueError, "no tests"):
                shards.discover_tests()

    def test_invalid_shard_is_rejected(self):
        for value in ("0/3", "4/3", "1/0", "-1/3", "1", "a/b", "1/2/3"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                shards.parse_shard(value)

    def test_pattern_selects_only_the_assigned_top_level_tests(self):
        names = [f"TestCase{i}" for i in range(20)]
        pattern = shards.test_pattern(names, "2/3")
        selected = [name for name in names if re.fullmatch(pattern, name)]
        self.assertCountEqual(selected, shards.selected_tests(names, 2, 3))
        self.assertFalse(re.fullmatch(pattern, "TestCaseOther"))
        self.assertTrue(re.fullmatch(pattern, selected[0]))

    def test_empty_shard_runs_no_tests(self):
        name = "TestOnly"
        empty = next(i for i in range(1, 4) if not shards.selected_tests([name], i, 3))
        self.assertEqual(shards.test_pattern([name], f"{empty}/3"), "^$")


class CoverageTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.output = self.root / "merged.out"

    def profiles(self, *contents):
        paths = []
        for i, content in enumerate(contents):
            path = self.root / f"coverage-{i}.out"
            path.write_text(content)
            paths.append(path)
        return paths

    def test_union_preserves_uncovered_blocks_and_sums_atomic_counts(self):
        paths = self.profiles("mode: atomic\npkg/a.go:1.1,2.2 2 1\npkg/b.go:2.1,3.2 1 0\n",
                              "mode: atomic\npkg/a.go:1.1,2.2 2 3\npkg/c.go:2.1,3.2 1 5\n",
                              "mode: atomic\npkg/a.go:1.1,2.2 2 0\n")
        shards.merge_coverage(paths, self.output, 3)
        self.assertEqual(self.output.read_text(), "mode: atomic\npkg/a.go:1.1,2.2 2 4\npkg/b.go:2.1,3.2 1 0\npkg/c.go:2.1,3.2 1 5\n")

    def test_missing_or_duplicate_profiles_fail(self):
        paths = self.profiles("mode: atomic\n", "mode: atomic\n")
        for inputs in (paths, [paths[0]] * 3):
            with self.assertRaisesRegex(ValueError, "expected 3 distinct"):
                shards.merge_coverage(inputs, self.output, 3)

    def test_incompatible_profiles_fail_before_writing(self):
        for invalid in ("mode: set\n", "mode: atomic\npkg/a.go:1.1,2.2 2 1\n",
                        "mode: atomic\npkg/a.go:1.1,2.2 1 -1\n"):
            paths = self.profiles("mode: atomic\npkg/a.go:1.1,2.2 1 1\n", invalid)
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                shards.merge_coverage(paths, self.output, 2)
            self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
