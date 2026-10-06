#!/usr/bin/env python3
"""Compile-only four-variant benchmarks and offline Markdown table generation.

The run command requires Linux, cgroup v2 and a systemd user manager. Every
build gets an exclusive scope so detached Orchestrion daemons are accounted
for. No test executable is run. See docs/build-benchmarks.md for the protocol.
"""

import argparse
import csv
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import random
import re
import shutil
import signal
import statistics
import subprocess
import sys
import threading
import time

ROOT = Path(__file__).resolve().parents[1]
CONFIG = ROOT / "scripts/build-benchmark.json"
INPUTS = ROOT / "scripts/testdata/build-benchmark"
VARIANTS = ("native", "orchestrion", "sdk", "mini")
SCENARIOS = {
    "cold": "Cold compilation",
    "cached": "Cached compilation — unchanged output reused",
    "warm-link": "Warm dependencies — forced fresh link",
    "test-body-edit": "Incremental compilation — reachable test-body edit",
    "edited": "Unused-constant edit — diagnostic",
}
FIELDS = ("series", "project", "cpus", "variant", "scenario", "iteration",
          "validation_only", "wall_s", "cpu_s", "user_s", "system_s",
          "peak_bytes", "binary_bytes", "returncode", "process_tree_drained",
          "post_exit_wait_s", "cold_output_removed", "command")
SDK_MODULE = "github.com/DataDog/dd-trace-go/v2"
POC_MODULE = "github.com/tonyredondo/dd-ci-testing-poc"


def read_json(path):
    return json.loads(path.read_text())


def write_json(path, value):
    # A killed driver must leave the last complete checkpoint readable.
    temporary = path.with_suffix(path.suffix + ".new")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(path)


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def boolean(value):
    if value in (True, "True", "true"):
        return True
    if value in (False, "False", "false"):
        return False
    raise ValueError(f"invalid boolean: {value!r}")


def observations(directory, manifest):
    """Reject failures, duplicates and incomplete cells instead of dropping rows."""
    path = directory / "observations.csv"
    if manifest.get("raw_csv_sha256") and digest(path) != manifest["raw_csv_sha256"]:
        raise ValueError("raw CSV does not match its recorded SHA256")
    with path.open(newline="") as source:
        rows = list(csv.DictReader(source))
    grouped, seen = {}, set()
    cases = {case["id"]: case for case in manifest["cases"]}
    for row in rows:
        wall = float(row["wall_s"])
        if int(row["returncode"]) or not boolean(row["process_tree_drained"]):
            raise ValueError("a failed or undrained command is present")
        if not math.isfinite(wall) or wall <= 0:
            raise ValueError("wall time must be finite and positive")
        command = json.loads(row["command"])
        if "-c" not in command or "test" not in command or "-o" not in command:
            raise ValueError("observation is not a compile-only test command")
        key = tuple(row[k] for k in ("series", "project", "cpus", "variant", "scenario", "iteration"))
        if key in seen:
            raise ValueError(f"duplicate observation: {key}")
        seen.add(key)
        if boolean(row["validation_only"]):
            continue
        case = cases.get(row["project"])
        if case is None or row["series"] != case["series"]:
            raise ValueError("unknown case or changed series")
        if row["scenario"] not in SCENARIOS or row["variant"] not in VARIANTS:
            raise ValueError("unknown scenario or variant")
        cpus = int(row["cpus"])
        if cpus not in manifest["cpus"]:
            raise ValueError("unexpected CPU configuration")
        if row["scenario"] == "cold" and not boolean(row["cold_output_removed"]):
            raise ValueError("cold observation retained its previous output")
        grouped.setdefault((row["project"], cpus, row["scenario"], row["variant"]), []).append(row)
    for case in manifest["cases"]:
        for cpus in manifest["cpus"]:
            for scenario in SCENARIOS:
                count = manifest["repetitions"][case["series"]][scenario]
                for variant in VARIANTS:
                    key = (case["id"], cpus, scenario, variant)
                    current = grouped.get(key, [])
                    if sorted(int(r["iteration"]) for r in current) != list(range(count)):
                        raise ValueError(f"incomplete or unexpected repetitions: {key}")
    return rows, grouped


def distribution(values):
    mean = statistics.mean(values)
    # Fixed seed and resample count make offline regeneration deterministic.
    rng = random.Random(20261003)
    bootstrap = sorted(statistics.median(rng.choices(values, k=len(values))) for _ in range(2000))
    return {"n": len(values), "median": statistics.median(values),
            "min": min(values), "max": max(values),
            "cv_pct": 100 * statistics.pstdev(values) / mean,
            "bootstrap95_median": [bootstrap[50], bootstrap[1949]]}


