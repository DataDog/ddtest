# dd-ci-testing-poc

A testing-only CI Visibility tool built on native Go build overlays.
The driver uses the Go standard library only. Its default `mini` backend uses
SDK-derived CI logic and a native event client with no external runtime module
dependencies. Use `--runtime=sdk` to run the unmodified `dd-trace-go` SDK at the
exact revision pinned in [`internal/version`](internal/version/version.go).
See [native runtime usage and contracts](docs/mini-runtime.md).

For maintainers, start with the [documentation guide](docs/README.md):
[architecture and diagrams](docs/architecture.md),
[source updates](docs/maintenance.md) and
[performance and profiling](docs/performance.md),
[CLI build diagnostics](docs/cli-debug.md), and
[CI feature parity, combinations and remaining gaps](docs/ci-parity.md).
[Fuzz and Examples](docs/fuzz-examples.md) documents their native lifecycle,
deferred delivery and SDK PR comparison.

```sh
go build -o bin/ddtest ./cmd/ddtest
# Run from the target module; no go.mod changes are needed:
/path/to/ddtest test -count=1 -race ./...
```

Use a Go 1.26 or 1.27 toolchain. The minimum is Go 1.26.0; a client module
can declare an older Go version. When providing Mini locally, `ddtest` raises
the Go directive only in its temporary module file to meet Mini's requirement.

If the module does not require the selected runtime (the pinned SDK, or this
module for Mini), `ddtest` provides it through a temporary copy of `go.mod` and
`go.sum` passed with `-modfile`. The module's files are never modified, so
`go mod tidy` cannot break a later run. Mini prefers its local sources or exact
cached version; any required `go get` reports progress on `stderr`.
See [native runtime usage](docs/mini-runtime.md#use-the-local-poc).

Set `DD_TRACE_DEBUG=true` for CLI preparation and build diagnostics on `stderr`,
including phase durations, runtime provisioning and selective tool decisions.
See [CLI build diagnostics](docs/cli-debug.md) for the fields and timing limits.

Runtime selection accepts `--runtime=mini` and `--runtime mini`, before or
after Go flags and package names. Place it before custom test flags, `-args`
or `--`; those begin the test binary's arguments. Invalid runtime values fail
in the CLI before Go starts. For example:

```sh
ddtest test -count 1 --runtime=mini ./...
ddtest test -count=1 --runtime sdk ./...
```

When `DD_CIVISIBILITY_ENABLED` is absent, the CLI sets it to `parent`. The SDK
activates CI Visibility for each test process and disables it for ordinary child
processes. Explicit values, including `false` and an empty value, are retained by
the CLI; runtime normalization remains the SDK's responsibility.

The tool prepares the SDK's nine `testing` aspects, injects an external test
file importing the selected runtime, then calls native `go test` with an
overlay. Original project sources, GOROOT and the SDK are not modified on
disk. Temporary sources are removed after Go finishes. Go owns compilation and
cache invalidation.
The exact SDK ownership marker and linkname ABI are retained, including process
retry control and abnormal finalization.

This is an experimental POC, not a replacement for supported Orchestrion releases.
It supports package-mode tests with the pinned SDK, build tags, test selection,
count/shuffle, JSON, benchmarks, race and coverage. Arguments follow `go test`'s
own rules, including `-C`, `--flag` spellings and custom test flags without
`-args`. It preserves the user's result-cache choice; use `-count=1` for fresh CI
events. A user overlay is merged into ours, and ours takes precedence. A user
`-toolexec` runs every tool after ours. Missing or ambiguous hooks fail before
compilation. Help and explicit `.go` file mode run native `go test` without
instrumentation, with a warning for file mode. SDK replacements and
standard-library test targets are outside this POC.
[Testify suite support](docs/testify.md) covers v1.4.0 and newer v1 releases,
including callers in external dependencies. Other versions, or an unrecognized
`suite.Run`, produce a warning; their suites run as ordinary instrumented tests. A selective `-toolexec` hook is
activated only for reachable Testify suites, goleak in Mini, or covered rewritten
`testing` sources. Other builds use the overlay directly. Preparation validates the selected
Testify version and API even when Go can reuse a cached archive. Version fixtures
cover v1.10.0, v1.11.1 and v1.12.1. Run from the desired module directory. Runtime
configuration and retry/skip/quarantine behavior remain in the selected runtime.

Mini automatically integrates with reachable goleak v1.3.0 or newer v1 releases,
independently of `DD_CIVISIBILITY_DEFERRED_DELIVERY`. Older versions produce a
warning and run without the CI goroutine filters. Set
`DD_CIVISIBILITY_DEFERRED_DELIVERY=true` to send buffered CI data between completed
test groups. The next group waits while delivery finishes. See
[delivery and goleak](docs/delivery.md)
for connection ownership, parallel tests, memory costs and cache identity.

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

<!-- build-benchmark-summary:start -->
## Benchmarks

[Build time, runtime and memory comparisons](docs/benchmarks.md) cover
Native, Orchestrion, POC SDK and POC Mini at 4/32 CPUs, including Testify,
coverage, race and deferred delivery. The report also links the repeated
115-case CI parity comparison and records failed runtime combinations.

The [compile-only dataset](docs/results/20261005-linux-go1.27.1/build/README.md) contains
6,224 comparative observations, measured at
POC commit `9d4786fbd27f573bb68c5516bf23b336e8d7bdaa`.

[Run benchmarks or regenerate the tables](docs/build-benchmarks.md).
<!-- build-benchmark-summary:end -->

## Source maintenance

Incorporated sources live in [`internal/thirdparty`](internal/thirdparty/README.md).
Every origin records its repository, exact upstream SHA, licenses and file hashes.
The current SDK extraction base is `dd-trace-go/main` at
`96aedb31048c07e29e7a20a4333dc3b8d289c52d`; differential fixtures use the same
version. The native client/transport remain separate from the upstream subsets.
Run `python3 scripts/upstream.py verify` to audit the source record offline.
The [maintenance guide](docs/maintenance.md) covers three-way SDK updates, codec
regeneration, platform changes and the required checks. Benchmark reports keep
the source revision that was measured.
