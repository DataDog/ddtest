#!/usr/bin/env python3
"""Run disjoint CI groups and collect complete, revision-matched parity evidence."""

import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import threading
import time

import parity_report


ROOT = Path(__file__).resolve().parents[1]
INTEGRATION = "github.com/tonyredondo/dd-ci-testing-poc/integration"
CONFIGURATIONS = (
    ("differential", "ubuntu-latest", "1.26.x", "normal"),
    ("differential", "ubuntu-latest", "1.27.x", "normal"),
    ("differential", "ubuntu-latest", "1.27.x", "race"),
    ("differential", "macos-latest", "1.27.x", "normal"),
    ("differential", "windows-latest", "1.27.x", "normal"),
    ("mini", "ubuntu-latest", "1.25.x", "normal"),
    ("mini", "ubuntu-latest", "1.25.x", "race"),
    ("mini", "ubuntu-latest", "tip", "normal"),
)
SHARDS = {
    "differential": ("units", "cli", "transparency", "parity", "testify", "fuzz", "orchestrion", "sdk", "sdk-orchestrion"),
    "mini": ("units", "native", "fuzz"),
}
TEST_NAME = re.compile(r"^(?:Test|Fuzz|Example)\w*$")
PLATFORMS = {"ubuntu-latest": "linux", "macos-latest": "darwin", "windows-latest": "windows"}
# A race-enabled program sleeps for one second before exiting so that other
# goroutines can finish their reports. Fixtures start thousands of programs.
RACE_OPTIONS = "atexit_sleep_ms=0"


def job_key(config):
    suite, platform, _, mode = config
    return f"{suite}/{platform}/{mode}"


def read_groups(path=ROOT / "scripts/ci_shards.json"):
    groups = json.loads(path.read_text())
    for suite in SHARDS:
        if set(groups[suite]) != set(SHARDS[suite]) - {"units"}:
            raise ValueError(f"unexpected groups for {suite}")
        names = [name for group in groups[suite].values() for name in group]
        if len(names) != len(set(names)):
            raise ValueError(f"duplicated {suite} test assignment")
    all_tests = {name for group in groups["differential"].values() for name in group}
    for group in (*groups["mini"].values(), *groups["tip_additional"].values()):
        if not set(group) <= all_tests:
            raise ValueError("native selection contains an unassigned test")
    for shard, names in groups["tip_additional"].items():
        if shard not in groups["mini"] or set(names) & set(groups["mini"][shard]):
            raise ValueError("invalid additional tip selection")
    for platform in PLATFORMS:
        if not set(groups["platform_tests"][PLATFORMS[platform]]) <= set(SHARDS["differential"]) - {"units"}:
            raise ValueError("unexpected platform group")
        assigned = [name for shard in SHARDS["differential"][1:]
                    for name in selected_tests(groups, "differential", "1.27.x", shard, platform)]
        if len(assigned) != len(set(assigned)) or any(not TEST_NAME.fullmatch(name) for name in assigned):
            raise ValueError("invalid platform test assignment")
    tip_names = [name for shard in SHARDS["mini"][1:]
                 for name in selected_tests(groups, "mini", "tip", shard, "ubuntu-latest")]
    if len(tip_names) != len(set(tip_names)):
        raise ValueError("duplicated tip test assignment")
    # Every configuration runs each of its groups in exactly one job.
    keys = {job_key(config) for config in CONFIGURATIONS}
    if set(groups["jobs"]) != keys:
        raise ValueError("job layouts must match the configurations")
    for key, jobs in groups["jobs"].items():
        packed = [shard for job in jobs for shard in job]
        if any(not job for job in jobs) or sorted(packed) != sorted(SHARDS[key.split("/")[0]]):
            raise ValueError(f"job layout for {key} must run every group once")
    return groups


def selected_tests(groups, suite, go_version, shard, platform):
    names = list(groups[suite][shard])
    if suite == "differential":
        names += groups["platform_tests"][PLATFORMS[platform]].get(shard, [])
    if suite == "mini" and go_version == "tip":
        names += groups["tip_additional"].get(shard, [])
    if len(names) != len(set(names)):
        raise ValueError("duplicated selected test")
    return sorted(names)


def validate_inventory(groups, discovered, platform):
    assigned = {name for shard in SHARDS["differential"][1:]
                for name in selected_tests(groups, "differential", "1.27.x", shard, platform)}
    if assigned != set(discovered):
        raise ValueError(f"test inventory changed: unassigned={sorted(set(discovered) - assigned)} "
                         f"not found={sorted(assigned - set(discovered))}")


def configurations(tip):
    return tuple(config for config in CONFIGURATIONS if tip or config[2] != "tip")


