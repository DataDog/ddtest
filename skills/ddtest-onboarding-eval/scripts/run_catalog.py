#!/usr/bin/env python3
"""Run standard catalog cases through run_eval.py, one after another.

Each case: run_eval.py with the catalog's image, commit, trims, setup, readiness,
flags and answers -> retain the .testoptimization reports and small untracked
files -> run_eval.py --cleanup. Required trims are always applied, speed trims
only with --speed-trims; each is committed with a scratch identity inside the
trial container, so the checkout stays clean; run_eval.py records the HEAD after
setup as `agent_baseline`, so changes.patch excludes the trims.

After each case its evidence is checked against the case's `expected` outcome
(catalog_check.py); the batch ends with a summary table, an ERROR block per case
that did not pass, and <output>/results.json. --evaluate-only checks existing
evidence folders without running anything.

Exit status: 0 every case passed; 1 a regression, unexpected blocker, harness
or agent failure; 3 only improvements (update `expected`); 128+N interrupted.
"""

import argparse
import json
import os
from pathlib import Path
import re
import shlex
import signal
import stat
import subprocess
import sys
import threading

import catalog_check
import run_eval
import user_answers

SCRIPTS = Path(__file__).resolve().parent
CATALOG = SCRIPTS.parent / "catalog" / "cases.yaml"
RUN_EVAL = SCRIPTS / "run_eval.py"
IMAGES_VARIABLE = "DDTEST_EVAL_IMAGES"
PROVIDERS = ("github", "gitlab", "circleci", "buildkite", "jenkins")
CASE_FIELDS = ("id", "framework", "language", "ci_provider", "ci_config_path", "repo", "commit", "subdir", "image",
               "runtime", "system_needs", "browsers", "services", "setup_commands", "readiness_commands", "harness",
               "trims", "timings_seconds", "tests", "prepared_image", "notes", "risks", "expected")
HARNESS_FIELDS = ("framework", "python", "pnpm", "bundle_binstubs", "answers")
TIMINGS = ("clone", "install", "browser", "build", "tests_full", "tests_trimmed")
TRIM_KINDS = ("required", "optional_speed")
EXPECTED_FIELDS = ("verdict", "reason", "run", "ddtest")
# Trims are committed inside the disposable trial container, whose private HOME
# has no Git configuration; only the identity is supplied here.
SCRATCH_COMMIT = ["git", "-c", "user.name=ddtest-eval-scratch", "-c", "user.email=scratch@example.invalid",
                  "commit", "-q", "-a", "-m"]
DEFAULT_TIMEOUT = 5400
UNTRACKED_LIMIT = 200_000
REPORT_LIMIT = 20_000_000


def load_yaml(path):
    try:
        import yaml
    except ImportError:
        raise SystemExit("run_catalog.py needs PyYAML: python3 -m pip install pyyaml")
    with open(path) as stream:
        return yaml.safe_load(stream)


def load_catalog(path=CATALOG):
    return load_yaml(path)


def validate(catalog):
    """Return the catalog's schema problems; an empty list means valid."""
    problems = []
    images = catalog.get("images") or {}
    for key, image in images.items():
        if not isinstance(image, dict) or not image.get("target") or not isinstance(image.get("build_args", {}), dict):
            problems.append(f"image {key}: needs a target and build_args mapping")
    cases = catalog.get("cases") or []
    ids = [case.get("id") for case in cases]
    problems += [f"duplicate id {identifier}" for identifier in sorted({i for i in ids if ids.count(i) > 1})]
    frameworks = [case.get("framework") for case in cases]
    for framework in run_eval.FRAMEWORKS:
        if frameworks.count(framework) != 1:
            problems.append(f"framework {framework}: {frameworks.count(framework)} cases, want 1")
    problems += [f"unsupported framework {f}" for f in frameworks if f not in run_eval.FRAMEWORKS]
    missing_providers = set(PROVIDERS) - {case.get("ci_provider") for case in cases}
    if missing_providers:
        problems.append("providers not covered: " + ", ".join(sorted(missing_providers)))
    for case in cases:
        name = case.get("id", "?")
        problems += [f"{name}: missing {field}" for field in CASE_FIELDS if field not in case]
        if not re.fullmatch(r"E\d{2}-[a-z0-9-]+", str(name)):
            problems.append(f"{name}: id must look like E01-name")
        if not re.fullmatch(r"[0-9a-f]{40}", str(case.get("commit"))):
            problems.append(f"{name}: commit must be 40 lowercase hex characters")
        if case.get("ci_provider") not in PROVIDERS:
            problems.append(f"{name}: unknown ci_provider {case.get('ci_provider')}")
        if case.get("image") not in images:
            problems.append(f"{name}: image {case.get('image')} is not in images")
        harness = case.get("harness") or {}
        problems += [f"{name}: harness missing {field}" for field in HARNESS_FIELDS if field not in harness]
        if harness.get("framework") != case.get("framework"):
            problems.append(f"{name}: harness.framework differs from framework")
        for answer in harness.get("answers") or []:
            try:
                user_answers.parse_answer(answer)
            except ValueError as error:
                problems.append(f"{name}: answer {answer}: {error}")
        trims = case.get("trims") or {}
        if set(trims) != set(TRIM_KINDS):
            problems.append(f"{name}: trims needs exactly {', '.join(TRIM_KINDS)}")
        for kind in TRIM_KINDS:
            trim = trims.get(kind)
            if trim is not None and (not trim.get("reason") or not trim.get("commands")):
                problems.append(f"{name}: trims.{kind} needs a reason and commands")
        commands = [*case.get("setup_commands", []), *case.get("readiness_commands", []),
                    *(command for kind in TRIM_KINDS for command in (trims.get(kind) or {}).get("commands", []))]
        for command in commands:
            try:
                if not shlex.split(command):
                    raise ValueError("empty")
            except ValueError as error:
                problems.append(f"{name}: command {command!r}: {error}")
        timings = case.get("timings_seconds") or {}
        problems += [f"{name}: timings_seconds missing {field}" for field in TIMINGS if field not in timings]
        if (timings.get("tests_trimmed") is None) != all(trims.get(kind) is None for kind in TRIM_KINDS):
            problems.append(f"{name}: timings_seconds.tests_trimmed is set exactly when the case has a trim")
        problems += validate_expected(name, case.get("expected"))
    return problems


