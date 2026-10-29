#!/usr/bin/env python3
"""Run an uncoached onboarding trial; leave grading to the evaluator."""

import argparse
import codecs
import contextlib
import getpass
import hashlib
import math
import json
import os
import re
from pathlib import Path
import shlex
import shutil
import selectors
import signal
import subprocess
import sys
import tempfile
import time
from urllib.parse import unquote

from model_auth import ModelLogin, login_directory, install_login, remove_login

from isolation import Container, check_socket_budget, cleanup, environment, interrupted, mark_trial, trial_root, validate_image, check_interrupted, begin_teardown, termination_signal


EXCERPT_LIMIT = 4000
LOGIN_SECRETS = set()


def excerpt(value):
    text = value if isinstance(value, str) else json.dumps(value)
    return text if len(text) <= EXCERPT_LIMIT else "[truncated]\n" + text[-EXCERPT_LIMIT:]


def redact(value):
    """Redact before truncation, so truncation cannot expose half a token."""
    if isinstance(value, dict):
        return {key: "[REDACTED]" if re.search(r"(?i)(password|secret|token|api.?key|authorization|credential|private.?key)", key)
                else redact(item) for key, item in value.items()
                if key not in ("reasoning", "thinking", "signature", "encrypted_content")}
    if isinstance(value, list):
        return [redact(item) for item in value]
    if not isinstance(value, str):
        return value
    for secret in LOGIN_SECRETS:
        value = value.replace(secret,"[REDACTED]")
    for key, secret in os.environ.items():
        if len(secret) >= 6 and re.search(r"(?i)(password|secret|token|api.?key|authorization|credential)", key):
            value = value.replace(secret, "[REDACTED]")
    value = re.sub(r"-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----", "[REDACTED PRIVATE KEY]", value, flags=re.S)
    value = re.sub(r"\b(?:AKIA[A-Z0-9]{16}|(?:gh[pousr]_|github_pat_|npm_|sk-)[A-Za-z0-9_-]{12,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b", "[REDACTED]", value)
    # A GitHub Actions expression such as ${{ secrets.DD_API_KEY }} names a
    # secret without containing it, so the workflow evidence keeps it intact.
    value = re.sub(r"(?i)((?:\b[\w-]{0,80}(?:token|secret|password|api[_-]?key)|\bauthorization)[\"']?\s*[:=]\s*[\"']?)(?!\$\{\{)(?:bearer\s+)?[^\s,;\"'}]+", r"\1[REDACTED]", value)
    return value


class LineRedactor:
    """Keep multiline private keys redacted across streamed setup output."""
    def __init__(self):
        self.private_key = False

    def __call__(self, line):
        if self.private_key or re.search(r"-----BEGIN [^-]*PRIVATE KEY-----",line):
            self.private_key = not bool(re.search(r"-----END [^-]*PRIVATE KEY-----",line))
            return "[REDACTED PRIVATE KEY]"
        return redact(line)


def public_detail(value):
    value = redact(value)
    if isinstance(value,dict):
        return {key:public_detail(item) for key,item in value.items()}
    if isinstance(value,list):
        return [public_detail(item) for item in value]
    return excerpt(value) if isinstance(value,str) else value


def public_events(event, agent):
    """Retain every public tool type, including its targets, but never reasoning."""
    kind = event.get("type")
    if agent == "codex":
        item = event.get("item", {})
        item_type = item.get("type")
        if item_type in ("reasoning", "thinking"):
            return
        if kind == "thread.started":
            yield {"type": "session", "id": event.get("thread_id")}
        elif kind in ("item.started", "item.completed"):
            if item_type == "command_execution":
                yield {"type": kind, "id": item.get("id"), "command": item.get("command"),
                       "exit_code": item.get("exit_code"), "output": excerpt(redact(item.get("aggregated_output", "")))}
            elif item_type == "agent_message":
                if kind == "item.completed":
                    yield {"type": "message", "text": excerpt(redact(item.get("text", "")))}
            else:
                yield {"type": "tool", "event_type": kind, "tool": item_type, "details": public_detail(item)}
        elif kind in ("error", "turn.failed"):
            yield {"type": "agent_error", "error": excerpt(redact(event.get("error", event.get("message", ""))))}
        else:
            yield {"type": "result" if kind == "turn.completed" else "event", "event_type": kind}
    else:
        if kind == "system" and event.get("subtype") == "init":
            yield {"type": "session", "id": event.get("session_id"), "model": event.get("model")}
        elif kind == "assistant":
            for block in event.get("message", {}).get("content", []):
                if block.get("type") == "text":
                    yield {"type": "message", "text": excerpt(redact(block.get("text", "")))}
                elif block.get("type") == "tool_use":
                    yield {"type": "command" if block.get("name") == "Bash" else "tool",
                           "tool": block.get("name"), "id": block.get("id"),
                           "command": block.get("input", {}).get("command"), "input": public_detail(block.get("input", {}))}
                elif block.get("type") not in ("thinking", "redacted_thinking"):
                    yield {"type": "tool", "tool": block.get("name", block.get("type")),
                           "details": public_detail(block)}
        elif kind == "user":
            content = event.get("message", {}).get("content", [])
            for block in content if isinstance(content, list) else []:
                if block.get("type") == "tool_result":
                    yield {"type": "tool_result", "id": block.get("tool_use_id"),
                           "is_error": block.get("is_error", False), "output": excerpt(redact(block.get("content", "")))}
        elif kind == "result":
            yield {"type": "agent_error" if event.get("is_error") else "result",
                   "text": excerpt(redact(event.get("result", event.get("errors", "")))),
                   "permission_denials": excerpt(redact(event.get("permission_denials", [])))}
        else:
            yield {"type": "event", "event_type": kind, "subtype": event.get("subtype")}


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