def summarize(grouped):
    result = {}
    for (case, cpus, scenario, variant), rows in grouped.items():
        key = f"{case}/{cpus}/{scenario}"
        stats = {"wall_s": distribution([float(r["wall_s"]) for r in rows])}
        for field in ("cpu_s", "peak_bytes"):
            stats[field + "_median"] = statistics.median(float(r[field]) for r in rows)
        result.setdefault(key, {})[variant] = stats
    return result


def table_lines(manifest, summary, scenarios=None, cases=None):
    lines = []
    for scenario in scenarios or SCENARIOS:
        lines += ["## " + SCENARIOS[scenario], "",
                  "| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |",
                  "| --- | --- | ---: | ---: | ---: | ---: | ---: |"]
        for case in cases or manifest["cases"]:
            for cpus in manifest["cpus"]:
                cell = summary[f"{case['id']}/{cpus}/{scenario}"]
                values = []
                for variant in VARIANTS:
                    median = cell[variant]["wall_s"]["median"]
                    text = f"{median:.3f} s"
                    if variant in ("sdk", "mini"):
                        reference = cell["orchestrion"]["wall_s"]["median"]
                        native = cell["native"]["wall_s"]["median"]
                        text += f" ({100 * (median / reference - 1):+.1f}%; {100 * (median / native - 1):+.1f}%)"
                    values.append(text)
                flags = " ".join(case["flags"]) or "none"
                lines.append(f"| {case['label']} | `{flags}` | {cpus} | " + " | ".join(values) + " |")
        lines.append("")
    return lines


def render(directory):
    manifest = read_json(directory / "manifest.json")
    if not manifest.get("historical") and manifest.get("status") != "verified":
        raise ValueError("the run has not passed output qualification")
    rows, grouped = observations(directory, manifest)
    summary = summarize(grouped)
    header = (directory / "methodology.md").read_text().rstrip()
    notes = (directory / "notes.md").read_text().rstrip() if (directory / "notes.md").exists() else ""
    report = header + "\n\n" + "\n".join(table_lines(manifest, summary)) + "\n" + notes + "\n"
    measured = sum(not boolean(r["validation_only"]) for r in rows)
    return report, {"schema_version": 1, "source_head": manifest["source_head"],
                    "completed_commands": len(rows), "measured_observations": measured,
                    "controls_and_trace_checks": len(rows) - measured,
                    "bootstrap_resamples": 2000, "bootstrap_seed": 20261003,
                    "statistics": summary}


def overview_lines(manifest, summary, link):
    """Keep the README short; comparison tables belong in the linked reports."""
    return "\n".join([
        "## Benchmarks", "",
        "[Build time, runtime and memory comparisons](docs/benchmarks.md) cover",
        "Native, Orchestrion, POC SDK and POC Mini at 4/32 CPUs, including Testify,",
        "coverage, race and deferred delivery. The report also links the repeated",
        "115-case CI parity comparison and records failed runtime combinations.", "",
        f"The [compile-only dataset]({link}/README.md) contains",
        f"{summary['measured_observations']:,} comparative observations, measured at",
        f"POC commit `{manifest['source_head']}`.", "",
        "[Run benchmarks or regenerate the tables](docs/build-benchmarks.md).", "",
    ])


def report_command(args):
    directory = args.input.resolve()
    markdown, summary = render(directory)
    output = args.output or directory / "README.md"
    updated_readme = None
    if getattr(args, "update_readme", False):
        link = directory.relative_to(ROOT).as_posix()
        readme = (ROOT / "README.md").read_text()
        begin, end = "<!-- build-benchmark-summary:start -->", "<!-- build-benchmark-summary:end -->"
        if readme.count(begin) != 1 or readme.count(end) != 1:
            raise ValueError("README must contain exactly one benchmark summary block")
        before, tail = readme.split(begin)
        _, after = tail.split(end)
        updated_readme = before + begin + "\n" + overview_lines(read_json(directory / "manifest.json"), summary, link) + end + after
        if args.check and readme != updated_readme:
            raise ValueError("README benchmark links are stale")
    if args.check:
        if output.read_text() != markdown or read_json(directory / "statistics.json") != summary:
            raise ValueError("generated tables or statistics are stale")
    else:
        output.write_text(markdown)
        write_json(directory / "statistics.json", summary)
        if updated_readme is not None:
            (ROOT / "README.md").write_text(updated_readme)
    print(f"{summary['measured_observations']} observations; tables in seconds: {output}")


