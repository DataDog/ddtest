#!/usr/bin/env python3
"""Run an uncoached onboarding trial; leave grading to the evaluator."""

import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from urllib.parse import unquote


EXCERPT_LIMIT = 4000
JAVA_FRAMEWORKS = ("junit4", "junit5", "testng", "spock", "cucumber-java", "karate", "scalatest", "munit")


def excerpt(value):
    text = value if isinstance(value, str) else json.dumps(value)
    return text if len(text) <= EXCERPT_LIMIT else "[truncated]\n" + text[-EXCERPT_LIMIT:]


def public_events(event, agent):
    """Keep public messages, commands and outcomes; never persist reasoning."""
    kind = event.get("type")
    if agent == "codex":
        item = event.get("item", {})
        if kind == "thread.started":
            yield {"type": "session", "id": event.get("thread_id")}
        elif kind in ("item.started", "item.completed"):
            if item.get("type") == "command_execution":
                yield {"type": kind, "id": item.get("id"),
                       "command": item.get("command"), "exit_code": item.get("exit_code"),
                       "output": excerpt(item.get("aggregated_output", ""))}
            elif kind == "item.completed" and item.get("type") == "agent_message":
                yield {"type": "message", "text": excerpt(item.get("text", ""))}
        elif kind in ("error", "turn.failed"):
            yield {"type": "agent_error", "error": excerpt(event.get("error", event.get("message", "")))}
        elif kind == "turn.completed":
            yield {"type": "result"}
    else:
        if kind == "system" and event.get("subtype") == "init":
            yield {"type": "session", "id": event.get("session_id"), "model": event.get("model")}
        elif kind == "assistant":
            for block in event.get("message", {}).get("content", []):
                if block.get("type") == "text":
                    yield {"type": "message", "text": excerpt(block.get("text", ""))}
                elif block.get("type") == "tool_use" and block.get("name") == "Bash":
                    yield {"type": "command", "id": block.get("id"),
                           "command": block.get("input", {}).get("command")}
        elif kind == "user":
            content = event.get("message", {}).get("content", [])
            for block in content if isinstance(content, list) else []:
                if block.get("type") == "tool_result":
                    yield {"type": "tool_result", "id": block.get("tool_use_id"),
                           "is_error": block.get("is_error", False),
                           "output": excerpt(block.get("content", ""))}
        elif kind == "result":
            yield {"type": "agent_error" if event.get("is_error") else "result",
                   "text": excerpt(event.get("result", event.get("errors", ""))),
                   "permission_denials": excerpt(event.get("permission_denials", []))}


class BackgroundCommands:
    """Track structured Claude Bash tasks; a final message cannot complete them."""

    def __init__(self):
        self.bash = set()
        self.pending = set()
        self.reads = {}

    def observe(self, event):
        if event.get("type") == "assistant":
            for block in event.get("message", {}).get("content", []):
                if block.get("type") != "tool_use":
                    continue
                if block.get("name") == "Bash":
                    self.bash.add(block["id"])
                elif block.get("name") == "TaskOutput":
                    self.reads[block["id"]] = block.get("input", {}).get("task_id")
        elif event.get("type") == "system" and event.get("subtype") == "task_notification":
            if event.get("status") in ("completed", "failed", "stopped"):
                self.pending.discard(event.get("task_id"))
        elif event.get("type") == "user":
            content = event.get("message", {}).get("content", [])
            if not isinstance(content, list):
                content = [{"type": "text", "text": content}]
            for block in content:
                text = block.get("content", block.get("text", ""))
                if not isinstance(text, str):
                    text = json.dumps(text)
                tool_id = block.get("tool_use_id")
                if tool_id in self.bash:
                    self.bash.remove(tool_id)
                    match = re.search(r"background (?:with ID: |\(ID: )([\w-]+)", text)
                    if match:
                        self.pending.add(match[1])
                # TaskOutput and task notifications identify the terminal state.
                task = re.search(r"<task[_-]id>([\w-]+)</task[_-]id>", text)
                status = re.search(r"<status>(completed|failed|stopped)</status>", text)
                if status and (task or tool_id in self.reads):
                    self.pending.discard(task[1] if task else self.reads[tool_id])

    def unfinished(self):
        return sorted(self.bash | self.pending)


