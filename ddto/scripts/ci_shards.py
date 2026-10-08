#!/usr/bin/env python3
"""Run disjoint CI groups and collect complete, revision-matched parity evidence."""

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time

import parity_report


ROOT = Path(__file__).resolve().parents[1]
INTEGRATION = "github.com/tonyredondo/dd-ci-testing-poc/integration"
CONFIGURATIONS = (
    ("differential", "ubuntu-latest", "1.26.x", "normal"),
    ("differential", "ubuntu-latest", "1.26.x", "race"),
    ("differential", "ubuntu-latest", "1.27.x", "normal"),
    ("differential", "ubuntu-latest", "1.27.x", "race"),
    ("differential", "macos-latest", "1.27.x", "normal"),
    ("differential", "windows-latest", "1.27.x", "normal"),
    ("mini", "ubuntu-latest", "1.25.x", "normal"),
    ("mini", "ubuntu-latest", "1.25.x", "race"),
    ("mini", "ubuntu-latest", "tip", "normal"),
)
SHARDS = {
    "differential": ("units", "cli", "parity", "fuzz", "orchestrion", "sdk"),
    "mini": ("units", "native", "fuzz"),
}
TEST_NAME = re.compile(r"^(?:Test|Fuzz|Example)\w*$")
PLATFORMS = {"ubuntu-latest": "linux", "macos-latest": "darwin", "windows-latest": "windows"}


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


def matrix(suite, tip=False):
    return {"include": [dict(suite=s, os=os_name, go=go_version, mode=mode, shard=shard)
                        for s, os_name, go_version, mode in CONFIGURATIONS
                        if s == suite and (go_version == "tip") == tip
                        for shard in SHARDS[s]]}


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


def stream_tests(command, output, units):
    """Keep native output readable while retaining Go's complete JSON event stream."""
    results = {}
    with (output / "tests.jsonl").open("w", encoding="utf-8") as events, \
            (output / "tests.log").open("w", encoding="utf-8") as log:
        with subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              text=True, encoding="utf-8", errors="replace") as process:
            for line in process.stdout:
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    # Download/build diagnostics may appear outside the test JSON.
                    log.write(line)
                    print(line, end="", flush=True)
                    continue
                events.write(line)
                text = event.get("Output", "")
                log.write(text)
                print(text, end="", flush=True)
                name = event.get("Test")
                if units:
                    name = event.get("Package") if not name else None
                if name and (units or TEST_NAME.fullmatch(name)) and event.get("Action") in ("pass", "skip", "fail"):
                    if name in results:
                        raise ValueError(f"duplicate terminal result: {name}")
                    results[name] = event["Action"]
            code = process.wait()
    return code, results


def run(args):
    groups = read_groups()
    config = (args.suite, args.os, args.go, args.mode)
    if config not in CONFIGURATIONS or args.shard not in SHARDS[args.suite]:
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
    flags = ["-race"] if args.mode == "race" else []
    units = args.shard == "units"
    if units:
        selected = sorted(package for package in capture("go", "list", "./...").splitlines()
                          if package != INTEGRATION)
        targets = selected
        run_flags = []
    else:
        listing = capture("go", "test", *flags, "-list=^(Test|Fuzz|Example)", "./integration")
        validate_inventory(groups, [line for line in listing.splitlines() if TEST_NAME.fullmatch(line)], args.os)
        selected = selected_tests(groups, args.suite, args.go, args.shard, args.os)
        targets = ["./integration"]
        run_flags = ["-run=^(" + "|".join(selected) + ")$"]
    if not selected:
        raise ValueError("empty CI selection")
    os.environ["PARITY_TEST_MODE"] = args.mode
    os.environ["PARITY_EXECUTION_ORDER"] = "mini-first" if args.mode == "race" else "sdk-first"
    report = "parity-race" if args.mode == "race" else "parity"
    os.environ["PARITY_REPORT_PATH"] = str(args.output.resolve() / (report + ".json"))
    command = ["go", "test", *flags, "-json", "-count=1", "-timeout=" + args.timeout, *run_flags, *targets]
    manifest = dict(schema=1, suite=args.suite, os=args.os, go=args.go, mode=args.mode, shard=args.shard,
                    revision=capture("git", "rev-parse", "HEAD"), tree=capture("git", "rev-parse", "HEAD^{tree}"),
                    toolchain=environment, tip_sha=os.environ.get("GO_TIP_SHA", ""),
                    selected=selected, command=command, status="running")
    path = args.output / "manifest.json"
    path.write_text(json.dumps(manifest, indent=2) + "\n")
    started = time.monotonic()
    code, results = stream_tests(command, args.output, units)
    manifest.update(duration_seconds=time.monotonic() - started, exit_code=code, results=results, status="failed")
    path.write_text(json.dumps(manifest, indent=2) + "\n")
    if code:
        raise ValueError(f"go test exited {code}; see {args.output / 'tests.log'}")
    check_results(selected, results, allowed_skips(args.os, args.shard))
    manifest["status"] = "passed"
    path.write_text(json.dumps(manifest, indent=2) + "\n")


def collect_manifests(source, groups, revision, tree):
    expected = {(*config, shard) for config in CONFIGURATIONS for shard in SHARDS[config[0]]}
    manifests = {}
    for path in sorted(source.glob("*/manifest.json")):
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
    for config in CONFIGURATIONS:
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
    manifests = collect_manifests(args.input, read_groups(), capture("git", "rev-parse", "HEAD"),
                                 capture("git", "rev-parse", "HEAD^{tree}"))
    args.output.mkdir(parents=True, exist_ok=False)
    summary = ["# Compatibility shards", "", "All selected tests and runtime packages completed. "
               "Durations below cover each `go test` invocation, including compilation.", "",
               "| Suite | Platform | Go | Mode | Group | Seconds | Passed | Skipped |",
               "| --- | --- | --- | --- | --- | ---: | ---: | ---: |"]
    for config in CONFIGURATIONS:
        suite, platform, version, mode = config
        destination = args.output / f"{suite}-{platform}-go-{version}-{mode}"
        destination.mkdir()
        for shard in SHARDS[suite]:
            path, item = manifests[(*config, shard)]
            counts = list(item["results"].values())
            summary.append(f"| {suite} | {platform} | {version} | {mode} | {shard} | "
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
    (args.output / "summary.md").write_text("\n".join(summary) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("matrix")
    run_parser = commands.add_parser("run")
    for name in ("suite", "os", "go", "mode", "shard"):
        run_parser.add_argument("--" + name, required=True)
    run_parser.add_argument("--timeout", default="20m")
    run_parser.add_argument("--output", type=Path, required=True)
    aggregate_parser = commands.add_parser("aggregate")
    aggregate_parser.add_argument("--input", type=Path, required=True)
    aggregate_parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "matrix":
            read_groups()
            for name, suite, tip in (("differential", "differential", False), ("mini", "mini", False), ("tip", "mini", True)):
                print(name + "=" + json.dumps(matrix(suite, tip), separators=(",", ":")))
        elif args.command == "run":
            run(args)
        else:
            aggregate(args)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"ci_shards: {error}\n")


if __name__ == "__main__":
    main()
