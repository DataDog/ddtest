"""Verify active PR selection, reverse Go dependencies and required check gates."""

import copy
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ci_test_plan as planner


ROOT = Path(__file__).resolve().parents[1]
CONFIG = json.loads(planner.CONFIG.read_text())


class SelectionTests(unittest.TestCase):
    def test_python_change_selects_only_python_smoke(self):
        plan = planner.make_plan(["internal/testdrive/python_validation.go"], CONFIG)
        self.assertEqual(plan["suites"], ["python"])
        self.assertEqual(plan["integrations"], [{"id": "python", "runtime": "python"}])
        self.assertTrue(plan["go"])
        self.assertFalse(plan["ddto"])
        self.assertEqual(plan["mode"], "fast")

    def test_shared_unknown_build_and_deleted_inputs_select_every_smoke(self):
        for path in ("internal/ext/command.go", "internal/onboard/ci_shell_state.go",
                     "internal/testdrive/jest_features.go", "internal/testdrive/mocha.go",
                     "internal/testdrive/new_fixture.json",
                     "go.mod", "go.sum", ".github/workflows/ci.yml"):
            with self.subTest(path=path):
                plan = planner.make_plan([path], CONFIG)
                self.assertEqual(set(plan["suites"]), set(CONFIG["suites"]))
                self.assertTrue(plan["playwright"])

    def test_docs_only_and_empty_diff_omit_runtime_checks(self):
        for files in ([], ["docs/onboarding.md", "README.md", "ddto/docs/validation.md"]):
            plan = planner.make_plan(files, CONFIG)
            self.assertFalse(plan["go"])
            self.assertFalse(plan["ddto"])
            self.assertFalse(plan["playwright"])
            self.assertEqual(plan["integrations"], [])

    def test_unknown_history_runs_all_checks(self):
        plan = planner.make_plan(None, CONFIG)
        self.assertEqual(set(plan["suites"]), set(CONFIG["suites"]))
        self.assertTrue(plan["go"] and plan["ddto"] and plan["evaluator"])

    def test_embedded_instructions_and_scripts_are_code_inputs(self):
        for path in ("internal/onboard/instructions/pytest.md", "internal/testdrive/scripts/pytest_observer.py"):
            plan = planner.make_plan([path], CONFIG)
            self.assertIn("python", plan["suites"])
            self.assertTrue(plan["go"])

    def test_javascript_dependencies_propagate_to_every_javascript_consumer(self):
        plan = planner.make_plan(["internal/platform/scripts/javascript_env.js"], CONFIG)
        self.assertEqual(set(plan["suites"]), {"javascript", "jest", "vitest", "mocha", "cypress",
                                              "playwright", "cucumber", "webdriverio", "onboarding"})

    def test_javascript_sources_select_only_javascript_consumers(self):
        consumers = {"javascript", "jest", "vitest", "mocha", "cypress", "playwright",
                     "cucumber", "webdriverio", "onboarding"}
        for directory in ("platform", "onboard", "testdrive"):
            for name in ("javascript.go", "javascript_language.go"):
                path = f"internal/{directory}/{name}"
                with self.subTest(path=path):
                    self.assertEqual(set(planner.make_plan([path], CONFIG)["suites"]), consumers)

    def test_java_sources_still_select_both_build_tool_smokes(self):
        for path, suites in (
            ("internal/platform/java.go", {"java"}),
            ("internal/onboard/java_ci.go", {"java", "onboarding"}),
            ("internal/testdrive/java_validation.go", {"java"}),
        ):
            with self.subTest(path=path):
                plan = planner.make_plan([path], CONFIG)
                self.assertEqual(set(plan["suites"]), suites)
                self.assertTrue({"java-maven", "java-gradle"}.issubset(
                    entry["id"] for entry in plan["integrations"]))

    def test_cross_framework_onboarding_helpers_select_all_smokes(self):
        for path in ("internal/onboard/jest_command.go", "internal/onboard/python_handwritten.go"):
            with self.subTest(path=path):
                plan = planner.make_plan([path], CONFIG)
                self.assertEqual(set(plan["suites"]), set(CONFIG["suites"]))

    def test_cross_framework_coverage_and_path_helpers_select_their_consumers(self):
        for path, expected in (
            ("internal/testdrive/cucumber_coverage.go", set(CONFIG["suites"])),
            ("internal/testdrive/cypress_coverage.go", set(CONFIG["suites"])),
            ("internal/framework/cypress.go", {"cypress", "playwright"}),
        ):
            with self.subTest(path=path):
                self.assertEqual(set(planner.make_plan([path], CONFIG)["suites"]), expected)

    def test_mocha_fixture_helper_tracks_webdriverio_without_expanding_unit_only_checks(self):
        path = "internal/testdrive/mocha_validation_test.go"
        self.assertEqual(planner.affected_suites(path, CONFIG), {"mocha", "webdriverio", "onboarding"})
        plan = planner.make_plan([path], CONFIG)
        self.assertTrue(plan["go"])
        self.assertEqual(plan["integrations"], [])
        self.assertFalse(plan["playwright"])

    def test_instrumentation_changes_enable_native_module_checks(self):
        for path in ("ddto/internal/runner/run.go", "main.go", "go.mod", "internal/cmd/instrument.go"):
            self.assertTrue(planner.make_plan([path], CONFIG)["ddto"])

    def test_fast_workflow_changes_execute_its_conditional_checks(self):
        plan = planner.make_plan([".github/workflows/ci.yml"], CONFIG)
        self.assertTrue(plan["go"])
        self.assertTrue(plan["ddto"])
        self.assertTrue(plan["evaluator"])

    def test_instrumentation_module_changes_do_not_run_other_language_frameworks(self):
        plan = planner.make_plan(["ddto/internal/runner/run.go"], CONFIG)
        self.assertTrue(plan["ddto"] and plan["go"])
        self.assertEqual(plan["integrations"], [])
        self.assertFalse(plan["playwright"])

    def test_evaluator_checks_are_selected_for_evaluator_changes(self):
        plan = planner.make_plan(["skills/ddtest-onboarding-eval/scripts/run_eval.py"], CONFIG)
        self.assertTrue(plan["evaluator"])
        self.assertFalse(plan["go"])
        self.assertEqual(plan["integrations"], [])
        self.assertFalse(planner.make_plan(["internal/testdrive/python_validation.go"], CONFIG)["evaluator"])

    def test_unit_test_edits_run_go_without_external_framework_smokes(self):
        plan = planner.make_plan(["internal/testdrive/jest_test.go"], CONFIG)
        self.assertTrue(plan["go"])
        self.assertEqual(plan["integrations"], [])
        self.assertFalse(plan["playwright"])


