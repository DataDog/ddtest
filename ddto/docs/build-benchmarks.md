# Run and regenerate the benchmarks

The comparison uses Native, Orchestrion and `ddtest instrument`. POC SDK is no
longer a benchmark variant. Runtime runs `ddtest instrument` with normal and
deferred delivery; memory and CPU are recorded for every measured command.

The [comparison tables](benchmarks.md) link to the complete matrix. They come
from the latest data, [20261027-linux-go1.27.1](results/20261027-linux-go1.27.1/README.md):
build, runtime and the local Agent control of every variant, recorded here
with `--repeats 3`, as in [Record every variant](#record-every-variant). Its
[compose.py](results/20261027-linux-go1.27.1/compose.py) writes
[benchmarks.md](benchmarks.md) from the raw data in its `raw.zip`. A new
dataset replaces it: remove the old directory and update the links that point
to it.

The runtime benchmark validates Orchestrion's and `ddtest instrument`'s CI
events against the SDK's event inventories in
[20261016-linux-go1.27.1](https://github.com/tonyredondo/dd-ci-testing-poc/tree/8cc70ca41bdf7f2f5ed276a691ea582f6f15250e/docs/results/20261016-linux-go1.27.1),
a dataset of ddto's original repository. Fetch it, and pass its directory as
`--baseline`:

```sh
git clone --filter=blob:none https://github.com/tonyredondo/dd-ci-testing-poc.git /var/tmp/dd-ci-testing-poc
git -C /var/tmp/dd-ci-testing-poc checkout 8cc70ca41bdf7f2f5ed276a691ea582f6f15250e
SDK_BASELINE=/var/tmp/dd-ci-testing-poc/docs/results/20261016-linux-go1.27.1
```

## Record every variant

Without `--variant` and `--baseline`, the build benchmark measures Native,
Orchestrion and `ddtest instrument` together, and the runtime benchmark then runs each
variant's binaries, with deferred delivery too, in rotating order. `--repeats 3` runs
three repetitions of every build scenario and three measured runtime runs after
one warmup, instead of the recorded counts; the Agent control always runs three.

```sh
BENCH_RUN=/var/tmp/ddtest-benchmark-new
GO_BENCH=/path/to/go1.27.1/bin/go
ORCHESTRION_BENCH=/path/to/orchestrion

python3 scripts/build_benchmark.py run --repeats 3 --download \
  --go "$GO_BENCH" --orchestrion "$ORCHESTRION_BENCH" \
  --output "$BENCH_RUN/build" --max-seconds 28800 --max-gib 24

python3 scripts/runtime_benchmark.py --repeats 3 \
  --build "$BENCH_RUN/build" --baseline "$SDK_BASELINE" \
  --output "$BENCH_RUN/runtime" --max-seconds 14400 --max-gib 12

python3 scripts/runtime_benchmark.py --agent \
  --build "$BENCH_RUN/build" --baseline "$SDK_BASELINE" \
  --output "$BENCH_RUN/agent" --max-seconds 3600 --max-gib 4
```

`--baseline` gives the runtime benchmark those SDK event inventories. The
build matrix took about 3.4 hours on 2026-10-27, and the runtime about two. If a
runtime collection stops, measure the remaining cases with `--case` into
another output directory: 20261027-linux-go1.27.1 does, and its `compose.py`
takes each case from one collection.

## Refresh only Mini

A refresh measures only `ddtest instrument` and keeps a baseline's Native and
Orchestrion observations. It needs a baseline of every variant recorded with
`refresh_benchmark_report.py`'s layout from commits of this repository: the
layout of 20261016-linux-go1.27.1, which was recorded in ddto's original
repository, so refresh from it in that checkout. 20261027-linux-go1.27.1 has
two runtime collections that `compose.py` merges; record every variant again
instead of refreshing from it.

Keep the default
repetitions here: `refresh_benchmark_report.py` merges Mini into a baseline
recorded with them and rejects a runtime collection with `--repeats`. The runner checks
its Go version and CPU affinity, and restores the original project sources and
module templates. The original input revision must be available in local Git
history. Keep the checkout unchanged until collection finishes.
Use a new artifact directory on disk, outside the repository and `/tmp`.

```sh
BENCH_RUN=/var/tmp/dd-ci-mini-benchmark-new
BASELINE=docs/results/<baseline>
GO_BENCH=/path/to/go1.27.1/bin/go
ORCHESTRION_BENCH=/path/to/orchestrion

python3 scripts/build_benchmark.py run \
  --variant mini --baseline "$BASELINE" \
  --go "$GO_BENCH" --orchestrion "$ORCHESTRION_BENCH" \
  --output "$BENCH_RUN/build" --control-repeats 0 \
  --max-seconds 14400 --max-gib 16

python3 scripts/runtime_benchmark.py \
  --build "$BENCH_RUN/build" --baseline "$BASELINE" \
  --output "$BENCH_RUN/runtime" --max-seconds 7200 --max-gib 6

python3 scripts/runtime_benchmark.py --agent \
  --build "$BENCH_RUN/build" --baseline "$BASELINE" \
  --output "$BENCH_RUN/agent" --max-seconds 900 --max-gib 1

python3 scripts/refresh_benchmark_report.py \
  --baseline "$BASELINE" --build "$BENCH_RUN/build" \
  --runtime "$BENCH_RUN/runtime" --agent "$BENCH_RUN/agent" \
  --output docs/results/new-run

python3 scripts/build_benchmark.py report \
  --input docs/results/new-run/build --update-readme
python3 scripts/benchmark_report.py --input docs/results/new-run
```

Name the new dataset after its collection date, platform and Go version, as in
`20261027-linux-go1.27.1`, instead of `new-run`. It replaces the previous
dataset: remove the old directory and update the links that point to it.

Orchestrion is checked as a reference tool during setup, although the
Mini-only run does not execute its instrumentation; its version must match the
baseline, and the Go binary must match the recorded toolchain. Public module
downloads are disabled by default; add `--download` to the build command to
allow them during setup. Downloads remain disabled during measured commands.

Run a pilot first with `--case testify-external--cover-testing --cpus 4 --repeats 1`.
Use another output directory for the full matrix. A pilot does not replace the
recorded repetitions or qualify the remaining cells.

## What is measured

Build uses `go test -c -o <directory>/ -ldflags=-w ./...`, so no test executable
runs inside the compilation clock. The matrix covers Gin, Chi, direct Testify
and an external Testify caller at 4/32 CPUs, with coverage and race
combinations. `build-benchmark.json` records all 24 configurations and the
repetition counts.

Cold builds start with an empty Go build cache and removed output. Modules and
the OS page cache remain warm. The remaining scenarios reuse output, force a
fresh link, edit a reachable test body and edit an unused constant. The constant
edit is a diagnostic: compiled code can remain reusable. Traces check whether
compilation and linking occurred; symbols check the expected instrumentation.

Runtime rebuilds the original subject sources outside the clock. It excludes
the edit markers used for build qualification. Each group runs the package
binaries concurrently, with five repetitions after one warmup. Normal and
deferred Mini use the same executable. The clock covers startup, settings,
tests and final delivery; receiver startup and MessagePack decoding are excluded.

The receiver uses loopback HTTP with real gzip and MessagePack. Test names,
statuses and multiplicities are checked against the retained Native inventory.
The recorded SDK serves only as an event oracle and has no benchmark column.
Native Examples added since that reference are identified from the executable
and checked explicitly, and missing references and failed runs stay visible.
Go's set coverage mode produces aggregate coverage; per-test uploads require
count or atomic mode. The Agent control separately checks local EVP delivery
with telemetry.

Every command has an exclusive systemd user scope. Cgroup v2 `cpu.stat` and
`memory.peak` include nested processes and detached daemons. Memory includes
charged file-cache pages and kernel memory as well as the Go heap. The runtime
receiver runs outside that scope. CPU-seconds can exceed wall-clock seconds on
multiple cores. Tables use median wall time in seconds and memory in MiB.

Orchestrion's percentage compares the same metric against Native. Mini's
percentages compare Orchestrion first, then Native. All use unrounded medians.
Positive values mean more time or memory; negative values mean less. Ranges,
sample counts and deterministic bootstrap intervals remain in
`statistics.json`. The intervals describe the observed samples; they do not
predict future runs. No slow observation is discarded.

## Requirements and limits

Collection needs Linux, cgroup v2, a systemd user manager, Python 3.9 or newer,
`taskset`, `readelf`, a C compiler for race builds and the recorded Go toolchain.
The archived controls use Go 1.27.1 on Linux/amd64. macOS and Windows can
regenerate the tables offline.

Choose time, disk and command limits before running. The runners retain
completed observations when a build, timeout, process drain or budget check
fails. They do not resume interrupted matrices or turn incomplete data into a
successful report. Large binaries, caches and full HTTP bodies belong in the
local artifact directory; the repository keeps timings, hashes and summaries.

Native and Orchestrion retained from another date provide fixed comparison
points, and their old convergence control describes only that collection. Read
the new ranges before attributing a small wall-time difference to code
changes. The [validation guide](validation.md) covers feature compatibility
and live-intake boundaries separately.

## Verify regeneration

These commands use the retained files only, without Go or network access:

```sh
python3 scripts/build_benchmark.py report \
  --input docs/results/new-run/build --update-readme --check
python3 scripts/benchmark_report.py --input docs/results/new-run --check
```

For a dataset that keeps its raw data in `raw.zip`, such as
20261027-linux-go1.27.1, extract it first, as its README shows. The scripts
check file hashes, complete rounds and validation outcomes. The
merger also verifies that every Native and Orchestrion build observation is
unchanged. Regeneration changes only the presentation and refreshes no
measurement.

## Per-test coverage modes

`scripts/file_coverage_benchmark.py` compares Go's counters, which Test Impact
Analysis uses today, with ddto's experimental file coverage. It runs `go test`
with each mode on cloned projects, with a loopback receiver that enables code
coverage. It reports, for each mode:
- the wall time;
- the production files per test;
- the tests that a change to one production file selects;
- the tests that miss a file of Go's counters;
- with `--bench`, the project's benchmarks.

```sh
(cd .. && go build -o ddto/ddtest .)   # ddtest's executable, which runs ddtest instrument
python3 scripts/file_coverage_benchmark.py --ddtest "$PWD/ddtest" --output /var/tmp/file-coverage-run \
  --bench 'fiber=Benchmark_Router_(Chain|Handler|Next|NotFound)$' \
  --report docs/results/file-coverage-<date>-<platform> \
  lo=/path/to/lo fiber=/path/to/fiber gin=/path/to/gin
```

Build ddtest's executable from the revision to measure, and measure from a
separate worktree: `ddtest instrument` compiles Mini from the checkout that
built it. Each test's files stay in the output's `files.json`. The report
records each project's commit and local changes. See [Experimental file coverage](mini-runtime.md#experimental-file-coverage)
for the latest numbers.
