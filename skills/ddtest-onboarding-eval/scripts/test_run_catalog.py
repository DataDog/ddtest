"""Offline checks of the case catalog and run_catalog.py: no network, no Docker."""

import contextlib
import copy
import io
import json
import os
from pathlib import Path
import re
import shlex
import tempfile
import unittest
from unittest.mock import patch

import run_catalog
import run_eval

try:
    import yaml
except ImportError:
    yaml = None


def setup_commands(argv):
    return [argv[index + 1] for index, value in enumerate(argv) if value == "--setup-command"]


def scratch_commits(argv):
    return [command for command in setup_commands(argv) if command.startswith("git -c user.name=ddtest-eval-scratch")]


@unittest.skipIf(yaml is None, "PyYAML is not installed")
class CatalogTests(unittest.TestCase):
    def setUp(self):
        self.catalog = run_catalog.load_catalog()
        self.cases = {case["id"]: case for case in self.catalog["cases"]}

    def test_catalog_is_valid(self):
        self.assertEqual(run_catalog.validate(self.catalog), [])

    def test_cases_cover_each_framework_once_and_every_provider(self):
        cases = self.catalog["cases"]
        self.assertEqual(len(self.cases), len(cases))
        self.assertEqual(sorted(case["framework"] for case in cases), sorted(run_eval.FRAMEWORKS))
        self.assertEqual({case["ci_provider"] for case in cases}, set(run_catalog.PROVIDERS))
        for case in cases:
            self.assertRegex(case["commit"], r"^[0-9a-f]{40}$")
            for field in run_catalog.CASE_FIELDS:
                self.assertIn(field, case, case["id"])

    def test_catalog_has_no_local_state(self):
        text = run_catalog.CATALOG.read_text()
        for stale in ("sha256:73f77a38", "previous_id", "trial_result", "agent_timing", "/tmp/eval", "gpgsign"):
            self.assertNotIn(stale, text)

    def test_validation_reports_broken_catalogs(self):
        broken = copy.deepcopy(self.catalog)
        broken["cases"] = [case for case in broken["cases"] if case["ci_provider"] != "jenkins"]
        broken["cases"][1]["id"] = broken["cases"][0]["id"]
        broken["cases"][2]["commit"] = "main"
        broken["cases"][3]["framework"] = "jest"
        broken["cases"][4]["trims"]["optional_speed"] = {"reason": "speed", "commands": []}
        broken["cases"][5]["harness"]["answers"] = ["ci_runtime=node"]
        problems = "\n".join(run_catalog.validate(broken))
        for expected in ("duplicate id", "commit must be 40", "framework jest: 2 cases", "providers not covered: jenkins",
                         "trims.optional_speed needs a reason and commands", "answer ci_runtime=node"):
            self.assertIn(expected, problems)