# Ruby and Java validations build their own environments (bundles, Maven
# reactors in disposable copies) repeatedly; they need longer foreground runs
# and container-native scratch storage.
RUBY_FRAMEWORKS = ("rspec", "minitest")
JAVA_FRAMEWORKS = ("junit4", "junit5", "testng", "spock", "cucumber-java", "karate", "scalatest", "munit")


def agent_environment(agent, timeout, framework=None):
    if agent != "claude":
        return {}
    # Print mode cannot resume a conversation after its final result. Keep Bash
    # synchronous and allow long paired runs without the default 120s handoff.
    maximum = 3600000 if framework in ("cypress", *RUBY_FRAMEWORKS, *JAVA_FRAMEWORKS) else 600000
    bash_timeout = str(min(maximum, int(timeout * 1000)))
    # Auto-memory would persist trial conclusions in the disposable home and
    # mix agent notes into the evidence; trials must stay independent.
    return {"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
            "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1",
            "BASH_DEFAULT_TIMEOUT_MS": bash_timeout,
            "BASH_MAX_TIMEOUT_MS": bash_timeout}


def agent_memory_files(home):
    """Names only: the trial home is writable by untrusted code, so never follow links."""
    found = []
    projects = home / ".claude" / "projects"
    # os.walk follows a symlinked starting path even without followlinks.
    if any(path.is_symlink() for path in (home, home / ".claude", projects)):
        return []
    for root, directories, files in os.walk(projects):
        if os.path.basename(root) == "memory":
            found.extend(str(Path(root, name).relative_to(home)) for name in files)
    return sorted(found)


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
    """Require the product's copyable handoff; audit extra claims independently."""
    report = report if isinstance(report, dict) else {}
    summary = report.get("summary") if isinstance(report.get("summary"),dict) else {}
    canonical = summary.get("final_response")
    missing, differences, checks, unsupported = [], [], [], []
    if isinstance(canonical, str) and canonical and canonical in text:
        remaining = text.replace(canonical, "")
        checks.append("unchanged summary.final_response")
    else:
        remaining = text
        missing.append("unchanged summary.final_response")
    def normalize(value):
        value = re.sub(r"[*_`#]", "", value).lower()
        # Conditional requirements describe future work, not present completion.
        return re.sub(r"\bonboarding(?:\s+validation)?\s*(?::|is|was|has been)?\s*complete\s+only\s+(?:after|when|if)\b[^.!?\n]*", "", value)

    plain = normalize(text)
    # Prose outside the product's own handoff: the handoff's wording is audited
    # by the field comparisons below, never by these heuristics.
    extra = normalize(remaining)

    def compare(label, expected, found):
        if expected is None:
            missing.append("JSON "+label)
        elif found != expected:
            differences.append(f"{label}: stated {found}, report says {expected}.")

    for match in re.finditer(r"\b(?:onboarding(?:\s+validation)?|status)\s*(?::|is|was|has been|remains)?\s*(n't\s+|not\s+|never\s+)?(?:successfully\s+|fully\s+)?(incomplete|complete locally|complete|completed|done|successful|passed|failed)\b", plain):
        if negated(plain, match.start()):
            continue
        stated = match[2].upper()
        # Plain completion words claim the product's complete verdict, but the
        # difference keeps the agent's own wording.
        wording = stated
        if stated in ("COMPLETE", "COMPLETED", "DONE", "SUCCESSFUL") or stated == "PASSED" and summary.get("status") == "COMPLETE LOCALLY":
            stated = "COMPLETE LOCALLY"
        if match[1]:
            # "Status is not incomplete" denies the reported status.
            if summary.get("status") == stated:
                differences.append(f"onboarding verdict: stated not {wording}, report says {stated}.")
            continue
        if summary.get("status") is not None and summary.get("status") != stated and wording != stated:
            differences.append(f"onboarding verdict: stated {wording} (a completion claim), report says {summary.get('status')}.")
            continue
        compare("onboarding verdict", summary.get("status"), stated)
    if summary.get("status") != "COMPLETE LOCALLY":
        for match in re.finditer(r"\b(?:(?:fully|successfully|completely)\s+onboarded|onboarded\s+(?:this|the)\s+(?:repo|repository|project)|onboarding\s+(?:succeeded|is\s+finished|was\s+a\s+success)|(?:everything|all)\s+(?:is\s+)?(?:done|complete|set up))\b", extra):
            if not negated(extra, match.start()):
                unsupported.append(f"Success claim \"{match[0]}\" while the report status is {summary.get('status') or 'unavailable'}.")
    for match in re.finditer(r"\blocal\s+validation\s*(?::|is|was|remains)?\s*((?:passed|failed|incomplete|not exercised)(?: earlier; not rerun)?|historical only)\b", plain):
        compare("local verdict", summary.get("local_validation"), match[1].strip().upper())
    for key, label in (("selected_validation", "selected"), ("executed_validation", "executed")):
        expected = summary.get(key) or {}
        for match in re.finditer(r"\b"+label+r"[^:\n]*\b(?:testdrive|validations?):\s*(passed|incomplete|not exercised|failed)\b(?:\s*\((\d+)\s*/\s*(\d+)\))?", plain):
            compare(label+" verdict", expected.get("status"), match[1].upper())
            if key == "executed_validation" and match[2]:
                compare("executed configuration ratio", (expected.get("passed"), expected.get("configurations")), (int(match[2]),int(match[3])))
    coverage = summary.get("configuration_coverage") or {}
    for match in re.finditer(r"\bconfigurations:\s*([^\n.]*)", plain):
        line = match[1]
        ratio = re.search(r"(\d+)\s*/\s*(\d+)\s+(?:fully\s+)?validated", line)
        if ratio:
            # CI opt-outs (excluded from instrumentation) are not unvalidated work.
            discovered = coverage.get("discovered")
            if isinstance(discovered, int):
                discovered -= coverage.get("excluded") or 0
            compare("validated/discovered configurations", (coverage.get("validated"),discovered), (int(ratio[1]),int(ratio[2])))
        excluded = re.search(r"\((\d+)\s+more\s+excluded\b", line)
        if excluded:
            compare("excluded configurations", coverage.get("excluded") or 0, int(excluded[1]))
        for key, label in (("executed","executed"),("paired","paired"),("paired","(?:executed\\s+)?with\\s+paired\\s+comparison"),("blocked","blocked"),("not_exercised","not exercised"),("incomplete","incomplete")):
            found = re.search(r"(?:^|[;,])\s*(\d+)\s+"+label+r"\b",line)
            if found:
                compare(key.replace("_"," ")+" configurations", coverage.get(key), int(found[1]))
    counts = report.get("reporting_counts") if isinstance(report.get("reporting_counts"),dict) else {}
    evidence = report.get("last_execution") if isinstance(report.get("last_execution"),dict) else {}
    current = not report.get("check_only") and evidence.get("current", True)
    count_pattern = r"\b(\d[\d,]*)\s+(source[- ]test identities|unique tests|distinct tests|(?:reported )?test executions?)\b"
    for match in re.finditer(count_pattern, plain):
        key = "test_executions" if "executions" in match[2] or "execution" in match[2] else "source_test_identities"
        compare(key, "unavailable" if key == "source_test_identities" and counts.get(key) is None else counts.get(key), int(match[1].replace(",","")))
        if match[2] in ("unique tests", "distinct tests"):
            # Matching source identities across commands don't make tests unique
            # or establish equivalent configurations, even if the number agrees.
            unsupported.append(f"\"{match[0]}\" is stronger than the report's source-test identities.")
    if not current:
        for line in plain.splitlines():
            if re.search(count_pattern, line) and not any(label in line for label in ("historical","not revalidated","earlier","previous","prior")):
                differences.append("Historical counts were presented as current.")
                break
    if summary.get("blocking_checks") and re.search(r"\b(?:no|zero|0)\s+(?:blocking checks|blockers)\b|\b(?:blocking checks|blockers):\s*(?:none|no|zero|0)\b", plain):
        differences.append("Agent dismissed the reported blocking_checks.")
    directory = report.get("working_directory")
    if directory:
        for a,b in re.findall(r"\[[^\]]+\]\((?:<([^>]+)>|([^\s)]+))\)", text):
            link = unquote(a or b)
            if Path(link).name in ("testdrive.json", "report.html") and Path(link).is_absolute():
                compare("report path", str(Path(directory)/".testoptimization"/Path(link).name), link)
    outcome = "fail" if differences else "review" if missing or unsupported else "pass"
    return {"status":outcome, "reason":"Direct claims contradict JSON." if differences else
            "Missing canonical handoff needs independent review." if missing else
            "Claims beyond the report need independent review." if unsupported else
            "Canonical handoff retained without direct contradictions; independent technical evaluation is still required.",
            "canonical_handoff":bool(isinstance(canonical,str) and canonical and canonical in text),
            "checks":checks, "differences":differences[:10], "missing":missing, "unsupported":unsupported[:10]}


