#!/usr/bin/env python3
"""Regenerate build/runtime summaries and memory tables from an archived run.

No builds, tests or network requests are made. The archive records original
file hashes; generated summaries do not change the measured source revision.
See docs/build-benchmarks.md for collection and regeneration commands.
"""

import argparse
import csv
import gzip
import hashlib
import json
from pathlib import Path
import statistics

import build_benchmark as build

RUNTIME_VARIANTS = (*build.VARIANTS, "mini-deferred")
MIB = 1024 ** 2


def verify_archive(directory):
    archive = build.read_json(directory / "archive.json")
    for relative, record in archive["files"].items():
        path = directory / relative
        if build.digest(path) != record["sha256"]:
            raise ValueError(f"archive SHA256 mismatch: {relative}")
        if path.suffix == ".gz":
            digest = hashlib.sha256()
            with gzip.open(path, "rb") as source:
                for chunk in iter(lambda: source.read(1024 * 1024), b""):
                    digest.update(chunk)
            if digest.hexdigest() != record["source_sha256"]:
                raise ValueError(f"uncompressed SHA256 mismatch: {relative}")
    return archive


def metric(cell, variant, field):
    entry = cell[variant]
    if not entry.get("valid", True):
        return None
    return entry["wall_s"]["median"] if field == "wall_s" else entry["peak_bytes_median"]


def format_cell(cell, variant, field):
    value = metric(cell, variant, field)
    if value is None:
        entry = cell[variant]
        return f"FAIL {entry['failed']}/{entry['attempted']}" if entry["failed"] else "UNVERIFIED"
    text = f"{value / MIB:.1f} MiB" if field == "peak_bytes" else f"{value:.6f} s"
    if variant in ("sdk", "mini", "mini-deferred"):
        native = metric(cell, "native", field)
        orchestrion = metric(cell, "orchestrion", field)
        if orchestrion is None:
            text += " (vs Orchestrion unavailable"
            text += f"; {100 * (value / native - 1):+.1f}% vs Native)"
        else:
            text += f" ({100 * (value / orchestrion - 1):+.1f}%"
            text += f"; {100 * (value / native - 1):+.1f}%)"
    return text


def comparison_table(manifest, summary, field, scenario=None, cases=None):
    variants = build.VARIANTS if scenario else RUNTIME_VARIANTS
    lines = ["| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |" +
             (" Mini deferred |" if not scenario else ""),
             "| --- | --- | ---: | ---: | ---: | ---: | ---: |" +
             (" ---: |" if not scenario else "")]
    for case in cases or manifest["cases"]:
        flags = " ".join(case["flags"]) or "none"
        for cpus in manifest["cpus"]:
            key = f"{case['id']}/{cpus}" + (f"/{scenario}" if scenario else "")
            values = [format_cell(summary[key], variant, field) for variant in variants]
            lines.append(f"| {case['label']} | `{flags}` | {cpus} | " + " | ".join(values) + " |")
    return "\n".join(lines) + "\n"


def verify_runtime_memory(directory, summary):
    """Cross-check reported peaks against all successful measured CSV rows.

    A failure in any repetition, including warmup, invalidates that variant.
    Such a group must never acquire a median by filtering its failed samples.
    """
    grouped = {}
    with (directory / "runtime/observations.csv").open(newline="") as source:
        for row in csv.DictReader(source):
            key = (f"{row['case']}/{row['cpus']}", row["variant"])
            grouped.setdefault(key, []).append(row)
    for (key, variant), rows in grouped.items():
        entry = summary[key][variant]
        if entry["attempted"] != len(rows):
            raise ValueError(f"incomplete runtime group: {key}/{variant}")
        failed = any(int(row["returncode"]) or not build.boolean(row["validated_contract"]) for row in rows)
        if entry["valid"] == failed:
            raise ValueError(f"runtime validation mismatch: {key}/{variant}")
        if entry["valid"]:
            peaks = [int(row["peak_bytes"]) for row in rows if not build.boolean(row["warmup"])]
            if len(peaks) != entry["valid_measured"] or statistics.median(peaks) != entry["peak_bytes_median"]:
                raise ValueError(f"runtime memory mismatch: {key}/{variant}")
    expected = {(key, variant) for key, cell in summary.items() for variant in cell}
    if set(grouped) != expected:
        raise ValueError("missing runtime CSV groups")


