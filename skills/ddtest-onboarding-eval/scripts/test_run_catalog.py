"""Offline checks of the case catalog and run_catalog.py: no network, no Docker."""

import contextlib
import copy
import io
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

import run_catalog
import run_eval

FAKE_HARNESS = Path(__file__).resolve().parent / "fixtures" / "fake_harness.py"

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
        with patch.object(run_catalog.subprocess, "Popen", side_effect=AssertionError("ran")):
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
        with patch.object(run_catalog.subprocess, "Popen", side_effect=AssertionError("ran")):
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

    def trials(self, root, mode, *ids, interrupts=0, agent="codex", **environment):
        """Run cases against the fake harness, sending SIGINT once every lane is ready."""
        lanes = 2 if agent == "both" else 1
        checkout = root / "trial" / "repo"
        (checkout / ".testoptimization").mkdir(parents=True)
        (checkout / ".testoptimization" / "testdrive.json").write_text(json.dumps({"summary": {"status": "COMPLETE LOCALLY"}}))
        log = root / "harness.log"
        log.touch()

        def interrupt():
            deadline = time.monotonic() + 30
            while log.read_text().count("ready") < lanes and time.monotonic() < deadline:
                time.sleep(0.02)
            for _ in range(interrupts):
                os.kill(os.getpid(), signal.SIGINT)
                time.sleep(0.3)

        sender = threading.Thread(target=interrupt)
        variables = {"FAKE_HARNESS_LOG": str(log), "FAKE_HARNESS_MODE": mode, "FAKE_HARNESS_CHECKOUT": str(checkout),
                     **environment}
        output = io.StringIO()
        with patch.object(run_catalog, "RUN_EVAL", FAKE_HARNESS), patch.dict(os.environ, variables), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            if interrupts:
                sender.start()
            try:
                code = run_catalog.main([*ids, "--image-map", "agents=img", "--ddtest", str(root / "ddtest"),
                                         "--output", str(root / "out"), "--existing-login", "--agent", agent])
            finally:
                if interrupts:
                    sender.join()
        return code, output.getvalue(), log.read_text().splitlines()

    def test_cases_run_one_at_a_time_then_retain_and_clean_up(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            code, output, events = self.trials(root, "complete", "E01", "E02")
            self.assertEqual(code, 0)
            self.assertEqual([event.split()[0] for event in events],
                             ["start", "finished", "cleanup", "start", "finished", "cleanup"])
            results = json.loads((root / "out" / "results.json").read_text())
            self.assertEqual([(case["id"], case["agent"], case["outcome"]) for case in results["cases"]],
                             [("E01-nion", "codex", "pass"), ("E02-pipeline-schema", "codex", "pass")])
            self.assertEqual(results["exit_code"], 0)
            self.assertIn("[codex] E01-nion: started", (root / "out" / "lane-codex.log").read_text())
            self.assertIn("start E01-nion-codex session=True", events)
            self.assertEqual(events[2], "cleanup " + str(root / "out" / "E01-nion-codex"))
            self.assertTrue((root / "out" / "E01-nion-codex" / "retained" / ".testoptimization" / "testdrive.json").is_file())
            self.assertIn("--existing-login", (root / "out" / "E01-nion-codex.log").read_text().splitlines()[0])
            self.assertIn("[codex] E01-nion: exit 0, run completed, report COMPLETE LOCALLY (expected COMPLETE LOCALLY)", output)
            self.assertIn("outcome pass", output)

    def test_interrupt_waits_for_teardown_then_retains_cleans_up_and_stops(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            code, output, events = self.trials(root, "teardown", "E01", "E02", interrupts=1, FAKE_HARNESS_TEARDOWN="1.5")
            self.assertEqual(code, 128 + signal.SIGINT)
            evidence = root / "out" / "E01-nion-codex"
            # Forwarded once, waited for past teardown, then cleaned up; E02 never starts.
            self.assertEqual([event.split(" at=")[0] for event in events],
                             [f"start {evidence.name} session=True", "ready", f"signal {int(signal.SIGINT)}",
                              f"finished {evidence.name}", f"cleanup {evidence}"])
            self.assertEqual(json.loads((evidence / "run.json").read_text())["signals"], [signal.SIGINT])
            self.assertTrue((evidence / "retained" / ".testoptimization" / "testdrive.json").is_file())
            self.assertFalse((root / "out" / "E02-pipeline-schema-codex").exists())
            self.assertIn("run interrupted", output)
            self.assertIn("not run: E02-pipeline-schema", output)
            results = json.loads((root / "out" / "results.json").read_text())
            self.assertEqual(results["exit_code"], 130)
            self.assertEqual([(case["id"], case["outcome"], case["harness_category"]) for case in results["cases"]],
                             [("E01-nion", "harness_failure", "interrupted")])

    def test_second_interrupt_kills_the_trial_and_still_cleans_up(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            code, output, events = self.trials(root, "stuck", "E01", "E02", interrupts=2)
            self.assertEqual(code, 128 + signal.SIGINT)
            evidence = root / "out" / "E01-nion-codex"
            self.assertEqual(events.count(f"signal {int(signal.SIGINT)}"), 1)
            self.assertFalse(any(event.startswith(f"finished {evidence.name}") for event in events))
            self.assertEqual(events[-1], f"cleanup {evidence}")
            self.assertIn("nothing retained", (root / "out" / "E01-nion-codex.log").read_text())
            self.assertIn("Killing 1 trial(s); their cleanup still runs; if it does not finish, run: ", output)
            self.assertFalse((root / "out" / "E02-pipeline-schema-codex").exists())


    def test_both_agents_run_in_two_concurrent_lanes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            barrier = root / "barrier"
            barrier.mkdir()
            code, output, events = self.trials(root, "barrier", "E01", "E02", agent="both",
                                               FAKE_HARNESS_BARRIER=str(barrier))
            self.assertEqual(code, 0, output)
            self.assertNotIn("barrier timeout", "\n".join(events))
            # Both lanes' first trials were running together: each passed the barrier only once the other started.
            starts = [event.split()[1] for event in events if event.startswith("start ")]
            first_finish = next(index for index, event in enumerate(events) if event.startswith("finished "))
            self.assertEqual({event.split()[1] for event in events[:first_finish] if event.startswith("start ")},
                             {"E01-nion-codex", "E01-nion-claude"})
            for agent in ("codex", "claude"):
                self.assertEqual([name for name in starts if name.endswith(agent)],
                                 [f"E01-nion-{agent}", f"E02-pipeline-schema-{agent}"])
                lane = (root / "out" / f"lane-{agent}.log").read_text()
                self.assertLess(lane.index(f"[{agent}] E01-nion: exit 0"), lane.index(f"[{agent}] E02-pipeline-schema: started"))
                self.assertNotIn("[codex]" if agent == "claude" else "[claude]", lane)
            self.assertIn("--agent claude", (root / "out" / "E02-pipeline-schema-claude.log").read_text().splitlines()[0])
            results = json.loads((root / "out" / "results.json").read_text())
            self.assertEqual([(case["id"], case["agent"], case["outcome"]) for case in results["cases"]],
                             [("E01-nion", "codex", "pass"), ("E01-nion", "claude", "pass"),
                              ("E02-pipeline-schema", "codex", "pass"), ("E02-pipeline-schema", "claude", "pass")])
            self.assertEqual((results["exit_code"], results["agents_disagree"]), (0, []))
            table = output[output.index("case "):]
            self.assertRegex(table, r"E01-nion\s+claude\s+circleci\s+jest\s+COMPLETE LOCALLY\s+COMPLETE LOCALLY\s+pass")

    def test_interrupt_reaches_both_lanes_once_and_cleans_up_both(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            code, output, events = self.trials(root, "teardown", "E01", "E02", interrupts=1, agent="both",
                                               FAKE_HARNESS_TEARDOWN="1")
            self.assertEqual(code, 128 + signal.SIGINT)
            for agent in ("codex", "claude"):
                evidence = root / "out" / f"E01-nion-{agent}"
                self.assertEqual(json.loads((evidence / "run.json").read_text())["signals"], [signal.SIGINT])
                self.assertIn(f"cleanup {evidence}", events)
                self.assertTrue((evidence / "retained" / ".testoptimization" / "testdrive.json").is_file())
                self.assertFalse((root / "out" / f"E02-pipeline-schema-{agent}").exists())
            self.assertEqual(events.count(f"signal {int(signal.SIGINT)}"), 2)
            self.assertIn("waiting for 2 trial(s)", output)
            results = json.loads((root / "out" / "results.json").read_text())
            self.assertEqual(results["exit_code"], 130)
            self.assertEqual({(case["agent"], case["outcome"]) for case in results["cases"]},
                             {("codex", "harness_failure"), ("claude", "harness_failure")})

    def test_second_interrupt_kills_both_lanes_and_still_cleans_up(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            code, output, events = self.trials(root, "stuck", "E01", "E02", interrupts=2, agent="both")
            self.assertEqual(code, 128 + signal.SIGINT)
            self.assertEqual(events.count(f"signal {int(signal.SIGINT)}"), 2)
            for agent in ("codex", "claude"):
                self.assertIn(f"cleanup {root / 'out' / f'E01-nion-{agent}'}", events)
                self.assertFalse((root / "out" / f"E02-pipeline-schema-{agent}").exists())
            self.assertIn("Killing 2 trial(s)", output)
            killed = output[output.index("Killing 2 trial(s)"):].splitlines()[0]
            self.assertIn("--cleanup " + str(root / "out" / "E01-nion-codex"), killed)
            self.assertIn("--cleanup " + str(root / "out" / "E01-nion-claude"), killed)

    def test_retention_failure_still_cleans_up_and_the_batch_goes_on(self):
        real_retain = run_catalog.retain

        def retain(output):
            if output.name.startswith("E01-"):
                raise OSError(28, "No space left on device")
            return real_retain(output)

        for agent in ("codex", "both"):
            with self.subTest(agent=agent), tempfile.TemporaryDirectory() as temporary, \
                    patch.object(run_catalog, "retain", retain):
                root = Path(temporary)
                code, output, events = self.trials(root, "complete", "E01", "E02", agent=agent)
                self.assertEqual(code, 1, output)
                agents = ("codex", "claude") if agent == "both" else ("codex",)
                for name in agents:
                    for case in ("E01-nion", "E02-pipeline-schema"):
                        self.assertIn(f"cleanup {root / 'out' / f'{case}-{name}'}", events)
                    self.assertIn(f"[{name}] E01-nion: retention failed: [Errno 28] No space left on device", output)
                    self.assertIn("retention failed", (root / "out" / f"E01-nion-{name}.log").read_text())
                results = json.loads((root / "out" / "results.json").read_text())
                outcomes = {(case["id"], case["agent"]): case for case in results["cases"]}
                for name in agents:
                    failed = outcomes[("E01-nion", name)]
                    self.assertEqual((failed["outcome"], failed["harness_category"]), ("harness_failure", "retention"))
                    self.assertIn("retention failed: [Errno 28] No space left on device", failed["problems"])
                    self.assertIn("/retained failed", failed["next_step"])
                    self.assertEqual(outcomes[("E02-pipeline-schema", name)]["outcome"], "pass")

    def test_dry_run_both_prints_each_lane(self):
        code, output = self.main("--dry-run", "E02", "E07", "--agent", "both")
        self.assertEqual(code, 0)
        self.assertLess(output.index("## lane codex"), output.index("## lane claude"))
        claude = output[output.index("## lane claude"):]
        trials = [shlex.split(line) for line in claude.splitlines() if "run_eval.py --image" in line]
        self.assertEqual(len(trials), 2)
        for argv in trials:
            self.assertEqual(argv[argv.index("--agent") + 1], "claude")
            self.assertTrue(argv[argv.index("--output") + 1].endswith("-claude"))
        self.assertIn("<output>/E07-qwed-ucp-claude", claude)
        self.assertEqual(output.count(" --cleanup "), 4)


class RetainTests(unittest.TestCase):
    def test_unquotes_git_paths(self):
        for printed, path in (("plain.txt", "plain.txt"), ('"a b.txt"', "a b.txt"), ('"say \\"hi\\".txt"', 'say "hi".txt'),
                              ('"line\\nbreak\\ttab\\\\.txt"', "line\nbreak\ttab\\.txt"),
                              ('"caf\\303\\251.txt"', "caf\u00e9.txt"), ('"\\346\\227\\245.txt"', "\u65e5.txt")):
            with self.subTest(printed):
                self.assertEqual(run_catalog.unquote_path(printed), path)
        with self.assertRaises(ValueError):
            run_catalog.unquote_path('"bad\\q"')

    def test_retains_untracked_files_with_quoted_names(self):
        names = ["a b.txt", 'say "hi".txt', "line\nbreak.txt", "caf\u00e9/r\u00e9sum\u00e9.yml", "tab\there.txt"]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            checkout, output = root / "repo", root / "out"
            checkout.mkdir()
            output.mkdir()
            for name in names:
                (checkout / name).parent.mkdir(parents=True, exist_ok=True)
                (checkout / name).write_text(name)
            (output / "run.json").write_text(json.dumps({"checkout": str(checkout)}))
            git = subprocess.run(["git", "init", "-q", str(checkout)], capture_output=True)
            if git.returncode:
                self.skipTest("git is unavailable")
            status = subprocess.run(["git", "-c", "core.quotePath=true", "status", "--short", "--untracked-files=all"],
                                    cwd=checkout, capture_output=True, text=True, check=True).stdout
            self.assertTrue(status.isascii())
            self.assertIn('?? "a b.txt"', status)
            (output / "status.txt").write_text(status + '?? "../outside.txt"\n?? "/etc/hosts"\n?? "bad\\q"\n')
            run_catalog.retain(output)
            retained = output / "retained"
            for name in names:
                with self.subTest(name):
                    self.assertEqual((retained / "untracked" / name).read_text(), name)
            self.assertEqual(sorted(path.name for path in (retained / "untracked").rglob("*") if path.is_file()),
                             sorted(Path(name).name for name in names))
            listed = (retained / "untracked.txt").read_text().splitlines()
            self.assertEqual(len(listed), len(names) + 3)
            self.assertIn('"a b.txt"', listed)

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