def negated(text, start):
    """A claim negated earlier in its own clause is not a claim."""
    sentence = re.split(r"[.!?;,\n]|\bbut\b", text[:start])[-1]
    return re.search(r"\b(?:not|never|no longer|isn't|wasn't|hasn't|haven't|cannot|can't|without|yet to)\b|n't\b", sentence) is not None


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


# JAVA_RUNTIME_SCRIPT prints the JDK home and its release file's JAVA_VERSION.
JAVA_RUNTIME_SCRIPT = (
    'home="${JAVA_HOME:-$(dirname "$(dirname "$(readlink -f "$(command -v java)")")")}"; '
    'printf "%s\\n" "$home"; sed -n \'s/^JAVA_VERSION="\\(.*\\)"$/\\1/p\' "$home/release"')


def report_preflight_runtime(report, requested):
    # Ruby and Java reports serialize the interpreter or JDK as preflight.runtime.
    observed = sorted({preflight["runtime"] for preflight in [report.get("preflight") or {},
        *[(configuration.get("preflight") or {}) for configuration in report.get("configurations") or []]]
        if isinstance(preflight, dict) and isinstance(preflight.get("runtime"), str) and preflight.get("runtime")})
    return {"requested_version": requested, "observed_versions": observed,
            "status": "not observed" if not observed else "matched" if observed == [requested] else "mismatch"}


