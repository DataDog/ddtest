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
import posixpath
import re
from pathlib import Path, PurePosixPath
import shlex
import shutil
import selectors
import signal
import stat
import subprocess
import sys
import tempfile
import time
from urllib.parse import unquote

from model_auth import ModelLogin, login_directory, install_login, remove_login

from isolation import INTAKE_GUARD_VERSION, Container, check_socket_budget, cleanup, environment, interrupted, mark_trial, trial_root, validate_image, check_interrupted, begin_teardown, termination_signal

import user_answers
import dependency_cache
import case_image


# Characters kept of each text in activity.jsonl (--excerpt-limit). ddtest
# results are single JSON documents that can exceed several thousand
# characters; the excerpt keeps their start (status, code) and their end.
EXCERPT_LIMIT = 64000
# Once activity.jsonl holds ACTIVITY_BUDGET bytes, later texts keep only
# REDUCED_EXCERPT_LIMIT characters, so the file stays bounded.
REDUCED_EXCERPT_LIMIT = 4000
ACTIVITY_BUDGET = 64 * 1024 * 1024
# Setup command tails retained in run.json.
COMMAND_TAIL_LIMIT = 4000
excerpt_limit = EXCERPT_LIMIT
LOGIN_SECRETS = set()


def excerpt(value, limit=None):
    text = value if isinstance(value, str) else json.dumps(value)
    limit = limit or excerpt_limit
    if len(text) <= limit:
        return text
    head = limit // 2
    return text[:head] + f"\n[truncated: {len(text) - limit} characters omitted]\n" + text[len(text) - (limit - head):]


# A variable reference names a secret without containing it: $NAME, ${NAME...}
# (or $${NAME...}, escaped for make or Compose), ${{ secrets.NAME }},
# $(command), process.env.NAME, os.environ, or a lookup such as
# credentials('id') or ENV['NAME'] whose literal ends at the quote. Only the
# whole value counts: "$ecret-123" or a "$2b$12$..." hash is a literal.
SECRET_REFERENCE = re.compile(r"\$\$?\{[#!]?[A-Za-z_]\w*(?:\W.*)?|(?:\$[A-Za-z_]\w*)+|\$\([A-Za-z_./]\S*"
                              r"|process\.env\.[A-Za-z_]\w*|(?:process\.env|os\.environ)(?:\[|\.get\()?", re.S)
# Names expanded in single quotes or in a data file (a quoted key, as in JSON)
# stay literal; a CI expression or interpolation (${{ ... }}, ${NAME}) does not.
EXPRESSION_REFERENCE = re.compile(r"\$\{\{.*|\$\$?\{[#!]?[A-Za-z_]\w*(?:\W.*)?", re.S)


def secret_reference(value, literal_names=False):
    """Whether value, whole, is a reference rather than a literal: with
    literal_names, $NAME and $(command) are taken literally."""
    return bool((EXPRESSION_REFERENCE if literal_names else SECRET_REFERENCE).fullmatch(value)
                or value.startswith("${{"))
SECRET_LOOKUP = re.compile(r"[A-Za-z_][\w.]*[(\[]")
SECRET_ASSIGNMENT = re.compile(r"(?i)((?:\b[\w-]{0,80}(?:token|secret|password|api[_-]?key)|\bauthorization)[\"']?\s*[:=]\s*[\"']?)((?:bearer\s+)?)([^\s,;\"'}]+)")


# A POSIX (or Bash) parameter expansion of a secret-named variable:
# ${NAME}, ${#NAME}, ${NAME:-word}, ${NAME+word}, ${NAME:?message},
# ${NAME#pattern}, ${NAME%%pattern}, ${NAME/pattern/string}, ${NAME:offset},
# ${NAME^^}, ${NAME@Q}. Only a default ((:)- or (:)=) supplies the variable's
# value; the other operands replace, test, trim or slice it.
SECRET_EXPANSION = re.compile(r"(\$\{[#!]?(?i:[A-Za-z_]\w*(?:token|secret|password|api_?key)|authorization))"
                              r"(:?[-=]|:?[?+]|##?|%%?|//?|\^\^?|,,?|@|:)?([^}]*)\}")
# A secret-named assignment's value is the expansion's, whatever variable it
# expands: DD_API_KEY=${VAULT_VALUE:-word} assigns a literal default word.
# Without its closing brace (a truncated line) the word ends at the line.
SECRET_ASSIGNED_EXPANSION = re.compile(r"(?i)((?:\b[\w-]{0,80}(?:token|secret|password|api[_-]?key)|\bauthorization)"
                                       r"[\"']?\s*[:=]\s*[\"']?)(\$\{[#!]?[A-Za-z_]\w*)"
                                       r"(:?[-=]|:?[?+]|##?|%%?|//?|\^\^?|,,?|@|:)?([^}\n]*)(\}|$)", re.M)


# A parameter expansion nested in a default, such as ${FALLBACK:-word} in
# ${PRIMARY:-${FALLBACK:-word}}, without the closing braces the enclosing
# match stops at.
NESTED_EXPANSION = re.compile(r"\$\$?\{[#!]?[A-Za-z_]\w*(:?[-=]|:?[?+]|##?|%%?|//?|\^\^?|,,?|@|:)?(.*)", re.S)


def literal_default(operator, word):
    """Whether a parameter expansion's operand supplies a literal value: a
    default ((:)- or (:)=) that is not a reference, or a reference whose own
    nested default is one."""
    bare = word.strip(" \t\"'")
    if operator not in ("-", ":-", "=", ":=") or not bare:
        return False
    single_quoted = word.lstrip(" \t").startswith("'")
    nested = None if single_quoted else NESTED_EXPANSION.fullmatch(bare)
    if nested:
        return literal_default(nested[1] or "", nested[2])
    return not secret_reference(bare, literal_names=single_quoted)


def redact_expansion(match):
    head, operator, word = match.groups()
    return head + operator + "[REDACTED]}" if literal_default(operator, word) else match[0]


def redact_assigned_expansion(match):
    prefix, head, operator, word, end = match.groups()
    return prefix + head + operator + "[REDACTED]" + end if literal_default(operator, word) else match[0]


# Values a secret-named key can hold that are clearly not secrets: an object or
# array opener (`"token": {` in a JSON file), a boolean or null, a version or
# version range, or a package path or specifier.
NON_SECRET_VALUE = re.compile(r"[\[\]{]+|(?i:true|false|null|none|nil|undefined|~)"
                              r"|(?:(?:[~^]|[<>]=?|=)v?\d+(?:\.(?:\d+|[xX*]))*|v?\d+(?:\.(?:\d+|[xX*]))+)"
                              r"(?:-[0-9A-Za-z.]+)?(?:\+[0-9A-Za-z.]+)?"
                              r"|(?:node_modules/|npm:|workspace:|file:|link:)\S*")
# A lockfile package key such as "node_modules/jsonwebtoken" names a package.
LOCKFILE_PACKAGE = re.compile(r"node_modules/(?:@[\w.-]+/)?[\w.-]*$")
SECRET_NAME = re.compile(r"(?i)(password|secret|token|api.?key|authorization|credential|private.?key)")


def lockfile_package_key(key):
    return "node_modules/" in key


def redact_assignment(match):
    prefix, bearer, value = match.groups()
    if not bearer and (NON_SECRET_VALUE.fullmatch(value) or
                       LOCKFILE_PACKAGE.search(match.string[max(0, match.start() - 300):match.start()])):
        return match[0]
    # A single-quoted value, or a quoted key's (a data file such as JSON),
    # does not expand $NAME.
    literal_names = prefix.endswith("'") or re.search(r"[\"']\s*[:=]\s*[\"']?$", prefix) is not None
    if not bearer and (secret_reference(value, literal_names) or
                       SECRET_LOOKUP.fullmatch(value) and match.string[match.end():match.end() + 1] in ("'", '"')):
        return match[0]
    if match.string[:match.start()].rstrip("#!").endswith("${"):
        # A parameter expansion redact_expansion did not see, such as one
        # without its closing brace: still only a literal default is a value.
        operator = prefix[-1] + value[:1] if prefix.endswith(":") and value[:1] in "-=" else prefix[-1]
        if literal_default(operator, value[len(operator) - 1:]):
            return prefix + value[:len(operator) - 1] + "[REDACTED]"
        return match[0]
    return prefix + "[REDACTED]"


