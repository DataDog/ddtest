"""Docker boundary for untrusted repositories, setup scripts and coding agents."""

import contextlib
import json
import base64
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time
import uuid


# Versions checked by the runner and installed by the adjacent Dockerfile.
CLI_VERSIONS = {"codex": "0.163.0", "claude": "2.1.220"}
BASE_ENVIRONMENT = ("PATH", "LANG", "TERM", "USER", "SHELL")


def environment(home):
    """Never forward credentials, parent sessions, or arbitrary npm options."""
    result = {key: os.environ[key] for key in BASE_ENVIRONMENT if key in os.environ}
    result.update(HOME=str(home), GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null",
                  GIT_TERMINAL_PROMPT="0")
    return result


def validate_image(image):
    if not re.fullmatch(r"(?:sha256:|[a-z0-9][a-z0-9./:_-]*@sha256:)[a-f0-9]{64}",image or ""):
        raise ValueError("--image must be a prebuilt immutable image ID or repository@sha256 digest")


# Runtime locations a trusted image may declare, such as an architecture-
# matching browser driver; every other image variable is unset per command.
IMAGE_RUNTIME_VARIABLES = ("CHROMEDRIVER_PATH",)
CONTROL_TIMEOUT = 20
TRIAL_ROOT_VARIABLE = "DDTEST_EVAL_TRIAL_ROOT"
CONTAINER_NAME = re.compile(r"ddtest-eval-[a-f0-9]{32}")


def docker_control(*arguments, check=True):
    """Every Docker control failure, including a slow daemon, is a RuntimeError."""
    try:
        return subprocess.run(["docker", *arguments], capture_output=True, text=True,
                              check=check, timeout=CONTROL_TIMEOUT)
    except subprocess.CalledProcessError as error:
        raise RuntimeError("Docker control failed: " + error.stderr.strip()) from error
    except subprocess.TimeoutExpired as error:
        raise RuntimeError(f"Docker control timed out after {CONTROL_TIMEOUT}s: docker {arguments[0]}") from error


# Docker injects proxies from the client's config.json, credentials included,
# into every container's initial environment. Empty values prevent that.
PROXY_OVERRIDES = [argument for name in ("HTTP_PROXY", "HTTPS_PROXY", "FTP_PROXY", "ALL_PROXY", "NO_PROXY")
                   for variable in (name, name.lower()) for argument in ("--env", variable + "=")]


