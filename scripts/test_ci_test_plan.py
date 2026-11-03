"""Exercise selective CI decisions without using a live GitHub account."""

import hashlib
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


def source(content, kind="file"):
    return [kind, hashlib.sha256(content.encode()).hexdigest()]


class SelectionTests(unittest.TestCase):
    def setUp(self):
        self.inputs = {"internal/ext/command.go": source("shared"),
                       "internal/testdrive/python_validation.go": source("python"),
                       "internal/testdrive/java_validation.go": source("java")}
        self.baseline = planner.make_plan(self.inputs, CONFIG, "DataDog/ddtest", "old")

    def plan(self, inputs, baseline=None):
        return planner.make_plan(inputs, CONFIG, "DataDog/ddtest", "new",
                                 self.baseline if baseline is None else baseline)

    def running(self, plan):
        return {name for name, suite in plan["suites"].items() if suite["decision"] == "run"}

    def test_same_sources_are_candidates_but_never_certify_external_inputs(self):
        plan = self.plan(dict(reversed(list(self.inputs.items()))))
        self.assertFalse(self.running(plan))
        self.assertEqual(plan["mode"], "shadow")
        self.assertFalse(plan["external_inputs_verified"])
        for name in CONFIG["suites"]:
            self.assertEqual(plan["suites"][name]["fingerprint"],
                             self.baseline["suites"][name]["fingerprint"])
        self.assertIn("All tests still run", planner.summary(plan))
        self.assertIn("runtime identities have not been verified", planner.summary(plan))

    def test_python_edit_keeps_unrelated_suites_as_candidates(self):
        inputs = {**self.inputs, "internal/testdrive/python_validation.go": source("changed")}
        plan = self.plan(inputs)
        self.assertEqual(self.running(plan), {"python"})
        self.assertEqual(plan["suites"]["python"]["changed_inputs"],
                         ["internal/testdrive/python_validation.go"])

    def test_shared_unknown_build_and_workflow_edits_invalidate_every_suite(self):
        for path in ("internal/ext/command.go", "internal/testdrive/new_shared.go",
                     "internal/onboard/ci_shell_state.go", "internal/onboard/new_shared.go",
                     "go.mod", "go.sum", "main.go", "ddto/cli/cli.go",
                     ".github/workflows/ci.yml", "scripts/ci_test_plan.json",
                     "internal/testdrive/jest_features.go", "internal/testdrive/jest_preflight.go",
                     "internal/testdrive/testdata/new fixture/data.json"):
            with self.subTest(path=path):
                self.assertEqual(self.running(self.plan({**self.inputs, path: source("changed")})),
                                 set(CONFIG["suites"]))

    def test_test_edits_additions_deletions_renames_and_modes_invalidate_the_owner(self):
        old = "internal/testdrive/python_validation.go"
        for inputs in ({key: value for key, value in self.inputs.items() if key != old},
                       {**self.inputs, old: source("python", "executable")},
                       {**self.inputs, "internal/testdrive/python_new_test.go": source("new test")},
                       {**{key: value for key, value in self.inputs.items() if key != old},
                        "internal/testdrive/java_renamed.go": source("python")}):
            with self.subTest(inputs=inputs):
                self.assertIn("python", self.running(self.plan(inputs)))
        renamed = {**{key: value for key, value in self.inputs.items() if key != old},
                   "internal/testdrive/java_renamed.go": source("python")}
        self.assertEqual(self.running(self.plan(renamed)), {"python", "java"})

    def test_embedded_scripts_invalidate_their_consumers(self):
        for path, owner in (("internal/testdrive/scripts/pytest_observer.py", "python"),
                            ("internal/platform/scripts/ruby_env.rb", "ruby"),
                            ("internal/framework/scripts/vitest.mjs", "vitest"),
                            ("internal/onboard/instructions/pytest.md", "python")):
            with self.subTest(path=path):
                self.assertIn(owner, self.running(self.plan({**self.inputs, path: source("changed")})))

    def test_javascript_inputs_propagate_to_all_javascript_suites_and_onboarding(self):
        plan = self.plan({**self.inputs, "internal/platform/scripts/javascript_env.js": source("changed")})
        self.assertEqual(self.running(plan), {"javascript", "jest", "vitest", "mocha", "cypress",
                                             "playwright", "cucumber", "webdriverio", "onboarding"})

    def test_overlapping_and_transitive_inputs_keep_every_consumer(self):
        self.assertEqual(planner.affected_suites("internal/testdrive/python_ruby_parity_test.go", CONFIG),
                         {"python", "ruby"})
        self.assertEqual(planner.affected_suites("internal/onboard/python_venv.go", CONFIG),
                         {"python", "onboarding"})
        self.assertEqual(planner.affected_suites("internal/framework/scripts/vitest.mjs", CONFIG),
                         {"vitest", "onboarding"})

    def test_only_explicit_documentation_paths_are_ignored(self):
        for path in ("docs/onboarding.md", "README.md", "AGENTS.md", "ddto/docs/validation.md"):
            self.assertFalse(planner.affected_suites(path, CONFIG))
        self.assertTrue(planner.affected_suites("internal/onboard/instructions/pytest.md", CONFIG))

    def test_missing_foreign_incompatible_or_malformed_baselines_run_everything(self):
        invalid = [None, [], {}, {**self.baseline, "repository": "someone/else"},
                   {**self.baseline, "schema": 0}, {**self.baseline, "inputs": []},
                   {**self.baseline, "inputs": {"go.mod": "malformed"}}]
        for baseline in invalid:
            with self.subTest(baseline=baseline):
                plan = planner.make_plan(self.inputs, CONFIG, "DataDog/ddtest", "new", baseline)
                self.assertEqual(self.running(plan), set(CONFIG["suites"]))
                self.assertIsNone(plan["baseline"])


