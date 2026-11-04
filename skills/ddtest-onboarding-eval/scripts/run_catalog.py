#!/usr/bin/env python3
"""Run standard catalog cases through run_eval.py, one after another.

Each case: run_eval.py with the catalog's image, commit, trims, setup, readiness,
flags and answers -> retain the .testoptimization reports and small untracked
files -> run_eval.py --cleanup. Required trims are always applied, speed trims
only with --speed-trims; each is committed with a scratch identity inside the
trial container, so the checkout stays clean.
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

import run_eval
import user_answers

SCRIPTS = Path(__file__).resolve().parent
CATALOG = SCRIPTS.parent / "catalog" / "cases.yaml"
RUN_EVAL = SCRIPTS / "run_eval.py"
IMAGES_VARIABLE = "DDTEST_EVAL_IMAGES"
PROVIDERS = ("github", "gitlab", "circleci", "buildkite", "jenkins")
CASE_FIELDS = ("id", "framework", "language", "ci_provider", "ci_config_path", "repo", "commit", "subdir", "image",
               "runtime", "system_needs", "browsers", "services", "setup_commands", "readiness_commands", "harness",
               "trims", "timings_seconds", "tests", "prepared_image", "notes", "risks")
HARNESS_FIELDS = ("framework", "python", "pnpm", "bundle_binstubs", "answers")
TIMINGS = ("clone", "install", "browser", "build", "tests_full", "tests_trimmed")
TRIM_KINDS = ("required", "optional_speed")
VERDICTS = ("COMPLETE LOCALLY", "PASSED", "INCOMPLETE", "FAILED", "STOPPED")
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
        expected = case.get("expected")
        if expected is not None and (expected.get("verdict") not in VERDICTS or not expected.get("ddtest") or not expected.get("reason")):
            problems.append(f"{name}: expected needs a verdict ({', '.join(VERDICTS)}), ddtest and reason")
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
    """Owns the batch's SIGINT/SIGTERM/SIGHUP while trials run.

    The harness runs in its own session, so a terminal Ctrl-C reaches only this
    runner, which forwards the first signal once; the harness then finalizes
    run.json and removes its container. A second signal kills the harness;
    retention and --cleanup still run, and the cleanup command is printed.
    """

    def __init__(self):
        self.signal = None
        self.process = None
        self.forwarded = 0
        self.cleanup_command = None
        self.previous = {}

    def __enter__(self):
        for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            self.previous[number] = signal.signal(number, self.handle)
        return self

    def __exit__(self, *_):
        for number, handler in self.previous.items():
            signal.signal(number, handler)

    def handle(self, number, _frame):
        self.signal = self.signal or number
        process = self.process
        if process is not None and process.returncode is None:
            try:
                if self.forwarded == 0:
                    os.kill(process.pid, number)
                    print("Interrupted: waiting for the trial to finalize its evidence and remove its container "
                          "(interrupt again to kill it now)", file=sys.stderr, flush=True)
                else:
                    os.killpg(process.pid, signal.SIGKILL)
                    print("Killing the trial; its cleanup still runs", file=sys.stderr, flush=True)
            except ProcessLookupError:
                pass
            self.forwarded += 1
        elif self.cleanup_command:
            print("Interrupted: finishing retention and cleanup; if it does not finish, run: " + self.cleanup_command,
                  file=sys.stderr, flush=True)

    def run(self, argv, stream, forward=True):
        """Run a child in its own session and wait for it, however long teardown takes."""
        process = subprocess.Popen(argv, stdout=stream, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                                   start_new_session=True)
        self.process = process if forward else None
        if forward and self.signal and not self.forwarded:
            self.handle(self.signal, None)  # The signal arrived while the child was starting.
        try:
            return process.wait()
        finally:
            self.process = None


def run_case(case, argv, output, log, supervisor):
    cleanup_argv = [sys.executable, str(RUN_EVAL), "--cleanup", str(output)]
    supervisor.cleanup_command = shlex.join(cleanup_argv)
    with open(log, "w") as stream:
        stream.write("argv: " + shlex.join(argv) + "\n")
        stream.flush()
        code = supervisor.run(argv, stream)
        result = retain(output)
        stream.write(f"\nexit={code}\n{result}\n")
        stream.flush()
        cleanup = supervisor.run(cleanup_argv, stream, forward=False)
    supervisor.cleanup_command = None
    expected = (case.get("expected") or {}).get("verdict", "none")
    print(f"{case['id']}: exit {code}, {summary(output)} (expected {expected}); cleanup exit {cleanup}; log {log}", flush=True)
    if cleanup:
        print(f"{case['id']}: cleanup failed; retry with: {shlex.join(cleanup_argv)}", flush=True)
    return code or cleanup


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("ids", nargs="*", help="Case ids (such as E02-pipeline-schema) or their E<nn> prefix")
    parser.add_argument("--all", action="store_true", help="Run every case in catalog order")
    parser.add_argument("--catalog", type=Path, default=CATALOG)
    parser.add_argument("--image-map", action="append", default=[], metavar="KEY=IMAGE",
                        help="Image for a catalog image key (repeatable); overrides --image-file")
    parser.add_argument("--image-file", help=f"JSON/YAML mapping of image key to image (default: ${IMAGES_VARIABLE})")
    parser.add_argument("--ddtest", type=Path, help="Linux ddtest executable for the images' architecture")
    parser.add_argument("--agent", choices=("codex", "claude"), default="codex")
    parser.add_argument("--existing-login", action="store_true",
                        help="Reuse the authorized CLI login; it is locked for a whole trial, so cases run one at a time")
    parser.add_argument("--model")
    parser.add_argument("--timeout", type=float, default=DEFAULT_TIMEOUT, help=f"Per-command timeout (default {DEFAULT_TIMEOUT})")
    parser.add_argument("--speed-trims", action="store_true", help="Also apply each case's optional speed trims")
    parser.add_argument("--output", type=Path, help="Directory for <id>-<agent> evidence and logs")
    parser.add_argument("--dry-run", action="store_true", help="Print each case's run_eval.py arguments and exit")
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
    if not args.dry_run:
        if missing:
            parser.error("no image for: " + ", ".join(missing) + " (pass --image-map KEY=IMAGE)")
        if not args.ddtest or not args.output:
            parser.error("trials require --ddtest and --output")
    output = Path(os.path.abspath(args.output)) if args.output else Path("<output>")
    ddtest = Path(os.path.abspath(args.ddtest)) if args.ddtest else "<ddtest>"
    plans = []
    for case in cases:
        evidence = output / f"{case['id']}-{args.agent}"
        plans.append((case, evidence, build_argv(case, images.get(case["image"], f"<image:{case['image']}>"), ddtest,
                                                 evidence, args.agent, args.existing_login, args.timeout,
                                                 args.speed_trims, args.model)))
    if args.dry_run:
        for case, evidence, arguments in plans:
            print(f"# {case['id']} ({case['framework']}, {case['ci_provider']}, image {case['image']})")
            print(shlex.join(arguments))
            print(shlex.join([sys.executable, str(RUN_EVAL), "--cleanup", str(evidence)]) + "\n")
        return 0
    output.mkdir(parents=True, exist_ok=True)
    failed = 0
    with Supervisor() as supervisor:
        for case, evidence, arguments in plans:
            if supervisor.signal:
                break
            if evidence.exists():
                print(f"{case['id']}: skipped, {evidence} exists", flush=True)
                failed += 1
                continue
            failed += bool(run_case(case, arguments, evidence, output / f"{evidence.name}.log", supervisor))
    if supervisor.signal:
        remaining = [case["id"] for case, evidence, _ in plans if not evidence.exists()]
        print(f"Interrupted by signal {supervisor.signal}; not run: {', '.join(remaining) or 'none'}", flush=True)
        return 128 + supervisor.signal
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
