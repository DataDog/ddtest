"""Offline runner checks using real Git clones and fake coding-agent CLIs."""

import contextlib
import io
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import run_eval


FAKE_AGENT = '''#!/usr/bin/env python3
import json, os, pathlib, sys
if "--version" in sys.argv:
    print("fake-agent 1.0")
    sys.exit(0)
prompt = sys.stdin.read()
pathlib.Path("observed.json").write_text(json.dumps({
    "prompt": prompt, "argv": sys.argv[1:],
    "dd_env": [key for key in os.environ if key.startswith(("DD_", "DATADOG_"))],
}))
with pathlib.Path("README.md").open("a") as stream:
    stream.write("\\nFake agent changed this tracked file.\\n")
if pathlib.Path(sys.argv[0]).name == "codex":
    events = [
        {"type": "item.completed", "item": {"type": "reasoning", "text": "PRIVATE_REASONING"}},
        {"type": "item.completed", "item": {"type": "command_execution", "command": "ddtest onboard", "exit_code": 0, "aggregated_output": "instructions"}},
        {"type": "item.completed", "item": {"type": "agent_message", "text": "Done"}},
        {"type": "turn.completed"},
    ]
else:
    events = [
        {"type": "assistant", "message": {"content": [
            {"type": "thinking", "thinking": "PRIVATE_REASONING"},
            {"type": "tool_use", "name": "Bash", "id": "cmd1", "input": {"command": "ddtest onboard"}},
        ]}},
        {"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "cmd1", "content": "instructions"}]}},
        {"type": "result", "is_error": bool(os.environ.get("FAKE_AGENT_ERROR")), "result": "Done"},
    ]
for event in events:
    print(json.dumps(event))
'''


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.binary = self.root / "ddtest"
        self.binary.write_text("#!/bin/sh\nexit 0\n")
        self.binary.chmod(0o755)
        for agent in ("codex", "claude"):
            executable = self.root / agent
            executable.write_text(FAKE_AGENT)
            executable.chmod(0o755)
        # Reuse an existing commit: fixtures do not create unsigned Git commits.
        self.source = subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"], cwd=Path(__file__).parent, text=True).strip()
        self.args = type("Args", (), dict(
            repo=self.source, ref="HEAD", ddtest=self.binary, agent="codex",
            setup_command=[], output=self.root / "evidence", model=None, timeout=30))()

    def trial(self, extra_env=None):
        env = {"PATH": str(self.root) + os.pathsep + os.environ["PATH"],
               "DD_API_KEY": "fake-key", "DATADOG_API_KEY": "fake-key", **(extra_env or {})}
        with patch.dict(os.environ, env), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            code = run_eval.run(self.args)
        manifest = json.loads((self.args.output / "run.json").read_text())
        self.addCleanup(shutil.rmtree, manifest["trial_directory"])
        return code, manifest

    def test_fresh_trials_minimal_prompt_and_independent_evaluation(self):
        checkouts = []
        for agent in ("codex", "claude"):
            with self.subTest(agent=agent):
                self.args.agent = agent
                self.args.output = self.root / f"{agent}-evidence"
                code, manifest = self.trial()
                checkout = Path(manifest["checkout"])
                checkouts.append(checkout)
                observed = json.loads((checkout / "observed.json").read_text())
                self.assertEqual(code, 0)
                self.assertEqual(manifest["status"], "completed")
                self.assertEqual(manifest["evaluation"], "pending")
                self.assertEqual(manifest["ci_execution"], "not exercised")
                self.assertEqual(observed["prompt"], manifest["prompt"])
                self.assertEqual(observed["dd_env"], [])
                self.assertNotIn("--model", observed["argv"])
                copied = checkout.parent / "bin" / "ddtest"
                self.assertEqual(copied.read_bytes(), self.binary.read_bytes())
                self.assertFalse(copied.is_symlink())
                self.assertEqual(list(copied.parent.iterdir()), [copied])
                activity = (self.args.output / "activity.jsonl").read_text()
                self.assertNotIn("PRIVATE_REASONING", activity)
                self.assertIn("ddtest onboard", activity)
                self.assertIn("Fake agent changed", (self.args.output / "changes.patch").read_text())
                self.assertIn("observed.json", (self.args.output / "status.txt").read_text())
        self.assertNotEqual(*checkouts)

    def test_setup_failure_is_recorded_without_launching_agent(self):
        self.args.setup_command = [f"{shlex.quote(sys.executable)} -c 'import sys; sys.exit(7)'"]
        code, manifest = self.trial()
        self.assertEqual(code, 1)
        self.assertEqual(manifest["status"], "failed")
        self.assertEqual(manifest["commands"][-1]["exit_code"], 7)
        self.assertFalse((Path(manifest["checkout"]) / "observed.json").exists())

    def test_dirty_setup_does_not_contaminate_trial(self):
        self.args.setup_command = [f"{shlex.quote(sys.executable)} -c 'open(\"setup-file\", \"w\").close()'"]
        code, manifest = self.trial()
        self.assertEqual(code, 1)
        self.assertIn("setup changed", manifest["error"])
        self.assertFalse((Path(manifest["checkout"]) / "observed.json").exists())

    def test_error_event_is_failure_even_with_zero_cli_exit(self):
        self.args.agent = "claude"
        code, manifest = self.trial({"FAKE_AGENT_ERROR": "1"})
        self.assertEqual(code, 1)
        self.assertEqual(manifest["status"], "agent_failed")
        self.assertEqual(manifest["commands"][-1]["exit_code"], 0)

    def test_existing_evidence_is_never_overwritten(self):
        self.args.output.mkdir()
        sentinel = self.args.output / "keep"
        sentinel.write_text("original")
        with self.assertRaises(FileExistsError):
            run_eval.run(self.args)
        self.assertEqual(sentinel.read_text(), "original")

    def test_timeout_stops_descendants(self):
        sentinel = self.root / "should-not-exist"
        child = f"import time; time.sleep(1); open({str(sentinel)!r}, 'w').close()"
        parent = f"import subprocess, sys, time; subprocess.Popen([sys.executable, '-c', {child!r}]); print('started', flush=True); time.sleep(30)"
        with self.assertRaises(TimeoutError):
            run_eval.execute([sys.executable, "-c", parent], self.root, os.environ, 0.3, lambda _: None)
        time.sleep(1)
        self.assertFalse(sentinel.exists())

    def test_long_command_output_is_bounded_without_losing_command(self):
        event = {"type": "item.completed", "item": {"type": "command_execution",
                 "command": "npm test", "aggregated_output": "x" * 100_000, "exit_code": 1}}
        result, = run_eval.public_events(event, "codex")
        self.assertEqual(result["command"], "npm test")
        self.assertEqual(result["exit_code"], 1)
        self.assertLess(len(result["output"]), 4100)
        self.assertTrue(result["output"].startswith("[truncated]"))


if __name__ == "__main__":
    unittest.main()