def agent_environment(agent, timeout, framework=None):
    if agent != "claude":
        return {}
    # Print mode cannot resume a conversation after its final result. Keep Bash
    # synchronous and allow long paired runs without the default 120s handoff.
    maximum = 3600000 if framework == "cypress" else 600000
    bash_timeout = str(min(maximum, int(timeout * 1000)))
    return {"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
            "BASH_DEFAULT_TIMEOUT_MS": bash_timeout,
            "BASH_MAX_TIMEOUT_MS": bash_timeout}


def failure_kind(messages, timed_out=False):
    """Only recognized transport/service interruptions qualify for a fresh retry."""
    text = "\n".join(messages).lower()
    if timed_out or any(marker in text for marker in (
            "response stalled mid-stream", "overloaded_error", "api error: 529",
            "api error: 503", "stream disconnected", "connection reset by peer")):
        return "infrastructure"
    return "agent"


CLI_STARTUP_TIMEOUT = 15


def reporting_verdict(text, report):
    """Compare labeled facts, not prose quality or byte-identical handoff text."""
    report = report if isinstance(report, dict) else {}
    summary = report.get("summary") if isinstance(report.get("summary"), dict) else {}
    plain = re.sub(r"[*_`#]", "", text).lower()
    checks, differences, missing = [], [], []

    def check(label, expected, found):
        if found is None:
            missing.append(label)
        elif found != expected:
            differences.append(f"{label}: stated {found}, report says {expected}.")
        else:
            checks.append(label)

    def groups(pattern, value=plain):
        match = re.search(pattern, value)
        return match.groups() if match else None

    def numbers(pattern, value=plain):
        found = groups(pattern, value)
        return tuple(int(item.replace(",", "")) for item in found) if found else None

    status = summary.get("status")
    if status == "INCOMPLETE" and re.search(r"\bonboarding(?:\s+validation)?\s*(?::|is|was|has been)?\s*(?:complete locally|complete|completed|done|successful)\b", plain):
        differences.append("Agent claimed completed onboarding despite INCOMPLETE validation.")
    stated = groups(r"\bonboarding(?:\s+validation)?\s*(?::|is|was|has been)?\s*(incomplete|complete locally|complete|completed|done|successful)\b")
    if status:
        value = stated[0].upper() if stated else None
        if value in ("COMPLETE", "COMPLETED", "DONE", "SUCCESSFUL"):
            value = "COMPLETE LOCALLY"
        check("onboarding verdict", status, value)
    else:
        missing.append("JSON onboarding verdict")
    for key, label in (("selected_validation", "selected"), ("executed_validation", "executed")):
        expected = summary.get(key)
        if not isinstance(expected, dict) or "status" not in expected:
            continue
        found = groups(r"\b" + label + r"[^:\n]*\b(?:testdrive|validations?):\s*(passed|incomplete|not exercised|failed)\b")
        check(label + " verdict", expected["status"], found[0].upper() if found else None)
        if key == "executed_validation" and "passed" in expected and "configurations" in expected:
            found = numbers(r"\bexecuted[^:\n]*validations?:\s*[\w ]+\s*\((\d+)\s*/\s*(\d+)\)")
            check("executed configuration ratio", (expected["passed"], expected["configurations"]), found)
    coverage = summary.get("configuration_coverage")
    coverage_line = groups(r"(?m)^configurations:\s*(.*)$")
    if isinstance(coverage, dict) and all(key in coverage for key in ("validated", "discovered", "executed", "paired", "blocked", "not_exercised", "incomplete")):
        line = coverage_line[0] if coverage_line else ""
        check("validated/discovered configurations", (coverage["validated"], coverage["discovered"]),
              numbers(r"(\d+)\s*/\s*(\d+)\s+(?:fully\s+)?validated", line))
        for key, label in (("executed", "executed"), ("paired", "paired"), ("blocked", "blocked"),
                           ("not_exercised", "not exercised"), ("incomplete", "incomplete")):
            found = numbers(r"(?:^|[;,])\s*(\d+)\s+" + label + r"\b", line)
            check(label + " configurations", (coverage[key],), found)
    else:
        missing.append("JSON configuration coverage")
    counts = report.get("reporting_counts")
    reporting_line = groups(r"(?m)^reporting:\s*(.*)$")
    if isinstance(counts, dict) and "test_executions" in counts and not report.get("check_only"):
        line = reporting_line[0] if reporting_line else ""
        check("reported test executions", (counts["test_executions"],), numbers(r"(\d[\d,]*)\s+(?:reported\s+)?test executions?\b", line))
        expected = counts.get("source_test_identities")
        found = numbers(r"(\d[\d,]*)\s+source[- ]test identities\b", line)
        if found is None:
            found = numbers(r"(\d[\d,]*)\s+(?:unique|distinct) tests\b")
        check("source-test identities", (expected,) if expected is not None else "unavailable",
              found if found is not None else "unavailable" if "source-test identities unavailable" in line else None)
    else:
        missing.append("current JSON reporting counts")
    links = [unquote(a or b) for a, b in re.findall(r"\[[^\]]+\]\((?:<([^>]+)>|([^\s)]+))\)", text)]
    working_directory = report.get("working_directory")
    if working_directory and Path(working_directory).is_absolute():
        expected = ["testdrive.json"]
        if report.get("html_report_current") or "report.html" in summary.get("final_response", ""):
            expected.append("report.html")
        for name in expected:
            path = str(Path(working_directory) / ".testoptimization" / name)
            actual = [link for link in links if Path(link).name == name]
            if any(Path(link).is_absolute() and link != path for link in actual):
                differences.append(f"{name} link does not reference the current report path.")
            elif path not in actual:
                missing.append(name + " absolute Markdown link")
            else:
                checks.append(name + " link")
    else:
        missing.append("JSON report directory and evidence links")
    outcome = "fail" if differences else "review" if missing else "pass"
    reason = "Labeled facts contradict JSON." if differences else "Missing or ambiguous facts need independent review." if missing else "Labeled verdicts, scope, counts and report links match JSON; independent technical evaluation is still required."
    return {"status": outcome, "reason": reason, "checks": checks,
            "differences": differences[:10], "missing": missing[:10]}