def redact(value):
    """Redact before truncation, so truncation cannot expose half a token."""
    if isinstance(value, dict):
        return {key: "[REDACTED]" if SECRET_NAME.search(key) and not lockfile_package_key(key)
                and not (item is None or isinstance(item, bool))
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
    # References such as ${{ secrets.DD_API_KEY }}, ${DD_API_KEY:-} or
    # ${DD_API_KEY:+set} name a secret without containing it, so the CI
    # evidence keeps them intact.
    value = SECRET_EXPANSION.sub(redact_expansion, value)
    value = SECRET_ASSIGNED_EXPANSION.sub(redact_assigned_expansion, value)
    return SECRET_ASSIGNMENT.sub(redact_assignment, value)


# The folder the prompt names holds this wrapper. It runs the real ddtest with
# unchanged arguments, input, error output and exit status, and copies its
# standard output to a record outside the checkout. The scripted-answer check
# then sees ddtest's result even when the agent redirects or reformats it.
DDTEST_WRAPPER = """#!/bin/sh
record=$(mktemp {records}/XXXXXXXXXX 2>/dev/null) || exec {real} "$@"
exec 4>&1
status=$( {{ {{ {real} "$@" 3>&- 4>&-; echo "$?" >&3; }} | tee "$record" >&4 3>&-; }} 3>&1 )
exec 4>&-
mv -f "$record" "$record.done" 2>/dev/null
exit "${{status:-1}}"
"""
# ddtest prints its own path in next_command and resume_command, or the
# executable DDTEST_SELF_PATH names. The real binary stays in the harness
# folder and DDTEST_SELF_PATH names the wrapper, so every command ddtest prints
# runs the wrapper and an agent that follows them stays recorded.
REAL_DDTEST = "ddtest"
SELF_PATH_VARIABLE = "DDTEST_SELF_PATH"
# Older builds ignore DDTEST_SELF_PATH and print os.Executable. On Linux that
# is /proc/self/exe, which ignores argv[0] and reads "<path> (deleted)" for a
# deleted file; Go trims that suffix. For them the real binary moves beside the
# wrapper under that name, so the printed path is still the wrapper's.
LEGACY_REAL_DDTEST = "ddtest (deleted)"
# Unconfirmed testdrive in a new empty repository writes nothing and returns
# consent_required, whose next_command starts with the path ddtest prints.
SELF_PATH_PROBE = 'd=$(mktemp -d) && cd "$d" && git init -q . && { "$0" testdrive --agent; rm -rf "$d"; }; exit 0'


def printed_executable(output):
    """The executable of the first next_command in ddtest's output, or None."""
    for line in output.splitlines():
        try:
            result = json.loads(line)
        except ValueError:
            continue
        command = result.get("next_command") if isinstance(result, dict) else None
        if isinstance(command, list) and command and isinstance(command[0], str):
            return command[0]
    return None
# The runner moves each completed record out of the trial, into evidence the
# container does not mount, as soon as the agent reports a tool result. The
# harness log is therefore never in the checkout or in the agent's later view,
# and a record cannot change after it is collected. (The container user owns
# all trial storage, and the Docker VM's mount cannot hide a folder from it.)


def submodule_update_argv(local_mirrors):
    """The submodule update command. Git refuses file transport for
    submodules by default; local mirrors need it, for this command only."""
    allow = ["-c", "protocol.file.allow=always"] if local_mirrors else []
    return ["git", *allow, "submodule", "update", "--init", "--recursive"]


def submodule_entries(repository, config):
    """The (path, url) of each submodule a .gitmodules lists, from git config
    arguments that select it in repository, such as -f <file> or
    --blob <rev>:.gitmodules."""
    listed = subprocess.run(["git", "-C", str(repository), "config", *config, "--get-regexp", r"^submodule\..*\.(path|url)$"],
                            capture_output=True, text=True)
    fields = {}
    for line in listed.stdout.splitlines():
        key, _, value = line.partition(" ")
        name, _, field = key[len("submodule."):].rpartition(".")
        fields.setdefault(name, {})[field] = value.strip()
    return [(entry.get("path", ""), entry["url"]) for entry in fields.values() if "url" in entry]


def local_submodule_mirrors(checkout, local, source):
    """Mirrors, beside the mirror source of the local repository local, each
    relative submodule repository the checkout's .gitmodules names, and the
    relative submodules those name at the revisions their parents pin,
    resolved from local as Git resolves them without a remote, so the
    container can clone them. Returns the mirrors made."""
    mirrors = []

    def visit(entries, pinned, origin_base, mirror_base):
        for path, url in entries:
            if not url.startswith(("./", "../")):
                continue
            origin = (origin_base / url).resolve()
            mirror = Path(os.path.normpath(mirror_base / url))
            if not origin.is_dir():
                continue
            if not mirror.exists():
                subprocess.run(["git", "clone", "--mirror", "--quiet", "--", str(origin), str(mirror)], check=True)
                mirrors.append(mirror)
            # Its own relative submodules, at the revision its parent pins,
            # resolve beside its mirror, as Git resolves them from its URL.
            revision = pinned(path)
            if revision:
                nested = submodule_entries(origin, ["--blob", revision + ":.gitmodules"])
                visit(nested, lambda child, origin=origin, revision=revision: tree_entry(origin, revision, child), origin, mirror)

    visit(submodule_entries(checkout, ["-f", str(checkout / ".gitmodules")]), lambda path: tree_entry(checkout, "HEAD", path), local, source)
    return mirrors


def tree_entry(repository, revision, path):
    """The commit a gitlink at path of revision in repository pins; empty
    when there is none."""
    listed = subprocess.run(["git", "-C", str(repository), "ls-tree", revision, "--", path], capture_output=True, text=True)
    fields = listed.stdout.split()
    return fields[2] if len(fields) >= 3 and fields[1] == "commit" else ""


def binstub_names(directory):
    """The binstub folder's file names. Setup code may replace the folder, so
    it is listed without following a symlink out of the trial."""
    try:
        fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    except OSError as error:
        raise RuntimeError(f"Dependency setup replaced the binstub folder {directory.name}; it must stay a folder") from error
    try:
        return sorted(os.listdir(fd))
    finally:
        os.close(fd)


# The most of one record the runner reads; ddtest's results are far smaller.
RECORD_LIMIT = 4 * 1024 * 1024


def read_record(name, directory_fd):
    """A record's text, or "" for one that is not a regular file. Code in the
    trial can write the records folder, so a record is opened without
    following a symlink or blocking on a FIFO, and read up to RECORD_LIMIT."""
    try:
        fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory_fd)
    except OSError:
        return ""
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            return ""
        chunks, size = [], 0
        while size < RECORD_LIMIT:
            chunk = os.read(fd, min(1024 * 1024, RECORD_LIMIT - size))
            if not chunk:
                break
            chunks.append(chunk)
            size += len(chunk)
        return b"".join(chunks).decode(errors="replace")
    finally:
        os.close(fd)


NOFOLLOW_DIRECTORY = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW


class SymlinkedPath(OSError):
    """A component of a path inside an owned trial is a symlink, which is never followed."""


def refusal(directory_fd, name, error, shown):
    """SymlinkedPath when `name` (below directory_fd) is a symlink, else the original error."""
    try:
        linked = stat.S_ISLNK(os.stat(name, dir_fd=directory_fd, follow_symlinks=False).st_mode)
    except OSError:
        linked = False
    return SymlinkedPath(f"{shown} is a symlink") if linked else error


def open_directory(path):
    """An fd of an owned folder, refusing a symlink in its place."""
    try:
        return os.open(path, NOFOLLOW_DIRECTORY)
    except OSError as error:
        raise refusal(None, path, error, str(path)) from error