def validate_expected(name, expected):
    if not isinstance(expected, dict):
        return [f"{name}: expected needs {', '.join(EXPECTED_FIELDS)}"]
    problems = [f"{name}: expected.{field} is missing" for field in EXPECTED_FIELDS if not expected.get(field)]
    unknown = set(expected) - {*EXPECTED_FIELDS, "blockers", "agents"}
    if unknown:
        problems.append(f"{name}: expected has unknown fields {', '.join(sorted(unknown))}")
    overrides = expected.get("agents") or {}
    if not isinstance(overrides, dict):
        return problems + [f"{name}: expected.agents must map an agent to its overrides"]
    for agent, override in overrides.items():
        where = f"{name}: expected.agents.{agent}"
        if agent not in catalog_check.AGENTS:
            problems.append(f"{where}: unknown agent (use {', '.join(catalog_check.AGENTS)})")
        if not isinstance(override, dict) or not override:
            problems.append(f"{where}: needs at least one of {', '.join(catalog_check.OVERRIDE_FIELDS)}")
            continue
        extra = set(override) - set(catalog_check.OVERRIDE_FIELDS)
        if extra:
            problems.append(f"{where}: unknown fields {', '.join(sorted(extra))}")
        if "verdict" in override and not override.get("reason"):
            problems.append(f"{where}: a verdict override needs its own reason")
    for agent in (None, *overrides):
        label = f"{name}: expected" + (f".agents.{agent}" if agent else "")
        resolved = catalog_check.expected_for({"expected": expected}, agent)
        verdict = resolved.get("verdict")
        if verdict not in catalog_check.EXPECTED_VERDICTS:
            problems.append(f"{label}: verdict must be one of {', '.join(catalog_check.EXPECTED_VERDICTS)}")
        if verdict != "COMPLETE LOCALLY" and not resolved.get("reason"):
            problems.append(f"{label}: a {verdict} verdict needs a reason")
        blockers = resolved.get("blockers")
        if blockers is None:
            continue
        if verdict not in catalog_check.BLOCKER_VERDICTS:
            problems.append(f"{label}: blockers only with an INCOMPLETE or FAILED verdict")
        if (not isinstance(blockers, list) or not blockers or len(set(map(str, blockers))) != len(blockers)
                or not all(isinstance(blocker, str) and blocker.strip() for blocker in blockers)):
            problems.append(f"{label}: blockers must be a non-empty list of distinct identifiers")
    return problems


def trim_commands(case, speed_trims=False):
    """Setup commands that apply and commit the case's trims, required first."""
    commands = []
    for kind in TRIM_KINDS[:2 if speed_trims else 1]:
        trim = (case.get("trims") or {}).get(kind)
        if trim:
            commands += [*trim["commands"], shlex.join([*SCRATCH_COMMIT, f"Eval trim ({kind}): {trim['reason']}"])]
    return commands


def build_argv(case, image, ddtest, output, agent="codex", existing_login=False, timeout=DEFAULT_TIMEOUT,
               speed_trims=False, model=None, ddtest_label=None):
    argv = [sys.executable, str(RUN_EVAL), "--image", image, "--repo", case["repo"], "--ref", case["commit"],
            "--ddtest", str(ddtest), "--agent", agent, "--timeout", str(timeout), "--output", str(output)]
    if ddtest_label:
        argv += ["--ddtest-label", ddtest_label]
    if existing_login:
        argv.append("--existing-login")
    if model:
        argv += ["--model", model]
    harness = case["harness"]
    argv += ["--framework", harness["framework"]]
    if harness.get("python"):
        argv += ["--python", harness["python"]]
    if harness.get("pnpm"):
        argv += ["--pnpm", str(harness["pnpm"])]
    for gem in harness.get("bundle_binstubs") or []:
        argv += ["--bundle-binstubs", gem]
    for command in [*trim_commands(case, speed_trims), *case["setup_commands"]]:
        argv += ["--setup-command", command]
    for command in case["readiness_commands"]:
        argv += ["--readiness-command", command]
    for answer in harness.get("answers") or []:
        argv += ["--user-answer", answer]
    return argv


IMAGE_TAG = "ddtest-eval-{key}"


