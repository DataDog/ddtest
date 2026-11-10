"""Offline runner checks using real Git clones and fake coding-agent CLIs."""

import contextlib
import io
import json
import os
import platform
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
from isolation import environment, cleanup, interrupted, trial_root

# Unit trials never use the real home default on macOS, and stay short enough
# for Watchman's Unix socket. Container tests patch the root themselves.
os.environ.setdefault("DDTEST_EVAL_TRIAL_ROOT", str(Path("/tmp" if sys.platform == "darwin" else tempfile.gettempdir()).resolve()))


class FakeContainer:
    """Unit seam only: production never has a host-execution option."""
    cli_versions = {"codex":"1.0", "claude":"1.0"}
    def __init__(self, trial, image):
        self.name = "fake-container"
        self.path = os.environ["PATH"]
    def start(self):
        pass
    def close(self):
        pass
    def stop_processes(self):
        pass
    def execute(self, argv, cwd, env, timeout, on_line, prompt, executor):
        env = {**env, **{k:v for k,v in os.environ.items() if k.startswith("FAKE_")}}
        return executor(argv,cwd,env,timeout,on_line,prompt)



FAKE_AGENT = '''#!/usr/bin/env python3
import json, os, pathlib, subprocess, sys
if "--version" in sys.argv:
    print("fake-agent 1.0")
    sys.exit(0)
if os.environ.get("FAKE_PLAIN_ERROR"):
    print(os.environ["FAKE_PLAIN_ERROR"], file=sys.stderr)
    sys.exit(1)
prompt = sys.stdin.read()
if prompt.startswith("Reply AUTH_OK only"):
    if pathlib.Path(sys.argv[0]).name == "codex":
        events=[{"type":"item.completed","item":{"type":"agent_message","text":"AUTH_OK"}},{"type":"turn.completed"}]
    else:
        events=[{"type":"result","result":"AUTH_OK"}]
    for event in events: print(json.dumps(event))
    sys.exit(0)
pathlib.Path("observed.json").write_text(json.dumps({
    "prompt": prompt, "argv": sys.argv[1:], "environment":dict(os.environ),
    "agent_environment": {key: os.environ.get(key) for key in ("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "CLAUDE_CODE_DISABLE_AUTO_MEMORY", "BASH_DEFAULT_TIMEOUT_MS", "BASH_MAX_TIMEOUT_MS")},
    "dd_env": [key for key in os.environ if key.startswith(("DD_", "DATADOG_"))],
    "cache_environment": {key: os.environ.get(key) for key in ("npm_config_cache", "YARN_CACHE_FOLDER", "XDG_CONFIG_HOME", "PLAYWRIGHT_BROWSERS_PATH", "PUPPETEER_CACHE_DIR", "TMPDIR")},
    "npm_unknown_config": [key for key in os.environ if key.lower() in ("npm_config_store_dir", "npm_config_cache_dir")],
    "zdotdir": os.environ.get("ZDOTDIR"),
    "login": {name: json.loads(pathlib.Path(name).read_text()) for name in (
        os.path.join(os.environ.get("CODEX_HOME", "/nonexistent"), "auth.json"),
        os.path.join(os.environ.get("CLAUDE_CONFIG_DIR", "/nonexistent"), ".credentials.json")) if os.path.isfile(name)},
}))
if os.environ.get("FAKE_ASK_USER"):
    resumed = "--resume" in sys.argv or sys.argv[1:3] == ["exec", "resume"]
    with pathlib.Path("turns.jsonl").open("a") as stream:
        stream.write(json.dumps({"argv": sys.argv[1:], "stdin": prompt}) + "\\n")
    if not resumed or os.environ.get("FAKE_ASK_AGAIN"):
        asked = "Exit code 1\\n" + os.environ["FAKE_ASK_USER"]
        if os.environ.get("FAKE_REDIRECT_DDTEST"):
            # Run the ddtest the prompt names, keep its JSON in a private file,
            # and show only a reformatted summary.
            ddtest = os.path.join(prompt.split("Use ddtest in ", 1)[1].split(" and onboard", 1)[0], "ddtest")
            saved = os.path.join(os.environ["TMPDIR"], "onboard.json")
            status = subprocess.run(ddtest + " onboard > " + saved, shell=True).returncode
            result = json.loads(pathlib.Path(saved).read_text())
            asked = "exit " + str(status) + "\\n" + "\\n".join("== " + key + " ==\\n" + str(value) for key, value in result.items())
        question = "Which Ruby version does the Jenkinsfile / Run Tests job run?"
        if pathlib.Path(sys.argv[0]).name == "codex":
            events = [{"type": "thread.started", "thread_id": "thread-1"},
                      {"type": "item.completed", "item": {"type": "command_execution", "command": "/trial/bin/ddtest onboard", "exit_code": 1, "aggregated_output": asked}},
                      {"type": "item.completed", "item": {"type": "agent_message", "text": question}},
                      {"type": "turn.completed"}]
        else:
            events = [{"type": "system", "subtype": "init", "session_id": "session-1"},
                      {"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "Bash", "id": "ask", "input": {"command": "/trial/bin/ddtest onboard"}}]}},
                      {"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "ask", "is_error": True, "content": asked}]}},
                      {"type": "result", "result": question, "session_id": "session-1"}]
        for event in events:
            print(json.dumps(event))
        sys.exit(0)
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
        {"type": "system", "subtype": "init", "session_id": "fake-session", "model": "claude-fake-model[1m]"},
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
    encoded = os.environ["FAKE_REPORT_JSON"]
    if os.environ.get("FAKE_REPORT_PYTHON_ENVIRONMENT"):
        report = json.loads(encoded)
        report["preflight"].update(executable=sys.executable, prefix=sys.prefix)
        encoded = json.dumps(report)
    pathlib.Path(".testoptimization/testdrive.json").write_text(encoded)
    for event in events:
        if event.get("item", {}).get("type") == "agent_message":
            event["item"]["text"] = os.environ.get("FAKE_RESPONSE", "Done")
        elif event["type"] == "result":
            event["result"] = os.environ.get("FAKE_RESPONSE", "Done")
if os.environ.get("FAKE_MEMORY"):
    memory = pathlib.Path(os.environ["HOME"], ".claude", "projects", "-trial-repo", "memory")
    memory.mkdir(parents=True)
    (memory / "note.md").write_text("remembered")
if os.environ.get("FAKE_INITIAL_RUNTIME"):
    events.insert(-1, {"type":"item.completed", "item":{"type":"command_execution", "command":"ddtest testdrive --check-only", "exit_code":0,
                   "aggregated_output":"Local runtime: Node " + os.environ["FAKE_INITIAL_RUNTIME"] + "."}})
for event in events:
    print(json.dumps(event))
'''


