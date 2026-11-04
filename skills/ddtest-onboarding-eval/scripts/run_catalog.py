#!/usr/bin/env python3
"""Run standard catalog cases through run_eval.py, one after another.

Each case: run_eval.py with the catalog's image, commit, trims, setup, readiness,
flags and answers -> retain the .testoptimization reports and small untracked
files -> run_eval.py --cleanup. Required trims are always applied, speed trims
only with --speed-trims; each is committed with a scratch identity inside the
trial container, so the checkout stays clean.

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
import shutil
import signal
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
               speed_trims=False, model=None):
    argv = [sys.executable, str(RUN_EVAL), "--image", image, "--repo", case["repo"], "--ref", case["commit"],
            "--ddtest", str(ddtest), "--agent", agent, "--timeout", str(timeout), "--output", str(output)]
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


def inside(path, root):
    try:
        return Path(os.path.realpath(path)).is_relative_to(os.path.realpath(root))
    except OSError:
        return False


def copy_regular(source, target, root, limit):
    if source.is_symlink() or not source.is_file() or not inside(source, root) or source.stat().st_size >= limit:
        return False
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, target)
    return True


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


def retain(output):
    """Copy the report pair and small untracked files out of the trial checkout.

    Untracked paths come from the container-collected status.txt; Git never runs
    on the host in the agent-modified checkout.
    """
    try:
        checkout = Path(json.loads((output / "run.json").read_text())["checkout"])
    except (OSError, ValueError, KeyError) as error:
        return f"nothing retained: {error}"
    destination = output / "retained"
    destination.mkdir(exist_ok=True)
    reports = checkout / ".testoptimization"
    kept = 0
    if reports.is_dir() and not reports.is_symlink():
        for path in sorted(reports.rglob("*")):
            kept += copy_regular(path, destination / ".testoptimization" / path.relative_to(reports), checkout, REPORT_LIMIT)
    status = output / "status.txt"
    untracked = untracked_paths(status.read_text(errors="replace")) if status.is_file() else []
    # Paths as git printed them: quoted names stay on one line.
    (destination / "untracked.txt").write_text("".join(printed + "\n" for printed, _ in untracked))
    for _, relative in untracked:
        if (not relative or "\0" in relative or os.path.isabs(relative) or ".." in Path(relative).parts
                or relative.startswith((".testoptimization/", "node_modules/", "vendor/", ".m2/", ".gradle/"))):
            continue
        copy_regular(checkout / relative, destination / "untracked" / relative, checkout, UNTRACKED_LIMIT)
    return f"retained {kept} report file(s) in {destination}"


def summary(output):
    try:
        run = json.loads((output / "run.json").read_text())
    except (OSError, ValueError):
        return "no run.json"
    report = output / "retained" / ".testoptimization" / "testdrive.json"
    try:
        status = json.loads(report.read_text())["summary"]["status"]
    except (OSError, ValueError, KeyError, TypeError):
        status = "no report"
    return f"run {run.get('status')}, report {status}"


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


def run_case(case, argv, output, log, supervisor, rerun=None, agent="codex"):
    cleanup_argv = [sys.executable, str(RUN_EVAL), "--cleanup", str(output)]
    with open(log, "w") as stream:
        stream.write("argv: " + shlex.join(argv) + "\n")
        stream.flush()
        code = supervisor.run(argv, stream, cleanup=shlex.join(cleanup_argv))
        with supervisor.lock:
            supervisor.cleanup_commands[agent] = shlex.join(cleanup_argv)
        result = retain(output)
        stream.write(f"\nexit={code}\n{result}\n")
        stream.flush()
        cleanup = supervisor.run(cleanup_argv, stream, forward=False)
    with supervisor.lock:
        supervisor.cleanup_commands.pop(agent, None)
    record = catalog_check.evaluate(case, output, log, rerun, agent)
    if cleanup and record["outcome"] != "harness_failure":
        record["harness_category"] = "cleanup"
        record = catalog_check.finish(record, "harness_failure", f"cleanup (run_eval.py --cleanup exit {cleanup})",
                                      "", rerun)
    expected = record["expected"]["verdict"] or "none"
    lines = [f"[{agent}] {case['id']}: exit {code}, {summary(output)} (expected {expected}); cleanup exit {cleanup}; "
             f"outcome {catalog_check.label(record)}; log {log}"]
    if cleanup:
        lines.append(f"[{agent}] {case['id']}: cleanup failed; retry with: {shlex.join(cleanup_argv)}")
    for line in lines:
        print(line, flush=True)
    return record, lines


def write_results(path, catalog, output, records, code):
    counts = {}
    for record in records:
        counts[catalog_check.label(record)] = counts.get(catalog_check.label(record), 0) + 1
    document = {"catalog": str(catalog), "output": str(output), "exit_code": code, "outcomes": counts,
                "agents_disagree": catalog_check.disagreements(records), "cases": records}
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".partial")
    temporary.write_text(json.dumps(document, indent=2) + "\n")
    temporary.replace(path)


class Lane(threading.Thread):
    """One agent's cases, in catalog order, one trial at a time."""

    def __init__(self, agent, plans, supervisor, output, sink):
        super().__init__(name=f"lane-{agent}", daemon=True)
        self.agent, self.plans, self.supervisor, self.output, self.sink = agent, plans, supervisor, output, sink
        self.records = []
        self.log = output / f"lane-{agent}.log"

    def note(self, stream, lines):
        stream.write("".join(line + "\n" for line in lines))
        stream.flush()

    def run(self):
        with open(self.log, "a") as stream:
            for case, evidence, arguments in self.plans:
                if self.supervisor.signal:
                    break
                rerun = rerun_hint(case, self.agent)
                if evidence.exists():
                    line = f"[{self.agent}] {case['id']}: skipped, {evidence} exists"
                    print(line, flush=True)
                    self.note(stream, [line])
                    record = catalog_check.evaluate(case, Path("/nonexistent"), rerun=rerun, agent=self.agent)
                    record["evidence"] = {"folder": str(evidence)}
                    record["harness_category"] = "exists"
                    self.records.append(catalog_check.finish(
                        record, "harness_failure", f"not run: {evidence} already exists", "", rerun))
                    continue
                self.note(stream, [f"[{self.agent}] {case['id']}: started, evidence {evidence}"])
                record, lines = run_case(case, arguments, evidence, self.output / f"{evidence.name}.log",
                                         self.supervisor, rerun, self.agent)
                self.note(stream, lines)
                self.records.append(record)
                self.sink()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("ids", nargs="*", help="Case ids (such as E02-pipeline-schema) or their E<nn> prefix")
    parser.add_argument("--all", action="store_true", help="Run every case in catalog order")
    parser.add_argument("--catalog", type=Path, default=CATALOG)
    parser.add_argument("--image-map", action="append", default=[], metavar="KEY=IMAGE",
                        help="Image for a catalog image key (repeatable); overrides --image-file")
    parser.add_argument("--image-file", help=f"JSON/YAML mapping of image key to image (default: ${IMAGES_VARIABLE})")
    parser.add_argument("--ddtest", type=Path, help="Linux ddtest executable for the images' architecture")
    parser.add_argument("--agent", choices=(*catalog_check.AGENTS, "both"), default="codex",
                        help="both: a Codex lane and a Claude lane run at the same time")
    parser.add_argument("--existing-login", action="store_true",
                        help="Reuse each agent's authorized CLI login; it is locked for a whole trial, so a lane runs "
                             "one case at a time")
    parser.add_argument("--model")
    parser.add_argument("--timeout", type=float, default=DEFAULT_TIMEOUT, help=f"Per-command timeout (default {DEFAULT_TIMEOUT})")
    parser.add_argument("--speed-trims", action="store_true", help="Also apply each case's optional speed trims")
    parser.add_argument("--output", type=Path, help="Directory for <id>-<agent> evidence and logs")
    parser.add_argument("--dry-run", action="store_true", help="Print each case's run_eval.py arguments and exit")
    parser.add_argument("--evaluate-only", action="store_true",
                        help="Check existing <output>/<id>-<agent> evidence against expected; run nothing")
    parser.add_argument("--results", type=Path, help="Where to write results.json (default: <output>/results.json)")
    args = parser.parse_args(argv)
    catalog = load_catalog(args.catalog)
    problems = validate(catalog)
    if problems:
        parser.error("invalid catalog:\n  " + "\n  ".join(problems))
    cases = catalog["cases"]
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
    agents = catalog_check.AGENTS if args.agent == "both" else (args.agent,)
    lanes = {}
    for agent in agents:
        for case in cases:
            evidence = output / f"{case['id']}-{agent}"
            lanes.setdefault(agent, []).append((case, evidence, build_argv(
                case, images.get(case["image"], f"<image:{case['image']}>"), ddtest, evidence, agent,
                args.existing_login, args.timeout, args.speed_trims, args.model)))
    if args.dry_run:
        for agent, plans in lanes.items():
            if len(lanes) > 1:
                print(f"## lane {agent} (runs at the same time as the other lane)\n")
            for case, evidence, arguments in plans:
                print(f"# {case['id']} ({case['framework']}, {case['ci_provider']}, image {case['image']}, agent {agent})")
                print(shlex.join(arguments))
                print(shlex.join([sys.executable, str(RUN_EVAL), "--cleanup", str(evidence)]) + "\n")
        return 0
    results = Path(os.path.abspath(args.results)) if args.results else output / "results.json"
    if args.evaluate_only:
        records = []
        for case in cases:
            for agent in agents:
                evidence = output / f"{case['id']}-{agent}"
                if (args.all or args.agent == "both") and not evidence.exists():
                    print(f"[{agent}] {case['id']}: not evaluated, no {evidence}", flush=True)
                    continue
                records.append(catalog_check.evaluate(case, evidence, rerun=rerun_hint(case, agent), agent=agent))
        code = catalog_check.report(records, sys.stdout)
        write_results(results, args.catalog, output, records, code)
        print(f"Results: {results}", flush=True)
        return code
    output.mkdir(parents=True, exist_ok=True)
    order = {case["id"]: index for index, case in enumerate(cases)}
    sink_lock = threading.Lock()

    def collected():
        records = [record for lane in workers for record in lane.records]
        return sorted(records, key=lambda r: (order[r["id"]], catalog_check.AGENTS.index(r["agent"])))

    def sink():
        with sink_lock:
            write_results(results, args.catalog, output, collected(), None)

    with Supervisor() as supervisor:
        workers = [Lane(agent, plans, supervisor, output, sink) for agent, plans in lanes.items()]
        for worker in workers:
            worker.start()
        # Join with a timeout so the main thread keeps handling signals.
        while any(worker.is_alive() for worker in workers):
            for worker in workers:
                worker.join(0.2)
    records = collected()
    code = catalog_check.report(records, sys.stdout) if records else 0
    if supervisor.signal:
        remaining = [f"{case['id']}-{agent}" for agent, plans in lanes.items() for case, evidence, _ in plans
                     if not evidence.exists()]
        print(f"Interrupted by signal {supervisor.signal}; not run: {', '.join(remaining) or 'none'}", flush=True)
        code = 128 + supervisor.signal
    write_results(results, args.catalog, output, records, code)
    print(f"Results: {results}", flush=True)
    return code


def rerun_hint(case, agent):
    return (f"python3 {SCRIPTS / 'run_catalog.py'} {case['id'].split('-')[0]} --agent {agent} "
            "<same --image-map/--ddtest/--existing-login options> --output <new folder>")


if __name__ == "__main__":
    sys.exit(main())
