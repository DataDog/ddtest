# Runtime adaptations to the SDK port

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

## Startup metric and global-call replay

[`telemetry/metrichandle.go`](telemetry/metrichandle.go) uses `startupMu` to
coordinate recording a submission with the first handle swap and replay.
A producer that observed an empty pointer checks it again under this lock.
It either records before replay or submits to the installed handle. This
prevents an increment from entering the recorder after it has been drained.

The active-client path still loads the atomic pointer and submits directly.
Recorder bounds, overflow logging and later client swaps keep their existing
behavior. During an SDK update, preserve this handover even if the recorder's
own queue is already safe for concurrent access: queue safety alone does not
make recording and replay one operation.

Checks: `TestMetricStartupReplayIncludesConcurrentSubmissions` overlaps 64
producers with installation, below the recorder limit, and checks every
increment. Run it with `-race`, alongside client-swap and HTTP telemetry tests.

`telemetry/globalclient.go` uses a separate startup lock for global callbacks
such as logs, configuration and product changes. The cold path rechecks the
client and records under that lock; `SwapClient` publishes under the same lock.
Replay and callbacks run after release, so nested telemetry calls cannot
self-deadlock. Active-client calls retain their atomic load and direct dispatch.
`TestGlobalClientStartupReplaysEveryCall` overlaps 64 calls with publication and
requires every callback once. The unchanged implementation loses calls under
that same test, even without a data race.

## Close-action registration

[`civisibility/integrations/civisibility.go`](civisibility/integrations/civisibility.go)
appends close actions and pre-close barriers, then executes each list backwards.
Registration grows linearly with the number of actions. Coverage registers an
action per test, so prepending a copied slice would allocate quadratically.

Keep LIFO ordering within each list, and run every pre-close barrier before the
ordinary actions. A barrier can register an ordinary action; shutdown must
include it. The existing locks, single shutdown owner and wait for that owner
also apply to signal-triggered shutdown.

Checks: `TestCloseActionsKeepLIFOAndRunBarriersFirst`,
`TestConcurrentCloseActionsRunOnce` and the signal-handler tests. Use
`BenchmarkCloseActionRegistration` to compare allocation growth at 1,000,
4,000 and 16,000 registrations.

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

`gotesting/coverage_cleanup.go` registers the after-snapshot before the user
body, so Go executes user cleanups and descendants first. The coverage barrier
stays in place through this snapshot. Retry-owned cleanup follows the same
order and only the initial attempt collects coverage. An active collector owns
a small shutdown marker until cleanup; a body or descendant panic transfers
terminal delivery to that cleanup rather than closing its writers too early.

Coverage and log writers force-drain their final partial payload during stop,
including terminal shutdown before test admission is released. An ordinary
idle checkpoint would skip that work and leave the writer waiting for itself.
`TestMiniCoverageIncludesCleanup` checks cleanup-only lines under race, ordinary
and deferred delivery, including retries, EFD, parallel descendants and panic.
This corrects the pinned SDK's cleanup omission instead of reproducing it.

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
goroutine in normal mode. The settings client uses
`NewClientWithConcurrentTelemetry` in
[`civisibility/utils/net/client.go`](civisibility/utils/net/client.go) to overlap
that flush with settings initialization. The constructor returns a wait
function; `ensureSettingsInitialization` defers it inside its `sync.Once`
callback. That join must cover every return path, including cached settings,
offline modes and request errors. Other client constructors stay synchronous.

Both operations finish before test admission, so no test runs while startup
requests read `time.Local`, and `go test` does not count them in a test's
duration. The CI client sends only the standalone `app-started` request during
startup. `telemetry/client.go` snapshots the other data sources at that same
point and retains their mapped payloads in its existing bounded queue. Their
values and timestamps survive later submissions. A regular flush sends those
snapshots before newer payloads; `StopApp` drains them before `app-closing`.
Retries and oversized-payload handling use the same queue and writer paths.
Other implementations of the telemetry client interface retain their ordinary
`Flush` behavior.

