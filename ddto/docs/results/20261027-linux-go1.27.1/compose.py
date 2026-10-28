#!/usr/bin/env python3
"""Compose docs/benchmarks.md and this dataset's runtime, Agent and memory reports.

Native, Orchestrion and POC Mini, through ddtest instrument, were measured
together from 2026-10-27 at one ddtest revision: the build with
build_benchmark.py, the runtime and the local Agent control with
runtime_benchmark.py. benchmark_report.py renders a dataset whose variants
were measured at different dates, or a complete one with fixed historical
text, so this script writes the pages with its table renderers, and those of
build_benchmark.py and refresh_benchmark_report.py, from this dataset's raw
data. No number is typed by hand.

Run from ddto/ with this dataset's raw data extracted (see README.md):

    python3 docs/results/20261027-linux-go1.27.1/compose.py
"""
import gzip
import json
from pathlib import Path
import sys

DATASET = Path(__file__).resolve().parent
ROOT = DATASET.parents[2]
sys.path.insert(0, str(ROOT / "scripts"))

import benchmark_report as report  # noqa: E402
import build_benchmark as build  # noqa: E402
import refresh_benchmark_report as refresh  # noqa: E402


def runtime_collections(manifest):
    """The runtime records of both collections, each case from one of them.

    The first collection stopped at chi--cover-client with 32 CPUs: validating
    that group, go tool objdump stalled on a stat in the Go build cache. The
    second measured the remaining cases, chi--cover-client again, with the same
    revision, repetitions and variants."""
    collections = []
    for name in ("collection-1", "collection-2"):
        collection = build.read_json(DATASET / "runtime" / name / "manifest.json")
        records = build.read_json(DATASET / "runtime" / name / "observations.json")
        if collection["source_head"] != manifest["source_head"]:
            raise ValueError(f"the runtime {name} is not a run of the build's revision")
        collections.append((collection, records))
    (first, early), (second, late) = collections
    if "'tool', 'objdump'" not in first.get("failure", "") or "failure" in second or second["status"] not in ("verified", "partial"):
        raise ValueError("the runtime collections did not end as recorded")
    for key in ("toolchain", "baseline_sha256", "repetitions", "warmups", "variants"):
        if first[key] != second[key]:
            raise ValueError(f"the runtime collections differ in {key}")
    # The first covers the cases up to the one it stopped in, in the matrix's
    # order; the second, that case and the ones after it.
    order = [case["id"] for case in manifest["cases"]]
    remeasured = {r["case"] for r in late}
    stopped = min(order.index(case) for case in remeasured)
    if {r["case"] for r in early} != set(order[:stopped + 1]) or remeasured != set(order[stopped:]):
        raise ValueError("the runtime collections do not meet where the first stopped")
    records = [r for r in early if r["case"] not in remeasured] + late
    return first, second, records


def runtime_summary(manifest):
    """Statistics of the runtime collections, written beside their records for
    benchmark_report's memory cross-check."""
    first, second, records = runtime_collections(manifest)
    summary = refresh.runtime_statistics(records, manifest, first["repetitions"], tuple(first["variants"]))
    refresh.runtime_csv(DATASET / "runtime/observations.csv", records)
    build.write_json(DATASET / "runtime/statistics.json", summary)
    report.verify_runtime_memory(DATASET, summary)
    return first, second, summary


def agent_statistics(manifest):
    collection = build.read_json(DATASET / "agent/manifest.json")
    if collection["source_head"] != manifest["source_head"] or collection["status"] != "verified":
        raise ValueError("the Agent collection is not a verified run of the build's revision")
    records = build.read_json(DATASET / "agent/observations.json")
    with gzip.GzipFile(DATASET / "agent/observations.json.gz", "wb", mtime=0) as target:
        target.write(json.dumps(records, separators=(",", ":")).encode())
    return collection, {key + "/agent": cell for key, cell in report.agent_summary(DATASET, manifest["cpus"]).items()}


def runs(count):
    return refresh.COUNTS.get(count, str(count))


