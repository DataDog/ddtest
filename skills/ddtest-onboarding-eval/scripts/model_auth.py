"""Reuse explicitly authorized model authentication, without copying CLI profiles."""

import contextlib
import fcntl
import json
import os
from pathlib import Path
import subprocess
import tempfile


def selected_login(data, agent):
    key = "tokens" if agent == "codex" else "claudeAiOauth"
    if not isinstance(data.get(key), dict):
        raise ValueError("Selected login has no supported model credentials")
    result = {key: data[key]}
    if agent == "codex":
        result["auth_mode"] = "chatgpt"
        # Omitting this makes a fresh copy look overdue for refresh, invalidating
        # sibling copies even when its access token was still usable.
        if data.get("last_refresh"):
            result["last_refresh"] = data["last_refresh"]
    return result


def remember(value, secrets):
    if isinstance(value, dict):
        for item in value.values():
            remember(item, secrets)
    elif isinstance(value, str) and len(value) >= 6:
        secrets.add(value)


def login_directory(trial, parts):
    """Open trial/parts without following symlinks; setup commands own the trial
    files, so any component may have been replaced to point outside it."""
    descriptor = os.open(trial, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        for part in parts:
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = child
        if os.fstat(descriptor).st_uid != os.getuid():
            raise OSError("not owned by this user")
        return descriptor
    except OSError as error:
        os.close(descriptor)
        raise RuntimeError("CLI login directory " + "/".join(parts) +
                           " is not a plain directory in the trial (" + (error.strerror or str(error)) +
                           "); refusing to install or remove the login") from error


def install_login(login, trial):
    """Write the login after setup, through descriptors that never leave the trial."""
    if login is None:
        return
    descriptor = login_directory(trial, login["directory"])
    try:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(login["name"], dir_fd=descriptor)
        file = os.open(login["name"], os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=descriptor)
        with os.fdopen(file, "w") as stream:
            os.fchmod(stream.fileno(), 0o600)
            stream.write(login["payload"])
    finally:
        os.close(descriptor)


def remove_login(login, trial):
    """A retained trial must not keep the agent's login."""
    descriptor = login_directory(trial, login["directory"])
    try:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(login["name"], dir_fd=descriptor)
    finally:
        os.close(descriptor)


class ModelLogin:
    """Serialize evaluator refreshes and persist rotation with compare-and-swap.

    The lock coordinates evaluators, not unrelated host CLIs. If another client
    changes the login while a trial runs, never overwrite its newer credentials.
    """
    def __init__(self, agent, home, secrets, path=None, existing=False):
        self.agent, self.home, self.secrets = agent, home, secrets
        self.path = Path(path) if path else None
        self.existing = existing
        self.lock = None
        self.destination = home / (".codex/auth.json" if agent == "codex" else ".claude/.credentials.json")
        self.original = None
        self.initial = None
        self.env = {}
        self.suspended = None

    def install(self):
        if self.existing and self.path is None:
            if self.agent == "claude":
                self.env = managed_claude()
                if self.env:
                    remember(self.env, self.secrets)
                    return
            self.path = Path.home() / (".codex/auth.json" if self.agent == "codex" else ".claude/.credentials.json")
        if self.path is None:
            return
        # Lock outside disposable storage, shared by all trials using this login.
        self.lock = self.path.with_name(self.path.name + ".ddtest-eval.lock").open("a")
        os.chmod(self.lock.name, 0o600)
        try:
            fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            self.lock.close()
            self.lock = None
            raise ValueError("This model login is in use by another evaluation; serialize trials for this account") from error
        self.original = self.path.read_bytes()
        self.initial = selected_login(json.loads(self.original), self.agent)
        remember(self.initial, self.secrets)
        install_login(self.payload(self.initial), self.home.parent)

    def payload(self, value):
        return {"directory": ("home", self.destination.parent.name),
                "name": self.destination.name, "payload": json.dumps(value)}

    def check_destination(self):
        descriptor = login_directory(self.home.parent, ("home", self.destination.parent.name))
        os.close(descriptor)

    def read_destination(self):
        descriptor = login_directory(self.home.parent, ("home", self.destination.parent.name))
        try:
            try:
                file = os.open(self.destination.name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=descriptor)
            except FileNotFoundError:
                return None
            with os.fdopen(file) as stream:
                return selected_login(json.load(stream), self.agent)
        finally:
            os.close(descriptor)

    def suspend(self):
        self.check_destination()
        # Dependency scripts must not see model credentials during setup.
        self.suspended = self.read_destination()
        remove_login(self.payload(None), self.home.parent)

    def resume(self):
        self.check_destination()
        if self.suspended is not None:
            install_login(self.payload(self.suspended), self.home.parent)

    def close(self, persist=True):
        try:
            self.check_destination()
            if self.initial is not None and persist:
                updated = self.read_destination() or self.suspended or self.initial
                remember(updated, self.secrets)
                if updated != self.initial:
                    if self.path.read_bytes() != self.original:
                        raise ValueError("Model login changed outside this trial; refreshed credentials were not written over it")
                    # Preserve host-only metadata and replace only authentication.
                    original = json.loads(self.original)
                    original.update(updated)
                    descriptor, name = tempfile.mkstemp(prefix=".ddtest-auth-", dir=self.path.parent)
                    try:
                        with os.fdopen(descriptor, "w") as target:
                            json.dump(original, target)
                        os.replace(name, self.path)
                    finally:
                        Path(name).unlink(missing_ok=True)
        finally:
            # Never traverse a directory replaced by untrusted setup/agent code.
            try:
                remove_login(self.payload(None), self.home.parent)
            finally:
                if self.lock:
                    fcntl.flock(self.lock, fcntl.LOCK_UN)
                    self.lock.close()
                    self.lock = None


def managed_claude():
    """Evaluate only a trusted host model helper; never mount its configuration."""
    settings = {}
    for path in (Path.home()/".claude/settings.json",
                 Path("/Library/Application Support/ClaudeCode/managed-settings.json")):
        if path.is_file():
            config = json.loads(path.read_text())
            settings.update({key: config[key] for key in ("apiKeyHelper", "env") if key in config})
    config = settings.get("env", {})
    # Routing for the model service only. No OTEL, MCP, package or cloud settings.
    result = {key: config[key] for key in ("ANTHROPIC_BASE_URL", "ANTHROPIC_CUSTOM_HEADERS",
              "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY") if isinstance(config.get(key), str)}
    helper = settings.get("apiKeyHelper")
    if helper:
        response = subprocess.run(helper, shell=True, capture_output=True, text=True, timeout=30)
        if response.returncode or not response.stdout.strip():
            raise ValueError("The configured Claude model authentication helper failed")
        token = response.stdout.strip()
        result["ANTHROPIC_AUTH_TOKEN" if token.startswith("eyJ") else "ANTHROPIC_API_KEY"] = token
    if not any(key in result for key in ("ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY")):
        return {}
    return result
