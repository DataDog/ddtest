"""Missing tests, skipped checks and incomplete artifacts must make CI fail."""

import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

import ci_shards
import test_parity_report


class ShardTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.groups = ci_shards.read_groups()

    def test_matrix_and_platform_inventory(self):
        self.assertEqual(36, len(ci_shards.matrix("differential")["include"]))
        self.assertEqual(6, len(ci_shards.matrix("mini")["include"]))
        self.assertEqual(3, len(ci_shards.matrix("mini", tip=True)["include"]))
        for platform, count in (("ubuntu-latest", 120), ("macos-latest", 120), ("windows-latest", 121)):
            tests = [name for shard in ci_shards.SHARDS["differential"][1:]
                     for name in ci_shards.selected_tests(self.groups, "differential", "1.27.x", shard, platform)]
            self.assertEqual(count, len(tests))
            self.assertEqual(len(tests), len(set(tests)))
            ci_shards.validate_inventory(self.groups, tests, platform)
            for changed in (tests[:-1], tests + ["TestNewUnassignedCase"]):
                with self.assertRaisesRegex(ValueError, "test inventory changed"):
                    ci_shards.validate_inventory(self.groups, changed, platform)
        for version, count in (("1.25.x", 26), ("tip", 30)):
            tests = [name for shard in ci_shards.SHARDS["mini"][1:]
                     for name in ci_shards.selected_tests(self.groups, "mini", version, shard, "ubuntu-latest")]
            self.assertEqual(count, len(tests))
            self.assertEqual(len(tests), len(set(tests)))

    def test_reject_duplicate_and_unknown_assignments(self):
        for change in (
                lambda g: g["differential"]["sdk"].append(g["differential"]["cli"][0]),
                lambda g: g["mini"]["native"].append("TestMissing"),
                lambda g: g["tip_additional"]["native"].append(g["mini"]["fuzz"][0]),
                lambda g: g["platform_tests"]["windows"].update(other=["TestOther"])):
            groups = copy.deepcopy(self.groups)
            change(groups)
            path = self.root / "groups.json"
            path.write_text(json.dumps(groups))
            with self.assertRaises(ValueError):
                ci_shards.read_groups(path)

    def test_terminal_results_require_every_selected_test(self):
        ci_shards.check_results(["TestOne"], {"TestOne": "pass"}, set())
        for results in ({}, {"TestOne": "pass", "TestExtra": "pass"}, {"TestOne": "fail"}, {"TestOne": "skip"}):
            with self.assertRaises(ValueError):
                ci_shards.check_results(["TestOne"], results, set())
        ci_shards.check_results(["TestOne"], {"TestOne": "skip"}, {"TestOne"})
        for selected in ([], ["TestOne", "TestOne"]):
            with self.assertRaisesRegex(ValueError, "execution selection"):
                ci_shards.check_results(selected, {}, set())

    def test_tip_revision_accepts_git_abbreviations_and_checks_the_actual_commit(self):
        sha = "2557edd671b4ec84af8a5841a621a6abab39774b"
        for version in ("go1.28-devel_2557edd6 Thu Oct 8 11:55:37 2026 -0700",
                        "devel go1.28-2557edd671b4", "go1.28-devel_" + sha):
            ci_shards.validate_tip_version(version, sha)
        for version, source in (("go1.28", sha), ("go1.28-devel_2557edd6", ""),
                                ("go1.28-devel_deadbeef", sha), ("go1.28-devel_2557edd6", "deadbeef" * 5)):
            with self.assertRaisesRegex(ValueError, "differs from toolchain"):
                ci_shards.validate_tip_version(version, source)

    def test_stream_keeps_logs_and_json_with_subtests_and_failure(self):
        events = [dict(Action="output", Output="native diagnostic\n"),
                  dict(Action="pass", Test="TestOne/child"),
                  dict(Action="pass", Test="TestOne")]
        script = "import sys;print('download diagnostic');" + ";".join(
            "print(" + repr(json.dumps(event)) + ")" for event in events) + ";sys.exit(3)"
        with mock.patch("sys.stdout"):
            code, results = ci_shards.stream_tests([sys.executable, "-c", script], self.root, units=False)
        self.assertEqual(3, code)
        self.assertEqual({"TestOne": "pass"}, results)
        self.assertEqual(events, [json.loads(line) for line in (self.root / "tests.jsonl").read_text().splitlines()])
        self.assertEqual("download diagnostic\nnative diagnostic\n", (self.root / "tests.log").read_text())

    def manifests(self):
        source = self.root / "shards"
        source.mkdir()
        for config in ci_shards.CONFIGURATIONS:
            suite, platform, version, mode = config
            for shard in ci_shards.SHARDS[suite]:
                selected = ["example/unit"] if shard == "units" else ci_shards.selected_tests(
                    self.groups, suite, version, shard, platform)
                path = source / f"{suite}-{platform}-{version}-{mode}-{shard}"
                path.mkdir()
                item = dict(schema=1, suite=suite, os=platform, go=version, mode=mode, shard=shard,
                            revision="revision", tree="tree", toolchain=dict(GOVERSION=version, GOOS=ci_shards.PLATFORMS[platform], GOARCH="amd64"),
                            tip_sha="tip" if version == "tip" else "", selected=selected, results=dict.fromkeys(selected, "pass"),
                            status="passed", exit_code=0, duration_seconds=1.5)
                (path / "manifest.json").write_text(json.dumps(item))
        return source

    def test_collect_rejects_missing_duplicate_failed_and_different_inputs(self):
        source = self.manifests()
        self.assertEqual(45, len(ci_shards.collect_manifests(source, self.groups, "revision", "tree")))
        path = next(source.glob("*/manifest.json"))
        original = json.loads(path.read_text())
        for key, value in (("revision", "other"), ("tree", "other"), ("status", "failed"),
                           ("exit_code", 1), ("results", {}), ("selected", ["TestNotSelected"]),
                           ("toolchain", {"GOVERSION": "other"}), ("tip_sha", "other")):
            changed = dict(original, **{key: value})
            path.write_text(json.dumps(changed))
            with self.assertRaises(ValueError):
                ci_shards.collect_manifests(source, self.groups, "revision", "tree")
        path.write_text(json.dumps(original))
        duplicate = source / "duplicate"
        duplicate.mkdir()
        (duplicate / "manifest.json").write_text(path.read_text())
        with self.assertRaisesRegex(ValueError, "duplicated shard"):
            ci_shards.collect_manifests(source, self.groups, "revision", "tree")
        (duplicate / "manifest.json").unlink()
        path.unlink()
        with self.assertRaisesRegex(ValueError, "missing shards"):
            ci_shards.collect_manifests(source, self.groups, "revision", "tree")

    def test_report_collision_never_overwrites_another_group(self):
        source = self.root / "input"
        destination = self.root / "output"
        source.mkdir()
        destination.mkdir()
        (source / "parity.json").write_text("new")
        (destination / "parity.json").write_text("original")
        with self.assertRaisesRegex(ValueError, "multiple shards wrote"):
            ci_shards.copy_reports(source, destination)
        self.assertEqual("original", (destination / "parity.json").read_text())

    def test_aggregate_runs_existing_full_parity_validator(self):
        source = self.manifests()
        fixture = test_parity_report.ParityReportTests()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        fixture.current_feature_evidence()
        fixture.render()
        for suite, platform, version, mode in ci_shards.CONFIGURATIONS:
            if suite != "differential":
                continue
            directory = source / f"{suite}-{platform}-{version}-{mode}-parity"
            for path in fixture.path.parent.glob("*.json"):
                name = path.name.replace("parity", "parity-race", 1) if mode == "race" else path.name
                report = json.loads(path.read_text())
                if path.name == "parity.json":
                    report.update(go=version, os=ci_shards.PLATFORMS[platform], architecture="amd64")
                    if platform != "windows-latest":
                        (directory / name.replace(".json", "-uds.json")).write_text(json.dumps(fixture.evidence["manual"]))
                (directory / name).write_text(json.dumps(report))
        args = type("Args", (), dict(input=source, output=self.root / "reports"))()
        with mock.patch("ci_shards.capture", side_effect=["revision", "tree"]):
            ci_shards.aggregate(args)
        self.assertEqual(6, len(list(args.output.glob("*/parity.md"))))
        self.assertIn("| mini | ubuntu-latest | tip | normal | fuzz |", (args.output / "summary.md").read_text())
        report = next(source.glob("*/parity.json"))
        original = report.read_text()
        report.write_text(json.dumps(dict(json.loads(original), go="other")))
        args.output = self.root / "wrong-toolchain"
        with mock.patch("ci_shards.capture", side_effect=["revision", "tree"]):
            with self.assertRaisesRegex(ValueError, "parity report used a different toolchain"):
                ci_shards.aggregate(args)
        report.write_text(original)
        # Completing Go tests does not excuse missing feature combinations.
        next(source.glob("*/parity-fuzz-examples.json")).unlink()
        args.output = self.root / "incomplete"
        with mock.patch("ci_shards.capture", side_effect=["revision", "tree"]):
            with self.assertRaises(FileNotFoundError):
                ci_shards.aggregate(args)


if __name__ == "__main__":
    unittest.main()