# Prints next_command as ddtest does: DDTEST_SELF_PATH, or, for an older build
# (FAKE_LEGACY_DDTEST), its own path with Go's " (deleted)" trimming. A stale
# build (FAKE_STALE_DDTEST) prints its own path unchanged.
FAKE_DDTEST = """#!/bin/sh
if [ "$1" = testdrive ]; then
  self=${0% (deleted)}
  [ -n "$FAKE_LEGACY_DDTEST" ] || self=${DDTEST_SELF_PATH:-$self}
  [ -z "$FAKE_STALE_DDTEST" ] || self=$0
  printf '{"status":"needs_input","code":"consent_required","next_command":["%s","testdrive","--agent=true","--yes"]}\\n' "$self"
  exit 1
fi
exit 0
"""


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.binary = self.root / "ddtest"
        self.binary.write_text(FAKE_DDTEST)
        self.binary.chmod(0o755)
        for agent in ("codex", "claude"):
            executable = self.root / agent
            executable.write_text(FAKE_AGENT)
            executable.chmod(0o755)
        # Reuse an existing commit: fixtures do not create unsigned Git commits.
        self.source = subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"], cwd=Path(__file__).parent, text=True).strip()
        self.args = type("Args", (), dict(
            repo=self.source, ref="HEAD", ddtest=self.binary, agent="codex", image="sha256:"+"0"*64,
            setup_command=[], output=self.root / "evidence", model=None, timeout=30))()

    def trial(self, extra_env=None):
        env = {"PATH": str(self.root) + os.pathsep + os.environ["PATH"],
               "DD_API_KEY": "fake-key", "DATADOG_API_KEY": "fake-key", **(extra_env or {})}
        with patch.dict(os.environ, env), patch.object(run_eval,"Container",FakeContainer), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            code = run_eval.run(self.args)
        manifest = json.loads((self.args.output / "run.json").read_text())
        self.addCleanup(shutil.rmtree, manifest["trial_directory"], ignore_errors=True)
        return code, manifest

    def test_non_json_ddtest_instructions_are_retained_and_redacted(self):
        text = 'Add /.testoptimization/ to .gitignore. DD_API_KEY=synthetic-output-key'
        self.binary.write_text(FAKE_DDTEST.replace('\nexit 0\n', "\nprintf '%s\\n' " + shlex.quote(text) + "\nexit 0\n"))
        original = FakeContainer.execute
        def execute(container, argv, cwd, env, timeout, on_line, prompt, executor):
            if prompt and not prompt.startswith('Reply AUTH_OK only'):
                subprocess.run([env[run_eval.SELF_PATH_VARIABLE], 'onboard', '--instructions'], cwd=cwd, env=env, check=True, capture_output=True)
            return original(container, argv, cwd, env, timeout, on_line, prompt, executor)
        with patch.object(FakeContainer, 'execute', execute):
            code, _ = self.trial()
        self.assertEqual(code, 0)
        rows = [json.loads(line) for line in (self.args.output / 'ddtest-results.jsonl').read_text().splitlines()]
        self.assertTrue(any(row['result'] is None and 'Add /.testoptimization/ to .gitignore.' in row.get('output', '') for row in rows))
        self.assertNotIn('synthetic-output-key', json.dumps(rows))

    def test_local_repository_is_cloned_from_a_copy_in_the_trial(self):
        # The container mounts only the trial, so a host repository, as a
        # project patched for an eval, reaches it as a bare copy there.
        code, manifest = self.trial()
        remote = next(record["argv"] for record in manifest["commands"] if record["argv"][:3] == ["git", "remote", "add"])
        trial = Path(manifest["trial_directory"])
        self.assertEqual(Path(remote[-1]), trial / "harness" / "source.git")
        self.assertFalse((trial / "harness" / "source.git").exists(), "the copy is removed once checked out")
        self.assertEqual(manifest["repository"], self.source)
        self.assertEqual(manifest["repository_source"], "local")
        self.assertTrue((trial / "repo" / ".git").exists())
        # Relative submodule URLs resolve against the source's own remote.
        expected = subprocess.run(["git", "-C", self.source, "remote", "get-url", "origin"], capture_output=True, text=True)
        if expected.returncode == 0:
            remote = subprocess.check_output(["git", "-C", str(trial / "repo"), "remote", "get-url", "origin"], text=True).strip()
            self.assertEqual(remote, expected.stdout.strip())

    def test_local_repository_without_origin_preserves_history_during_setup(self):
        history = subprocess.check_output(["git", "-C", self.source, "rev-list", "--max-count=2", "HEAD"], text=True).splitlines()
        local = self.root / "local.git"
        subprocess.run(["git", "clone", "--mirror", "--quiet", "--", self.source, str(local)], check=True)
        subprocess.run(["git", "-C", str(local), "remote", "remove", "origin"], check=True, capture_output=True)
        self.args.repo = str(local)
        self.args.ref = history[0]
        # A shallow CI source may have only HEAD; preserve whatever history
        # the local input actually has without needing a deleted remote.
        self.args.setup_command = [shlex.join(["git", "merge-base", "--is-ancestor", history[-1], "HEAD"])]
        code, manifest = self.trial()
        self.assertEqual(code, 0, manifest)
        self.assertEqual(manifest["commit"], history[0])
        self.assertFalse((Path(manifest["trial_directory"]) / "harness" / "source.git").exists())

    def test_remote_pinned_commit_has_only_one_commit_of_history(self):
        # Prefer a non-tip SHA; a shallow CI checkout may have only HEAD.
        expected = subprocess.check_output(["git", "-C", self.source, "rev-list", "--max-count=2", "HEAD"], text=True).splitlines()[-1]
        self.args.repo = Path(self.source).as_uri()
        self.args.ref = expected
        code, manifest = self.trial()
        self.assertEqual(code, 0, manifest)
        self.assertEqual(manifest["commit"], expected)
        checkout = manifest["checkout"]
        self.assertEqual(subprocess.check_output(["git", "-C", checkout, "rev-list", "--count", "HEAD"], text=True).strip(), "1")
        self.assertEqual(subprocess.check_output(["git", "-C", checkout, "rev-parse", "--is-shallow-repository"], text=True).strip(), "true")

    def test_preparation_never_runs_an_agent_and_records_phase_timings(self):
        self.args.prepare_cache = "a" * 64
        capture = [sys.executable, "-c", "print('captured fixture cache')"]
        with patch.object(run_eval.dependency_cache, "command", return_value=capture):
            code, manifest = self.trial()
        self.assertEqual(code, 0, manifest)
        self.assertEqual(manifest["status"], "prepared")
        self.assertEqual(manifest["agent_reporting"]["status"], "not run")
        self.assertFalse((Path(manifest["checkout"]) / "observed.json").exists())
        self.assertNotIn("agent", manifest["timings_seconds"]["commands"])
        self.assertIn("dependency_cache_capture", manifest["timings_seconds"]["commands"])
        for command in manifest["commands"]:
            self.assertGreaterEqual(command["duration_seconds"], 0)
            self.assertLessEqual(command["duration_seconds"], manifest["timings_seconds"]["total"])
        events = [json.loads(line) for line in (self.args.output / "activity.jsonl").read_text().splitlines()]
        self.assertTrue(all(isinstance(event["observed_at"], float) for event in events))

    def test_preparation_refuses_model_credentials_before_creating_a_trial(self):
        self.args.prepare_cache = "a" * 64
        self.args.existing_login = True
        with self.assertRaisesRegex(ValueError, "cannot use model credentials"):
            run_eval.run(self.args)
        self.assertFalse(self.args.output.exists())

    def test_relative_submodules_of_a_local_repository_without_origin_are_mirrored(self):
        # Without an origin, relative submodule URLs resolve against the
        # mirror, so each relative submodule's repository is mirrored beside
        # it; a URL with a scheme is left to Git.
        local = self.root / "work" / "app"
        local.mkdir(parents=True)
        subprocess.run(["git", "clone", "--bare", "--quiet", self.source, str(self.root / "work" / "shared.git")], check=True)
        checkout = self.root / "checkout"
        checkout.mkdir()
        (checkout / ".gitmodules").write_text('[submodule "shared"]\n\tpath = shared\n\turl = ../shared.git\n'
                                              '[submodule "remote"]\n\tpath = remote\n\turl = https://example.com/remote.git\n')
        source = self.root / "trial" / "harness" / "source.git"
        source.mkdir(parents=True)
        mirrors = run_eval.local_submodule_mirrors(checkout, local, source)
        self.assertEqual(mirrors, [self.root / "trial" / "harness" / "shared.git"])
        self.assertTrue((mirrors[0] / "HEAD").exists())
        # Git refuses file transport for submodules unless the command allows it.
        self.assertEqual(run_eval.submodule_update_argv(True), ["git", "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive"])
        self.assertEqual(run_eval.submodule_update_argv(False), ["git", "submodule", "update", "--init", "--recursive"])

    def test_nested_relative_submodules_of_a_local_repository_are_mirrored(self):
        # A mirrored submodule's own relative submodules, at the revision it
        # pins, resolve beside its mirror, as Git resolves them.
        gpg = shutil.which("gpg")
        if not gpg:
            self.skipTest("GnuPG is required for signed submodule fixtures")
        signing = self.root / "signing"
        signing.mkdir(mode=0o700)
        sign_env = {**os.environ, "GNUPGHOME": str(signing)}
        gpgconf = shutil.which("gpgconf")
        if gpgconf:
            self.addCleanup(subprocess.run, [gpgconf, "--homedir", str(signing), "--kill", "gpg-agent"], capture_output=True)
        subprocess.run([gpg, "--batch", "--pinentry-mode", "loopback", "--passphrase", "",
                        "--quick-generate-key", "Eval fixture <eval@example.com>", "ed25519", "sign", "0"],
                       env=sign_env, check=True, capture_output=True)
        git = ["git", "-c", "gpg.format=openpgp", "-c", "gpg.openpgp.program=" + gpg,
               "-c", "user.signingkey=eval@example.com", "-c", "user.name=eval", "-c", "user.email=eval@example.com"]
        work = self.root / "work"
        work.mkdir()
        subprocess.run(["git", "clone", "--bare", "--quiet", self.source, str(work / "leaf.git")], check=True)
        leaf = subprocess.check_output(["git", "-C", str(work / "leaf.git"), "rev-parse", "HEAD"], text=True).strip()

        def repository_with_submodule(path, name, url, pinned):
            subprocess.run(["git", "init", "--quiet", str(path)], check=True)
            (path / ".gitmodules").write_text('[submodule "' + name + '"]\n\tpath = ' + name + '\n\turl = ' + url + '\n')
            subprocess.run(["git", "-C", str(path), "update-index", "--add", "--cacheinfo", "160000," + pinned + "," + name], check=True)
            subprocess.run(["git", "-C", str(path), "add", ".gitmodules"], check=True)
            message = "pin " + name + "\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
            subprocess.run([*git, "-C", str(path), "commit", "-S", "--quiet", "-m", message], env=sign_env, check=True)
            subprocess.run([*git, "-C", str(path), "verify-commit", "HEAD"], env=sign_env, check=True, capture_output=True)
            return subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()

        shared = repository_with_submodule(work / "shared", "leaf", "../leaf.git", leaf)
        subprocess.run(["git", "clone", "--bare", "--quiet", str(work / "shared"), str(work / "shared.git")], check=True)
        local = work / "app"
        repository_with_submodule(local, "shared", "../shared.git", shared)
        source = self.root / "trial" / "harness" / "source.git"
        source.mkdir(parents=True)
        mirrors = run_eval.local_submodule_mirrors(local, local, source)
        harness = self.root / "trial" / "harness"
        self.assertEqual(mirrors, [harness / "shared.git", harness / "leaf.git"])
        self.assertTrue(all((mirror / "HEAD").exists() for mirror in mirrors))

    def test_preflight_refresh_is_redacted_before_finalization(self):
        source = self.root / "selected-login.json"
        source.write_text(json.dumps({"tokens": {"access_token": "synthetic-original-access", "refresh_token": "synthetic-original-refresh"}}))
        self.args.login_json = source
        refreshed = "opaqueRotatedValueForReview218"
        execute = FakeContainer.execute
        def refresh_or_print(container, argv, cwd, env, timeout, on_line, prompt, executor):
            code = execute(container, argv, cwd, env, timeout, on_line, prompt, executor)
            if prompt and prompt.startswith("Reply AUTH_OK only"):
                destination = Path(env["CODEX_HOME"]) / "auth.json"
                data = json.loads(destination.read_text())
                data["tokens"]["refresh_token"] = refreshed
                destination.write_text(json.dumps(data))
            elif prompt:
                on_line(json.dumps({"type": "item.completed", "item": {
                    "type": "command_execution", "command": "echo model response", "exit_code": 0,
                    "aggregated_output": "response " + refreshed}}))
            return code
        with patch.object(FakeContainer, "execute", refresh_or_print):
            code, manifest = self.trial()
        self.assertEqual(code, 0)
        activity = (self.args.output / "activity.jsonl").read_text()
        self.assertIn("response [REDACTED]", activity)
        self.assertNotIn(refreshed, activity)
        self.assertEqual(json.loads(source.read_text())["tokens"]["refresh_token"], refreshed)
        self.assertEqual(manifest["login"], "removed")

    def test_non_object_trial_login_does_not_abort_evidence_collection(self):
        execute = FakeContainer.execute
        for replacement in ([], None, 42):
            with self.subTest(replacement=replacement):
                source = self.root / "selected-login.json"
                original = {"tokens": {"access_token": "synthetic-original-access"}}
                source.write_text(json.dumps(original))
                self.args.login_json = source
                self.args.output = self.root / ("non-object-" + str(replacement))
                def replace_login(container, argv, cwd, env, timeout, on_line, prompt, executor):
                    code = execute(container, argv, cwd, env, timeout, on_line, prompt, executor)
                    if prompt and not prompt.startswith("Reply AUTH_OK only"):
                        (Path(env["CODEX_HOME"]) / "auth.json").write_text(json.dumps(replacement))
                    return code
                with patch.object(FakeContainer, "execute", replace_login):
                    code, manifest = self.trial()
                self.assertEqual(code, 0)
                self.assertEqual(manifest["login"], "removed")
                self.assertIn("not persisted", manifest["login_refresh"])
                self.assertEqual(json.loads(source.read_text()), original)
                self.assertTrue((self.args.output / "changes.patch").is_file())
                self.assertTrue((self.args.output / "activity.jsonl").is_file())

    def selected_node(self, version="24.18.0"):
        selected = self.root / "selected" / "node"
        selected.parent.mkdir()
        selected.write_text(f"#!{sys.executable}\nimport json, pathlib, sys\n"
                            f"print(json.dumps({{'executable':str(pathlib.Path(__file__).resolve()), 'version':'v{version}', 'requested_executable':str(pathlib.Path(__file__).resolve())}}) if '-p' in sys.argv else 'v{version}')\n")
        selected.chmod(0o755)
        return selected

    def test_pytest_uses_private_python_and_audits_the_final_report(self):
        for agent in ("codex", "claude"):
            with self.subTest(agent=agent):
                self.args.agent = agent
                self.args.python = Path(sys.executable)
                self.args.framework = "pytest"
                self.args.output = self.root / (agent + "-python-evidence")
                report = {"summary": {"status": "INCOMPLETE"},
                          "preflight": {"runtime": platform.python_version()},
                          "runs": [{"role": "baseline", "command": "python -m pytest", "exit_code": 0}],
                          "retained_execution": {"result": {"preflight": {"runtime": "0.0.0"}}}}
                code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps(report), "FAKE_REPORT_PYTHON_ENVIRONMENT": "1"})
                self.assertEqual(code, 0, manifest)
                venv = str(Path(manifest["trial_directory"]) / "venv")
                self.assertEqual(manifest["python_runtime"]["prefix"], venv)
                self.assertEqual(manifest["ddtest_python_runtime"]["status"], "matched")
                observed = json.loads((Path(manifest["checkout"]) / "observed.json").read_text())
                self.assertEqual(observed["environment"]["VIRTUAL_ENV"], venv)
                self.assertEqual(observed["environment"]["UV_PROJECT_ENVIRONMENT"], venv)
                self.assertIn(venv + "/bin", observed["environment"]["PATH"].split(os.pathsep))
                self.assertIn("for pytest only", observed["prompt"])
                for key in ("PIP_CACHE_DIR", "UV_CACHE_DIR"):
                    self.assertTrue(Path(manifest["private_caches"][key]).is_dir())
                if agent == "codex":
                    self.assertIn('shell_environment_policy.set.VIRTUAL_ENV=' + json.dumps(venv), observed["argv"])

    def test_pytest_requested_version_is_frozen_before_setup(self):
        self.args.framework = "pytest"
        self.args.python = Path(sys.executable)
        self.args.setup_command = [shlex.join(["python", "-c",
            "import os, pathlib; pathlib.Path(os.environ['VIRTUAL_ENV'], 'setup-finished').touch()"])]
        execute = FakeContainer.execute
        for changed in (True, False):
            with self.subTest(changed=changed):
                self.args.output = self.root / ("changed-version" if changed else "same-version")
                version = "0.0.0" if changed else platform.python_version()
                def replace_runtime(container, argv, cwd, env, timeout, on_line, prompt, executor):
                    if changed and argv[:2] == ["python", "-c"] and "platform.python_version()" in argv[2] and (Path(env["VIRTUAL_ENV"]) / "setup-finished").exists():
                        on_line(json.dumps({"executable": str(Path(env["VIRTUAL_ENV"]) / "bin/python"),
                                            "prefix": env["VIRTUAL_ENV"], "version": version}))
                        return 0
                    return execute(container, argv, cwd, env, timeout, on_line, prompt, executor)
                report = {"summary": {"status": "INCOMPLETE"}, "preflight": {"runtime": version},
                          "runs": [{"role": "baseline", "command": "python -m pytest", "exit_code": 0}]}
                with patch.object(FakeContainer, "execute", replace_runtime):
                    code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps(report), "FAKE_REPORT_PYTHON_ENVIRONMENT": "1"})
                self.assertEqual(code, 1 if changed else 0)
                self.assertEqual(manifest["python_runtime"]["requested_version"], platform.python_version())
                self.assertEqual(manifest["python_runtime"]["version"], version)
                if changed:
                    self.assertEqual(manifest["failure_kind"], "evaluator_environment")
                    self.assertEqual(manifest["failure_stage"], "python_environment")
                    self.assertFalse(manifest["retry_recommended"])
                    self.assertFalse((Path(manifest["checkout"]) / "observed.json").exists())
                else:
                    self.assertEqual(manifest["ddtest_python_runtime"]["status"], "matched")

    def test_pytest_same_version_other_environment_fails_the_trial(self):
        self.args.framework = "pytest"
        self.args.python = Path(sys.executable)
        other = self.root / "other-venv"
        subprocess.run([sys.executable, "-m", "venv", str(other)], check=True, capture_output=True)
        runtime = json.loads(subprocess.check_output([str(other / "bin/python"), "-c",
            "import json, platform, sys; print(json.dumps({'runtime': platform.python_version(), 'executable': sys.executable, 'prefix': sys.prefix}))"], text=True))
        self.assertEqual(runtime["runtime"], platform.python_version())
        base = {"runtime": platform.python_version(), "executable": sys.executable, "prefix": sys.prefix}
        for name, preflight, configurations in (("base", base, []), ("other-venv", runtime, []),
                ("mixed-configurations", {"runtime": platform.python_version()}, [{"preflight": runtime}])):
            with self.subTest(name=name):
                self.args.output = self.root / (name + "-evidence")
                report = {"summary": {"status": "INCOMPLETE"}, "preflight": preflight,
                          "configurations": configurations,
                          "runs": [{"role": "baseline", "command": "python -m pytest", "exit_code": 0}]}
                env = {"FAKE_REPORT_JSON": json.dumps(report)}
                if configurations:
                    env["FAKE_REPORT_PYTHON_ENVIRONMENT"] = "1"
                code, manifest = self.trial(env)
                self.assertEqual(code, 1)
                self.assertEqual(manifest["ddtest_python_runtime"]["observed_versions"], [platform.python_version()])
                self.assertEqual(manifest["ddtest_python_runtime"]["status"], "mismatch")
                self.assertEqual(manifest["failure_kind"], "evaluator_environment")
                self.assertEqual(manifest["failure_stage"], "ddtest_python_runtime")
                self.assertFalse(manifest["retry_recommended"])

    def test_pytest_partial_environment_identity_fails_the_trial(self):
        self.args.framework = "pytest"
        self.args.python = Path(sys.executable)
        for field, value in (("executable", sys.executable), ("prefix", sys.prefix)):
            with self.subTest(field=field):
                self.args.output = self.root / (field + "-evidence")
                report = {"summary": {"status": "INCOMPLETE"},
                          "preflight": {"runtime": platform.python_version(), field: value},
                          "runs": [{"role": "baseline", "command": "python -m pytest", "exit_code": 0}]}
                code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps(report)})
                self.assertEqual(code, 1)
                self.assertEqual(manifest["ddtest_python_runtime"]["status"], "not observed")
                self.assertEqual(manifest["failure_kind"], "evaluator_environment")

    def test_python_runtime_paths_keep_interpreter_aliases_and_venv_identity(self):
        selected = {"version": "3.12.8", "executable": "/trial/venv/bin/python", "prefix": "/trial/venv"}
        for executable, prefix, status in (("/trial/venv/bin/python3", "/trial/venv/", "matched"),
                ("/usr/bin/python3", "/usr", "mismatch"),
                ("/usr/bin/python3", "/trial/venv", "mismatch"),
                ("/trial/venv/bin/python", "/usr", "mismatch"),
                ("/trial/venv/bin/../../other/bin/python", "/trial/venv", "mismatch")):
            with self.subTest(executable=executable, prefix=prefix):
                report = {"preflight": {"runtime": "3.12.8", "executable": executable, "prefix": prefix},
                          "configurations": [{"preflight": {"verdict": {"status": "not exercised"}}}]}
                audit = run_eval.report_python_runtime(report, selected)
                self.assertEqual(audit["status"], status)
                self.assertEqual(audit["requested_prefix"], selected["prefix"])
                self.assertEqual(audit["observed_environments"][0]["executable"], executable)

    def test_pytest_runtime_mismatch_fails_the_trial(self):
        self.args.framework = "pytest"
        self.args.python = Path(sys.executable)
        code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps({"summary": {"status": "INCOMPLETE"}, "preflight": {"runtime": "0.0.0"}})})
        self.assertEqual(code, 1)
        self.assertEqual(manifest["failure_stage"], "ddtest_python_runtime")
        self.assertFalse(manifest["retry_recommended"])

    def test_pytest_execution_without_runtime_fails_the_trial(self):
        self.args.framework = "pytest"
        self.args.python = Path(sys.executable)
        handoff = "Local validation: PASSED."
        for name, preflight in (("missing", None), ("empty", {"runtime": ""})):
            with self.subTest(name=name):
                self.args.output = self.root / (name + "-runtime-evidence")
                report = {"summary": {"status": "PASSED", "local_validation": "PASSED", "final_response": handoff},
                          "preflight": preflight,
                          "runs": [{"role": "baseline", "command": "python -m pytest", "exit_code": 0}],
                          "retained_execution": {"result": {"preflight": {"runtime": platform.python_version()}}}}
                code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps(report), "FAKE_RESPONSE": handoff})
                self.assertEqual(code, 1, manifest)
                self.assertEqual(manifest["agent_reporting"]["status"], "pass")
                self.assertEqual(manifest["ddtest_python_runtime"]["status"], "not observed")
                self.assertEqual(manifest["status"], "failed")
                self.assertEqual(manifest["failure_kind"], "evaluator_environment")
                self.assertEqual(manifest["failure_stage"], "ddtest_python_runtime")
                self.assertIn("missing", manifest["error"])
                self.assertFalse(manifest["retry_recommended"])

    def test_pytest_without_current_execution_does_not_require_runtime(self):
        self.args.framework = "pytest"
        self.args.python = Path(sys.executable)
        runs = [{"role": "baseline", "command": "python -m pytest", "exit_code": 0}]
        for name, evidence in (("not run", {}), ("check only", {"check_only": True, "runs": runs}),
                               ("historical", {"runs": runs, "last_execution": {"current": False}})):
            with self.subTest(name=name):
                self.args.output = self.root / (name.replace(" ", "-") + "-runtime-evidence")
                report = {"summary": {"status": "INCOMPLETE"}, **evidence,
                          "retained_execution": {"result": {"preflight": {"runtime": platform.python_version()}, "runs": runs}}}
                code, manifest = self.trial({"FAKE_REPORT_JSON": json.dumps(report)})
                self.assertEqual(code, 0, manifest)
                self.assertEqual(manifest["status"], "completed")
                self.assertEqual(manifest["ddtest_python_runtime"]["status"], "not observed")

    def test_python_selection_rejects_ambiguous_or_non_image_paths(self):
        for python, framework, node in ((Path("python"), "pytest", None), (Path("/usr/../bin/python"), "pytest", None),
                                        (Path(sys.executable), "jest", None), (Path(sys.executable), "pytest", Path("/usr/bin/node"))):
            with self.subTest(python=python, framework=framework, node=node):
                self.args.python, self.args.framework, self.args.node = python, framework, node
                with self.assertRaisesRegex(ValueError, "--python"):
                    run_eval.run(self.args)
                self.assertFalse(self.args.output.exists())

    def test_prepared_mode_is_recorded_before_restoration_can_fail(self):
        self.args.case_image = True
        self.args.ref = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=self.source, text=True).strip()
        with patch.object(run_eval.case_image, 'restore', side_effect=ValueError('synthetic restore failure')):
            code, manifest = self.trial()
        self.assertEqual(code, 1)
        self.assertIs(manifest['case_image'], True)
        self.assertFalse((Path(manifest['checkout']) / 'observed.json').exists())

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
                self.assertEqual(observed["dd_env"], ["DD_INSTRUMENTATION_TELEMETRY_ENABLED"])
                self.assertEqual(observed["environment"]["DD_INSTRUMENTATION_TELEMETRY_ENABLED"], "false")
                self.assertEqual(manifest["isolation"]["intake_guard"], "us1-defaults-v2")
                self.assertIs(manifest["case_image"], False)
                self.assertEqual(observed["npm_unknown_config"], [])
                for directory in observed["cache_environment"].values():
                    self.assertTrue(Path(directory).is_relative_to(checkout.parent / "caches"))
                    self.assertTrue(Path(directory).is_dir())
                private = manifest["private_caches"]
                pnpm_rc = Path(private["XDG_CONFIG_HOME"]) / "pnpm" / "rc"
                self.assertIn("store-dir=" + private["pnpm_store"], pnpm_rc.read_text())
                self.assertIn("cache-dir=" + private["pnpm_cache"], pnpm_rc.read_text())
                yaml = pnpm_rc.with_name("config.yaml").read_text().splitlines()
                self.assertEqual(json.loads(yaml[0].split(": ", 1)[1]), private["pnpm_store"])
                self.assertEqual(json.loads(yaml[1].split(": ", 1)[1]), private["pnpm_cache"])
                if agent == "claude":
                    self.assertEqual(observed["agent_environment"], manifest["agent_environment"])
                    self.assertEqual(observed["agent_environment"]["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"], "1")
                    self.assertEqual(observed["agent_environment"]["BASH_MAX_TIMEOUT_MS"], "30000")
                self.assertNotIn("--model", observed["argv"])
                # The model the CLI reports in its session event; Codex reports none.
                self.assertEqual(manifest["resolved_model"], "claude-fake-model[1m]" if agent == "claude" else None)
                self.assertNotIn("resolved_models", manifest)
                self.assertIsNone(manifest["ddtest_label"])
                # The folder the prompt names holds only the recording wrapper;
                # DDTEST_SELF_PATH makes ddtest's printed commands name it.
                wrapper = checkout.parent / "bin" / "ddtest"
                copied = checkout.parent / "harness" / "ddtest"
                self.assertEqual(copied.read_bytes(), self.binary.read_bytes())
                self.assertFalse(copied.is_symlink())
                self.assertEqual(sorted(wrapper.parent.iterdir()), [wrapper])
                self.assertIn(shlex.quote(str(copied)), wrapper.read_text())
                self.assertEqual(manifest["ddtest_self_path"], {
                    "mode": "DDTEST_SELF_PATH", "wrapper": str(wrapper), "real_binary": str(copied),
                    "printed_executable": str(wrapper), "wrapper_printed": True})
                self.assertEqual(observed["environment"]["DDTEST_SELF_PATH"], str(wrapper))
                activity = (self.args.output / "activity.jsonl").read_text()
                self.assertNotIn("PRIVATE_REASONING", activity)
                self.assertEqual((self.args.output / "final-response.md").read_text(), "Done\n")
                self.assertIn("ddtest onboard", activity)
                self.assertIn("Fake agent changed", (self.args.output / "changes.patch").read_text())
                self.assertIn("observed.json", (self.args.output / "status.txt").read_text())
        self.assertNotEqual(*checkouts)

    def test_builds_without_self_path_use_the_deleted_suffix(self):
        for name, extra, printed in (("legacy", {"FAKE_LEGACY_DDTEST": "1"}, True),
                                     ("stale", {"FAKE_STALE_DDTEST": "1"}, False)):
            with self.subTest(name=name):
                self.args.output = self.root / f"{name}-evidence"
                self.args.setup_command = ["sh -c 'echo \"setup:${DDTEST_SELF_PATH-unset}\"'"]
                code, manifest = self.trial(extra)
                self.assertEqual(code, 0, manifest)
                trial = Path(manifest["trial_directory"])
                wrapper, copied = trial / "bin" / "ddtest", trial / "bin" / "ddtest (deleted)"
                self.assertEqual(sorted(wrapper.parent.iterdir()), [wrapper, copied])
                self.assertEqual(copied.read_bytes(), self.binary.read_bytes())
                self.assertFalse((trial / "harness" / "ddtest").exists())
                self.assertIn(shlex.quote(str(copied)), wrapper.read_text())
                record = manifest["ddtest_self_path"]
                self.assertEqual((record["mode"], record["real_binary"], record["wrapper_printed"]),
                                 ("deleted suffix", str(copied), printed))
                self.assertEqual(record["printed_executable"], str(wrapper if printed else copied))
                observed = json.loads((Path(manifest["checkout"]) / "observed.json").read_text())
                self.assertNotIn("DDTEST_SELF_PATH", observed["environment"])
                setup = next(c for c in manifest["commands"] if c["argv"][:2] == ["sh", "-c"] and "setup:" in c["argv"][2])
                self.assertIn("setup:unset", setup["output_excerpt"])
                diagnostics = [json.loads(line) for line in (self.args.output / "activity.jsonl").read_text().splitlines()]
                self.assertEqual(any("not recorded" in event.get("text", "") for event in diagnostics), not printed)

    def test_printed_executable_reads_the_first_next_command(self):
        self.assertEqual(run_eval.printed_executable('noise\n{"code":"x"}\n{"next_command":["/trial/bin/ddtest","testdrive"]}\n'),
                         "/trial/bin/ddtest")
        for output in ("", "not json", '{"next_command":[]}', '["/trial/bin/ddtest"]', '{"next_command":[1]}'):
            self.assertIsNone(run_eval.printed_executable(output))

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
            "requested": str(selected), "executable": str(selected.resolve()),
            "version": "v24.18.0", "codex_login_shell": False})
        setup = next(c for c in manifest["commands"] if c["argv"] == ["node", "--version"])
        self.assertEqual(setup["output_excerpt"], "v24.18.0\n")
        checkout = Path(manifest["checkout"])
        self.assertEqual(json.loads((checkout / "agent-runtime.json").read_text()), {"version":"v24.18.0", "login":False})
        observed = json.loads((checkout / "observed.json").read_text())
        self.assertEqual(observed["prompt"], f"Use ddtest in {checkout.parent / 'bin'} and onboard this repository to test-optimization.")
        self.assertNotIn("--model", observed["argv"])

    def test_ruby_runtime_checks_every_configuration(self):
        self.assertEqual(run_eval.report_preflight_runtime({}, "3.3.11")["status"], "not observed")
        self.assertEqual(run_eval.report_preflight_runtime({"preflight": {"runtime": "3.3.11"}}, "3.3.11")["status"], "matched")
        report = {"preflight": {"runtime": "3.3.11"}, "configurations": [{"preflight": {"runtime": "3.4.9"}}, {"preflight": None}]}
        self.assertEqual(run_eval.report_preflight_runtime(report, "3.3.11")["status"], "mismatch")
        # The legacy key was never written by ddtest; it must not count.
        self.assertEqual(run_eval.report_preflight_runtime({"preflight": {"ruby": "3.3.11"}}, "3.3.11")["status"], "not observed")

    def test_short_owned_state_path_accepts_real_watchman_socket(self):
        code, manifest = self.trial()
        self.assertEqual(code, 0)
        trial = Path(manifest["trial_directory"])
        self.assertTrue(trial.name.startswith("dde-"))
        self.assertEqual(trial.parent, trial_root())
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
        env["npm_config_http_proxy"] = "http://127.0.0.1:9"
        for command, expected in (([npm,"config","get","cache"], private["npm_config_cache"]),
                                  ([pnpm,"config","get","store-dir"], private["pnpm_store"]),
                                  ([pnpm,"config","get","cache-dir"], private["pnpm_cache"])):
            result = subprocess.run(command, cwd=manifest["checkout"], env=env, capture_output=True, text=True, check=True, timeout=30)
            self.assertEqual(result.stdout.strip(), expected)
            for key in ("store-dir", "cache-dir"):
                self.assertNotIn(f'Unknown env config "{key}"', result.stderr)
        store = subprocess.check_output([pnpm,"store","path"], cwd=manifest["checkout"], env=env, text=True, timeout=30).strip()
        self.assertTrue(Path(store).is_relative_to(private["pnpm_store"]))
        (self.root / "package.json").write_text('{"private":true}\n')
        nested = subprocess.run([pnpm,"exec",npm,"config","get","cache"], cwd=self.root, env=env,
                                capture_output=True, text=True, check=True, timeout=30)
        self.assertEqual(nested.stdout.strip(), private["npm_config_cache"])
        for key in ("store-dir", "cache-dir"):
            self.assertNotIn(f'Unknown env config "{key}"', nested.stderr)

    def test_ruby_scratch_uses_container_tmpfs_and_keeps_reports_in_checkout(self):
        ruby = self.root / "ruby"
        ruby.write_text("#!/usr/bin/env python3\nimport json\nprint(json.dumps({'executable':'/usr/local/bin/ruby','version':'3.3.11'}))\n")
        ruby.chmod(0o755)
        self.args.framework = "minitest"
        code, manifest = self.trial()
        self.assertEqual(code, 0)
        self.assertEqual(manifest["private_caches"]["TMPDIR"], "/tmp")
        observed = json.loads((Path(manifest["checkout"]) / "observed.json").read_text())
        self.assertEqual(observed["cache_environment"]["TMPDIR"], "/tmp")
        self.assertTrue((self.args.output / "changes.patch").is_file())
        self.assertEqual(manifest["isolation"]["cleanup"], "removed")

    def test_java_records_the_jdk_and_uses_container_tmpfs(self):
        jdk = self.root / "jdk"
        (jdk / "bin").mkdir(parents=True)
        (jdk / "bin" / "java").write_text("#!/bin/sh\n")
        (jdk / "bin" / "java").chmod(0o755)
        (jdk / "release").write_text('IMPLEMENTOR="Eclipse Adoptium"\nJAVA_VERSION="17.0.20.1"\n')
        (self.root / "java").symlink_to(jdk / "bin" / "java")
        self.args.framework = "junit5"
        with patch.dict(os.environ):
            os.environ.pop("JAVA_HOME", None)
            code, manifest = self.trial()
        self.assertEqual(code, 0)
        self.assertEqual(manifest["java_runtime"], {"home": str(jdk.resolve()), "version": "17.0.20.1"})
        self.assertEqual(manifest["private_caches"]["TMPDIR"], "/tmp")
        self.assertEqual(manifest["ddtest_java_runtime"]["requested_version"], "17.0.20.1")

    def fake_java_tools(self):
        """A JDK, and a mvn that resolves its local repository as Maven does:
        from user.home, which is "?" (relative) without a passwd entry."""
        jdk = self.root / "jdk"
        (jdk / "bin").mkdir(parents=True)
        (jdk / "bin" / "java").write_text("#!/bin/sh\n")
        (jdk / "bin" / "java").chmod(0o755)
        (jdk / "release").write_text('JAVA_VERSION="21.0.5"\n')
        (self.root / "java").symlink_to(jdk / "bin" / "java")
        mvn = self.root / "mvn"
        mvn.write_text("#!/usr/bin/env python3\nimport os, pathlib, re\n"
                       "found = re.search(r'-Duser\\.home=(\\S+)', os.environ.get('MAVEN_OPTS', ''))\n"
                       "repository = pathlib.Path(found[1] if found else '?', '.m2', 'repository', 'junit')\n"
                       "repository.mkdir(parents=True)\n(repository / 'junit.pom').write_text('pom')\n")
        mvn.chmod(0o755)
        self.args.framework = "junit5"

    def test_java_builds_keep_their_user_homes_out_of_the_checkout(self):
        self.fake_java_tools()
        self.args.readiness_command = ["mvn -B -q dependency:go-offline"]
        with patch.dict(os.environ):
            os.environ.pop("JAVA_HOME", None)
            code, manifest = self.trial()
        self.assertEqual(code, 0, manifest.get("error"))
        home = Path(manifest["trial_directory"]) / "home"
        observed = json.loads((Path(manifest["checkout"]) / "observed.json").read_text())["environment"]
        self.assertEqual(observed["MAVEN_OPTS"], f"-Duser.home={home}")
        self.assertEqual(observed["GRADLE_USER_HOME"], str(home / ".gradle"))
        self.assertTrue((home / ".m2" / "repository" / "junit" / "junit.pom").is_file())
        self.assertFalse((Path(manifest["checkout"]) / "?").exists())
        self.assertEqual(manifest["private_caches"]["maven_local_repository"], str(home / ".m2" / "repository"))
        self.assertEqual(manifest["private_caches"]["GRADLE_USER_HOME"], str(home / ".gradle"))

    def test_other_frameworks_leave_java_build_options_unset(self):
        code, manifest = self.trial()
        self.assertEqual(code, 0)
        observed = json.loads((Path(manifest["checkout"]) / "observed.json").read_text())["environment"]
        self.assertNotIn("MAVEN_OPTS", observed)
        self.assertNotIn("GRADLE_USER_HOME", observed)
        self.assertNotIn("maven_local_repository", manifest["private_caches"])

    def test_readiness_that_changes_the_checkout_stops_before_the_agent(self):
        self.fake_java_tools()
        # A readiness check the runner does not configure, as with user.home "?".
        self.args.readiness_command = ["env MAVEN_OPTS= mvn -B -q dependency:go-offline"]
        with patch.dict(os.environ):
            os.environ.pop("JAVA_HOME", None)
            code, manifest = self.trial()
        self.assertNotEqual(code, 0)
        self.assertEqual(manifest["failure_stage"], "runtime_readiness")
        self.assertIn("readiness command changed the checkout", manifest["error"])
        self.assertFalse((Path(manifest["checkout"]) / "observed.json").exists())

    def test_framework_scope_reaches_agent_without_extra_instructions(self):
        self.args.framework = "playwright"
        code, manifest = self.trial()
        self.assertEqual(code, 0)
        checkout = Path(manifest["checkout"])
        observed = json.loads((checkout / "observed.json").read_text())
        self.assertEqual(observed["prompt"],
                         f"Use ddtest in {checkout.parent / 'bin'} and onboard this repository to test-optimization for playwright only.")
        self.assertEqual(manifest["framework_scope"], "playwright")

    def test_ddtest_label_is_recorded_beside_the_version(self):
        self.args.ddtest_label = "b7a9d2ba (go build, no -ldflags)"
        code, manifest = self.trial()
        self.assertEqual(code, 0)
        self.assertEqual(manifest["ddtest_label"], "b7a9d2ba (go build, no -ldflags)")
        self.assertIn("ddtest_version", manifest)

    def test_refused_agent_command_exits_nonzero(self):
        execute = FakeContainer.execute
        def refusing(container, argv, *rest):
            if argv and argv[0] in ("codex", "claude") and "--version" not in argv:
                raise ValueError("Environment entry 'X' cannot be forwarded to the container")
            return execute(container, argv, *rest)
        with patch.object(FakeContainer, "execute", refusing):
            code, manifest = self.trial()
        self.assertEqual(code, 1)
        self.assertEqual(manifest["status"], "failed")
        self.assertIn("cannot be forwarded", manifest["error"])

    def test_requested_pnpm_is_private_and_verified(self):
        npm = self.root / "npm"
        npm.write_text("#!/bin/sh\nprefix=$3\nmkdir -p \"$prefix/node_modules/.bin\"\n"
                       "printf '#!/bin/sh\\necho %s\\n' \"$FAKE_PNPM\" > \"$prefix/node_modules/.bin/pnpm\"\n"
                       "chmod +x \"$prefix/node_modules/.bin/pnpm\"\n")
        npm.chmod(0o755)
        self.args.pnpm = "10"
        code, manifest = self.trial({"FAKE_PNPM": "10.33.4"})
        self.assertEqual(code, 0)
        self.assertEqual(manifest["package_manager"], {"pnpm": "10.33.4", "requested": "10"})
        self.args.output = self.root / "mismatch"
        code, manifest = self.trial({"FAKE_PNPM": "12.14.0"})
        self.assertEqual(code, 1)
        self.assertEqual(manifest["failure_kind"], "evaluator_environment")
        self.assertNotIn("observed.json", os.listdir(manifest["checkout"]))

    def test_agent_memory_is_disabled_and_any_notes_are_recorded(self):
        self.assertEqual(run_eval.agent_environment("claude", 60)["CLAUDE_CODE_DISABLE_AUTO_MEMORY"], "1")
        code, manifest = self.trial({"FAKE_MEMORY": "1"})
        self.assertEqual(code, 0)
        self.assertEqual(manifest["agent_artifacts"], {"memory_files": [".claude/projects/-trial-repo/memory/note.md"]})

    def test_memory_scan_does_not_follow_symlinked_roots(self):
        home = self.root / "memory-home"
        outside = self.root / "outside" / "projects" / "-x" / "memory"
        outside.mkdir(parents=True)
        (outside / "host-note.md").write_text("host")
        home.mkdir()
        (home / ".claude").symlink_to(self.root / "outside", target_is_directory=True)
        self.assertEqual(run_eval.agent_memory_files(home), [])
        (home / ".claude").unlink()
        (home / ".claude").mkdir()
        (home / ".claude" / "projects").symlink_to(self.root / "outside" / "projects", target_is_directory=True)
        self.assertEqual(run_eval.agent_memory_files(home), [])

    def test_setup_failure_is_recorded_without_launching_agent(self):
        self.args.setup_command = [f"{shlex.quote(sys.executable)} -c 'import sys; sys.exit(7)'"]
        code, manifest = self.trial()
        self.assertEqual(code, 1)
        self.assertEqual(manifest["status"], "failed")
        self.assertEqual(manifest["commands"][-1]["exit_code"], 7)
        self.assertFalse((Path(manifest["checkout"]) / "observed.json").exists())

    def test_selected_python_setup_failures_preserve_durable_evidence(self):
        self.args.framework = "pytest"
        self.args.python = Path(sys.executable)
        execute = FakeContainer.execute
        for stage in ("venv", "dependencies"):
            with self.subTest(stage=stage):
                self.args.output = self.root / (stage + "-failure")
                self.args.setup_command = [shlex.join(["python", "-c", "import sys; sys.exit(7)"])]
                def fail_venv(container, argv, cwd, env, timeout, on_line, prompt, executor):
                    if argv[1:3] == ["-m", "venv"]:
                        self.addCleanup(shutil.rmtree, Path(env["VIRTUAL_ENV"]).parent, ignore_errors=True)
                        if stage == "venv":
                            on_line("venv creation failed")
                            return 7
                    return execute(container, argv, cwd, env, timeout, on_line, prompt, executor)
                with patch.object(FakeContainer, "execute", fail_venv):
                    code, manifest = self.trial()
                self.assertEqual(code, 1)
                self.assertEqual(manifest["status"], "failed")
                self.assertEqual(manifest["failure_kind"], "evaluator_environment")
                self.assertEqual(manifest["failure_stage"], "python_environment")
                self.assertFalse(manifest["retry_recommended"])
                self.assertIn("Command exited 7:", manifest["error"])
                self.assertEqual(manifest["commands"][-1]["exit_code"], 7)
                self.assertEqual(manifest["python_runtime"], {
                    "requested": sys.executable, "requested_version": platform.python_version()})
                self.assertNotIn("ddtest_python_runtime", manifest)
                self.assertNotIn("collection_errors", manifest)
                self.assertEqual(manifest["isolation"]["cleanup"], "removed")
                self.assertFalse((Path(manifest["checkout"]) / "observed.json").exists())
                for name in ("activity.jsonl", "changes.patch", "status.txt"):
                    self.assertTrue((self.args.output / name).is_file(), name)

    def test_dirty_setup_does_not_contaminate_trial(self):
        self.args.setup_command = [f"{shlex.quote(sys.executable)} -c 'open(\"setup-file\", \"w\").close()'"]
        code, manifest = self.trial()
        self.assertEqual(code, 1)
        self.assertIn("setup changed", manifest["error"])
        self.assertFalse((Path(manifest["checkout"]) / "observed.json").exists())

    def test_changes_patch_starts_after_setup_commits(self):
        # A catalog trim: remove a tracked file and commit it inside the trial,
        # as run_catalog.py does, before the agent starts.
        import run_catalog
        tracked = "skills/ddtest-onboarding-eval/catalog/cases.yaml"
        case = {"trims": {"required": {"reason": "test trim", "commands": [f"git rm -q {tracked}"]}}}
        self.args.setup_command = run_catalog.trim_commands(case)
        code, manifest = self.trial()
        self.assertEqual(code, 0, manifest.get("error"))
        checkout = Path(manifest["checkout"])
        self.assertFalse((checkout / tracked).exists())
        self.assertEqual(manifest["commit"], subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=self.source, text=True).strip())
        self.assertNotEqual(manifest["agent_baseline"], manifest["commit"])
        rev_parses = [entry for entry in manifest["commands"] if entry["argv"] == ["git", "rev-parse", "HEAD"]]
        self.assertEqual(len(rev_parses), 2)
        patch_text = (self.args.output / "changes.patch").read_text()
        self.assertIn("Fake agent changed", patch_text)
        self.assertNotIn(tracked, patch_text)
        self.assertNotIn(tracked, (self.args.output / "status.txt").read_text())

    def test_changes_patch_preserves_carriage_returns_through_capture(self):
        import trajectory_check
        agent = self.root / 'codex'
        text = agent.read_text().replace('Fake agent changed this tracked file.\\n',
                                         'Fake agent changed this tracked file.\\r\\n')
        agent.write_text(text)
        code, manifest = self.trial()
        self.assertEqual(code, 0, manifest.get('error'))
        raw = subprocess.check_output(['git', 'diff', '--no-ext-diff', '--no-textconv',
                                       manifest['agent_baseline'], '--'], cwd=manifest['checkout'])
        self.assertIn(b'Fake agent changed this tracked file.\r\n', raw)
        captured = (self.args.output / 'changes.patch').read_bytes()
        self.assertEqual(captured, run_eval.redact(raw.decode()).encode())
        grade = self.root / 'capture-grade'
        grade.mkdir()
        (grade / 'changes.patch').write_bytes(captured)
        (grade / 'status.txt').write_text(' M README.md\n')
        for prescription, expected in ((captured.replace(b'\r', b''), 'review'), (captured, 'pass')):
            (grade / 'ddtest-results.jsonl').write_text(json.dumps({'result': {'instruction': prescription.decode()}}) + '\n')
            self.assertEqual(trajectory_check.evaluate(grade)['status'], expected)

    def test_changes_patch_preserves_non_utf8_bytes_through_capture(self):
        import trajectory_check
        agent = self.root / 'codex'
        text = agent.read_text().replace('with pathlib.Path("README.md").open("a") as stream:',
                                         'with pathlib.Path("README.md").open("ab") as stream:')
        text = text.replace('stream.write("\\nFake agent changed this tracked file.\\n")',
                            'stream.write(b"\\nFake agent changed this tracked file.\\xff\\n")')
        agent.write_text(text)
        code, manifest = self.trial()
        self.assertEqual(code, 0, manifest.get('error'))
        raw = subprocess.check_output(['git', 'diff', '--no-ext-diff', '--no-textconv',
                                       manifest['agent_baseline'], '--'], cwd=manifest['checkout'])
        self.assertIn(b'\xff', raw)
        captured = (self.args.output / 'changes.patch').read_bytes()
        self.assertEqual(captured, raw)
        grade = self.root / 'capture-grade'
        grade.mkdir()
        (grade / 'changes.patch').write_bytes(captured)
        (grade / 'status.txt').write_text(' M README.md\n')
        (grade / 'ddtest-results.jsonl').write_text(json.dumps({'result': {'instruction': raw.decode(errors='replace')}}) + '\n')
        self.assertEqual(trajectory_check.evaluate(grade)['status'], 'review')

    def test_failed_setup_diffs_against_the_fetched_commit(self):
        self.args.setup_command = [f"{shlex.quote(sys.executable)} -c 'import sys; sys.exit(7)'"]
        code, manifest = self.trial()
        self.assertEqual(code, 1)
        self.assertNotIn("agent_baseline", manifest)
        self.assertTrue((self.args.output / "changes.patch").is_file())

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

    def test_plain_cli_transport_errors_are_classified_after_nonzero_exit(self):
        for agent in ("codex", "claude"):
            for message, expected in (("API Error: 503 service unavailable", "infrastructure"),
                                      ("Permission denied (publickey)", "agent")):
                with self.subTest(agent=agent, message=message):
                    self.args.agent = agent
                    self.args.output = self.root / f"{agent}-{expected}"
                    code, manifest = self.trial({"FAKE_PLAIN_ERROR": message})
                    self.assertEqual(code, 1)
                    self.assertEqual(manifest["failure_kind"], expected)
                    self.assertEqual(manifest["retry_recommended"], expected == "infrastructure")
                    self.assertEqual(manifest["commands"][-1]["exit_code"], 1)

    def test_long_command_output_is_bounded_without_losing_command(self):
        event = {"type": "item.completed", "item": {"type": "command_execution",
                 "command": "npm test", "aggregated_output": "x" * 100_000, "exit_code": 1}}
        result, = run_eval.public_events(event, "codex")
        self.assertEqual(result["command"], "npm test")
        self.assertEqual(result["exit_code"], 1)
        self.assertLess(len(result["output"]), run_eval.EXCERPT_LIMIT + 100)
        self.assertIn("characters omitted]", result["output"])


