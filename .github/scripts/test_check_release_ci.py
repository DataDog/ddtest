import copy
import os
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

import check_release_ci as gate


class ReleaseCITest(unittest.TestCase):
    def setUp(self):
        self.runs = {}
        for workflow in gate.WORKFLOWS:
            self.runs[workflow] = [{"workflow_runs": [{
                "id": 100, "head_sha": "tested-sha", "head_branch": "main",
                "event": "push", "path": f".github/workflows/{workflow}",
                "head_repository": {"full_name": "DataDog/ddtest"},
                "status": "completed", "conclusion": "success", "run_attempt": 1,
                "html_url": "https://github.com/DataDog/ddtest/actions/runs/100",
            }]}]

    def api(self, endpoint):
        self.assertIn("branch=main&event=push&head_sha=tested-sha&per_page=100", endpoint)
        workflow = endpoint.split("/workflows/")[1].split("/")[0]
        return self.runs[workflow]

    def check(self):
        return gate.check("DataDog/ddtest", "tested-sha", self.api)

    def test_both_main_workflows_pass(self):
        self.assertEqual(len(self.check()), 2)

    def test_missing_workflow_blocks(self):
        for workflow in gate.WORKFLOWS:
            with self.subTest(workflow=workflow):
                self.setUp()
                self.runs[workflow] = [{"workflow_runs": []}]
                with self.assertRaisesRegex(ValueError, "no main push run"):
                    self.check()

    def test_non_green_latest_run_blocks_even_with_older_success(self):
        for workflow in gate.WORKFLOWS:
            for status, conclusion in [("queued", None), ("in_progress", None),
                                       ("completed", "failure"), ("completed", "cancelled"),
                                       ("completed", "skipped"), ("completed", "neutral"),
                                       ("completed", "timed_out"), ("waiting", None)]:
                with self.subTest(workflow=workflow, status=status, conclusion=conclusion):
                    self.setUp()
                    latest = self.runs[workflow][0]["workflow_runs"][0]
                    older = copy.deepcopy(latest)
                    older["id"] = 99
                    self.runs[workflow].append({"workflow_runs": [older]})
                    latest.update(status=status, conclusion=conclusion, run_attempt=2)
                    with self.assertRaisesRegex(ValueError, "wait for main CI"):
                        self.check()

    def test_successful_rerun_supersedes_older_failure(self):
        for workflow in gate.WORKFLOWS:
            latest = self.runs[workflow][0]["workflow_runs"][0]
            latest["run_attempt"] = 2
            older = dict(latest, id=99, conclusion="failure")
            self.runs[workflow][0]["workflow_runs"].insert(0, older)
        self.assertTrue(all("attempt 2" in line for line in self.check()))

    def test_wrong_commit_branch_event_workflow_or_repository_blocks(self):
        for field, value in [("head_sha", "another-sha"), ("head_branch", "feature"),
                             ("event", "pull_request"), ("event", "schedule"),
                             ("event", "workflow_dispatch"), ("path", ".github/workflows/other.yml"),
                             ("head_repository", {"full_name": "someone/ddtest"})]:
            with self.subTest(field=field, value=value):
                self.setUp()
                self.runs["ci.yml"][0]["workflow_runs"][0][field] = value
                with self.assertRaisesRegex(ValueError, "does not identify a main push"):
                    self.check()

    def test_api_failure_propagates(self):
        api = Mock(side_effect=subprocess.CalledProcessError(1, "gh api"))
        with self.assertRaises(subprocess.CalledProcessError):
            gate.check("DataDog/ddtest", "tested-sha", api)

    def test_invalid_dispatch_context_blocks_before_api_access(self):
        env = {"GITHUB_REPOSITORY": "DataDog/ddtest", "GITHUB_REF": "refs/heads/main",
               "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_SHA": "tested-sha"}
        for key, value in [("GITHUB_REPOSITORY", "someone/ddtest"),
                           ("GITHUB_REF", "refs/heads/feature"), ("GITHUB_EVENT_NAME", "push")]:
            with self.subTest(key=key), patch.dict(os.environ, dict(env, **{key: value})), \
                    patch.object(gate, "github_api") as api:
                with self.assertRaisesRegex(ValueError, "must be dispatched from main"):
                    gate.main()
                api.assert_not_called()

    def test_checkout_and_dispatch_must_match(self):
        env = {"GITHUB_REPOSITORY": "DataDog/ddtest", "GITHUB_REF": "refs/heads/main",
               "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_SHA": "tested-sha"}
        with patch.dict(os.environ, env), patch.object(gate, "github_api", side_effect=self.api), \
                patch.object(gate.subprocess, "check_output", return_value="another-sha\n"):
            with self.assertRaisesRegex(ValueError, "does not match dispatched commit"):
                gate.main()

    def test_main_writes_run_evidence(self):
        with tempfile.NamedTemporaryFile() as summary:
            env = {"GITHUB_REPOSITORY": "DataDog/ddtest", "GITHUB_REF": "refs/heads/main",
                   "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_SHA": "tested-sha",
                   "GITHUB_STEP_SUMMARY": summary.name}
            with patch.dict(os.environ, env), patch.object(gate, "github_api", side_effect=self.api), \
                    patch.object(gate.subprocess, "check_output", return_value="tested-sha\n"):
                gate.main()
            self.assertIn(b"Main CI verified for tested-sha", summary.read())


if __name__ == "__main__":
    unittest.main()