def agent_summary(directory, cpus):
    with gzip.open(directory / "agent/observations.json.gz", "rt") as source:
        rows = json.load(source)
    result = {}
    for count in cpus:
        cell = result[f"gin/{count}"] = {}
        for variant in build.VARIANTS:
            group = [r for r in rows if r["cpus"] == count and r["variant"] == variant]
            if len(group) != 4 or any(r["returncode"] or not r["execution_and_event_counts_verified"] for r in group):
                raise ValueError(f"incomplete or failed Agent group: {count}/{variant}")
            measured = [r for r in group if not r["warmup"]]
            if len(measured) != 3:
                raise ValueError("Agent groups require three measured runs and one warmup")
            cell[variant] = {"wall_s": {"median": statistics.median(r["wall_s"] for r in measured)},
                             "peak_bytes_median": statistics.median(r["peak_bytes"] for r in measured)}
    return result


def continuous_parity(directory, cpus):
    lines = ["| CPUs | Continuous block | SDK | Mini | Mini vs SDK |",
             "| ---: | --- | ---: | ---: | ---: |"]
    for count in cpus:
        for filename, label in (("parity", "testing (65 cases)"), ("parity-deferred", "deferred (17 cases)")):
            rounds = [build.read_json(p) for p in sorted((directory / f"parity/cpu{count}").glob(f"round*/{filename}.json"))]
            orders = [r["execution_order"] for r in rounds]
            if len(rounds) != 6 or orders.count(["sdk", "mini"]) != 3 or orders.count(["mini", "sdk"]) != 3:
                raise ValueError("parity blocks require six balanced rounds")
            sdk = statistics.median(r["execution_block"]["sdk_wall_ns"] / 1e9 for r in rounds)
            mini = statistics.median(r["execution_block"]["mini_wall_ns"] / 1e9 for r in rounds)
            lines.append(f"| {count} | {label} | {sdk:.6f} s | {mini:.6f} s | {100 * (mini / sdk - 1):+.1f}% |")
    return "\n".join(lines) + "\n"


MEMORY_DESCRIPTION = """Each cell is the median of the per-run `memory.peak` values, in MiB
(1 MiB = 1,048,576 bytes). The cgroup includes all build or test processes,
detached daemons, nested builds and the measurement helper. Charged file-cache
pages and kernel memory are included. This is neither a Go heap measurement
nor the sum of independently observed process RSS peaks. Runtime receivers run
outside the measured cgroup. Warmups and build qualification commands are excluded.

The percentages compare the same memory metric against Orchestrion first and
Native second. Failed variants have no comparative median; their individual
peaks remain in the raw records.
"""


