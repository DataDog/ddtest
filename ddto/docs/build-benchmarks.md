# Repeat the compile-only benchmarks

[`scripts/build_benchmark.py`](../scripts/build_benchmark.py) compares Native,
Orchestrion, POC SDK and POC Mini. It compiles test binaries and never executes
them. Use the separate [CI parity tests](ci-parity.md) to compare runtime
behavior and events.

The [latest comparison](benchmarks.md) includes build time, runtime and memory
for Gin, Chi, direct Testify and external Testify callers at 4/32 logical CPUs.
Its [build dataset](results/20261005-linux-go1.27.1/build/README.md) preserves
every completed command, including controls and trace checks. Reports keep
their measured POC revision, Go toolchain and SDK pin when regenerated.

## Regenerate the recorded tables

From the repository root:

```sh
python3 scripts/build_benchmark.py report \
  --input docs/results/20261005-linux-go1.27.1/build --update-readme
python3 scripts/benchmark_report.py \
  --input docs/results/20261005-linux-go1.27.1

# Check that the generated files are current, without writing:
python3 scripts/build_benchmark.py report \
  --input docs/results/20261005-linux-go1.27.1/build --update-readme --check
python3 scripts/benchmark_report.py \
  --input docs/results/20261005-linux-go1.27.1 --check
```

`build_benchmark.py report` needs Python 3.9 or newer. It reads `manifest.json`,
`observations.csv`,
`methodology.md` and `notes.md`, then writes `README.md` and `statistics.json`.
With `--update-readme`, it updates the short report links and measured revision
in the repository README. Comparison tables stay in dedicated documents. That option requires
a result directory inside the repository and preserves the rest of the README.
It runs no Go commands and uses no network. It rejects changed CSV contents,
failed commands, missing rounds and duplicate observations. Controls and traces
stay in the CSV but do not contribute to comparative medians.

`benchmark_report.py` writes `docs/benchmarks.md` and the dataset's complete
`memory.md`. It verifies the archived file hashes, the original contents of
compressed records and runtime memory medians against the per-run CSV. It uses
the recorded validation outcomes: a failed repetition cannot become a successful
comparison by filtering it out. No Go toolchain, local binary or original
`/var/tmp` path is needed to regenerate the documents.

All durations in the tables are wall-clock seconds, with `s` in each cell.
For example, `1.200 s (-40.0%; +20.0%)` means 40% less total command time than
Orchestrion and 20% more than Native. The two percentages use unrounded medians
and retain their signs, including a positive first value when the POC is slower.
`statistics.json` adds ranges, sample counts, variation and a deterministic
bootstrap interval for the median. The interval estimates uncertainty by resampling
the observed runs; it is not a range for future builds. Ranges are the actual
fastest and slowest observations. Runtime statistics and individual records
are in the dataset's `runtime/` directory. Memory tables use the median of
per-run cgroup `memory.peak`, in MiB; that includes charged page-cache and
kernel memory as well as all measured processes. It is not isolated Go heap
usage or a sum of independent process RSS peaks.

## Prepare the tools

The runner needs Linux with cgroup v2, a working systemd user manager, Python 3.9 or newer,
`taskset`, `readelf`, a C compiler for race builds, and the chosen Go toolchain.
The archived matrix used Go 1.27.1 on Linux/amd64. macOS and Windows can regenerate
its tables; this runner does not measure builds on those platforms.

Build tools and dependency setup are outside the timed commands. The runner
builds `ddto` from the checkout it is launched from and records the HEAD,
working-tree status, source hashes and executable hashes. Keep that checkout
unchanged during a run. The prepared subjects are copies in a new artifact
directory; their source repositories, GOROOT and incorporated SDK stay untouched.

Install the frozen Orchestrion reference before running the benchmark:

```sh
go -C testdata/orchestrion build -mod=readonly -o "$(go env GOPATH)/bin/orchestrion" github.com/DataDog/orchestrion
```

[`build-benchmark.json`](../scripts/build-benchmark.json) records project
versions, upstream SHAs, SDK/Orchestrion references, CPU counts, repetition
counts and all 24 subject/flag combinations. The selected SDK must match
`internal/thirdparty/dd-trace-go/SOURCE.json`. The runner checks Gin/Chi's
module-origin commit and uses the frozen dependency templates in
[`scripts/testdata/build-benchmark`](../scripts/testdata/build-benchmark).
All variants use the same prepared module graph. Orchestrion loads only the
SDK's testing rules, including Testify.

By default setup uses modules already in the Go module cache and fails if any
are missing. Add `--download` to allow public module downloads during setup.
Downloads are always disabled during measurement. Preparation may populate the
module cache; its temporary Go build cache is removed before timing starts.

## Run the complete matrix

```sh
python3 scripts/build_benchmark.py run \
  --go /path/to/go \
  --orchestrion /path/to/orchestrion \
  --output /var/tmp/dd-ci-build-matrix-new \
  --cpus 4,32 --download \
  --max-seconds 21600 --max-gib 24 --max-commands 7500
```