class Container:
    """Only trial storage is mounted; the evaluator and evidence stay on the host."""

    cli_versions = CLI_VERSIONS
    path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

    def __init__(self, trial, image):
        validate_image(image)
        self.trial = trial
        self.image = image
        self.name = "ddtest-eval-" + uuid.uuid4().hex
        self.connection = None
        # Variables the container itself sets (image ENV, HOSTNAME); unset per
        # command so only the allowlist remains, as `env -i` used to ensure.
        self.container_environment = []
        # The init and keep-alive processes; everything else may be stopped.
        self.keepalive = []

    def control(self, *arguments, check=True):
        # Docker's own context can use host configuration. It is never mounted or
        # passed to commands inside the container. Images must be preloaded.
        return docker_control(*arguments, check=check)

    def start(self):
        try:
            self.create()
        except RuntimeError as error:
            if "bind source path does not exist" not in str(error):
                raise
            raise RuntimeError(f"Docker cannot see the trial folder {self.trial}: share {self.trial.parent} "
                               f"with the Docker VM (Colima shares only $HOME by default; add it under mounts: "
                               f"in ~/.colima/<profile>/colima.yaml and restart Colima), or set {TRIAL_ROOT_VARIABLE} "
                               "to a folder the VM already shares") from error
        self.attach()

    def create(self):
        # --rm: if the runner is killed, closing the controller pipe ends PID 1 and
        # Docker removes the stopped container instead of leaving it behind.
        self.control("create", "--rm", "--pull=never", "--name", self.name, "--interactive", "--init",
                     "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
                     "--pids-limit=2048", "--memory=6g", "--cpus=4", "--shm-size=1g",
                     "--user", f"{os.getuid()}:{os.getgid()}",
                     "--mount", f"type=bind,src={self.trial},dst=/trial",
                     "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g", "--entrypoint", "/bin/sh",
                     *PROXY_OVERRIDES, self.image, "-c", "cat >/dev/null")

    def attach(self):
        # Closing this input on controller death also ends PID 1, including all
        # detached processes in its PID namespace. No process scanning race.
        self.connection = subprocess.Popen(["docker", "start", "--attach", "--interactive", self.name],
                                           stdin=subprocess.PIPE, stdout=subprocess.DEVNULL,
                                           stderr=subprocess.DEVNULL, start_new_session=True)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            state = self.control("inspect", "--format", "{{.State.Running}}", self.name)
            if state.stdout.strip() == "true":
                configured = self.control("inspect", "--format", "{{json .Config.Env}}", self.name)
                self.container_environment = [item.partition("=")[0] for item in json.loads(configured.stdout or "[]")]
                # Docker adds HOSTNAME to every exec without listing it in Config.Env.
                if "HOSTNAME" not in self.container_environment:
                    self.container_environment.append("HOSTNAME")
                listing = self.control("exec", self.name, "/bin/sh", "-c", LIST_PROCESSES)
                self.keepalive = listing.stdout.split()
                return
            if self.connection.poll() is not None:
                break
            time.sleep(0.05)
        raise RuntimeError("Container did not start; inspect the Docker daemon and image")

    def inside(self, value):
        return str(value).replace(str(self.trial), "/trial")

    def execute(self, argv, cwd, env, timeout, on_line, prompt, executor):
        argv = [self.inside(value) for value in argv]
        cwd = self.inside(cwd)
        env = {key: self.inside(value) for key, value in env.items()}
        prompt = self.inside(prompt) if prompt else prompt
        # Values never appear on the docker command line, where any local
        # process can read them with ps. The private env file is read once by
        # the docker CLI and removed when the command ends.
        decode = []
        for key, value in list(env.items()):
            if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key) or "\0" in value:
                raise ValueError(f"Environment entry {key!r} cannot be forwarded to the container")
            if "\n" in value:
                # The env file holds one line per entry; restore multi-line
                # values (such as custom model headers) inside the container.
                del env[key]
                env["DDTEST_EVAL_B64_" + key] = base64.b64encode(value.encode()).decode()
                decode.append(key)
        if decode:
            restore = "".join(f'{key}="$(printf %s "$DDTEST_EVAL_B64_{key}" | base64 -d)"; export {key}; unset DDTEST_EVAL_B64_{key}; ' for key in decode)
            argv = ["/bin/sh", "-c", restore + 'exec "$@"', "sh", *argv]
        descriptor, env_file = tempfile.mkstemp(prefix="ddtest-eval-env-")
        try:
            with os.fdopen(descriptor, "w") as stream:
                stream.writelines(f"{key}={value}\n" for key, value in env.items())
            arguments = ["docker", "exec", "--interactive", "--env-file", env_file, "--workdir", str(cwd),
                         self.name, "/usr/bin/env"]
            for key in self.container_environment:
                if key not in env and key not in IMAGE_RUNTIME_VARIABLES:
                    arguments.extend(["-u", key])
            arguments.extend(argv)
            return executor(arguments, self.trial, os.environ, timeout, on_line, prompt)
        finally:
            os.unlink(env_file)

    def stop_processes(self):
        """Kill every process but init and the keep-alive, so evidence is stable."""
        if not self.keepalive:
            raise RuntimeError("Container processes were not recorded at start; cannot stop the agent safely")
        result = self.control("exec", self.name, "/bin/sh", "-c", STOP_PROCESSES, "sh", " ".join(self.keepalive), check=False)
        if result.returncode:
            raise RuntimeError("Agent processes could not be stopped: " + (result.stderr.strip() or "unknown error"))

    def close(self):
        try:
            remove_container(self.name)
        finally:
            if self.connection is not None:
                self.connection.stdin.close()
                try:
                    self.connection.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    os.killpg(self.connection.pid, signal.SIGKILL)
                    self.connection.wait(timeout=2)


# Every PID in the container's namespace except this listing shell itself.
LIST_PROCESSES = 'for p in /proc/[0-9]*; do pid=${p#/proc/}; [ "$pid" = "$$" ] || echo "$pid"; done'

# Kill everything outside the recorded keep-alive set until nothing is left.
# Zombies are skipped: init reaps them, and they can no longer write.
STOP_PROCESSES = """keep=" $1 $$ "
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  left=
  for p in /proc/[0-9]*; do
    pid=${p#/proc/}
    case "$keep" in *" $pid "*) continue;; esac
    if grep -q '^State:[[:space:]]*Z' "$p/status" 2>/dev/null; then continue; fi
    kill -KILL "$pid" 2>/dev/null && left="$left $pid"
  done
  [ -z "$left" ] && exit 0
  sleep 0.2
done
echo "processes remain:$left" >&2
exit 1"""


def missing(result):
    # Docker CLI versions differ in case: "No such container", "error: no such object".
    return result.returncode != 0 and "no such" in result.stderr.lower()


def remove_container(name):
    """Remove a container and confirm it is gone, including an --rm already in progress."""
    result = docker_control("rm", "--force", name, check=False)
    if result.returncode and not missing(result) and "already in progress" not in result.stderr:
        raise RuntimeError("Container cleanup failed: " + result.stderr.strip())
    deadline = time.monotonic() + CONTROL_TIMEOUT
    while time.monotonic() < deadline:
        result = docker_control("inspect", "--format", "{{.Id}}", name, check=False)
        if missing(result):
            return
        time.sleep(0.1)
    raise RuntimeError("Container cleanup failed: " + name + " is still present")


_cancellation = None


