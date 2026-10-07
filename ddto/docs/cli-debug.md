# CLI build diagnostics

Set `DD_TRACE_DEBUG=true` to see what `ddtest` does before the test runtime
starts. The CLI writes diagnostics to `stderr` with the prefix
`TestOptimization.build v0.0.0`. Mini runtime logs use
`TestOptimization.run  v0.0.0`. Both prefixes use the version in
`internal/version`; the SDK backend keeps its own logger and SDK version.

```sh
DD_TRACE_DEBUG=true ddtest test -count=1 ./...
```

For a build-only investigation, compile the test binary without running it:

```sh
DD_TRACE_DEBUG=true ddtest test -c -o ./tests.bin .
```

`DD_TRACE_DEBUG` accepts Go's `strconv.ParseBool` values, including `true`, `1`
and `TRUE`. An absent, empty, false or invalid value disables CLI debug logs.
There is no new flag, dependency or log file. Without debug, the CLI keeps its
usual warnings, errors and live `go get` progress.

## Read the output

Each debug line starts with local date and time (`YYYY/MM/DD HH:MM:SS`),
followed by the component, version and log level, matching Mini's runtime logger.
The message starts with `+` elapsed time since the CLI began. A phase has a
`started` line followed by `finished duration=... status=ok|error`. The start
line appears before work begins, so a stalled command still shows where it is
waiting. Durations include subprocess waiting and I/O; they do not measure CPU.

The log records:

- The selected runtime, its resolved module version and replacement status, and
  the effective working directory, including `-C`.
- Package resolution, with the number of requested patterns and resolved packages.
- An early `resolve runtime module` probe when Mini is absent from the effective
  module text. It uses a temporary module and checksum file. An unknown module
  is an expected result; Go still resolves every package after provisioning.
- Runtime provisioning and each `go env`, `go mod` or `go get` invocation.
  Mini reports whether it uses a client replacement, local CLI sources, or a
  published version. Local CLI sources can be a checkout or its exact cached
  module. Raising the temporary module's Go directive is recorded too.
- The `testing` transformation, its source and rewritten-file counts, and the
  Fuzz and parallel-stop hooks selected for that runtime.
- Optional library discovery, including how many imports still need a query.
  Testify and goleak each report detection, instrumentation and warnings.
- User-overlay loading, final-overlay writing, test-package counts and whether
  a temporary modfile or coverage bridge is needed. `generated_backing_files`
  counts the distinct generated hook/import files; rewritten standard-library
  files are counted separately.
- The selective tool decision for Testify, goleak and coverage, plus whether
  the client supplied its own `toolexec`.
- Native `go test` duration and exit code, followed by the whole CLI duration
  and exit code. Interrupt forwarding and context cancellation are recorded.

For example, these abbreviated lines explain a local Mini build. The times
illustrate the format; they are not benchmark results:

```text
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +120µs runtime=mini
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +200µs prepare started
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +400µs resolve packages started
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +24ms resolve packages finished duration=23.6ms status=ok
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +24.1ms provide runtime started
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +31ms mini source=cli-local
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +36ms provide runtime finished duration=11.9ms status=ok
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +58ms plan ready testing_files=3 test_packages=1 overlay_entries=5 generated_backing_files=2 temporary_modfile=true cover_bridge=false
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +59ms tool selection testify=false goleak=false cover=false user_toolexec=false
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +60ms go test started
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +410ms go test finished duration=350ms status=ok
2026/10/07 10:00:00 TestOptimization.build v0.0.0 DEBUG: +410.1ms go test exit_code=0
```

Preparation contains the smaller preparation phases. Whole-CLI time contains
preparation, `go test` and cleanup. Do not add these overlapping durations.
A normal `go test` duration includes building and running tests. With `-c`, it
covers building only. To inspect Go's individual compiler and linker commands,
combine debug with native `-x`; the CLI does not profile those tools itself.

## Runtime timing

Mini records these additional durations under the same debug setting:

| Log field | What the duration includes |
| --- | --- |
| `runtime bootstrap finished` | CI tags, tracer setup and other synchronous bootstrap work |
| `settings initialization finished` | Settings setup and its join with initial telemetry, including error paths |
| `ciVisibilityHttpClient: request finished` | One attempt, serialization, response consumption and any retry backoff |
| `telemetry: request finished` | One endpoint attempt through response EOF and close |

The bootstrap can launch feature initialization asynchronously. Its duration
therefore does not mean the runtime is ready to enter a test. Settings and
`app-started` overlap; do not sum their durations. HTTP summaries identify the
endpoint path or telemetry request type, status and retry information without
adding headers, query strings or bodies.

The telemetry debug duration includes response consumption. The existing
`telemetry_api.ms` metric keeps its measurement at response headers. Logging
must not redefine that metric or change request accounting.

These logs can produce a phase table for one invocation. Repeated invocations
are needed for medians and ranges. They do not measure the exact first test-body
entry, individual compiler/linker durations, CPU time or memory. A normal
`go test` duration includes both building and executing tests; use `-c` for a
build-only observation.

## Output, cache and ownership

Debug lines stay out of `stdout`, including `go test -json`. The CLI does not
change test arguments, generated sources, fingerprints, compiler identities or
exit codes when logging is enabled. Go still decides cache reuse; environment
variables read by a test can legitimately affect Go's test-result cache.

The CLI's debug summaries do not dump arguments, `GOFLAGS`, credentials or
subprocess output. Existing native output, download progress and errors pass
through as before. The working directory is included to diagnose path issues.
Logging failures do not replace the Go command's result.

`tool-overlay` bypasses do not initialize logging or read `DD_TRACE_DEBUG`.
Unrelated tools and compiler/linker version probes keep their native output and
fast dispatch. The parent logs which integrations required the tool wrapper.
There is no per-tool debug stream or additional `toolexec` activation.

The logger is invocation-local in `internal/runner/debug.go`. A context carries
it through preparation and Go helpers; package APIs such as `PrepareRuntime`
stay silent even when the environment enables debug. The debug writer also
serializes Go's `stderr` with cancellation messages, so callers can safely use
a buffer for that stream. Runtime durations use the incorporated SDK logger;
its adaptations and update checks are recorded in
[`ADAPTATIONS.md`](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md).

The CLI tests compare debug-disabled and debug-enabled binaries byte for byte,
check JSON output and Go cache reuse, and cover build errors, downloads,
provisioning, signals and tool bypasses. These tests run in the compatibility
workflow on Linux, macOS and Windows, including Linux `-race` jobs.
