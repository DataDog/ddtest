# Performance adaptations to the SDK port

Base: [`96aedb31048c07e29e7a20a4333dc3b8d289c52d`](https://github.com/DataDog/dd-trace-go/commit/96aedb31048c07e29e7a20a4333dc3b8d289c52d).
`SOURCE.json` retains each original path/hash alongside the local hash. This
record explains changes that an upstream synchronization must review manually.

## Retained metric handles

[`civisibility/utils/telemetry/telemetry_count.go`](civisibility/utils/telemetry/telemetry_count.go)
binds counters for the ordinary test/suite/module/session tags and the enqueue
counter. Each binding lazily registers through the existing global registry;
tags are sorted and the swappable handle is created once. Steady-state calls
submit directly, without rebuilding tag slices or joining a metric key.

The set is bounded: two framework classifications, four hierarchy types and two
lifecycle counters. Additional feature tags use the general path. Retry, EFD,
quarantine, disabled/attempt-to-fix and benchmark tags must never be discarded
just to select a bound handle. Values of the SDK's mutable tag slices are checked
on each call; their slice identity is not a cache key.

[`telemetry/metrichandle.go`](telemetry/metrichandle.go) owns a copy of bound tags.
Bindings preserve disabled telemetry, calls before `StartApp`, startup replay and
`SwapClient`. `MockClient` clears global registrations, so
[`telemetry/globalclient.go`](telemetry/globalclient.go) advances a generation at
that reset. A binding registers again on its next use; ordinary swaps still use
the existing swappable pointer. If upstream changes recorder limits, client
swapping, disablement or test resets, review this lifetime as well.

Checks: `TestBoundCountReplaysSwapsAndResets`,
`TestGlobalRegistrationReplaysAndSwapsOnce` and
`TestEventCountersKeepCanonicalAndFeatureTagsAcrossClients` and
`TestEventCountersDisabled`, plus the HTTP
telemetry parity matrix. Verify the feature-tag fallback and disabled behavior,
not just the ordinary three-counter benchmark.

## Counter points kept inline

[`telemetry/metrics.go`](telemetry/metrics.go) keeps a metric's value, timestamp
and presence flag together under a short `sync.Mutex`. Upstream allocated an
immutable point on every count/gauge submission and replaced an atomic pointer.
The port allocates no point on submission. Collection detaches and resets the
whole point under the same lock, then constructs the wire payload after release.

Every concurrent increment belongs to exactly one collection. `Get` returns
`NaN` until a submission and after collection, while a submitted zero is a real
point. Fractional/negative values and gauge replacement keep their semantics.
Timestamps are captured before competing for the lock. They need not be
monotonic between concurrent callers.

Rate metrics retain their existing atomic interval start and short-interval
rule. A short interval does not consume the accumulated count; an eligible
collection detaches the count and divides it by that interval. Distributions,
wire fields and interval calculation have not been redesigned.

Checks: `TestMetricPointLifecycle`,
`TestConcurrentMetricCollectionPreservesValues`, telemetry HTTP parity and
`-race`. Compare both serial updates and contended updates when changing this
lock; an allocation reduction alone does not establish faster throughput.

## Coverage processing and clock ownership

[`civisibility/integrations/gotesting/coverage/coverage_payload.go`](civisibility/integrations/gotesting/coverage/coverage_payload.go)
measures serialization with differences from one monotonic origin captured at
package initialization. This measures elapsed work without reading `time.Local`
from a coverage worker. Event and metric timestamps retain their wall clocks.

[`civisibility/integrations/gotesting/coverage/test_coverage.go`](civisibility/integrations/gotesting/coverage/test_coverage.go)
captures both counter profiles in the test hooks. Deferred delivery queues only
their parsing, subtraction and serialization for an idle checkpoint; capturing
the second profile later would include another test's counters. In ordinary
delivery, processing runs synchronously when telemetry or debug logging needs
wall-clock timestamps. With both disabled, it can run in a worker using the
monotonic duration clock. This synchronization adds post-test work to ordinary
delivery when diagnostics are enabled.

Each processing job closes its completion channel, including on a profile error.
The close action waits for that channel. A completed worker can exit immediately;
it does not remain blocked until the session closes. Keep the profile snapshots,
idle admission, terminal shutdown and completion signal together when updating
this code. Coverage still uses the SDK's initial-attempt-only retry policy.

Checks: `TestCoveragePayloadConcurrentLocalChange`,
`TestCoverageProcessingFinishesBeforeShutdown`,
`TestDeferredCoverageProcessingWaitsForWholeTestGroup`,
`TestDeferredCoverageProcessingFailureStillCompletes` and
`TestMiniCoverageWithGlobalTimeChanges`. The integration check compares exact
per-test bitmaps and CI events with a safe SDK reference, with 4/32 CPUs,
ordinary/deferred delivery and telemetry off/on. Its Mini process changes the
global local zone; the SDK reference runs the same application paths without
that mutation because its background clocks are not safe under it.

## Telemetry startup and HTTP completion

[`telemetry/globalclient.go`](telemetry/globalclient.go) runs the flush in
`StartApp` synchronously in both delivery modes; the SDK starts it in a
goroutine in normal mode. CI initialization calls `StartApp` before the first
test, so no test waits for the startup requests or runs while they read
`time.Local`, and `go test` does not count them in a test's duration. As in the
SDK, `app-started` is a standalone request followed by a `message-batch`: the
mapper keeps `app-started` out of batches. A slow endpoint delays the session
start within the client timeouts. `StopApp` still waits on the existing
WaitGroup, so a concurrent close sends `app-closing` after startup.

[`telemetry/internal/ticker.go`](telemetry/internal/ticker.go) orders `Stop`
with interval changes under the ticker mutex, so an interval change after `Stop`
cannot restart the native timer.

[`telemetry/internal/writer.go`](telemetry/internal/writer.go) consumes each HTTP
response to EOF and closes it before trying another endpoint or returning.
Go's transport can otherwise finish draining an unread body in the background,
after the next test has started. The client timeout bounds consumption. Error
messages keep only their 256-byte prefix, and request duration retains its
measurement at the response headers, before draining. Status classification,
fallback and payload accounting retain their SDK rules.

Checks: `TestStartupTelemetrySendsBeforeTests` blocks a real loopback startup
response and checks that `StartApp` waits for it, initial configuration, retry,
concurrent close and failed initialization in both modes.
`TestStoppedTickerCannotRestart` checks terminal close.
`TestWriterFlushWaitsForResponseCompletion` holds the transport's return-to-idle
handshake for successful and failed responses.
Run these with `-race`, plus the clock integration and HTTP telemetry parity.
When synchronizing upstream, check response ownership and startup scheduling
alongside the telemetry metrics; checking wire fields alone misses these races.

## Source metadata parsing

[`civisibility/integrations/manual_api_sourcecache.go`](civisibility/integrations/manual_api_sourcecache.go)
adds `parser.SkipObjectResolution`. The cache reads declarations, source ranges,
function literals and ITR comments; it does not use identifier objects, scopes
or unresolved-identifier lists. `ParseComments` and `AllErrors` remain enabled.

If upstream adds semantic identifier inspection, reassess this flag. Preserve
the source-cache tests for named functions, adjacent literals, missing/invalid
files and ITR comments, and the CI matrix's source/coverage comparisons.

## Testify method names

[`civisibility/integrations/gotesting/testify.go`](civisibility/integrations/gotesting/testify.go)
uses `strings.HasPrefix(name, "Test")` where upstream compiled `^Test` on every
method. Both match the same literal prefix, including a method named `Test`.
The SDK's other method-registration and suite-grouping rules stay in place.
Run Testify parity with method filtering, lifecycle hooks, helpers, retries and
coverage whenever upstream changes this advice.

## Pack file cleanup

Upstream `CreatePackFiles` in [`civisibility/utils/git.go`](civisibility/utils/git.go)
runs `git pack-objects` into a `.dd-pack-objects*` directory in the temporary
directory, then in the working directory when git cannot move its pack across
devices, which is the usual case for a tmpfs `/tmp`. After the upload,
`sendObjectsPackFile` removed only the `.pack` files. The failed attempt's empty
directory stayed in the temporary directory, and the fallback left a directory
with git's `.idx` and `.rev` files in the user's working tree.

Here the fallback is the repository's common git directory: it holds the
objects, so the move succeeds, and the working tree never receives a temporary
directory. `git rev-parse --git-common-dir` runs without command telemetry, so
git metrics match the SDK. A failed attempt or an empty result removes its
directory, and `RemovePackFiles` removes the directory after the upload.

Checks: `TestRemovePackFilesRemovesTemporaryDirectory`,
`TestPackFilesFallBackToGitDirectory`, `TestFailedPackFilesLeaveNoDirectory`
and the git upload parity case, where Mini must leave its temporary directory
empty. The fake git in these tests and in `TestCreatePackFilesMissingPackFile`
skips the options before the command, which cmd also splits at `=` for
`git.bat`, and the tests check that `pack-objects` ran, so Windows exercises the
same paths.

## Parallel retry accounting

Every execution of a test with additional features (retries, early flake
detection, attempt to fix) runs as a fresh attempt in
[`gotesting/retry_attempt_runner.go`](civisibility/integrations/gotesting/retry_attempt_runner.go),
outside testing's `tRunner`. testing counts each attempt that calls `Parallel`
as a started parallel test. The first such attempt moves the parallel lease to
the original test, whose `tRunner` records one end. Upstream records no end for
later parallel attempts, so after a retried parallel test testing still counts
a parallel test as running, and `testing.AllocsPerRun` panics in every later
test of the binary.

Mini records those ends. ddtest's testing overlay adds `ParallelStopHook` from
`internal/instrument/hooks.go`, only for Mini and only when testing declares
`parallelStop atomic.Int64`; the linker allows no other access to the counter.
The hook registers a function in
[`retry_attempt_parallel_stop.go`](civisibility/integrations/gotesting/retry_attempt_parallel_stop.go).
`beginRootParallelSchedulerTransfer` marks attempts that go parallel after the
lease moved, and `finalizeFreshRetryAttempt` records their end before it
publishes the result, as `tRunner` does before it releases its caller. Both
edits replace single lines, so no line in a retry's error stack moves. The SDK
runtime keeps upstream behavior.

Checks: `TestSharedParallelLeaseAttemptsRecordTheirEnd`,
`TestSequentialAttemptsRecordNoParallelEnd`, `TestParallelStopHookOnlyForMini`,
`TestToolchainTestingDeclaresParallelStop` and
`TestMiniInProcessRetryKeepsAllocsPerRun`. This package's own tests run without
the overlay, so `TestAdditionalFeatureSelectorDoesNotAllocate` measures
allocations without `testing.AllocsPerRun`.

## Per-test allocations

Each instrumented test runs the SDK's span creation, source lookup and Testify
checks. With 20,000 trivial subtests under Mini, that path added about 88
allocations per test to `testing`'s own 17; these adaptations remove about half
of them without changing any event:

- Debug logs on per-test paths in `manual_api_ddtest.go`,
  `gotesting/instrumentation_orchestrion.go` and `gotesting/testing.go` are
  guarded by `log.DebugEnabled()`: Go evaluates and boxes the arguments of a
  disabled call. In `instrumentation_orchestrion.go` the guards replace blank
  lines or an `else`, so no line moves: its closures appear in error stacks,
  which parity compares line by line with the pinned SDK. Logs on rare paths
  (directives, failures, skips, benchmarks and retries) stay unguarded.
- `truncateCIVisibilityTagValue` in `meta.go` returns the original value when a
  string needs no truncation, instead of boxing it again.
- `getTestifyTest` in `gotesting/testify.go` returns at once until a suite
  registers, then walks parents through the validated `testing` offsets, like
  the other private field accesses; reflection remains the fallback. Method
  matching no longer builds `"/" + method` strings.
- `createTest` uses the module's test operation name, a concatenated resource
  name and hierarchy IDs formatted once by the session, module and suite
  constructors (their only construction sites; the IDs never change). Origin and
  manual-keep options are built once in `manual_api_common.go`.
- `instrumentTestingTFunc` declares the module and suite names inside its
  closure with `TestifyTest.moduleAndSuite` instead of reassigning the captured
  names, so closures capture them by value instead of moving them to the heap.
- `utils.GetModuleAndSuiteName` caches its result by program counter.

The CI tag snapshot check and per-call CI metrics are unchanged (see shared CI
string tags). Checks: `TestTestifyLookupMatchesReflection`,
`TestFindTestifyTestMatchesFinalElement`, Testify and native parity. When
syncing, keep new per-test debug logs guarded without moving any line that can
appear in an error stack, and re-measure allocations per test with a
many-subtest fixture.

## Lazy stack classification

[`stacktrace/stacktrace.go`](stacktrace/stacktrace.go) constructs its immutable
prefix tries through independent `sync.OnceValue` initializers. Internal-frame
filtering does not construct the large third-party table. Raw stack capture
constructs neither table; third-party classification builds its trie on first
use, using the same generated library list and `golang.org/` prefix.

No redaction or matching rule changes. This moves table construction from package
startup to the first lookup that needs it. `stacktrace/trie.go` documents the
publication boundary: initialize completely, publish once, then only read.
Update the generated library list with upstream and retain
`TestConcurrentStackClassification` under `-race`, plus error-stack parity.

## Deferred delivery and goleak checkpoints

The POC's `internal/cidelivery` owns the optional
`DD_CIVISIBILITY_DEFERRED_DELIVERY` coordinator. The port integrates with it in
`integrations/civisibility.go`, `civisibility_features.go`, `gotesting/testing.go`,
the coverage/log writers and the telemetry ticker. Test activity begins
inside the existing instrumented closure, not an outer wrapper: the SDK uses
that closure's identity to recognize already instrumented tests. The first
registered cleanup runs last, covering user cleanups and parallel descendants.
Preserve this placement when syncing retry or testing lifecycle changes.

In deferred mode, settings and repository upload are synchronous. Full
coverage/log payloads transfer ownership to an idle queue; a partial payload
waits for the writer's stop, so a serial suite does not send one per test.
Telemetry ticks at checkpoints once its current interval has elapsed
(`telemetry/internal/ticker.go`), instead of starting a periodic worker.
Coverage acquires its delivery concurrency permit when the queued work runs,
not while a parallel test is buffering it.
Normal mode keeps background sending and periodic telemetry; telemetry startup
and coverage follow the rules above. Terminal shutdown force-drains
pending work before writer barriers. Memory may grow across a parallel group;
outgoing payload bounds remain unchanged. See
[delivery checkpoints](../../../docs/delivery.md).

The automatic goleak shim applies in both Mini delivery modes. CI HTTP paths in
`utils/net/http.go` and `telemetry/internal/writer.go` bracket requests with the
send gate. A goleak check waits for active sends, pauses new sends and closes
owned idle CI connections before taking snapshots. In normal mode the
asynchronous feature initialization and repository upload in
`integrations/civisibility.go` and `civisibility_features.go` register with
`cidelivery.TrackBackground`, so a check first waits for them, including git
subprocesses, for up to one minute. Named telemetry, coverage and
log worker functions, and Mini's background and checkpoint test-cycle senders, permit exact
filters without ignoring `net/http` or a user goroutine snapshot. Preserve worker names together with
`internal/instrument/goleak.go` when moving these functions.

Checks: `TestDeferredDeliveryParityMatrix` (17 policy combinations),
`TestDeferredDeliveryTestifyParity` (seven suite combinations),
`TestMiniGoleakIntegration` (normal/deferred, covered library, race, external
helper and real leak controls), coordinator/transport tests and `-race`.
The error-stack comparator maps only the relocated wrapper's known line 844
to the pinned SDK's line 838; application frames and other library lines remain
strict. A source move must update that explicit mapping with proof, not erase
stack locations globally.

## Shared CI string tags

`civisibility/utils/environmentTags.go` adds `GetCITagsSnapshot`, an owned,
read-only snapshot with a revision. Current contents are checked with `maps.Equal`
under the existing mutex. This retains the SDK's sequential direct cached-map
edits as well as `AddCITags`, `AddCITagsMap` and resets. Published snapshots never
change. Do not replace this content check with pointer identity or update-only
invalidation while the original `GetCITags` map remains mutable.

`civisibility/integrations/manual_api_common.go` caches truncated string options
per snapshot revision and Bazel mode. CI/Git/OS/runtime strings and
`_dd.ci.env_vars` bind one Mini `CommonTags` option; other strings keep ordinary
tag options, and numeric CI metrics remain fresh per call. Keep the original
option order: common tags replace earlier options, and event-specific tags and
metrics applied afterward can replace them. Session name stays in its original
envelope entry. The UTF-8 character limit still applies before sharing.

Bazel filtering precedes snapshot binding. CI tags must not reappear through
envelope metadata in payload-file mode. New CI metrics or special tag handling
in upstream require review of this boundary.

Mini's `internal/minitracer/common_tags.go` owns getters and wire projection.
Homogeneous event kinds share strings in metadata; text/numeric overrides or a
mixed snapshot fall back to local strings without mutating sealed maps. Child
spans do not receive CI defaults. Accounting excludes replaced default values
and includes envelope overhead conservatively, even when the wire payload
shrinks. A large masked default must never reject a smaller valid event.

Checks: `TestCITagsSnapshotUpdatesAndRetainsOldValues`,
`TestCommonTagOptionsKeepUpdatesTruncationAndBazelFiltering`, the native shared-tag
wire/concurrency/bounds tests and the SDK/Mini parity matrices. The differential
capture expands only the declared shared CI keys before semantic comparison;
its negative controls retain missing/wrong values, overrides and numeric
collisions. Keep the raw-payload placement assertions too.

## POC-owned code outside this source subset

`internal/minitracer/span.go` encodes the high eight trace-ID bytes directly with
`hex.EncodeToString`. This retains the same 16-character, zero-padded lowercase
`_dd.p.tid` value. It is owned by Mini; propagation and event tests cover it.

The front-end also prunes known dependency queries, decodes package JSON from
stdout, reserves rewritten-source buffer capacity and reuses the validated
Testify AST within a preparation. See the
[performance guide](../../../docs/performance.md) for those POC-owned paths and
their separate build-time measurements.

`internal/minitracer/batch.go` creates a timeout context only on a waiting or
flushing enqueue, retaining the deadline captured at entry. Deferred batches
split by the original intake thresholds and retain only unsent chunks after
failure. `internal/citransport/compression.go` pools `gzip.BestSpeed` writers;
this changes compression ratio, not content or protocol. The source rewriter
omits comment AST construction while preserving original comment bytes.

## Update checklist

1. Produce `scripts/upstream.py patch` against the recorded SDK snapshot and
   compare the affected paths with the new default-branch snapshot.
2. Review changes to tags, metric lifetimes, timestamps, source metadata and
   stack redaction against the invariants above. Preserve improvements explicitly;
   do not overwrite them with an import-path-only copy.
3. Run the focused checks and SDK/Mini differential suite on the same SDK base.
   Run concurrency cases with `-race` and the supported platform workflows.
4. Refresh `TESTS.json` for adapted assertions and `SOURCE.json` using the shared
   [maintenance procedure](../../../docs/maintenance.md). Rerun comparable
   benchmarks if upstream changes a hot path.
