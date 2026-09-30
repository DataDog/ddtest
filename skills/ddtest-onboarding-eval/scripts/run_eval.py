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


EXCERPT_LIMIT = 4000


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


def agent_environment(agent, timeout):
    if agent != "claude":
        return {}
    # Print mode cannot resume a conversation after its final result. Keep Bash
    # synchronous and allow long paired runs without the default 120s handoff.
    bash_timeout = str(min(600000, int(timeout * 1000)))
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
    """Flag explicit upgrades; retain the original response for human review."""
    status = report.get("summary", {}).get("status")
    plain = re.sub(r"[*_`#]", "", text).lower()
    claims_complete = re.search(r"\bonboarding\s+(?:(?:is|was|has been)\s+)?(?:done|complete|completed|successful)\b", plain)
    if status == "INCOMPLETE" and claims_complete:
        return {"status": "fail", "reason": "Agent claimed completed onboarding despite INCOMPLETE validation."}
    if status and status.lower() not in plain:
        return {"status": "review", "reason": "Final response does not state the report verdict."}
    return {"status": "review", "reason": "No explicit verdict upgrade detected; independently review counts, causes, and caveats."}


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
        kill_group()
        process.wait()
        process.stdout.close()
        process.stdin.close()


def run(args):
    binary = args.ddtest.resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError("--ddtest must be an executable file")
    setup_commands = [shlex.split(command) for command in args.setup_command]
    if any(not command for command in setup_commands):
        raise ValueError("--setup-command must not be empty")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    trial = Path(tempfile.mkdtemp(prefix="ddtest-onboarding-eval-")).resolve()
    checkout = trial / "repo"
    bin_dir = trial / "bin"
    bin_dir.mkdir()
    shutil.copy2(binary, bin_dir / "ddtest")
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("DD_", "DATADOG_")) and key not in ("NODE_OPTIONS", "NODE_PATH")}
    for key, name in (("npm_config_cache", "npm"), ("YARN_CACHE_FOLDER", "yarn"),
                      ("pnpm_config_store_dir", "pnpm-store"), ("pnpm_config_cache_dir", "pnpm"),
                      ("TMPDIR", "tmp")):
        directory = trial / "caches" / name
        directory.mkdir(parents=True, exist_ok=True)
        env[key] = str(directory)
    env["PATH"] = str(bin_dir) + os.pathsep + env.get("PATH", "")
    prompt = f"Use ddtest in {bin_dir} and onboard this repository to test-optimization."
    manifest = {"repository": args.repo, "requested_ref": args.ref,
                "trial_directory": str(trial), "checkout": str(checkout),
                "ddtest_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "agent": args.agent, "requested_model": args.model, "prompt": prompt,
                "started_at": time.time(), "commands": [], "status": "preparing",
                "evaluation": "pending", "ci_execution": "not exercised"}
    activity = (output / "activity.jsonl").open("w")
    agent_failed = False
    agent_completed = False
    agent_errors = []
    agent_timed_out = False
    final_response = ""
    background = BackgroundCommands()
    overrides = agent_environment(args.agent, args.timeout)
    manifest["agent_environment"] = overrides

    def emit(event):
        nonlocal agent_failed, agent_completed
        if event["type"] == "agent_error":
            agent_failed = True
            agent_errors.append(str(event.get("error", event.get("text", ""))))
        if event["type"] == "result":
            agent_completed = True
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
        command(agent_command(args.agent, args.model), cwd=checkout, agent=True, stdin=prompt)
        if background.unfinished():
            manifest["unfinished_background_commands"] = background.unfinished()
            manifest["failure_kind"] = "infrastructure"
            manifest["retry_recommended"] = True
            raise RuntimeError("CLI exited with unfinished Bash tasks; local validation was interrupted, not completed")
        if not agent_completed and not agent_failed:
            raise RuntimeError("CLI exited without a recognized completion event; inspect the activity")
        manifest["status"] = "agent_failed" if agent_failed else "completed"
        code = 1 if agent_failed else 0
    except (OSError, RuntimeError, TimeoutError, KeyboardInterrupt) as error:
        manifest["status"] = "interrupted" if isinstance(error, KeyboardInterrupt) else "failed"
        agent_timed_out = isinstance(error, TimeoutError) and bool(manifest["commands"]) and manifest["commands"][-1]["argv"] == agent_command(args.agent, args.model)
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
        if final_response and report_path.is_file():
            try:
                manifest["agent_reporting"] = reporting_verdict(final_response, json.loads(report_path.read_text()))
                if manifest["agent_reporting"]["status"] == "fail":
                    manifest["status"] = "agent_failed"
                    manifest["failure_kind"] = "agent_reporting"
                    manifest["retry_recommended"] = False
                    code = 1
            except (OSError, ValueError) as error:
                manifest.setdefault("collection_errors", []).append(str(error))
        activity.close()
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
