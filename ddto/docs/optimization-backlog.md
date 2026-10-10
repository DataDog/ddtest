# Optimization backlog

Each item is a separate experiment and change. Within each table, the order
balances likely CPU or allocation savings, frequency and implementation risk.
Impact estimates are qualitative unless a measurement is cited; validate them
on the current revision before implementing. Use
[performance and profiling](performance.md) for measurement and ownership
rules, and [feature parity](ci-parity.md) for the behavior to preserve.

Items 1–10 come from earlier reviews. Items 11–53 come from a review of
`063c50a` on 2026-10-10, grouped by where the work happens. Unless an item
says otherwise, their figures were measured on darwin/arm64 (Apple M5 Pro,
Go 1.27.2) on a loaded host. Compare them with each other, not with the
Linux/amd64 figures of items 1–10, and measure again on Linux before keeping
a change. Line numbers refer to `b776593`. In items 11–53, `civisibility/`,
`hostname/`, `osinfo/` and `telemetry/` paths are relative to
`internal/thirdparty/dd-trace-go/`, and `gotesting/` stands for
`civisibility/integrations/gotesting/`. Items that change incorporated SDK
code also need an entry in
[`ADAPTATIONS.md`](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md).

## Summary

### Earlier items

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
binaries through the environment and files they already read. Items 42, 51 and
part of 53 are smaller steps in the same direction.

### CLI and build path

These costs occur once per `ddto` invocation or once per compiled package.

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 14 | Leave standard-library imports out of library discovery | A second `go list` in most invocations | Low |
| 15 | Overlap the Orchestrion tool lookup with preparation | Seconds of serial `go tool -n` in Orchestrion modes | Low to medium |
| 16 | Write and remove generated files concurrently | Serial file I/O proportional to package count | Low |
| 17 | Start the package query alongside `go env` | One serial subprocess per invocation | Medium |
| 18 | Parse fewer SDK sources, in parallel | Serial parsing when the SDK is reachable | Low |
| 19 | Walk the vendor tree once | Two serial stat passes per vendored file | Medium |

### Mini event client

These costs occur per event, batch or flush in `internal/minitracer`,
`internal/citransport`, `internal/cidelivery` and `propagation`.

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 21 | Skip the common-metadata override scan when nothing is overridden | Map work for every event at each flush | Low |
| 22 | Project common tags without cloning `Meta` | Allocations for SDK span copies and mixed batches | Medium |
| 23 | Charge the shared common snapshot once per batch | Requests per binary; payload buffer reuse | Medium to high |
| 24 | Store the event inside `Span` | One allocation per event | Medium |
| 25 | Keep gzip compressors across garbage collections | 814 KB and 80 µs per Agentless batch | Low |
| 26 | Remove the remaining producer-side costs | Clock reads, lock hold time and context allocations per event | Low |
| 27 | Encode straight into the payload buffer | A second copy of every payload byte | Low to medium |
| 28 | Drain the final backlog in parallel | Events dropped behind a slow intake at close | Low to medium |
| 29 | Reuse deferred-mode connections | TLS handshakes per checkpoint | Low |

### Per-test instrumentation

These costs occur per test, subtest, assertion or retry in `gotesting` and the
SDK's manual API objects.

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 32 | Scope retry subtest-name cleanup to the attempt | Binary-wide scans under `testing`'s matcher lock | Medium to high |
| 35 | Stop rewriting the instrumentation map on every `T.Run` | A global write lock per subtest | Low |
| 36 | Return the hook lease without allocating | One allocation per hook call | Low |
| 37 | Look up modules and suites before building options | 5–6 allocations and two exclusive locks per test | Low |
| 38 | Match test identity and management data by slicing | Splits and joins per subtest | Low |
| 39 | Parse selected sources concurrently at startup | Serial parsing before the first test | Medium |
| 40 | Resolve module and suite counters once | Four mutex-protected map updates per subtest | Low to medium |
| 41 | Stop the Testify parent walk when no suite is active; extends 3 | Lock-protected walks on every `T.Run` after any suite | Low |

### Test binary startup and shutdown

These costs occur in every test binary, so `ddto test ./...` pays them once per
package.

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 42 | Share base-branch discovery between binaries; extends 5 | Repeated base-branch queries and fetches per binary | Medium to high |
| 46 | Overlap telemetry round trips; extends 10 | One round trip at startup and one at exit | Medium |
| 48 | Let read-cache waiters wait for a slow owner; relates to 9 | Two seconds and a duplicate request per waiter | Medium |
| 49 | Stream read-cache expiry fields | Full JSON decoding before a cache miss's request | Low |
| 50 | Keep idle connections during deferred startup | TLS handshakes on the startup critical path | Low to medium |
| 51 | Store known tests per module in the read cache | Decoding and retaining the whole repository's tests | Medium to high |
| 53 | Remove small bootstrap costs | Log scans, CODEOWNERS serialization and build-info reads | Low |

### Completed

These items were implemented on 2026-10-10, together with the bugs found during
the review. The results are medians of alternating baseline and candidate runs
on the same loaded darwin/arm64 host, against local fixtures and receivers;
the item sections give the details.