def image_builds(images, keys):
    """The docker build commands for these image keys, and an --image-file for their tags.

    Nothing is built: the commands are printed for the evaluator to run.
    """
    keys = sorted(keys)
    lines = [f"# Images the selected cases need: {', '.join(keys)}. Build each (nothing is built automatically):"]
    for key in keys:
        image = images[key]
        argv = ["docker", "build", "--target", image["target"]]
        for name, value in (image.get("build_args") or {}).items():
            argv += ["--build-arg", f"{name}={value}"]
        argv += ["-t", IMAGE_TAG.format(key=key), "-f", str(SCRIPTS / "Dockerfile"), str(SCRIPTS)]
        lines.append(shlex.join(argv))
    lines.append("# Then write their image IDs to an --image-file (or pass --image-map KEY=IMAGE for each):")
    lines.append("{ printf '{'; sep=; for key in " + " ".join(keys) + "; do printf '%s\"%s\": \"%s\"' \"$sep\" \"$key\" "
                 "\"$(docker image inspect --format '{{.Id}}' " + IMAGE_TAG.format(key="$key") + ")\"; sep=', '; done; "
                 "printf '}\\n'; } > images.json")
    lines.append("# --image-file template:")
    lines.append(json.dumps({key: f"<image ID of {IMAGE_TAG.format(key=key)}>" for key in keys}))
    return lines


def resolve_images(pairs, path=None):
    """Image per catalog key: a JSON/YAML mapping file, then --image-map pairs."""
    images = {}
    path = path or os.environ.get(IMAGES_VARIABLE)
    if path:
        loaded = load_yaml(path)
        if not isinstance(loaded, dict):
            raise ValueError(f"{path}: expected a mapping of image key to image")
        images.update({str(key): str(value) for key, value in loaded.items()})
    for pair in pairs:
        key, separator, image = pair.partition("=")
        if not separator or not key or not image:
            raise ValueError(f"--image-map takes KEY=IMAGE, not {pair}")
        images[key] = image
    return images


# Git's C-style path quoting (quote_c_style): these escapes plus \ooo octal bytes.
GIT_ESCAPES = {"a": 7, "b": 8, "t": 9, "n": 10, "v": 11, "f": 12, "r": 13, '"': 34, "\\": 92}


def unquote_path(text):
    """Decode a path as git status prints it: verbatim, or quoted with escapes."""
    if not (len(text) >= 2 and text[0] == text[-1] == '"'):
        return text
    body, decoded, index = text[1:-1], bytearray(), 0
    while index < len(body):
        character = body[index]
        if character != "\\":
            decoded += character.encode()
            index += 1
        elif body[index + 1:index + 2] in GIT_ESCAPES:
            decoded.append(GIT_ESCAPES[body[index + 1]])
            index += 2
        elif re.fullmatch(r"[0-3][0-7]{2}", body[index + 1:index + 4]):
            decoded.append(int(body[index + 1:index + 4], 8))
            index += 4
        else:
            raise ValueError(f"unknown escape in {text}")
    return os.fsdecode(bytes(decoded))


def untracked_paths(status):
    """(as printed, decoded) for each untracked entry of git status --short."""
    entries = []
    for line in status.splitlines():
        if line.startswith("?? "):
            try:
                path = unquote_path(line[3:])
            except ValueError:
                path = None
            entries.append((line[3:], path))
    return entries


RETENTION = "RETENTION.json"
EXCLUDED_UNTRACKED = (".testoptimization/", "node_modules/", "vendor/", ".m2/", ".gradle/")


def record_retention(output, status, **fields):
    """Write <evidence>/retained/RETENTION.json, the retention outcome catalog_check.py honors."""
    destination = output / "retained"
    destination.mkdir(exist_ok=True)
    temporary = destination / (RETENTION + ".partial")
    temporary.write_text(json.dumps({"status": status, **fields}, indent=2) + "\n")
    temporary.replace(destination / RETENTION)


def copy_beneath(directory_fd, parts, target, limit, shown, skipped):
    """Copy the regular file at `parts` below directory_fd to target, following no symlink.
    Returns whether it was copied; a symlink, a non-regular or a large file is listed in skipped."""
    try:
        data = run_eval.read_beneath(directory_fd, parts, limit)
    except run_eval.SymlinkedPath:
        skipped.append({"path": shown, "reason": "symlink"})
        return False
    except (FileNotFoundError, NotADirectoryError):
        return False
    if data is None or len(data) >= limit:
        skipped.append({"path": shown, "reason": "not a regular file" if data is None else "too large"})
        return False
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(data)
    return True


def open_checkout(run):
    """An fd of the trial checkout, or None when it does not exist.

    Trial code can replace the checkout, or any folder in it, with a symlink
    (to the host's / for example) and can shadow git to list chosen paths, so
    the checkout must sit directly in the trial folder named by run.json and is
    opened without following a symlink; every later path is opened relative to
    it the same way, so nothing outside the checkout can be copied.
    """
    checkout = Path(run["checkout"])
    trial = Path(run.get("trial_directory") or checkout.parent)
    if not checkout.is_absolute() or checkout.parent != trial or checkout.name in ("", ".", ".."):
        raise run_eval.SymlinkedPath(f"checkout {checkout} is not directly inside the trial folder {trial}")
    try:
        trial_fd = run_eval.open_directory(trial)
    except run_eval.SymlinkedPath:
        raise run_eval.SymlinkedPath(f"trial folder {trial} was replaced by a symlink; nothing retained") from None
    except (FileNotFoundError, NotADirectoryError):
        return None
    try:
        return run_eval.open_directory_beneath(trial_fd, [checkout.name])
    except run_eval.SymlinkedPath:
        raise run_eval.SymlinkedPath(f"checkout {checkout} was replaced by a symlink; nothing retained") from None
    except (FileNotFoundError, NotADirectoryError):
        return None
    finally:
        os.close(trial_fd)