def compose():
    manifest = build.read_json(DATASET / "build/manifest.json")
    stats = build.read_json(DATASET / "build/statistics.json")
    confirm = build.read_json(DATASET / "confirm-race/statistics.json")
    runtime, second, summary = runtime_summary(manifest)
    agent, agent_stats = agent_statistics(manifest)
    base = [case for case in manifest["cases"] if not case["flags"]]
    agent_manifest = {**manifest, "cases": [{"id": "gin", "label": "Gin", "flags": []}]}
    link = DATASET.relative_to(ROOT / "docs").as_posix()
    started = manifest["started_at"][:10]
    refresh.runtime_readme(DATASET, manifest, summary, runtime["repetitions"], retained=False)
    late = sorted({r["case"] for r in build.read_json(DATASET / "runtime/collection-2/observations.json")})
    with (DATASET / "runtime/README.md").open("a") as target:
        target.write("\n## Collections\n\n"
                     f"Two collections from {runtime['started_at'][:10]} make up these results, both from ddtest "
                     f"`{manifest['source_head']}`. The first stopped at chi--cover-client with 32 CPUs: validating that "
                     "group, `go tool objdump` stalled on a stat in the Go build cache, and that group's run, "
                     f"measured but not validated, was not recorded ({runtime['commands']} commands, "
                     f"{len(build.read_json(DATASET / 'runtime/collection-1/observations.json'))} observations). The second, "
                     "with the same toolchain, binaries' revision and SDK references, and scripts that differ only by "
                     f"a retry of stalled `go tool` calls, measured chi--cover-client again and the cases after it: "
                     f"{', '.join(late)}. Each case comes from one "
                     "collection; `runtime/collection-1` and `runtime/collection-2` in [raw.zip](../raw.zip) keep "
                     "both, including the first's chi--cover-client groups, which are not used.\n")
    (DATASET / "agent/README.md").write_text(
        "# Gin with local Agent delivery and telemetry\n\n"
        f"{runs(agent['repetitions'])} measured runs follow one warmup at each CPU count, "
        "for Native, Orchestrion and POC Mini together. Real CI events and telemetry are sent through "
        "the local EVP/telemetry proxy.\n\n"
        "Orchestrion's percentage compares the same metric against Native. Mini's percentages "
        "compare Orchestrion first, then Native.\n\n"
        + report.comparison_table(agent_manifest, agent_stats, "wall_s", "agent")
        + "\n## Memory\n\n" + report.comparison_table(agent_manifest, agent_stats, "peak_bytes", "agent"))
    sections = [f"""<!-- Composed by {link}/compose.py from scripts/build_benchmark.py's, scripts/refresh_benchmark_report.py's and scripts/benchmark_report.py's renderers. -->
# Build, runtime and memory comparisons

Native, Orchestrion and POC Mini were measured together from {started}, at
ddtest `{manifest['source_head']}`, where POC Mini runs as `ddtest instrument`:
the build, the runtime and the local Agent control. Each build scenario ran 3
times instead of the recorded counts (cold, cached, forced link, body edit,
constant edit: 5/10/20/10/3 for Gin and Chi without flags, 3/10/10/5/3 for the
other cases), and each runtime group {runtime['repetitions']} times instead of
5, to shorten the collection; a
[confirmation run]({link}/confirm-race/README.md) repeated the Gin and Testify
race builds, Native and POC Mini, {confirm['measured_observations']:,} builds,
with the recorded counts.
All variants use `{manifest['toolchain']}` and the recorded subject versions,
flags and CPU affinity. The host is an AMD Ryzen 9 5950X: four CPUs use four
physical cores; 32 use all hardware threads.

The build comparison contains {stats['measured_observations']:,} measured builds,
24 subject/flag combinations and 4/32 CPUs. All {manifest['qualified_binaries']}
final binaries passed traces and binary qualification, and the Native link
control converged before the matrix.

All time cells are medians in seconds. Orchestrion's percentage compares its
time against Native. Mini's percentages compare Orchestrion first, then Native.
For example, `(-50%; +20%)` means half Orchestrion's time and 20% more than Native's. Memory uses aggregate cgroup MiB.
The records keep CPU time, ranges, repetitions, failures and median uncertainty.
No slow observations are removed. With three repetitions, a small difference
in wall time alone does not establish a stable gain.

| Report | Contents |
| --- | --- |
| [Build]({link}/build/README.md) | Every flag combination in cold, cached, forced-link and both edit scenarios |
| [Race confirmation]({link}/confirm-race/README.md) | Gin and Testify race builds with the recorded repetitions |
| [Runtime]({link}/runtime/README.md) | Native, Orchestrion, normal/deferred Mini, each run, CPU, memory and event validation |
| [Agent control]({link}/agent/README.md) | Gin through the local EVP and telemetry proxy |
| [Memory]({link}/memory.md) | All build and runtime cells and the local Agent control |
| [Data and provenance]({link}/README.md) | Dates, revisions, repetitions and how this page is composed |

## Compilation

Commands compile with `go test -c -o <directory>/ -ldflags=-w ./...`.
Cold runs start with an empty Go build cache and removed output; modules and
the OS page cache remain warm. The other scenarios reuse output, force a link,
edit a reachable test body or edit an unused constant. The unused-constant edit
is a diagnostic; it can leave compiled code reusable.
"""]
    sections.append("\n".join(build.table_lines(manifest, stats["statistics"], cases=base)))
    sections += ["## Cold compilation: memory\n\n" + report.MEMORY_DESCRIPTION +
                 report.comparison_table(manifest, stats["statistics"], "peak_bytes", "cold", base),
                 f"""## Runtime of the original test binaries

{runs(runtime['repetitions'])} measured runs follow one warmup, for every variant in rotating
order. The clock includes process startup, concurrent package tests, settings
and final delivery to loopback HTTP. Compilation, receiver startup and decoding
are excluded. Telemetry is disabled in this matrix and enabled in the separate
local Agent control. Mini deferred uses the same executable as normal Mini. The
runtime comes from two collections, each case from one of them; the
[runtime report]({link}/runtime/README.md#collections) explains why.

Test names, statuses and multiplicities match the archived Native inventory.
The archived SDK supplies CI event expectations for Orchestrion and Mini. New
native Examples are identified from the executable's function/source table and
checked explicitly. A failed or unavailable reference remains visible in the
validation results.

""" + report.comparison_table(manifest, summary, "wall_s", cases=base),
                 "## Runtime: memory\n\n" + report.comparison_table(manifest, summary, "peak_bytes", cases=base),
                 """## Read and repeat the measurements

[Collection and regeneration](build-benchmarks.md) explains how to run the
benchmarks. The [runtime report](""" + link + """/runtime/README.md) lists
failed or unverified groups and preserves their observed durations. Those groups
have no comparative median in the main tables. Coverage in Go's default set
mode is aggregate only; per-test uploads use count or atomic mode.

These are Linux/amd64 measurements with a loopback receiver. Live intake and
other operating systems are covered separately by [validation](validation.md).
"""]
    memory = ["# Aggregate memory comparisons\n", report.MEMORY_DESCRIPTION]
    for scenario, title in build.SCENARIOS.items():
        memory.append(f"## {title}\n\n" + report.comparison_table(manifest, stats["statistics"], "peak_bytes", scenario))
    memory.append("## Runtime\n\n" + report.comparison_table(manifest, summary, "peak_bytes"))
    memory.append(f"## Gin: local Agent delivery and telemetry\n\n{runs(agent['repetitions'])} measured runs follow one warmup.\n\n" +
                  report.comparison_table(agent_manifest, agent_stats, "peak_bytes", "agent"))
    return {ROOT / "docs/benchmarks.md": "\n\n".join(part.rstrip() for part in sections) + "\n",
            DATASET / "memory.md": "\n\n".join(part.rstrip() for part in memory) + "\n"}


if __name__ == "__main__":
    for path, content in compose().items():
        path.write_text(content)
        print(f"wrote {path.relative_to(ROOT)}")