| Item | Change | Measured result |
| --- | --- | --- |
| 11 | One `go list -deps` graph instead of per-package `Deps` | 2,000 packages: preparation 2.65 → 2.08 s, CPU 2.55 → 1.60 s, peak RSS 175 → 88 MB. Testify through a production helper: 106.8 → 87.0 ms, one `go list` less. 61 small packages: 92.1 → 98.9 ms |
| 12 | `go mod edit -json` only for workspace modules that name Mini | 80-module workspace: 80 → 0 commands, preparation 1.34 → 0.17 s |
| 13 | Runtime import in the internal test package | 61 packages, empty client cache: compiles 180 → 121, vets 120 → 61, CPU 52.9 → 51.2 s, wall 8.66 → 8.16 s |
| 20 | Trace and span IDs from `math/rand/v2` | Root ID 268 → 19.4 ns, 518 → 1.4 ns with 18 goroutines; child 82 → 13.6 ns. `BenchmarkEventLifecycle` at `GOMAXPROCS=1`: −12% without gzip, −5% with it |
| 26, first point | Unset hierarchy fields skip `ParseUint` | Text-only span metadata: 416 → 361 ns, 11 → 8 allocations |
| 29, second point | The runtime client fixes the delivery mode | An atomic load per test instead of an environment read |
| 30 | Per-test coverage compared by index, parsed without strings | 5,000 blocks: 4.99 → 1.62 ms, 81,140 → 365 allocations, 4.58 → 1.35 MB per covered test |
| 31 | Test metadata in a `sync.Map` | 10,200 parallel subtests ×10: 0.81–0.89 → 0.66–0.71 s; lifecycle with 18 goroutines 372 → 161 ns; serial unchanged, one 48-byte allocation more per test |
| 33 | Known tests indexed once for lists above 32 names | New test against 1,000 names: 0.93–0.97 µs → 41–48 ns; against 10,000: 7.5–8.8 µs → 28–29 ns |
| 34 | Stack traces only when they are kept | Repeated `Errorf` with 16 frames: 5.4 µs, 15.5 KB, 40 allocations → 27 ns, none |
| 42, first step | One exact `git ls-remote` for all missing base branches | GitHub over SSH with impacted tests: 6 → 1 network calls, test binary 4.60 → 1.49 s |
| 43 | No pull-request head fetch when the commit is present | Depth-one GitHub clone: bootstrap 807 → 186 ms, test binary 1.34 → 0.81 s |
| 44 | No backoff after the final HTTP attempt | Without an Agent: settings 1.508 → 0.706 s, whole `ddto test` 4.05 → 3.20 s |
| 45 | One final delivery at exit | Refused connection: shutdown after session close 609.8 → 305.1 ms; a black-holed intake saves up to the 10 s flush timeout (estimated) |
| 47 | macOS version from `sysctl`, OS metadata on first use | A process reading OS metadata: 29.4 → 16.2 ms |
| 52 | CI log hostname from `os.Hostname()` | No metadata probes, `hostname -f` process or goroutine; goleak no longer sees it |

Item 11 trades a few milliseconds in small modules, whose graph now lists their
standard-library dependencies, for seconds and hundreds of megabytes in large
ones. Item 13 recompiles internal test packages when Mini changes, because they
now import the runtime; edit rebuilds are unchanged.

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

## 11. One dependency graph instead of per-package `Deps`