def cgroup_measure(output, timeout, command):
    """Executed inside one scope; CPU is read only after its descendants exit."""
    relative = next(line.split(":", 2)[2] for line in Path("/proc/self/cgroup").read_text().splitlines()
                    if line.startswith("0::"))
    group = Path("/sys/fs/cgroup") / relative.lstrip("/")
    if not any(part.startswith("ddci-bench-") for part in group.parts):
        raise ValueError("measurement requires the runner's exclusive cgroup")

    def cpu():
        return dict(line.split() for line in (group / "cpu.stat").read_text().splitlines())

    def survivors():
        processes = set()
        for path in group.rglob("cgroup.procs"):
            try:
                processes.update(map(int, path.read_text().split()))
            except FileNotFoundError:
                pass
        return processes - {os.getpid()}

    expired = threading.Event()

    def terminate():
        expired.set()
        for pid in survivors():
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass

    before = cpu()
    started = time.perf_counter()
    with output.with_suffix(".log").open("wb") as log:
        child = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT)
        timer = threading.Timer(timeout, terminate)
        timer.start()
        try:
            code = child.wait()
        finally:
            timer.cancel()
    wall = time.perf_counter() - started
    drain_started = time.perf_counter()
    while survivors() and time.perf_counter() - drain_started < 20:
        time.sleep(.02)
    after, remaining = cpu(), survivors()
    row = {"command": command, "returncode": code, "wall_s": wall,
           "cpu_s": (int(after["usage_usec"]) - int(before["usage_usec"])) / 1e6,
           "user_s": (int(after["user_usec"]) - int(before["user_usec"])) / 1e6,
           "system_s": (int(after["system_usec"]) - int(before["system_usec"])) / 1e6,
           "peak_bytes": int((group / "memory.peak").read_text()),
           "post_exit_wait_s": time.perf_counter() - drain_started,
           "process_tree_drained": not remaining, "surviving_pids": sorted(remaining),
           "timeout": expired.is_set(), "affinity": sorted(os.sched_getaffinity(0))}
    write_json(output, row)
    if remaining:
        terminate()
    return code if code else int(bool(remaining or expired.is_set()))