def render(directory):
    archive = verify_archive(directory)
    manifest = build.read_json(directory / "build/manifest.json")
    build_stats = build.read_json(directory / "build/statistics.json")
    runtime = build.read_json(directory / "runtime/statistics.json")
    if archive["source_head"] != manifest["source_head"] or build_stats["source_head"] != manifest["source_head"]:
        raise ValueError("mixed source revisions in benchmark archive")
    verify_runtime_memory(directory, runtime)
    agents = agent_summary(directory, manifest["cpus"])
    base = [case for case in manifest["cases"] if not case["flags"]]
    link = directory.relative_to(build.ROOT / "docs").as_posix()
    sections = [f"""<!-- Generated by scripts/benchmark_report.py; edit the renderer to change this report. -->
# Build, runtime and memory comparisons

This is the 2026-10-04/05 Linux run, measured at POC commit
`{manifest['source_head']}` with `{manifest['toolchain']}`.
The SDK is `{manifest['sdk_version']}` (commit `{manifest['sdk_commit']}`);
Orchestrion is `{manifest['orchestrion_version']}`.
The host is an AMD Ryzen 9 5950X: four CPUs bind four physical cores;
32 CPUs use all hardware threads of its 16 cores.

The run contains {build_stats['measured_observations']:,} comparative builds
({build_stats['completed_commands']:,} commands including controls and qualification),
1,200 measured runtime groups after 240 warmups, and 115 parity cases repeated
six times at each CPU count. All 48 build cells are qualified. Runtime compatibility
passes in 44 of 48 cells; four Gin race combinations have real failures.

All time cells are medians in **seconds**. POC percentages show the signed change
against total Orchestrion time first, then Native. `(-50%; +20%)` means half
Orchestrion time and 20% more than Native. Positive first values remain positive.
The excerpts below use no extra flags; the linked reports include every Testify,
coverage and race combination, all observations, ranges and median uncertainty.
No slow observations were removed.

| Report | Contents |
| --- | --- |
| [All build comparisons]({link}/build/README.md) | Five scenarios, 24 configurations, 4/32 CPUs; [each command]({link}/build/observations.csv) and [statistics]({link}/build/statistics.json) |
| [All runtime comparisons]({link}/runtime/README.md) | Default and deferred Mini, failed combinations, CPU, memory, ranges and event delivery; [each run]({link}/runtime/observations.csv) |
| [All memory comparisons]({link}/memory.md) | Build peaks in all five scenarios, runtime and the Agent control |
| [115-case parity comparison]({link}/parity/README.md) | Per-case times, matching session/module/suite/test/span counts and six rounds per CPU count |
| [Agent delivery control]({link}/agent/README.md) | Gin with local EVP delivery and CI telemetry enabled |
| [Data and provenance]({link}/README.md) | Original and validated compressed records, input hashes and collection limits |

## Compilation

Commands use `go test -c -o <directory>/ -ldflags=-w ./...`; no test executable
runs during compilation timing. Each variant uses the same sources and module
resolution graph. Orchestrion loads only the SDK's testing aspects.
Cold runs use an empty Go build cache and removed output; downloaded modules and
the OS page cache stay warm. Builds run serially with rotating variant order.
Affinity, `GOMAXPROCS` and `-p` match the CPU column.

Plain Gin/Chi use 5/10/20/10/3 repetitions for cold/cache/link/body-edit/constant-edit.
The other configurations use 3/10/10/5/3. The 160-run Native link control passed
the recorded convergence thresholds. It checks that control's median, rather
than the stability of every matrix cell. Both control datasets are retained.
"""]
    sections.append("\n".join(build.table_lines(manifest, build_stats["statistics"], cases=base)))
    sections.append(f"""The unused-constant edit is a diagnostic: compiled code can remain reusable.
Use the reachable test-body edit to assess editing and recompiling a test.

## Cold compilation: memory

{MEMORY_DESCRIPTION}
""" + comparison_table(manifest, build_stats["statistics"], "peak_bytes", "cold", base))
    sections.append("""## Runtime of the prebuilt test binaries

Each runtime group executes the original binaries saved before any source edit.
The timer includes concurrent package startup, settings, tests and final delivery.
Compilation, CLI preparation, receiver startup and offline decoding are excluded.
Agentless gzip/MessagePack requests go to a successful loopback receiver;
the primary runtime matrix disables telemetry. Mini deferred uses the same binary.
Each cell uses five measured runs after one warmup. Test names, status and
multiplicity are checked against Native, and CI inventories against the SDK.

""" + comparison_table(manifest, runtime, "wall_s", cases=base))
    sections.append("## Runtime: memory\n\n" + comparison_table(manifest, runtime, "peak_bytes", cases=base))
    sections.append(f"""### Runtime limits and failures

Gin `-race` and `-race -coverpkg=./... -covermode=atomic` fail at both CPU counts.
`binding.TestMappingTime` writes `time.Local` while SDK background work calls
`time.Now`. The SDK-derived asynchronous coverage processor also exposes the
race in Mini with atomic coverage. Failed or unverified variants have no valid
comparative median. No test was removed and no race detector was disabled.

Passing plain-race Mini runs are checked against identical Native test results
and the successful non-race SDK CI inventory; this fallback is restricted to
cases without coverage. Eleven otherwise passing coverage groups lack a
successful SDK coverage oracle and remain unverified.

Go's default `-covermode=set` produces aggregate Go coverage. The pinned SDK
and Mini support per-test uploads only in count/atomic modes. Runtime eligibility
uses the validated classifications in the archive; the raw records retain every
observation and classification for audit.
Gin atomic coverage takes about 6.5–6.9 seconds in the instrumented variants;
these build improvements do not eliminate that runtime cost. Chi's full suite
takes about 26 seconds even in Native, so its overall runtime hides small SDK costs.

## Repeated CI parity

All 1,380 scenario comparisons passed, plus the manual, additional-span,
multi-package, fuzz, telemetry, Unix Agent and goleak fixtures in every round.
The 65-case testing and 17-case deferred blocks each use a continuous clock,
including receiver setup and child shutdown but excluding compilation and
comparison. Three rounds run SDK first and three run Mini first at each CPU count.
The 26 Testify and seven deferred Testify cases use paired SDK-first order;
their race-runtime shutdown delays account for about 33 seconds of the full
115-case child-clock sum. That sum is not a continuous wall-time measurement.
The [per-case report]({link}/parity/README.md) keeps the event counts and ranges.
We did not collect separate SDK/Mini memory peaks for those individual cases;
the runtime memory matrix above measures complete project test groups.

""" + continuous_parity(directory, manifest["cpus"]))
    sections.append(f"""## Reproduce and interpret

[Collection and regeneration commands](build-benchmarks.md) describe the build
runner and offline report scripts. The dataset contains raw observations,
validation outcomes and input hashes. SDK source records, adaptations and
compatibility tests have their own maintenance procedure.

These measurements cover the recorded Linux/amd64 inputs and a loopback protocol
receiver. They do not establish live Datadog intake acceptance or timings on
macOS/Windows. Read the ranges and bootstrap intervals before treating a small
difference as a stable gain. The [CI parity contract](ci-parity.md) describes the
feature checks and their remaining boundaries.
""")
    memory = ["# Aggregate memory comparisons\n", MEMORY_DESCRIPTION]
    for scenario, title in build.SCENARIOS.items():
        memory.append(f"## {title}\n\n" + comparison_table(manifest, build_stats["statistics"], "peak_bytes", scenario))
    memory.append("## Runtime of original test executables\n\n" + comparison_table(manifest, runtime, "peak_bytes"))
    agent_manifest = {**manifest, "cases": [{"id": "gin", "label": "Gin", "flags": []}]}
    # The Agent control has only the four standard variants, so use the build table shape.
    agent_stats = {key + "/agent": cell for key, cell in agents.items()}
    memory.append("## Gin: local Agent delivery with telemetry\n\nThree measured runs after one warmup.\n\n" +
                  comparison_table(agent_manifest, agent_stats, "peak_bytes", "agent"))
    return {build.ROOT / "docs/benchmarks.md": "\n\n".join(s.rstrip() for s in sections) + "\n",
            directory / "memory.md": "\n\n".join(s.rstrip() for s in memory) + "\n"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True, help="archived build/runtime/parity dataset inside docs")
    parser.add_argument("--check", action="store_true", help="verify hashes and generated reports without writing")
    args = parser.parse_args()
    for path, content in render(args.input.resolve()).items():
        if args.check:
            if path.read_text() != content:
                raise ValueError(f"generated report is stale: {path}")
        else:
            path.write_text(content)
    print("Archive hashes, runtime memory medians and comparison documents verified." if args.check else
          "Generated docs/benchmarks.md and the complete memory comparison.")


if __name__ == "__main__":
    main()
