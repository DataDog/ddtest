# Benchmark data

Native, Orchestrion and POC Mini were compiled together from 2026-10-27, at
ddtest `282b22a15776c3dfb1073d2e0197c78445c00d6f`, the first revision after
the instrumentation moved into ddtest, where POC Mini runs as
`ddtest instrument`. The toolchain is `go version go1.27.1 linux/amd64` and
Orchestrion `v1.13.2-0.20260917114356-5c24783fcd76`: the same executables as
in [20261016-linux-go1.27.1](../20261016-linux-go1.27.1/README.md), whose
SHA-256 match its manifest.

[Comparison tables](../../benchmarks.md) cover build, runtime and memory.
[Collection and regeneration](../../build-benchmarks.md) describe the commands.

## What was measured

- [Build](build/README.md): the complete matrix, 24 subject/flag combinations
  at 4/32 CPUs, with `build_benchmark.py run --repeats 3 --download`: three
  repetitions of each scenario instead of the recorded 5/10/20/10/3, to
  shorten the collection to about 3.4 hours. 2,160 measured builds; all 324
  final binaries passed traces and binary qualification; the Native link
  control converged (80 repetitions).
- [Race confirmation](confirm-race/README.md): Gin and Testify External with
  `-race`, Native and POC Mini, with the recorded repetition counts, such as ten
  forced links instead of three.
- [Memory](memory.md): every build scenario.

Runtime and the local Agent control were not rerun. `runtime_benchmark.py` and
`refresh_benchmark_report.py` refresh only POC Mini against a dataset whose
inputs are commits of this repository, and 20261016-linux-go1.27.1 was recorded
in ddto's former repository. [benchmarks.md](../../benchmarks.md) therefore
keeps that dataset's runtime sections and takes its build and build-memory
tables from this one; [compose.py](compose.py) writes it with the renderers of
`scripts/build_benchmark.py` and `scripts/benchmark_report.py`.

Two earlier attempts of the full collection on 2026-10-27 were stopped and are
not part of this dataset: the first because the Native link control did not
converge, the second because another workload ran on the host.

## Files

This repository keeps the reports and the composition script. The raw data,
`manifest.json`, `observations.csv`, `statistics.json`, `final-binaries.json`,
`convergence.json` and the trace and module records of `build/` and
`confirm-race/`, is not in this repository; `archive.json` lists the SHA-256 of
every file of the dataset.
