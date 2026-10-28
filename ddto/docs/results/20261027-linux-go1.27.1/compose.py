#!/usr/bin/env python3
"""Compose docs/benchmarks.md and this dataset's memory.md.

The build of 2026-10-27 measured Native, Orchestrion and POC Mini, through
ddtest instrument, at one ddtest revision. The runtime scripts refresh only Mini
against a dataset whose inputs are commits of this repository, and the last
runtime data, 20261016-linux-go1.27.1, was recorded in ddto's former
repository. This page therefore takes its build and build-memory tables from
this dataset, through the renderers of scripts/build_benchmark.py and
scripts/benchmark_report.py, and keeps the runtime sections that
scripts/benchmark_report.py rendered from 20261016-linux-go1.27.1, unchanged.

Run from ddto/ with this dataset's raw build data in build/ (see README.md):

    python3 docs/results/20261027-linux-go1.27.1/compose.py
"""
from pathlib import Path
import sys

DATASET = Path(__file__).resolve().parent
ROOT = DATASET.parents[2]
sys.path.insert(0, str(ROOT / "scripts"))

import benchmark_report as report  # noqa: E402
import build_benchmark as build  # noqa: E402

RUNTIME_START = "## Runtime of the original test binaries"
RUNTIME_END = "## Read and repeat the measurements"
RETAINED = "results/20261016-linux-go1.27.1"


def retained_runtime(page):
    """The runtime sections of the current page, rendered from 20261016."""
    start, end = page.index(RUNTIME_START), page.index(RUNTIME_END)
    sections = page[start:end].rstrip()
    if RETAINED in sections or "Five measured runs follow one warmup" not in sections:
        raise ValueError("unexpected runtime sections")
    return sections


def compose():
    manifest = build.read_json(DATASET / "build/manifest.json")
    stats = build.read_json(DATASET / "build/statistics.json")
    confirm = build.read_json(DATASET / "confirm-race/statistics.json")
    base = [case for case in manifest["cases"] if not case["flags"]]
    link = DATASET.relative_to(ROOT / "docs").as_posix()
    page_path = ROOT / "docs/benchmarks.md"
    runtime = retained_runtime(page_path.read_text())
    started = manifest["started_at"][:10]
    sections = [f"""<!-- Composed by {link}/compose.py from scripts/build_benchmark.py's and scripts/benchmark_report.py's renderers. -->
# Build, runtime and memory comparisons

Build: Native, Orchestrion and POC Mini were measured together from {started},
at ddtest `{manifest['source_head']}`, where POC Mini runs as `ddtest instrument`.
Each scenario ran 3 times instead of the recorded 5/10/20/10/3 (cold, cached,
forced link, body edit, constant edit), to shorten the collection; a
[confirmation run]({link}/confirm-race/README.md) repeated the Gin and Testify
race cases, Native and POC Mini, {confirm['measured_observations']:,} builds,
with the recorded counts.
Runtime: retained from [20261016-linux-go1.27.1]({RETAINED}/README.md), where POC
Mini was measured on 2026-10-16 in ddto's former repository and Native and
Orchestrion on 2026-10-04; it was not rerun.
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
| [Race confirmation]({link}/confirm-race/README.md) | Gin and Testify race cases with the recorded repetitions |
| [Runtime]({RETAINED}/runtime/README.md) | Normal/deferred Mini, each run, CPU, memory and event validation (2026-10-16) |
| [Memory]({link}/memory.md) | All build cells; [runtime and the local Agent control]({RETAINED}/memory.md) (2026-10-16) |
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
                 runtime,
                 f"""{RUNTIME_END}

[Collection and regeneration](build-benchmarks.md) explains how to run the
benchmarks. The [runtime report]({RETAINED}/runtime/README.md) lists
failed or unverified groups and preserves their observed durations. Those groups
have no comparative median in the main tables. Coverage in Go's default set
mode is aggregate only; per-test uploads use count or atomic mode.

These are Linux/amd64 measurements with a loopback receiver. Live intake and
other operating systems are covered separately by [validation](validation.md).
The archived runtime and parity measurements keep their original revisions and
dates; they were not repeated by this build collection.
"""]
    memory = ["# Aggregate memory comparisons\n", report.MEMORY_DESCRIPTION.rstrip() +
              f"\n\nRuntime and the local Agent control: [20261016-linux-go1.27.1](../20261016-linux-go1.27.1/memory.md).\n"]
    for scenario, title in build.SCENARIOS.items():
        memory.append(f"## {title}\n\n" + report.comparison_table(manifest, stats["statistics"], "peak_bytes", scenario))
    return {page_path: "\n\n".join(part.rstrip() for part in sections) + "\n",
            DATASET / "memory.md": "\n\n".join(part.rstrip() for part in memory) + "\n"}


if __name__ == "__main__":
    for path, content in compose().items():
        path.write_text(content)
        print(f"wrote {path.relative_to(ROOT)}")
