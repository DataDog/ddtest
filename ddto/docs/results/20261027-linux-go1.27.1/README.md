# Benchmark data

Native, Orchestrion and `ddtest instrument` were compiled together from
2026-10-27 and run together on 2026-10-28, at ddtest
`282b22a15776c3dfb1073d2e0197c78445c00d6f`, after the instrumentation moved
into ddtest as `ddtest instrument`. The toolchain is `go version go1.27.1 linux/amd64` and
Orchestrion `v1.13.2-0.20260917114356-5c24783fcd76`: the same executables as
in [20261016-linux-go1.27.1](../20261016-linux-go1.27.1/README.md), whose
SHA-256 match its manifest.

[Comparison tables](../../benchmarks.md) cover build, runtime and memory.
[Collection and regeneration](../../build-benchmarks.md) describe the commands.

## What was measured

- [Build](build/README.md): the complete matrix, 24 subject/flag combinations
  at 4/32 CPUs, with `build_benchmark.py run --repeats 3 --download`: three
  repetitions of each scenario instead of the recorded counts (5/10/20/10/3
  for Gin and Chi without flags, 3/10/10/5/3 for the other cases), to shorten
  the collection to about 3.4 hours. 2,160 measured builds; all 324
  final binaries passed traces and binary qualification; the Native link
  control converged (80 repetitions).
- [Race confirmation](confirm-race/README.md): Gin and Testify External with
  `-race`, Native and `ddtest instrument`, with the recorded repetition counts, such as ten
  forced links instead of three.
- [Runtime](runtime/README.md): the original test binaries of every
  combination at 4/32 CPUs, Native, Orchestrion and `ddtest instrument` with
  normal and deferred delivery, in rotating order, with
  `runtime_benchmark.py --repeats 3`: three measured runs after one warmup
  instead of five. Two collections on 2026-10-28 make it up,
  each case from one of them: the first stopped when `go tool objdump`, which
  checks the native Examples, stalled on the Go build cache, and the second
  measured the remaining cases ([collections](runtime/README.md#collections)).
- [Local Agent control](agent/README.md): Gin through the local EVP and
  telemetry proxy, Native, Orchestrion and Mini, three measured runs after one
  warmup.
- [Memory](memory.md): every build scenario, the runtime and the Agent control.

The runtime and the Agent control validate Orchestrion and Mini against the SDK
event inventories archived in
[20261016-linux-go1.27.1](../20261016-linux-go1.27.1/README.md), and test
names, statuses and counts against Native's.

Two earlier attempts of the full build collection on 2026-10-27 were stopped and are
not part of this dataset: the first because the Native link control did not
converge, the second because another workload ran on the host.

## Files

The results are in the reports: [benchmarks.md](../../benchmarks.md), the
[build](build/README.md), the [race confirmation](confirm-race/README.md), the
[runtime](runtime/README.md), the [Agent control](agent/README.md) and
[memory](memory.md). The raw data that they come from is in [raw.zip](raw.zip),
to verify or regenerate them: `manifest.json`, `observations.csv`,
`statistics.json`, `final-binaries.json`, `convergence.json` and the trace and
module records of `build/` and `confirm-race/`; the manifests and observations
of both runtime collections in `runtime/collection-1` and
`runtime/collection-2`, and the `observations.csv` and `statistics.json` that
`compose.py` derives from them; and the Agent control's `manifest.json`,
`observations.json` and `observations.json.gz`. The reports link to those files
as extracted. `archive.json` lists the SHA-256 of every file. From `ddto/`:

```sh
(cd docs/results/20261027-linux-go1.27.1 && unzip -o raw.zip)
python3 scripts/build_benchmark.py report --input docs/results/20261027-linux-go1.27.1/build --update-readme --check
python3 scripts/build_benchmark.py report --input docs/results/20261027-linux-go1.27.1/confirm-race --check
python3 docs/results/20261027-linux-go1.27.1/compose.py   # rewrites benchmarks.md and the runtime, Agent and memory reports unchanged
```

Remove the extracted files afterwards; only `raw.zip` is kept.