@contextlib.contextmanager
def interrupted():
    """Signal handlers only set state; teardown is never interrupted halfway."""
    global _cancellation
    previous, outer = {}, _cancellation
    state = {"signal": None, "finishing": False}
    def handle(number, _frame):
        state["signal"] = number
    try:
        _cancellation = state
        for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            previous[number] = signal.signal(number, handle)
        yield state
    finally:
        _cancellation = outer
        for number, handler in previous.items():
            signal.signal(number, handler)


def check_interrupted():
    if _cancellation and _cancellation["signal"] and not _cancellation["finishing"]:
        raise KeyboardInterrupt(f"Interrupted by signal {_cancellation['signal']}")


def begin_teardown():
    if _cancellation:
        _cancellation["finishing"] = True


def termination_signal():
    return _cancellation["signal"] if _cancellation else None


def trial_root():
    configured = os.environ.get(TRIAL_ROOT_VARIABLE)
    if configured:
        root = Path(configured)
        if not root.is_absolute() or not root.is_dir():
            raise ValueError(f"{TRIAL_ROOT_VARIABLE} must be an existing absolute directory")
    elif sys.platform == "darwin":
        # Colima shares only $HOME with its VM by default, and Docker Desktop shares
        # /Users; the system temporary roots are not visible to Colima. Keep the
        # name short: Watchman's Unix socket lives inside the trial.
        root = Path.home() / ".dde"
        if root.is_symlink():
            raise ValueError(f"{root} must not be a symlink; set {TRIAL_ROOT_VARIABLE} to another folder")
        root.mkdir(mode=0o700, exist_ok=True)
    else:
        root = Path(tempfile.gettempdir())
    return root.resolve()


# Agents bind sockets inside the Linux container: 108 bytes, terminator included.
SOCKET_PATH_LIMIT = 107


def check_socket_budget(root, user):
    """Watchman binds state/watchman/<user>-state/sock inside each trial."""
    example = Path(root) / "dde-xxxxxxxx" / "state" / "watchman" / (user + "-state") / "sock"
    if len(str(example).encode()) > SOCKET_PATH_LIMIT:
        raise ValueError(f"Trial root {root} is too long for Watchman's Unix socket; set {TRIAL_ROOT_VARIABLE} "
                         "to a shorter folder the Docker VM shares")


def mark_trial(trial, output, container):
    marker = {"trial_directory": str(trial), "output": str(output), "owner": uuid.uuid4().hex,
              "container": container}
    (trial / ".ddtest-eval-owner.json").write_text(json.dumps(marker))
    (output / "ownership.json").write_text(json.dumps(marker))


def cleanup(output):
    """Delete only the exact independently marked root bound to these results."""
    output = Path(output)
    if output.is_symlink():
        raise ValueError("Cleanup refuses symlink evidence directories")
    output = output.resolve(strict=True)
    for name in ("run.json", "ownership.json"):
        if (output / name).is_symlink():
            raise ValueError("Cleanup refuses symlink manifests")
    marker = json.loads((output / "ownership.json").read_text())
    # A runner killed before it wrote run.json leaves only ownership.json.
    run = json.loads((output / "run.json").read_text()) if (output / "run.json").exists() else None
    trial = Path(run["trial_directory"] if run else marker.get("trial_directory", ""))
    if (not trial.is_absolute() or trial.is_symlink() or trial.parent != trial_root()
            or not re.fullmatch(r"dde-[a-zA-Z0-9_-]+", trial.name)
            or marker.get("trial_directory") != str(trial) or marker.get("output") != str(output)
            or not re.fullmatch(r"[a-f0-9]{32}", marker.get("owner", ""))):
        raise ValueError("Cleanup path is not an owned trial in the expected temporary root")
    owner_file = trial / ".ddtest-eval-owner.json"
    if owner_file.is_symlink() or json.loads(owner_file.read_text()) != marker:
        raise ValueError("Cleanup ownership marker does not match")
    if run is None or run.get("isolation", {}).get("cleanup") != "removed":
        remove_recorded_container(marker, trial, run)
    import shutil
    shutil.rmtree(trial)
    return trial


def remove_recorded_container(marker, trial, run):
    """Remove only the container both ownership records name, mounting this trial."""
    name = marker.get("container", "")
    if not CONTAINER_NAME.fullmatch(name) or (run is not None and run.get("isolation", {}).get("name") != name):
        raise ValueError("Container removal is not confirmed and no owned container is recorded; "
                         "retain trial storage and resolve container cleanup first")
    try:
        result = docker_control("inspect", "--format", "{{range .Mounts}}{{println .Source}}{{end}}", name, check=False)
        if missing(result):
            return
        if result.returncode:
            raise RuntimeError(result.stderr.strip())
        if str(trial) not in result.stdout.splitlines():
            raise ValueError(f"Container {name} does not mount the owned trial; remove it manually after review")
        remove_container(name)
    except RuntimeError as error:
        raise ValueError("Container removal is not confirmed: " + str(error)) from error
