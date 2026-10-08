# Optimization backlog

Each item is a separate experiment and change. The order balances likely CPU
or allocation savings, frequency and implementation risk. Impact estimates
are qualitative; validate them on the current revision before implementing.
Use [performance and profiling](performance.md) for measurement and ownership
rules, and [feature parity](ci-parity.md) for the behavior to preserve.

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 1 | Represent span options as values | CPU and allocations on every event; clearer metadata-capacity estimates | Medium |
| 2 | Cache common numeric CI options | Repeated option construction per span | Low to medium |
| 3 | Reduce per-test metadata lookup | Contention and lookup work in large parallel suites | Medium to high |
| 4 | Reduce remaining provisioning commands | Startup of CLI invocations that must provide the runtime | Low only if existing metadata is sufficient |
| 5 | Supply coverage module identity from the CLI | A repeated subprocess at covered-binary startup | Medium |
| 6 | Share Git metadata within one CLI invocation | Repeated serial Git queries across test packages | Medium |
| 7 | Store small metric sets without a map | Allocations for metric-bearing events | Medium; measure lookup/encoding tradeoffs |
| 8 | Collect coverage counters in memory | Profile I/O and parsing per covered test | High |
| 9 | Share matching settings requests | Network startup for many small packages | High |
| 10 | Coordinate telemetry startup/shutdown requests | Network round trips around tests | High; changes lifecycle ownership |

## 1. Span options as values

`StartSpanOption` is a function. Options create closures and their count does
not say how many local metadata entries they add. Try a value representation
for timestamps, resource, type, tags and hierarchy fields, with explicit entry
counts. The experimental public API can change before a release; retaining the
closure shape is not a requirement.

Measure `BenchmarkSpanMetadata` with CI-shaped and text-heavy options, then
`BenchmarkEventLifecycle` with and without gzip. Preserve option ordering,
string/numeric transitions, errors, common snapshots, getters after `Finish`
and identical decoded events. Record SDK bindings in `ADAPTATIONS.md`.

## 2. Common numeric CI options

String tags use an immutable revisioned snapshot. Numeric CI metrics still
construct fresh options for each call. Test whether a revisioned numeric base
or prepared value options reduce that work.

Preserve explicit metric updates, direct sequential edits allowed by the SDK
API, per-event overrides and Bazel filtering. Benchmark lookup plus complete
span creation; a cache that scans a larger map can cost more than it saves.

## 3. Per-test metadata lookup

Execution metadata is tracked outside native testing objects. Profile its
lookup and update path with serial tests, parallel subtests and retries before
changing storage. A field added through the `testing` overlay could remove
some shared-map operations, but introduces another private layout contract.

Preserve cleanup lifetime, native cancellation, Testify scopes, retry ownership,
fuzz seeds and SDK-free tests. Validate offsets on every supported toolchain,
cache invalidation and both delivery modes. Prefer a smaller change when the
profile does not justify changing `testing.common`.

## 4. Runtime provisioning commands

Use build debug logs to count the remaining `go env` and `go mod` calls. A
module-root Mini invocation can provision before its full package query; that
path already resolves selected packages only once. Do not add another query
or a persistent cache to improve it.

Investigate combining commands or reusing metadata already obtained. Keep Go
responsible for parsing effective module files, overlays, replacements and
workspaces. Compare Mini already declared, a local replacement, CLI-local
provisioning and a missing published source. The common declared-runtime path
must not become slower or modify client files.

## 5. Coverage module identity

Coverage initialization discovers its module in each binary. Try supplying
that identity during CLI preparation while retaining the runtime fallback for
manual instrumentation and standalone binaries.

Check workspaces, replacements, `-C`, `-trimpath`, package working directories,
`TestMain` directory changes and retry children. Measure covered startup
separately from profile collection. Do not replace Go's source-path resolution
with a guessed module path.

## 6. Per-invocation Git metadata

Multiple package binaries repeat repository, branch and commit queries. Try
collecting reusable values once in the CLI and passing them through the
existing Git overrides or an invocation-local record. Keep Git commands serial;
parallelizing them is not part of this experiment.

Share only equivalent repository inputs. Check worktrees, nested modules,
repository overrides, `-C`, CI-provider precedence and standalone binaries.
Measure one-package and many-package commands; package starts already overlap.

## 7. Small metric sets

Most CI events have few numeric metrics. Compare their map with a small value
slice or inline representation, including text-heavy and custom-metric events.
A linear lookup can regress larger sets, and converting back to a map during
encoding would lose the allocation saving.

Preserve numeric precision, replacement/removal, getter results, option ordering
and the MessagePack map schema. Include parallel finish/capture and retry tests.

## 8. Coverage counters in memory

Per-test coverage emits and parses profiles around attempts. An in-memory
counter path could reduce I/O and allocations, but depends on private runtime
coverage data and synchronization.

Prototype outside the main implementation first. Preserve cleanup-only code,
parallel attribution, initial-attempt retry policy, impacted-test inputs,
aggregate percentages, normal/deferred processing and shutdown on errors.
Compare count and atomic modes, including `-race`, across supported toolchains.
Measure profile bytes, CPU, allocations, temporary disk and peak memory.

## 9. Shared settings requests

The runtime owns settings and feature discovery per binary. Experiment with
CLI-scoped sharing only for requests with identical repository, commit,
service, environment and effective test configuration. CODEOWNERS services
can make packages' request keys different.

The manifest/read-cache path is a possible input mechanism; events must still
upload normally. Preserve response/error fallbacks for ITR, retries, EFD,
impacted tests and management, independent invocations and event hierarchy.
Compile-only `-c` must send no runtime requests. Standalone binaries, retry
children and interrupted parents need a safe fallback without a live CLI.

Measure first-test admission and whole-command time with one and many packages,
local and delayed receivers, cold and cached builds. Sharing helps wall time
only when it removes work from the command's critical path.

## 10. Telemetry requests

Start with the two final `message-batch` sends: investigate whether compatible
messages can share a request while preserving order, timestamps, retries and
`app-closing`. Keep `app-started` separate under its existing mapper contract.

CLI coordination of initial telemetry is a separate, larger step. One startup
request per CLI would change per-process runtime identity. Define application
and parent/child lifecycles before reducing that count; keep child metrics and
configuration observable. Startup and shutdown must respect deferred admission
and goleak checkpoints, including errors and cancellation.

Use decoded telemetry and actual HTTP captures to check values and logical
payload/attempt counts. Network waits need delayed-receiver experiments;
CPU microbenchmarks cannot establish that saving.

## Experiment record

Keep the baseline and candidate SHAs, inputs, commands, raw repetitions and
limits in a task-scoped artifact directory. Alternate execution order and
retain slow or failed samples. Record CPU, allocations, complete wall time,
wire bytes and peak memory when they apply. A hot function's profile share is
not a predicted percentage saving.

Before keeping a change, run the affected contract tests and the SDK comparison.
Document runtime adaptations beside the incorporated SDK code so an upstream
update can preserve their ownership rules. Compiled-code refactoring is outside
this queue until its scope is agreed.