class Runner:
    """Owns one new artifact directory and leaves source checkouts untouched."""

    def __init__(self, args):
        self.args = args
        self.started = time.monotonic()
        self.config = read_json(CONFIG)
        self.output = args.output.resolve()
        if self.output.exists() or self.output == Path("/tmp") or Path("/tmp") in self.output.parents:
            raise ValueError("use a new output directory outside /tmp")
        if ROOT == self.output or ROOT in self.output.parents:
            raise ValueError("benchmark artifacts must be outside the repository")
        self.go = Path(shutil.which(args.go) or args.go).resolve(strict=True)
        self.orchestrion = args.orchestrion.resolve(strict=True)
        # Go parses -toolexec as command words; an unquoted path with spaces is ambiguous.
        if any(char.isspace() for char in str(self.orchestrion)):
            raise ValueError("Orchestrion executable path must not contain whitespace")
        if sys.platform != "linux" or not Path("/sys/fs/cgroup/cgroup.controllers").exists():
            raise ValueError("run requires Linux with cgroup v2; report works with Python alone")
        for tool in ("systemd-run", "systemctl", "taskset", "readelf"):
            if not shutil.which(tool):
                raise ValueError(f"required executable missing: {tool}")
        available = sorted(os.sched_getaffinity(0))
        if max(args.cpus) > len(available):
            raise ValueError(f"only {len(available)} logical CPUs are available")
        selected = set(args.case or [case["id"] for case in self.config["cases"]])
        if selected - {case["id"] for case in self.config["cases"]}:
            raise ValueError("unknown --case; use list to inspect case IDs")
        self.cases = [case for case in self.config["cases"] if case["id"] in selected]
        if args.control_repeats and "gin" not in selected:
            raise ValueError("native control requires --case gin; use --control-repeats 0 for a subset")
        self.output.mkdir(parents=True)
        for name in ("tmp", "subjects", "caches", "outputs", "rows", "edits"):
            (self.output / name).mkdir()
        self.env = {key: value for key, value in os.environ.items() if key in
                    ("PATH", "HOME", "USER", "LOGNAME", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "LANG", "LC_ALL")}
        self.env.update(PATH=str(self.go.parent) + os.pathsep + self.env.get("PATH", os.defpath),
                        GOTOOLCHAIN="local", GOWORK="off", GOFLAGS="", CGO_ENABLED="1",
                        GOCACHE=str(self.output / "setup-cache"), GOMAXPROCS="4",
                        GOPROXY="https://proxy.golang.org" if args.download else "off",
                        GOSUMDB="sum.golang.org" if args.download else "off",
                        TMPDIR=str(self.output / "tmp"), GOTMPDIR=str(self.output / "tmp"),
                        DD_CIVISIBILITY_ENABLED="parent", DD_INSTRUMENTATION_TELEMETRY_ENABLED="false",
                        DD_APPSEC_ENABLED="false", DD_CIVISIBILITY_GIT_UPLOAD_ENABLED="false",
                        PYTHONDONTWRITEBYTECODE="1")
        self.affinity = {n: available[:n] for n in args.cpus}
        self.count = 0
        self.manifest = {"schema_version": 1, "historical": False,
                         "source_head": self.setup(["git", "rev-parse", "HEAD"], ROOT).strip(),
                         "worktree_status": self.setup(["git", "status", "--porcelain"], ROOT),
                         "toolchain": self.setup([str(self.go), "version"], ROOT).strip(),
                         "sdk_version": self.config["sdk_version"], "sdk_commit": self.config["sdk_commit"],
                         "orchestrion_version": self.config["orchestrion_version"],
                         "cpus": args.cpus, "affinity": self.affinity, "cases": self.cases,
                         "repetitions": self.config["repetitions"], "tests_executed": False,
                         "limits": {"seconds": args.max_seconds, "bytes": int(args.max_gib * 1024 ** 3),
                                    "commands": args.max_commands, "command_seconds": args.command_seconds},
                         "measurement_backend": "exclusive systemd user scope; cgroup v2 cpu.stat and memory.peak"}
        if args.repeats:
            self.manifest["repetitions"] = {series: {scenario: args.repeats for scenario in SCENARIOS}
                                             for series in self.config["repetitions"]}
        self.manifest["cpu_topology"] = {str(cpu): {name: (Path(f"/sys/devices/system/cpu/cpu{cpu}/topology") / name).read_text().strip()
                                                  for name in ("core_id", "physical_package_id")}
                                        for cpu in available[:max(args.cpus)]}
        self.manifest["started_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        self.head = self.manifest["source_head"]
        self.subjects = {}
        self.binaries = []
        with (self.output / "observations.csv").open("w", newline="") as dest:
            csv.DictWriter(dest, fieldnames=FIELDS).writeheader()
        write_json(self.output / "manifest.json", self.manifest)

    def budget(self):
        if time.monotonic() - self.started >= self.args.max_seconds:
            raise RuntimeError("overall time limit reached; evidence retained")
        total = int(subprocess.check_output(["du", "-sb", str(self.output)], text=True).split()[0])
        if total > self.manifest["limits"]["bytes"]:
            raise RuntimeError("artifact limit reached; evidence retained")

    def setup(self, command, cwd):
        remaining = self.args.max_seconds - (time.monotonic() - self.started)
        if remaining <= 0:
            raise RuntimeError("overall time limit reached during setup")
        result = subprocess.run(command, cwd=cwd, env=self.env, text=True, capture_output=True,
                                timeout=min(self.args.command_seconds, remaining), check=True)
        return result.stdout

    def snapshot(self, directory):
        return {str(path): digest(path) for path in directory.rglob("*")
                if path.is_file() and not path.is_symlink()}

    def prepare(self):
        source = read_json(ROOT / "internal/thirdparty/dd-trace-go/SOURCE.json")
        if source["version"] != self.config["sdk_version"] or source["commit"] != self.config["sdk_commit"]:
            raise ValueError("SDK pin changed; update benchmark inputs before measuring")
        version = self.setup([str(self.orchestrion), "version"], ROOT)
        if self.config["orchestrion_version"] not in version:
            raise ValueError("Orchestrion executable does not match the configured version")
        sdk = read_json_string(self.setup([str(self.go), "mod", "download", "-json",
                                          SDK_MODULE + "@" + self.config["sdk_version"]], ROOT))
        yaml = Path(sdk["Dir"]) / "internal/civisibility/integrations/gotesting/orchestrion.yml"
        self.ddtest = self.output / "ddtest"
        self.setup([str(self.go), "build", "-mod=readonly", "-o", str(self.ddtest), "./cmd/ddtest"], ROOT)
        for name in dict.fromkeys(case["subject"] for case in self.cases):
            spec = self.config["subjects"][name]
            target = self.output / "subjects" / name
            if spec.get("fixture"):
                shutil.copytree(INPUTS / name, target)
                (target / "go.mod.template").unlink()
            else:
                module = read_json_string(self.setup([str(self.go), "mod", "download", "-json",
                                                     spec["module"] + "@" + spec["version"]], ROOT))
                if module.get("Origin", {}).get("Hash") != spec["commit"]:
                    raise ValueError(f"upstream revision mismatch: {name}")
                shutil.copytree(module["Dir"], target)
                # Module-cache files are read-only; only this owned copy is changed.
                target.chmod(0o755)
                for path in target.rglob("*"):
                    path.chmod(0o755 if path.is_dir() else 0o644)
            module_text = (INPUTS / name / "go.mod.template").read_text().replace("@POC_ROOT@", json.dumps(str(ROOT)))
            (target / "go.mod").write_text(module_text)
            shutil.copyfile(INPUTS / name / "go.sum", target / "go.sum")
            (target / "orchestrion.yml").write_bytes(yaml.read_bytes())
            (target / "orchestrion.tool.go").write_text(
                '//go:build tools\n\npackage ' + spec["package"] + '\nimport _ "github.com/DataDog/orchestrion"\n')
            before = {f: digest(target / f) for f in ("go.mod", "go.sum")}
            if self.args.download:
                self.setup([str(self.go), "mod", "download"], target)
            self.setup([str(self.go), "list", "-mod=readonly", "-deps", "-test", "./...", "testing",
                        SDK_MODULE + "/civisibility", POC_MODULE + "/testopt"], target)
            if before != {f: digest(target / f) for f in before}:
                raise ValueError("setup changed a frozen module graph")
            modules = self.setup([str(self.go), "list", "-mod=readonly", "-m", "-json", "all"], target)
            (self.output / f"{name}-modules.json").write_text(modules)
            self.subjects[name] = target
            self.budget()
        # Setup builds never warm a measured cache. Timing also forbids downloads.
        shutil.rmtree(self.output / "setup-cache")
        self.env.update(GOPROXY="off", GOSUMDB="off")
        files = self.setup(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], ROOT).split("\0")
        runtime_files = [ROOT / f for f in files if f and (f.endswith(".go") or f in ("go.mod", "go.sum"))]
        self.frozen = {str(path): digest(path) for path in runtime_files}
        self.frozen.update({str(self.go): digest(self.go), str(self.orchestrion): digest(self.orchestrion),
                            str(self.ddtest): digest(self.ddtest), str(Path(__file__).resolve()): digest(Path(__file__).resolve()),
                            str(CONFIG): digest(CONFIG)})
        for target in self.subjects.values():
            self.frozen.update(self.snapshot(target))
        self.manifest.update(source_and_tool_sha256=self.frozen, status="in progress")
        write_json(self.output / "manifest.json", self.manifest)

    def check_frozen(self):
        if self.setup(["git", "rev-parse", "HEAD"], ROOT).strip() != self.head:
            raise ValueError("POC HEAD changed during the experiment")
        for name, expected in self.frozen.items():
            if digest(Path(name)) != expected:
                raise ValueError(f"input drift: {name}")

    def destination(self, case, cpus, variant):
        return self.output / "outputs" / f"{case['id']}-cpu{cpus}" / variant

    def command(self, case, cpus, variant, extra):
        prefixes = {"native": [str(self.go), "test"],
                    "orchestrion": [str(self.go), "test", f"-toolexec={self.orchestrion} toolexec"],
                    "sdk": [str(self.ddtest), "test", "--runtime=sdk"],
                    "mini": [str(self.ddtest), "test", "--runtime=mini"]}
        ldflags = ["-w"] + [flag.removeprefix("-ldflags=") for flag in extra if flag.startswith("-ldflags=")]
        rest = [flag for flag in extra if not flag.startswith("-ldflags=")]
        return prefixes[variant] + [f"-p={cpus}", "-mod=readonly", "-c", "-o",
                                    str(self.destination(case, cpus, variant)) + os.sep,
                                    "-ldflags=" + " ".join(ldflags)] + case["flags"] + rest + ["./..."]

    def measure(self, case, cpus, variant, scenario, iteration, cache, extra=(), validation=False):
        self.budget()
        self.count += 1
        if self.count > self.args.max_commands:
            raise RuntimeError("command-count limit reached")
        label = f"{case['id']}-cpu{cpus}-{scenario}-{iteration}-{variant}"
        output = self.output / "rows" / (label + ".json")
        if output.exists():
            raise ValueError("refusing to overwrite an observation")
        dest = self.destination(case, cpus, variant)
        dest.mkdir(parents=True, exist_ok=True)
        cold = scenario == "cold"
        if cold:
            if any(cache.iterdir()):
                raise ValueError("cold cache is not empty")
            for binary in dest.glob("*.test"):
                binary.unlink()
        command = self.command(case, cpus, variant, extra)
        unit = f"ddci-bench-{os.getpid()}-{self.count}"
        remaining = self.args.max_seconds - (time.monotonic() - self.started)
        timeout = max(1, min(self.args.command_seconds, remaining - 30))
        if remaining <= 30:
            raise RuntimeError("not enough time to start and drain another scope")
        env = dict(self.env, GOMAXPROCS=str(cpus), GOCACHE=str(cache))
        launch = ["systemd-run", "--user", "--scope", "--quiet", "--unit=" + unit,
                  f"--property=RuntimeMaxSec={timeout + 25}", "taskset", "-c",
                  ",".join(map(str, self.affinity[cpus])), sys.executable, str(Path(__file__).resolve()),
                  "_measure", str(output), str(timeout)] + command
        result = subprocess.run(launch, cwd=self.subjects[case["subject"]], env=env,
                                capture_output=True, text=True, timeout=timeout + 35)
        output.with_suffix(".launcher.log").write_text(result.stdout + result.stderr)
        if not output.exists():
            write_json(output.with_suffix(".interruption.json"), {"command": command, "scope": unit,
                                                                  "launcher_exit": result.returncode,
                                                                  "reason": "scope ended without a usable timing"})
            raise RuntimeError(f"no timing for {label}; inspect launcher log")
        row = read_json(output)
        binaries = sorted(dest.glob("*.test"))
        row.update(series=case["series"], project=case["id"], cpus=cpus, variant=variant,
                   scenario=scenario, iteration=iteration, validation_only=validation,
                   cold_output_removed=cold, binary_bytes=sum(p.stat().st_size for p in binaries))
        write_json(output, row)
        csv_row = {field: row[field] for field in FIELDS}
        csv_row["command"] = json.dumps(command)
        with (self.output / "observations.csv").open("a", newline="") as dest_csv:
            csv.DictWriter(dest_csv, fieldnames=FIELDS).writerow(csv_row)
        print(f"{label}: {row['wall_s']:.3f} s; {row['cpu_s']:.3f} CPU-s", flush=True)
        if row["returncode"] or result.returncode or row["timeout"] or not row["process_tree_drained"]:
            raise RuntimeError(f"build failed or process tree did not drain: {label}")
        if row["affinity"] != self.affinity[cpus]:
            raise ValueError("CPU affinity was not applied")
        if [p.name for p in binaries] != self.config["subjects"][case["subject"]]["expected_binaries"]:
            raise ValueError("unexpected test binary set")
        self.budget()
        return row

    def edit(self, case, iteration, body):
        spec = self.config["subjects"][case["subject"]]
        original = self.subjects[case["subject"]] / spec["edit_file"]
        text = original.read_text()
        if body:
            signature = spec["edit_signature"]
            if text.count(signature) != 1:
                raise ValueError("test-body signature changed")
            text = text.replace(signature, signature + f'\n\tt.Log("ddci-real-test-body-edit-{iteration}")\n', 1)
        else:
            text += f'\nconst ddCiBuildOnlyMarker = "build-only-{iteration}"\n'
        backing = self.output / "edits" / f"{case['id']}-{body}-{iteration}.go"
        backing.write_text(text)
        overlay = backing.with_suffix(".json")
        write_json(overlay, {"Replace": {str(original): str(backing)}})
        return ["-overlay=" + str(overlay)]

    def validate(self, case, cpus, caches):
        traces = []
        for variant in VARIANTS:
            self.measure(case, cpus, variant, "trace-prime", 0, caches[variant], validation=True)
            checks = [("trace-cached", []), ("trace-link", [f"-ldflags=-buildid=trace-{case['id']}-{cpus}"]),
                      ("trace-body", self.edit(case, 999, True))]
            for scenario, extra in checks:
                self.measure(case, cpus, variant, scenario, 0, caches[variant], extra + ["-x"], True)
                log = (self.output / "rows" / f"{case['id']}-cpu{cpus}-{scenario}-0-{variant}.log").read_text()
                tools = {name: [line for line in log.splitlines() if "-V=full" not in line and
                                re.search(r"/pkg/tool/[^/]+/" + name + r"(?: |$)", line)]
                         for name in ("compile", "link", "cover")}
                if scenario == "trace-cached" and (tools["compile"] or tools["link"]):
                    raise ValueError("unchanged build compiled or linked")
                if scenario == "trace-link" and len(tools["link"]) != len(self.config["subjects"][case["subject"]]["expected_binaries"]):
                    raise ValueError("forced link did not link every binary")
                if scenario == "trace-body" and (not tools["compile"] or not tools["link"]):
                    raise ValueError("reachable test edit did not compile and link")
                traces.append({"variant": variant, "scenario": scenario, "tools": tools})
            self.qualify(case, cpus, variant)
        write_json(self.output / f"{case['id']}-cpu{cpus}-traces.json", traces)
        write_json(self.output / "final-binaries.json", self.binaries)

    def qualify(self, case, cpus, variant):
        spec = self.config["subjects"][case["subject"]]
        for binary in sorted(self.destination(case, cpus, variant).glob("*.test")):
            symbols = self.setup([str(self.go), "tool", "nm", str(binary)], ROOT)
            sdk = SDK_MODULE + "/internal/"
            mini = POC_MODULE + "/internal/thirdparty/dd-trace-go/"
            suffix = "civisibility/integrations/gotesting.instrumentTestingMWithControl"
            if ((sdk + suffix in symbols) != (variant in ("orchestrion", "sdk")) or
                    (mini + suffix in symbols) != (variant == "mini")):
                raise ValueError("testing instrumentation hook mismatch")
            if case["subject"].startswith("testify"):
                suffix = "civisibility/integrations/gotesting.instrumentTestifySuiteRun"
                if ((sdk + suffix in symbols) != (variant in ("orchestrion", "sdk")) or
                        (mini + suffix in symbols) != (variant == "mini")):
                    raise ValueError("Testify instrumentation hook mismatch")
            sections = self.setup(["readelf", "-SW", str(binary)], ROOT)
            if re.search(r"\.(?:z)?debug_", sections):
                raise ValueError("DWARF is present despite -w")
            edited_binary = case["subject"] + ".test"
            if binary.name == edited_binary and b"ddci-real-test-body-edit-999" not in binary.read_bytes():
                raise ValueError("reachable test edit is absent from the output")
            self.binaries.append({"case": case["id"], "cpus": cpus, "variant": variant,
                                  "path": str(binary), "sha256": digest(binary), "bytes": binary.stat().st_size,
                                  "instrumentation_verified": True, "debug_sections_absent": True})

    def remove_cache(self, cache):
        if cache.parent != self.output / "caches" or cache.is_symlink():
            raise ValueError("refusing to remove a cache outside this run")
        shutil.rmtree(cache)

    def controls(self):
        if not self.args.control_repeats:
            return
        case = next((c for c in self.cases if c["id"] == "gin"), None)
        if case is None:
            raise ValueError("native link convergence control requires --case gin; use --control-repeats 0 for a subset")
        cpus = self.args.cpus[0]
        cache = self.output / "caches" / "native-control"
        cache.mkdir()
        self.measure(case, cpus, "native", "control-prime", 0, cache, validation=True)
        values, medians = [], []
        for i in range(self.args.control_repeats):
            row = self.measure(case, cpus, "native", "control-link", i, cache,
                               [f"-ldflags=-buildid=control-{i}"], True)
            values.append(row["wall_s"])
            if len(values) % 20 == 0:
                medians.append(statistics.median(values))
        stats = distribution(values)
        changes = [abs(100 * (b / a - 1)) for a, b in zip(medians, medians[1:])]
        width = 100 * (stats["bootstrap95_median"][1] - stats["bootstrap95_median"][0]) / stats["median"]
        passed = len(changes) >= 2 and all(x <= 5 for x in changes[-2:]) and width <= 20
        write_json(self.output / "convergence.json", {"wall_s": stats, "checkpoint_medians_s": medians,
                                                      "median_changes_pct": changes, "interval_width_pct": width,
                                                      "passed": passed, "protocol": "last two 20-run median changes <=5%; 95% median interval width <=20%"})
        self.remove_cache(cache)
        if not passed:
            raise ValueError("native control did not converge; all observations retained")

    def run(self):
        try:
            self.prepare()
            self.controls()
            for case in self.cases:
                for cpus in self.args.cpus:
                    self.check_frozen()
                    reps = self.manifest["repetitions"][case["series"]]
                    caches = {}
                    for i in range(reps["cold"]):
                        for variant in rotated(i + int(cpus == max(self.args.cpus))):
                            if variant in caches:
                                self.remove_cache(caches[variant])
                            cache = self.output / "caches" / f"{case['id']}-cpu{cpus}-{variant}-{i}"
                            cache.mkdir()
                            caches[variant] = cache
                            self.measure(case, cpus, variant, "cold", i, cache)
                    # The unused-constant diagnostic precedes real edits, matching the recorded protocol.
                    for scenario in ("cached", "warm-link", "edited", "test-body-edit"):
                        for i in range(reps[scenario]):
                            extra = []
                            if scenario == "warm-link":
                                extra = [f"-ldflags=-buildid=link-{case['id']}-{cpus}-{i}"]
                            elif scenario in ("edited", "test-body-edit"):
                                extra = self.edit(case, i, scenario == "test-body-edit")
                            for variant in rotated(i):
                                self.measure(case, cpus, variant, scenario, i, caches[variant], extra)
                    self.validate(case, cpus, caches)
                    self.check_frozen()
                    for cache in caches.values():
                        self.remove_cache(cache)
                    self.manifest.setdefault("verified_cells", []).append({"case": case["id"], "cpus": cpus})
                    write_json(self.output / "manifest.json", self.manifest)
            self.manifest.update(status="verified", completed_commands=self.count,
                                 qualified_binaries=len(self.binaries),
                                 raw_csv_sha256=digest(self.output / "observations.csv"),
                                 elapsed_seconds=time.monotonic() - self.started)
            write_json(self.output / "manifest.json", self.manifest)
            self.write_methodology()
            report_command(argparse.Namespace(input=self.output, output=None, check=False))
        except BaseException as error:
            with (self.output / "observations.csv").open(newline="") as source:
                completed = sum(1 for _ in csv.DictReader(source))
            self.manifest.update(status="partial", failure=str(error), completed_commands=completed,
                                 attempted_commands=self.count,
                                 elapsed_seconds=time.monotonic() - self.started)
            write_json(self.output / "manifest.json", self.manifest)
            raise

    def write_methodology(self):
        m = self.manifest
        lines = ["# Compile-only comparison: Native, Orchestrion, POC SDK and POC Mini", "",
                 f"POC `{m['source_head']}`; `{m['toolchain']}`; SDK `{m['sdk_version']}`; Orchestrion `{m['orchestrion_version']}`.", "",
                 "Values are medians in seconds. POC percentages show the signed change against total Orchestrion wall time first, then against Native; both use unrounded medians.", "",
                 "All variants compile with `go test -c -o <directory>/ -ldflags=-w ./...`. Test binaries are never run. The same prepared source and module graph is used for all four variants. Orchestrion loads only the pinned SDK's testing aspects.", "",
                 "Builds run serially in rotating order. Each cold run has an empty Go build cache and no existing output. Downloads are disabled during timing; modules and OS page cache stay warm. Affinity, `GOMAXPROCS` and `-p` match the selected CPU count; manifest.json records logical CPU IDs and physical core topology.", "",
                 "Unchanged output reuse, forced linking and reachable edits are checked with tool traces. Final binaries are checked for the expected testing/Testify hooks and absence of DWARF. CPU and memory include daemons and nested builds through exclusive cgroups. Wall time ends at the top-level command's exit; drain wait is recorded separately.", "",
                 f"{m['completed_commands']} completed build commands, {m['qualified_binaries']} qualified binaries. Native control repetitions: {self.args.control_repeats}. A skipped control in a smoke run provides no stability evidence.", "",
                 "[Every observation](observations.csv), [ranges and uncertainty](statistics.json), [inputs](manifest.json) and [binary qualification](final-binaries.json) remain alongside this report. No slow samples are discarded. Fixture results are not a general application performance claim.", ""]
        (self.output / "methodology.md").write_text("\n".join(lines))


