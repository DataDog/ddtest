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
a change. Line numbers refer to `b776593`.

Items 54–58 come from a review of `2d0059a` on 2026-10-11, measured on the
[benchmark](benchmarks.md) host: Linux/amd64, AMD Ryzen 9 5950X with 32
threads, Go 1.27.0, against a local fake Agent, with medians of alternating
runs. Their line numbers refer to `2d0059a`.

Implemented items are removed from this list; the pull requests that
implemented them record their results, for example
[#25](https://github.com/tonyredondo/dd-ci-testing-poc/pull/25),
[#29](https://github.com/tonyredondo/dd-ci-testing-poc/pull/29),
[#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31) and
[#32](https://github.com/tonyredondo/dd-ci-testing-poc/pull/32). Item numbers
do not change, so a missing number is a completed item.

In items 11–58, `civisibility/`, `hostname/`, `osinfo/` and `telemetry/` paths
are relative to `internal/thirdparty/dd-trace-go/`, and `gotesting/` stands for
`civisibility/integrations/gotesting/`. Items that change incorporated SDK code
also need an entry in
[`ADAPTATIONS.md`](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md).

## Summary

### Earlier items

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 1 | Represent span options as values | CPU and allocations on every event; clearer metadata-capacity estimates | Medium |
| 2 | Cache common numeric CI options | Repeated option construction per span | Low to medium |
| 3 | Keep per-test state in `testing`'s structures | Map, lock and offset work on every test; field copies on retries | Medium to high |
| 4 | Reduce remaining provisioning commands | Startup of CLI invocations that must provide the runtime | Low only if existing metadata is sufficient |
| 7 | Store small metric sets without a map | Allocations for metric-bearing events | Medium; measure lookup/encoding tradeoffs |
| 8 | Collect coverage counters in memory | Profile I/O and parsing per covered test | High |
| 9 | Fetch settings in the CLI | The first request on the critical path; requests outside the read cache | High |
| 10 | Reduce telemetry requests at exit | A round trip at each test process's exit | Medium |

Item 9 moves work that every test binary repeats into the CLI, which runs once
per invocation. The CLI does the work and hands the results to the binaries
through the environment and files they already read. [#32](https://github.com/tonyredondo/dd-ci-testing-poc/pull/32) did this
for the Git metadata, the repository upload and the base-branch discovery,
through a Git worker that the CLI starts once, and
[#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31) for the GitHub
Actions job ID. Item 51 is a smaller step in the same direction.

### CLI and build path

These costs occur once per `ddto` invocation or once per compiled package.

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 14 | Leave standard-library imports out of library discovery | A second `go list` in most invocations | Low |
| 56 | Reuse the preparation plan between unchanged invocations | About 100 ms and 330 ms of CPU per cached Gin build | Medium to high |
| 15 | Overlap the Orchestrion tool lookup with preparation | Seconds of serial `go tool -n` in Orchestrion modes | Low to medium |
| 16 | Write and remove generated files concurrently | Serial file I/O proportional to package count | Low |
| 17 | Start the package query alongside `go env` | One serial subprocess per invocation | Medium |
| 18 | Parse fewer SDK sources, in parallel | Serial parsing when the SDK is reachable | Low |
| 19 | Walk the vendor tree once | Two serial stat passes per vendored file | Medium |
| 57 | Deliver events through the CLI; extends 9 and 10 | Up to about 100 ms of link and 5.6 MB per test binary that does not use HTTP or TLS itself | High |

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
| 55 | Parse test sources once, with a persistent cache; extends 39 | About 7.5 ms per run of Gin's root package, after the first run | Medium; needs a cache location |
| 40 | Resolve module and suite counters once | Four mutex-protected map updates per subtest | Low to medium |
| 41 | Stop the Testify parent walk when no suite is active; extends 3 | Lock-protected walks on every `T.Run` after any suite | Low |
| 58 | Drop the per-test `t.Helper()` in the wrapper | 0.7 µs and 185 bytes per test | Low |

### Test binary startup and shutdown

These costs occur in every test binary, so `ddto test ./...` pays them once per
package.

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 46 | Overlap telemetry round trips; extends 10 | One round trip at startup and one at exit for a binary that reports its own application | Medium |
| 48 | Let read-cache waiters wait for a slow owner; relates to 9 | Two seconds and a duplicate request per waiter | Medium |
| 49 | Stream read-cache expiry fields | Full JSON decoding before a cache miss's request | Low |
| 50 | Keep idle connections during deferred startup | TLS handshakes on the startup critical path | Low to medium |
| 51 | Store known tests per module in the read cache | Decoding and retaining the whole repository's tests | Medium to high |

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

Execution metadata lives in a `sync.Map` keyed by the testing object
(`gotesting/instrumentation.go`), which replaced a map behind one `RWMutex`.
Coverage shutdown markers and two Testify lookups use more maps keyed by tests.
Private `testing` fields are read through offsets computed by reflection
(`reflection_offsets.go`), and retries copy them through the same offsets
(`retry_attempt.go:231-234`). Measured on
Linux/amd64 at `44c7fd8`, before the `sync.Map`: a metadata lookup took 12 ns
alone and 28 ns with eight goroutines; creating and deleting an entry took
215 ns and one allocation. Reading the private fields through offsets takes
68 ns and 112 bytes per call. The reflection fallback,
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
scope repeat, such as packages with different CODEOWNERS services. ddto's Git
worker ([#32](https://github.com/tonyredondo/dd-ci-testing-poc/pull/32)) already requests the settings once per invocation, while it
uploads, to decide whether to compute the impacted-tests state; in CI the read
cache shares that response with the binaries, but locally its scope is the
worker's parent process.

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

Under `ddto test`, ddto's Git worker owns one telemetry application per
invocation ([#33](https://github.com/tonyredondo/dd-ci-testing-poc/pull/33)). Test processes that use its state send no
`app-started` and one request at exit; the worker sends `app-started` and,
after `go test` exits, `app-closing`. What remains:

- A test binary that reports its own application, run alone or without a
  handoff, still makes two final `message-batch` sends: investigate whether
  compatible messages can share a request while preserving order, timestamps,
  retries and `app-closing`. Keep `app-started` separate under its existing
  mapper contract.
- Under `ddto test`, each test process still sends its telemetry at exit,
  after its test cycle, and the worker's `app-closing` follows the last one.
  Handing that data to the worker, which would send it with `app-closing`,
  would remove a round trip from each test process's exit. Startup and
  shutdown must respect deferred admission and goleak checkpoints, including
  errors and cancellation.

Use decoded telemetry and actual HTTP captures to check values and logical
payload/attempt counts. Network waits need delayed-receiver experiments;
CPU microbenchmarks cannot establish that saving.

## 14. Standard-library imports in library discovery

Any test import missing from the selected packages' dependencies becomes a
request in `internal/runner/testify.go:139-155` and switches the second query
in `libraries.go:46-75` to `-deps`. That includes standard-library packages,
whose closure can never reach Testify, goleak or the SDK. A fixture whose tests
import only `net/http/httptest` resolves 197 standard packages in 51–81 ms;
preparation took 132–148 ms, against about 56 ms without the query. In an
83-module workspace any `go list` costs at least 0.24 s, and this query took
367 ms.

Leave standard-library paths out of the request, using Go's rule: the first
path element has no dot and `$GOROOT/src/<path>` exists, where `$GOROOT/src` is
the parent of `testing`'s directory. External helpers and unknown imports
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

## 21. Common-metadata override scan

A CPU profile of the CI-shaped `BenchmarkCommonMetadataLifecycle` at
`GOMAXPROCS=1`, about 3.6–4.8 µs per event including background delivery,
attributed 8.8% to `eventSize`, 6.4% to `prepareCommonMetadata`, about 8% to
gzip and about 5% to MessagePack encoding; its 7.5% for `crypto/rand` is gone
since IDs come from `math/rand/v2`. Items 21–23 address those shares.

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

Each of these is a separate small change in `internal/minitracer`:

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

After `Close` sets `closed` (`internal/minitracer/batch.go:422`), background
senders exit (`:137`) and `drainLocked` (`:243-252`) sends the remaining batches one at a
time within one 10 s context. Only deferred mode uses parallel `drainWorker`s.
The runtime flushes before it stops, while senders still help, so this mainly
affects explicit clients and recovery after a failure backoff. With a slow
intake, the serial drain can exceed its budget and drop events. Use the
`drainWorker` fan-out in both modes.

With a refused connection, the session-close flush and the final delivery at
exit each spend a retry cycle of about 0.3 s on the same payload. Once the
session-close flush has failed with a network error, the final delivery could
skip its retries; a black-holed intake would then also wait for one flush
timeout instead of two.

## 29. Deferred-mode connections and mode checks

- `internal/citransport/transport.go:153`: `CloseIdleAfterSend` closes
  connections after every send, so a drain worker that sends a second batch
  dials and negotiates TLS again. This matters for checkpoints with more than
  eight batches. Closing once per checkpoint still leaves no connection open
  while tests run.

## 32. Retry name cleanup scoped to the attempt

From the second attempt on, `gotesting/retry_attempt.go:151-201` walks
`testing`'s binary-wide `matcher.subNames`, which holds every subtest name
created so far, to snapshot and delete the names below the test's prefix.
`restore`, called from `retire`, walks it again. Both run under `matcher.mu`,
which every `T.Run` in the binary also needs; `beginAttempt` is called from
`retry_attempt_runner.go:115`. A 10-attempt group, such as EFD on a fast new
test, took 0.15 ms with 1,000 names, 1.0 ms with 10,000 and 8.8 ms with 100,000,
blocking parallel `T.Run` calls throughout.

Scan once for the baseline, or not at all when names are tracked from the
first attempt. Later attempts delete only the names recorded through the
`T.Run` hook below the group's attempt root, including the deduplication keys
that `matcher.unique` adds. Preserve native subtest names, including `#NN`
suffixes, with `-count` above one, nested subtests and late parallel subtests.

## 35. Instrumentation-map writes on every `T.Run`

Each wrapper call runs `FuncForPC` on a closure whose code pointer never
changes, allocates an entry and takes the global write lock to store the same
key again (`gotesting/instrumentation.go:313-327`, written from
`instrumentation_orchestrion.go:436`, `instrumentation.go:629`, `:751`, `:992`
and `testing.go:873`); `T.Run` also takes the read lock to check it. A write
took 20 ns serially and 129 ns with 18 writers; a get plus set took 203 ns
against 42 ns for the get alone. With test metadata in a `sync.Map`, skipping
the redundant writes saved another 3–4% in the parallel run, from a 0.67 to a
0.64 s median.

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
(`civisibility/integrations/manual_api_sourcecache.go:134-141` and
`:204-210`). With EFD and
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

## 46. Telemetry round trips

Against a fake Agent that answered every request after 100 ms, a binary served
by the read cache spent its whole 102 ms settings phase waiting for
`app-started`. The cache owner made two round trips at startup, settings
together with `app-started` and then known tests. Every binary made three
serial round trips at exit: the test cycle, then two telemetry
`message-batch` requests. Bootstrap took 37–95 ms, almost all of it four Git
commands of 8–25 ms, which [#32](https://github.com/tonyredondo/dd-ci-testing-poc/pull/32) removed from the test processes that
ddto starts. Items 46–53 use that timeline as their baseline. [#33](https://github.com/tonyredondo/dd-ci-testing-poc/pull/33)
removed, for the test processes that use the worker's state, the wait for
`app-started` and one of the two telemetry requests at exit; the suggestions
below remain for binaries that report their own application.

This extends item 10. A binary served by the read cache spends its settings
phase waiting for `app-started` (`civisibility/utils/net/client.go:226-255`,
`civisibility/integrations/civisibility_features.go:124-127`): 102 ms with a
100 ms round trip. At exit (`telemetry/globalclient.go:132-146`,
`civisibility/integrations/civisibility.go:392`) it makes three serial round
trips: `telemetry stop` took 216.8 ms after a 101 ms test-cycle flush.

- Start telemetry before the Git phase of `createCITagsMap` when the service
  name does not depend on Git, because `DD_SERVICE` is set or the CI provider
  supplies the repository URL. `app-started` then overlaps the 37–95 ms
  bootstrap: an estimated 10–90 ms less.
- Send the retained startup `message-batch` concurrently with the session-close
  flush, and the closing batch afterwards: one round trip less at exit.

Keep `app-started` in its own request and keep the payload order.

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

## 55. Test source locations from the CLI

This extends item 39. The first run of each test function resolves its source
file and line range: `SetTestFunc` calls `loadSourceFunctionMetadata`, which
parses the function's file with `go/parser` once per process. In Gin's root
package, 21 test files and 10,700 lines, the parse took about 7.5 ms of CPU per
run, on the first test of each file, and the rest of the resolution about
2.5 ms. [#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31) removed the build-information read of every distinct
test, about 2 µs per test. The rest costs about 1–1.5 µs per test, much of it
stack growth that a test goroutine pays at its first deep call anyway.

The CLI could parse each test package's `TestGoFiles` and `XTestGoFiles` and
write a table of its test functions into the generated file. Without a
persistent cache, this only moves work: each test file is parsed once per run
either way, by its own package's binary, and the CLI would also parse the files
of every package whose result Go takes from its test cache. An incremental run
of a large repository would parse all of its test files to run a few packages.
A cache keyed by each file's path, size and modification time removes that
cost after the first run, but an ephemeral CI runner starts with it empty unless
it is saved with the Go build cache: choose where it lives before implementing.
The runtime keeps the parser for misses, such as methods of suites defined
outside test files and binaries built without the table, so its link cost of
about 7 ms per binary stays. Keep the source-root, CODEOWNERS and source-range
tags identical, including for generics, `//line` directives and build-tagged
files. The table must also carry the `//dd:test.unskippable` and
`//dd:suite.unskippable` directives, which make ITR run a test that the backend
lists as skippable.

## 56. Preparation plan reused between invocations

With an unchanged module, preparation repeats the same work. In Gin, 7
packages, `ddto test -c ./...` with a warm build cache took 205 ms against
93 ms natively, and 981 against 496 ms of CPU. Preparation took about 100 ms:
the package query 53 ms (189 ms of CPU), the library query 29 ms (136 ms of
CPU), and `go env`, the runtime probe and two `go mod edit` calls the rest.

Cache the plan in the user cache: overlay entries, generated files, tool
selection and library decisions. Key it on everything the queries read: the Go
environment and version, `go.mod`, `go.sum`, `go.work` and any `-modfile`, the
flags and build tags, the user's overlay, and the file names, build constraints
and import declarations of every Go file in the main modules and local
replacements, ddto's version, and the contents of every source that preparation
rewrites or inspects, such as `testing`, Testify's `suite` package and goleak.
A local Testify replacement whose `suite.Run` body changes keeps its file names
and imports, so without its contents the key would reuse the old rewritten
copy. Reading those files costs far less than parsing them, but measure it on
large modules. A miss runs today's path. An input missing from the key would
reuse a wrong plan, so validate the key with a debug mode that also runs the
queries and compares their results. Item 4 rules out a persistent cache for
provisioning commands; this cache needs its own decision.

## 57. Events through the CLI

This extends items 9 and 10. Every test binary links Mini's HTTP and TLS
delivery. A one-test binary grows from 4.8 to 16.7 MB, and its link from 106 to
289 ms, medians of 12. A probe binary that references only
`http.DefaultClient.Do` and `tls.Client` links in 210 ms and is 5.6 MB larger,
so the HTTP and TLS stack accounts for about 104 ms of the 183 ms Mini adds per
link. `encoding/json`, `go/parser`, and `compress/gzip` with `log/slog` add
7–12 ms each. These figures come from an empty fixture and a standalone probe:
a binary whose own code already uses HTTP or TLS, such as tests with
`httptest.NewTLSServer`, keeps that stack, so measure the marginal saving on
representative binaries. Every edit rebuild and every cold build links each
affected test binary again: in a cold Gin build, the six links took 2.24 s of
CPU, against 1.78 s natively. The larger binary also starts about 0.9 ms
slower.

For `ddto test` runs, test binaries could hand encoded batches to the CLI
through an inherited pipe or a local socket. The CLI would deliver them over
HTTP with one connection pool, batching across packages. The binary drops
`net/http` and `crypto/tls` only when settings, known tests and telemetry also
come from the CLI (items 9 and 10), so this is the transport those items need.
[#32](https://github.com/tonyredondo/dd-ci-testing-poc/pull/32) already gave the CLI the SDK's HTTP client, in its Git worker. Builds with `-c` and standalone binaries keep today's
delivery through a different generated import. The CLI then owns retries and
the final flush, and must keep each binary's event order, back-pressure,
deferred admission and goleak checkpoints, and not lose events when it is
interrupted.

## 58. `t.Helper()` in the test wrapper

`gotesting/testing.go:727` calls `t.Helper()` for every instrumented test:
about 0.7 µs and 185 bytes per test, measured with 2,000 empty tests run 15
times, about 2% of Mini's per-test cost. It marks the wrapper as a helper, so
it can also change where `testing` attributes the test's own messages: when a
test function calls `t.Helper()` itself, `testing` looks past it, and the
wrapper's frame is next. Before dropping it, or calling it only on the paths
where Mini logs through `t`, compare the reported locations with native
`go test` for such tests, for subtests and for retry attempts. The SDK does the
same, so record the adaptation.

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

The 2026-10-11 review added these, measured on Linux:

- Package initialization in a test binary: Mini adds 64 packages and 0.43 ms
  (0.55 against 0.12 ms with `GODEBUG=inittrace=1`).
- Goroutine stack growth: every test goroutine grows its stack natively, about
  2.4 µs per test; Mini adds about 0.8 µs.
- Cold builds at `2d0059a`: Gin with 32 CPUs took 11.3–11.5 s against
  11.0–11.3 s natively, and 66.6 against 60.4 s of CPU. Mini's 47 packages
  took 3.0 s of CPU to compile and 1.2 s of vet fact analysis, both cached
  afterwards. The benchmarks measured +51% for this cell at `20caa45`, before
  [#29](https://github.com/tonyredondo/dd-ci-testing-poc/pull/29) removed
  duplicate compiles; this review did not reproduce it. cmd/go computes vet
  facts for every dependency, so skipping Mini's packages would change
  `go vet`.
- Per-test allocations: Mini allocates about 8.5 KB and 72 objects more per
  test than native `testing`, mostly in span creation, the payload buffer and
  build information. Items 1, 7, 22–24, 27 and 38 cover the others;
  [#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31) removed the build-information reads.

Implementing items 53 and 54 in [#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31) also checked these:

- Compiling Mini's packages with `-race=false` saves more than marking their
  functions `//go:norace`: 33 instead of 22 µs per test with 2,000 empty tests.
  But the compiler then turns Mini's `sync/atomic` calls into plain
  instructions, while the race runtime still checks map accesses, so a map
  published through an atomic pointer is reported as a race and fails the
  client's test.
- Overlapping the CODEOWNERS preload with the rest of startup: in Gin, the
  session reads the result 0.16–0.24 ms after the preload, and 2,000 rules now
  parse in 1.35 ms. Most of the preload's 7–8 ms is the CI tags it computes
  first.
- A cache of each file's resolved source path: about 0.75 µs of CPU per test
  with 2,000 tests, within the noise of wall time.

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
  runtime never calls it.

Before keeping a change, run the affected contract tests and the SDK comparison.
Document runtime adaptations beside the incorporated SDK code so an upstream
update can preserve their ownership rules. Compiled-code refactoring is outside
this queue until its scope is agreed.
