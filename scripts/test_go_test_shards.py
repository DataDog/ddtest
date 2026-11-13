"""Keep CI partitioning exhaustive and coverage aggregation lossless."""

from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import go_test_shards as shards


class ShardTests(unittest.TestCase):
    def test_every_package_runs_on_exactly_one_shard(self):
        packages = [f"example.com/pkg{i}" for i in range(200)] + [package for group in shards.PINNED_SHARDS for package in group]
        for total in range(1, 7):
            groups = [shards.selected_packages(packages, i, total) for i in range(1, total + 1)]
            self.assertCountEqual([package for group in groups for package in group], packages)
            self.assertEqual(groups, [shards.selected_packages(reversed(packages), i, total) for i in range(1, total + 1)])

    def test_the_slowest_packages_run_on_different_shards(self):
        packages = [package for group in shards.PINNED_SHARDS for package in group]
        for index, group in enumerate(shards.PINNED_SHARDS, start=1):
            self.assertEqual(shards.selected_packages(packages, index, 3), sorted(group))

    def test_pinned_packages_exist(self):
        packages = shards.discover_packages()
        for group in shards.PINNED_SHARDS:
            for package in group:
                self.assertIn(package, packages)

    def test_discovery_lists_each_package_once(self):
        result = subprocess.CompletedProcess([], 0, "example.com/b\nexample.com/a\nexample.com/b\n")
        with patch.object(subprocess, "run", return_value=result) as run:
            self.assertEqual(shards.discover_packages(), ["example.com/a", "example.com/b"])
        self.assertEqual(run.call_args.args[0], ["go", "list", "./..."])

    def test_failed_or_empty_discovery_cannot_silently_pass(self):
        with patch.object(subprocess, "run", side_effect=subprocess.CalledProcessError(1, ["go"])):
            with self.assertRaises(subprocess.CalledProcessError):
                shards.discover_packages()
        with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "")):
            with self.assertRaisesRegex(ValueError, "no packages"):
                shards.discover_packages()

    def test_invalid_shard_is_rejected(self):
        for value in ("0/3", "4/3", "1/0", "-1/3", "1", "a/b", "1/2/3"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                shards.parse_shard(value)

    def test_an_empty_shard_cannot_silently_pass(self):
        with self.assertRaisesRegex(ValueError, "no packages"):
            shards.shard_packages([], "1/3")


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
