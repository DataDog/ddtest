"""Offline checks for Docker control failures, teardown classification and cleanup."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import isolation
import test_run_eval as fixtures


def completed(arguments, code=0, stdout="", stderr=""):
    return subprocess.CompletedProcess(["docker", *arguments], code, stdout, stderr)


class ControlTests(unittest.TestCase):
    def test_docker_timeout_is_a_runtime_error(self):
        with patch.object(isolation.subprocess, "run", side_effect=subprocess.TimeoutExpired(["docker", "create"], 20)):
            with self.assertRaisesRegex(RuntimeError, "timed out after 20s: docker create"):
                isolation.docker_control("create")

    def test_invisible_bind_mount_names_the_folder_and_the_fix(self):
        container = isolation.Container(Path("/private/tmp/dde-trial"), "sha256:" + "0" * 64)
        failure = RuntimeError("Docker control failed: invalid mount config for type \"bind\": bind source path does not exist: /private/tmp/dde-trial")
        with patch.object(isolation, "docker_control", side_effect=failure):
            with self.assertRaises(RuntimeError) as raised:
                container.start()
        message = str(raised.exception)
        self.assertIn("Docker cannot see the trial folder /private/tmp/dde-trial", message)
        self.assertIn("colima.yaml", message)
        self.assertIn(isolation.TRIAL_ROOT_VARIABLE, message)

    def test_missing_containers_are_recognized_in_any_case(self):
        for message in ("Error: No such object: x", "error: no such object: x", "Error response from daemon: No such container: x"):
            self.assertTrue(isolation.missing(completed(["inspect"], 1, stderr=message)))
        self.assertFalse(isolation.missing(completed(["inspect"], 1, stderr="permission denied")))

    def test_container_removal_waits_for_an_rm_already_in_progress(self):
        calls = []
        def control(*arguments, check=True):
            calls.append(arguments[0])
            if arguments[0] == "rm":
                return completed(arguments, 1, stderr="removal of container x is already in progress")
            return completed(arguments, 1, stderr="Error: No such object: x") if calls.count("inspect") > 1 else completed(arguments, stdout="id")
        with patch.object(isolation, "docker_control", side_effect=control), patch.object(isolation.time, "sleep"):
            isolation.remove_container("x")
        self.assertEqual(calls, ["rm", "inspect", "inspect"])


class TeardownTests(unittest.TestCase):
    def setUp(self):
        self.case = fixtures.RunnerTests()
        self.case.setUp()
        self.addCleanup(self.case.doCleanups)

    def test_unconfirmed_container_removal_outranks_a_retryable_agent_failure(self):
        def close(_self):
            raise RuntimeError("Container cleanup failed: daemon unreachable")
        with patch.object(fixtures.FakeContainer, "close", close):
            code, manifest = self.case.trial({"FAKE_PLAIN_ERROR": "API Error: Response stalled mid-stream"})
        self.assertEqual(code, 1)
        self.assertEqual(manifest["isolation"]["cleanup"], "failed")
        self.assertEqual(manifest["failure_kind"], "evaluator_environment")
        self.assertEqual(manifest["failure_stage"], "container_cleanup")
        self.assertFalse(manifest["retry_recommended"])
        self.assertEqual(manifest["status"], "failed")

    def test_docker_control_timeout_is_recorded_instead_of_escaping(self):
        def start(_self):
            with patch.object(isolation.subprocess, "run", side_effect=subprocess.TimeoutExpired(["docker", "create"], 20)):
                isolation.docker_control("create")
        with patch.object(fixtures.FakeContainer, "start", start):
            code, manifest = self.case.trial()
        self.assertEqual(code, 1)
        self.assertEqual(manifest["status"], "failed")
        self.assertIn("timed out after 20s", manifest["error"])
        self.assertNotIn("commit", manifest)


class CleanupWithoutRunManifestTests(unittest.TestCase):
    """A SIGKILLed runner leaves ownership.json and the marker, but no run.json."""

    def setUp(self):
        self.storage = tempfile.TemporaryDirectory()
        self.addCleanup(self.storage.cleanup)
        root = Path(self.storage.name).resolve()
        self.trial = Path(tempfile.mkdtemp(prefix="dde-", dir=root)).resolve()
        self.output = root / "evidence"
        self.output.mkdir()
        self.name = "ddtest-eval-" + "a" * 32
        isolation.mark_trial(self.trial, self.output, self.name)
        environment = patch.dict(os.environ, {isolation.TRIAL_ROOT_VARIABLE: str(root)})
        environment.start()
        self.addCleanup(environment.stop)

    def control(self, mounts, calls):
        removed = []
        def control(*arguments, check=True):
            calls.append(arguments[0])
            if arguments[0] == "rm":
                removed.append(True)
                return completed(arguments)
            if removed or mounts is None:
                return completed(arguments, 1, stderr="Error: No such object: " + self.name)
            return completed(arguments, stdout="\n".join(mounts) + "\n")
        return control

    def test_cleanup_works_from_the_ownership_record_when_the_container_is_gone(self):
        calls = []
        with patch.object(isolation, "docker_control", side_effect=self.control(None, calls)):
            self.assertEqual(isolation.cleanup(self.output), self.trial)
        self.assertFalse(self.trial.exists())
        self.assertEqual(calls, ["inspect"])

    def test_cleanup_removes_a_leftover_container_that_mounts_the_trial(self):
        calls = []
        with patch.object(isolation, "docker_control", side_effect=self.control([str(self.trial)], calls)):
            self.assertEqual(isolation.cleanup(self.output), self.trial)
        self.assertFalse(self.trial.exists())
        self.assertEqual(calls, ["inspect", "rm", "inspect"])

    def test_cleanup_refuses_a_container_that_does_not_mount_the_trial(self):
        calls = []
        with patch.object(isolation, "docker_control", side_effect=self.control(["/somewhere/else"], calls)):
            with self.assertRaisesRegex(ValueError, "does not mount the owned trial"):
                isolation.cleanup(self.output)
        self.assertTrue(self.trial.exists())
        self.assertNotIn("rm", calls)

    def test_cleanup_refuses_without_a_recorded_container(self):
        marker = json.loads((self.output / "ownership.json").read_text())
        del marker["container"]
        for path in (self.output / "ownership.json", self.trial / ".ddtest-eval-owner.json"):
            path.write_text(json.dumps(marker))
        with patch.object(isolation, "docker_control") as control:
            with self.assertRaisesRegex(ValueError, "no owned container is recorded"):
                isolation.cleanup(self.output)
        control.assert_not_called()
        self.assertTrue(self.trial.exists())


class TrialRootTests(unittest.TestCase):
    def test_override_must_be_an_existing_absolute_directory(self):
        with tempfile.TemporaryDirectory() as root:
            with patch.dict(os.environ, {isolation.TRIAL_ROOT_VARIABLE: root}):
                self.assertEqual(isolation.trial_root(), Path(root).resolve())
        for value in ("relative/root", "/nonexistent/ddtest-eval-root"):
            with patch.dict(os.environ, {isolation.TRIAL_ROOT_VARIABLE: value}):
                with self.assertRaises(ValueError):
                    isolation.trial_root()

    def test_macos_default_is_a_private_folder_in_the_shared_home(self):
        with tempfile.TemporaryDirectory() as home:
            environment = {key: value for key, value in os.environ.items() if key != isolation.TRIAL_ROOT_VARIABLE}
            with patch.dict(os.environ, environment, clear=True), patch.object(isolation.sys, "platform", "darwin"), \
                    patch.object(isolation.Path, "home", return_value=Path(home)):
                root = isolation.trial_root()
            self.assertEqual(root, (Path(home) / ".dde").resolve())
            self.assertEqual(root.stat().st_mode & 0o777, 0o700)

    def test_long_roots_are_refused_before_watchman_cannot_bind(self):
        isolation.check_socket_budget("/tmp", "user")
        with self.assertRaisesRegex(ValueError, "too long for Watchman"):
            isolation.check_socket_budget("/" + "a" * 70, "user")


if __name__ == "__main__":
    unittest.main()