class FrameworkEnvironmentTests(unittest.TestCase):
    def test_long_matrix_timeout_is_scoped_and_bounded(self):
        for framework in ("cypress", "rspec", "minitest", "junit4", "junit5", "testng", "spock", "cucumber-java", "karate", "scalatest", "munit"):
            self.assertEqual(run_eval.agent_environment("claude", 7200, framework)["BASH_MAX_TIMEOUT_MS"], "3600000")
            self.assertEqual(run_eval.agent_environment("claude", 1800, framework)["BASH_MAX_TIMEOUT_MS"], "1800000")
            self.assertEqual(run_eval.agent_environment("claude", 30, framework)["BASH_MAX_TIMEOUT_MS"], "30000")
        self.assertEqual(run_eval.agent_environment("claude", 7200, "jest")["BASH_MAX_TIMEOUT_MS"], "600000")
        self.assertEqual(run_eval.agent_environment("codex", 7200, "cypress"), {})


class TrialReportTests(unittest.TestCase):
    """Finalization reads the report on the host without following any symlink trial code planted."""

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.host = self.root / "host"
        (self.host / ".testoptimization").mkdir(parents=True)
        (self.host / ".testoptimization" / "testdrive.json").write_text('{"secret": true}')
        self.trial = self.root / "dde-trial"
        self.checkout = self.trial / "repo"

    def test_reads_a_regular_report_and_none_when_absent(self):
        self.checkout.mkdir(parents=True)
        self.assertIsNone(run_eval.read_trial_report(self.trial, self.checkout, 100))
        (self.checkout / ".testoptimization").mkdir()
        (self.checkout / ".testoptimization" / "testdrive.json").write_text('{"summary": {}}')
        self.assertEqual(run_eval.read_trial_report(self.trial, self.checkout, 100), b'{"summary": {}}')
        self.assertEqual(len(run_eval.read_trial_report(self.trial, self.checkout, 4)), 5)

    def test_symlinks_anywhere_on_the_way_are_refused(self):
        for name in ("checkout", "folder", "report"):
            with self.subTest(name):
                shutil.rmtree(self.trial, ignore_errors=True)
                self.trial.mkdir()
                if name == "checkout":
                    self.checkout.symlink_to(self.host, target_is_directory=True)
                else:
                    self.checkout.mkdir()
                if name == "folder":
                    (self.checkout / ".testoptimization").symlink_to(self.host / ".testoptimization",
                                                                     target_is_directory=True)
                if name == "report":
                    (self.checkout / ".testoptimization").mkdir()
                    (self.checkout / ".testoptimization" / "testdrive.json").symlink_to(
                        self.host / ".testoptimization" / "testdrive.json")
                with self.assertRaisesRegex(ValueError, "Report path escapes owned trial storage: .* is a symlink"):
                    run_eval.read_trial_report(self.trial, self.checkout, 100)
        with self.assertRaisesRegex(ValueError, "escapes owned trial storage"):
            run_eval.read_trial_report(self.trial, self.host, 100)


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
                "summary": {"status": "INCOMPLETE", "local_validation":"PASSED", "final_response": self.compact_answer(), "selected_validation": {"status": "PASSED"},
                            "executed_validation": {"status": "PASSED", "passed": 5, "configurations": 5},
                            "configuration_coverage": {"discovered": 8, "executed": 5, "paired": 5,
                                                       "validated": 5, "blocked": 2, "not_exercised": 1, "incomplete": 0}}}

    def compact_answer(self):
        return ("Selected Cypress testdrive: PASSED.\nExecuted Cypress validations: PASSED (5/5).\n"
                "Onboarding validation: INCOMPLETE. Local validation: PASSED.\n"
                "Configurations: 5/8 validated; 5 executed with paired comparison; 2 blocked, 1 not exercised, 0 incomplete.\n"
                "Reporting: 96 test executions; 32 source-test identities.\n"
                "[HTML report](</customer/repo/.testoptimization/report.html>)\n"
                "[JSON evidence](</customer/repo/.testoptimization/testdrive.json>)\n")

    def test_canonical_handoff_allows_extra_prose_but_paraphrase_needs_review(self):
        answer = self.compact_answer()
        result = run_eval.reporting_verdict(answer + "\nSome owner setup is still required.\n", self.compact_report())
        self.assertEqual(result["status"], "pass")
        self.assertEqual(result["differences"], [])
        self.assertEqual(result["missing"], [])
        self.assertIn("independent technical evaluation", result["reason"])
        paraphrase = answer.replace("5/8 validated", "5 / 8 fully validated")
        self.assertEqual(run_eval.reporting_verdict(paraphrase,self.compact_report())["status"],"review")

    def test_canonical_handoff_in_code_formatting_is_unchanged(self):
        answer = self.compact_answer()
        code_spans = answer.replace("5/8 validated", "`5/8` validated").replace(
            "Local validation: PASSED.", "`Local validation: PASSED.`")
        lines = "".join(f"`{line}`\n" if line else "\n" for line in answer.splitlines())
        for quoted in (code_spans, lines, "final_response\n\n```text\n" + answer + "```\n", "```\n" + code_spans + "```\n"):
            with self.subTest(quoted=quoted):
                result = run_eval.reporting_verdict(quoted + "\nSome owner setup is still required.\n", self.compact_report())
                self.assertEqual(result["status"], "pass", result)
                self.assertTrue(result["canonical_handoff"])
                self.assertEqual(result["checks"], ["summary.final_response unchanged apart from code formatting"])
        # Backticks never excuse a changed character.
        for altered in (code_spans.replace("`5/8`", "`6/8`"), code_spans.replace("Local validation", "Local  validation"),
                        "```\n" + answer.replace("\n[HTML", "\n\n[HTML") + "```\n"):
            with self.subTest(altered=altered):
                result = run_eval.reporting_verdict(altered, self.compact_report())
                self.assertNotEqual(result["status"], "pass", result)
                self.assertFalse(result["canonical_handoff"])

    def test_plain_language_verdicts_are_checked_but_canonical_handoff_is_required(self):
        answer = self.compact_answer().replace("Onboarding validation: INCOMPLETE. Local validation: PASSED.",
            "Overall onboarding remains INCOMPLETE. Local validation passed: all controls matched.")
        result = run_eval.reporting_verdict(answer, self.compact_report())
        self.assertEqual(result["status"], "review", result)
        self.assertFalse(result["canonical_handoff"])
        for wrong in (answer.replace("remains INCOMPLETE", "remains complete"),
                      answer.replace("validation passed", "validation failed")):
            self.assertEqual(run_eval.reporting_verdict(wrong, self.compact_report())["status"], "fail")

    def test_local_verdict_excludes_following_explanation(self):
        answer = self.compact_answer().replace("Local validation: PASSED.",
            "Local validation: PASSED — compatibility and native outcomes match.")
        result = run_eval.reporting_verdict(answer, self.compact_report())
        self.assertEqual(result["status"], "review", result)
        wrong = answer + "\nLocal validation: FAILED — the run did not finish."
        self.assertEqual(run_eval.reporting_verdict(wrong, self.compact_report())["status"], "fail")

    def test_swapped_distinct_counts_and_scope_upgrades_fail(self):
        for original, wrong in (("96 test executions; 32 source-test identities", "32 test executions; 96 source-test identities"),
                                ("5/8 validated", "8/8 validated"),
                                ("5 executed with paired comparison", "8 executed with paired comparison"),
                                ("5 executed with paired comparison", "5 executed, 4 with paired comparison"),
                                ("PASSED (5/5)", "PASSED (8/8)"),
                                ("Onboarding validation: INCOMPLETE", "Onboarding validation: COMPLETE LOCALLY"),
                                ("Selected Cypress testdrive: PASSED", "Selected Cypress testdrive: INCOMPLETE")):
            with self.subTest(wrong=wrong):
                result = run_eval.reporting_verdict(self.compact_answer().replace(original, wrong), self.compact_report())
                self.assertEqual(result["status"], "fail")
                self.assertTrue(result["differences"])

    def test_report_links_must_name_the_checkout_root_report(self):
        # A run in a package below the root keeps its reports at the root.
        report = self.compact_report()
        report["working_directory"] = "/customer/repo/test"
        answer = self.compact_answer()
        self.assertEqual(run_eval.reporting_verdict(answer, report, "/customer/repo")["differences"], [])
        nested = answer.replace("/customer/repo/.testoptimization/", "/customer/repo/test/.testoptimization/")
        result = run_eval.reporting_verdict(nested, report, "/customer/repo")
        self.assertEqual(result["status"], "fail")
        self.assertTrue(any("report path" in difference for difference in result["differences"]), result)

    def test_ci_opt_outs_are_not_counted_as_unvalidated(self):
        report = self.compact_report()
        report["summary"]["configuration_coverage"].update(discovered=9, excluded=1)
        answer = self.compact_answer().replace("5/8 validated;", "5/8 validated (1 more excluded from instrumentation in CI);")
        self.assertEqual(run_eval.reporting_verdict(answer, report)["differences"], [])
        for wrong in (answer.replace("5/8 validated", "5/9 validated"), answer.replace("(1 more excluded", "(2 more excluded")):
            with self.subTest(wrong=wrong):
                self.assertEqual(run_eval.reporting_verdict(wrong, report)["status"], "fail")

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
        report["summary"]["final_response"] = answer
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

    def test_completion_claim_keeps_the_agents_wording(self):
        result = run_eval.reporting_verdict("Onboarding is complete.", {"summary": {"status": "INCOMPLETE"}})
        self.assertEqual(result["status"], "fail")
        self.assertEqual(result["differences"], ["onboarding verdict: stated COMPLETE (a completion claim), report says INCOMPLETE."])
        self.assertEqual(run_eval.reporting_verdict("Onboarding is complete.", {"summary": {"status": "COMPLETE LOCALLY"}})["differences"], [])

    def test_negated_reported_status_and_other_verdicts_contradict_incomplete(self):
        for claim in ("Status is not incomplete.", "Onboarding isn't incomplete.", "Onboarding validation: PASSED.",
                      "Onboarding validation: FAILED.", "Setup did not fail; onboarding is complete.",
                      "I did not encounter errors, onboarding is complete.", "CI was not run, but onboarding is complete."):
            with self.subTest(claim=claim):
                result = run_eval.reporting_verdict(self.compact_answer() + "\n" + claim, self.compact_report())
                self.assertEqual(result["status"], "fail")
                self.assertTrue(result["differences"])
        for status in ("PASSED", "COMPLETE LOCALLY", "FAILED"):
            with self.subTest(status=status):
                report = {"summary": {"status": status}}
                self.assertEqual(run_eval.reporting_verdict("Onboarding validation: " + status + ".", report)["status"], "review")
        self.assertEqual(run_eval.reporting_verdict("Onboarding validation: PASSED.", {"summary": {"status": "COMPLETE LOCALLY"}})["status"], "review")

    def test_repeated_labeled_facts_must_all_agree(self):
        for claim in (
                "Reporting: 999 test executions; 888 source-test identities.",
                "Reporting: 96 test executions; 999 source-test identities.",
                "Configurations: 8/8 validated; 8 executed with paired comparison; 0 blocked, 0 not exercised, 0 incomplete.",
                "Executed Cypress validations: PASSED (8/8).",
                "Selected Cypress testdrive: INCOMPLETE."):
            with self.subTest(claim=claim):
                result = run_eval.reporting_verdict(self.compact_answer()+"\n"+claim, self.compact_report())
                self.assertEqual(result["status"], "fail")
        repeated = self.compact_answer().replace("5/8 validated", "5 / 8 fully validated")
        self.assertEqual(run_eval.reporting_verdict(self.compact_answer()+"\n"+repeated, self.compact_report())["status"], "pass")

    def test_local_validation_is_independent_of_overall_and_selected_verdict(self):
        for status in ("PASSED", "INCOMPLETE", "NOT EXERCISED", "HISTORICAL ONLY"):
            with self.subTest(status=status):
                report = self.compact_report()
                report["summary"]["local_validation"] = status
                answer = self.compact_answer().replace("Local validation: PASSED", "Local validation: "+status)
                report["summary"]["final_response"] = answer
                self.assertEqual(run_eval.reporting_verdict(answer,report)["status"],"pass")
                self.assertEqual(run_eval.reporting_verdict(answer+"\nLocal validation: FAILED.",report)["status"],"fail")
        report = self.compact_report()
        report["summary"]["local_validation"] = "INCOMPLETE"
        self.assertEqual(run_eval.reporting_verdict(self.compact_answer(),report)["status"],"fail")

    def test_plain_success_claims_on_a_report_that_is_not_complete_need_review(self):
        # B08 opened with "Onboarded." on an INCOMPLETE report.
        for claim in ("Onboarded. ddtest reports Python has no coordinator.", "Done!", "All set.", "Success.",
                      "Fully onboarded — see below.", "The repository is now onboarded to Test Optimization.",
                      "This project has been set up.", "Test Optimization is set up in CI.",
                      "I've onboarded the repository.", "We have finished onboarding.", "Successfully set up Test Optimization.",
                      # A completed action before the claim is not a condition on it.
                      "After updating Jenkinsfile, I successfully onboarded the repository.",
                      "I onboarded the repository after I updated the Jenkinsfile.",
                      # A condition must qualify the claim: not the reader's needs or another clause.
                      "If you need anything else, everything is done.", "Everything is done if you need anything else.",
                      "When you're ready, the repository is onboarded.", "If needed, everything is done.",
                      "Everything is done; CI reruns once a day.", "Everything is done, and CI runs when tests change.",
                      "Everything is done: when CI runs, it reports to Datadog.", "Everything is done — if CI fails, rerun it."):
            with self.subTest(claim=claim):
                result = run_eval.reporting_verdict(self.compact_answer() + "\n" + claim, self.compact_report())
                self.assertEqual(result["status"], "review")
                self.assertEqual(len(result["unsupported"]), 1, result)
                self.assertIn("while the report status is INCOMPLETE", result["unsupported"][0])
                for status in ("PASSED", "FAILED"):
                    report = {"summary": {"status": status}}
                    self.assertTrue(run_eval.reporting_verdict(claim, report)["unsupported"], status)
                complete = {"summary": {"status": "COMPLETE LOCALLY"}}
                self.assertEqual(run_eval.reporting_verdict(claim, complete)["unsupported"], [])
        self.assertEqual(run_eval.reporting_verdict("Onboarded.", self.compact_report())["unsupported"],
                         ['Success claim "onboarded" while the report status is INCOMPLETE.'])

    def test_quoted_negated_conditional_or_partial_completion_is_not_a_success_claim(self):
        report = self.compact_report()
        report["blocking_checks"] = [{"message": "Setup complete for the release job; the test job needs review."}]
        for text in ("ddtest printed:\n> Onboarded.\n", "It printed `Done.`", "```\nDone.\n```", 'The result says "Onboarded."',
                     "Setup complete for the release job; the test job needs review.",
                     "Not onboarded yet.", "I have not onboarded the repository.", "Workflow edits are complete.",
                     "Once CI runs, the repository is onboarded.", "Test Optimization is set up when the secret is added.",
                     "After the first CI run, the repository will be onboarded.",
                     "If the secret is added, everything is done.", "Everything is done once you add the secret.",
                     "Once CI runs and the secret is added, the repository is onboarded.",
                     "CI is set up to skip forks.", "Onboarded, except the testwheel job."):
            with self.subTest(text=text):
                result = run_eval.reporting_verdict(self.compact_answer() + "\n" + text, report)
                self.assertEqual(result["unsupported"], [])
                self.assertEqual(result["status"], "pass")


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