def open_directory_beneath(directory_fd, parts, shown=""):
    """An fd of the folder at `parts` below directory_fd. Trial code can replace
    any folder with a symlink, so no component is followed: a symlink raises
    SymlinkedPath, a missing one FileNotFoundError, a file NotADirectoryError."""
    fd = os.dup(directory_fd)
    try:
        for index, part in enumerate(parts):
            try:
                child = os.open(part, NOFOLLOW_DIRECTORY, dir_fd=fd)
            except OSError as error:
                raise refusal(fd, part, error, posixpath.join(shown, *parts[:index + 1])) from error
            os.close(fd)
            fd = child
        return fd
    except BaseException:
        os.close(fd)
        raise


def read_beneath(directory_fd, parts, limit, shown="", *, with_mode=False):
    """Up to limit+1 bytes of the regular file at `parts` below directory_fd, or
    None for anything else (a FIFO, a folder). Like open_directory_beneath, it
    follows no symlink, the file's own name included, and never blocks on a FIFO.
    with_mode also returns the mode captured from the same open file descriptor."""
    parent = open_directory_beneath(directory_fd, parts[:-1], shown)
    try:
        try:
            fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
        except OSError as error:
            raise refusal(parent, parts[-1], error, posixpath.join(shown, *parts)) from error
        try:
            mode = os.fstat(fd).st_mode
            if not stat.S_ISREG(mode):
                return None
            chunks, size = [], 0
            while size <= limit:
                chunk = os.read(fd, min(1024 * 1024, limit + 1 - size))
                if not chunk:
                    break
                chunks.append(chunk)
                size += len(chunk)
            data = b"".join(chunks)
            return (data, mode) if with_mode else data
        finally:
            os.close(fd)
    finally:
        os.close(parent)


def read_trial_report(trial, checkout, limit):
    """The checkout's .testoptimization/testdrive.json bytes (up to limit+1), or
    None when there is none. The checkout must sit directly in the trial folder,
    and none of the trial folder, the checkout, its report folder or the report
    is followed if it is a symlink."""
    if Path(checkout).parent != Path(trial):
        raise ValueError("Report path escapes owned trial storage")
    trial_fd = open_directory(trial)
    try:
        return read_beneath(trial_fd, [Path(checkout).name, ".testoptimization", "testdrive.json"], limit, str(trial))
    except SymlinkedPath as error:
        raise ValueError(f"Report path escapes owned trial storage: {error}") from None
    except (FileNotFoundError, NotADirectoryError):
        return None
    finally:
        os.close(trial_fd)


def harvest_ddtest_records(directory):
    """Completed ddtest invocations' standard output, oldest first, removed
    from the trial. A record still being written is left for later; a record
    that is not a regular file is removed and kept as an empty record.
    Code in the trial can replace the records folder or its parent (the
    harness folder) with a symlink, so neither is followed: the parent is
    opened without following it, and the folder relative to it."""
    try:
        parent_fd = os.open(directory.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            directory_fd = os.open(directory.name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent_fd)
        finally:
            os.close(parent_fd)
    except OSError:
        return []
    try:
        with os.scandir(directory_fd) as scan:
            entries = sorted((entry.stat(follow_symlinks=False).st_mtime_ns, entry.name)
                             for entry in scan if entry.name.endswith(".done"))
        records = []
        for _, name in entries:
            records.append(read_record(name, directory_fd))
            with contextlib.suppress(OSError):
                os.unlink(name, dir_fd=directory_fd)
        return records
    except OSError:
        return []
    finally:
        os.close(directory_fd)


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


def observed_session(event, agent):
    """The conversation a CLI event belongs to, so a reply can continue it."""
    if agent == "codex":
        return event.get("thread_id") if event.get("type") == "thread.started" else None
    return event.get("session_id") if event.get("type") in ("system", "result") else None


def tool_output(event, agent):
    """The whole text a tool returned to the agent, before any excerpt."""
    if agent == "codex":
        item = event.get("item")
        if event.get("type") == "item.completed" and isinstance(item, dict) and item.get("type") == "command_execution":
            return item.get("aggregated_output")
        return None
    message = event.get("message") if event.get("type") == "user" else None
    content = message.get("content") if isinstance(message, dict) else None
    if not isinstance(content, list):
        return None
    return "\n".join(user_answers.tool_output_text(block.get("content")) for block in content
                     if isinstance(block, dict) and block.get("type") == "tool_result") or None


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
FRAMEWORKS = ("jest", "vitest", "playwright", "mocha", "cypress", "cucumber", "webdriverio", "pytest", *RUBY_FRAMEWORKS, *JAVA_FRAMEWORKS)


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


def canonical_handoff(text, canonical):
    """The match of the product's handoff in the answer, or None. Agents quote
    it in a fenced block or mark its lines or code spans with backticks; that
    formatting is accepted, but any other change to its characters is not."""
    if not isinstance(canonical, str) or not canonical.strip():
        return None
    characters = canonical.replace("`", "")
    return re.search("`*" + "`*".join(map(re.escape, characters)) + "`*", text)


def reporting_verdict(text, report, report_directory=None):
    """Require the product's copyable handoff; audit extra claims independently.

    report_directory is the checkout root, as the agent sees it, whose
    .testoptimization holds the report read here; links must name that
    report. Without it, the report's working directory stands in."""
    report = report if isinstance(report, dict) else {}
    summary = report.get("summary") if isinstance(report.get("summary"),dict) else {}
    canonical = summary.get("final_response")
    missing, differences, checks, unsupported = [], [], [], []
    handoff = canonical_handoff(text, canonical)
    if handoff:
        remaining = text[:handoff.start()] + text[handoff.end():]
        checks.append("unchanged summary.final_response" if handoff[0] == canonical
                      else "summary.final_response unchanged apart from code formatting")
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
        # Quoted report text, in code, a blockquote, quotation marks or a
        # sentence the report itself contains, is not the agent's claim.
        prose = normalize(unquoted(remaining))
        quoted = normalize("\n".join(report_strings(report)))
        for match in SUCCESS_CLAIM.finditer(prose):
            if negated(prose, match.start()):
                continue
            sentence = re.split(r"[.!?\n]", prose[:match.start()])[-1] + re.split(r"[.!?\n]", prose[match.start():])[0]
            if len(sentence.strip()) >= 12 and sentence.strip() in quoted:
                continue
            # "Onboarded once CI runs" states a condition, not completion;
            # "after updating the Jenkinsfile" describes a completed action.
            if qualified_claim(prose, match.start()):
                continue
            claim = match[0].strip(" \t.!—–-")
            unsupported.append(f"Success claim \"{claim}\" while the report status is {summary.get('status') or 'unavailable'}.")
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
    # ddtest keeps one report pair at the repository root, also for runs in a
    # package below it; a link elsewhere names a report the evaluator never read.
    directory = report_directory or report.get("working_directory")
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
            "canonical_handoff":bool(handoff),
            "checks":checks, "differences":differences[:10], "missing":missing, "unsupported":unsupported[:10]}


# A success claim, in normalized prose: a sentence that is only a completion
# word ("Onboarded.", "Done!", "All set."), the repository or Test
# Optimization said to be onboarded, set up or complete, or the agent saying it
# onboarded the repository or finished onboarding.
SUCCESS_CLAIM = re.compile(
    r"(?m)(?:^|(?<=[.!?])[ \t]+)[ \t]*(?:[-+>][ \t]+)?(?:(?:fully|successfully|all)[ \t]+)?"
    r"(?:onboarded|done|complete|completed|finished|success|succeeded|set up|all set)[ \t]*(?:[.!]|$|[—–][ \t]|-[ \t])"
    r"|\b(?:(?:fully|successfully|completely)\s+(?:onboarded|set\s+up\s+(?:test\s+optimization|datadog))"
    r"|onboarded\s+(?:this|the)\s+(?:repo|repository|project|codebase)"
    r"|onboarding\s+(?:succeeded|is\s+finished|was\s+a\s+success|has\s+succeeded)"
    r"|(?:everything|all)\s+(?:is\s+)?(?:done|complete|set\s+up)"
    r"|(?:this|the)\s+(?:repo|repository|project|codebase)\s+(?:is|has\s+been|was)\s+(?:now\s+)?(?:fully\s+|successfully\s+)?(?:onboarded|set\s+up)"
    r"|(?:datadog\s+)?test[\s-]+optimization\s+(?:is|has\s+been|was)\s+(?:now\s+)?(?:fully\s+|successfully\s+)?(?:onboarded|set\s+up|complete|done|in\s+place)"
    r"|(?:i|we)(?:'ve|\s+have)?\s+(?:now\s+)?(?:fully\s+|successfully\s+)?(?:onboarded|finished\s+(?:the\s+)?onboarding|completed\s+(?:the\s+)?onboarding|set\s+up\s+test[\s-]+optimization))\b")