def matrix(groups, suite, tip=False):
    include = []
    for config in CONFIGURATIONS:
        s, os_name, go_version, mode = config
        if s != suite or (go_version == "tip") != tip:
            continue
        for index, shards in enumerate(groups["jobs"][job_key(config)]):
            # Jobs of one configuration share a Go cache; the first job saves it.
            include.append(dict(suite=s, os=os_name, go=go_version, mode=mode,
                                shards=",".join(shards), job="-".join(shards), owner=index == 0))
    return {"include": include}


def capture(*command):
    return subprocess.check_output(command, cwd=ROOT, text=True).strip()


def check_results(expected, results, allowed_skips=None):
    if not expected or len(expected) != len(set(expected)):
        raise ValueError("empty or duplicated execution selection")
    if set(expected) != set(results):
        raise ValueError(f"execution differs from selection: missing={sorted(set(expected) - set(results))} "
                         f"unexpected={sorted(set(results) - set(expected))}")
    if any(action not in ("pass", "skip") for action in results.values()):
        raise ValueError("a selected test or package did not finish successfully")
    if allowed_skips is not None and any(name not in allowed_skips and action == "skip" for name, action in results.items()):
        raise ValueError("an integration test was unexpectedly skipped")


def allowed_skips(platform, shard):
    if shard == "units":
        return None  # Go emits a package skip for packages without test files.
    if platform == "windows-latest":
        return {"TestMiniChdirSymlinkWithUserTool", "TestWorkspaceCommandPreservesLogicalWorkingDirectory"}
    return set()


def validate_tip_version(version, source_sha):
    # cmd/dist uses Git's abbreviated hash. Both its current devel_ form and
    # older devel prefixes are valid; the abbreviation length is not fixed.
    match = re.search(r"(?:go[0-9.]+-devel_|devel go[0-9.]+-)([0-9a-f]{7,40})\b", version)
    if not re.fullmatch(r"[0-9a-f]{40}", source_sha) or not match or not source_sha.startswith(match[1]):
        raise ValueError(f"Go tip source revision {source_sha!r} differs from toolchain {version!r}")


def race_options(current):
    if "atexit_sleep_ms" in current:
        return current
    return (current + " " + RACE_OPTIONS).strip()


PRINT_LOCK = threading.Lock()


def show(text, prefix):
    if prefix:
        text = "".join(prefix + line for line in text.splitlines(keepends=True))
    with PRINT_LOCK:
        print(text, end="", flush=True)


def stream_tests(command, output, units, env=None, prefix=""):
    """Keep native output readable while retaining Go's complete JSON event stream."""
    results = {}
    with (output / "tests.jsonl").open("w", encoding="utf-8") as events, \
            (output / "tests.log").open("w", encoding="utf-8") as log:
        with subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              text=True, encoding="utf-8", errors="replace") as process:
            for line in process.stdout:
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    # Download/build diagnostics may appear outside the test JSON.
                    log.write(line)
                    show(line, prefix)
                    continue
                events.write(line)
                text = event.get("Output", "")
                log.write(text)
                show(text, prefix)
                name = event.get("Test")
                if units:
                    name = event.get("Package") if not name else None
                if name and (units or TEST_NAME.fullmatch(name)) and event.get("Action") in ("pass", "skip", "fail"):
                    if name in results:
                        raise ValueError(f"duplicate terminal result: {name}")
                    results[name] = event["Action"]
            code = process.wait()
    return code, results


def run_group(args, groups, shard, base, environment, shards):
    """Run one group into its own directory, so jobs can hold several groups."""
    output = args.output / shard
    output.mkdir()
    flags = ["-race"] if args.mode == "race" else []
    units = shard == "units"
    if units:
        selected = sorted(package for package in capture("go", "list", "./...").splitlines()
                          if package != INTEGRATION)
        targets = selected
        # TestConv64 alone walks 2^31 floats; under the race detector that takes
        # four minutes. Normal-mode unit groups still run the complete loop.
        run_flags = ["-short"] if args.mode == "race" else []
    else:
        selected = selected_tests(groups, args.suite, args.go, shard, args.os)
        targets = ["./integration"]
        run_flags = ["-run=^(" + "|".join(selected) + ")$"]
    if not selected:
        raise ValueError("empty CI selection")
    report = "parity-race" if args.mode == "race" else "parity"
    env = dict(base, PARITY_REPORT_PATH=str(output.resolve() / (report + ".json")))
    command = ["go", "test", *flags, "-json", "-count=1", "-timeout=" + args.timeout, *run_flags, *targets]
    manifest = dict(schema=1, suite=args.suite, os=args.os, go=args.go, mode=args.mode, shard=shard,
                    revision=capture("git", "rev-parse", "HEAD"), tree=capture("git", "rev-parse", "HEAD^{tree}"),
                    toolchain=environment, tip_sha=os.environ.get("GO_TIP_SHA", ""),
                    selected=selected, command=command, job=shards, parallel=args.parallel, status="running")
    path = output / "manifest.json"
    path.write_text(json.dumps(manifest, indent=2) + "\n")
    started = time.monotonic()
    prefix = f"[{shard}] " if args.parallel > 1 and len(shards) > 1 else ""
    code, results = stream_tests(command, output, units, env, prefix)
    manifest.update(duration_seconds=time.monotonic() - started, exit_code=code, results=results, status="failed")
    path.write_text(json.dumps(manifest, indent=2) + "\n")
    if code:
        raise ValueError(f"go test exited {code}; see {output / 'tests.log'}")
    check_results(selected, results, allowed_skips(args.os, shard))
    manifest["status"] = "passed"
    path.write_text(json.dumps(manifest, indent=2) + "\n")