Delivery keeps the selected mode: ordinary telemetry uses its existing periodic
flush, while deferred telemetry flushes at eligible idle checkpoints. Both
modes flush on shutdown. No startup worker is left sending that second payload
during the first test, and no new goroutine or timer is created. Ordinary mode
continues to permit later periodic requests during tests; use
`DD_CIVISIBILITY_DEFERRED_DELIVERY=true` to keep those requests outside test
bodies. A slow `app-started` response still delays startup within the client
timeouts. `StopApp` waits for the startup WaitGroup before final delivery.

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
`integration/TestMiniStartupOverlapsSettingsAndTelemetry` independently blocks
the two HTTP responses, verifies overlap and holds test admission until both
finish, including failed requests. It runs the test binary with `-race` in
ordinary and deferred modes. Keep that join when updating the client or settings
initialization; asynchronous startup without it reintroduces the clock race.
`TestStartupFlushRetainsMetricsUntilNextFlush` compares a retained payload
byte for byte after a later submission and final flush, including failed and
oversized startup requests. Keep snapshot ownership and request ordering when
updating the mapper or queue.
`TestStoppedTickerCannotRestart` checks terminal close.
`TestWriterFlushWaitsForResponseCompletion` holds the transport's return-to-idle
handshake for successful and failed responses.
Run these with `-race`, plus the clock integration and HTTP telemetry parity.
When synchronizing upstream, check response ownership and startup scheduling
alongside the telemetry metrics; checking wire fields alone misses these races.

## Runtime delivery and shutdown diagnostics

`civisibility/integrations/manual_api_ddtestsession.go` measures session closure,
including its mutex wait, module closure and `tracer.Flush`. Its deferred summary
runs after unlocking and identifies repeated closes. The exit-code field stays
the command result; a failed delivery is logged separately.

`civisibility/integrations/civisibility.go` measures the complete shutdown call
and its barriers, close actions, CI logger and final telemetry phases. The total
summary runs after shutdown defers, state publication and the signal-handler
join. A non-owner call includes its wait or inactive check. Keep the existing
LIFO order, single owner, error handling and signal semantics during an update.

These timings use the incorporated logger and are enabled only in debug mode.
They add no worker, timer or request. Native `internal/minitracer/runtime.go`
measures flush/close, and `internal/citransport/transport.go` records each HTTP
attempt plus complete payload delivery. Per-attempt logs end after response
consumption/close, before backoff; payload delivery includes admission, retries
and ownership cleanup. Existing telemetry request-latency metrics still end at
headers. Host/path diagnostics omit queries, headers, bodies and raw errors.