def retain(output):
    """Copy the report pair and small untracked files out of the trial checkout.

    Untracked paths come from the container-collected status.txt; Git never runs
    on the host in the agent-modified checkout. Nothing is followed through a
    symlink (see open_checkout). retained/RETENTION.json records the outcome: it
    says "started" until everything was copied, then "ok"; run_trial records
    "failed" with the error when this raises.
    """
    try:
        run = json.loads((output / "run.json").read_text())
        run["checkout"]
    except (OSError, ValueError, KeyError, TypeError) as error:
        return f"nothing retained: {error}"
    destination = output / "retained"
    record_retention(output, "started")
    checkout_fd = open_checkout(run)
    kept, copied, skipped = 0, 0, []
    try:
        status = output / "status.txt"
        untracked = untracked_paths(status.read_text(errors="replace")) if status.is_file() else []
        # Paths as git printed them: quoted names stay on one line.
        (destination / "untracked.txt").write_text("".join(printed + "\n" for printed, _ in untracked))
        try:
            reports = checkout_fd is not None and os.stat(".testoptimization", dir_fd=checkout_fd, follow_symlinks=False)
        except FileNotFoundError:
            reports = None
        if reports and stat.S_ISLNK(reports.st_mode):
            skipped.append({"path": ".testoptimization", "reason": "symlink"})
        elif reports:
            # fwalk without following symlinks never descends into a symlinked folder,
            # and checks that each folder it opens is the one it listed; each
            # folder_fd is valid only until the walk moves on.
            for folder, _, names, folder_fd in os.fwalk(".testoptimization", dir_fd=checkout_fd, follow_symlinks=False):
                for name in sorted(names):
                    kept += copy_beneath(folder_fd, [name], destination / folder / name, REPORT_LIMIT,
                                         f"{folder}/{name}", skipped)
        if checkout_fd is not None:
            for _, relative in untracked:
                if (not relative or "\0" in relative or os.path.isabs(relative) or ".." in Path(relative).parts
                        or relative.startswith(EXCLUDED_UNTRACKED) or not Path(relative).parts):
                    continue
                copied += copy_beneath(checkout_fd, list(Path(relative).parts), destination / "untracked" / relative,
                                       UNTRACKED_LIMIT, relative, skipped)
    finally:
        if checkout_fd is not None:
            os.close(checkout_fd)
    record_retention(output, "ok", reports=kept, untracked=copied, skipped=skipped,
                     checkout="present" if checkout_fd is not None else "missing")
    return f"retained {kept} report file(s) in {destination}"


def summary(output):
    run = catalog_check.read_json(output / "run.json")
    if not isinstance(run, dict):
        return "no run.json"
    report = catalog_check.read_json(output / "retained" / ".testoptimization" / "testdrive.json")
    status = ((report.get("summary") if isinstance(report, dict) else None) or {})
    status = status.get("status") if isinstance(status, dict) else None
    return f"run {run.get('status')}, report {status if isinstance(status, str) else 'no report'}"


class Supervisor:
    """Owns the batch's SIGINT/SIGTERM/SIGHUP while trials run, in one or two lanes.

    Each harness runs in its own session, so a terminal Ctrl-C reaches only this
    runner. The first signal is forwarded once to every running harness; each
    then finalizes run.json and removes its container, and no lane starts
    another case. A second signal kills every running harness; retention and
    --cleanup still run, and the pending cleanup commands are printed.
    """

    def __init__(self):
        self.signal = None
        self.forwarded = 0
        self.processes = {}  # pid -> (process, signals sent to it, its trial's cleanup command)
        self.cleanup_commands = {}  # lane -> cleanup command, while that lane retains and cleans up
        self.lock = threading.RLock()
        self.previous = {}

    def __enter__(self):
        for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            self.previous[number] = signal.signal(number, self.handle)
        return self

    def __exit__(self, *_):
        for number, handler in self.previous.items():
            signal.signal(number, handler)

    def deliver(self, process, sent):
        """Bring one harness up to the batch's level: the signal once, then SIGKILL to its group."""
        while process.returncode is None and sent < min(self.forwarded, 2):
            try:
                if sent == 0:
                    os.kill(process.pid, self.signal)
                else:
                    os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            sent += 1
        return sent

    def handle(self, number, _frame):
        with self.lock:
            self.signal = self.signal or number
            self.forwarded += 1
            running = [pid for pid, (process, *_) in self.processes.items() if process.returncode is None]
            for pid in running:
                process, sent, cleanup = self.processes[pid]
                self.processes[pid] = (process, self.deliver(process, sent), cleanup)
            if running and self.forwarded == 1:
                print(f"Interrupted: waiting for {len(running)} trial(s) to finalize their evidence and remove their "
                      "containers (interrupt again to kill them now)", file=sys.stderr, flush=True)
            elif running:
                print(f"Killing {len(running)} trial(s); their cleanup still runs; if it does not finish, run: "
                      + "; ".join(self.processes[pid][2] for pid in running if self.processes[pid][2]),
                      file=sys.stderr, flush=True)
            for lane, command in self.cleanup_commands.items():
                print(f"Interrupted: {lane} is finishing retention and cleanup; if it does not finish, run: {command}",
                      file=sys.stderr, flush=True)

    def run(self, argv, stream, forward=True, cleanup=None):
        """Run a child in its own session and wait for it, however long teardown takes."""
        process = subprocess.Popen(argv, stdout=stream, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                                   start_new_session=True)
        if forward:
            with self.lock:
                # A signal that arrived while the child was starting still reaches it.
                self.processes[process.pid] = (process, self.deliver(process, 0), cleanup)
        try:
            return process.wait()
        finally:
            with self.lock:
                self.processes.pop(process.pid, None)