@unittest.skipIf(yaml is None, "PyYAML is not installed")
class ArgvTests(unittest.TestCase):
    def setUp(self):
        self.cases = {case["id"]: case for case in run_catalog.load_catalog()["cases"]}

    def argv(self, identifier, **options):
        return run_catalog.build_argv(self.cases[identifier], "sha256:" + "1" * 64, Path("/abs/ddtest"), Path("/abs/out"), **options)

    def test_plain_case_maps_catalog_fields(self):
        argv = self.argv("E01-nion", existing_login=True)
        self.assertEqual(argv[1], str(run_catalog.RUN_EVAL))
        for flag, value in (("--image", "sha256:" + "1" * 64), ("--repo", "https://github.com/Patreon/nion.git"),
                            ("--ref", "16cb1cd923b493fba12324f2a211fbe59d47a505"), ("--agent", "codex"),
                            ("--framework", "jest"), ("--timeout", "5400"), ("--output", "/abs/out")):
            self.assertEqual(argv[argv.index(flag) + 1], value)
        self.assertIn("--existing-login", argv)
        self.assertEqual(setup_commands(argv), ["npm ci --no-audit --no-fund"])
        self.assertNotIn("--user-answer", argv)

    def test_python_answers_and_readiness(self):
        argv = self.argv("E07-qwed-ucp")
        self.assertEqual(argv[argv.index("--python") + 1], "/usr/local/bin/python3")
        self.assertEqual(argv[argv.index("--user-answer") + 1], "ci_provider=buildkite")
        self.assertNotIn("--existing-login", argv)
        java = self.argv("E12-kalamar-export")
        self.assertEqual(java[java.index("--readiness-command") + 1], "mvn -B -q dependency:go-offline")

    def test_required_trims_are_committed_before_setup(self):
        argv = self.argv("E11-cdoc2-auth")
        commands = setup_commands(argv)
        self.assertTrue(commands[0].startswith("git rm -q src/test/java/ee/cyber/cdoc2/auth/AuthTest.java"))
        commit = shlex.split(commands[1])
        self.assertEqual(commit[:-1], run_catalog.SCRATCH_COMMIT)
        self.assertTrue(commit[-1].startswith("Eval trim (required): "))
        self.assertEqual(argv.count("--setup-command"), 2)

    def test_speed_trims_only_when_requested(self):
        self.assertEqual(scratch_commits(self.argv("E08-kitchensink")), [])
        trimmed = setup_commands(self.argv("E08-kitchensink", speed_trims=True))
        self.assertIn("git rm -q $f", trimmed[0])
        self.assertIn("Eval trim (optional_speed)", trimmed[1])
        self.assertEqual(trimmed[2:], ["npm ci --no-audit --no-fund"])

    def test_required_then_speed_trims_then_setup(self):
        required = setup_commands(self.argv("E09-lux-design-system"))
        self.assertEqual(len(scratch_commits(self.argv("E09-lux-design-system"))), 1)
        self.assertNotIn("git rm -q end2end/singleselectasync.spec.js", required)
        both = setup_commands(self.argv("E09-lux-design-system", speed_trims=True))
        kinds = [re.search(r"Eval trim \((\w+)\)", command).group(1) for command in both if "Eval trim" in command]
        self.assertEqual(kinds, ["required", "optional_speed"])
        self.assertLess(both.index("git rm -q end2end/singleselectasync.spec.js"), both.index("npm ci --no-audit --no-fund"))
        self.assertTrue(both[0].startswith("perl "))

    def test_trims_apply_only_where_listed(self):
        for identifier, case in self.cases.items():
            listed = [kind for kind in run_catalog.TRIM_KINDS if case["trims"][kind]]
            with self.subTest(identifier):
                self.assertEqual(len(scratch_commits(self.argv(identifier, speed_trims=True))), len(listed))
                self.assertEqual(len(scratch_commits(self.argv(identifier))), int("required" in listed))
                self.assertNotIn("gpgsign", " ".join(self.argv(identifier, speed_trims=True)))

    def test_trim_commands_survive_run_eval_parsing(self):
        for case in self.cases.values():
            for command in run_catalog.trim_commands(case, speed_trims=True):
                self.assertTrue(shlex.split(command))