def run(args):
    groups = read_groups()
    config = (args.suite, args.os, args.go, args.mode)
    shards = args.shard.split(",")
    if config not in CONFIGURATIONS or not shards or len(shards) != len(set(shards)) \
            or any(shard not in SHARDS[args.suite] for shard in shards) or args.parallel < 1:
        raise ValueError("unsupported CI configuration")
    if args.output.exists():
        raise ValueError("output directory already exists; choose a fresh directory")
    args.output.mkdir(parents=True)
    environment = json.loads(capture("go", "env", "-json", "GOVERSION", "GOOS", "GOARCH"))
    version = environment["GOVERSION"]
    if args.go == "tip":
        validate_tip_version(version, os.environ.get("GO_TIP_SHA", ""))
    elif not version.startswith("go" + args.go.removesuffix(".x") + "."):
        raise ValueError(f"unexpected toolchain {version} for {args.go}")
    expected_os = PLATFORMS[args.os]
    if environment["GOOS"] != expected_os:
        raise ValueError("selected CI platform differs from go env GOOS")
    if any(shard != "units" for shard in shards):
        # One listing checks the complete assignment for every group in the job.
        flags = ["-race"] if args.mode == "race" else []
        listing = capture("go", "test", *flags, "-list=^(Test|Fuzz|Example)", "./integration")
        validate_inventory(groups, [line for line in listing.splitlines() if TEST_NAME.fullmatch(line)], args.os)
    base = dict(os.environ, PARITY_TEST_MODE=args.mode,
                PARITY_EXECUTION_ORDER="mini-first" if args.mode == "race" else "sdk-first",
                GORACE=race_options(os.environ.get("GORACE", "")))
    failures = []

    def attempt(shard):
        try:
            run_group(args, groups, shard, base, environment, shards)
        except (ValueError, OSError, subprocess.CalledProcessError) as error:
            failures.append(f"{shard}: {error}")

    if args.parallel == 1:
        for shard in shards:
            attempt(shard)
    else:
        with ThreadPoolExecutor(max_workers=args.parallel) as pool:
            list(pool.map(attempt, shards))
    if failures:
        raise ValueError("; ".join(sorted(failures)))


def retry(args, sleep=time.sleep):
    """Run a network step again when it fails or stalls; a module fetch can hang for minutes."""
    command = args.argv[1:] if args.argv[:1] == ["--"] else args.argv
    if not command or args.attempts < 1:
        raise ValueError("retry requires a command and at least one attempt")
    for attempt in range(1, args.attempts + 1):
        try:
            if subprocess.run(command, cwd=ROOT, timeout=args.timeout).returncode == 0:
                return
            reason = "failed"
        except subprocess.TimeoutExpired:
            reason = f"did not finish within {args.timeout} seconds"
        print(f"::warning::attempt {attempt} of {' '.join(command)} {reason}", flush=True)
        if attempt < args.attempts:
            sleep(15 * attempt)
    raise ValueError(f"{' '.join(command)} failed after {args.attempts} attempts")


def collect_manifests(source, groups, revision, tree, expected_configurations=CONFIGURATIONS):
    expected = {(*config, shard) for config in expected_configurations for shard in SHARDS[config[0]]}
    manifests = {}
    for path in sorted(source.rglob("manifest.json")):
        item = json.loads(path.read_text())
        identity = tuple(item[key] for key in ("suite", "os", "go", "mode", "shard"))
        if identity not in expected or identity in manifests:
            raise ValueError(f"unexpected or duplicated shard: {identity}")
        if item.get("schema") != 1 or item.get("status") != "passed" or item.get("exit_code") != 0:
            raise ValueError(f"unfinished or failed shard: {identity}")
        if item["revision"] != revision or item["tree"] != tree:
            raise ValueError(f"shard tested a different checkout: {identity}")
        if identity[-1] != "units" and item["selected"] != selected_tests(groups, identity[0], identity[2], identity[-1], identity[1]):
            raise ValueError(f"shard changed its test selection: {identity}")
        check_results(item["selected"], item["results"], allowed_skips(identity[1], identity[-1]))
        manifests[identity] = (path, item)
    if set(manifests) != expected:
        raise ValueError(f"missing shards: {sorted(expected - set(manifests))}")
    for config in expected_configurations:
        items = [manifests[(*config, shard)][1] for shard in SHARDS[config[0]]]
        if any(item["toolchain"] != items[0]["toolchain"] or item["tip_sha"] != items[0]["tip_sha"] for item in items):
            raise ValueError(f"shards used different toolchains: {config}")
    return manifests