def log_write(state, stream, text):
    """Write to a trial's log; a failure (such as a full disk) is recorded, never raised."""
    try:
        stream.write(text)
        stream.flush()
    except (OSError, ValueError) as error:
        state["log_errors"].append(f"writing {stream.name} failed: {error}")


def run_cleanup(supervisor, cleanup_argv, stream):
    """run_eval.py --cleanup's exit status, or why it could not start."""
    try:
        return supervisor.run(cleanup_argv, stream, forward=False)
    except Exception as error:  # noqa: BLE001 - reported as a cleanup failure
        return "could not start: " + catalog_check.error_text(error)[0]


def run_trial(state, argv, output, log, supervisor, agent, cleanup_argv):
    """Run the harness, retain, then clean up. Cleanup runs once the harness was started, whatever
    fails before it (retention, the log, the runner itself); `state` records each step."""
    command = shlex.join(cleanup_argv)
    stream = None
    try:
        stream = open(log, "w")
        log_write(state, stream, "argv: " + shlex.join(argv) + "\n")
        state["started"] = True
        state["code"] = supervisor.run(argv, stream, cleanup=command)
        with supervisor.lock:
            supervisor.cleanup_commands[agent] = command
        try:
            state["result"] = retain(output)
        except OSError as error:
            # Cleanup still runs and the batch goes on; the case fails, also on a later --evaluate-only.
            state["retention_error"] = f"retention failed: {error}"
            state["result"] = state["retention_error"]
            try:
                record_retention(output, "failed", error=str(error))
            except OSError as record_error:
                # RETENTION.json still says "started", which catalog_check.py also fails.
                state["log_errors"].append(f"recording the retention failure failed: {record_error}")
        log_write(state, stream, f"\nexit={state['code']}\n{state['result']}\n")
    finally:
        try:
            if state["started"]:
                state["cleanup"] = run_cleanup(supervisor, cleanup_argv, stream or subprocess.DEVNULL)
        finally:
            with supervisor.lock:
                supervisor.cleanup_commands.pop(agent, None)
            if stream:
                try:
                    stream.close()
                except OSError as error:
                    state["log_errors"].append(f"writing {log} failed: {error}")


def runner_failure(case, evidence, log, rerun, agent, ddtest_label, error):
    """The harness_failure (`runner`) record of a case run_catalog.py itself failed on."""
    text, trace = catalog_check.error_text(error)
    return catalog_check.failure(case, evidence, "runner", f"run_catalog.py failed: {text}", trace, log, rerun, agent,
                                 ddtest_label)


def run_case(case, argv, output, log, supervisor, rerun=None, agent="codex", ddtest_label=None):
    cleanup_argv = [sys.executable, str(RUN_EVAL), "--cleanup", str(output)]
    state = {"started": False, "code": None, "result": "retention did not finish", "retention_error": None,
             "cleanup": None, "log_errors": []}
    runner_error = None
    try:
        run_trial(state, argv, output, log, supervisor, agent, cleanup_argv)
        record = catalog_check.evaluate(case, output, log, rerun, agent, ddtest_label)
    except Exception as error:  # noqa: BLE001 - one case's failure must not stop its lane
        runner_error = catalog_check.error_text(error)[0]
        record = runner_failure(case, output, log, rerun, agent, ddtest_label, error)
    retention_error, cleanup = state["retention_error"], state["cleanup"]
    if retention_error and record["outcome"] != "harness_failure":
        record["harness_category"] = "retention"
        record = catalog_check.finish(record, "harness_failure", retention_error, "", rerun)
    elif retention_error and retention_error not in record["problems"]:
        record["problems"].append(retention_error)
    cleanup_text = "not run" if not state["started"] else f"exit {cleanup}" if isinstance(cleanup, int) else cleanup
    if cleanup and record["outcome"] != "harness_failure":
        record["harness_category"] = "cleanup"
        record = catalog_check.finish(record, "harness_failure", f"cleanup (run_eval.py --cleanup {cleanup_text})",
                                      "", rerun)
    elif cleanup:
        record["problems"].append(f"cleanup (run_eval.py --cleanup {cleanup_text})")
    record["notes"] += state["log_errors"]
    expected = record["expected"]["verdict"] or "none"
    lines = [f"[{agent}] {case['id']}: exit {state['code']}, {summary(output)} (expected {expected}); "
             f"cleanup {cleanup_text}; outcome {catalog_check.label(record)}; log {log}"]
    if runner_error:
        lines.append(f"[{agent}] {case['id']}: run_catalog.py failed: {runner_error}")
    if retention_error:
        lines.append(f"[{agent}] {case['id']}: retention failed: {retention_error.removeprefix('retention failed: ')}")
    for problem in state["log_errors"]:
        lines.append(f"[{agent}] {case['id']}: {problem}")
    if cleanup:
        lines.append(f"[{agent}] {case['id']}: cleanup failed; retry with: {shlex.join(cleanup_argv)}")
    for line in lines:
        print(line, flush=True)
    return record, lines