Choose a new directory and limits suitable for the host. The command above
allows six hours including setup; it is an example budget, not a prediction.
The latest build, runtime and repeated-parity collection used about ten hours
of active experiment time. The build command alone does not run those runtime
phases. Artifact limits are checked between
commands, so one build can exceed the disk threshold before the runner stops.
Per-command timeouts default to 600 seconds. Only this run's build scopes and
owned caches are stopped or removed.

The default matrix has five scenarios:

| Scenario | What is measured |
| --- | --- |
| Cold | A fresh empty Go build cache and removed output for every variant/repetition |
| Cached | The variant's cache and existing output, with no changes |
| Forced link | Warm dependency archives and a unique linker `-buildid` |
| Real test-body edit | A different reachable `t.Log` call inserted through an overlay |
| Unused-constant diagnostic | A different unused constant; compiled code may be reused |

Gin/Chi without additional flags use 5/10/20/10/3 repetitions respectively.
The remaining combinations use 3/10/10/5/3. Variant order rotates and builds run
serially. CPU affinity, `GOMAXPROCS` and `-p` agree; the runner selects the first
N logical CPUs allowed to the process and records their physical-core topology.
That selection can include SMT siblings. It does not reserve those CPUs against
other host work. The archived host's first four CPUs were distinct physical
cores; its 32 CPUs included SMT.

Before the full matrix, an 80-repetition Native Gin forced-link control checks
convergence: the last two cumulative 20-run median changes must be at most 5%,
and the bootstrap 95% median interval width must be at most 20% of the median.
If it fails, the run stops and keeps every observation. This checks that control's
median, not stability of every matrix cell. A new run has its own control and
does not inherit the archived result.

Each CPU/case cell also has separate `-x` checks: unchanged compilation invokes
no compiler/linker, a forced link links every expected binary, and a reachable
edit compiles and links. Symbol checks verify the SDK/Mini testing hook and,
for Testify fixtures, the suite hook. `readelf` checks that `-w` removed DWARF.
No executable is run for qualification.

## Run a smaller selection

```sh
python3 scripts/build_benchmark.py list

python3 scripts/build_benchmark.py run \
  --go /path/to/go --orchestrion /path/to/orchestrion \
  --output /var/tmp/dd-ci-build-smoke-new \
  --cpus 4 --case gin --case testify-external--race-cover-testing \
  --repeats 1 --control-repeats 0 \
  --max-seconds 1200 --max-gib 4 --max-commands 100
```

`--case` can be repeated. `--repeats` overrides the count for every scenario;
one round is useful to verify the driver, not to claim a performance gain.
Skipping the native control provides no convergence evidence. Testify-only
selections must use `--control-repeats 0`, since the control uses Gin.

Flag combinations include `-race`, `-cover`, client-wide atomic coverage,
coverage of `testing` and `testify/suite`, and race plus those coverage modes.
The external fixture reaches `suite.Run` through a separate replacement module,
so it checks the selective compiler hook and the coverage bridge together.

## Evidence and interrupted runs

Every completed command gets JSON, build stdout/stderr, a launcher log and a CSV
row. Final results include the manifest, traces, binary hashes, `statistics.json`
and the five-table README. Wall time covers the top-level command until exit;
`post_exit_wait_s` records the additional process-tree drain. `cpu.stat` and
`memory.peak` cover the exclusive scope, including detached daemons and nested
builds. They also include the small measurement helper. Direct-child resource
accounting is not substituted for those whole-build values. CPU-seconds add work
across processes and threads, so they can exceed wall-clock seconds on multiple
cores. Table cells always use wall time.

Time, disk, build failures and undrained processes stop the run. Completed rows
and remaining artifacts are retained; an attempt without a usable timing gets
an interruption record. Partial matrices cannot produce a successful report.
The runner does not resume an interrupted matrix. Start a new output directory
for a new experiment; preserve the partial run if it matters for diagnosis.
The latest dataset retains both Native controls and all 1,440 runtime groups,
including failed and unverified results.

The checked-in report keeps timings, input provenance, summaries, control data,
traces and qualification hashes. Large test binaries, build caches and full
stdout/stderr remain in the original local artifact directory. Its absolute paths
identify the collection environment; table regeneration does not require that
directory.

## Update the benchmark inputs

When changing the SDK pin, update `build-benchmark.json` and the prepared
`go.mod.template`/`go.sum` files together. Resolve the test targets and both
runtime imports with the new SDK outside measurement, confirm the selected
versions, then apply the same graph to all four variants. Keep only the POC
path placeholder and the external fixture's relative `./testkit` replacement.
Changing Gin/Chi requires updating their version, upstream SHA, expected binary
names and reachable test-body signature as well.

Run a reduced matrix and inspect the traces and symbol checks before repeating
the expensive series. A new run records the new inputs and belongs in a new
results directory. After validating the new dataset, update the comparison
document and README links together. Keep only the latest benchmark dataset in
the repository. Preserve SDK provenance, adaptation notes and compatibility
fixtures independently of benchmark data.