def report_runtime(report, requested=None):
    """Audit the final command runtime; bundled framework runtimes are separate."""
    report = report if isinstance(report, dict) else {}
    observed = {}

    def record(source, value):
        if isinstance(value, str) and value:
            observed.setdefault(value.removeprefix("v"), []).append(source)

    record("summary.local_node", report.get("summary", {}).get("local_node"))
    record("preflight.node", (report.get("preflight") or {}).get("node"))
    for index, configuration in enumerate(report.get("configurations") or []):
        record(f"configurations[{index}].preflight.node", (configuration.get("preflight") or {}).get("node"))
    framework_nodes = sorted({run["framework_node"] for run in report.get("runs") or [] if run.get("framework_node")})
    expected = requested.removeprefix("v") if requested else None
    status = "not observed"
    if observed:
        status = "observed" if expected is None else "matched" if set(observed) == {expected} else "mismatch"
    return {"status": status, "requested_version": requested,
            "observed": [{"version": version, "sources": sources[:20], "source_count": len(sources)}
                         for version, sources in sorted(observed.items())],
            "framework_owned_nodes": framework_nodes,
            "scope": "Final ddtest report only; framework-owned Node runtimes and historical execution have separate scopes."}


def agent_command(agent, model):
    if agent == "codex":
        command = ["codex", "exec", "--approve-for-me", "--json"]
    else:
        command = ["claude", "--print", "--permission-mode", "auto",
                   "--output-format", "stream-json", "--verbose"]
    if model:
        command.extend(["--model", model])
    if agent == "codex":
        command.append("-")
    return command


