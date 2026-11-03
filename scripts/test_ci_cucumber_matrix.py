"""Keep every distinct configuration while eliminating resolved aliases."""

import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import ci_cucumber_matrix as planner


class MatrixTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.output = Path(directory.name) / "fixtures"
        self.latest = "13.3.0"
        self.commands = []

    def execute(self, command, cwd=None):
        self.commands.append(command)
        if command == ["node", "--version"]:
            return "v24.14.1"
        if command == ["npm", "--version"]:
            return "11.11.0"
        if command[:2] == ["npm", "view"]:
            return json.dumps(self.latest)
        self.assertEqual(command, ["npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"])
        manifest = json.loads((cwd / "package.json").read_text())
        lock = {"lockfileVersion": 3, "packages": {
            f"node_modules/{name}": {"version": version}
            for name, version in manifest["dependencies"].items()}}
        lock["packages"]["node_modules/transitive"] = {"version": "1.2.3", "integrity": "sha512-fixture"}
        (cwd / "package-lock.json").write_text(json.dumps(lock))
        return ""

    def build(self):
        with patch.object(planner, "execute", side_effect=self.execute):
            return planner.build_plan(self.output)

    def test_equal_latest_and_pin_share_locks_and_jobs_but_keep_every_shard(self):
        plan = self.build()
        rows = plan["matrix"]["include"]
        self.assertEqual(plan["requested_jobs"], 13)
        self.assertEqual(len(rows), 9)
        self.assertEqual(sum(len(row["aliases"].split(", ")) for row in rows), 13)
        shared = [row for row in rows if row["version"] == "13.3.0"]
        self.assertEqual({row["shard"] for row in shared}, {"1/4", "2/4", "3/4", "4/4"})
        self.assertTrue(all(row["aliases"] == "13.3.0, latest" for row in shared))
        self.assertEqual({path.name for path in self.output.iterdir()}, {"8.11.1", "10.9.0", "13.3.0"})
        self.assertEqual(sum(command[:2] == ["npm", "view"] for command in self.commands), 1)
        for row in rows:
            lock = json.loads((self.output / row["fixture"] / "package-lock.json").read_text())
            self.assertEqual(row["lock"], planner.fingerprint(lock))
            self.assertEqual((row["node"], row["npm"]), ("24.14.1", "11.11.0"))

    def test_a_new_latest_release_retains_all_thirteen_jobs(self):
        self.latest = "13.4.0"
        rows = self.build()["matrix"]["include"]
        self.assertEqual(len(rows), 13)
        self.assertEqual(len([row for row in rows if row["version"] == "13.4.0"]), 4)
        self.assertTrue(all(row["aliases"] == row["cucumber"] for row in rows))

    def test_version_equality_never_collapses_floor_and_full_scenarios(self):
        self.latest = "8.11.1"
        rows = self.build()["matrix"]["include"]
        self.assertEqual(len(rows), 13)
        floor = [row for row in rows if row["cucumber"] == "8.11.1"]
        latest = [row for row in rows if row["cucumber"] == "latest"]
        self.assertEqual(floor[0]["tests"], "^TestPublicCucumberTestdrive/passing$")
        self.assertTrue(all(row["tests"] == "^TestPublicCucumberTestdrive$" for row in latest))

    def test_runtime_graph_filter_and_shard_differences_cannot_be_deduplicated(self):
        row = self.build()["matrix"]["include"][5]
        row = {key: value for key, value in row.items() if key != "aliases"}
        alias = {**row, "cucumber": "latest"}
        self.assertEqual(len(planner.deduplicate([row, alias])), 1)
        for key in ("node", "npm", "lock", "tests", "shard"):
            with self.subTest(key=key):
                self.assertEqual(len(planner.deduplicate([row, {**alias, key: "different"}])), 2)
        lock = json.loads((self.output / row["fixture"] / "package-lock.json").read_text())
        lock["packages"]["node_modules/transitive"]["version"] = "1.2.4"
        self.assertNotEqual(planner.fingerprint(lock), row["lock"])

    def test_ambiguous_or_invalid_registry_versions_fail(self):
        for version in (["13.3.0", "13.4.0"], None, "latest", "../unsafe"):
            self.latest = version
            with self.subTest(version=version), patch.object(planner, "execute", side_effect=self.execute):
                with self.assertRaises(ValueError):
                    planner.resolve_version("latest")

    def test_lock_resolution_failure_never_publishes_a_partial_matrix(self):
        github_output = self.output.parent / "github-output"
        command = ["planner", "--output", str(self.output), "--github-output", str(github_output)]
        with patch.object(sys, "argv", command), patch.object(planner, "execute", side_effect=[
                "v24.14.1", "11.11.0", subprocess.CalledProcessError(1, ["npm"], stderr="registry unavailable")]):
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as error:
                planner.main()
        self.assertEqual(error.exception.code, 1)
        self.assertFalse(github_output.exists())
        self.assertFalse((self.output / "matrix.json").exists())

    def test_success_publishes_the_same_complete_matrix_to_github_and_the_artifact(self):
        github_output = self.output.parent / "github-output"
        github_output.write_text("existing=value\n")
        summary = self.output.parent / "summary.md"
        command = ["planner", "--output", str(self.output), "--github-output", str(github_output),
                   "--summary", str(summary)]
        with patch.object(sys, "argv", command), patch.object(planner, "execute", side_effect=self.execute):
            with contextlib.redirect_stdout(io.StringIO()):
                planner.main()
        lines = github_output.read_text().splitlines()
        self.assertEqual(lines[0], "existing=value")
        self.assertEqual(len(lines), 2)
        key, value = lines[1].split("=", 1)
        self.assertEqual(key, "matrix")
        artifact = json.loads((self.output / "matrix.json").read_text())
        self.assertEqual(json.loads(value), artifact["matrix"])
        self.assertEqual(len(artifact["matrix"]["include"]), 9)
        self.assertIn("13 requested lanes → 9 distinct jobs", summary.read_text())

    def test_an_incomplete_or_wrong_lockfile_fails(self):
        self.output.mkdir()
        with patch.object(planner, "execute") as execute:
            execute.side_effect = lambda command, cwd: (cwd / "package-lock.json").write_text('{"packages":{}}')
            with self.assertRaisesRegex(ValueError, "lockfile did not retain"):
                planner.lock_fixture(self.output, "13.3.0")

    def test_existing_output_is_not_reused(self):
        self.build()
        with self.assertRaises(FileExistsError):
            self.build()


class WorkflowTests(unittest.TestCase):
    def test_workers_use_the_planned_runtime_and_lockfile_without_resolving_again(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/ci-full.yml").read_text()
        worker = workflow.split("  testdrive-cucumber-workflow:\n", 1)[1].split("\n  testdrive-webdriverio-workflow:", 1)[0]
        self.assertIn("configuration: ${{ fromJSON(needs.cucumber-matrix.outputs.matrix).include }}", worker)
        self.assertIn("node-version: ${{ matrix.configuration.node }}", worker)
        self.assertIn('test "$(npm --version)" =', worker)
        self.assertIn("name: ci-cucumber-fixtures", worker)
        self.assertIn('npm ci --prefix "$RUNNER_TEMP/cucumber-testdrive"', worker)
        self.assertNotIn("npm install", worker)
        self.assertNotIn("npm view", worker)
        self.assertIn("-run '${{ matrix.configuration.tests }}'", worker)


if __name__ == "__main__":
    unittest.main()