def read_json_string(text):
    value = json.loads(text)
    if value.get("Error"):
        raise ValueError(value["Error"])
    return value


def rotated(index):
    offset = index % len(VARIANTS)
    return VARIANTS[offset:] + VARIANTS[:offset]


def positive(value):
    number = int(value)
    if number <= 0:
        raise argparse.ArgumentTypeError("must be positive")
    return number


def cpu_counts(value):
    try:
        values = [positive(v) for v in value.split(",")]
        if len(values) != len(set(values)):
            raise ValueError("duplicate CPU count")
        return values
    except (ValueError, argparse.ArgumentTypeError) as error:
        raise argparse.ArgumentTypeError(str(error)) from error


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "_measure":
        sys.exit(cgroup_measure(Path(sys.argv[2]), float(sys.argv[3]), sys.argv[4:]))
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("list", help="list subject/flag combinations")
    report = sub.add_parser("report", help="regenerate tables from retained CSV, without Go or network")
    report.add_argument("--input", type=Path, required=True)
    report.add_argument("--output", type=Path)
    report.add_argument("--check", action="store_true", help="verify generated files without writing")
    report.add_argument("--update-readme", action="store_true", help="also regenerate the marked README summary; input must be inside this repository")
    run = sub.add_parser("run", help="prepare targets and execute a bounded compile-only matrix")
    run.add_argument("--go", default="go")
    run.add_argument("--orchestrion", type=Path, required=True)
    run.add_argument("--output", type=Path, required=True)
    run.add_argument("--cpus", type=cpu_counts, default=[4, 32])
    run.add_argument("--case", action="append", help="repeat to select cases; default is the complete matrix")
    run.add_argument("--repeats", type=positive, help="override each scenario's recorded repetition count")
    run.add_argument("--control-repeats", type=int, default=80)
    run.add_argument("--download", action="store_true", help="allow module downloads during setup only")
    run.add_argument("--max-seconds", type=positive, required=True)
    run.add_argument("--max-gib", type=positive, required=True)
    run.add_argument("--max-commands", type=positive, default=7500)
    run.add_argument("--command-seconds", type=positive, default=600)
    args = parser.parse_args()
    try:
        if args.action == "list":
            for case in read_json(CONFIG)["cases"]:
                print(case["id"] + ": " + (" ".join(case["flags"]) or "no extra flags"))
        elif args.action == "report":
            report_command(args)
        else:
            if args.control_repeats < 0 or args.control_repeats > args.max_commands:
                parser.error("control repetitions must be between zero and --max-commands")
            Runner(args).run()
    except (ValueError, RuntimeError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f"benchmark: {error}\n")


if __name__ == "__main__":
    main()