class SnapshotTests(unittest.TestCase):
    def test_snapshot_uses_content_and_modes_without_following_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            file = root / "go.mod"
            file.write_text("first")
            (root / "link").symlink_to("missing-outside-source")
            output = subprocess.CompletedProcess([], 0, b"go.mod\0link\0deleted.go\0")
            with patch.object(planner.subprocess, "run", return_value=output):
                first = planner.source_inputs(root, CONFIG)
                os.utime(file, (1, 1))
                self.assertEqual(first, planner.source_inputs(root, CONFIG))
                file.write_text("other")
                self.assertNotEqual(first, planner.source_inputs(root, CONFIG))
                file.chmod(0o755)
                self.assertEqual(planner.source_inputs(root, CONFIG)["go.mod"][0], "executable")
            self.assertNotIn("deleted.go", first)
            self.assertEqual(first["link"], source("missing-outside-source", "symlink"))


class BaselineTests(unittest.TestCase):
    def test_lookup_pins_workflow_branch_event_success_and_artifact_identity(self):
        baseline = planner.make_plan({}, CONFIG, "DataDog/ddtest", "good")
        baseline["run_id"] = "2"

        def run(command, **kwargs):
            if command[1:3] == ["run", "list"]:
                return subprocess.CompletedProcess(command, 0, json.dumps([
                    {"databaseId": 1, "headSha": "old"}, {"databaseId": 2, "headSha": "good"}]))
            if command[3] == "1":
                return subprocess.CompletedProcess(command, 1)
            (Path(command[-1]) / "plan.json").write_text(json.dumps(baseline))
            return subprocess.CompletedProcess(command, 0)

        with patch.object(planner.subprocess, "run", side_effect=run) as invoke:
            self.assertEqual(planner.github_baseline("DataDog/ddtest"), baseline)
        command = invoke.call_args_list[0].args[0]
        for flag, value in (("--workflow", "ci.yml"), ("--branch", "main"),
                            ("--event", "push"), ("--status", "success"), ("--repo", "DataDog/ddtest")):
            self.assertEqual(command[command.index(flag) + 1], value)
        self.assertIn(planner.ARTIFACT, invoke.call_args_list[-1].args[0])

        baseline["head"] = "wrong-revision"
        with patch.object(planner.subprocess, "run", side_effect=run):
            self.assertIsNone(planner.github_baseline("DataDog/ddtest"))

    def test_network_authentication_and_timeout_failures_produce_no_baseline(self):
        for error in (OSError("missing gh"), subprocess.CalledProcessError(1, ["gh"]),
                      subprocess.TimeoutExpired(["gh"], 30)):
            with self.subTest(error=error), patch.object(planner.subprocess, "run", side_effect=error):
                self.assertIsNone(planner.github_baseline("DataDog/ddtest"))


class WorkflowTests(unittest.TestCase):
    def test_every_job_has_one_owner_and_all_suite_jobs_gate_evidence(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text()
        jobs = re.findall(r"^  ([\w-]+):$", workflow.split("jobs:\n", 1)[1], re.M)
        registered = CONFIG["always_run"] + [job for suite in CONFIG["suites"].values() for job in suite["jobs"]]
        self.assertCountEqual(jobs, registered)
        evidence = workflow.split("  integration-evidence:\n", 1)[1]
        dependencies = re.search(r"    needs: \[([^]]+)\]", evidence).group(1)
        self.assertCountEqual(re.split(r",\s*", dependencies),
                              ["integration-plan"] + [job for suite in CONFIG["suites"].values() for job in suite["jobs"]])
        for suite in CONFIG["suites"].values():
            self.assertTrue(set(suite.get("depends_on", [])).issubset(CONFIG["suites"]))

    def test_shadow_decisions_do_not_gate_existing_jobs(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text().split("jobs:\n", 1)[1]
        for block in re.split(r"(?=^  [\w-]+:\n)", workflow, flags=re.M):
            if block.startswith(("  integration-plan:", "  integration-evidence:")):
                continue
            self.assertNotIn("integration-plan", block)
            self.assertNotIn("source_unchanged", block)

    def test_failed_skipped_or_cancelled_jobs_cannot_publish_evidence(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text().split("  integration-evidence:\n", 1)[1]
        guard = re.search(r"python3 - <<'PY'\n(.*?)\n          PY", workflow, re.S).group(1)
        code = "\n".join(line[10:] for line in guard.splitlines())
        for status in ("success", "failure", "skipped", "cancelled"):
            result = subprocess.run(["python3", "-c", code], capture_output=True,
                                    env={**os.environ, "RESULTS": json.dumps({"matrix": {"result": status}})})
            self.assertEqual(result.returncode == 0, status == "success")


if __name__ == "__main__":
    unittest.main()
