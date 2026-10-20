"""Offline runner checks using real Git clones and fake coding-agent CLIs."""

import contextlib
import io
import json
import os
import getpass
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import run_eval


FAKE_AGENT = '''#!/usr/bin/env python3
import json, os, pathlib, subprocess, sys
if "--version" in sys.argv:
    print("fake-agent 1.0")
    sys.exit(0)
prompt = sys.stdin.read()
pathlib.Path("observed.json").write_text(json.dumps({
    "prompt": prompt, "argv": sys.argv[1:],
    "agent_environment": {key: os.environ.get(key) for key in ("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "BASH_DEFAULT_TIMEOUT_MS", "BASH_MAX_TIMEOUT_MS")},
    "dd_env": [key for key in os.environ if key.startswith(("DD_", "DATADOG_"))],
    "cache_environment": {key: os.environ.get(key) for key in ("npm_config_cache", "YARN_CACHE_FOLDER", "XDG_CONFIG_HOME", "PLAYWRIGHT_BROWSERS_PATH", "PUPPETEER_CACHE_DIR")},
    "npm_unknown_config": [key for key in os.environ if key.lower() in ("npm_config_store_dir", "npm_config_cache_dir")],
    "zdotdir": os.environ.get("ZDOTDIR"),
}))
if os.environ.get("FAKE_RUNTIME_SHELL"):
    settings = {}
    for index, arg in enumerate(sys.argv[:-1]):
        if arg == "-c":
            key, _, value = sys.argv[index + 1].partition("=")
            settings[key] = value
    env = os.environ.copy()
    for key, value in settings.items():
        if key.startswith("shell_environment_policy.set."):
            env[key.removeprefix("shell_environment_policy.set.")] = json.loads(value)
    login = settings.get("allow_login_shell") != "false"
    result = subprocess.check_output([os.environ["FAKE_RUNTIME_SHELL"], "-lc" if login else "-c", "node --version"], env=env, text=True).strip()
    pathlib.Path("agent-runtime.json").write_text(json.dumps({"version":result, "login":login}))
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
if os.environ.get("FAKE_BACKGROUND"):
    events.insert(-1, {"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "Bash", "id": "long", "input": {"command": "ddtest testdrive --yes"}}]}})
    events.insert(-1, {"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "long", "content": "Command did not complete within its 120s timeout and was moved to the background (ID: bg123). You will be notified when it completes."}]}})
if os.environ.get("FAKE_LONG_VERDICT"):
    pathlib.Path(".testoptimization").mkdir()
    pathlib.Path(".testoptimization/testdrive.json").write_text(json.dumps({"summary": {"status": "INCOMPLETE"}}))
    response = "Onboarding is complete. " + "Details. " * 1000 + "INCOMPLETE"
    for event in events:
        if event.get("item", {}).get("type") == "agent_message":
            event["item"]["text"] = response
        elif event["type"] == "result":
            event["result"] = response
if os.environ.get("FAKE_REPORT_JSON"):
    pathlib.Path(".testoptimization").mkdir(exist_ok=True)
    pathlib.Path(".testoptimization/testdrive.json").write_text(os.environ["FAKE_REPORT_JSON"])
    for event in events:
        if event.get("item", {}).get("type") == "agent_message":
            event["item"]["text"] = os.environ.get("FAKE_RESPONSE", "Done")
        elif event["type"] == "result":
            event["result"] = os.environ.get("FAKE_RESPONSE", "Done")
if os.environ.get("FAKE_INITIAL_RUNTIME"):
    events.insert(-1, {"type":"item.completed", "item":{"type":"command_execution", "command":"ddtest testdrive --check-only", "exit_code":0,
                   "aggregated_output":"Local runtime: Node " + os.environ["FAKE_INITIAL_RUNTIME"] + "."}})
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

    def selected_node(self, version="24.18.0"):
        selected = self.root / "selected" / "node"
        selected.parent.mkdir()
        selected.write_text(f"#!{sys.executable}\nimport json, pathlib, sys\n"
                            f"print(json.dumps({{'executable':str(pathlib.Path(__file__).resolve()), 'version':'v{version}'}}) if '-p' in sys.argv else 'v{version}')\n")
        selected.chmod(0o755)
        return selected

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
                self.assertEqual(observed["npm_unknown_config"], [])
                for directory in observed["cache_environment"].values():
                    self.assertTrue(Path(directory).is_relative_to(checkout.parent / "caches"))
                    self.assertTrue(Path(directory).is_dir())
                private = manifest["private_caches"]
                pnpm_rc = Path(private["XDG_CONFIG_HOME"]) / "pnpm" / "rc"
                self.assertIn("store-dir=" + private["pnpm_store"], pnpm_rc.read_text())
                self.assertIn("cache-dir=" + private["pnpm_cache"], pnpm_rc.read_text())
                if agent == "claude":
                    self.assertEqual(observed["agent_environment"], manifest["agent_environment"])
                    self.assertEqual(observed["agent_environment"]["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"], "1")
                    self.assertEqual(observed["agent_environment"]["BASH_MAX_TIMEOUT_MS"], "30000")
                self.assertNotIn("--model", observed["argv"])
                copied = checkout.parent / "bin" / "ddtest"
                self.assertEqual(copied.read_bytes(), self.binary.read_bytes())
                self.assertFalse(copied.is_symlink())
                self.assertEqual(list(copied.parent.iterdir()), [copied])
                activity = (self.args.output / "activity.jsonl").read_text()
                self.assertNotIn("PRIVATE_REASONING", activity)
                self.assertEqual((self.args.output / "final-response.md").read_text(), "Done\n")
                self.assertIn("ddtest onboard", activity)
                self.assertIn("Fake agent changed", (self.args.output / "changes.patch").read_text())
                self.assertIn("observed.json", (self.args.output / "status.txt").read_text())
        self.assertNotEqual(*checkouts)

    def test_selected_node_survives_login_path_reset_without_prompt_changes(self):
        selected = self.selected_node()
        old = self.root / "old" / "node"
        old.parent.mkdir()
        old.write_text("#!/bin/sh\necho v24.14.1\n")
        old.chmod(0o755)
        shell = self.root / "resetting-shell"
        shell.write_text(f"#!/bin/sh\nif [ \"$1\" = '-lc' ]; then\n  export PATH={shlex.quote(str(old.parent))}:/usr/bin:/bin\nfi\nexec /bin/sh -c \"$2\"\n")
        shell.chmod(0o755)
        self.assertEqual(subprocess.check_output([str(shell), "-lc", "node --version"], text=True).strip(), "v24.14.1")
        self.args.node = selected
        self.args.setup_command = ["node --version"]
        code, manifest = self.trial({"FAKE_RUNTIME_SHELL": str(shell)})
        self.assertEqual(code, 0)
        self.assertEqual(manifest["node_runtime"], {
            "requested": str(selected.resolve()), "executable": str(selected.resolve()),
            "version": "v24.18.0", "codex_login_shell": False})
        setup = next(c for c in manifest["commands"] if c["argv"] == ["node", "--version"])
        self.assertEqual(setup["output_excerpt"], "v24.18.0\n")
        checkout = Path(manifest["checkout"])
        self.assertEqual(json.loads((checkout / "agent-runtime.json").read_text()), {"version":"v24.18.0", "login":False})
        observed = json.loads((checkout / "observed.json").read_text())
        self.assertEqual(observed["prompt"], f"Use ddtest in {checkout.parent / 'bin'} and onboard this repository to test-optimization.")
        self.assertNotIn("--model", observed["argv"])

    def test_ruby_trials_pin_runtime_and_keep_dependencies_private_for_both_agents(self):
        selected = self.root / "selected" / "ruby"
        selected.parent.mkdir()
        selected.write_text(f"#!{sys.executable}\nimport json\nprint(json.dumps({{'executable':{str(selected)!r},'version':'3.4.9'}}))\n")
        selected.chmod(0o755)
        self.args.ruby = selected
        for agent in ("codex", "claude"):
            with self.subTest(agent=agent):
                self.args.agent = agent
                self.args.output = self.root / (agent + "-ruby")
                self.args.disable_mcp_server = ["datadog-code-exec"] if agent == "codex" else []
                self.args.strict_mcp = agent == "claude"
                code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps({"preflight":{"ruby":"3.4.9"}})})
                self.assertEqual(code, 0)
                self.assertEqual(manifest["ruby_runtime"]["version"], "3.4.9")
                self.assertEqual(manifest["ddtest_ruby_runtime"]["status"], "matched")
                checkout = Path(manifest["checkout"])
                for key in ("BUNDLE_PATH", "BUNDLE_APP_CONFIG", "BUNDLE_USER_HOME", "GEM_HOME"):
                    self.assertTrue(Path(manifest["private_caches"][key]).is_relative_to(checkout.parent / "caches"))
                observed = json.loads((checkout / "observed.json").read_text())
                if agent == "codex":
                    self.assertIn("mcp_servers.datadog-code-exec.enabled=false", observed["argv"])
                    self.assertIn("allow_login_shell=false", observed["argv"])
                else:
                    self.assertIn("--strict-mcp-config", observed["argv"])
                    self.assertIn('{"mcpServers":{}}', observed["argv"])
                self.assertEqual(observed["prompt"], f"Use ddtest in {checkout.parent / 'bin'} and onboard this repository to test-optimization.")

    def test_ruby_runtime_mismatch_and_missing_evidence_remain_distinct(self):
        self.assertEqual(run_eval.report_ruby_runtime({}, "3.4.9")["status"], "not observed")
        self.assertEqual(run_eval.report_ruby_runtime({"preflight":{"ruby":"3.2.2"}}, "3.4.9")["status"], "mismatch")
        self.assertEqual(run_eval.report_ruby_runtime({"configurations":[{"preflight":{"ruby":"3.4.9"}}]}, "3.4.9")["status"], "matched")

    def test_short_owned_state_path_accepts_real_watchman_socket(self):
        code, manifest = self.trial()
        self.assertEqual(code, 0)
        trial = Path(manifest["trial_directory"])
        self.assertTrue(trial.name.startswith("dde-"))
        if sys.platform == "darwin":
            self.assertEqual(trial.parent, Path("/tmp").resolve())
        endpoint = Path(manifest["private_caches"]["XDG_STATE_HOME"]) / "watchman" / (getpass.getuser() + "-state") / "sock"
        self.assertTrue(endpoint.is_relative_to(trial))
        endpoint.parent.mkdir(parents=True)
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as server:
            server.bind(str(endpoint))
            server.listen()
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
                client.connect(str(endpoint))
                connection, _ = server.accept()
                with connection:
                    client.sendall(b"owned state")
                    self.assertEqual(connection.recv(64), b"owned state")

    def test_selected_node_survives_actual_nonlogin_zshenv_override(self):
        zsh = shutil.which("zsh")
        if not zsh:
            self.skipTest("zsh is required for startup-file isolation regression")
        selected = self.selected_node()
        host = self.root / "host"
        host.mkdir()
        old = self.root / "old" / "node"
        old.parent.mkdir()
        old.write_text("#!/bin/sh\necho v24.14.1\n")
        old.chmod(0o755)
        startup = host / ".zshenv"
        startup.write_text(f"export PATH={shlex.quote(str(old.parent))}:/usr/bin:/bin\nprintf read > {shlex.quote(str(host / 'read-marker'))}\n")
        original = startup.read_bytes()
        hostile = {**os.environ, "HOME": str(host), "ZDOTDIR": str(host)}
        self.assertEqual(subprocess.check_output([zsh, "-c", "node --version"], env=hostile, text=True).strip(), "v24.14.1")
        (host / "read-marker").unlink()
        self.args.node = selected
        code, manifest = self.trial({"FAKE_RUNTIME_SHELL": zsh, "HOME": str(host), "ZDOTDIR": str(host)})
        self.assertEqual(code, 0)
        checkout = Path(manifest["checkout"])
        self.assertEqual(json.loads((checkout / "agent-runtime.json").read_text()), {"version": "v24.18.0", "login": False})
        isolation = manifest["shell_isolation"]
        self.assertTrue(Path(isolation["zdotdir"]).is_relative_to(checkout.parent))
        self.assertEqual(list(Path(isolation["zdotdir"]).iterdir()), [])
        self.assertEqual(isolation, {"zdotdir": str(checkout.parent / "zsh"), "login_shell": False, "shell_snapshot": False})
        observed = json.loads((checkout / "observed.json").read_text())
        self.assertEqual(observed["zdotdir"], isolation["zdotdir"])
        self.assertIn("features.shell_snapshot=false", observed["argv"])
        self.assertIn("shell_environment_policy.set.ZDOTDIR=" + json.dumps(isolation["zdotdir"]), observed["argv"])
        self.assertFalse((host / "read-marker").exists())
        self.assertEqual(startup.read_bytes(), original)

    def test_final_ddtest_runtime_mismatch_is_an_evaluator_environment_failure(self):
        self.args.node = self.selected_node()
        report = {"summary": {"status": "INCOMPLETE", "local_node": "v24.14.1"},
                  "preflight": {"node": "v24.14.1"},
                  "runs": [{"framework_node": "22.0.0"}]}
        code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps(report), "FAKE_RESPONSE": "Onboarding validation: INCOMPLETE."})
        self.assertEqual(code, 1)
        self.assertEqual(manifest["node_runtime"]["version"], "v24.18.0", "setup success cannot certify the agent's runtime")
        self.assertEqual(manifest["failure_kind"], "evaluator_environment")
        self.assertEqual(manifest["failure_stage"], "ddtest_runtime")
        self.assertFalse(manifest["retry_recommended"])
        self.assertEqual(manifest["ddtest_runtime"]["status"], "mismatch")
        self.assertEqual(manifest["ddtest_runtime"]["observed"][0]["version"], "24.14.1")
        self.assertEqual(manifest["ddtest_runtime"]["framework_owned_nodes"], ["22.0.0"])

    def test_corrected_runtime_keeps_earlier_mismatch_without_failing_final_match(self):
        self.args.node = self.selected_node()
        report = {"summary": {"status": "INCOMPLETE", "local_node": "v24.18.0"},
                  "preflight": {"node": "24.18.0"},
                  "configurations": [{"preflight": {"node": "v24.18.0"}}],
                  "runs": [{"framework_node": "22.0.0"}],
                  "retained_execution": {"result": {"preflight": {"node": "v24.14.1"}}}}
        code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps(report), "FAKE_RESPONSE": "Onboarding validation: INCOMPLETE.",
                                     "FAKE_INITIAL_RUNTIME": "v24.14.1"})
        self.assertEqual(code, 0)
        self.assertEqual(manifest["ddtest_runtime"]["status"], "matched")
        self.assertEqual(manifest["ddtest_runtime"]["framework_owned_nodes"], ["22.0.0"])
        self.assertEqual(manifest["runtime_observations"], [{"source": "public ddtest command output", "version": "v24.14.1"}])
        self.assertEqual(manifest["ddtest_runtime"]["observed"][0]["source_count"], 3)

    def test_private_cache_configuration_does_not_modify_host_settings(self):
        host = self.root / "host"
        host.mkdir()
        config = host / ".npmrc"
        config.write_text("registry=https://registry.npmjs.org/\n")
        code, manifest = self.trial({"XDG_CONFIG_HOME":str(host),
                                     "NPM_CONFIG_STORE_DIR":"/outside-store",
                                     "npm_config_cache_dir":"/outside-cache"})
        self.assertEqual(code, 0)
        checkout = Path(manifest["checkout"])
        observed = json.loads((checkout / "observed.json").read_text())
        self.assertEqual(observed["npm_unknown_config"], [])
        self.assertEqual(config.read_text(), "registry=https://registry.npmjs.org/\n")
        self.assertEqual(list(host.iterdir()), [config])

    def test_unverified_node_does_not_launch_the_trial(self):
        for valid_json in (False, True):
            with self.subTest(valid_json=valid_json):
                executable = self.root / f"wrong-node-{valid_json}"
                response = json.dumps({"executable":sys.executable, "version":"v24.18.0"}) if valid_json else "not a Node runtime"
                executable.write_text(f"#!/bin/sh\nprintf '%s\\n' {shlex.quote(response)}\n")
                executable.chmod(0o755)
                self.args.node = executable
                self.args.output = self.root / f"wrong-node-evidence-{valid_json}"
                code, manifest = self.trial()
                self.assertEqual(code, 1)
                self.assertEqual(manifest["status"], "failed")
                self.assertNotIn("commit", manifest)
                self.assertEqual(len(manifest["commands"]), 1)

    def test_real_package_managers_use_private_caches_without_npm_warnings(self):
        npm, pnpm = shutil.which("npm"), shutil.which("pnpm")
        if not npm or not pnpm:
            self.skipTest("npm and pnpm are required for cache routing integration")
        code, manifest = self.trial()
        self.assertEqual(code, 0)
        private = manifest["private_caches"]
        env = {key:value for key,value in os.environ.items()
               if key.lower() not in ("npm_config_store_dir", "npm_config_cache_dir")}
        env.update({key:value for key,value in private.items() if key not in ("pnpm_store", "pnpm_cache")})
        for command, expected in (([npm,"config","get","cache"], private["npm_config_cache"]),
                                  ([pnpm,"config","get","store-dir"], private["pnpm_store"]),
                                  ([pnpm,"config","get","cache-dir"], private["pnpm_cache"])):
            result = subprocess.run(command, cwd=manifest["checkout"], env=env, capture_output=True, text=True, check=True, timeout=30)
            self.assertEqual(result.stdout.strip(), expected)
            self.assertNotIn("Unknown env config", result.stderr)
        store = subprocess.check_output([pnpm,"store","path"], cwd=manifest["checkout"], env=env, text=True, timeout=30).strip()
        self.assertTrue(Path(store).is_relative_to(private["pnpm_store"]))
        (self.root / "package.json").write_text('{"private":true}\n')
        nested = subprocess.run([pnpm,"exec",npm,"config","get","cache"], cwd=self.root, env=env,
                                capture_output=True, text=True, check=True, timeout=30)
        self.assertEqual(nested.stdout.strip(), private["npm_config_cache"])
        for key in ("store-dir", "cache-dir"):
            self.assertNotIn(f'Unknown env config "{key}"', nested.stderr)

    def test_framework_scope_reaches_agent_without_extra_instructions(self):
        self.args.framework = "playwright"
        code, manifest = self.trial()
        self.assertEqual(code, 0)
        checkout = Path(manifest["checkout"])
        observed = json.loads((checkout / "observed.json").read_text())
        self.assertEqual(observed["prompt"],
                         f"Use ddtest in {checkout.parent / 'bin'} and onboard this repository to test-optimization for playwright only.")
        self.assertEqual(manifest["framework_scope"], "playwright")

    def test_cleanup_permission_error_requires_observed_process_group_exit(self):
        for disappears in (True, False):
            with self.subTest(disappears=disappears):
                signals = []

                def killpg(pid, signal):
                    signals.append(signal)
                    if disappears and len(signals) == 3:
                        raise ProcessLookupError()
                    raise PermissionError("group cleanup denied")

                lines = []
                with patch.object(run_eval.os, "killpg", side_effect=killpg), patch.object(run_eval.time, "sleep"):
                    command = [sys.executable, "-c", "print('version probe completed')"]
                    if disappears:
                        self.assertEqual(run_eval.execute(command, self.root, os.environ.copy(), 5, lines.append), 0)
                    else:
                        with self.assertRaisesRegex(PermissionError, "group cleanup denied"):
                            run_eval.execute(command, self.root, os.environ.copy(), 5, lines.append)
                self.assertEqual(lines, ["version probe completed"])
                self.assertEqual(signals[0], run_eval.signal.SIGKILL)
                self.assertGreater(len(signals), 1)
                self.assertTrue(all(value == 0 for value in signals[1:]))

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

    def test_verdict_detection_uses_full_answer_before_excerpting(self):
        for agent in ("codex", "claude"):
            with self.subTest(agent=agent):
                self.args.agent = agent
                self.args.output = self.root / f"{agent}-long-answer"
                code, manifest = self.trial({"FAKE_LONG_VERDICT": "1"})
                self.assertEqual(code, 1)
                self.assertEqual(manifest["failure_kind"], "agent_reporting")
                self.assertEqual(manifest["agent_reporting"]["status"], "fail")
                self.assertFalse(manifest["retry_recommended"])

    def test_unfinished_background_work_fails_even_after_success_result(self):
        self.args.agent = "claude"
        code, manifest = self.trial({"FAKE_BACKGROUND": "1"})
        self.assertEqual(code, 1)
        self.assertEqual(manifest["commands"][-1]["exit_code"], 0)
        self.assertEqual(manifest["failure_kind"], "infrastructure")
        self.assertTrue(manifest["retry_recommended"])
        self.assertEqual(manifest["unfinished_background_commands"], ["bg123"])
        self.assertNotEqual(manifest["status"], "completed")

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


class CypressEnvironmentTests(unittest.TestCase):
    def test_long_matrix_timeout_is_scoped_and_bounded(self):
        self.assertEqual(run_eval.agent_environment("claude", 7200, "cypress")["BASH_MAX_TIMEOUT_MS"], "3600000")
        self.assertEqual(run_eval.agent_environment("claude", 30, "cypress")["BASH_MAX_TIMEOUT_MS"], "30000")
        self.assertEqual(run_eval.agent_environment("claude", 7200, "jest")["BASH_MAX_TIMEOUT_MS"], "600000")
        self.assertEqual(run_eval.agent_environment("codex", 7200, "cypress"), {})


class FailureClassificationTests(unittest.TestCase):
    def test_transport_failure_is_retryable_but_generic_failure_is_not(self):
        self.assertEqual(run_eval.failure_kind(['API Error: Response stalled mid-stream']), 'infrastructure')
        self.assertEqual(run_eval.failure_kind([], timed_out=True), 'infrastructure')
        for message in ['permission denied', 'authentication required', 'testdrive failed', 'Done']:
            self.assertEqual(run_eval.failure_kind([message]), 'agent')


class BackgroundTests(unittest.TestCase):
    def test_observed_background_completion_and_ordinary_output(self):
        tracker = run_eval.BackgroundCommands()
        def bash(identifier):
            tracker.observe({"type":"assistant", "message":{"content":[{"type":"tool_use", "name":"Bash", "id":identifier}]}})
        def result(identifier, text):
            tracker.observe({"type":"user", "message":{"content":[{"type":"tool_result", "tool_use_id":identifier, "content":text}]}})
        bash("one")
        result("one", "Command running in background with ID: task1. Output: /tmp/out")
        self.assertEqual(tracker.unfinished(), ["task1"])
        tracker.observe({"type":"system", "subtype":"task_notification", "task_id":"task1", "status":"completed"})
        self.assertEqual(tracker.unfinished(), [])
        bash("two")
        result("two", "Moved to the background (ID: task2).")
        tracker.observe({"type":"user", "message":{"content":"<task-notification><task-id>task2</task-id><status>completed</status></task-notification>"}})
        self.assertEqual(tracker.unfinished(), [])
        bash("three")
        result("three", "These tests discuss background work; all finished.")
        self.assertEqual(tracker.unfinished(), [])
        bash("four")
        self.assertEqual(tracker.unfinished(), ["four"])

class VerdictTests(unittest.TestCase):
    def compact_report(self):
        return {"working_directory": "/customer/repo", "html_report_current": True,
                "reporting_counts": {"test_executions": 96, "source_test_identities": 32},
                "summary": {"status": "INCOMPLETE", "selected_validation": {"status": "PASSED"},
                            "executed_validation": {"status": "PASSED", "passed": 5, "configurations": 5},
                            "configuration_coverage": {"discovered": 8, "executed": 5, "paired": 5,
                                                       "validated": 5, "blocked": 2, "not_exercised": 1, "incomplete": 0}}}

    def compact_answer(self):
        return ("Selected Cypress testdrive: PASSED.\nExecuted Cypress validations: PASSED (5/5).\n"
                "Onboarding validation: INCOMPLETE. Local validation: PASSED.\n"
                "Configurations: 5/8 validated; 5 executed, 5 paired; 2 blocked, 1 not exercised, 0 incomplete.\n"
                "Reporting: 96 test executions; 32 source-test identities.\n"
                "[HTML report](</customer/repo/.testoptimization/report.html>)\n"
                "[JSON evidence](</customer/repo/.testoptimization/testdrive.json>)\n")

    def test_compact_labeled_facts_match_without_byte_identical_prose(self):
        answer = self.compact_answer().replace("5/8 validated", "5 / 8 fully validated").replace("PASSED (5/5)", "**PASSED** (5 / 5)")
        result = run_eval.reporting_verdict(answer + "\nSome owner setup is still required.\n", self.compact_report())
        self.assertEqual(result["status"], "pass")
        self.assertEqual(result["differences"], [])
        self.assertEqual(result["missing"], [])
        self.assertIn("independent technical evaluation", result["reason"])

    def test_swapped_distinct_counts_and_scope_upgrades_fail(self):
        for original, wrong in (("96 test executions; 32 source-test identities", "32 test executions; 96 source-test identities"),
                                ("5/8 validated", "8/8 validated"),
                                ("5 executed, 5 paired", "8 executed, 8 paired"),
                                ("PASSED (5/5)", "PASSED (8/8)"),
                                ("Onboarding validation: INCOMPLETE", "Onboarding validation: COMPLETE LOCALLY"),
                                ("Selected Cypress testdrive: PASSED", "Selected Cypress testdrive: INCOMPLETE")):
            with self.subTest(wrong=wrong):
                result = run_eval.reporting_verdict(self.compact_answer().replace(original, wrong), self.compact_report())
                self.assertEqual(result["status"], "fail")
                self.assertTrue(result["differences"])

    def test_missing_ambiguous_facts_and_relative_links_require_review(self):
        for answer in ("", "Done", self.compact_answer().replace("Reporting: 96 test executions; 32 source-test identities.\n", ""),
                       self.compact_answer().replace("Selected Cypress testdrive: PASSED.\n", ""),
                       self.compact_answer().replace("</customer/repo/.testoptimization/", "<.testoptimization/")):
            with self.subTest(answer=answer[:80]):
                result = run_eval.reporting_verdict(answer, self.compact_report())
                self.assertEqual(result["status"], "review")
                self.assertTrue(result["missing"])
                self.assertEqual(result["differences"], [])
        for report in ({}, {"summary": {"status": "INCOMPLETE"}}):
            self.assertEqual(run_eval.reporting_verdict("Onboarding validation: INCOMPLETE.", report)["status"], "review")

    def test_ambiguous_native_identities_are_never_upgraded_to_unique_tests(self):
        report = self.compact_report()
        report["reporting_counts"]["source_test_identities"] = None
        answer = self.compact_answer().replace("32 source-test identities", "source-test identities unavailable")
        self.assertEqual(run_eval.reporting_verdict(answer, report)["status"], "pass")
        for claim in ("32 source-test identities", "96 unique tests"):
            wrong = answer.replace("source-test identities unavailable", claim)
            self.assertEqual(run_eval.reporting_verdict(wrong, report)["status"], "fail")

    def test_stale_absolute_report_link_fails_but_missing_links_warn(self):
        answer = self.compact_answer().replace("/customer/repo/.testoptimization/testdrive.json", "/another/trial/.testoptimization/testdrive.json")
        self.assertEqual(run_eval.reporting_verdict(answer, self.compact_report())["status"], "fail")
        answer = self.compact_answer().split("[HTML report]")[0]
        self.assertEqual(run_eval.reporting_verdict(answer, self.compact_report())["status"], "review")

    def test_completion_claim_does_not_override_incomplete(self):
        report = {"summary": {"status": "INCOMPLETE"}}
        self.assertEqual(run_eval.reporting_verdict("Onboarding is done on my end.\nINCOMPLETE", report)["status"], "fail")
        self.assertEqual(run_eval.reporting_verdict(self.compact_answer() + "\nOnboarding is complete on my end.", self.compact_report())["status"], "fail")
        for text in ("Onboarding validation: INCOMPLETE.", "Onboarding is not complete. INCOMPLETE", "Workflow edits are complete. INCOMPLETE"):
            self.assertEqual(run_eval.reporting_verdict(text, report)["status"], "review")

    def test_startup_timeout_prevents_clone_and_is_retryable(self):
        case = RunnerTests()
        case.setUp()
        self.addCleanup(case.doCleanups)
        seen = []
        def hung(command, cwd, env, timeout, on_line, prompt=None):
            seen.append((command, timeout))
            raise TimeoutError("CLI startup hung")
        with patch.object(run_eval, "execute", side_effect=hung):
            code, manifest = case.trial()
        self.assertEqual(code, 1)
        self.assertEqual(seen, [(["codex", "--version"], 15)])
        self.assertEqual(manifest["failure_stage"], "cli_startup")
        self.assertEqual(manifest["failure_kind"], "infrastructure")
        self.assertTrue(manifest["retry_recommended"])


if __name__ == "__main__":
    unittest.main()