Checks: transport diagnostics cover ordinary/agentless delivery, gzip, retries,
429, permanent and network failures, early cancellation and paused admission.
`TestRequestDiagnosticWaitsForResponseClose` checks the completion boundary;
`TestCloseActionsKeepLIFOAndRunBarriersFirst` checks shutdown ownership and order.
`integration/TestMiniStartupOverlapsSettingsAndTelemetry` checks emitted runtime
phases, four hierarchy events and successful process exit with real HTTP and
`-race`, including normal/deferred delivery and startup failures. See
[debug timing boundaries](../../../docs/cli-debug.md#runtime-timing).

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

Mini's `instrumentTestifySuiteRunScoped` entry hook registers only the current
suite invocation and restores an enclosing scope on return. Resolved methods
bind to their actual `*testing.T` until cleanup, preserving descendant identity
if the suite runner has already returned. Go's numeric `#NN` suffix is removed
only for matching a Go method name. Events keep the full native test name.
The SDK backend and legacy Orchestrion hook keep the original ABI.

`TestTestifyScopeRestoresAndBindsMethods` and
`TestTestifyMethodBindingOutlivesSuiteRun` exercise lookup and lifetime;
`TestMiniTestifyDuplicateIdentity` checks the wire hierarchy, suite and source
with a covered Testify library, nested children, `-count=2` and `-race`.

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

CI string snapshots use the revision/escaped-map contract below; numeric CI
metrics remain fresh per call. Checks: `TestTestifyLookupMatchesReflection`,
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

Checks: `TestDeferredDeliveryParityMatrix` (16 policy combinations),
`TestDeferredDeliveryTestifyParity` (seven suite combinations),
`TestMiniGoleakIntegration` (normal/deferred, covered library, race, external
helper and real leak controls), coordinator/transport tests and `-race`.
The error-stack comparator maps exact internal function/line pairs for the test
wrapper, subtest calls and retry helpers. The complete mapping is recorded in
[the comparison contract](../../../docs/ci-parity.md#comparison-contract).
Testify's embedded panic stack uses the same mappings; its `Error Trace` lists
only file/line locations. Application frames and other library lines remain
strict. A source move needs an explicit mapping backed by negative tests.

## Shared CI string tags

`civisibility/utils/environmentTags.go` adds `GetCITagsSnapshot`, an owned,
read-only snapshot with a revision. Internal consumers use `GetCITagsReadOnly`.
Without a mutable-map reader, an unchanged snapshot needs only the existing
mutex and two state checks. `AddCITags`, `AddCITagsMap` and resets invalidate the
current map; the next snapshot checks whether its contents actually changed,
so no-op updates keep their revision.

`GetCITags` marks the current map as exposed. Every later snapshot of that map
uses `maps.Equal` to observe sequential direct edits, including edits made long
after the map was returned. Rebuilding the current map clears that mark because
old references no longer affect it. Published snapshots never change. When
porting an upstream reader, use the read-only API unless that reader must mutate
the map. Keep the escaped-map path and the late-edit/concurrent-reader tests.

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
The immutable snapshot avoids rebuilding maps when spans start. Delivery copies
the effective strings onto each CI event, matching the SDK representation.
Text and numeric overrides retain their precedence; sealed maps never change.
Child spans do not receive CI defaults. Accounting includes the event strings
and excludes masked defaults. A large masked default must never reject a smaller
valid event. Standard language/runtime ID/library/env/session envelope entries
remain unchanged. Per-event projection adds wire bytes and delivery allocations;
changing that placement requires evidence from the real intake.

Checks: `TestCITagsSnapshotUpdatesAndRetainsOldValues`,
`TestCommonTagOptionsKeepUpdatesTruncationAndBazelFiltering`, the native shared-tag
wire/concurrency/bounds tests and the SDK/Mini parity matrices. The differential
capture expands only the declared shared CI keys before semantic comparison;
its negative controls retain missing/wrong values, overrides and numeric
collisions. Keep the raw-payload placement assertions too.

`BenchmarkCommonMetadataLifecycle` counts request bytes and requests with
atomics because its transport is shared by concurrent senders. Keep those
counters synchronized when changing the sink; a data race invalidates the
`wire-B/event` result. `TestCommonMetadataTransportCountsConcurrentSends`
checks both totals during ordinary test runs, including `-race` runs.

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

## Source ranges and runtime identity

`civisibility/integrations/manual_api_sourcecache.go` uses `declStartLine` for
named declarations whose range contains the runtime entry line. Go's
entry PC can point at the last line of an optimized function. Keep the runtime
line for matching declarations, especially methods with the same name, but use
the cached AST for the published range. Missing sources retain the runtime
fallback, as do unconfirmed declaration matches; closure matching is unchanged.
No additional source read or parse is needed. During an SDK update, check
whether upstream has corrected this too.

`TestMiniCLIRuntimeSelectionAndSourceRange` checks emitted lines 5–10 with normal,
unoptimized and race builds. The source-cache unit tests cover duplicate method
names, named `func1` declarations, closures, unavailable sources, impacted-test
classification and process-retry metadata.

`log/log.go` uses `internal/version.RunLogPrefix` for native runtime diagnostics:
`TestOptimization.run  v0.0.0`. CLI diagnostics use `BuildLogPrefix`:
`TestOptimization.build v0.0.0`. Both prefixes share the native version, also
used in event metadata and CI telemetry. Preserve this identity when updating
the incorporated logger. The original SDK's logger is unchanged.

## Fuzz and executable Examples

The feature port is SDK PR #5442 at
`7b32e1812cb5c1fb807a63cc5042750f3d3cd672`; it has its own source hashes,
separate from the general SDK base. `testingF.go`, `testingExample.go` and
`fuzz_events.go` retain the native lifecycle and original test assertions.
`instrumentation.go`, `instrumentation_orchestrion.go` and `testing.go` merge
that delta while preserving this port's coverage-aware shutdown.

Tracer flushes route to `minitracer.Flush`. F roots hold deferred admission
through seeds and cleanups; examples hold it through output capture and event
finalization. The selective goleak wrapper names the owned fuzz waiter, both
example readers and the managed-example waiter. User leak negative cases must
remain failures. No sender/worker filters are widened to net/http or all testing.

`fuzz_offsets.go` joins the existing one-time testing layout discovery. It
validates common type/offset, F's own scheduler pointer and fuzzCalled bool.
`nativeResult` reads cached failure/skip fields under common.mu. Only the
post-M.Run drain reads duration; fatal drains use the captured finish time.
This removes the per-event private-field reflection and common wrapper allocation.
Six repeated result-collection benchmarks are recorded in `docs/results`.

Checks: original F/Example/queue/lifecycle tests; synthetic offset/type drift;
`TestFuzzFatalResultDoesNotReadActiveDuration` with race; the exact PR's 16
scenarios through manual/automatic entrypoints and both delivery modes;
atomic coverage controls and negative goleak fixtures. The feature guide in
`docs/fuzz-examples.md` links all provenance and reproduction commands.

## CODEOWNERS-derived services

Mini's opt-in service selection lives in local
`civisibility/utils/service_name.go`. It reuses the incorporated CODEOWNERS
parser and derives one process service from the selected package. The default
is disabled; the format is `service-$(owner)`. A nonempty explicit `DD_SERVICE`
keeps priority. Formats are literal strings, with no shell evaluation.

`testopt` registers a generated test caller before TestMain changes its working
directory. That registration preserves shared backing files and records no
source when disabled. Runtime resolution keeps absolute/trimpath source paths
and workspace boundaries. The result is process-local and immutable after its
first resolution. Test-only resets require stopped readers.

`civisibility/utils/codeownership/` owns host-specific discovery, parsing and compiled
file/directory matching. `utils/codeowners_discovery.go` supplies CI context and caches
its resolver. The package implements GitHub and GitLab rules, including
inline-comment differences, ownerless GitHub rules, sections, defaults,
exclusions, roles and character classes. File selection follows each host's
location priority, including `docs/CODEOWNERS`.

Matching and owner classification use Go runes and standard Unicode tables.
Section names use `strings.ToUpper`. Keep these contracts when updating the
SDK; the package does not carry Unicode compatibility tables. Pattern and
matching limits apply per segment. File decoding accepts BOMs and common line
endings separately from the UTF-8 parser.

Results have private immutable state. Owner lists are deduplicated once and
tags are prepared with each result. File and directory patterns share compiled
tokens, ASCII literals use string equality, and rooted literal prefixes are
checked once before wildcard matching. A section without exclusions stops at
its winning rule. Sections with exclusions retain the scan needed for sticky
exclusions. Owner unions allocate only when another section adds an owner;
small lists use slices and large lists use a map, preserving declaration order.

Directory queries use an explicit target: a trailing slash includes the package
itself, terminal `/*` selects direct children, and other directory patterns can
be inherited. No guessed filename determines the service. File and service
caches retain their process lifetimes. The [package guide](../../../docs/codeownership.md)
describes the contracts and Go tests to keep during an SDK update.

The CI bootstrap and `civisibility/utils/net/client.go` share the selected
service. Preserve that binding during SDK updates: settings and telemetry must
not name a different service from events or logs. The new environment keys are
registered in local `env/ci_service.go`, without editing the generated SDK map.
The SDK base remains independently pinned.

Checks: `TestGitHubRules`, `TestGitLabRules`, `TestRepositoryExamples`,
`TestUnicodePaths`, `TestGoUnicodeSectionsAndOwners`, parser
bounds/concurrency/discovery tests,
`TestCodeOwnersPackageService`,
`TestCodeOwnersServiceMissingInputs`, `TestCodeOwnersServiceCacheConcurrentReaders`,
`TestCodeOwnersPackageDirectoryPaths`, `TestMiniCodeOwnersPackageServices` and
`TestMiniCodeOwnersCompiledServices`. Keep the compiled-binary cases with the
feature disabled at build time and enabled at execution, as well as explicit
service priority and the HTTP service assertions. See the
[feature guide](../../../docs/codeowners-service.md) for configuration and limits.

## Native contexts for SDK span copies

`civisibility/integrations/native_context.go` attaches a private Mini test scope
to the native testing context. `gotesting/context.go` uses the existing typed
offset cache before the user body starts. Root tests, subtests, fuzz callbacks
and benchmarks bind their own event identity. Go retains cancellation ownership;
isolated process-retry children retain their identity-free context contract.
SDK-free binaries skip binding with zero allocations.

The SDK compiler mirror lives in the POC's `internal/instrument/sdk_mirror.go`
and targets the original SDK module's inputs, leaving its APM parentage and
delivery intact. Mini captures final fields under the SDK lock and enqueues a
detached copy after unlock. State belongs to `SpanContext` to survive pooling.
There are no new dependencies or goroutines. Keep cache-marker invalidation,
lock ordering, API validation and context lifetime checks during SDK updates.

Checks: `TestNativeContextWithoutSDK`, `TestSDKMirrorScopeAndCancellation`,
`TestSDKMirrorCaptureOwnsFinalData`, `TestSDKMirrorConcurrentCapture`,
`TestSDKMirrorAPIDrift`, `TestMiniSDKSpanMirror` and
`TestMiniSDKMirrorContextMatrix`. The [mirror guide](../../../docs/sdk-span-mirror.md)
describes selection, delivery and the process-retry boundary.

## Dependency-free test assertions and source language

Ported SDK tests retain their inputs and boolean/fatal assertion behavior through
`internal/testassert` and `internal/testassert/require`. These helpers use the
standard library. Keeping external assertion imports in dependency tests would
make a consumer's `go mod tidy` resolve Testify even without runtime imports.
When synchronizing tests, relocate assert/require imports and add any missing
helper behavior with its own regression tests.

The root module declares Go 1.25 and uses native APIs for contexts, test helpers,
string sequences, reflection, maps, random values and `WaitGroup.Go`.
`internal/compat` retains only `AsType` and `Pointer`, the Go 1.26 helpers used
by this port. Keep real Go-version constraints around private `testing` layouts;
do not add a language header to ordinary files. Source manifests retain the
upstream hashes and record each local adaptation.

An older client language is preserved through a temporary workspace: Mini is a
separate main module, and each client's `go` directive still governs its sources.
The workspace keeps the caller's GODEBUG defaults, including explicit overrides.
Native `-mod=mod` first resolves the client's test imports; adding Mini does not
add requirements or raise the client's language. No process-wide environment
variables are changed.

Checks: `TestAssertionResults`, `TestFatalAssertionStopsExecution`,
`TestMiniConsumerAddsOnlyOwnModule`, `TestMiniPreservesConsumerLanguage`,
`TestMiniModModeResolvesClientRequirements` and
`TestMiniPreservesOlderConsumerDependencies`, plus the ported SDK tests on
Go 1.25, 1.26, 1.27 and tip.

Container and OS-release discovery check scanner errors and report them at debug
level. They retain metadata read before a later failure, preserving the SDK's
best-effort behavior. OS-release parsing skips malformed lines without a value.
`TestContainerReadersHandleReadErrors`, `TestContainerReaderStopsAtFirstID` and
`TestOSReleaseHandlesMalformedLinesAndReadErrors` cover these paths; tip's
scanner analyzer checks the error handling with `go vet`.