def agent_command(agent, model):
    if agent == "codex":
        # Production execution is always inside Container. A nested CLI namespace
        # sandbox cannot start with the container's dropped capabilities.
        command = ["codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "--json", "-c", "mcp_servers={}"]
    else:
        command = ["claude", "--print", "--permission-mode", "auto",
                   "--output-format", "stream-json", "--verbose", "--setting-sources", "",
                   "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}']
    if model:
        command.extend(["--model", model])
    if agent == "codex":
        command.append("-")
    return command


def execute(command, cwd, env, timeout, on_line, prompt=None):
    """Bound attached Docker pipe reads; container teardown owns descendants."""
    check_interrupted()
    process = subprocess.Popen(command, cwd=cwd, env=env, text=True,
                               errors="replace", stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, stdin=subprocess.PIPE,
                               start_new_session=True)
    deadline = time.monotonic() + timeout
    selector = selectors.DefaultSelector()
    try:
        if prompt:
            process.stdin.write(prompt)
        process.stdin.close()
        os.set_blocking(process.stdout.fileno(), False)
        selector.register(process.stdout, selectors.EVENT_READ)
        decoder = codecs.getincrementaldecoder("utf-8")(errors="replace")
        pending = ""
        oversized = False
        while True:
            check_interrupted()
            remaining = deadline-time.monotonic()
            if remaining <= 0:
                raise TimeoutError(f"Command exceeded {timeout}s: {shlex.join(command)}")
            if not selector.select(min(0.1,remaining)):
                continue
            chunk = os.read(process.stdout.fileno(),65536)
            pending += decoder.decode(chunk, final=not chunk)
            while "\n" in pending:
                line, _, pending = pending.partition("\n")
                on_line("[oversized CLI event omitted]" if oversized or len(line)>1024*1024 else line.rstrip("\r"))
                oversized = False
            if not chunk:
                if pending or oversized:
                    on_line("[oversized CLI event omitted]" if oversized else pending)
                break
            # A process can emit an arbitrarily long unterminated line.
            if len(pending) > 1024*1024:
                oversized = True
                pending = ""
        try:
            return process.wait(timeout=max(0,deadline-time.monotonic()))
        except subprocess.TimeoutExpired as error:
            raise TimeoutError(f"Command exceeded {timeout}s: {shlex.join(command)}") from error
    finally:
        selector.close()
        try:
            process.poll()
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        finally:
            process.wait(timeout=2)
            process.stdout.close()
            process.stdin.close()


def run(args):
    if not math.isfinite(args.timeout) or args.timeout <= 0 or not args.ref or args.ref.startswith("-"):
        raise ValueError("Use a positive finite timeout and a ref that does not start with '-'")
    validate_image(getattr(args,"image",None))
    LOGIN_SECRETS.clear()
    binary = args.ddtest.resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError("--ddtest must be an executable file")
    node = getattr(args, "node", None)
    if node is not None:
        if args.agent != "codex":
            raise ValueError("--node currently requires --agent codex")
        if not node.is_absolute():
            raise ValueError("--node must be an absolute executable path")
        # The executable belongs to the image, not the evaluator's host.
        if ".." in node.parts:
            raise ValueError("--node must be an absolute path inside the image")
    setup_commands = [shlex.split(command) for command in args.setup_command]
    if any(not command for command in setup_commands):
        raise ValueError("--setup-command must not be empty")
    output = args.output.resolve()
    # macOS's per-user TMPDIR is too deep for Watchman's Unix socket name.
    # A unique short root still owns every cache/configuration and its cleanup.
    root = trial_root()
    check_socket_budget(Path("/trial"), os.environ.get("USER") or getpass.getuser())
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    trial = Path(tempfile.mkdtemp(prefix="dde-", dir=root)).resolve()
    trial.chmod(0o700)
    container = Container(trial, getattr(args,"image",None))
    # Record the container before it exists, so --cleanup can find it after SIGKILL.
    mark_trial(trial, output, container.name)
    home = trial / "home"
    home.mkdir()
    for directory in (home/".codex",home/".claude",trial/"zsh"):
        directory.mkdir()
    checkout = trial / "repo"
    bin_dir = trial / "bin"
    bin_dir.mkdir()
    shutil.copy2(binary, bin_dir / "ddtest")
    env = environment(home)
    env["PATH"] = container.path
    env["SHELL"] = "/bin/bash"
    env["CODEX_HOME"] = str(home/".codex")
    env["CLAUDE_CONFIG_DIR"] = str(home/".claude")
    env["ZDOTDIR"] = str(trial/"zsh")
    # Trust only this newly owned clone when a VM maps bind-mount UIDs.
    env.update(GIT_CONFIG_COUNT="1",GIT_CONFIG_KEY_0="safe.directory",GIT_CONFIG_VALUE_0=str(checkout))
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
    # Ruby validation builds disposable bundles, and Java validation copies the
    # repository per configuration, repeatedly. Keep that high-file-count
    # scratch on the container's private tmpfs, not a macOS bind mount. It is
    # removed with the container; retained reports remain in the checkout.
    if getattr(args, "framework", None) in (*RUBY_FRAMEWORKS, *JAVA_FRAMEWORKS):
        env["TMPDIR"] = "/tmp"
    # pnpm's own configuration is not inherited by npm commands in test scripts.
    pnpm_config = Path(env["XDG_CONFIG_HOME"]) / "pnpm" / "rc"
    pnpm_config.parent.mkdir()
    pnpm_store, pnpm_cache = trial / "caches" / "pnpm-store", trial / "caches" / "pnpm"
    pnpm_store.mkdir()
    pnpm_cache.mkdir()
    inside = container.inside if hasattr(container,"inside") else str
    pnpm_config.write_text(f"store-dir={inside(pnpm_store)}\ncache-dir={inside(pnpm_cache)}\n")
    pnpm_config.with_name("config.yaml").write_text(
        f"storeDir: {json.dumps(inside(pnpm_store))}\ncacheDir: {json.dumps(inside(pnpm_cache))}\n")
    if node is not None:
        (bin_dir / "node").symlink_to(node)
        zdotdir = trial / "zsh"
        zdotdir.mkdir(exist_ok=True)
        env["ZDOTDIR"] = str(zdotdir)
        env["PATH"] = str(node.parent) + os.pathsep + env.get("PATH", "")
    pnpm = getattr(args, "pnpm", None)
    tools = trial / "tools"
    if pnpm is not None:
        # The project's pnpm, private to this trial, precedes the image's pnpm
        # for setup and the agent alike, so lockfiles keep their format.
        env["PATH"] = str(tools / "node_modules" / ".bin") + os.pathsep + env.get("PATH", "")
    env["PATH"] = str(bin_dir) + os.pathsep + env.get("PATH", "")
    agent_argv = agent_command(args.agent, args.model)
    if node is not None:
        # .zshenv also runs in non-login shells. Isolate it and disable shell
        # snapshots so neither host startup files nor an old snapshot reset PATH.
        agent_argv[2:2] = ["-c", "allow_login_shell=false", "-c",
                           "features.shell_snapshot=false", "-c",
                           "shell_environment_policy.set.PATH=" + json.dumps(env["PATH"]), "-c",
                           "shell_environment_policy.set.ZDOTDIR=" + json.dumps(env["ZDOTDIR"])]
    framework = getattr(args, "framework", None)
    scope = f" for {framework} only" if framework else ""
    prompt = f"Use ddtest in {bin_dir} and onboard this repository to test-optimization{scope}."
    manifest = {"isolation": {"kind":"container", "image":getattr(args,"image",None), "name":container.name,"git_safe_directory":str(checkout)}, "repository": args.repo, "requested_ref": args.ref,
                "trial_directory": str(trial), "checkout": str(checkout),
                "ddtest_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "agent": args.agent, "requested_model": args.model, "prompt": container.inside(prompt) if hasattr(container,"inside") else prompt,
                "framework_scope": framework,
                "started_at": time.time(), "commands": [], "status": "preparing",
                "evaluation": "pending", "ci_execution": "not exercised"}
    manifest["private_caches"] = {key: env[key] for key in (
        "npm_config_cache", "YARN_CACHE_FOLDER", "XDG_CACHE_HOME", "XDG_DATA_HOME",
        "XDG_STATE_HOME", "XDG_CONFIG_HOME", "PNPM_HOME", "TMPDIR",
        "CYPRESS_CACHE_FOLDER", "PLAYWRIGHT_BROWSERS_PATH", "PUPPETEER_CACHE_DIR")}
    manifest["private_caches"]["pnpm_store"] = str(pnpm_store)
    manifest["private_caches"]["pnpm_cache"] = str(pnpm_cache)
    if node is not None:
        manifest["shell_isolation"] = {"zdotdir": env["ZDOTDIR"], "login_shell": False, "shell_snapshot": False}
    activity = (output / "activity.jsonl").open("w")
    agent_failed = False
    agent_completed = False
    agent_errors = []
    agent_timed_out = False
    final_response = ""
    background = BackgroundCommands()
    overrides = agent_environment(args.agent, args.timeout, framework)
    manifest["agent_environment"] = overrides

    def finalization_failed(error):
        # Unconfirmed teardown or evidence outranks the agent's own result.
        manifest.setdefault("collection_errors", []).append(error)
        if manifest["status"] != "interrupted":
            manifest["status"] = "failed"
        return 1

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
        event = redact(event)
        activity.write(json.dumps(event) + "\n")
        activity.flush()
        print(json.dumps(event), flush=True)

    def command(argv, cwd=trial, agent=False, stdin=None, timeout=None, destination=None):
        nonlocal agent_failed
        limit = args.timeout if timeout is None else min(args.timeout, timeout)
        record = {"argv": argv, "cwd": str(cwd), "started_at": time.time(), "timeout_seconds": limit}
        if destination is None:
            manifest["commands"].append(record)
        emit({"type": "harness_command", **record})
        tail = ""
        line_redactor = LineRedactor()

        def line_received(line):
            nonlocal tail, final_response
            clean_line = line_redactor(line) if not agent else redact(line)
            tail = (tail + clean_line + "\n")[-EXCERPT_LIMIT:]
            if not agent:
                if destination is not None:
                    destination.write(clean_line+"\n")
                else:
                    print(clean_line, flush=True)
                return
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                emit({"type": "diagnostic", "text": excerpt(line_redactor(line))})
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
            record["exit_code"] = container.execute(argv, cwd, {**env, **overrides} if agent else env, limit, line_received, stdin, execute)
        finally:
            record["finished_at"] = time.time()
            if not agent:
                record["output_excerpt"] = tail
        if record["exit_code"] != 0:
            if agent:
                agent_failed = True
                agent_errors.append(tail)
            raise RuntimeError(f"Command exited {record['exit_code']}: {shlex.join(argv)}")
        return tail.strip()

    login = ModelLogin(args.agent, home, LOGIN_SECRETS, getattr(args,"login_json",None), getattr(args,"existing_login",False))
    code = 1
    container_started = False
    login_installed = False
    try:
        manifest["failure_stage"]="storage_readiness"
        if shutil.disk_usage(trial).free < 2*1024**3:
            raise RuntimeError("Evaluator storage has less than 2 GiB free; no clone or model session started")
        container.start()
        container_started = True
        manifest.pop("failure_stage",None)
        if node is not None:
            runtime = json.loads(command(["node", "-p",
                "JSON.stringify({executable: process.execPath, version: process.version, requested_executable: require('node:fs').realpathSync("+json.dumps(str(node))+")})"],
                timeout=CLI_STARTUP_TIMEOUT))
            expected_executable = runtime.pop("requested_executable",str(node))
            manifest["node_runtime"] = {"requested": str(node), **runtime,
                                        "codex_login_shell": False}
            if runtime["executable"] != expected_executable:
                raise RuntimeError("Selected Node executable did not run; inspect node_runtime")
        if framework in RUBY_FRAMEWORKS:
            manifest["ruby_runtime"] = json.loads(command(["ruby", "-rjson", "-e",
                'puts JSON.generate(executable: RbConfig.ruby, version: RUBY_VERSION)'], timeout=CLI_STARTUP_TIMEOUT))
        if framework in JAVA_FRAMEWORKS:
            # ddtest reads the JDK's release file, from JAVA_HOME or the java on PATH.
            java_home, java_version = command(["sh", "-c", JAVA_RUNTIME_SCRIPT], timeout=CLI_STARTUP_TIMEOUT).strip().split("\n")
            manifest["java_runtime"] = {"home": java_home, "version": java_version}
        manifest["cli_version"] = command([args.agent, "--version"], timeout=CLI_STARTUP_TIMEOUT)
        expected_cli = container.cli_versions[args.agent]
        if not re.search(r"(?<![\d.])"+re.escape(expected_cli)+r"(?![\d.])", manifest["cli_version"]):
            raise RuntimeError(f"Expected {args.agent} {expected_cli}; image CLI version differs")
        manifest["ddtest_version"] = command([str(bin_dir/"ddtest"),"--version"],timeout=CLI_STARTUP_TIMEOUT)
        if getattr(args,"login_json",None) or getattr(args,"existing_login",False):
            manifest["failure_stage"] = "model_authentication"
            login.install()
            overrides.update(login.env)
            preflight_argv = agent_command(args.agent,args.model)
            if args.agent == "codex":
                preflight_argv.insert(2,"--skip-git-repo-check")
            observed = []
            def auth_line(line):
                try:
                    event=json.loads(line)
                    observed.extend(public_events(event,args.agent))
                except (ValueError,TypeError):
                    pass
            code_auth = container.execute(preflight_argv, home, {**env,**overrides}, min(args.timeout,90), auth_line,
                "Reply AUTH_OK only. Do not use tools or inspect files.",execute)
            failed = any(event.get("type")=="agent_error" for event in observed)
            if code_auth or failed or not any("AUTH_OK" in str(event.get("text","")) for event in observed):
                raise RuntimeError("Model authentication preflight failed before clone/install; no onboarding trial started")
            manifest["authentication"] = {"status":"passed", "source":"existing CLI model login" if getattr(args,"existing_login",False) else "selected login", "preflight":"model response before clone/install"}
            login.suspend()
            manifest.pop("failure_stage",None)
        command(["git", "clone", "--", args.repo, str(checkout)])
        command(["git", "fetch", "--update-shallow", "--no-tags", "origin", args.ref], cwd=checkout)
        command(["git", "checkout", "--detach", "FETCH_HEAD"], cwd=checkout)
        manifest["submodules"] = "requested" if getattr(args,"submodules",False) else "not requested"
        if getattr(args,"submodules",False):
            command(["git", "submodule", "update", "--init", "--recursive"], cwd=checkout)
        manifest["commit"] = command(["git", "rev-parse", "HEAD"], cwd=checkout)
        if pnpm is not None:
            manifest["failure_stage"] = "package_manager"
            command(["npm", "install", "--prefix", str(tools), "--no-save", "--no-audit", "--no-fund", "pnpm@" + pnpm])
            selected = command(["pnpm", "--version"], cwd=checkout, timeout=CLI_STARTUP_TIMEOUT)
            if selected != pnpm and not selected.startswith(pnpm + "."):
                raise RuntimeError(f"Requested pnpm {pnpm}; pnpm {selected} runs in the checkout")
            manifest["package_manager"] = {"pnpm": selected, "requested": pnpm}
            manifest.pop("failure_stage", None)
        for setup in setup_commands:
            command(setup, cwd=checkout)
        status = command(["git", "status", "--porcelain"], cwd=checkout)
        if status:
            raise RuntimeError("Dependency setup changed the checkout; choose setup that leaves it clean")
        manifest["failure_stage"] = "runtime_readiness"
        for ready in getattr(args,"readiness_command",[]):
            command(shlex.split(ready),cwd=checkout)
        manifest.pop("failure_stage",None)
        # Setup descendants cannot mutate credential directories during installation.
        if hasattr(container,"control"):
            container.control("pause",container.name)
        try:
            login.resume()
            login_installed = bool(login.initial)
        finally:
            if hasattr(container,"control"):
                container.control("unpause",container.name)
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
    except (OSError, RuntimeError, TimeoutError, ValueError, KeyboardInterrupt, subprocess.SubprocessError) as error:
        manifest["status"] = "interrupted" if isinstance(error, KeyboardInterrupt) else "failed"
        agent_timed_out = isinstance(error, TimeoutError) and bool(manifest["commands"]) and manifest["commands"][-1]["argv"] == agent_argv
        if isinstance(error, TimeoutError) and manifest["commands"][-1]["argv"] == [args.agent, "--version"]:
            manifest["failure_kind"] = "infrastructure"
            manifest["failure_stage"] = "cli_startup"
            manifest["retry_recommended"] = True
        if manifest.get("failure_stage")=="model_authentication":
            manifest["failure_kind"]="authentication"
            manifest["retry_recommended"]=False
        elif manifest.get("failure_stage") in ("runtime_readiness","storage_readiness","package_manager"):
            manifest["failure_kind"]="evaluator_environment"
            manifest["retry_recommended"]=False
        if manifest.get("status")=="preparing" and not manifest.get("failure_kind"):
            manifest["failure_kind"]="evaluator_environment"
            manifest["retry_recommended"]=False
        manifest["error"] = str(error) or "Interrupted"
        print(redact(manifest["error"]), file=sys.stderr)
    finally:
        begin_teardown()
        if container_started:
            # A timed-out or interrupted agent keeps running after its docker
            # exec client is killed; stop it before reading the checkout.
            try:
                container.stop_processes()
                manifest["isolation"]["processes_stopped"] = True
            except (OSError, RuntimeError, subprocess.SubprocessError) as error:
                manifest["isolation"]["processes_stopped"] = False
                code = finalization_failed(str(error))
        # Processes that could not be stopped may still change the checkout, so
        # its diff and status would not be stable evidence.
        if "commit" in manifest and manifest["isolation"].get("processes_stopped", True):
            for name, argv in (
                ("changes.patch", ["git", "diff", "--no-ext-diff", "--no-textconv", manifest["commit"], "--"]),
                ("status.txt", ["git", "status", "--short", "--untracked-files=all"]),
            ):
                try:
                    with (output / name).open("w") as destination:
                        command(argv, cwd=checkout, timeout=30, destination=destination)
                except (OSError, subprocess.SubprocessError, RuntimeError, TimeoutError) as error:
                    code = finalization_failed(str(error))
        try:
            container.close()
            manifest["isolation"]["cleanup"] = "removed"
        except (OSError, RuntimeError, subprocess.SubprocessError) as error:
            manifest["isolation"]["cleanup"] = "failed"
            manifest["status"] = "failed"
            manifest.setdefault("collection_errors",[]).append(str(error))
            code = 1
        cleanup_confirmed = manifest["isolation"]["cleanup"] == "removed"
        if (agent_failed or agent_timed_out) and cleanup_confirmed:
            manifest["failure_kind"] = failure_kind(agent_errors, agent_timed_out)
            manifest["retry_recommended"] = manifest["failure_kind"] == "infrastructure"
        try:
            # Never read refreshed credentials from a potentially live agent.
            login.close(persist=cleanup_confirmed)
            if login_installed:
                manifest["login"] = "removed"
            if login.unpersisted_change:
                manifest["login_refresh"] = "not persisted: the trial login changed after repository code could write it; if the host CLI stops authenticating, log in again"
        except (OSError, RuntimeError, ValueError) as error:
            if login_installed:
                manifest["login"] = "not removed"
            manifest.setdefault("collection_errors",[]).append(str(error))
            manifest["status"]="failed"
            manifest["failure_kind"]="authentication"
            code=1
        report_path = checkout / ".testoptimization" / "testdrive.json"
        report = {}
        try:
            if not report_path.resolve().is_relative_to(trial):
                raise ValueError("Report path escapes owned trial storage")
            if report_path.is_file():
                with report_path.open("rb") as source:
                    encoded = source.read(16*1024*1024+1)
                if len(encoded)>16*1024*1024:
                    raise ValueError("Compact report exceeds 16 MiB")
                report = json.loads(encoded)
                if not isinstance(report,dict):
                    raise ValueError("Report must be a JSON object")
        except (OSError, ValueError) as error:
            code = finalization_failed(str(error))
        try:
            manifest["agent_reporting"] = reporting_verdict(final_response, report)
            manifest["ddtest_runtime"] = report_runtime(report, manifest.get("node_runtime", {}).get("version"))
            for language, name in (("ruby", "Ruby"), ("java", "Java")):
                if language+"_runtime" not in manifest:
                    continue
                manifest["ddtest_"+language+"_runtime"] = report_preflight_runtime(report, manifest[language+"_runtime"]["version"])
                if manifest["ddtest_"+language+"_runtime"]["status"] == "mismatch":
                    manifest["status"], manifest["failure_kind"] = "failed", "evaluator_environment"
                    manifest["failure_stage"] = "ddtest_"+language+"_runtime"
                    manifest["error"] = "Final ddtest "+name+" runtime differs from the selected image runtime"
                    manifest["retry_recommended"] = False
                    code = 1
        except (AttributeError, TypeError, ValueError) as error:
            # Reports are untrusted trial output. A malformed nested field must
            # not prevent durable evidence after the container has been removed.
            manifest.setdefault("collection_errors", []).append("Invalid report fields: " + str(error))
            manifest["agent_reporting"] = {"status": "review", "reason": "Invalid report fields."}
            manifest["ddtest_runtime"] = {"status": "not observed"}
            manifest["status"] = "failed"
            code = 1
        if manifest["agent_reporting"]["status"] == "fail":
            # An evaluator or finalization failure outranks the agent's reporting.
            if manifest["status"] != "failed":
                manifest["status"] = "agent_failed"
                manifest["failure_kind"] = "agent_reporting"
            manifest["retry_recommended"] = False
            code = 1
        if manifest["ddtest_runtime"]["status"] == "mismatch":
            manifest["status"] = "failed"
            manifest["failure_kind"] = "evaluator_environment"
            manifest["failure_stage"] = "ddtest_runtime"
            manifest["error"] = "Final ddtest runtime differs from --node; the setup probe does not validate the agent's command environment."
            manifest["retry_recommended"] = False
            code = 1
        memory = agent_memory_files(home)
        if memory:
            # Auto-memory is disabled for Claude; any remaining notes are agent
            # artifacts that later trials and graders must not rely on.
            manifest["agent_artifacts"] = {"memory_files": memory}
        activity.close()
        if final_response:
            (output / "final-response.md").write_text(redact(final_response) + "\n")
        if termination_signal():
            manifest["status"] = "interrupted"
            manifest["error"] = f"Interrupted by signal {termination_signal()}"
            code = 1
        if not cleanup_confirmed:
            # The previous container may still be running: this outranks the
            # trial's own outcome, and a blind retry is not safe.
            manifest["failure_kind"] = "evaluator_environment"
            manifest["failure_stage"] = "container_cleanup"
            manifest["retry_recommended"] = False
            if manifest["status"] != "interrupted":
                manifest["status"] = "failed"
        manifest["finished_at"] = time.time()
        (output / "run.json").write_text(json.dumps(redact(manifest), indent=2) + "\n")
        print(f"Evidence: {output}\nDisposable checkout retained for evaluation: {trial}")
    return code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cleanup", type=Path, help="Remove the verified owned trial recorded in this output directory")
    parser.add_argument("--image", help="Prebuilt Docker image ID or repository@sha256 digest; required for trials")
    parser.add_argument("--login-json", type=Path, help="Selected model login; refresh updates persist, never copies a profile")
    parser.add_argument("--existing-login", action="store_true", help="Reuse authorized existing CLI model authentication, including a managed Claude helper")
    parser.add_argument("--submodules", action="store_true", help="Initialize submodules at the requested commit")
    parser.add_argument("--repo", help="Public clone URL reachable from the container")
    parser.add_argument("--ref", help="Commit SHA or ref; resolved SHA is recorded")
    parser.add_argument("--ddtest", type=Path, help="Already-built Linux executable for the image architecture")
    parser.add_argument("--agent", choices=("codex", "claude"))
    parser.add_argument("--node", type=Path,
                        help="Absolute Node executable for setup and Codex non-login shells; Codex only")
    parser.add_argument("--framework", choices=("jest", "vitest", "playwright", "mocha", "cypress", "cucumber", "webdriverio", *RUBY_FRAMEWORKS, *JAVA_FRAMEWORKS),
                        help="Optional scope for mixed-framework repositories; adds only the framework to the minimal prompt")
    parser.add_argument("--readiness-command", action="append", default=[], help="Repeatable native runtime/browser readiness checks before the uncoached agent")
    parser.add_argument("--setup-command", action="append", default=[], help="Repeatable, no implicit shell")
    parser.add_argument("--pnpm", help="Install this pnpm version privately for setup and the agent (e.g. the version the project's CI pins)")
    parser.add_argument("--output", type=Path, help="New directory for evaluator evidence")
    parser.add_argument("--model", help="Optional requested model; default is the CLI's configured model")
    parser.add_argument("--timeout", type=float, default=1800, help="Seconds allowed per command")
    args = parser.parse_args()
    if args.cleanup is not None:
        try:
            print("Removed owned trial: " + str(cleanup(args.cleanup)))
            return 0
        except (OSError, ValueError, KeyError, RuntimeError) as error:
            parser.error(str(error))
    if not all((args.repo,args.ref,args.ddtest,args.agent,args.output,args.image)):
        parser.error("Trials require --repo, --ref, --ddtest, --agent, --output and --image")
    if args.pnpm is not None and not re.fullmatch(r"\d+(?:\.\d+){0,2}", args.pnpm):
        parser.error("--pnpm must be a version such as 10 or 10.33.4")
    try:
        with interrupted():
            return run(args)
    except (OSError, ValueError) as error:
        parser.error(str(error))


if __name__ == "__main__":
    sys.exit(main())