**Status:** Done; see [Completed](#completed). The measurements below are from the review.

The main query in `internal/runner/run.go:276` requests `Imports` and `Deps`
for every selected package, so Go prints each package's whole transitive
closure. The front-end decodes and keeps it through preparation only to answer
whole-graph questions in `libraries.go:34` and `testify.go:123-138`: whether
`testify/suite`, goleak or the SDK's CI packages are reachable, and which test
imports are already known. `Imports` is requested but never read.

On dd-go `./...` (5,027 packages, 6.0 million `Deps` entries):

| Query | Wall | User CPU | `go list` RSS | JSON output |
| --- | --- | --- | --- | --- |
| Current fields | 20.2–20.7 s | 8.5–8.8 s | 906–925 MB | 298 MB |
| Without `Deps` and `Imports` | 17.3–18.1 s | 4.8–4.9 s | 470–492 MB | 4.4 MB |
| `-deps` with `Imports` and `DepOnly` | 17.1 s | 4.9 s | 534 MB | 18.5 MB, 12,633 records |

Decoding the current output took 960 ms, with 573 MB allocated and 418 MB
live; the `-deps` output took 86 ms and 38 MB. Small modules are neutral:
0.06–0.07 s for a 60-package fixture and 0.61–0.62 s for dd-trace-go's root
module with either query.

Run one `go list -e -deps` with `Imports` and `DepOnly` and drop `Deps`.
Records with `DepOnly` false are the selected packages. Walk `Imports` from the
client roots to compute reachability and the known set, skipping
runtime-only edges, and read suite, goleak and SDK metadata from the `DepOnly`
records. That also removes the `-find` query when those libraries are
production dependencies.

Preserve `commandLine`, which equals `!DepOnly` and drives gcflags matching;
package errors reported only for selected packages, `testing` and the runtime;
and the separate read-only runtime query under `-mod=mod`. Expect about 3 s of
wall time, 4.5 s of CPU and up to 0.8 GB of combined peak memory less for a
monorepo-wide run, and no change for small repositories.

## 12. Workspace modules that cannot replace Mini

**Status:** Done; see [Completed](#completed). Modules are queried concurrently, and the
results are applied in workspace order.

This extends item 4. When a workspace neither requires nor replaces Mini,
`internal/runner/workspace.go:325-370` copies every `use` module's file to
`client-N.mod` and parses it with its own `go mod edit -json` (`:345`),
serially, only to look for a replacement of Mini. In dd-trace-go's 83-module
`go.work`, 84 calls of 9–24 ms took 0.93 s of a 1.76 s preparation. None of
those files mentions Mini.

Run the command only when the module file, read through `readModuleFile` so
that overlays apply, contains Mini's module path or a backslash. That is the
literal-mention rule `preprovideMini` already applies (`provide.go:38`). Run
the remaining commands concurrently.

Preserve the conflicting-replacement error and overlays of `go.mod`. An
unparsable module file is then reported by the next Go command rather than by
this one.

## 13. Runtime import in the internal test package

**Status:** Done; see [Completed](#completed). The review of the initialization order
found no regression.

`internal/runner/run.go:438-455` writes `zz_dd_ci_visibility_test.go` as
`package <name>_test` for every test package. A package without external test
files then gains a whole external test package, which Go compiles and vets
separately. In a 60-package fixture, with a warm standard library and a cold
client cache, the build ran 178 compiler and 118 vet actions, against 119 and
59 with the file in the package itself. Over six alternating runs the median
saving was 3.8 s of user and system CPU, about 65 ms per package, and wall time
went from 7.2 s to 6.55 s. Rebuilds after an edit showed no difference: the
extra package imports only the runtime and stays cached. In dd-go, 2,885 of
2,971 test packages have no external tests.

When `XTestGoFiles` is empty, declare the file in the package under test. Keep
the external placement when the package has external tests or belongs to the
runtime's dependency closure, where an internal import would form a cycle.

Preserve `RegisterTestPackage` identity, which uses the caller's directory, the
result-cache fingerprint, and the absence of a clash with `__dd_ci_runtime` in
the package scope. Review the initialization order: the runtime then
initializes before the package under test, and registration runs as that
package's last `init`. The saving applies to cold or invalidated builds: CI
without a persistent `GOCACHE`, `-a`, and toolchain or Mini changes.

## 14. Standard-library imports in library discovery

Any test import missing from the selected packages' dependencies becomes a
request in `internal/runner/testify.go:139-155` and switches the second query
in `libraries.go:180-218` to `-deps`. That includes standard-library packages,
whose closure can never reach Testify, goleak or the SDK. A fixture whose tests
import only `net/http/httptest` resolves 197 standard packages in 51–81 ms;
preparation took 132–148 ms, against about 56 ms without the query. In an
83-module workspace any `go list` costs at least 0.24 s, and this query took
367 ms.

Leave standard-library paths out of the request, using Go's rule: the first
path element has no dot and `$GOROOT/src/<path>` exists, with `GOROOT` taken
from the parent of `testing`'s directory. External helpers and unknown imports
from other modules stay in the query. Merging the query into the main one with
`-test` is not a general improvement: dd-go took 23.7 s merged, against 17.1 s
plus 0.83 s with separate queries.

## 15. Orchestrion tool lookup in parallel

With `ddto go tool orchestrion go test`, or a `go tool orchestrion toolexec`
wrapper, `internal/runner/orchestrion.go:144-188`, called from `run.go:520`,
resolves the executable with `go tool -n orchestrion` after the overlay is
written. The lookup depends only on the directory, `-mod`, the module file and
the user's overlay. In dd-go it took 1.6–2.0 s warm and 20.7 s on the first
run; the main `go list` for one package took 0.47 s.

Start the lookup in a goroutine once the module file is settled after
`preprovideMini`, and restart it only when `provideRuntime` changes that file.
Do not overlap with `-mod=mod`, where both commands could write `go.mod`.
Preserve progress streaming and error precedence: report preparation errors
first, then cancel the lookup. The shorter of the two steps disappears from the
critical path, an estimated 0.5–2 s in a dd-go-sized module.

## 16. Generated files written and removed concurrently

The generated backing files are written serially in
`internal/runner/run.go:368-394` and `:438-455`, and the plan directory is
removed after `go test` exits (`:604-607`). With 600 packages on APFS, writing
took 59–64 ms and removal 36–51 ms. In a microbenchmark, eight parallel writers
were 1.4–1.7 times faster and parallel removal 1.2 times.

Write the per-package files concurrently with the library query, whose inputs
are independent, or with a small worker pool. Keep updates to the replacement
map on one goroutine, and preserve collision checks and content sharing. Linux
was not measured.

## 17. Package query alongside `go env`

`go env -json` (`internal/runner/environment.go:48`, called from `run.go:201`)
takes 9–26 ms and runs before anything else. Where the files alone decide the
flags — no `go.work`, a `go.mod` that mentions Mini, Go 1.25 or newer and no
`-mod=mod` — start the main `go list` at the same time. Then compare `GOMOD`
and `GOWORK` with the assumptions and repeat the query when they differ. A
mismatch must not leave a partially used result. Expect 10–25 ms per
invocation.

## 18. SDK source parsing

When the SDK's CI packages are reachable, which is true for any client that
imports the v2 tracer, the preflight in `internal/runner/sdk_ci.go:34-61`
parses all 60 tracer files (704 KB) serially in every invocation: 7.5–7.9 ms,
5.5 MB and 164,000 allocations per pass. The compiler wrapper (`:79-101`) parses
them again on a cache miss. `transformSDKCIGates`
(`internal/instrument/sdk_ci.go:32`) uses parser mode 0, with object
resolution: 1.19 ms for the config package, against 0.85 ms with
`SkipObjectResolution`. The Testify wrapper (`internal/runner/testify.go:182-205`)
parses every suite file again even when it substitutes none and the inputs are
the original module-cache paths.

Parse in parallel and filter on bytes first: `__ddto`, `SpanContext`,
`spanStart`, `finish` and `ContextWithSpan` leave 25 of the 60 files. Use
`SkipObjectResolution` for the gates and skip parsing compiler inputs outside
`$WORK` again. Expect 5–7 ms per invocation and about 1 ms per compilation.

## 19. Vendor tree walks

Every run stats each vendored file twice, serially: once for the workspace key
in `readVendorTree` (`internal/runner/vendor.go:579`) and once to validate the
cached workspace in `validVendorWorkspace` (`:657`).
[Performance and profiling](performance.md) reports about 80 ms per 20,000
files on Linux. Hashing is negligible: 1.2 ms per 5,900 entries, or 0.4 ms with
`strconv.Append` instead of `fmt.Fprintf`.

Walk top-level module directories in parallel, or add inode identity to the
key and validate only the directory structure and manifest. Expect tens of
milliseconds for large vendor trees; this was not measured end to end.

## 20. Trace and span IDs without `crypto/rand`

**Status:** Done; see [Completed](#completed).

A CPU profile of the CI-shaped `BenchmarkCommonMetadataLifecycle` at
`GOMAXPROCS=1`, about 3.6–4.8 µs per event including background delivery,
attributes 7.5% to `crypto/rand`, 8.8% to `eventSize`, 6.4% to
`prepareCommonMetadata`, about 8% to gzip and about 5% to MessagePack encoding.
Items 20–23 address those shares.

`propagation/context.go:34-64`, called from `internal/minitracer/span.go:392-394`,
reads 16 bytes from `crypto/rand` for every root event and 8 for every child.
Every test, suite, module and session event starts from a background context
and is therefore a root. A root took 270 ns and a child 83 ns. With 18
goroutines one operation took 490 ns, because the darwin system call
serializes; it was 36% of all CPU in a parallel lifecycle benchmark. On Linux,
Go uses the getrandom vDSO from kernel 6.11; older kernels pay a system call,
an estimated 150–400 ns.

Draw the IDs from `math/rand/v2`, a per-thread ChaCha8 generator seeded by the
operating system: 19 ns for a root, 15 ns for a child and about 1.5 ns per
operation in parallel. Preserve 63-bit span IDs, nonzero IDs, a root span ID
equal to the trace ID's low half, a child ID different from its parent's, and
the public signatures of `New` and `Child`.

## 21. Common-metadata override scan

At each flush, `prepareCommonMetadata` (`internal/minitracer/common_tags.go:111-118`)
walks every event's `Meta` and `Metrics` and deletes overridden keys from the
kind's defaults. It does so even when there are no defaults: in Bazel mode,
for `span` events and in batches without common tags. For 1,000 events it took
250 µs without common tags and 660 µs with them, 0.25–0.6 µs per event.
`eventSize` (`:66-86`) already performs the same lookups on the producer path.

Have `eventSize` record on the event, in a `msg:"-"` field, whether it
overrides a common key: unknown, none or some. Skip the scan when the kind has
no defaults or no event overrides one, and scan when the state is unknown. A
prototype measured 11 µs and 40 µs and passed the minitracer tests. Fusing
`Msgsize` with the override loop also walks `Meta` once: 610 to 400 ns per
`eventSize`, with noisy measurements.

## 22. Common tags without cloning `Meta`

A projected event (`internal/minitracer/common_tags.go:134-172`) receives
`maps.Clone(Meta)`, about 30 inserts and a heap copy of the event on every
encoding attempt, including retries. That applies to every SDK span copy,
since kind `span` never receives kind defaults (`sdk_mirror.go:72`), and to CI
events in batches with mixed snapshots or overrides. Encoding 1,000 events with
22 local and 30 common keys took 4.9–7.3 ms with projection and 0.62 ms without:
about 5 µs, 3.8 KB and 7 allocations per projected event.

Encode `meta` by hand: count the qualifying common keys, write a map header of
`len(Meta)` plus that count, write `Meta`, then the common values that are
neither overridden locally nor equal to the kind default. Carry those decisions
in a side slice instead of copying events. Preserve the decoded content, the
immutability of sealed events and the byte accounting. The benefit depends on
how many SDK spans tests create.

## 23. Shared snapshot charged once

Each event's accounted size includes the whole common snapshot
(`internal/minitracer/common_tags.go:66-70`, `batch.go:78` and `:321`), although
a homogeneous batch writes it once. With a snapshot of 40 values of 80 bytes,
batches sealed at 486.6 events instead of 1,000, about twice the requests,
while the real payload was about 280 KB against the 2.5 MiB threshold. A
GitHub-like environment without commit fields produced a 908-byte snapshot;
long commit messages or environments push typical CI past the event-count cap,
for an estimated 0–60% more requests. `payload.Grow(batch.bytes+envelope)` also
reserves up to 2.5 MiB for small payloads, and a buffer that grows past that
threshold is discarded and allocated again.

Keep two totals: today's worst case and a shared total that charges each
kind's snapshot once. Use the shared total while the open batch is safe to
project: one snapshot per kind and no overrides, which item 21's flag records.
Preserve the 5 MiB limit under any mix of events. [Delivery](delivery.md)
describes the current accounting as deliberately conservative; update it with
the change.

## 24. Event storage inside `Span`

`Finish` (`internal/minitracer/span.go:358-363`) copies the span's content into
a new 200-byte `ciEvent` in the 208-byte size class, and SDK copies in
`sdk_mirror.go` do the same. Embed the event in `Span`: setters write it, and
`Finish` finalizes it in place and enqueues its address. `Span` grows from 320
to 352 bytes, and each event saves one allocation and about 176 bytes. Content
is only accessed before `Finish`; the getters read maps that are already
shared. Update the ownership documentation and tests with the change.

## 25. Gzip compressors across garbage collections

Agentless compressors live in a `sync.Pool`
(`internal/citransport/compression.go:16-35`, used at `transport.go:177`),
which keeps an entry for at most two collections. Batches are about 1,000 tests
apart, so a real binary probably misses the pool on most batches; this is
estimated, because the benchmark mostly hits it. A new `BestSpeed` writer costs
814 KB, 15 allocations and about 80 µs, and a binary with one batch always pays
it. Keep one or two compressors for the client's lifetime in a transport-owned
free list, like `Client.buffers`.

## 26. Producer-side small costs

**Status:** The first point is done; the others remain.

Each of these is a separate small change in `internal/minitracer`:

- `span.go:364-366`: `strconv.ParseUint("")` allocates a `NumError`, 18 ns and
  48 bytes, for every unset hierarchy field: up to three per session, module or
  custom event. Check whether the field was set first.
- `span.go:348`: `Finish` reads `time.Now()` even when a `FinishTime` option
  supplies the time, as every CI close does. Read the clock only without it.
- `batch.go:75`: the telemetry submission, a clock read plus another mutex,
  runs inside `c.mu`. Moving it after the unlock shortens the critical section
  under parallel finishes.
- `span.go:421`: `propagation.WithContext` boxes a 64-byte value: two
  allocations, 112 bytes and 32 ns. Storing `&s.identity`, already on the heap,
  and accepting `*Context` in `FromContext` measured one allocation, 48 bytes
  and 15.5 ns.
- `span.go:417`: the `_dd.p.tid` hex string allocates 16 bytes per span, 15 ns.
  Child spans could reuse their root's.

## 27. Encoding into the payload buffer

`msgp.Encode` (`internal/minitracer/batch.go:323`,
`internal/thirdparty/msgp/msgp/write.go:156`) stages output through a pooled
2 KB writer and then copies it into the `bytes.Buffer`, so every payload byte is
copied twice. Generate `MarshalMsg` and append into `payload.AvailableBuffer()`,
which is already grown to an upper bound. Estimated saving: 10–15% of encoding,
about 20 ns per event.

## 28. Final drain in parallel

After `Close` sets `closed` (`internal/minitracer/batch.go:137`), background
senders exit and `drainLocked` (`:243-252`) sends the remaining batches one at a
time within one 10 s context. Only deferred mode uses parallel `drainWorker`s.
The runtime flushes before it stops, while senders still help, so this mainly
affects explicit clients and recovery after a failure backoff. With a slow
intake, the serial drain can exceed its budget and drop events. Use the
`drainWorker` fan-out in both modes.

## 29. Deferred-mode connections and mode checks

**Status:** The second point is done: the runtime client fixes the mode while it is
active. The first point remains.

- `internal/citransport/transport.go:153`: `CloseIdleAfterSend` closes
  connections after every send, so a drain worker that sends a second batch
  dials and negotiates TLS again. This matters for checkpoints with more than
  eight batches. Closing once per checkpoint still leaves no connection open
  while tests run.
- `internal/cidelivery/idle.go:15`: `Enabled()` reads the environment and
  parses a boolean for every test (`gotesting/testing.go:714`). The client fixes
  its mode when it starts; cache the value with it.

## 30. Per-test coverage processing

**Status:** Done; see [Completed](#completed).

This extends item 8 without depending on runtime coverage internals. For every
covered test, `gotesting/coverage/test_coverage.go:664-731` parses both text
profiles with `Text`, `Fields`, three `Split` calls and six `Atoi` calls per
line. The difference (`:734-798`) builds keys with `fmt.Sprintf` for every
block before and after (`:758`, `:764`). Each covered block resolves its
relative path again (`:738`, `:801-804`): the module-information mutex,
`ReplaceAll` and `GetRelativePathFromCITagsSourceRoot`. The file bitmap is
rebuilt with `FromLineCount` and `Or` whenever a block's end line grows
(`:740-747`), which happens for nearly every block because blocks arrive in
line order.

With 5,000 blocks, parsing took 1.0 ms, 1.66 MB and 25,000 allocations per
profile, twice per test, and the difference took 1.4–2.0 ms and 21,000–33,000
allocations: about 3.5–4 ms of CPU and 5 MB of garbage per covered test. The
cost grows linearly with blocks, so `-coverpkg=./...` in a large module reaches
tens of milliseconds per test.

Compare by index: both profiles come from the same binary and list blocks in
the same order. Check that files and positions agree, and fall back to a map
with a struct key, without `Sprintf`, when they do not. Parse bytes directly,
resolve each file's path once and size each bitmap from the file's largest end
line. Preserve the covered-line sets, the sorted file order and the test file
first. Only initial attempts are processed.

## 31. Test metadata without a global lock

**Status:** Done with `sync.Map`; a 64-shard map measured the same. Item 3 remains.

This extends item 3 with a smaller first step. Creating and deleting metadata
in `gotesting/instrumentation.go:250-256` and `:330-358` takes the write lock of
one global `RWMutex`, and while a writer waits, readers queue behind it; every
hook calls `getTestMetadata`. In a fully parallel synthetic run,
`createTestMetadata` and `deleteTestMetadata` caused 34–77% and 4–7% of all
mutex delay, more than `testing`'s own matcher lock.

Replacing only the three accessors with a `sync.Map` reduced 102,000 parallel
subtests from 0.82 to 0.63 s at the minimum, and from about 0.875 to 0.67 s at
the median of eight alternating runs: 20–23% less wall time. Moving the
allocation out of the lock alone changed nothing measurable. Use `sync.Map` or
a sharded map now; the fields proposed in item 3 can follow, with this map as
their fallback.

## 32. Retry name cleanup scoped to the attempt

From the second attempt on, `gotesting/retry_attempt.go:151-201` walks
`testing`'s binary-wide `matcher.subNames`, which holds every subtest name
created so far, to snapshot and delete the names below the test's prefix.
`restore`, called from `retire`, walks it again. Both run under `matcher.mu`,
which every `T.Run` in the binary also needs; `beginAttempt` is called from
`retry_attempt_runner.go:116`. A 10-attempt group, such as EFD on a fast new
test, took 0.15 ms with 1,000 names, 1.0 ms with 10,000 and 8.8 ms with 100,000,
blocking parallel `T.Run` calls throughout.

Scan once for the baseline, or not at all when names are tracked from the
first attempt. Later attempts delete only the names recorded through the
`T.Run` hook below the group's attempt root, including the deduplication keys
that `matcher.unique` adds. Preserve native subtest names, including `#NN`
suffixes, with `-count` above one, nested subtests and late parallel subtests.

## 33. Known tests as a set

**Status:** Done; see [Completed](#completed). Lists of up to 32 names keep the linear
scan.

`isKnownTest` (`gotesting/testing.go:1194-1208`) scans the suite's known list,
which includes subtest names, with `slices.Contains` (`:1200`). It runs for every
top-level test during `M.Run` setup, before any test starts and including tests
excluded by `-run`, from `testing.go:705` and `:1014`, and again from
`instrumentation.go:707` when EFD is enabled. A lookup took 0.7 µs with 1,000
names and 7.2–7.8 µs with 10,000, against about 6 ns for a map; 1,000 tests
against 10,000 known names cost about 15 ms per binary.

Build a set per module and suite once, and reuse the result between the call
sites. Preserve the `hasKnownData` result.

## 34. Stack traces only when they are kept

**Status:** Done; see [Completed](#completed).

`utils.GetStacktrace` is an argument of `CompareAndSwap` in
`gotesting/instrumentation_orchestrion.go:46-52`, so it runs on every `Error*`
and `Fatal*` call, even after the first error is stored. `instrumentSetErrorInfo`
(`:461-468`) builds a stack and discards it when a formatted error exists, and
process-retry children build two per `Errorf` (`gotesting/retry_process.go:3513-3520`).
A stack of about ten frames costs 5.4 µs, 12.5 KB and 32 allocations. Testify
assertions end in `Errorf`, and retries multiply the calls.

Check `Load() == nil` before building the stack, build it in
`instrumentSetErrorInfo` only without a formatted error, and optionally append
frames instead of calling `Fprintf` for each. The first error still wins.

## 35. Instrumentation-map writes on every `T.Run`

Each wrapper call runs `FuncForPC` on a closure whose code pointer never
changes, allocates an entry and takes the global write lock to store the same
key again (`gotesting/instrumentation.go:313-327`, written from
`instrumentation_orchestrion.go:436`, `instrumentation.go:629`, `:751`, `:992`
and `testing.go:873`); `T.Run` also takes the read lock to check it. A write
took 20 ns serially and 129 ns with 18 writers; a get plus set took 203 ns
against 42 ns for the get alone. After item 31, skipping the redundant writes
saved another 3–4% in the parallel run, from a 0.67 to a 0.64 s median.

Record the five closure entry points once in an immutable set and check
membership without a lock.

## 36. Hook lease without allocation

`return e.release` in `gotesting/testing.go:236-272` creates a method value that
escapes to the heap, as escape analysis reports for line 264: 16 bytes per hook
call, about one allocation per test. Every hook also updates one shared atomic
counter. A lease took 12.9 ns serially and 64.6 ns with 18 goroutines. Return the
epoch and call `release` explicitly; stripe the counter only if measurements
justify it.

## 37. Module, suite and source tags per test

`GetOrCreateModule` and `GetOrCreateSuite`
(`civisibility/integrations/manual_api_ddtestsession.go:232-258` and
`manual_api_ddtestmodule.go:145-167`) allocate an options structure and read the
clock before looking up the map, under exclusive mutexes that appear in the
parallel mutex profile. `SetTestFunc` (`manual_api_ddtest.go:240-242`,
`:297-298` and `:331`) converts the cached file and line values to interfaces on
every call, two or three allocations, sets the start line twice and writes the
source file and CODEOWNERS tags on the shared suite for every test.
Finalization converts the status string to an interface
(`gotesting/test_execution_finalizer.go:102`).

Look up first and build options only on a miss, or cache the module and suite
on `testingTInfo`, whose entries are never deleted. Store already converted tag
values in the cached source metadata, skip suite tags whose value has not
changed and use constant interface values for statuses. Expect 5–6
allocations and 100–200 ns less per test, and less contention.

## 38. Test identity and test-management matching

Every `T.Run` builds a test identity with `strings.Split`
(`gotesting/testing.go:157-171`): 65 ns and two allocations. With test
management and subtest features enabled, the default, each subtest calls
`matchTestManagementData` twice (`instrumentation_orchestrion.go:218` and
`:245`, `testing.go:1239`, `instrumentation.go:461`), and each call builds
ancestor names with `strings.Join`: 117 ns and five allocations at depth three.
Detect subtests with `IndexByte`, build segments lazily, match by slicing
`FullName` at its `/` positions and reuse the first match.

## 39. Source parsing at startup

Parsed sources are cached; a cached lookup takes 17 ns
(`civisibility/integrations/manual_api_sourcecache.go:264-317`). With EFD and
impacted tests, however, `IsTestFuncModified` (`gotesting/instrumentation.go:705-711`)
parses the file of every known test during `M.Run` setup, including tests
excluded by `-run`, which CI test splitting relies on. Otherwise the first test
of each file pays for its parse: 1.08 ms for 2,300 lines and 4.5 ms for 8,300.
Prefetch the files of selected tests with a bounded worker pool, or restrict
the modified-test check to selected tests.

## 40. Module and suite counters

Each subtest increments module and suite counters
(`gotesting/instrumentation_orchestrion.go:272-273`) with four map operations
under a global mutex (`gotesting/testing.go:1163-1191`): 91 ns serially and
583 ns with 18 goroutines in a microbenchmark. Atomic counters showed no
wall-time change beyond noise in the parallel synthetic run. Resolve a
per-name `*atomic.Int64` once, and keep the change only if it shows a
wall-time effect.

## 41. Testify parent walk

This extends item 3. Once any Testify suite registers, a flag set at
`gotesting/testify.go:230` and `:282` is never cleared, so every later `T.Run` in
the binary walks the whole parent chain with two read locks per level
(`:52-80`, `:225-243`). Each subtest under a suite method also takes a write
lock to bind itself and registers a cleanup. Count active scopes and bindings
instead, and skip the walk when the count is zero. Not measured.

The first execution of a test eligible for automatic retries always runs in a
fresh retry attempt: two cloned `testing.T` values and a goroutine, 4.5–7 µs,
38 allocations and 3.1 KB more than a plain subtest. The SDK does the same. The
only narrow saving would bypass the attempt when no retry can be admitted,
because the global budget is exhausted, but the path is chosen at `M.Run`
before the budget is used. It is not listed as an item.

## 42. Base-branch discovery

**Status:** The batched `git ls-remote` is done; see [Completed](#completed). Sharing the
base branch and diff between binaries remains.

Against a fake Agent that answered every request after 100 ms, a binary served
by the read cache spent its whole 102 ms settings phase waiting for
`app-started`. The cache owner made two round trips at startup, settings
together with `app-started` and then known tests. Every binary made three
serial round trips at exit: the test cycle, then two telemetry
`message-batch` requests. Bootstrap took 37–95 ms, almost all of it four Git
commands of 8–25 ms. Items 42–53 use that timeline as their baseline.

This extends item 5. With impacted-test detection enabled and no
`GitPrBaseCommit`, every binary computes the base branch
(`civisibility/utils/git.go:914-921`, `civisibility/utils/impactedtests/impacted_tests.go:116-127`,
`civisibility/integrations/civisibility_features.go:459-472`). That covers
everything except GitLab merge-request pipelines and an explicit
`DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA`. Without a pull-request base branch, as
in push builds and local runs, each of the seven `possibleBaseBranches`
(`git.go:68`) gets a `show-ref`. Each branch missing locally then gets a
`git ls-remote --heads`, a network call, and possibly a `fetch --depth 1`
(`checkAndFetchBranch`, `:1010-1029`). Branches that do not exist, such as
`preprod`, `prod` or `dev`, are queried again in every binary of every run.
GitHub pull requests check and fetch their base branch in every binary of the
first wave. The additional-features wait group waits for all of it before the
first test.

`git ls-remote` to GitHub over SSH took 0.54–0.60 s, so six missing branches
cost about 3.3 s per binary; over HTTPS in CI an estimated 0.6–1.8 s. With
impacted tests, settings initialization also waits up to 30 s for the
repository upload at startup (`civisibility_features.go:274-277`), not only at
exit, which makes item 5 more valuable.

Run one `git ls-remote --heads <remote>` for all missing candidates, match
`refs/heads/<name>` exactly, and fetch only branches that exist. Preserve the
candidate order, `findBestBranch` tie-breaking and the fetch side effects. A
later step can compute the base SHA and diff once per invocation, in the CLI
or in a file keyed by repository and HEAD, through a private channel:
`DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA` would add a tag to events.

## 43. Pull-request head commit

**Status:** Done; see [Completed](#completed). Fetching once in the CLI remains part of
item 6.

This extends item 6. When a head commit is known and the repository is shallow
(`civisibility/utils/environmentTags.go:350-364`, `civisibility/utils/git.go:377-428`),
every binary runs these commands before settings: `rev-parse
--is-shallow-repository`, `git --version`, `rev-parse @{upstream}`, which fails
on a detached HEAD, `git remote`, `git fetch --update-shallow … <sha>` over the
network, and `git show`. The head commit comes from GitHub's
`pull_request.head.sha` (`ci_providers.go:727`) or GitLab's
`CI_MERGE_REQUEST_SOURCE_BRANCH_SHA` (`:779`), so this covers `actions/checkout`
at depth one on `pull_request` events and GitLab merged-results pipelines.
Nothing checks whether the commit is already present, and the repository stays
shallow after the first fetch, so every binary fetches again.

Skip the fetch when `git cat-file -e <sha>^{commit}` succeeds. That saves one
network fetch, an estimated 0.2–1 s, and four or five subprocesses of 7–9 ms
per binary, with the same tags. Fetching once in the CLI can follow with item 6.
Parallel binaries currently run `git fetch --update-shallow` on the same
repository; `gitCommandMutex` (`git.go:61`) only serializes commands within
one process, so contention on `.git/shallow.lock` could make a fetch fail and
drop head-commit tags. Skipping fetches makes that less likely; it was not
reproduced.

## 44. No backoff after the final HTTP attempt

**Status:** Done; see [Completed](#completed).

`civisibility/utils/net/http.go:209-216` retries a request up to `MaxRetries`
times, and every failure path in `internalSendRequest` sleeps with
`exponentialBackoff` (`:323`, `:355`, `:363`, `:370`, `:381`, `:403`), also after
the final attempt, before the loop returns `max retries exceeded`. Without an
Agent, settings initialization took 1.512 s: 100, 200, 400 and 800 ms of
backoff, the last 800 ms after the last attempt. This applies to settings,
known tests, skippable tests, test management, commit search, packfile,
coverage and log requests.

Return without sleeping after the final attempt; the number of attempts is
unchanged. That saves about 0.8 s per binary whenever the Agent is unreachable
or the backend answers 5xx or 429, which is common for local runs without an
Agent: a trivial package took 4.1 s. The same fix applies upstream.

## 45. Redundant flush before stop

**Status:** Done; see [Completed](#completed). Stopping retries after a network failure
at session close remains optional.

Session close already flushes (`civisibility/integrations/manual_api_ddtestsession.go:228`),
and exit then calls `tracer.Flush()` and `tracer.Stop()`
(`civisibility/integrations/civisibility.go:387-388`), whose stop flushes again
(`internal/minitracer/runtime.go:77-116`). On the success path these calls cost
2–64 µs. With a refused connection, each starts its own retry cycle of about
303 ms for the same payload, 0.91 s in total, and each is bounded by the 10 s
flush timeout, so a black-holed intake can cost about 30 s (estimated).

Drop the middle `Flush`, and consider stopping retries once the session-close
flush has failed with a network error. That saves 303 ms with a refused
connection and up to 10 s with a black hole, and delivers the same events.

## 46. Telemetry round trips

This extends item 10. A binary served by the read cache spends its settings
phase waiting for `app-started` (`civisibility/utils/net/client.go:226-255`,
`civisibility/integrations/civisibility_features.go:124-127`): 102 ms with a
100 ms round trip. At exit (`telemetry/globalclient.go:55-82`,
`civisibility/integrations/civisibility.go:392`) it makes three serial round
trips: `telemetry stop` took 216.8 ms after a 101 ms test-cycle flush.

- Start telemetry before the Git phase of `createCITagsMap` when the service
  name does not depend on Git, because `DD_SERVICE` is set or the CI provider
  supplies the repository URL. `app-started` then overlaps the 37–95 ms
  bootstrap: an estimated 10–90 ms less.
- Send the retained startup `message-batch` concurrently with the session-close
  flush, and the closing batch afterwards: one round trip less at exit.

Keep `app-started` in its own request and keep the payload order.

## 47. macOS version without `sw_vers`

**Status:** Done; see [Completed](#completed).

`osinfo/osinfo_unix.go:30-38` runs `sw_vers -productVersion` in `init` on
macOS, in every test binary, retry child and binary whose CI initialization
never runs: 12 ms median over 20 runs. `syscall.Sysctl("kern.osproductversion")`
returned the same `26.6.2`. Read the sysctl, keep `sw_vers` as the fallback and
resolve the value on first use. `SYSTEM_VERSION_COMPAT` can make the two
values differ, so keep `sw_vers` when it is set.

## 48. Read-cache waiters

A binary waiting for another binary's request
(`civisibility/utils/net/read_cache.go:58-63`, `:349-358`, `:646-675`) polls
after 20, 40, 80 and then every 100 ms. After 2 s it sends the request itself,
without the lock, and never writes the result. When the owner takes longer, as
with large paginated known-test or skippable responses or a slow backend,
every binary of the first wave pays 2 s plus a duplicate request.

Wait while the lock's process is alive and the lock is younger than the
request bound, try to acquire the lock again before falling back, and cap the
poll interval at 10–20 ms: a poll is one stat and a failed open. Expect 0–100 ms
less per wait normally and 2 s less per waiter when the owner is slow. The
cache format is shared with dd-trace-go binaries, so keep it compatible. This
relates to item 9, which would remove the waits.

## 49. Read-cache expiry fields

Before a cache miss sends its request (`civisibility/utils/net/read_cache.go:331`),
cleanup (`:739-791`) reads and unmarshals each JSON entry, up to 32, only to
obtain two leading fields. Its 5 ms budget is checked between files, so one
large file can exceed it. A 3.2 MB known-tests entry for 100,000 tests took
4.2 ms with a complete `json.Unmarshal` and 17 µs with a streaming decoder that
stops after `created_at_unix_nano` and `ttl_seconds`, which are serialized
first. Use the streaming decoder, or clean up after `live()` returns. Expect an
estimated 5–15 ms on CI hardware per cache miss and endpoint; waiters wait for
it too.

## 50. Idle connections during deferred startup

With `DD_CIVISIBILITY_DEFERRED_DELIVERY=true`, every `SendRequest` closes the
shared connection pool (`civisibility/utils/net/http.go:165-169`), which
telemetry also uses (`civisibility/utils/net/client.go:240-242`). Settings,
commit search, packfile and the three parallel feature requests each dial
again, with another TLS handshake in Agentless mode, and telemetry loses its
pooled connection. Close idle connections once at test admission and at
checkpoints, as `PrepareLeakCheck` already does for goleak, and keep no idle
connection while tests run. Expect an estimated one to four handshakes, of
50–150 ms each, less on the startup critical path.

## 51. Known tests per module

The known-tests request covers the whole repository
(`civisibility/utils/net/known_tests_api.go:87-176`), and every binary that hits
the read cache (`read_cache.go:486-505`) decodes and keeps all of it, although
it uses only its own module (`gotesting/testing.go:1195`). The 3.2 MB entry
took 8.4 ms to decode, and its heap stays alive for the whole run.

Have the owner write a per-module index and a precomputed total, which
`countUniqueKnownTopLevelTests` still needs, so that cache hits decode only
their module. Expect an estimated 10–25 ms of CPU and several megabytes of heap
less per binary in a repository with 100,000 tests, and nothing measurable in
small repositories. SDK binaries share the cache directory, so the index needs
its own key or version.

## 52. Hostname for CI logs

**Status:** Done; see [Completed](#completed).

With CI logs enabled, `logs.Initialize`
(`civisibility/integrations/logs/logs.go:87`) calls `hostname.Get()`. The first
call returns an empty string because its cache is empty, and starts
`updateHostname` (`hostname/providers.go:121-140`), which probes the GCE (1 s),
Azure (300 ms) and EC2 metadata endpoints and runs `/bin/hostname -f`. The code
then falls back to `os.Hostname()`, and nothing reads the probe's result. The
goroutine lives up to 2–3 s, its transports are never closed, and the goleak
shim's filters (`internal/instrument/goleak.go:46`) do not cover it, so
`VerifyNone` can report it as a leak. Call `os.Hostname()` directly; the output
is identical.

## 53. Small bootstrap costs

- GitHub Actions job ID: each binary scans the runner's `Worker_*.log` files
  (`civisibility/utils/ci_providers.go:65-79`, `:130-169`): up to 10 MB read, a
  JSON attempt, a regular expression and a sort that stats files inside its
  comparator. The CLI could resolve the ID once and export `JOB_CHECK_RUN_ID`,
  which already takes precedence. Estimated 1–5 ms per binary.
- CODEOWNERS: parsing costs 1.5 ms per 2,000 rules, and `newOwnership`'s
  `json.Marshal` is about 28% of that CPU; compute it on first use. The preload
  at `civisibility/integrations/civisibility.go:124` could overlap the settings
  round trip when the service name does not come from CODEOWNERS.
- Build information: `ResolveSourceFilePathFromCITags`
  (`civisibility/utils/source_paths.go:34-35`) calls `debug.ReadBuildInfo()` for
  every distinct test function, even for absolute paths: 14 µs with 200
  dependencies. Memoize it with `sync.OnceValue`.

## Checked without a change

The 2026-10-10 review also checked these costs and found nothing worth changing:

- Package initialization on the tool path takes about 0.2 ms.
- Running a tool through `tool-overlay` costs about 3.5 ms more than
  `/usr/bin/true` on macOS, about 0.7 s of CPU per 240 tool calls in an edit
  build. That cost comes with `-toolexec`, and overlays cannot replace
  module-cache sources.
- The plan JSON is decoded only for suite, goleak and covered `testing`
  compilations.
- Marshaling and writing the overlay take 0.2–0.36 ms; the `testing`
  transformation takes 2.5–3 ms.
- `matchImportPattern` compiles a regular expression on each call, but runs
  only a few times per invocation.

## Experiment record

Keep the baseline and candidate SHAs, inputs, commands, raw repetitions and
limits in a task-scoped artifact directory. Alternate execution order and
retain slow or failed samples. Record CPU, allocations, complete wall time,
wire bytes and peak memory when they apply. A hot function's profile share is
not a predicted percentage saving.

Two existing benchmarks need care:

- `BenchmarkEventLifecycle` at the default `GOMAXPROCS` shows gzip enabled and
  disabled at nearly the same speed, 1.5 and 1.6 µs, because delivery runs on
  other cores. At `GOMAXPROCS=1`, the gzip case takes 4.3 µs. Report CPU time,
  or run it with `GOMAXPROCS=1`.
- `BenchmarkCommonMetadataLifecycle` calls `utils.GetCITags()`, which opts into
  the mutable tag path. `maps.Equal` then takes 13.6% of its CPU, although the
  runtime never calls it. The benchmark also leaves hierarchy fields unset, so
  three of its 22 allocations per operation are `ParseUint` errors (item 26).

Before keeping a change, run the affected contract tests and the SDK comparison.
Document runtime adaptations beside the incorporated SDK code so an upstream
update can preserve their ownership rules. Compiled-code refactoring is outside
this queue until its scope is agreed.