def load_previous(path, output):
    """The case records of an existing results.json for this output folder, to merge into."""
    if not path.exists():
        return []
    try:
        document = json.loads(path.read_text())
        cases = document["cases"]
        if not isinstance(cases, list) or not all(isinstance(case, dict) and "id" in case for case in cases):
            raise ValueError("cases is not a list of records")
    except (OSError, ValueError, KeyError, TypeError) as error:
        raise ValueError(f"{path} exists but is not a results.json ({error}); pass a new --results") from None
    if document.get("output") != str(output):
        raise ValueError(f"{path} holds results for {document.get('output')}, not {output}; pass a new --results")
    return cases


def merge(previous, records, replaced_agents=(), order=None):
    """records replace previous entries with the same (id, agent) and, for a full batch, every
    entry of the agents it covers; other entries are kept. Sorted in catalog order."""
    keys = {(record["id"], record["agent"]) for record in records}
    kept = [record for record in previous
            if (record["id"], record["agent"]) not in keys and record["agent"] not in replaced_agents]
    order = order or {}
    return sorted(kept + list(records), key=lambda r: (order.get(r["id"], len(order)), str(r["id"]),
                                                       catalog_check.AGENTS.index(r["agent"])
                                                       if r["agent"] in catalog_check.AGENTS else len(catalog_check.AGENTS)))


def write_results(path, catalog, output, records, code, ddtest_label=None, not_evaluated=(), kept=0):
    """Write results.json; exit_code covers every entry, merged ones included (None while running)."""
    counts = {}
    for record in records:
        counts[catalog_check.label(record)] = counts.get(catalog_check.label(record), 0) + 1
    if code is not None and code < 128 and code != catalog_check.EXIT_FAILED and records:
        # Merged entries count too; a failure of this invocation (such as missing evidence) stays.
        code = catalog_check.exit_code(records)
    questions = [f"{record['id']} ({record['agent']}): question not scriptable: {record['question']['code']}"
                 for record in records if record.get("question")]
    document = {"catalog": str(catalog), "output": str(output), "ddtest_label": ddtest_label, "exit_code": code,
                "outcomes": counts, "merged_entries_kept": kept, "agents_disagree": catalog_check.disagreements(records),
                "questions_not_scriptable": questions, "not_evaluated": list(not_evaluated), "cases": records}
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".partial")
    temporary.write_text(json.dumps(document, indent=2) + "\n")
    temporary.replace(path)


class Lane(threading.Thread):
    """One agent's cases, in catalog order, one trial at a time.

    An exception on one case makes that case a harness_failure (`runner`) and the
    lane goes on; if the lane itself stops, `error` says why and the batch counts
    its unfinished cases as not run.
    """

    def __init__(self, agent, plans, supervisor, output, sink, ddtest_label=None):
        super().__init__(name=f"lane-{agent}", daemon=True)
        self.agent, self.plans, self.supervisor, self.output, self.sink = agent, plans, supervisor, output, sink
        self.ddtest_label = ddtest_label
        self.records = []
        self.error = None
        self.log = output / f"lane-{agent}.log"

    def note(self, stream, lines):
        try:
            stream.write("".join(line + "\n" for line in lines))
            stream.flush()
        except (OSError, ValueError) as error:
            print(f"[{self.agent}] writing {self.log} failed: {error}", file=sys.stderr, flush=True)

    def run(self):
        try:
            with open(self.log, "a") as stream:
                for case, evidence, arguments in self.plans:
                    if self.supervisor.signal:
                        break
                    rerun = rerun_hint(case, self.agent)
                    log = self.output / f"{evidence.name}.log"
                    try:
                        record, lines = self.run_one(case, evidence, arguments, log, rerun, stream)
                    except Exception as error:  # noqa: BLE001 - recorded; the lane goes on
                        record = runner_failure(case, evidence, log, rerun, self.agent, self.ddtest_label, error)
                        lines = [f"[{self.agent}] {case['id']}: {record['problems'][-1]}"]
                        print(lines[0], flush=True)
                    self.records.append(record)
                    self.note(stream, lines)
                    try:
                        self.sink()
                    except Exception as error:  # noqa: BLE001 - the final results.json is written by main
                        print(f"[{self.agent}] writing partial results failed: {error}", file=sys.stderr, flush=True)
        except BaseException as error:  # noqa: BLE001 - main counts the unfinished cases
            self.error = catalog_check.error_text(error)
            print(f"[{self.agent}] lane stopped: {self.error[0]}", file=sys.stderr, flush=True)

    def run_one(self, case, evidence, arguments, log, rerun, stream):
        if evidence.exists():
            line = f"[{self.agent}] {case['id']}: skipped, {evidence} exists"
            print(line, flush=True)
            record = catalog_check.failure(case, evidence, "exists", f"not run: {evidence} already exists", "",
                                           rerun=rerun, agent=self.agent, ddtest_label=self.ddtest_label)
            return record, [line]
        started = f"[{self.agent}] {case['id']}: started, evidence {evidence}"
        print(started, flush=True)
        self.note(stream, [started])
        return run_case(case, arguments, evidence, log, self.supervisor, rerun, self.agent, self.ddtest_label)


