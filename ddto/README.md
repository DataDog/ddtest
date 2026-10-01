# dd-ci-testing-poc

A testing-only CI Visibility instrumentator using native Go build overlays.
The driver uses the Go standard library only. Test binaries use the **unmodified
`github.com/DataDog/dd-trace-go/v2 v2.11.0-rc.1`** SDK and its existing hooks.

```sh
go build -o bin/ddtest ./cmd/ddtest
# Run from a target module which already requires the supported SDK:
/path/to/ddtest test -count=1 -race ./...
```

When `DD_CIVISIBILITY_ENABLED` is absent, the CLI sets it to `parent`. The SDK
activates CI Visibility for each test process and disables it for ordinary child
processes. Explicit values, including `false` and an empty value, are retained by
the CLI; runtime normalization remains the SDK's responsibility.

The tool prepares the SDK's nine `testing` aspects, injects an external test
file importing `dd-trace-go/v2/civisibility`, then calls native `go test` with an
overlay. Project sources, GOROOT and the SDK are not rewritten. Temporary
sources are removed after Go finishes. Go owns compilation and cache invalidation.
The exact SDK ownership marker and linkname ABI are retained, including process
retry control and abnormal finalization.

This is an experimental POC, not a replacement for supported Orchestrion releases.
It supports package-mode tests with the pinned SDK, build tags, test selection,
count/shuffle, JSON, benchmarks, race and coverage. It forwards native flags and
preserves the user's result-cache choice; use `-count=1` for fresh CI events.
Existing overlays are merged. Missing or ambiguous hooks and conflicting `-toolexec`
configuration fail before compilation. Explicit `.go` file mode, `-C`, SDK
replacements, standard-library test targets and `testify/suite` are outside this
POC. Run from the desired module directory. Runtime configuration and intentional
retry/skip/quarantine behavior remain in the SDK.

## Reproduce verification

```sh
go install github.com/DataDog/orchestrion@v1.13.2-0.20260917114356-5c24783fcd76
ORCHESTRION_BIN="$(go env GOPATH)/bin/orchestrion" go test -v ./...
```

With `ORCHESTRION_BIN`, the suite compares **both instruments using the same
temporary module graph, SDK version, fixture sources and binary basename**.
Orchestrion loads the SDK's actual `gotesting/orchestrion.yml`; other APM
integrations are outside the comparison. Its required tool dependency is added
only to the temporary comparison fixture, never the driver module or SDK.
Without that variable, tests still verify the overlay against native Go and the
real SDK, but the Orchestrion differential check has not run.

Tests send actual SDK MessagePack payloads to a loopback HTTP server with a
synthetic API key. They compare test/session/module/suite events, statuses,
skip reasons, error messages/stacks and source locations. Generated IDs,
timestamps/durations and invocation names are excluded from the semantic
comparison. Native output checks retain all lines and their multiplicity while
allowing native parallel completion order and elapsed times to differ.

GitHub Actions is configured to run the differential suite on Go 1.26/1.27 Linux and Go 1.27
macOS/Windows. A green Go version is compatibility evidence for that tested
version; it does not imply support for every future toolchain or SDK.

## Compilation performance

The following compile-only measurements use **`go test -c -o <directory>/ -ldflags=-w ./...`**. No test binaries are executed, so SDK runtime and network flushing are excluded. Times are wall-clock medians in seconds. The percentage in the POC column is the reduction relative to Orchestrion, calculated from unrounded medians as `100 × (1 − POC / Orchestrion)`.

Measured on an AMD Ryzen 9 5950X running Arch Linux amd64 and the development toolchain `go1.27.0-X:nodwarf5`. Each configuration uses the first N physical cores (no SMT), `GOMAXPROCS=N`, and `-p=N`. Gin v1.12.0 produces six test binaries; Chi v5.3.2 produces two. All variants use the same project sources and module resolution graph. Both instrumentators use the unmodified `dd-trace-go/v2@v2.11.0-rc.1` SDK; Orchestrion `v1.13.2-0.20260917114356-5c24783fcd76` loads only its `gotesting` aspects. The measured POC source is commit `4e56e8657949aaa1209afbaa19ef9767bbf6da90`.

### Cold build