def copy_reports(source, destination):
    for path in sorted(source.glob("parity*.json")):
        target = destination / path.name
        if target.exists():
            raise ValueError(f"multiple shards wrote {path.name}")
        shutil.copyfile(path, target)


def aggregate(args):
    expected = configurations(args.with_tip)
    manifests = collect_manifests(args.input, read_groups(), capture("git", "rev-parse", "HEAD"),
                                  capture("git", "rev-parse", "HEAD^{tree}"), expected)
    args.output.mkdir(parents=True, exist_ok=False)
    summary = ["# Compatibility shards", "", "All selected tests and runtime packages completed. "
               "Durations below cover each `go test` invocation, including compilation. "
               "Groups that share a job may run at the same time.", "",
               "| Suite | Platform | Go | Mode | Group | Job | Seconds | Passed | Skipped |",
               "| --- | --- | --- | --- | --- | --- | ---: | ---: | ---: |"]
    for config in expected:
        suite, platform, version, mode = config
        destination = args.output / f"{suite}-{platform}-go-{version}-{mode}"
        destination.mkdir()
        for shard in SHARDS[suite]:
            path, item = manifests[(*config, shard)]
            counts = list(item["results"].values())
            job = "+".join(item.get("job", [shard]))
            summary.append(f"| {suite} | {platform} | {version} | {mode} | {shard} | {job} | "
                           f"{item['duration_seconds']:.2f} | {counts.count('pass')} | {counts.count('skip')} |")
            shutil.copyfile(path, destination / f"manifest-{shard}.json")
            copy_reports(path.parent, destination)
        if suite == "differential":
            report = destination / ("parity-race.json" if mode == "race" else "parity.json")
            recorded = json.loads(report.read_text())
            toolchain = manifests[(*config, "units")][1]["toolchain"]
            if (recorded["go"], recorded["os"], recorded["architecture"]) != (
                    toolchain["GOVERSION"], toolchain["GOOS"], toolchain["GOARCH"]):
                raise ValueError(f"parity report used a different toolchain: {config}")
            (destination / "parity.md").write_text(parity_report.render(report))
            summary += ["", f"[{platform}, Go {version}, {mode} parity]({destination.name}/parity.md)", ""]
    if not args.with_tip:
        summary += ["", "Go tip runs in the scheduled and manually dispatched workflow.", ""]
    (args.output / "summary.md").write_text("\n".join(summary) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    matrix_parser = commands.add_parser("matrix")
    matrix_parser.add_argument("--with-tip", action="store_true")
    run_parser = commands.add_parser("run")
    for name in ("suite", "os", "go", "mode"):
        run_parser.add_argument("--" + name, required=True)
    run_parser.add_argument("--shard", required=True, help="comma-separated groups to run in this job")
    run_parser.add_argument("--parallel", type=int, default=1, help="groups to run at the same time")
    run_parser.add_argument("--timeout", default="20m")
    run_parser.add_argument("--output", type=Path, required=True)
    aggregate_parser = commands.add_parser("aggregate")
    aggregate_parser.add_argument("--input", type=Path, required=True)
    aggregate_parser.add_argument("--output", type=Path, required=True)
    aggregate_parser.add_argument("--with-tip", action="store_true")
    retry_parser = commands.add_parser("retry")
    retry_parser.add_argument("--attempts", type=int, default=3)
    retry_parser.add_argument("--timeout", type=float, default=300, help="seconds per attempt")
    retry_parser.add_argument("argv", nargs=argparse.REMAINDER, metavar="command")
    args = parser.parse_args()
    try:
        if args.command == "matrix":
            groups = read_groups()
            for name, suite, tip in (("differential", "differential", False), ("mini", "mini", False), ("tip", "mini", True)):
                value = matrix(groups, suite, tip) if not tip or args.with_tip else {"include": []}
                print(name + "=" + json.dumps(value, separators=(",", ":")))
        elif args.command == "run":
            run(args)
        elif args.command == "retry":
            retry(args)
        else:
            aggregate(args)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"ci_shards: {error}\n")


if __name__ == "__main__":
    main()