def unfinished(workers, interrupted):
    """not_run records for the cases a stopped lane never finished (an interrupt's are only listed)."""
    records = []
    for lane in workers:
        if interrupted and not lane.error:
            continue
        done = {record["id"] for record in lane.records}
        reason = f"lane {lane.agent} stopped" + (f": {lane.error[0]}" if lane.error else " without recording it")
        for case, evidence, _ in lane.plans:
            if case["id"] not in done:
                records.append(catalog_check.failure(
                    case, evidence, "not_run", f"not run: {reason}", lane.error[1] if lane.error else "",
                    rerun=rerun_hint(case, lane.agent), agent=lane.agent, ddtest_label=lane.ddtest_label))
    return records


def evaluate_only(cases, agents_option, output, ddtest_label, named):
    """Records for existing evidence, and what had none.

    Without --agent, each case's agents are the <id>-<agent> folders present.
    A requested agent without a folder is listed as "no <agent> evidence
    (found: ...)", not evaluated; for a named case with one agent requested,
    or with no evidence at all, that fails the invocation.
    """
    records, missing, failed = [], [], False
    for case in cases:
        found = [agent for agent in catalog_check.AGENTS if (output / f"{case['id']}-{agent}").is_dir()]
        wanted = found if agents_option is None else \
            catalog_check.AGENTS if agents_option == "both" else (agents_option,)
        for agent in wanted:
            evidence = output / f"{case['id']}-{agent}"
            if evidence.is_dir():
                records.append(catalog_check.evaluate(case, evidence, rerun=rerun_hint(case, agent), agent=agent,
                                                      ddtest_label=ddtest_label))
                continue
            missing.append({"id": case["id"], "agent": agent,
                            "reason": f"no {agent} evidence (found: {', '.join(found) or 'none'})"})
            failed = failed or (named and agents_option not in (None, "both"))
        if not wanted:
            missing.append({"id": case["id"], "agent": None, "reason": "no evidence (found: none)"})
            failed = failed or named
    for entry in missing:
        print(f"[{entry['agent'] or '-'}] {entry['id']}: not evaluated, {entry['reason']}", flush=True)
    return records, missing, failed


