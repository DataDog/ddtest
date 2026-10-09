# Run and regenerate the benchmarks

The comparison uses Native, Orchestrion and POC Mini. POC SDK is no longer a
benchmark variant. Runtime includes normal and deferred Mini; memory and CPU
are recorded for every measured command.

The [comparison tables](benchmarks.md) link to the complete matrix and raw data.
Each variant has its own measured revision and date. Refreshing Mini reuses the
recorded Native and Orchestrion observations without changing their values.

## Refresh only Mini

Use the baseline directory linked from the comparison tables. The runner checks
its Go version and CPU affinity, and restores the original project sources and
module templates. The original input revision must be available in local Git
history. Keep the POC checkout unchanged until collection finishes.
Use a new artifact directory on disk, outside the repository and `/tmp`.

```sh
BENCH_RUN=/var/tmp/dd-ci-mini-benchmark-new
BASELINE=docs/results/20261009-linux-go1.27.1
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

Orchestrion is checked as a reference tool during setup; the Mini-only run does
not execute its instrumentation. Its version must match the baseline. The Go
binary must match the recorded toolchain. Public module downloads are disabled
by default; add `--download` to the build command to allow them during setup.
Downloads remain disabled during measured commands.

Run a pilot first with `--case testify-external--cover-testing --cpus 4 --repeats 1`.
Use another output directory for the full matrix. A pilot does not replace the
recorded repetitions or qualify the remaining cells.

## What is measured

Build uses `go test -c -o <directory>/ -ldflags=-w ./...`; no test executable runs
inside the compilation clock. The matrix covers Gin, Chi, direct Testify and an
external Testify caller at 4/32 CPUs, with coverage and race combinations.
`build-benchmark.json` records all 24 configurations and the repetition counts.

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
The recorded SDK is an event oracle, not a POC SDK benchmark column. Native
Examples added since that reference are identified from the executable and
checked explicitly. Missing references and failed runs stay visible. Go's set
coverage mode produces aggregate coverage; per-test uploads require count or
atomic mode. The Agent control separately checks local EVP delivery with telemetry.

Every command has an exclusive systemd user scope. Cgroup v2 `cpu.stat` and
`memory.peak` include nested processes and detached daemons. Memory includes
charged file-cache pages and kernel memory, not just the Go heap. The runtime
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
points. Their old convergence control describes that collection, not the current
host. Read the new ranges before attributing a small wall-time difference to
code changes. The [validation guide](validation.md) covers feature compatibility
and live-intake boundaries separately.

## Verify regeneration

These commands use the retained files only, without Go or network access:

```sh
python3 scripts/build_benchmark.py report \
  --input docs/results/new-run/build --update-readme --check
python3 scripts/benchmark_report.py --input docs/results/new-run --check
```

The scripts check file hashes, complete rounds and validation outcomes. The
merger also verifies that every Native and Orchestrion build observation is
unchanged. Regeneration changes presentation; it does not refresh a measurement.