class GoSelectionTests(unittest.TestCase):
    def setUp(self):
        self.root = Path("/example").resolve()
        self.packages = [
            {"Dir": str(self.root / "lib"), "ImportPath": "example/lib"},
            {"Dir": str(self.root / "consumer"), "ImportPath": "example/consumer", "Imports": ["example/lib"]},
            {"Dir": str(self.root / "external"), "ImportPath": "example/external", "XTestImports": ["example/consumer"]},
            {"Dir": str(self.root / "other"), "ImportPath": "example/other"},
        ]

    def test_production_change_reaches_transitive_test_importers(self):
        self.assertEqual(planner.select_packages(self.packages, ["lib/lib.go"], self.root),
                         ["example/consumer", "example/external", "example/lib"])

    def test_test_only_change_does_not_invalidate_production_consumers(self):
        self.assertEqual(planner.select_packages(self.packages, ["lib/lib_test.go"], self.root), ["example/lib"])

    def test_non_go_inputs_deleted_packages_and_unknown_history_run_all_packages(self):
        expected = sorted(package["ImportPath"] for package in self.packages)
        for files in (None, ["go.mod"], ["lib/testdata/input.txt"], ["deleted/old.go"], ["ddto/runtime.go"]):
            self.assertEqual(planner.select_packages(self.packages, files, self.root), expected)

    def test_go_json_stream_is_decoded_without_compiling_test_binaries(self):
        text = "\n" + "\n".join(json.dumps(package) for package in self.packages)
        self.assertEqual(list(planner.json_objects(text)), self.packages)

    def test_docs_do_not_start_go(self):
        with patch.object(planner.subprocess, "run") as run:
            planner.run_unit_tests(self.root, ["README.md"], CONFIG)
        run.assert_not_called()

    def test_native_run_keeps_package_selection_race_and_full_package_budget(self):
        inventory = subprocess.CompletedProcess([], 0, "\n".join(json.dumps(p) for p in self.packages))
        with patch.object(planner.subprocess, "run", return_value=inventory) as run:
            planner.run_unit_tests(self.root, ["lib/lib_test.go"], CONFIG, race=True)
        command = run.call_args.args[0]
        self.assertEqual(command[:2], ["go", "test"])
        self.assertIn("-race", command)
        self.assertEqual([arg for arg in command[2:] if not arg.startswith("-")], ["example/lib"])
        timeout = next(arg for arg in command if arg.startswith("-timeout="))
        package_minutes = int(timeout.removeprefix("-timeout=").removesuffix("m"))
        # Runtime-backed tests must fit the same 20-minute package budget as
        # the full CI lane, with extra wall time for setup and compilation.
        self.assertGreaterEqual(package_minutes, 20)
        fast = (ROOT / ".github/workflows/ci.yml").read_text()
        unit = fast.split("  unit:\n", 1)[1].split("\n  scripts:\n", 1)[0]
        job_minutes = int(re.search(r"timeout-minutes: (\d+)", unit).group(1))
        self.assertGreaterEqual(job_minutes, package_minutes + 5)