# A condition that qualifies completion: once, when, until, unless, if, as soon
# as, or after a pending event. "After updating the Jenkinsfile" or "after I
# updated it" describes an action done, not a condition.
COMPLETION_CONDITION = re.compile(r"\b(?:once|when|until|unless|if|as soon as"
                                  r"|after(?!\s+(?:\w+?(?<!th)ing|(?:i|we)(?:'ve|\s+have|\s+had)?\s+\w+ed)\b))\b")

# A boundary between coordinate clauses: a condition beyond it qualifies
# another clause, not the claim.
CLAUSE_BREAK = re.compile(r"[.!?;:\n]|[—–]|\s-\s|,\s*(?:and|but|so|then|or)\b|\bbut\b")
# A condition on the reader's needs, not on completion: "if you need anything
# else", "if needed", "when you're ready".
COURTESY_CONDITION = re.compile(r"(?:if|when|once)\s+(?:you(?:['’]d|\s+would|['’]re|\s+are)?\s+"
                                r"(?:need|want|like|prefer|wish|have\s+(?:any\s+)?(?:questions?|feedback|concerns?)|ready)"
                                r"|needed|necessary|desired|required|wanted|helpful|useful|anything|there\s+(?:is|are|['’]s)\s+anything)\b")


def qualified_claim(prose, start):
    """Whether a condition in the claim's own clause qualifies the claim at start."""
    clause = CLAUSE_BREAK.split(prose[:start])[-1] + CLAUSE_BREAK.split(prose[start:], maxsplit=1)[0]
    return any(not COURTESY_CONDITION.match(clause, condition.start())
               for condition in COMPLETION_CONDITION.finditer(clause))


def unquoted(text):
    """Text without code, blockquotes or quotations, as Markdown shows them."""
    text = re.sub(r"(?ms)^[ \t]*(```|~~~).*?(?:^[ \t]*\1[^\n]*$|\Z)", "", text)
    text = re.sub(r"(?m)^[ \t]*>.*$", "", text)
    text = re.sub(r"`[^`\n]*`", "", text)
    return re.sub(r"\"[^\"\n]*\"|“[^”\n]*”", "", text)