Two runs per variant with independent, empty Go build caches and no existing output binaries. Downloaded modules and the OS page cache remain warm.

| Project | Cores | Native | Orchestrion | POC |
| --- | ---: | ---: | ---: | ---: |
| Gin | 1 | 60.94 s | 141.76 s | 95.63 s (**−32.5%**) |
| Gin | 4 | 17.74 s | 45.96 s | 27.72 s (**−39.7%**) |
| Gin | 8 | 12.92 s | 28.32 s | 18.25 s (**−35.6%**) |
| Gin | 16 | 11.34 s | 26.82 s | 17.33 s (**−35.4%**) |
| Chi | 1 | 26.61 s | 94.27 s | 65.38 s (**−30.6%**) |
| Chi | 4 | 7.81 s | 29.41 s | 18.10 s (**−38.5%**) |
| Chi | 8 | 5.49 s | 19.61 s | 11.72 s (**−40.2%**) |
| Chi | 16 | 4.64 s | 17.11 s | 10.76 s (**−37.1%**) |

### Cached dependencies with forced linking

Five runs per variant with compiled dependencies in cache. Every run adds a unique `-buildid` alongside `-w` to force fresh links. These timings include CLI preparation, Go build planning, and any required generated-main compilation; they do not isolate the linker or measure unchanged-binary reuse.

| Project | Cores | Native | Orchestrion | POC |
| --- | ---: | ---: | ---: | ---: |
| Gin | 1 | 2.49 s | 10.96 s | 6.00 s (**−45.2%**) |
| Gin | 4 | 1.08 s | 4.98 s | 2.96 s (**−40.7%**) |
| Gin | 8 | 1.06 s | 5.43 s | 4.23 s (**−22.2%**) |
| Gin | 16 | 0.99 s | 4.97 s | 4.12 s (**−17.2%**) |
| Chi | 1 | 0.56 s | 3.87 s | 1.68 s (**−56.7%**) |
| Chi | 4 | 0.26 s | 1.80 s | 1.21 s (**−32.7%**) |
| Chi | 8 | 0.25 s | 1.61 s | 1.03 s (**−36.3%**) |
| Chi | 16 | 0.27 s | 1.73 s | 0.97 s (**−44.2%**) |

[Complete measurements](docs/benchmark-compile-no-dwarf.json) retain all 400 commands: 360 performance samples, 16 A/A controls, and 24 trace validations, with ranges and paired comparisons. All commands succeeded; all 96 output binaries were checked without DWARF; the SDK instrumentation hook was present only in the POC and Orchestrion outputs. CPU and peak memory accounting include the Orchestrion daemon and nested builds.

These are local, exploratory results. Two cold runs do not establish stability, and the physical cores were not reserved from other workloads. Results should not be extrapolated to other applications, platforms, or toolchains. The earlier series that retained DWARF was measured at a different time; its noisy Gin eight-core cold results do not isolate the effect of `-w`. The JSON also preserves unused-constant edit measurements as diagnostics, not evidence for real test-body edit performance.

The commands below illustrate the eight-core configuration from each prepared target module. Use separate Go build caches and output directories for each variant; for the cached-dependency scenario, append a different `-buildid` value to `-ldflags` on each run. CPU affinity and cache cleanup were managed outside the timed commands.

```sh
GOMAXPROCS=8 go test -p=8 -c -o ./out/native/ -ldflags=-w ./...
GOMAXPROCS=8 /path/to/ddtest test -p=8 -c -o ./out/poc/ -ldflags=-w ./...
GOMAXPROCS=8 go test -p=8 -c -o ./out/orchestrion/ \
  -toolexec="/path/to/orchestrion toolexec" -ldflags=-w ./...
```

## Compare compilation

```sh
python3 scripts/benchmark.py --ddtest /path/to/ddtest \
  --orchestrion /path/to/orchestrion --output /var/tmp/dd-ci-benchmark
```

The bounded script keeps every timing, alternates variant order, isolates cold
Go build caches, and checks warm compilation and identical package edits. It
uses `go test -c`, so runtime/network flushing is excluded. It reports wall time,
not incomplete CPU accounting. Two cold rounds and five warm/edit rounds are
exploratory data; this small fixture does not establish savings for your application.

See [validation scope](docs/validation.md) and [results](docs/results.md).