def main(argv=None):
    if hasattr(sys.stdout, "reconfigure"):
        # Status lines reach a redirected stdout as they happen, not in blocks.
        sys.stdout.reconfigure(line_buffering=True)
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("ids", nargs="*", help="Case ids (such as E02-pipeline-schema) or their E<nn> prefix")
    parser.add_argument("--all", action="store_true", help="Run every case in catalog order")
    parser.add_argument("--catalog", type=Path, default=CATALOG)
    parser.add_argument("--image-map", action="append", default=[], metavar="KEY=IMAGE",
                        help="Image for a catalog image key (repeatable); overrides --image-file")
    parser.add_argument("--image-file", help=f"JSON/YAML mapping of image key to image (default: ${IMAGES_VARIABLE})")
    parser.add_argument("--ddtest", type=Path, help="Linux ddtest executable for the images' architecture")
    parser.add_argument("--ddtest-label",
                        help="Free-text ddtest provenance, such as its commit; recorded in run.json and results.json "
                             "and named in next steps instead of a `dev` version")
    parser.add_argument("--agent", choices=(*catalog_check.AGENTS, "both"),
                        help="both: a Codex lane and a Claude lane run at the same time. Trials default to codex; "
                             "--evaluate-only defaults to the agents whose <id>-<agent> folders exist")
    parser.add_argument("--existing-login", action="store_true",
                        help="Reuse each agent's authorized CLI login; it is locked for a whole trial, so a lane runs "
                             "one case at a time")
    parser.add_argument("--model")
    parser.add_argument("--timeout", type=float, default=DEFAULT_TIMEOUT, help=f"Per-command timeout (default {DEFAULT_TIMEOUT})")
    parser.add_argument("--speed-trims", action="store_true", help="Also apply each case's optional speed trims")
    parser.add_argument("--output", type=Path, help="Directory for <id>-<agent> evidence and logs")
    parser.add_argument("--dry-run", action="store_true",
                        help="Print the image builds and each case's run_eval.py arguments and exit; works without "
                             "--ddtest and images (placeholders are printed)")
    parser.add_argument("--evaluate-only", action="store_true",
                        help="Check existing <output>/<id>-<agent> evidence against expected; run nothing")
    parser.add_argument("--results", type=Path,
                        help="results.json to write (default: <output>/results.json); an existing one for the same "
                             "output is merged into by case and agent")
    args = parser.parse_args(argv)
    catalog = load_catalog(args.catalog)
    problems = validate(catalog)
    if problems:
        parser.error("invalid catalog:\n  " + "\n  ".join(problems))
    cases = catalog["cases"]
    order = {case["id"]: index for index, case in enumerate(cases)}
    if args.all == bool(args.ids):
        parser.error("name case ids or pass --all")
    if not args.all:
        selected = []
        for identifier in args.ids:
            matches = [case for case in cases if case["id"] == identifier or case["id"].split("-")[0] == identifier]
            if len(matches) != 1:
                parser.error(f"unknown case {identifier}")
            selected += matches
        cases = selected
    try:
        images = resolve_images(args.image_map, args.image_file)
    except (OSError, ValueError) as error:
        parser.error(str(error))
    missing = sorted({case["image"] for case in cases} - set(images))
    if args.evaluate_only and not args.output:
        parser.error("--evaluate-only requires --output, the folder holding <id>-<agent> evidence")
    if not args.dry_run and not args.evaluate_only:
        if missing:
            parser.error("no image for: " + ", ".join(missing) + " (pass --image-map KEY=IMAGE)")
        if not args.ddtest or not args.output:
            parser.error("trials require --ddtest and --output")
    output = Path(os.path.abspath(args.output)) if args.output else Path("<output>")
    ddtest = Path(os.path.abspath(args.ddtest)) if args.ddtest else "<ddtest>"
    agents = catalog_check.AGENTS if args.agent == "both" else (args.agent or "codex",)
    lanes = {}
    for agent in agents:
        for case in cases:
            evidence = output / f"{case['id']}-{agent}"
            lanes.setdefault(agent, []).append((case, evidence, build_argv(
                case, images.get(case["image"], f"<image:{case['image']}>"), ddtest, evidence, agent,
                args.existing_login, args.timeout, args.speed_trims, args.model, args.ddtest_label)))
    if args.dry_run:
        print("\n".join(image_builds(catalog["images"], {case["image"] for case in cases})) + "\n")
        for agent, plans in lanes.items():
            if len(lanes) > 1:
                print(f"## lane {agent} (runs at the same time as the other lane)\n")
            for case, evidence, arguments in plans:
                print(f"# {case['id']} ({case['framework']}, {case['ci_provider']}, image {case['image']}, agent {agent})")
                print(shlex.join(arguments))
                print(shlex.join([sys.executable, str(RUN_EVAL), "--cleanup", str(evidence)]) + "\n")
        return 0
    results = Path(os.path.abspath(args.results)) if args.results else output / "results.json"
    try:
        previous = load_previous(results, output)
    except ValueError as error:
        parser.error(str(error))
    # A full batch replaces every earlier entry of the agents it covers.
    replaced = (catalog_check.AGENTS if args.evaluate_only and args.agent is None else agents) if args.all else ()

    def save(records, code, not_evaluated=()):
        merged = merge(previous, records, replaced, order)
        kept = len(merged) - len(records)
        write_results(results, args.catalog, output, merged, code, args.ddtest_label, not_evaluated, kept)
        return kept

    if args.evaluate_only:
        records, not_evaluated, failed = evaluate_only(cases, args.agent, output, args.ddtest_label, not args.all)
        code = catalog_check.report(records, sys.stdout) if records else catalog_check.EXIT_PASS
        if failed:
            code = catalog_check.EXIT_FAILED
        kept = save(records, code, not_evaluated)
        print(f"Results: {results}" + (f" (merged: {kept} earlier entries kept)" if kept else ""), flush=True)
        return code
    output.mkdir(parents=True, exist_ok=True)
    sink_lock = threading.Lock()

    def collected():
        records = [record for lane in workers for record in lane.records]
        return sorted(records, key=lambda r: (order[r["id"]], catalog_check.AGENTS.index(r["agent"])))

    def sink():
        with sink_lock:
            save(collected(), None)

    with Supervisor() as supervisor:
        workers = [Lane(agent, plans, supervisor, output, sink, args.ddtest_label) for agent, plans in lanes.items()]
        for worker in workers:
            worker.start()
        # Join with a timeout so the main thread keeps handling signals.
        while any(worker.is_alive() for worker in workers):
            for worker in workers:
                worker.join(0.2)
    lost = unfinished(workers, supervisor.signal)
    records = sorted(collected() + lost, key=lambda r: (order[r["id"]], catalog_check.AGENTS.index(r["agent"])))
    code = catalog_check.report(records, sys.stdout) if records else 0
    if lost:
        print("Not run: " + ", ".join(f"{record['id']}-{record['agent']}" for record in lost), flush=True)
        code = catalog_check.EXIT_FAILED
    if supervisor.signal:
        remaining = [f"{case['id']}-{agent}" for agent, plans in lanes.items() for case, evidence, _ in plans
                     if not evidence.exists()]
        print(f"Interrupted by signal {supervisor.signal}; not run: {', '.join(remaining) or 'none'}", flush=True)
        code = 128 + supervisor.signal
    kept = save(records, code)
    print(f"Results: {results}" + (f" (merged: {kept} earlier entries kept)" if kept else ""), flush=True)
    return code


def rerun_hint(case, agent):
    return (f"python3 {SCRIPTS / 'run_catalog.py'} {case['id'].split('-')[0]} --agent {agent} "
            "<same --image-map/--ddtest/--existing-login options> --output <new folder>")


if __name__ == "__main__":
    sys.exit(main())