def execute(command, cwd, env, timeout, on_line, prompt=None):
    """Stream a process and kill its process group on timeout or interruption."""
    process = subprocess.Popen(command, cwd=cwd, env=env, text=True,
                               errors="replace", stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, stdin=subprocess.PIPE,
                               start_new_session=True)
    timed_out = threading.Event()

    def kill_group():
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        except PermissionError:
            # A finishing process group may briefly reject signals on macOS.
            # Accept cleanup only after observing that the group is gone.
            for _ in range(20):
                try:
                    os.killpg(process.pid, 0)
                except ProcessLookupError:
                    return
                except PermissionError:
                    pass
                time.sleep(0.05)
            raise

    def expire():
        timed_out.set()
        kill_group()

    timer = threading.Timer(timeout, expire)
    timer.start()
    try:
        if prompt:
            process.stdin.write(prompt)
        process.stdin.close()
        for line in process.stdout:
            on_line(line.rstrip("\n"))
        process.wait()
        if timed_out.is_set():
            raise TimeoutError(f"Command exceeded {timeout}s: {shlex.join(command)}")
        return process.returncode
    finally:
        timer.cancel()
        try:
            kill_group()
            process.wait()
        finally:
            process.stdout.close()
            process.stdin.close()


def run(args):
    binary = args.ddtest.resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError("--ddtest must be an executable file")
    node = getattr(args, "node", None)
    java_home = getattr(args, "java_home", None)
    if java_home is not None:
        if not java_home.is_absolute() or not (java_home / "bin" / "java").is_file():
            raise ValueError("--java-home must identify an absolute JDK directory")
        java_home = java_home.resolve(strict=True)
    if node is not None:
        if args.agent != "codex":
            raise ValueError("--node currently requires --agent codex")
        if not node.is_absolute():
            raise ValueError("--node must be an absolute executable path")
        node = node.resolve(strict=True)
        if not node.is_file() or not os.access(node, os.X_OK):
            raise ValueError("--node must be an executable file")
    setup_commands = [shlex.split(command) for command in args.setup_command]
    if any(not command for command in setup_commands):
        raise ValueError("--setup-command must not be empty")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    # macOS's per-user TMPDIR is too deep for Watchman's Unix socket name.
    # A unique short root still owns every cache/configuration and its cleanup.
    trial = Path(tempfile.mkdtemp(prefix="dde-", dir="/tmp" if sys.platform == "darwin" else None)).resolve()
    checkout = trial / "repo"
    bin_dir = trial / "bin"
    bin_dir.mkdir()
    shutil.copy2(binary, bin_dir / "ddtest")
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("DD_", "DATADOG_")) and key not in ("NODE_OPTIONS", "NODE_PATH")
           and key.lower() not in ("npm_config_store_dir", "npm_config_cache_dir")}
    if java_home is not None:
        for key in ("MAVEN_OPTS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "BASH_ENV", "ENV"):
            env.pop(key, None)
    for key, name in (("npm_config_cache", "npm"), ("YARN_CACHE_FOLDER", "yarn"),
                      ("XDG_CACHE_HOME", "xdg-cache"), ("XDG_DATA_HOME", "xdg-data"),
                      ("XDG_STATE_HOME", "state"), ("XDG_CONFIG_HOME", "xdg-config"),
                      ("PNPM_HOME", "pnpm-home"), ("TMPDIR", "tmp"),
                      ("CYPRESS_CACHE_FOLDER", "cypress"),
                      ("PLAYWRIGHT_BROWSERS_PATH", "playwright"),
                      ("PUPPETEER_CACHE_DIR", "puppeteer")):
        directory = trial / name if key == "XDG_STATE_HOME" else trial / "caches" / name
        directory.mkdir(parents=True, exist_ok=True)
        env[key] = str(directory)
    # pnpm's own configuration is not inherited by npm commands in test scripts.
    pnpm_config = Path(env["XDG_CONFIG_HOME"]) / "pnpm" / "rc"
    pnpm_config.parent.mkdir()
    pnpm_store, pnpm_cache = trial / "caches" / "pnpm-store", trial / "caches" / "pnpm"
    pnpm_store.mkdir()
    pnpm_cache.mkdir()
    pnpm_config.write_text(f"store-dir={pnpm_store}\ncache-dir={pnpm_cache}\n")
    if java_home is not None:
        maven_cache = trial / "caches" / "maven"
        maven_cache.mkdir()
        env["JAVA_HOME"] = str(java_home)
        env["MAVEN_OPTS"] = "-Dmaven.repo.local=" + str(maven_cache)
        env["PATH"] = str(java_home / "bin") + os.pathsep + env.get("PATH", "")
    if node is not None or java_home is not None:
        if node is not None:
            (bin_dir / "node").symlink_to(node)
            env["PATH"] = str(node.parent) + os.pathsep + env.get("PATH", "")
        zdotdir = trial / "zsh"
        zdotdir.mkdir()
        env["ZDOTDIR"] = str(zdotdir)
    env["PATH"] = str(bin_dir) + os.pathsep + env.get("PATH", "")
    agent_argv = agent_command(args.agent, args.model)
    if (node is not None or java_home is not None) and args.agent == "codex":
        # .zshenv also runs in non-login shells. Isolate it and disable shell
        # snapshots so neither host startup files nor an old snapshot reset PATH.
        agent_argv[2:2] = ["-c", "allow_login_shell=false", "-c",
                           "features.shell_snapshot=false", "-c",
                           "shell_environment_policy.set.PATH=" + json.dumps(env["PATH"]), "-c",
                           "shell_environment_policy.set.ZDOTDIR=" + json.dumps(env["ZDOTDIR"])]
        if java_home is not None:
            agent_argv[2:2] = ["--ignore-user-config", "-c", "shell_environment_policy.set.JAVA_HOME=" + json.dumps(env["JAVA_HOME"]),
                              "-c", "shell_environment_policy.set.MAVEN_OPTS=" + json.dumps(env["MAVEN_OPTS"])]
    if java_home is not None and args.agent == "claude":
        agent_argv[1:1] = ["--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--setting-sources", "project", "--no-chrome"]
    framework = getattr(args, "framework", None)
    scope = f" for {framework} only" if framework else ""
    prompt = f"Use ddtest in {bin_dir} and onboard this repository to test-optimization{scope}."
    manifest = {"repository": args.repo, "requested_ref": args.ref,
                "trial_directory": str(trial), "checkout": str(checkout),
                "ddtest_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "agent": args.agent, "requested_model": args.model, "prompt": prompt,
                "framework_scope": framework,
                "started_at": time.time(), "commands": [], "status": "preparing",
                "evaluation": "pending", "ci_execution": "not exercised"}
    manifest["private_caches"] = {key: env[key] for key in (
        "npm_config_cache", "YARN_CACHE_FOLDER", "XDG_CACHE_HOME", "XDG_DATA_HOME",
        "XDG_STATE_HOME", "XDG_CONFIG_HOME", "PNPM_HOME", "TMPDIR",
        "CYPRESS_CACHE_FOLDER", "PLAYWRIGHT_BROWSERS_PATH", "PUPPETEER_CACHE_DIR")}
    manifest["private_caches"]["pnpm_store"] = str(pnpm_store)
    manifest["private_caches"]["pnpm_cache"] = str(pnpm_cache)
    if node is not None or java_home is not None:
        manifest["shell_isolation"] = {"zdotdir": env["ZDOTDIR"], "login_shell": False, "shell_snapshot": False}
    if java_home is not None:
        manifest["private_caches"]["maven"] = str(maven_cache)
        manifest["java_environment"] = {key: env[key] for key in ("JAVA_HOME", "MAVEN_OPTS", "PATH")}
        manifest["agent_configuration"] = "process-local CLI configuration: Codex ignores user config; Claude loads project settings only; no inherited MCP servers"
    activity = (output / "activity.jsonl").open("w")
    agent_failed = False
    agent_completed = False
    agent_errors = []
    agent_timed_out = False
    final_response = ""
    background = BackgroundCommands()
    overrides = agent_environment(args.agent, args.timeout, framework)
    manifest["agent_environment"] = overrides

    def emit(event):
        nonlocal agent_failed, agent_completed
        if event["type"] == "agent_error":
            agent_failed = True
            agent_errors.append(str(event.get("error", event.get("text", ""))))
        if event["type"] == "result":
            agent_completed = True
        if event["type"] == "item.completed" and "ddtest" in event.get("command", ""):
            for version in re.findall(r"Local runtime: Node (v?\d+\.\d+\.\d+(?:[-+][\w.-]+)?)", event.get("output", "")):
                observations = manifest.setdefault("runtime_observations", [])
                if len(observations) < 20:
                    observations.append({"source": "public ddtest command output", "version": version})
        activity.write(json.dumps(event) + "\n")
        activity.flush()
        print(json.dumps(event), flush=True)

    def command(argv, cwd=trial, agent=False, stdin=None, timeout=None):
        limit = args.timeout if timeout is None else min(args.timeout, timeout)
        record = {"argv": argv, "cwd": str(cwd), "started_at": time.time(), "timeout_seconds": limit}
        manifest["commands"].append(record)
        emit({"type": "harness_command", **record})
        tail = ""

        def line_received(line):
            nonlocal tail, final_response
            tail = (tail + line + "\n")[-EXCERPT_LIMIT:]
            if not agent:
                print(line, flush=True)
                return
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                emit({"type": "diagnostic", "text": excerpt(line)})
                return
            if isinstance(event, dict):
                # Grade the whole public answer, before bounded activity excerpts
                # can discard a contradictory opening completion claim.
                item = event.get("item", {})
                if args.agent == "codex" and event.get("type") == "item.completed" and item.get("type") == "agent_message":
                    final_response = item.get("text", "")
                elif args.agent == "claude" and event.get("type") == "result":
                    final_response = event.get("result", "")
                if args.agent == "claude":
                    background.observe(event)
                for public in public_events(event, args.agent):
                    emit(public)

        try:
            record["exit_code"] = execute(argv, cwd, {**env, **overrides} if agent else env, limit, line_received, stdin)
        finally:
            record["finished_at"] = time.time()
            if not agent:
                record["output_excerpt"] = tail
        if record["exit_code"] != 0:
            raise RuntimeError(f"Command exited {record['exit_code']}: {shlex.join(argv)}")
        return tail.strip()

    code = 1
    try:
        if java_home is not None:
            manifest["java_runtime"] = command([str(java_home / "bin" / "java"), "-version"], timeout=CLI_STARTUP_TIMEOUT)
            manifest["maven_runtime"] = command(["mvn", "--version"], timeout=CLI_STARTUP_TIMEOUT)
        if node is not None:
            runtime = json.loads(command(["node", "-p",
                "JSON.stringify({executable: process.execPath, version: process.version})"],
                timeout=CLI_STARTUP_TIMEOUT))
            manifest["node_runtime"] = {"requested": str(node), **runtime,
                                        "codex_login_shell": False}
            if not os.path.samefile(runtime["executable"], node):
                raise RuntimeError("Selected Node executable did not run; inspect node_runtime")
        manifest["cli_version"] = command([args.agent, "--version"], timeout=CLI_STARTUP_TIMEOUT)
        command(["git", "clone", "--", args.repo, str(checkout)])
        command(["git", "checkout", "--detach", args.ref], cwd=checkout)
        manifest["commit"] = command(["git", "rev-parse", "HEAD"], cwd=checkout)
        for setup in setup_commands:
            command(setup, cwd=checkout)
        status = command(["git", "status", "--porcelain"], cwd=checkout)
        if status:
            raise RuntimeError("Dependency setup changed the checkout; choose setup that leaves it clean")
        manifest["status"] = "agent_running"
        command(agent_argv, cwd=checkout, agent=True, stdin=prompt)
        if background.unfinished():
            manifest["unfinished_background_commands"] = background.unfinished()
            manifest["failure_kind"] = "infrastructure"
            manifest["retry_recommended"] = True
            raise RuntimeError("CLI exited with unfinished Bash tasks; local validation was interrupted, not completed")
        if not agent_completed and not agent_failed:
            raise RuntimeError("CLI exited without a recognized completion event; inspect the activity")
        manifest["status"] = "agent_failed" if agent_failed else "completed"
        code = 1 if agent_failed else 0
    except (OSError, RuntimeError, TimeoutError, ValueError, KeyboardInterrupt) as error:
        manifest["status"] = "interrupted" if isinstance(error, KeyboardInterrupt) else "failed"
        agent_timed_out = isinstance(error, TimeoutError) and bool(manifest["commands"]) and manifest["commands"][-1]["argv"] == agent_argv
        if isinstance(error, TimeoutError) and manifest["commands"][-1]["argv"] == [args.agent, "--version"]:
            manifest["failure_kind"] = "infrastructure"
            manifest["failure_stage"] = "cli_startup"
            manifest["retry_recommended"] = True
        manifest["error"] = str(error) or "Interrupted"
        print(manifest["error"], file=sys.stderr)
    finally:
        if "commit" in manifest:
            for name, argv in (
                ("changes.patch", ["git", "diff", "--no-ext-diff", "--no-textconv", manifest["commit"], "--"]),
                ("status.txt", ["git", "status", "--short", "--untracked-files=all"]),
            ):
                try:
                    with (output / name).open("w") as destination:
                        subprocess.run(argv, cwd=checkout, env=env, stdout=destination,
                                       stderr=subprocess.PIPE, check=True, timeout=30)
                except (OSError, subprocess.SubprocessError) as error:
                    manifest.setdefault("collection_errors", []).append(str(error))
                    code = 1
        if agent_failed or agent_timed_out:
            manifest["failure_kind"] = failure_kind(agent_errors, agent_timed_out)
            manifest["retry_recommended"] = manifest["failure_kind"] == "infrastructure"
        report_path = checkout / ".testoptimization" / "testdrive.json"
        report = {}
        try:
            if report_path.is_file():
                report = json.loads(report_path.read_text())
        except (OSError, ValueError) as error:
            manifest.setdefault("collection_errors", []).append(str(error))
        manifest["agent_reporting"] = reporting_verdict(final_response, report)
        if manifest["agent_reporting"]["status"] == "fail":
            manifest["status"] = "agent_failed"
            manifest["failure_kind"] = "agent_reporting"
            manifest["retry_recommended"] = False
            code = 1
        manifest["ddtest_runtime"] = report_runtime(report, manifest.get("node_runtime", {}).get("version"))
        if manifest["ddtest_runtime"]["status"] == "mismatch":
            manifest["status"] = "failed"
            manifest["failure_kind"] = "evaluator_environment"
            manifest["failure_stage"] = "ddtest_runtime"
            manifest["error"] = "Final ddtest runtime differs from --node; the setup probe does not validate the agent's command environment."
            manifest["retry_recommended"] = False
            code = 1
        activity.close()
        if final_response:
            (output / "final-response.md").write_text(final_response + "\n")
        manifest["finished_at"] = time.time()
        (output / "run.json").write_text(json.dumps(manifest, indent=2) + "\n")
        print(f"Evidence: {output}\nDisposable checkout retained for evaluation: {trial}")
    return code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True, help="Public clone URL (local paths also work)")
    parser.add_argument("--ref", required=True, help="Commit SHA or ref; resolved SHA is recorded")
    parser.add_argument("--ddtest", type=Path, required=True, help="Already-built executable")
    parser.add_argument("--agent", choices=("codex", "claude"), required=True)
    parser.add_argument("--node", type=Path,
                        help="Absolute Node executable for setup and Codex non-login shells; Codex only")
    parser.add_argument("--java-home", type=Path, help="Absolute JDK home for Maven trials with private dependency caches and process-local agent configuration")
    parser.add_argument("--framework", choices=("jest", "vitest", "playwright", "mocha", "cypress", "cucumber", "webdriverio") + JAVA_FRAMEWORKS,
                        help="Optional scope for mixed-framework repositories; adds only the framework to the minimal prompt")
    parser.add_argument("--setup-command", action="append", default=[], help="Repeatable, no implicit shell")
    parser.add_argument("--output", type=Path, required=True, help="New directory for evaluator evidence")
    parser.add_argument("--model", help="Optional requested model; default is the CLI's configured model")
    parser.add_argument("--timeout", type=float, default=1800, help="Seconds allowed per command")
    args = parser.parse_args()
    if args.timeout <= 0 or args.ref.startswith("-"):
        parser.error("Use a positive timeout and a ref that does not start with '-'")
    try:
        return run(args)
    except (OSError, ValueError) as error:
        parser.error(str(error))


if __name__ == "__main__":
    sys.exit(main())
