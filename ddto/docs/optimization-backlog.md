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
| 3 | Keep per-test state in `testing`'s structures | Map, lock and offset work on every test; field copies on retries | Medium to high |
| 4 | Reduce remaining provisioning commands | Startup of CLI invocations that must provide the runtime | Low only if existing metadata is sufficient |
| 5 | Upload repository data from the CLI | Git upload repeated, and waited for, at every test binary's exit | Medium to high |
| 6 | Collect Git metadata in the CLI | Repeated serial Git queries across test packages | Medium |
| 7 | Store small metric sets without a map | Allocations for metric-bearing events | Medium; measure lookup/encoding tradeoffs |
| 8 | Collect coverage counters in memory | Profile I/O and parsing per covered test | High |
| 9 | Fetch settings in the CLI | The first request on the critical path; requests outside the read cache | High |
| 10 | Coordinate telemetry startup/shutdown requests | Network round trips around tests | High; changes lifecycle ownership |

Items 5, 6 and 9 move work that every test binary repeats into the CLI, which
runs once per invocation. The CLI does the work and hands the results to the
binaries through the environment and files they already read.

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

## 3. Per-test state in `testing`'s structures

Execution metadata lives in a global map keyed by the testing object, behind
one `RWMutex` (`gotesting/instrumentation.go`). Coverage shutdown markers and
two Testify lookups use more maps keyed by tests. Private `testing` fields are
read through offsets computed by reflection (`reflection_offsets.go`), and
retries copy them by name. Measured on Linux/amd64 at `44c7fd8`: a metadata
lookup takes 12 ns alone and 28 ns with eight goroutines; creating and
deleting an entry takes 215 ns and one allocation; reading the private fields
through offsets takes 68 ns and 112 bytes per call. The reflection fallback,
used when layout validation fails, takes 7.2 µs and 71 allocations per call.
A field read directly costs almost nothing.

The `testing` overlay already rewrites the package for every supported
toolchain. It could add Mini's state to `testing.common` and generate
accessors, including a retry copy, for the fields read today through offsets.
That removes most of these maps, the lock and the per-call allocations, and
turns layout assumptions into errors when the overlay is built. Expect about a
microsecond and several allocations less per test, and less contention in
large parallel suites; the larger benefit is removing the offset and
reflection code.

The standard library cannot import Mini. Added fields must use standard types,
such as an `any` slot, and Mini must reach the accessors through registration
hooks, as `ParallelStopHook` does, or interface assertions. Mini still builds
and runs without the overlay, through `testopt.RunM` and its own unit tests, so
the map path stays as the fallback. Preserve cleanup lifetime, native
cancellation, Testify scopes, retry ownership, fuzz seeds and SDK-free tests.
Validate every supported toolchain, including tip, cache invalidation and both
delivery modes.

## 4. Runtime provisioning commands

Use build debug logs to count the remaining `go env` and `go mod` calls. A
module-root Mini invocation can provision before its full package query; that
path already resolves selected packages only once. Do not add another query
or a persistent cache to improve it.

The temporary workspace for older modules, vendor and `go.work` runs the most
commands: `go mod edit -json` and `go mod edit -print` on the client module,
`go work edit -json` and `go work edit -use` on the workspace, and a module
query. Investigate combining commands or reusing metadata already obtained.
Keep Go responsible for parsing effective module files, overlays, replacements
and workspaces. Compare Mini already declared, a local replacement, CLI-local
provisioning, a temporary workspace and a missing published source. The common
declared-runtime path must not become slower or modify client files.

## 5. Repository upload from the CLI

Each test binary uploads repository data when it closes: `search_commits`,
`git log`, `rev-list` and `pack-objects`, then `packfile`, and it waits for the
upload before exiting. Against a local agent that always reported missing
commits, that took about 0.45 s of each binary's 0.6 to 0.8 s. With the real
backend, binaries that start after an upload find its commits, but binaries
started together, as for a new commit in CI, all upload the same data.

Move the upload to the CLI: start it once per invocation, overlapping
compilation and tests, and let the binaries skip it. The CLI must finish or
abandon it before exiting, keep the SDK's commit search, unshallow and
size limits, and keep Bazel payload-file mode and disabled upload untouched.
Standalone binaries keep their own upload.

## 6. Git metadata from the CLI

Every test binary repeats the repository, branch and commit queries: four
serial Git commands at startup, about 10 ms. Collect those values once in the
CLI and pass them through the existing Git overrides or an invocation-local
record. Keep Git commands serial; parallelizing them is not part of this
experiment.

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

## 9. Settings from the CLI

The SDK's short-lived read cache already shares identical settings, known-tests
and skippable-tests responses between binaries: by parent process locally and
by CI job in CI. One `ddto test ./...` sends one settings request. The first
binary still waits for it after compilation, and requests outside the cache's
scope repeat, such as packages with different CODEOWNERS services.

Move the requests to the CLI: send them during compilation and write the files
that the test binaries read, such as the manifest or read-cache entries, so no
binary waits on the network. The same files can carry each package's module
path and directory, which per-test coverage, LCOV reports and backfill
otherwise resolve with `go list` in each binary. Events must still upload
normally. Preserve response/error fallbacks for ITR, retries, EFD, impacted
tests and management, independent invocations and event hierarchy. Compile-only
`-c` must send no runtime requests. Standalone binaries, retry children and
interrupted parents need a safe fallback without a live CLI.

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