class GitDiffTests(unittest.TestCase):
    def test_diff_includes_worktree_deletions_both_sides_of_renames_and_untracked_files(self):
        responses = [subprocess.CompletedProcess([], 0, "base\n"),
                     subprocess.CompletedProcess([], 0, b"old.go\0new.go\0deleted.go\0"),
                     subprocess.CompletedProcess([], 0, b"untracked.go\0")]
        with patch.object(planner.subprocess, "run", side_effect=responses) as run:
            self.assertEqual(planner.changed_files(Path('.'), "origin/main"),
                             ["deleted.go", "new.go", "old.go", "untracked.go"])
        self.assertIn("--no-renames", run.call_args_list[1].args[0])

    def test_missing_history_cannot_skip_checks(self):
        for base in ("", "0" * 40):
            self.assertIsNone(planner.changed_files(Path('.'), base))
        with patch.object(planner.subprocess, "run", side_effect=subprocess.CalledProcessError(1, ["git"])):
            self.assertIsNone(planner.changed_files(Path('.'), "missing"))


class WorkflowTests(unittest.TestCase):
    def test_full_matrix_is_preserved_and_gates_release(self):
        full = (ROOT / ".github/workflows/ci-full.yml").read_text()
        jobs = set(re.findall(r"^  ([\w-]+):$", full.split("jobs:\n", 1)[1], re.M))
        for suite in CONFIG["suites"].values():
            self.assertTrue(set(suite["jobs"]).issubset(jobs))
        self.assertIn("uses: ./.github/workflows/ci.yml", full)
        self.assertIn("uses: ./.github/workflows/go-instrumentation.yml", full)
        release = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertIn("uses: ./.github/workflows/ci-full.yml", release)
        self.assertIn("needs: compatibility", release)

    def test_fast_workflow_omits_historical_matrix_coverage_and_sdk_weaving(self):
        fast = (ROOT / ".github/workflows/ci.yml").read_text()
        self.assertNotIn("test-shards:", fast)
        self.assertNotIn("Configure Datadog Test Optimization", fast)
        self.assertNotIn("-coverpkg=./...", fast)
        self.assertNotIn("shadow", fast)
        self.assertIn("ci_test_plan.py run", fast)
        self.assertIn("ci_smoke.py --id", fast)
        self.assertIn("  lint:\n", fast)

    def test_fast_smokes_have_unique_ids_and_explicit_required_cases(self):
        entries = [entry for suite in CONFIG["suites"].values() for entry in suite["smoke"]]
        self.assertEqual(len(entries), len({entry["id"] for entry in entries}))
        for entry in entries:
            self.assertTrue(entry["required"])
            self.assertTrue(entry["tests"].startswith("^Test"))

    def test_smokes_preserve_integration_budgets_and_allow_job_setup_time(self):
        # These are the existing full lanes' Go test allowances, including Go's
        # default ten minutes for the JavaScript platform compatibility tests.
        budgets = {"python": 20, "ruby": 20, "java-maven": 20, "java-gradle": 20,
                   "javascript": 10, "jest": 12, "vitest": 15, "mocha": 10,
                   "cypress": 12, "playwright": 12, "cucumber": 12,
                   "webdriverio": 12, "onboarding": 8}
        workflow = (ROOT / ".github/workflows/ci.yml").read_text()
        integration = workflow.split("  integration:\n", 1)[1].split("\n  playwright:\n", 1)[0]
        playwright = workflow.split("  playwright:\n", 1)[1].split("\n  evaluator-isolation:\n", 1)[0]
        for suite in CONFIG["suites"].values():
            for entry in suite["smoke"]:
                with self.subTest(smoke=entry["id"]):
                    minutes = entry["timeout_minutes"]
                    self.assertGreaterEqual(minutes, budgets[entry["id"]])
                    job = playwright if entry["runtime"] == "playwright" else integration
                    job_minutes = int(re.search(r"timeout-minutes: (\d+)", job).group(1))
                    self.assertGreaterEqual(job_minutes, minutes + 5)

    def test_fast_units_provision_and_require_project_runtimes(self):
        fast = (ROOT / ".github/workflows/ci.yml").read_text()
        unit = fast.split("  unit:\n", 1)[1].split("\n  scripts:\n", 1)[0]
        for prerequisite in ("actions/setup-node@", "actions/setup-python@", "ruby/setup-ruby@",
                             "npm install --global yarn@1.22.22",
                             'python -m pip install "ddtrace>=4.11.0" pytest pytest-cov pytest-xdist'):
            self.assertIn(prerequisite, unit)
            self.assertLess(unit.index(prerequisite), unit.index("ci_test_plan.py run"))
        check = unit.split("- name: Test affected Go packages and their dependents", 1)[1]
        self.assertIn("DDTEST_REQUIRE_PROJECT_RUNTIMES: '1'", check)

    def test_required_test_gate_rejects_failed_cancelled_and_unexpectedly_skipped_checks(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text().split("  test:\n", 1)[1]
        guard = re.search(r"python3 - <<'PY'\n(.*?)\n          PY", workflow, re.S).group(1)
        code = "\n".join(line[10:] for line in guard.splitlines())
        outputs = {"go": "true", "ddto": "false", "evaluator": "false", "integrations": "[]", "playwright": "false"}
        results = {name: {"result": "success"} for name in ("plan", "scripts", "unit", "instrument", "lint")}
        results.update({name: {"result": "skipped"} for name in ("integration", "playwright", "instrumentation-unit", "evaluator-isolation")})
        results["plan"]["outputs"] = outputs
        def run(value):
            return subprocess.run(["python3", "-c", code], capture_output=True,
                                  env={**os.environ, "RESULTS": json.dumps(value)}).returncode
        self.assertEqual(run(results), 0)
        for name in ("plan", "scripts", "unit", "instrument", "lint"):
            for status in ("failure", "cancelled", "skipped"):
                broken = copy.deepcopy(results)
                broken[name]["result"] = status
                self.assertNotEqual(run(broken), 0, (name, status))
        selected = copy.deepcopy(results)
        selected["plan"]["outputs"]["integrations"] = '[{"id":"python"}]'
        self.assertNotEqual(run(selected), 0)
        selected["integration"]["result"] = "success"
        self.assertEqual(run(selected), 0)


if __name__ == "__main__":
    unittest.main()
