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