@unittest.skipIf(yaml is None, "PyYAML is not installed")
class MainTests(unittest.TestCase):
    def main(self, *argv):
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(io.StringIO()):
            code = run_catalog.main(list(argv))
        return code, output.getvalue()

    def test_dry_run_prints_arguments_without_running(self):
        with patch.object(run_catalog.subprocess, "call", side_effect=AssertionError("ran")):
            code, output = self.main("--dry-run", "E02", "--speed-trims", "--image-map", "agents=img")
        self.assertEqual(code, 0)
        lines = [line for line in output.splitlines() if line and not line.startswith("#")]
        trial = shlex.split(lines[0])
        self.assertEqual(trial[trial.index("--image") + 1], "img")
        self.assertEqual(trial[trial.index("--output") + 1], "<output>/E02-pipeline-schema-codex")
        self.assertEqual(shlex.split(lines[1])[-2:], ["--cleanup", "<output>/E02-pipeline-schema-codex"])

    def test_dry_run_all_prints_every_case(self):
        code, output = self.main("--dry-run", "--all")
        self.assertEqual(code, 0)
        self.assertEqual(output.count(" --cleanup "), len(run_catalog.load_catalog()["cases"]))
        self.assertIn("<image:java17>", output)

    def test_trials_need_images_ddtest_and_output(self):
        with patch.object(run_catalog.subprocess, "call", side_effect=AssertionError("ran")):
            for argv in (["E01"], ["E01", "--image-map", "agents=img"], ["--all", "--ddtest", "d", "--output", "o"]):
                with self.assertRaises(SystemExit):
                    self.main(*argv)
            with self.assertRaises(SystemExit):
                self.main("E99")

    def test_image_file_then_map(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "images.json"
            path.write_text(json.dumps({"agents": "a", "java17": "j"}))
            with patch.dict(os.environ, {run_catalog.IMAGES_VARIABLE: str(path)}):
                self.assertEqual(run_catalog.resolve_images(["java17=k"]), {"agents": "a", "java17": "k"})
        with self.assertRaises(ValueError):
            run_catalog.resolve_images(["agents"])

    def test_cases_run_one_at_a_time_then_retain_and_clean_up(self):
        calls = []
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            checkout = root / "trial" / "repo"
            (checkout / ".testoptimization").mkdir(parents=True)
            (checkout / ".testoptimization" / "testdrive.json").write_text(json.dumps({"summary": {"status": "COMPLETE LOCALLY"}}))

            def call(argv, **_):
                calls.append(argv)
                if "--cleanup" not in argv:
                    evidence = Path(argv[argv.index("--output") + 1])
                    evidence.mkdir()
                    (evidence / "run.json").write_text(json.dumps({"status": "completed", "checkout": str(checkout)}))
                return 0

            with patch.object(run_catalog.subprocess, "call", side_effect=call):
                code, output = self.main("E01", "E02", "--image-map", "agents=img", "--ddtest", str(root / "ddtest"),
                                         "--output", str(root / "out"), "--existing-login")
            self.assertEqual(code, 0)
            self.assertEqual([("--cleanup" in argv) for argv in calls], [False, True, False, True])
            self.assertTrue(all("--existing-login" in argv for argv in calls[::2]))
            self.assertTrue((root / "out" / "E01-nion-codex" / "retained" / ".testoptimization" / "testdrive.json").is_file())
            self.assertTrue((root / "out" / "E01-nion-codex.log").is_file())
            self.assertIn("E01-nion: exit 0, run completed, report COMPLETE LOCALLY (expected COMPLETE LOCALLY)", output)


class RetainTests(unittest.TestCase):
    def test_retains_reports_and_small_untracked_files_without_following_links(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            checkout, output, outside = root / "repo", root / "out", root / "secret"
            (checkout / ".testoptimization").mkdir(parents=True)
            output.mkdir()
            outside.write_text("secret")
            (checkout / ".testoptimization" / "testdrive.json").write_text("{}")
            (checkout / ".testoptimization" / "testdrive.html").write_text("<html>")
            (checkout / ".testoptimization" / "link.json").symlink_to(outside)
            (checkout / "ci").mkdir()
            (checkout / "ci" / "setup.rb").write_text("# setup")
            (checkout / "escape.txt").symlink_to(outside)
            (checkout / "big.log").write_text("x" * run_catalog.UNTRACKED_LIMIT)
            (output / "run.json").write_text(json.dumps({"checkout": str(checkout)}))
            (output / "status.txt").write_text(" M .gitlab-ci.yml\n?? ci/setup.rb\n?? escape.txt\n?? big.log\n?? .testoptimization/testdrive.json\n")
            self.assertIn("retained 2 report file(s)", run_catalog.retain(output))
            retained = output / "retained"
            self.assertEqual(sorted(path.name for path in (retained / ".testoptimization").iterdir()), ["testdrive.html", "testdrive.json"])
            self.assertEqual((retained / "untracked" / "ci" / "setup.rb").read_text(), "# setup")
            self.assertFalse((retained / "untracked" / "escape.txt").exists())
            self.assertFalse((retained / "untracked" / "big.log").exists())
            self.assertEqual((retained / "untracked.txt").read_text().split(), ["ci/setup.rb", "escape.txt", "big.log", ".testoptimization/testdrive.json"])

    def test_missing_run_record_retains_nothing(self):
        with tempfile.TemporaryDirectory() as temporary:
            self.assertIn("nothing retained", run_catalog.retain(Path(temporary)))


if __name__ == "__main__":
    unittest.main()