def report_strings(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for item in value.values():
            yield from report_strings(item)
    elif isinstance(value, list):
        for item in value:
            yield from report_strings(item)


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
    # Non-Node reports serialize the selected interpreter or JDK as preflight.runtime.
    observed = sorted({preflight["runtime"] for preflight in [report.get("preflight") or {},
        *[(configuration.get("preflight") or {}) for configuration in report.get("configurations") or []]]
        if isinstance(preflight, dict) and isinstance(preflight.get("runtime"), str) and preflight.get("runtime")})
    return {"requested_version": requested, "observed_versions": observed,
            "status": "not observed" if not observed else "matched" if observed == [requested] else "mismatch"}


def report_python_runtime(report, requested):
    audit = report_preflight_runtime(report, requested["version"])
    environments = [{"executable": preflight.get("executable"), "prefix": preflight.get("prefix")}
        for preflight in [report.get("preflight"),
            *[configuration.get("preflight") for configuration in report.get("configurations") or []]]
        if isinstance(preflight, dict) and any(preflight.get(key) for key in ("runtime", "executable", "prefix"))]
    audit.update(requested_executable=requested["executable"], requested_prefix=requested["prefix"],
                 observed_environments=environments)
    if audit["status"] == "matched":
        if not environments or any(not isinstance(item[key], str) or not item[key]
                                   for item in environments for key in ("executable", "prefix")):
            audit["status"] = "not observed"
        else:
            # Venv interpreters commonly symlink to the base interpreter. Compare
            # container paths lexically, allowing python/python3 in the same bin.
            executable = PurePosixPath(posixpath.normpath(requested["executable"]))
            prefix = PurePosixPath(posixpath.normpath(requested["prefix"]))
            if any(PurePosixPath(posixpath.normpath(item["prefix"])) != prefix or
                   PurePosixPath(posixpath.normpath(item["executable"])).parent != executable.parent
                   for item in environments):
                audit["status"] = "mismatch"
    return audit


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


def resume_command(agent, argv, session):
    """Continue the same conversation; stdin becomes the user's next message."""
    if agent == "codex":
        # codex exec resume [OPTIONS] <SESSION_ID> -: exec options follow the subcommand.
        return [*argv[:2], "resume", *argv[2:-1], session, "-"]
    return [*argv, "--resume", session]


def execute(command, cwd, env, timeout, on_line, prompt=None):
    """Bound attached Docker pipe reads; container teardown owns descendants."""
    check_interrupted()
    process = subprocess.Popen(command, cwd=cwd, env=env, text=True,
                               errors="surrogateescape", stdout=subprocess.PIPE,
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
        decoder = codecs.getincrementaldecoder("utf-8")(errors="surrogateescape")
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
                on_line("[oversized CLI event omitted]" if oversized or len(line)>1024*1024 else line)
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
    global excerpt_limit
    if not math.isfinite(args.timeout) or args.timeout <= 0 or not args.ref or args.ref.startswith("-"):
        raise ValueError("Use a positive finite timeout and a ref that does not start with '-'")
    validate_image(getattr(args,"image",None))
    prepare_cache = getattr(args, "prepare_cache", None)
    restore_cache = getattr(args, "dependency_cache", None)
    if prepare_cache and (restore_cache or getattr(args, "existing_login", False) or getattr(args, "login_json", None)):
        raise ValueError("--prepare-cache cannot use model credentials or another dependency cache")
    for key in (prepare_cache, restore_cache):
        if key:
            dependency_cache.validate_fingerprint(key)
    if getattr(args, "warmup_command", None) and not prepare_cache:
        raise ValueError("--warmup-command requires --prepare-cache")
    LOGIN_SECRETS.clear()
    selected_limit = getattr(args, "excerpt_limit", None) or EXCERPT_LIMIT
    if not 1000 <= selected_limit <= 1024 * 1024:
        raise ValueError("--excerpt-limit must be between 1000 and 1048576 characters")
    excerpt_limit = selected_limit
    # Scripted answers are validated before any clone or model session.
    answers = user_answers.load_answers(getattr(args, "user_answer", None), getattr(args, "user_answers", None))
    max_user_turns = getattr(args, "max_user_turns", None)
    max_user_turns = 5 if max_user_turns is None else max_user_turns
    if max_user_turns < 1:
        raise ValueError("--max-user-turns must be at least 1")
    binstubs = list(getattr(args, "bundle_binstubs", None) or [])
    if any(not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", gem) for gem in binstubs):
        raise ValueError("--bundle-binstubs takes a gem name, such as rspec-core")
    binary = args.ddtest.resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError("--ddtest must be an executable file")
    node = getattr(args, "node", None)
    python = getattr(args, "python", None)
    if python is not None:
        if node is not None or getattr(args, "framework", None) != "pytest":
            raise ValueError("--python requires --framework pytest and cannot be combined with --node")
        if not python.is_absolute() or ".." in python.parts:
            raise ValueError("--python must be an absolute executable path inside the image")
    if node is not None:
        if args.agent != "codex":
            raise ValueError("--node currently requires --agent codex")
        if not node.is_absolute():
            raise ValueError("--node must be an absolute executable path")
        # The executable belongs to the image, not the evaluator's host.
        if ".." in node.parts:
            raise ValueError("--node must be an absolute path inside the image")
    prepared_case = getattr(args, "case_image", False)
    if prepared_case and (prepare_cache or restore_cache):
        raise ValueError("--case-image cannot use dependency-cache preparation or restoration")
    if prepared_case and not re.fullmatch(r"[0-9a-f]{40}", args.ref):
        raise ValueError("--case-image requires --ref to be the full source commit")
    if prepared_case and args.setup_command:
        raise ValueError("--case-image already contains setup; omit --setup-command")
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
    harness = trial / "harness"
    ddtest_records = harness / "records"
    ddtest_records.mkdir(parents=True)
    real_ddtest = harness / REAL_DDTEST
    shutil.copy2(binary, real_ddtest)
    wrapper = bin_dir / "ddtest"
    env = environment(home)
    # Setup, readiness and the agent all print commands naming the wrapper.
    env[SELF_PATH_VARIABLE] = str(wrapper)
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
                      ("PUPPETEER_CACHE_DIR", "puppeteer"),
                      ("PIP_CACHE_DIR", "pip"), ("UV_CACHE_DIR", "uv")):
        directory = trial / name if key == "XDG_STATE_HOME" else trial / "caches" / name
        directory.mkdir(parents=True, exist_ok=True)
        env[key] = str(directory)
    # Ruby validation builds disposable bundles, and Java validation copies the
    # repository per configuration, repeatedly. Keep that high-file-count
    # scratch on the container's private tmpfs, not a macOS bind mount. It is
    # removed with the container; retained reports remain in the checkout.
    if getattr(args, "framework", None) in (*RUBY_FRAMEWORKS, *JAVA_FRAMEWORKS):
        env["TMPDIR"] = "/tmp"
    java_build_homes = {}
    if getattr(args, "framework", None) in JAVA_FRAMEWORKS:
        # The container user has no passwd entry, so the JVM's user.home is "?"
        # whatever HOME says, and Maven and Gradle would create ?/.m2 and
        # ?/.gradle in the working directory: the checkout, for a readiness
        # command or the agent's own build. ddtest keeps these options in both
        # validation runs.
        env["MAVEN_OPTS"] = f"-Duser.home={home}"
        env["GRADLE_USER_HOME"] = str(home / ".gradle")
        java_build_homes = {"maven_local_repository": str(home / ".m2" / "repository"),
                            "GRADLE_USER_HOME": env["GRADLE_USER_HOME"]}
    # pnpm's own configuration is not inherited by npm commands in test scripts.
    pnpm_config = Path(env["XDG_CONFIG_HOME"]) / "pnpm" / "rc"
    pnpm_config.parent.mkdir()
    pnpm_store, pnpm_cache = trial / "caches" / "pnpm-store", trial / "caches" / "pnpm"
    pnpm_store.mkdir()
    pnpm_cache.mkdir()
    inside = container.inside if hasattr(container,"inside") else str

    def write_wrapper():
        wrapper.write_text(DDTEST_WRAPPER.format(records=shlex.quote(inside(ddtest_records)), real=shlex.quote(inside(real_ddtest))))
        wrapper.chmod(0o755)
    write_wrapper()
    pnpm_config.write_text(f"store-dir={inside(pnpm_store)}\ncache-dir={inside(pnpm_cache)}\n")
    pnpm_config.with_name("config.yaml").write_text(
        f"storeDir: {json.dumps(inside(pnpm_store))}\ncacheDir: {json.dumps(inside(pnpm_cache))}\n")
    if python is not None:
        env["VIRTUAL_ENV"] = inside(trial / "venv")
        env["UV_PROJECT_ENVIRONMENT"] = env["VIRTUAL_ENV"]
        env["PATH"] = inside(trial / "venv" / "bin") + os.pathsep + env.get("PATH", "")
    if node is not None:
        (bin_dir / "node").symlink_to(node)
        zdotdir = trial / "zsh"
        zdotdir.mkdir(exist_ok=True)
        env["ZDOTDIR"] = str(zdotdir)
        env["PATH"] = str(node.parent) + os.pathsep + env.get("PATH", "")
    bundle_bin = trial / "bundle-bin"
    if binstubs:
        # Bundler binstubs make the gems' executables (bare rspec) available
        # like a CI image's, outside the ddtest folder the prompt names.
        bundle_bin.mkdir()
        env["PATH"] = str(bundle_bin) + os.pathsep + env.get("PATH", "")
    pnpm = getattr(args, "pnpm", None)
    tools = trial / "tools"
    if pnpm is not None:
        # The project's pnpm, private to this trial, precedes the image's pnpm
        # for setup and the agent alike, so lockfiles keep their format.
        env["PATH"] = str(tools / "node_modules" / ".bin") + os.pathsep + env.get("PATH", "")
    env["PATH"] = str(bin_dir) + os.pathsep + env.get("PATH", "")
    agent_argv = agent_command(args.agent, args.model)
    if (node is not None or python is not None) and args.agent == "codex":
        # .zshenv also runs in non-login shells. Isolate it and disable shell
        # snapshots so neither host startup files nor an old snapshot reset PATH.
        agent_argv[2:2] = ["-c", "allow_login_shell=false", "-c",
                           "features.shell_snapshot=false", "-c",
                           "shell_environment_policy.set.PATH=" + json.dumps(env["PATH"]), "-c",
                           "shell_environment_policy.set.ZDOTDIR=" + json.dumps(env["ZDOTDIR"])]
        if python is not None:
            for key in ("VIRTUAL_ENV", "UV_PROJECT_ENVIRONMENT"):
                agent_argv[2:2] = ["-c", "shell_environment_policy.set." + key + "=" + json.dumps(env[key])]
    framework = getattr(args, "framework", None)
    scope = f" for {framework} only" if framework else ""
    prompt = f"Use ddtest in {bin_dir} and onboard this repository to test-optimization{scope}."
    started = time.monotonic()
    phase = "preflight"
    phase_totals = {}
    manifest = {"isolation": {"kind":"container", "image":getattr(args,"image",None), "name":container.name,"git_safe_directory":str(checkout),"intake_guard":INTAKE_GUARD_VERSION}, "repository": args.repo, "requested_ref": args.ref,
                "trial_directory": str(trial), "checkout": str(checkout),
                "ddtest_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "agent": args.agent, "requested_model": args.model, "resolved_model": None,
                "ddtest_label": getattr(args, "ddtest_label", None), "prompt": container.inside(prompt) if hasattr(container,"inside") else prompt,
                "framework_scope": framework, "case_image": prepared_case,
                "started_at": time.time(), "commands": [], "status": "preparing",
                "evaluation": "pending", "ci_execution": "not exercised"}
    manifest["private_caches"] = {key: env[key] for key in (
        "npm_config_cache", "YARN_CACHE_FOLDER", "XDG_CACHE_HOME", "XDG_DATA_HOME",
        "XDG_STATE_HOME", "XDG_CONFIG_HOME", "PNPM_HOME", "TMPDIR",
        "CYPRESS_CACHE_FOLDER", "PLAYWRIGHT_BROWSERS_PATH", "PUPPETEER_CACHE_DIR", "PIP_CACHE_DIR", "UV_CACHE_DIR")}
    manifest["user_answers"] = {"scripted": [answer["text"] for answer in answers], "max_turns": max_user_turns,
                                "status": "not requested", "turns": []}
    manifest["activity_excerpts"] = {"limit_characters": excerpt_limit, "budget_bytes": ACTIVITY_BUDGET,
                                     "reduced_limit_characters": REDUCED_EXCERPT_LIMIT}
    manifest["private_caches"]["pnpm_store"] = str(pnpm_store)
    manifest["private_caches"]["pnpm_cache"] = str(pnpm_cache)
    manifest["private_caches"].update(java_build_homes)
    if node is not None or python is not None:
        manifest["shell_isolation"] = {"zdotdir": env["ZDOTDIR"], "login_shell": False if args.agent == "codex" else "CLI default", "shell_snapshot": False if args.agent == "codex" else "not applicable"}
    activity = (output / "activity.jsonl").open("w")
    agent_failed = False
    agent_completed = False
    agent_errors = []
    agent_timed_out = False
    final_response = ""
    # The agent's conversation, and the last ddtest result of the current turn.
    session_id = None
    last_result = None
    # The last ddtest result of the current turn in the harness log.
    logged_result = None
    ddtest_log = (output / "ddtest-results.jsonl").open("w")
    agent_argvs = []
    activity_bytes = 0
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
        nonlocal agent_failed, agent_completed, activity_bytes
        global excerpt_limit
        if event["type"] == "agent_error":
            agent_failed = True
            agent_errors.append(str(event.get("error", event.get("text", ""))))
        if event["type"] == "result":
            agent_completed = True
        if event["type"] == "session" and event.get("model"):
            # The model the CLI reports for its session (Claude's init event); Codex reports none.
            if manifest["resolved_model"] is None:
                manifest["resolved_model"] = event["model"]
            elif event["model"] != manifest["resolved_model"]:
                models = manifest.setdefault("resolved_models", [manifest["resolved_model"]])
                if event["model"] not in models:
                    models.append(event["model"])
        if event["type"] == "item.completed" and "ddtest" in event.get("command", ""):
            for version in re.findall(r"Local runtime: Node (v?\d+\.\d+\.\d+(?:[-+][\w.-]+)?)", event.get("output", "")):
                observations = manifest.setdefault("runtime_observations", [])
                if len(observations) < 20:
                    observations.append({"source": "public ddtest command output", "version": version})
        event = redact({**event, "observed_at": time.time()})
        encoded = json.dumps(event)
        activity.write(encoded + "\n")
        activity.flush()
        activity_bytes += len(encoded) + 1
        if activity_bytes > ACTIVITY_BUDGET and excerpt_limit > REDUCED_EXCERPT_LIMIT:
            excerpt_limit = REDUCED_EXCERPT_LIMIT
            manifest["activity_excerpts"]["reduced_after_bytes"] = activity_bytes
        print(encoded, flush=True)

    def collect_ddtest_results(turn):
        """Move completed ddtest records to the host-side log; return the last result."""
        found = None
        for record in harvest_ddtest_records(ddtest_records):
            result = user_answers.agent_result(record)
            entry = {"turn": turn, "collected_at": time.time(), "result": redact(result)}
            if result is None:
                entry["output"] = excerpt(redact(record), 1024 * 1024)
            ddtest_log.write(json.dumps(entry) + "\n")
            ddtest_log.flush()
            if result is not None:
                found = result
        return found

    def command(argv, cwd=trial, agent=False, stdin=None, timeout=None, destination=None):
        nonlocal agent_failed
        limit = args.timeout if timeout is None else min(args.timeout, timeout)
        command_started = time.monotonic()
        record = {"argv": argv, "cwd": str(cwd), "started_at": time.time(), "timeout_seconds": limit,
                  "phase": phase}
        if destination is None:
            manifest["commands"].append(record)
        emit({"type": "harness_command", **record})
        tail = ""
        line_redactor = LineRedactor()

        def line_received(line):
            nonlocal tail, final_response, session_id, last_result, logged_result
            clean_line = line_redactor(line) if not agent else redact(line)
            tail = (tail + clean_line + "\n")[-COMMAND_TAIL_LIMIT:]
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
                session_id = observed_session(event, args.agent) or session_id
                # Parse whole tool outputs, before excerpts can cut a ddtest result.
                output_text = tool_output(event, args.agent)
                if output_text is not None:
                    logged_result = collect_ddtest_results(len(agent_argvs)) or logged_result
                result = user_answers.agent_result(output_text)
                if result is not None:
                    last_result = result
                for public in public_events(event, args.agent):
                    emit(public)

        try:
            record["exit_code"] = container.execute(argv, cwd, {**env, **overrides} if agent else env, limit, line_received, stdin, execute)
        finally:
            record["finished_at"] = time.time()
            record["duration_seconds"] = time.monotonic() - command_started
            phase_totals[record["phase"]] = phase_totals.get(record["phase"], 0) + record["duration_seconds"]
            if not agent:
                record["output_excerpt"] = tail
        if record["exit_code"] != 0:
            if agent:
                agent_failed = True
                agent_errors.append(tail)
            raise RuntimeError(f"Command exited {record['exit_code']}: {shlex.join(argv)}")
        return tail.strip()

    def answer_question(asked, source):
        record = manifest["user_answers"]
        turn = {"turn": len(record["turns"]) + 1, "asked_at": time.time(), "question": public_detail(asked), "source": source,
                "agent_message": excerpt(redact(final_response))}
        record["turns"].append(turn)
        emit({"type": "user_question", "turn": turn["turn"], "code": asked["code"], "required_input": asked["required_input"]})
        composed = user_answers.reply(asked, answers)
        turn.update(answers=composed["answers"], missing=composed["missing"])
        stop = ("the scripted answers do not cover it" if composed["reply"] is None else
                f"the limit of {max_user_turns} user replies was reached" if turn["turn"] > max_user_turns else
                "the agent session id was not observed" if not session_id else None)
        if stop:
            record["status"] = "unanswered"
            turn["unanswered"] = stop
            emit({"type": "user_question_unanswered", "turn": turn["turn"], "reason": stop, "missing": composed["missing"]})
            return None
        turn.update(reply=composed["reply"], answered_at=time.time())
        record["status"] = "answered"
        emit({"type": "user_reply", "turn": turn["turn"], "text": composed["reply"], "answers": composed["answers"]})
        return composed["reply"]

    login = ModelLogin(args.agent, home, LOGIN_SECRETS, getattr(args,"login_json",None), getattr(args,"existing_login",False))
    code = 1
    container_started = False
    login_installed = False
    try:
        manifest["failure_stage"]="storage_readiness"
        if shutil.disk_usage(trial).free < 2*1024**3:
            raise RuntimeError("Evaluator storage has less than 2 GiB free; no clone or model session started")
        container_started_at = time.monotonic()
        try:
            container.start()
        finally:
            manifest["container_start_seconds"] = time.monotonic() - container_started_at
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
        manifest["ddtest_version"] = command([str(real_ddtest),"--version"],timeout=CLI_STARTUP_TIMEOUT)
        # The probe runs the real binary, so it leaves no record.
        def probe_printed_executable():
            return printed_executable(command(["sh", "-c", SELF_PATH_PROBE, str(real_ddtest)], timeout=CLI_STARTUP_TIMEOUT))
        printed, mode = probe_printed_executable(), SELF_PATH_VARIABLE
        if printed != inside(wrapper):
            mode = "deleted suffix"
            del env[SELF_PATH_VARIABLE]
            legacy = bin_dir / LEGACY_REAL_DDTEST
            real_ddtest.rename(legacy)
            real_ddtest = legacy
            write_wrapper()
            printed = probe_printed_executable()
        manifest["ddtest_self_path"] = {"mode": mode, "wrapper": inside(wrapper), "real_binary": inside(real_ddtest),
                                        "printed_executable": printed, "wrapper_printed": printed == inside(wrapper)}
        if printed != inside(wrapper):
            emit({"type": "diagnostic", "text": "ddtest's printed commands do not name the recording wrapper; "
                                                "ddtest calls made through them are not recorded"})
        if restore_cache:
            phase = "dependency_cache_check"
            manifest["failure_stage"] = "dependency_cache_restore"
            command(dependency_cache.command("check", restore_cache))
            manifest.pop("failure_stage", None)
            phase = "preflight"
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
            auth_started = time.monotonic()
            code_auth = container.execute(preflight_argv, home, {**env,**overrides}, min(args.timeout,90), auth_line,
                "Reply AUTH_OK only. Do not use tools or inspect files.",execute)
            manifest["model_preflight_seconds"] = time.monotonic() - auth_started
            failed = any(event.get("type")=="agent_error" for event in observed)
            if code_auth or failed or not any("AUTH_OK" in str(event.get("text","")) for event in observed):
                raise RuntimeError("Model authentication preflight failed before clone/install; no onboarding trial started")
            manifest["authentication"] = {"status":"passed", "source":"existing CLI model login" if getattr(args,"existing_login",False) else "selected login", "preflight":"model response before clone/install"}
            login.suspend()
            manifest.pop("failure_stage",None)
        phase = "clone"
        if prepared_case:
            phase = "case_image"
            manifest["failure_stage"] = "case_image"
            prepared = case_image.restore(command, container, args.image, args.repo, args.ref,
                                          framework, trial, checkout)
            manifest["prepared_image"] = prepared
            manifest["repository_source"] = "prepared image"
            manifest["commit"] = prepared["commit"]
            manifest["submodules"] = "prepared image"
        else:
            source = args.repo
            local = Path(os.path.expanduser(str(args.repo)))
            if local.is_dir():
                # The container mounts only the trial, so a repository on the host,
                # such as a project patched for an eval, is cloned from a mirror
                # in the harness folder, with every ref a --ref may name, which is
                # removed once checked out.
                source = harness / "source.git"
                subprocess.run(["git", "clone", "--mirror", "--quiet", "--", str(local), str(source)], check=True)
                manifest["repository_source"] = "local"
            # Fetch only the requested ref, without cloning the default branch or
            # its history first. Explicit refs also support pinned SHAs and PR refs.
            command(["git", "init", "--quiet", str(checkout)])
            command(["git", "remote", "add", "--", "origin", str(source)], cwd=checkout)
            command(["git", "fetch", "--depth=1", "--update-shallow", "--no-tags", "origin", args.ref], cwd=checkout)
            command(["git", "checkout", "--detach", "FETCH_HEAD"], cwd=checkout)
            copies = []
            local_mirrors = False
            if source != args.repo:
                copies.append(source)
                # Relative submodule URLs resolve against the checkout's remote:
                # the source's own origin, or, without one, the mirror, beside
                # which each relative submodule's repository is mirrored too.
                origin = subprocess.run(["git", "-C", str(local), "remote", "get-url", "origin"], capture_output=True, text=True)
                if origin.returncode == 0 and origin.stdout.strip():
                    command(["git", "remote", "set-url", "origin", origin.stdout.strip()], cwd=checkout)
                elif getattr(args,"submodules",False):
                    mirrors = local_submodule_mirrors(checkout, local, source)
                    copies.extend(mirrors)
                    local_mirrors = bool(mirrors)
            manifest["submodules"] = "requested" if getattr(args,"submodules",False) else "not requested"
            if getattr(args,"submodules",False):
                command(submodule_update_argv(local_mirrors), cwd=checkout)
            for copy in copies:
                shutil.rmtree(copy, ignore_errors=True)
            manifest["commit"] = command(["git", "rev-parse", "HEAD"], cwd=checkout)
        if restore_cache:
            phase = "dependency_cache_restore"
            manifest["failure_stage"] = phase
            manifest["dependency_cache"] = json.loads(command(dependency_cache.command("restore", restore_cache)))
            manifest.pop("failure_stage", None)
        phase = "package_manager"
        if pnpm is not None:
            manifest["failure_stage"] = "package_manager"
            if not prepared_case:
                command(["npm", "install", "--prefix", str(tools), "--no-save", "--no-audit", "--no-fund", "pnpm@" + pnpm])
            selected = command(["pnpm", "--version"], cwd=checkout, timeout=CLI_STARTUP_TIMEOUT)
            if selected != pnpm and not selected.startswith(pnpm + "."):
                raise RuntimeError(f"Requested pnpm {pnpm}; pnpm {selected} runs in the checkout")
            manifest["package_manager"] = {"pnpm": selected, "requested": pnpm}
            manifest.pop("failure_stage", None)
        requested_python_version = None
        if python is not None:
            manifest["failure_stage"] = "python_environment"
            requested_python_version = command([str(python), "-c", "import platform; print(platform.python_version())"])
            manifest["python_runtime"] = {"requested": str(python), "requested_version": requested_python_version}
            if not prepared_case:
                command([str(python), "-m", "venv", env["VIRTUAL_ENV"]])
        phase = "setup"
        for setup in setup_commands:
            command(setup, cwd=checkout)
        if binstubs and not prepared_case:
            manifest["failure_stage"] = "bundle_binstubs"
            command(["env", f"BUNDLE_BIN={bundle_bin}", "bundle", "binstubs", *binstubs], cwd=checkout)
            manifest["bundle_binstubs"] = {"directory": inside(bundle_bin), "gems": binstubs,
                                           "executables": binstub_names(bundle_bin)}
            manifest.pop("failure_stage", None)
        if framework == "pytest":
            manifest["failure_stage"] = "python_environment"
            runtime = json.loads(command(["python", "-c", "import json, platform, sys; print(json.dumps({'executable': sys.executable, 'version': platform.python_version(), 'prefix': sys.prefix}))"]))
            manifest["python_runtime"] = {"requested": str(python) if python else None, "requested_version": requested_python_version, **runtime}
            if python is not None and Path(runtime["prefix"]) != Path(env["VIRTUAL_ENV"]):
                raise RuntimeError("Selected Python environment did not run; inspect python_runtime")
            if python is not None and runtime["version"] != requested_python_version:
                raise RuntimeError("Dependency setup changed the selected Python version; inspect python_runtime")
            manifest.pop("failure_stage", None)
        status = command(["git", "status", "--porcelain"], cwd=checkout)
        if status:
            raise RuntimeError("Dependency setup changed the checkout; choose setup that leaves it clean")
        phase = "readiness"
        manifest["failure_stage"] = "runtime_readiness"
        readiness = getattr(args,"readiness_command",[])
        for ready in readiness:
            command(shlex.split(ready),cwd=checkout)
        # The agent must start from the clean checkout, as after setup.
        if readiness and command(["git", "status", "--porcelain"], cwd=checkout):
            raise RuntimeError("A readiness command changed the checkout; choose readiness checks that leave it clean")
        manifest.pop("failure_stage",None)
        # Setup may commit (catalog trims do): the agent's changes start here,
        # while "commit" stays the fetched ref's SHA.
        manifest["agent_baseline"] = command(["git", "rev-parse", "HEAD"], cwd=checkout)
        if prepare_cache:
            phase = "cache_warmup"
            for warm in getattr(args, "warmup_command", None) or []:
                command(dependency_cache.warmup(warm), cwd=checkout)
            # Stop package-manager descendants before sealing their caches.
            container.stop_processes()
            phase = "dependency_cache_capture"
            command(dependency_cache.command("capture", prepare_cache))
            manifest["prepared_cache"] = prepare_cache
            manifest["status"] = "prepared"
        else:
            phase = "agent"
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
            turn_argv, turn_input = agent_argv, prompt
            while True:
                # Records from before this turn are logged but are not its result.
                collect_ddtest_results(len(agent_argvs))
                agent_completed, last_result, logged_result = False, None, None
                agent_argvs.append(turn_argv)
                command(turn_argv, cwd=checkout, agent=True, stdin=turn_input)
                if background.unfinished():
                    manifest["unfinished_background_commands"] = background.unfinished()
                    manifest["failure_kind"] = "infrastructure"
                    manifest["retry_recommended"] = True
                    raise RuntimeError("CLI exited with unfinished Bash tasks; local validation was interrupted, not completed")
                if not agent_completed and not agent_failed:
                    raise RuntimeError("CLI exited without a recognized completion event; inspect the activity")
                if agent_failed:
                    break
                # The turn ended at a question only the user can answer: reply in
                # the same conversation, or stop and record the question.
                # The harness log holds every ddtest result, including one the agent
                # redirected to a file or reformatted; a tool output is the fallback.
                logged_result = collect_ddtest_results(len(agent_argvs)) or logged_result
                asked = user_answers.question(logged_result or last_result)
                turn_input = answer_question(asked, "ddtest log" if logged_result else "tool output") if asked else None
                if turn_input is None:
                    break
                turn_argv = resume_command(args.agent, agent_argv, session_id)
            manifest["status"] = "agent_failed" if agent_failed else "completed"
        code = 1 if agent_failed else 0
    except (OSError, RuntimeError, TimeoutError, ValueError, KeyboardInterrupt, subprocess.SubprocessError) as error:
        manifest["status"] = "interrupted" if isinstance(error, KeyboardInterrupt) else "failed"
        agent_timed_out = isinstance(error, TimeoutError) and bool(manifest["commands"]) and manifest["commands"][-1]["argv"] in agent_argvs
        if isinstance(error, TimeoutError) and manifest["commands"][-1]["argv"] == [args.agent, "--version"]:
            manifest["failure_kind"] = "infrastructure"
            manifest["failure_stage"] = "cli_startup"
            manifest["retry_recommended"] = True
        if manifest.get("failure_stage")=="model_authentication":
            manifest["failure_kind"]="authentication"
            manifest["retry_recommended"]=False
        elif manifest.get("failure_stage") in ("runtime_readiness","storage_readiness","package_manager","python_environment","bundle_binstubs","dependency_cache_restore"):
            manifest["failure_kind"]="evaluator_environment"
            manifest["retry_recommended"]=False
        if manifest.get("status")=="preparing" and not manifest.get("failure_kind"):
            manifest["failure_kind"]="evaluator_environment"
            manifest["retry_recommended"]=False
        manifest["error"] = str(error) or "Interrupted"
        print(redact(manifest["error"]), file=sys.stderr)
    finally:
        phase = "collection"
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
        # its diff and status would not be stable evidence. The diff starts
        # from the post-setup baseline, or the fetched commit if setup failed.
        if "commit" in manifest and manifest["isolation"].get("processes_stopped", True):
            baseline = manifest.get("agent_baseline", manifest["commit"])
            for name, argv in (
                ("changes.patch", ["git", "diff", "--no-ext-diff", "--no-textconv", baseline, "--"]),
                # Default quoting keeps status.txt ASCII; run_catalog.py decodes it.
                ("status.txt", ["git", "-c", "core.quotePath=true", "status", "--short", "--untracked-files=all"]),
            ):
                try:
                    with (output / name).open("w", newline="", errors="surrogateescape") as destination:
                        command(argv, cwd=checkout, timeout=30, destination=destination)
                except (OSError, subprocess.SubprocessError, RuntimeError, TimeoutError) as error:
                    code = finalization_failed(str(error))
        try:
            cleanup_started = time.monotonic()
            try:
                container.close()
            finally:
                manifest["container_cleanup_seconds"] = time.monotonic() - cleanup_started
            manifest["isolation"]["cleanup"] = "removed"
        except (OSError, RuntimeError, subprocess.SubprocessError) as error:
            manifest["isolation"]["cleanup"] = "failed"
            manifest["status"] = "failed"
            manifest.setdefault("collection_errors",[]).append(str(error))
            code = 1
        # The agent has stopped: log any remaining ddtest records.
        collect_ddtest_results(len(agent_argvs))
        ddtest_log.close()
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
        report = {}
        try:
            # Trial code may have replaced the checkout or its report folder with
            # a symlink (and processes may outlive a failed container removal),
            # so the report is read by walking open folders, never following one.
            encoded = read_trial_report(trial, checkout, 16*1024*1024)
            if encoded is not None:
                if len(encoded)>16*1024*1024:
                    raise ValueError("Compact report exceeds 16 MiB")
                report = json.loads(encoded)
                if not isinstance(report,dict):
                    raise ValueError("Report must be a JSON object")
        except (OSError, ValueError) as error:
            code = finalization_failed(str(error))
        try:
            manifest["agent_reporting"] = ({"status": "not run", "reason": "Dependency cache preparation only."}
                                           if prepare_cache else reporting_verdict(final_response, report, inside(checkout)))
            manifest["ddtest_runtime"] = report_runtime(report, manifest.get("node_runtime", {}).get("version"))
            for language, name in (("ruby", "Ruby"), ("java", "Java"), ("python", "Python")):
                if language+"_runtime" not in manifest:
                    continue
                selected_runtime = manifest[language+"_runtime"]
                # Setup can fail after recording the request, before probing identity.
                if language == "python" and not all(selected_runtime.get(key) for key in ("version", "executable", "prefix")):
                    continue
                runtime_audit = (report_python_runtime(report, selected_runtime) if language == "python" else
                                 report_preflight_runtime(report, selected_runtime["version"]))
                manifest["ddtest_"+language+"_runtime"] = runtime_audit
                missing_python_runtime = (
                    language == "python" and runtime_audit["status"] == "not observed"
                    and bool(report.get("runs")) and not report.get("check_only")
                    and (report.get("last_execution") or {}).get("current", True))
                if runtime_audit["status"] == "mismatch" or missing_python_runtime:
                    manifest["status"], manifest["failure_kind"] = "failed", "evaluator_environment"
                    manifest["failure_stage"] = "ddtest_"+language+"_runtime"
                    manifest["error"] = ("Final ddtest Python runtime identity is missing from current execution evidence"
                                         if missing_python_runtime else
                                         "Final ddtest "+name+" runtime differs from the selected image runtime")
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
        manifest["timings_seconds"] = {"total": time.monotonic() - started, "commands": phase_totals}
        (output / "run.json").write_text(json.dumps(redact(manifest), indent=2) + "\n")
        print(f"Evidence: {output}\nDisposable checkout retained for evaluation: {trial}")
    return code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cleanup", type=Path, help="Remove the verified owned trial recorded in this output directory")
    parser.add_argument("--image", help="Prebuilt Docker image ID or repository@sha256 digest; required for trials")
    parser.add_argument("--dependency-cache", help="Expected fingerprint of the dependency cache baked into --image")
    parser.add_argument("--prepare-cache", help="Prepare dependencies without model authentication or an agent; write a cache archive")
    parser.add_argument("--warmup-command", action="append", default=[],
                        help="With --prepare-cache only: warm build dependencies in a disposable copy before capturing caches")
    parser.add_argument("--login-json", type=Path, help="Selected model login; refresh updates persist, never copies a profile")
    parser.add_argument("--existing-login", action="store_true", help="Reuse authorized existing CLI model authentication, including a managed Claude helper")
    parser.add_argument("--submodules", action="store_true", help="Initialize submodules at the requested commit")
    parser.add_argument("--case-image", action="store_true",
                        help="Restore /opt/ddtest-case from the immutable image; skip clone and setup")
    parser.add_argument("--repo", help="Public clone URL reachable from the container, or a local repository, which the harness copies into the trial")
    parser.add_argument("--ref", help="Commit SHA or ref; resolved SHA is recorded")
    parser.add_argument("--ddtest", type=Path, help="Already-built Linux executable for the image architecture")
    parser.add_argument("--agent", choices=("codex", "claude"))
    parser.add_argument("--node", type=Path,
                        help="Absolute Node executable for setup and Codex non-login shells; Codex only")
    parser.add_argument("--python", type=Path, help="Absolute Python executable inside the image; create and activate a private trial venv before setup")
    parser.add_argument("--framework", choices=FRAMEWORKS,
                        help="Optional scope for mixed-framework repositories; adds only the framework to the minimal prompt")
    parser.add_argument("--readiness-command", action="append", default=[], help="Repeatable native runtime/browser readiness checks before the uncoached agent")
    parser.add_argument("--setup-command", action="append", default=[], help="Repeatable, no implicit shell")
    parser.add_argument("--pnpm", help="Install this pnpm version privately for setup and the agent (e.g. the version the project's CI pins)")
    parser.add_argument("--output", type=Path, help="New directory for evaluator evidence")
    parser.add_argument("--model", help="Optional requested model; default is the CLI's configured model")
    parser.add_argument("--ddtest-label", help="Free-text ddtest provenance, such as its source commit, recorded in run.json "
                                               "(a plain go build reports version dev)")
    parser.add_argument("--timeout", type=float, default=1800, help="Seconds allowed per command, each agent turn included")
    parser.add_argument("--user-answer", action="append", default=[], metavar="KIND=VALUE",
                        help="Repeatable scripted reply to a ddtest question only the user can answer: ci_runtime=<runtime>=<version>, "
                             "ci_runtime=<workflow / job>:<runtime>=<version>, ci_confirm=<fact>[=<value>] or ci_provider=<provider>")
    parser.add_argument("--user-answers", type=Path, help="File with one --user-answer value per line; blank lines and # comments are ignored")
    parser.add_argument("--max-user-turns", type=int, default=5, help="Most scripted replies in one trial (default 5)")
    parser.add_argument("--bundle-binstubs", action="append", default=[], metavar="GEM",
                        help="Repeatable: after setup, write Bundler binstubs of this gem (such as rspec-core) to a private folder on PATH")
    parser.add_argument("--excerpt-limit", type=int, default=EXCERPT_LIMIT,
                        help=f"Characters kept of each text in activity.jsonl (default {EXCERPT_LIMIT}; start and end are kept)")
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
