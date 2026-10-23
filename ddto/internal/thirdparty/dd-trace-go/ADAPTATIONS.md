# Runtime adaptations to the SDK port

Base: [`870449702d0a0cea26a6223eefe2f0a198069d79`](https://github.com/DataDog/dd-trace-go/commit/870449702d0a0cea26a6223eefe2f0a198069d79).
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
include it. The single shutdown owner and the wait for that owner also apply
to signal-triggered shutdown and to `os.Exit` (see [forced exits](#forced-exits)).

Shutdown takes the ordinary actions under the lock and runs them after
releasing it; upstream ran them holding it. A module registers a new suite's
close action while holding its own lock, and its own close action takes that
lock, so a suite created during shutdown deadlocked the two goroutines.
Actions registered after shutdown took the list do not run, as upstream,
where they waited for the lock and were then cleared.

Checks: `TestCloseActionsKeepLIFOAndRunBarriersFirst`,
`TestConcurrentCloseActionsRunOnce`, `TestCloseActionsRunWithoutTheRegistrationLock`
and the signal-handler tests. Use
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
point. Fractional/negative values keep their semantics.
Timestamps are captured before competing for the lock. They need not be
monotonic between concurrent callers.

CI telemetry emits only counts and distributions; the SDK's gauge and rate
metrics are not ported. Distributions and wire fields have not been redesigned.

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

## Coverage line printed once

Without `-coverprofile`, the pinned SDK takes the session's coverage percentage
from a profile that it writes through testing's tear-down once `M.Run` has
returned. That tear-down also prints `coverage: N% of statements` to
`os.Stdout`, which testing has already printed when `M.Run` ran tests. So with
`-v` or `-json` the line appears twice, and once after `-list`, where go test
prints none. dd-trace-go `main` still does this.

`useTemporaryCoverProfile`, which `InitializeCoverage` calls before `M.Run`,
sets an unset `test.coverprofile` to a file in the temporary directory instead,
so testing writes the profile itself, printing its line once, and
`runtimeCoverageSnapshotLocked` reads that file. A value from the command line,
which `M.Run` may parse later, still replaces it. `Value.Set` leaves the flag
unset for `flag.Visit`, though `flag.Lookup` shows the temporary path. The
file has no directory of its own: a signal can close CI Visibility while tests
still run, and testing exits with status 2 if it cannot create the file.
Process-retry and quarantine children, which write no profile of their own,
keep the tear-down. When a profile is set but testing wrote none, as after
`-list`, with or without `-coverprofile`, the snapshot fails instead of
printing a line. An explicitly empty `-test.coverprofile`, which replaces
ddto's as `M.Run` parses the flags, still takes the tear-down and its second
line: isolated retry coverage, the LCOV upload and the percentage need that
profile. The session's percentage now comes from the profile that
testing writes with go test's `-test.gocoverdir`, the one behind the line it
prints, so with `-coverpkg` it can count statements of selected packages that
the old tear-down, in a new directory, left out. Redirecting `os.Stdout`
around the tear-down instead would race with goroutines that a `TestMain`
leaves printing. Check: `TestMiniPrintsCoverageLineOnce`, with flags that a
`TestMain` parses before ddto sets the profile and that `M.Run` parses after,
which fails without the change. The Testify parity tests drop the pinned SDK's repeated line
(`withoutRepeatedCoverageLine`).

With `-test.list`, testing runs no test and writes no profile, so a file at
`-test.coverprofile` is an earlier run's: the session reports no coverage then,
instead of that file's percentage and LCOV, nor `testing.Coverage()`'s, which
counts only package initialization (`finalizeITRCoverageBackfill` returns no
publication). Checks: `TestRuntimeCoverageSnapshotIgnoresProfileWhenListing`,
`TestNoSessionCoverageWhenListing`.

A `-test.coverprofile` that is not a regular file, such as `/dev/stdout` after
`-args`, is not read back either: reading a pipe that the process holds open
waited forever, after testing had written the profile. The session then takes
`testing.Coverage()`'s percentage. Check:
`TestRuntimeCoverageSnapshotIgnoresIrregularProfile`.
Isolated process coverage is not merged into such a profile either: the merge
read it empty and failed the run with exit status 1, for passing tests.
Check: `TestFinalizeProcessCoverageProfilesSkipsAProfileThatIsNotAFile`.

The directories of per-test and process-retry coverage captures are created by
an absolute path (`mkdirCoverageTemp`): with a relative `TMPDIR`, a test's
`os.Chdir` moved the captures and left the directory behind. Checks:
`TestCoverageTempDirectoryIsAbsolute`,
`TestProcessTestCoverageTemporaryDirectoryIsAbsolute`. When initialization could
not create the per-test directory, text-profile captures wrote relative files
into the test's directory; they now report the error and write nothing. Check:
`TestTextProfileCaptureWithoutTemporaryDirectory`.

## Per-test profile subtraction

`readCoverProfile` and `coveredFilesBetween` in
[`civisibility/integrations/gotesting/coverage/test_coverage.go`](civisibility/integrations/gotesting/coverage/test_coverage.go)
replace upstream `parseCoverProfile` and `getFilesCovered`. Upstream split
each profile line into strings, keyed every block of both profiles with
`fmt.Sprintf`, resolved the relative path of every covered block and regrew a
file's bitmap whenever a block ended further down.

- The profile is read at once and each line is parsed in place. Only a file
  name that differs from the previous line's is copied. The read is sized with
  `Seek`, not `Stat` as in `os.ReadFile`: `Stat` converts timestamps with
  `time.Local`, which coverage workers must not read while a test may change
  it. The parser accepts and skips the same lines as `strings.Fields`, `strings.Split` and
  `strconv.Atoi` did: signs, extra fields and separators, Unicode whitespace,
  invalid UTF-8, `\r\n` and the scanner's maximum line length.
- Blocks keep their order. testing writes a binary's profiles with the same
  blocks in the same order, each file in one run sorted by position. When both
  profiles have that layout, block `i` is compared with block `i`. Otherwise,
  for example with a repeated position, blocks are matched by a struct key with
  upstream's rules: the last duplicate before block wins, and an after block
  without a match counts when its count is positive.
- Each file name is resolved to its relative name once, still only when one of
  its blocks is covered. Files with the same relative name share a bitmap,
  allocated once at the largest covered end line.

Results are identical: the test file first, then sorted names with the same
bitmap bytes. Upstream panicked on a covered block that started below line 1;
lines below 1 are now skipped. With 5,000 blocks, half of them covered,
reading both profiles and subtracting them takes about 2.4 ms instead of
4.9–8 ms on a loaded darwin/arm64 host (one CPU, medians), 367 allocations
instead of 81,140, and 1.35 MB instead of 4.58 MB. File reads dominate what
remains.

Checks: `TestReadCoverProfileMatchesSDKParser` and
`TestCoveredFilesMatchesSDK` compare generated and hand-written profiles with a
copy of the upstream functions, covering both comparison paths, merged names,
repeated and reordered blocks and odd ranges; the ported `test_coverage_test.go`
assertions run unchanged through adapters. `BenchmarkCoverageProfileDiff`
measures both algorithms. Integration: `TestMiniParallelAndRetryCoverageAttribution`,
`TestMiniCoverageWithGlobalTimeChanges` and the coverage parity cases.

## Per-test coverage counters in memory

Upstream captures each test's coverage with testing/internal/testdeps'
teardown before and after the test. Each capture writes meta-data and counter
files, merges them, writes a text profile with one unbuffered `Fprintf` per
unit, and prints `coverage: …` to the test binary's stdout. The per-test
subtraction above then reads both profiles back. In Gin with
`-coverpkg=./... -covermode=atomic`, this took 5.8 of 11.1 CPU-seconds.

When ddto's `testing` overlay registers its hook (`instrument.CoverageHook`),
[`civisibility/integrations/gotesting/coverage/memory_coverage.go`](civisibility/integrations/gotesting/coverage/memory_coverage.go)
reads the counters in memory instead. The hook compiles against the
toolchain's own `internal/coverage` packages and registers through the pushed
linkname `registerTestingCoverage`. It only exposes what those packages hold:

- the decoded meta-data of every registered package: the counter segment
  offsets, each package's counter mode, granularity and path, and each
  function's name, file, literal flag and units;
- a copy of every counter segment, made with atomic loads, and the number of
  registered packages;
- the toolchain's `cformat` rendering of such a copy as a text profile.

Once per process, before the first capture, `buildCoverageLayout` lays out the
text profile that the teardown would write, as its `processPod` and `cformat`
do: units with a parent are omitted, units with the same file, function,
literal flag and position merge, and packages are in import path order, with
each package's blocks sorted by file and position. Each capture then maps the
counter segments to one count per layout entry, adding merged units without
overflowing as `cformat` does. As `cfile` does, it skips a function without a
positive counter before reading its IDs, because a function that starts
running during the copy may have written only part of its header. A test's
coverage is every entry whose count grew, mapped to files and line bitmaps by
the same `coveredLines` code as the text path.

The process keeps text profiles unless the toolchain's rendering with every
count at its maximum reads back through `parseCoverProfileText` (the text
path's own parser, split out of `readCoverProfile`) as exactly the layout's
blocks in order. It also keeps them on 32-bit targets, when the counter mode is
not count or atomic, and when the meta-data has a counter mode or granularity
clash, a file name with a line break, two blocks sharing a position in a file
(where the text path's result depends on map order), or a malformed stream. A
capture whose package count or counter segments do not match the layout, as
when meta-data is registered later, fails its collector and turns the
in-memory path off for later baselines.

`testCoverage`'s capture and load methods choose the backend when the baseline
is taken, so test, retry and process collectors share it. In-memory baselines
are limited to 64 MiB for the whole process. Above that, collectors use text
profiles, and each baseline's reservation is released by its after-capture or
failure. Session coverage, LCOV reports, backfill and aggregate process
profiles keep the teardown.

In-memory captures print no `coverage:` lines, so test processes print native
go test's; upstream's per-test lines also made `go test`'s summary show the
first capture's percentage. `DD_CIVISIBILITY_INTERNAL_COVERAGE_IN_MEMORY=false`
keeps text profiles, and `DD_CIVISIBILITY_INTERNAL_COVERAGE_ORACLE=true` also
computes each in-memory collector's result from text profiles of the same
counters, rendered with the toolchain's `cformat`, and logs whether both agree.

Checks: the unit tests `TestBuildCoverageLayout`,
`TestBuildCoverageLayoutOrder`, `TestBuildCoverageLayoutRefusals`,
`TestCoverageLayoutCounts`, `TestMemoryCoverageMatchesTextProfiles`,
`TestMemoryCoverageEmptyLayout`, `TestMemoryCoverageRefusals`,
`TestMemoryCoverageBudget`, `TestMemoryCoverageCaptureFailure` and
`TestMemoryCoverageProcessSegments`. In integration,
`TestMiniPerTestCoverageInMemory` compares per-test coverage with text
profiles, and with the debug comparison, in count, atomic and race builds, and
checks a build that keeps text profiles; `TestMiniPerTestCoverageRefusesPlugins`
keeps them when a test imports `plugin`; and the coverage parity cases compare
it with the SDK reference. During prototyping, the oracle agreed on all 731
collectors of Gin, Chi and both Testify subjects.

## Per-test file coverage (experimental)

Upstream takes per-test coverage only from Go's coverage counters, so a test
binary built without `-cover` reports none. With ddto's
`--experimental-file-coverage`, the packages of the repository compile with
one mark per source file (see `instrument.MarkFileCoverage` and
[Experimental file coverage](../../../docs/mini-runtime.md#experimental-file-coverage)).

[`civisibility/integrations/gotesting/coverage/file_coverage.go`](civisibility/integrations/gotesting/coverage/file_coverage.go)
is local. It declares:
- `fileCoverageEpoch`, which marks are raised to;
- `fileCoverageBanksV1`, the list of the banks that the instrumented packages
  register while they initialize.

Both have no initializer, because the packages write them through linkname
before this package initializes. The layout version is in the list's name
and in each bank record. ddto checks the declaration before it emits banks,
and a bank of another layout turns file coverage off.

`FileCoverageActive` reports banks, Go's coverage off and coverage upload on.
It extends three places that upstream gates on Go's counters:
- the session's `test.code_coverage.enabled` tag;
- the test wrapper's choice to collect;
- `testCoverage`. The collector takes the test's epoch before the test and
  scans the banks when the coverage is captured, after the test's cleanups:
  a later scan would also see the marks of the tests that start afterwards.
  Its rows are the test file first, then the other files once each, in name
  order, without bitmaps.

`FileCoverageParallel` reports context banks, which
`--experimental-file-coverage=context` builds: a binary built with `-c` has
them too. With them, the test wrapper leaves the parent's barrier alone, so
`t.Parallel()` keeps working. `registerTestCoverageCleanup` then puts
back only a barrier that the test replaced; restoring the nil one it never
took would have turned `t.Parallel()` off for the tests after it.

`CanCollect`, `CanCollectPerTestCoverage`, process retries and quarantined
race processes keep Go's counters. Tests retried in a child process report no
per-test coverage in this mode. Session coverage, LCOV reports and backfill
stay native-only, so file marks never produce a coverage percentage.

Checks: the unit tests `TestFileCoverageWindows`, `TestFileCoverageRows`,
`TestFileCoverageRefusals` and `TestFileCoverageCollector`. In integration,
`TestMiniFileCoverage` compares the rows with Go's counters on a fixture with
several packages, `TestMiniFileCoverageParallel` runs two parallel tests that pass only together,
`TestMiniFileCoverageRefusals` checks the fallbacks, and
`TestMiniFileCoverageTestCache` checks Go's test cache and `-json`.

`--experimental-file-coverage=context` uses context banks instead, together
with ddto's edits of the runtime. See
[Experimental file coverage](../../../docs/mini-runtime.md#experimental-file-coverage).
[`civisibility/integrations/gotesting/coverage/file_coverage_context.go`](civisibility/integrations/gotesting/coverage/file_coverage_context.go)
is local.

**Variables without initializers,** for the same reason as above:
- `fileCoverageWords` and `fileCoverageBanksV2` are reserved and written by
  the banks;
- `fileCoverageUnknown` and the two state functions are initialized by the
  runtime's edit;
- the three hooks are set by this package once it reads the banks.

ddto checks every declaration, with its type, before it emits context banks:
a linknamed variable of another type would corrupt memory.

**`beginFileCoverage` and `scanFileCoverage`** dispatch to
`beginContextCoverage` and `scanContextCoverage` when context banks are
registered, so `test_coverage.go` and the adapted wrapper do not change:
- the begin gives the test's goroutine a context of its own, and makes a new
  window current;
- the capture takes the bits of:
  - every context that held the test, or held a singleflight call that it
    joined;
  - the windows since the test began, or since such a call leaked to them.

  A saturated call gives every file.

**The hooks** intern contexts, call sets and channel unions under one mutex.
They return what the runtime caches: always a context that this package
still holds, so that a cached address cannot come back as another context's.
- **A goroutine without a context** keeps its calls in a call set, and its
  sends carry them. Its calls leak: their joiners read the windows since
  then.
- **A context or union with more than 32 sinks** keeps only its calls, or
  saturates them.
- **A receive that adds sinks** walks the receiver's stack and sets its
  files' bits. If the walk does not reach `runtime.goexit`, or meets a frame
  of an instrumented package whose file maps to no bank, it sets every bit
  instead.
- **Retention:** a context or call set is dropped once all its sinks are
  dead. A retired call dies once no live test and no call in flight reaches
  it. Windows go once no live test or leaky call needs them.

**`fileCoverageEnter`, `fileCoverageLeave`, `fileCoverageJoin` and
`fileCoverageComplete`** are called through linkname by ddto's edits of
singleflight:
- before the banks are read, while packages initialize, they do nothing;
- the leader's sinks join the call, so its function's work counts for them;
- a call that a goroutine without a context joined publishes its files to
  the window before its waiters return.

Checks:
- **Unit tests:**
  - `TestContextCoverageAttributesToEachTest`,
    `TestContextCoverageMergesChannelUnions`,
    `TestContextCoverageKeepsCallsAcrossUnknownWorkers`,
    `TestContextCoverageIOKeepsCalls`;
  - `TestContextCoverageOverflow`, `TestContextCoverageOverflowSaturates`,
    `TestContextCoverageRetention`, `TestContextCoverageReclaimsCallCycles`;
  - `TestContextCoverageUnknownJoiner`,
    `TestContextCoverageUnknownJoinerGetsLeakyWindows`,
    `TestContextCoverageSingleflightBeforeReady`,
    `TestContextCoverageNestedCalls`, `TestContextCoveragePanickingLeader`,
    `TestContextCoverageRepairsTheStack`, `TestSymbolPrefix`.
- **Integration tests:** `TestMiniFileCoverageContext`,
  `TestMiniFileCoverageContextSingleflight` and
  `TestMiniFileCoverageContextFlows` run parallel tests that must not see each
  other's files, and must get the files that ran for them.

## Coverage module identity

Upstream `InitializeCoverage` in
[`civisibility/integrations/gotesting/coverage/test_coverage.go`](civisibility/integrations/gotesting/coverage/test_coverage.go)
runs `go list -f {{.Module.Path}};{{.Module.Dir}}` in every binary built with
coverage, even when settings disable per-test coverage. Only per-test coverage,
the LCOV report and profile backfill read the result. Here initialization
records the working directory, environment and `go` executable instead, and
the first reader runs `go list` with them, so a later directory or environment
change in `TestMain` or a test does not alter the module. A run that reads no
module identity starts no `go` process.

Per-test coverage still resolves the module during initialization. Its worker
runs while tests do, and starting a process reads state that tests may change:
`os.StartProcess` reads `time.Local`, which `TestMiniCoverageWithGlobalTimeChanges`
changes under `-race`. The ITR backfill preflight runs before the tests, and
the LCOV report and backfill finalization after them.

A test process that ddto hands its state takes the module from the state's
`tests.json` instead (`cliModuleInfo`, which is
`utils.CLITestPackageModule`; see
[Requests from ddto's CLI](#requests-from-ddtos-cli)), and starts no `go`
process. ddto writes each test package's module from its own package query.
A directory that the state does not describe, or a module without a
directory, still runs `go list`.

Checks: `TestModuleInfoResolvesOnFirstUseFromInitialization`,
`TestMiniCoverageWithGlobalTimeChanges`, `TestModuleInfoFromCLIState`,
`TestCLITestPackageModule` and
`integration/TestMiniCLIStateGivesModuleToCoverage`.

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

The startup requests can still leave work in Go's transport. They share the
settings client's connection pool, and a request can take a connection that
another one released while its own dial still runs. That dial then finishes in
a goroutine of the transport, which adds the connection to the idle pool and
calls `time.Now`, so it reads `time.Local` after the first test may have
started. On 2026-10-14 the race detector caught it once in the clock
integration, with ordinary delivery and telemetry. Deferred mode already closes
the idle connections after every request. Ordinary mode now does it once:
`instrumentTestingMWithOptions` in
[`civisibility/integrations/gotesting/testing.go`](civisibility/integrations/gotesting/testing.go)
calls `cidelivery.CloseConnections` after the last startup request and before
the first test. `CloseIdleConnections` cancels dials that no request waits for,
and a dial that completes afterwards finds the transport closing new idle
connections, so it closes its connection without reading the clock. A dial
that pooled its connection earlier did so under the pool's mutex, which the
close then takes, ordering that read before the tests. The next request
reconnects, which costs a TLS handshake for Agentless delivery.

That close covers plain HTTP, as to a local Agent: closing a TCP connection
reads no clock. A TLS dial that finishes late still reads it, in
`tls.Conn.Close` and when it sets up an HTTP/2 connection, and net/http offers
no way to wait for that dial. `createNewHTTPClient` in
[`civisibility/utils/net/http.go`](civisibility/utils/net/http.go) therefore
sends requests whose connection uses TLS, to an https destination or through an
https proxy, through a transport with `MaxConnsPerHost: 1`. Concurrent requests
share one HTTP/2 connection instead of each dialing its own, so no extra dial
exists: five concurrent requests made one dial instead of five, in the same
time. From Go 1.26, `HTTP2Config.StrictMaxConcurrentRequests` also makes a
request that finds the connection's streams in use wait for one instead of
dialing; Go 1.25 has no such setting, so a server that limits concurrent
streams below the startup requests can still cause an extra dial there. Plain
HTTP keeps concurrent connections, because HTTP/1.1 would serialize the
requests. Two more cases remain. An HTTPS endpoint that answers only HTTP/1.1
serializes its requests. With plain HTTP, a request that starts after
the close, such as one from the repository upload that ordinary mode runs in
the background, lets a late dial pool its connection again; in ordinary mode
those background requests read the clock during tests anyway, which deferred
mode avoids.

One case remains open by decision. net/http detaches a dial from its request
(`context.WithoutCancel`), so a startup request that gives up while its
connection is still being established leaves that dial running; telemetry gives
up after 5 seconds, while a TLS handshake may take 10. `CloseConnections`
cancels the dial, but cannot wait for it: a dial that has just finished its
handshake still sets up HTTP/2 or closes the connection afterwards, reading
the clock while the first test may change `time.Local`. It happens only when
a startup request expires before its connection is established, most likely on
a slow or failing network, although a long pause of the process can cause it
too; the race detector reports it only in a test that changes `time.Local`
under `-race`. Aborting such dials ourselves would
mean doing the TLS handshake in `DialTLSContext` and would not cover the
handshake that follows a proxy's `CONNECT`; the user chose to document the case
instead (2026-10-14).

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
`internal/cidelivery/TestCloseConnectionsKeepsLateDialsOutOfTheIdlePool` holds a
dial whose request took another request's connection, and checks that closing
the CI connections cancels it, or closes its connection when it had already
connected; it follows that connection by its address.
`TestCITransportDialsOnceForConcurrentHTTPSRequests` sends concurrent requests
to an HTTP/2 server and expects one dial;
`TestCITransportWaitsForHTTP2Streams`, from Go 1.26, does it with a one-stream
server; `TestCITransportRoutesTLSConnections` checks which requests use the
limited transport.
`TestMiniStartupOverlapsSettingsAndTelemetry` checks that the connections close
before the first test.
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

## CI HTTP retries

Upstream `RequestHandler.SendRequest` in
[`civisibility/utils/net/http.go`](civisibility/utils/net/http.go) makes
`MaxRetries + 1` attempts and backs off after every failed attempt, including
the last, before returning "max retries exceeded". With the default three
retries and 100 ms backoff, an unreachable agent or intake held each request
for 1.5 s, 800 ms of it after the final attempt. The settings request runs
before the first test, so every binary paid that wait at startup, and again in
the repository upload that a close action waits for.

Here `retryBackoff` and the rate-limit wait run only when another attempt
follows. Attempt counts, delays between attempts, response handling and
errors are unchanged. The debug summary's `retry` field now reports whether
another attempt follows. `retrySleep` performs every wait so tests can record
the delays.

A 429 response's `x-ratelimit-reset` header, a Unix timestamp or a number of
seconds, set the wait before the next attempt without any bound, so a distant
reset could hold settings, and the first test, indefinitely. The wait is now
honored only up to 60 seconds, the bound the test-cycle client applies to
`Retry-After`; a later reset uses the ordinary exponential backoff, as an
absent or invalid header does. Telemetry has no rate-limit wait.

Checks: `TestSendRequestWaitsOnlyBetweenAttempts` covers network errors, 5xx,
429 without a reset header and unexpected response formats;
`TestSendRequestBoundsRateLimitResetWait` covers resets within and beyond the
bound, as seconds and as a timestamp;
`TestSendRequestRateLimitResetWaitsOnlyBetweenAttempts` covers the
`x-ratelimit-reset` wait. The ported `TestRateLimitHandlingWithRetries`,
`TestRateLimitHandlingWithoutResetHeader` and
`TestSendRequestWithMaxRetriesExceeded` keep their lower bounds for the waits
between attempts and now also bound the total from above.

## Final tracer delivery at exit

Upstream `exitCiVisibility` in
[`civisibility/integrations/civisibility.go`](civisibility/integrations/civisibility.go)
calls `tracer.Flush()` and then `tracer.Stop()`, after the session close has
already flushed. Mini's `Stop` closes the process-wide client, and
`Client.Close` in [`internal/minitracer/batch.go`](../../minitracer/batch.go)
seals the open batch and delivers every queued batch directly, in both delivery
modes, before it abandons what still fails. The separate `Flush` delivered the
same queue: on success it found nothing to send, and after a failure it
repeated the retries and the `FlushTimeout` bound that `Close` then repeated
again. Exit now calls only `Stop`, so events finished after the session closed
are still delivered, once.

With an agent that refused connections, the exit's two delivery cycles took
about 303 ms each; against a blackholed intake each is bounded by the
10-second `FlushTimeout`. The session close's own flush and its diagnostics are
unchanged.

Checks: `TestExitCiVisibilityDeliversRemainingEventsOnce` delivers an event
finished after session close and sees three attempts, not six, against a
failing intake; `integration/TestMiniDeliveryFailurePreservesGoExit` keeps the
flush and close error diagnostics.

## Base-branch discovery

Upstream `GetBaseBranchSha` in [`civisibility/utils/git.go`](civisibility/utils/git.go)
runs when impacted-test detection is enabled and no pull-request base commit is
known. Without a pull-request base branch, `checkAndFetchBranch` checks each of
the seven `possibleBaseBranches` in turn: `git show-ref` for its
remote-tracking ref and, when that is missing, `git ls-remote --heads <remote>
<branch>`, a network round trip, before a `git fetch --depth 1` of a branch the
remote has. Branches that never exist, such as `preprod` or `trunk`, were listed
again in every test binary, before its first test.

`checkAndFetchBranches` keeps the `show-ref` checks, then lists every missing
branch with one `ls-remote` and fetches, in candidate order, the branches it
returned. Fetching one named branch updates only that branch's remote-tracking
ref, so checking all branches first sees the refs that the per-branch order
saw. The pull-request base branch uses the same function with one branch.

Patterns and output use exact `refs/heads/<name>` refs. An `ls-remote` pattern
matches the tail of a ref, so upstream's `master` also matched
`refs/heads/feature/master`, and the combined output could contain a warning;
either made upstream try a fetch that git rejects. Those failing fetches no
longer run. Successful fetches, their order, the remote-tracking refs and the
candidates that `findBestBranch` compares are unchanged. The CI telemetry
`git.command` count for `ls_remote_heads` is now one per discovery instead of
one per missing branch, and the rejected `fetch` commands are not counted.

Measured against GitHub over SSH, one package with impacted tests enabled and
`refs/remotes/origin/main` present, 5 alternating runs: 6 `ls-remote` calls
(about 0.55 s each) became 1, and the test binary's duration fell from 4.60 s
to 1.49 s (medians).

Checks: `TestCheckAndFetchBranchesListsMissingBranchesOnce`,
`TestCheckAndFetchBranchesMatchesPerBranchAlgorithm` (an upstream-algorithm
oracle on a second clone of the same remote must leave the same refs and base
SHA) and `TestCheckAndFetchBranchFetchesPullRequestBase` run real git against
a `file://` remote. `TestGitRecorderHelperProcess` records the commands.

With `git.pull_request.base_branch`, the SDK passes the bare branch name to
`git merge-base`, which finds only a local branch: a pull request's checkout,
as GitHub Actions makes it, has the base only as `origin/<base>`, so the base
SHA was not found and impacted-test detection found no modified files. Here
the candidate is `<remote>/<base>` when that remote-tracking ref exists,
checked without telemetry. Check: `TestGetBaseBranchShaUsesRemotePullRequestBase`.

## Pull-request head commit

Upstream `fetchCommitData` in [`civisibility/utils/git.go`](civisibility/utils/git.go)
reads the pull-request head commit that the CI provider names, for example
GitHub's `pull_request.head.sha` or GitLab's merge-request source SHA. In a
shallow checkout it first looks up the remote and runs `git fetch
--update-shallow` of that commit, synchronously during bootstrap. Nothing
checked whether the commit was already present, and a shallow checkout stays
shallow after the fetch, so every test binary repeated the network fetch.

After the existing shallow and Git-version checks, `commitObjectExists` runs
`git cat-file -e <sha>^{commit}`. When it succeeds, the remote lookup and fetch
are skipped and `git show` reads the commit as before. A missing commit is
fetched exactly as upstream does. The `cat-file` command records no command
telemetry; the skipped remote lookup and `fetch` are no longer counted. In a
partial clone, git may fetch a missing object while resolving `^{commit}`.

Parallel binaries that both miss the commit still fetch concurrently and can
contend for `.git/shallow.lock`; the upstream tests already tolerate that
error. The check removes the fetch from every binary that starts after one
succeeded.

Measured with a depth-1 GitHub clone over SSH, a GitHub pull-request event
whose head commit is present and one package, 5 alternating runs: bootstrap
807 ms -> 186 ms and test binary duration 1.34 s -> 0.81 s (medians); the
fetch alone took 616-1024 ms.

Checks: `TestFetchCommitDataFetchesOnlyMissingCommits` reads a present head
commit without a fetch and still fetches a missing one, with real git.

## OS metadata

Upstream [`osinfo`](osinfo/osinfo_unix.go) detects OS metadata in its package
`init`: `uname`, `/etc/os-release` and, on macOS, a `sw_vers -productVersion`
process. Every test binary and process-retry child paid for that process,
including those that never report OS metadata.

Here the platform's `detect` runs on the first accessor call, once, under a
`sync.Once`. In a CI test binary that is the tag bootstrap, before any test.
On macOS, [`osinfo_darwin.go`](osinfo/osinfo_darwin.go) reads the same value
from the `kern.osproductversion` sysctl. `SYSTEM_VERSION_COMPAT` can change
`sw_vers`' answer, so a process with that variable set still runs `sw_vers`, as
does a failed or empty sysctl. A failed `sw_vers` still skips the kernel
metadata, as upstream does. Windows' registry read is also deferred to first
use. Should a first use happen in a test, only the macOS fallback starts a
process.

Measured on macOS 26.6.2, the osinfo test binary running the kernel metadata
test, 30 alternating runs: 29.4 ms -> 16.2 ms (medians).

Checks: `TestOSMetadataLoadsOnFirstUse` proves that a fresh process detected
nothing during initialization; `TestMacOSProductVersionSources` covers the
sysctl, compatibility, error and empty cases; `TestMacOSProductVersionMatchesSWVers`
compares the sysctl with `sw_vers` on the host.

`readOSRelease` uses `VERSION_ID` when `/etc/os-release` has no `VERSION`, as
on Alpine. The SDK's fallback checked for an empty version, but the version
starts as `unknown`, so it never applied. Check: `TestOSReleaseVersionIDFallback`.

## CI log hostname

Upstream `logs.Initialize` in
[`civisibility/integrations/logs/logs.go`](civisibility/integrations/logs/logs.go)
sets the log entries' hostname from `hostname.Get()` and falls back to
`os.Hostname()` when it is empty. Nothing in the CI runtime fills that cache
first, so the call returns an empty string and starts `updateHostname` in a
goroutine: GCE, Azure and EC2 metadata requests and `/bin/hostname -f`, with
timeouts of up to a second each. Their result was never read, and the
goroutine, which the goleak shim does not filter, could still be running when a
test checked for leaks.

Here `Initialize` reads `os.Hostname()` directly, the value upstream's first
initialization used. A second initialization in the same process, after
`Stop`, also keeps that value instead of a probed name the earlier goroutine
might have cached. Telemetry still uses `hostname.Get()` only when
`os.Hostname()` fails.

Checks: `TestInitializeUsesOSHostnameWithoutProbes` initializes logs in a fresh
process, requires the OS hostname and finds no hostname-discovery goroutine.

## CI provider tags

`getProviderTags` in [`civisibility/utils/ci_providers.go`](civisibility/utils/ci_providers.go)
differs from the pinned SDK in three places, each of which could stop or
change the CI tags:

- The SDK ranges over the `providers` map and keeps the last provider whose
  variable is set, so with two variables each process could pick another one.
  CodeBuild's extractor returns no tags outside a CodePipeline build, which
  would erase another provider's tags on a CodeBuild-hosted runner. Here the
  providers are tried in name order, and the first whose tags name a provider
  wins.
- GitLab's `CI_COMMIT_AUTHOR` is split at `<` instead of indexing the fields of
  `strings.FieldsFunc`, which panicked when the variable was missing or the
  email empty (`Name <>`).
- Jenkins's branch is quoted with `regexp.QuoteMeta` before it is removed from
  the job name: a branch such as `feature/(cleanup` panicked in
  `regexp.MustCompile`, and `.` matched any character.

- GitHub Actions' `GITHUB_HEAD_REF` names a pull request's branch: a name
  that holds `tags/`, such as `feature/tags/filter`, was reported as a tag and
  lost its branch. Only `GITHUB_REF` names a tag here, with its `refs/tags/`
  prefix, so a push to such a branch keeps it too.
  Azure Pipelines, Jenkins and Codefresh had the same check: their ref names
  a tag only as `tags/<name>` once a `refs/heads/`, `refs/` or `origin/` prefix
  is removed, as the shared fixtures' `refs/heads/tags/0.1.0` and
  `origin/tags/0.1.0` still do.

- Outside a pull request, `pr.number` was `0` on GitHub Actions, whose push
  event JSON has no number, and `false` on Buildkite and Travis CI, which set
  `BUILDKITE_PULL_REQUEST=false` and `TRAVIS_PULL_REQUEST=false`. None is
  reported now.

Check: `TestProviderTagsFromUnusualValues`, which fails without each change.

## Credentials in requests and debug lines

The CI HTTP client in [`civisibility/utils/net/http.go`](civisibility/utils/net/http.go)
refuses redirects, as `internal/citransport` does for events: Go keeps a
custom header such as `dd-api-key` when it follows a redirect to another host.
Debug lines show URLs through `logURL`, without user information or query,
also the coverage report upload's in `coverage_report.go` (check:
`TestCoverageReportLogsNoCredentials`), and Git's debug lines in [`civisibility/utils/git.go`](civisibility/utils/git.go)
pass its arguments and output through `filterSensitiveInfo`: a CI clone's
remote URL can hold a job token, which `git ls-remote --get-url` prints.
The debug line of a request with a body shows its size, not its contents,
which the SDK printed up to 4 KB of: a logs payload holds the tests' output.
Check: `TestRequestDebugLinesHideTheBody`.
The test command's and the session name's debug lines in
[`civisibility/utils/environmentTags.go`](civisibility/utils/environmentTags.go)
say where the values come from, not the values, which hold the test's
arguments and so any token passed as one. Check:
`TestDebugLinesLeaveOutTheTestCommand`.
A request's error, an `*url.Error` that keeps the URL's user and query, goes
through `logError`, and `AgentURLFromEnv`'s warnings in
[`agent.go`](agent.go) leave out the URL of `DD_TRACE_AGENT_URL`. A URL that
a request cannot be built from, such as a malformed agentless URL, returns
its error through `redactURLError`, since callers log it, and telemetry's
writer in [`telemetry/internal/writer.go`](telemetry/internal/writer.go) keeps
a failed request's error the same way.
Checks: `TestCIClientDoesNotFollowRedirects`, `TestLogURL`, `TestLogError`,
`TestRequestErrorHidesURLCredentials`,
`TestWriterRequestErrorHidesURLCredentials` and
`TestGitDebugLinesHideRemoteCredentials`.
The URLs of `DD_TEST_OPTIMIZATION_ENV_DATA_FILE`'s file, which
`applyEnvironmentalDataIfRequired` (`utils/file_environmental_data.go`) adds
after `normalizeTags`, lose their credentials too (`filterSensitiveInfo`).
Check: `TestApplyEnvironmentalDataHidesURLCredentials`.
`UDSClient` ([`uds.go`](uds.go)) takes no proxy: it dials the Agent's socket
for every request, where an HTTPS or SOCKS proxy from the environment made the
client negotiate with the Agent. Check: `TestUDSClientUsesNoProxy`, in the
local `uds_local_test.go`.

## LCOV paths of nested modules

`lcovModuleSourceFilePath` in
[`gotesting/coverage/lcov_report.go`](civisibility/integrations/gotesting/coverage/lcov_report.go)
joins a module-relative path to the module's directory in the repository.
The SDK derives that directory from the module path's major version suffix
alone, so the files of a nested module, such as `services/api` with module
`github.com/acme/project/services/api`, were reported as `handler.go`. Here the
module directory relative to the CI workspace comes first, when the module
lies inside it. Check: `TestWriteLCOVReportFromProfileUsesNestedModuleDirectory`.
A path that the module resolves skips the filter for unresolved import paths,
which dropped a directory whose name has a dot, such as `api.v2`. Check:
`TestWriteLCOVReportFromProfileKeepsDottedModuleDirectories`.

ITR's coverage backfill (`coveragePathCandidates` in
[`gotesting/coverage/profile_backfill.go`](civisibility/integrations/gotesting/coverage/profile_backfill.go))
looks a profile file up in the backend's coverage by several paths and takes
the first one found. The path after the module path came before the module
directory's path in the repository, so a nested module's `handler.go` took the
coverage of a `handler.go` at the repository root when both existed, or when
only the root one had coverage. A file of the module now has the one path that
the module's directory in the CI workspace gives (`moduleRepositoryFile`); the
other paths remain for other files and without that directory. Check:
`TestBackfillPrefersNestedModuleRepositoryPath`.

## Benchmark context outside the measurement

The benchmark wrappers in `gotesting/testing.go` and
`gotesting/instrumentation_orchestrion.go` bound Mini's context to the
benchmark after `b.StartTimer()`: with the SDK mirror active, its allocations
counted in the first, and with `-benchtime=1x` only, run (4 allocations and
168 B for an empty benchmark). The binding now happens before the timer
restarts. Check: `TestMiniSDKMirrorBenchmarkMeasuresNoBinding`.

## Exit status of a TestMain without os.Exit

Go's generated main returns `m.exitCode` after a `TestMain` that does not call
`os.Exit`. Process retries run after `M.Run`, so the code that they produce
must reach that field too. ddto's `M.Run` hook (`internal/instrument`) stores
it there; the manual `(*M).Run` in `gotesting/testing.go`, behind
`testopt.RunM`, returned it without storing it, so `testopt.RunM(m)` alone
exited 0 for a test that still failed after its retries.
`setTestingMExitCode` stores it through a checked reflection lookup; the
`defer` replaces a comment line to keep stack lines. Checks:
`TestMiniTestMainWithoutExitKeepsRetryStatus` and
`TestMiniManualRunMWithoutExitKeepsRetryStatus`.

## Output of failed retry attempts

A fresh retry attempt in `gotesting/retry_attempt_runner.go` reports to a
synthetic parent, as a subtest reports to its test, and that parent is never
reported. With `-v`, each attempt streams its lines as it runs. Without it,
testing prints only a failed test's buffered output, so a test that still
failed after its automatic retries printed its `--- FAIL` line without its
messages. `reportRetryAttemptToOriginal` moves a failed attempt's output to
the original test, which testing reports, once per failed attempt. The call
is edited in place to keep its stack lines. An attempt that panics skips that
end: `snapshotPanickedRetryAttemptResult`, which replaces its snapshot call in
place, moves its output the same way and also counts the time that its body
ran, which dynamic automatic test retries read to choose a budget and which
stayed zero. Checks: `TestMiniRetriedPanicKeepsItsOutputAndDuration` and
`TestRetryAttemptPanicKeepsItsDuration`. Process retries run after testing
has reported the package, and the README describes their summary. Check:
`TestMiniRetriedFailureKeepsItsOutput`.

## Automatic retries that end in a skip

With automatic test retries, testing gets the last attempt's result: for a
test that failed and whose retry then skipped, that was a skip, and the
process exited 0, while its event's final status was `fail`. No attempt
passed, so the test now fails, in `gotesting/instrumentation.go`, edited in
place to keep its stack lines, and for process retries in `packageFailed`
(`gotesting/retry_process_deferred.go`). Check:
`TestMiniRetryThatSkipsAfterFailureFails`.

## Races in retry child processes

With process retries, a race that a retry child detected stops the retries,
but under Early Flake Detection an earlier execution that passed made the
group pass: this process's race detector never saw the child's race, so go
test exited 0. `applyCompletedAttempt` now records it as the group's terminal
failure, which fails the package and the final status, unless the test is
quarantined or disabled. A child reports any race that its detector found
(`buildResult` in `gotesting/retry_process.go`): testing reports a race in a
subtest for the subtest only, and the child runs one test. Checks:
`TestRaceOnRetry` and `TestRaceInSubtestOnRetry` in
`TestMiniRaceInstrumentsClientOnly`.

## Skips after a failure

`instrumentCloseAndSkip` and `instrumentSkipNow` in
`gotesting/instrumentation_orchestrion.go` closed a test as skipped when it
called `t.Skip` or `t.SkipNow`, also after `t.Error`: testing reports such a
test as failed. A test that has already failed is left to its normal
finalization, which closes it as failed. A test that skips is left to it too,
with its reason: a call that the test deferred can still fail it, as a leak
check can, and testing then reports it as failed. A benchmark is too: its
wrappers, in `gotesting/testing.go` and `gotesting/instrumentation_orchestrion.go`,
close a skipped one with its final status and reason in
`closeSkippedBenchmark`, after its deferred calls. Check:
`TestMiniSkipAfterFailureReportsFailure`, with tests, benchmarks and a
sub-benchmark.

## Failures in quarantined child processes

In a quarantined child process, `markProcessRetryChildFailed` in
`gotesting/retry_process.go` records a test's own failure as unmasked, so a
managed descendant's restore of its ancestors keeps it. testing's
`common.Fail` hook passes the failing test's `*testing.common`, which the
function ignored, accepting only a `*testing.T`: a quarantined parent that
called `t.Error` before a quarantined subtest was reported as passed once
that subtest finished. The failure is now recorded for any receiver; only a
`*testing.T` still gets the parent-first propagation, which `Fail` does
itself for the hook. Check: `TestProcessRetryChildFailureThroughCommon`.

## Package flags in retry children

A process-retry child gets its own values of testing's flags, such as the run
pattern and the count. The table of those flags in `gotesting/retry_process.go`
also held their names without the `test.` prefix, which the test binary does
not define: a package's own flag named `-run` or `-timeout`, which `-args`
passes, was taken for testing's and left out of the child, or became its run
pattern. Only `test.`-prefixed names are testing's here. Check:
`TestProcessRetryKeepsPackageFlagsWithoutTestPrefix`.

## Artifact options of retry children

`processRetryArtifactPolicyFromArgs` in `gotesting/retry_process.go` reads the
`-test.artifacts` and `-test.outputdir` options that a process-retry child gets
back. It stopped at the separate value of another flag, such as
`-args -test.parallel 1 -test.artifacts`, so the child ran without artifacts
and Go removed the files that its tests wrote. A registered flag's separate
value is skipped here, as `processRetryTimeoutFromArgs` does. Check:
`TestProcessRetryArtifactPolicyAfterSeparateValues`.

## Early Flake Detection terminal state being published

`publishTerminal` in `gotesting/efd_faulty_session.go` creates the session's
terminal-state file, then writes `faulty` or `unavailable` into it. Another
process that read the empty file in between took it as an unknown state:
Early Flake Detection became unavailable for its new tests for the rest of its
run, and its session could lose `test.early_flake.abort_reason=faulty`. An empty file
now reads as not yet published; the claim then takes the lock and counts, as
without the file. Check: `TestEFDFaultySessionTerminalBeingPublished`, in the
local `gotesting/efd_faulty_session_terminal_test.go`.

## Sub-benchmarks sharing a callback

`instrumentTestingBFunc` marked the original callback of a sub-benchmark as
instrumented, as well as its wrapper: in a table such as
`b.Run("a", work); b.Run("b", work)`, only the first sub-benchmark had an
event. Only the wrapper is marked here; the inner `b.Run` that the measuring
level makes still finds it. The event was also named from the name passed to
`b.Run`, before testing makes it unique and rewrites it: a repeated `b` and
`"input size=1"` gave `b` twice and a name with a space, where testing prints
`b#01` and `input_size=1`. The event is now created in the sub-benchmark, with
its own `b.Name()`. Check: `TestMiniSubBenchmarksSharingACallback`.

## Benchmark panics

The benchmark wrappers (`executeInternalBenchmark` in `gotesting/testing.go`
and `instrumentTestingBFunc` in `gotesting/instrumentation_orchestrion.go`)
set the panic handler only after `b.Run` returned, and replaced the wrapper
with the original function for the later runs: a panic in any run closed no
event and lost the process's buffered ones. The handler is now defined before
the benchmark runs, and the later runs use `recoveringBenchmarkFunc`
(`gotesting/testingB.go`), which handles a panic and panics again. The
handler marks the event as a benchmark's first, which `SetBenchmarkData` did
only after the run. Check: the `benchmark` cases of `TestMiniCrashKeepsEvents`.

## Benchmarks that TestMain selects

`instrumentTestingMWithOptions` (`gotesting/testing.go`) instruments the
benchmarks only when `-bench` is in `os.Args`: a `TestMain` that selects them
with `flag.Set("test.bench", …)` ran them without events. `benchmarkArgs`
(`gotesting/testingB.go`) adds `test.bench` when the flag has a value. Check:
the `benchmain` package of `TestMiniSubBenchmarksSharingACallback`.

## Module paths in coverage file names

`getRelativePathFromCITagsSourceRootForCoverage`
(`gotesting/coverage/test_coverage.go`) replaced every occurrence of the module
path with the module directory: module `app`'s `app/app.go` became
`<dir>/<dir>.go`. `moduleFilePath` replaces only the leading module path.
Check: `TestModuleFilePath`.

## Testing values without metadata

`getTestMetadata` and `deleteTestMetadata` (`gotesting/instrumentation.go`)
called `reflect.Value.UnsafePointer` on any `testing.TB`, which panics for one
that is no pointer, as a struct that embeds a test, passed to
`testopt.Context`. `testPointer` gives such a value no metadata, so the
native context comes back. Check: `TestTestMetadataOfAValueTB`.

## Coverage rows of trimmed test files

With `-trimpath`, the runtime names a test's source file by its import path,
such as `github.com/acme/project/pkg/example_test.go`. Per-test coverage
(`NewTestCoverage` in `gotesting/coverage/test_coverage.go`), process-retry
coverage and experimental file coverage made it relative to the CI workspace
only, which kept that name for the test's own row. `coverageSourceFile`
resolves it through the module's directory, as `moduleRepositoryFile` does for
backfill: `github.com/acme/project/api` in `services/api` gives
`services/api/example_test.go`. Other files resolve as test events' source
files are, to `pkg/example_test.go`.
Check: `TestTestCoverageNamesTrimmedTestFileInRepository`.

## Known-test pages

`GetKnownTests` in `utils/net/known_tests_api.go` requests the next page while
the response says `has_next`, with the response's cursor. A cursor that was
empty, or one already sent, as in a cycle `A`, `B`, `A`, requested the same
pages forever and kept appending their tests. Such a response now ends the request with an error, as
other invalid responses do. Check: `TestKnownTestsApiRequestRejectsAStuckCursor`.

## Logs intake limits

`integrations/logs/logs_writer.go` sent a payload once it passed 25 MB, from a
50 MB limit whose comment says 5 MB, and without counting entries. The logs
intake rejects a request over 5 MB uncompressed or with more than 1000
entries, so a test process that logged that much lost the whole batch after
its retries. The limit is 5 MB here, a payload is sent from 2.5 MB, and at
1000 entries. Check: `TestLogsWriterRespectsIntakeLimits`. The writer checked
the size only after adding an entry, so a line of a few megabytes, larger
still once JSON escapes it, could take a payload past 5 MB. `add` now encodes
the entry first, through `logsPayload.pushEncoded`, and starts the next
payload when the entry would not fit. Check:
`TestLogsWriterSendsLargeEntryInTheNextRequest`. An entry over 5 MB by itself,
which the intake would reject even alone, is not sent; a debug line says so.
Check: `TestLogsWriterDropsAnEntryOverTheLimit`.

## Logs without a client

`newLogsWriter` in `integrations/logs/logs_writer.go` built a writer around
`net.NewClientForLogs()`, which is nil in agentless mode without an API key:
with `DD_CIVISIBILITY_LOGS_ENABLED`, the first payload's send dereferenced nil
and a passing test process ended with a panic. Without a client there is no
writer, so logs are not sent, as no other event is. Check:
`TestLogsWriterWithoutClient`.

## Module directory and profile rewrites

`deferModuleInfo` in `gotesting/coverage/test_coverage.go` records where to
resolve the module of the tested package when coverage initializes, in
`M.Run`. A `TestMain` that changes directory first, as to the repository root,
made it resolve another module, and the coverage files' paths with it. The
directory is now the one that the process started in, read as the package
initializes (`testPackageDirectory`). Check:
`TestModuleInfoUsesTheStartingDirectory`.

`orderedCoverProfile.writeAtomic` in `gotesting/coverage/profile_backfill.go`,
which ITR backfill and retry merges use to rewrite the coverage profile,
renamed a temporary file over it: a profile path that is a symbolic link, which
testing writes through, became a regular file, and the profile's mode became
the temporary file's 0600. It now writes beside the link's target and keeps the
profile's mode. Check: `TestProfileRewriteKeepsLinkAndMode`.

## Nil subtest callbacks

`instrumentTestingTFuncWithSourceOptions` in
`gotesting/instrumentation_orchestrion.go`, which the `T.Run` hook calls before
testing decides whether to run the subtest, read the callback's function
first: `t.Run("excluded", nil)` panicked, where testing skips the subtest that
`-skip` excludes without calling it. A nil callback is returned unchanged, on
the kill-switch line, which keeps the file's stack lines. Check:
`TestMiniSkipAfterFailureReportsFailure`.

## Partial lines of t.Output

`collectAndWriteLogs` in `gotesting/testing.go` ends a partial line that a test
wrote to `t.Output()`, so that the logs integration reads it. It did so also
with logs disabled, before the test's cleanups, which can continue the line:
`hello ` from the test and `world` from a cleanup printed two lines instead of
go test's one. The line is ended only when logs are collected. Check:
`TestMiniKeepsPartialOutputLines`.

## Long log lines

`collectAndWriteLogs` in `gotesting/testing.go` reads a test's output with
`bufio.Scanner`, whose default limit is 64 KiB per line: with
`DD_CIVISIBILITY_LOGS_ENABLED`, a longer line ended the scan, and it and every
later line of that test were not sent. Each scanner's buffer can now hold the
whole output. Check: `TestCollectLogsKeepsLongLines`.

## Sub-benchmark patterns

The SDK's `B.Run` hook (`instrumentTestingBFunc` in
`gotesting/instrumentation_orchestrion.go`) renames each sub-benchmark to a
measuring level, `[DD:TestVisibility]`, which runs the real one inside it.
testing matches `-bench` and `-skip` against that level's name: with
`-bench='^BenchmarkWork$/^child$'` the child never ran, and with
`-skip='^BenchmarkWork$/^child$'` the measuring level ran without its child
and the hook dereferenced the missing result. Here a `-bench` or `-skip` value
with `/`, which selects sub-benchmarks by name (`subBenchmarkPatterns`, in
`testing_session_close.go` so that no import moves the file's lines), leaves
sub-benchmarks uninstrumented, as without CI Visibility, and a sub-benchmark
that testing did not run closes as skipped. The added lines come after every
line that error stacks show. Check: the `benchmarks` case of
`TestMiniTestingCompatibility`.

## Commit messages

`getLocalGitData` and `readCommitData` in
[`civisibility/utils/git.go`](civisibility/utils/git.go) split `git log`'s
fields at `","` into at most eight parts: the message comes last and may hold
that separator, which `strings.Split` cut it at, as for `Update ["a","b"]`.
Check: `TestCommitMessageWithFieldSeparator`.

## Impacted-test diff

`GetGitDiff` in [`civisibility/utils/git.go`](civisibility/utils/git.go)
runs `git -c core.quotePath=false -c diff.relative=false diff --no-color
--no-ext-diff --no-textconv --src-prefix=a/ --dst-prefix=b/ …`: the parser in
[`utils/impactedtests`](civisibility/utils/impactedtests/impacted_tests.go)
reads `diff --git a/<path> b/<path>` headers, and the SDK's plain `git diff`
let `diff.noprefix`, `diff.mnemonicPrefix`, `color.diff=always` or the
default quoting of non-ASCII paths hide modified files, and `diff.relative`
leave out the files outside the directory that ddto runs in. `diff.relative`
is turned off through `-c`, not `--no-relative`, which Git before 2.28, as
Ubuntu 20.04's 2.25, rejects: that diff failed, and no test was modified. A
header that the pattern still cannot read, as for a path with a quote, ends
the previous file instead of giving it the next file's hunks. Checks:
`TestGetGitDiffNormalizesHeaders`, `TestGetGitDiffBeforeGit228`,
`TestParseGitDiffOutputUnreadableHeader`.

The commit reads, `git log` and `git show` with a format, pass
`--no-show-signature`: with `log.showSignature`, Git prints a signed commit's
verification with them, which the SDK took as the commit's SHA, fields or the
list of commits that it searches the backend for. Check:
`TestGitReadsIgnoreShowSignature`.

Git's data comes from its standard output only (`gitOutput`): the SDK's
`CombinedOutput` also took Git's warnings, which Git prints on stderr when it
succeeds, as for the deprecated `core.fsyncObjectFiles`, into the SHA and the
paths. After an error, the output still includes stderr for the reports.
Check: `TestGitReadsIgnoreWarnings`.

## Settings that require Git

When the settings require Git, the SDK waits a minute for the repository
upload before requesting them again. With ddto's Git worker, which uploads
for the whole invocation, the wait is `--git-upload-timeout` when that is
longer (`requireGitUploadWait` in `integrations/civisibility_features.go`):
with `--git-upload-timeout=2m`, an upload of 90 seconds left the tests without
the settings that it enables. Check: `TestRequireGitUploadWait`.

## Git safe.directory for linked checkouts

Upstream `execGit` in [`civisibility/utils/git.go`](civisibility/utils/git.go)
passes `-c safe.directory=<root>` so git accepts a checkout owned by another
user, which is common in CI containers. `getParentGitFolder` found that root
only through a `.git` directory. A linked worktree or a submodule has a `.git`
file instead: a worktree got no `safe.directory`, and a submodule got its
superproject's root. When ownership differed, git refused with "dubious
ownership" and the Git metadata was lost.

Here a regular `.git` file also identifies the checkout root, as
`codeownership.findGitRoot` already accepts. The root of a worktree or
submodule is the directory git names in its `safe.directory` advice.

Checks: `TestSafeDirectoryConfigAcceptsGitFiles` resolves the root from a
nested directory of a repository, a linked worktree and a submodule.

## Test command

Upstream `createCITagsMap` in
[`civisibility/utils/environmentTags.go`](civisibility/utils/environmentTags.go)
joins the binary name and arguments with a trailing space and removes
`(?si)-test.gocoverdir=(.*)\s`, `-test.v=(.*)\s` and `-test.testlogfile=(.*)\s`
matches "to make the command more stable". The greedy `.*` runs to the last
whitespace, so every argument after the first such flag was removed too. `go
test` passes `-test.testlogfile` first for a cacheable run, which reduced
`test.command`, the session resource that contains it and the automatic
`test_session.name` to the binary name.

`testCommand` removes only arguments that set those flags, in `-name=value` or
`--name=value` form and without regard to case, as the patterns matched. It
also removes the other paths that `go test` injects and that change between
runs or machines: `-test.coverprofile`, rewritten to a file in the work
directory, the absolute `-test.outputdir` and `-test.fuzzcachedir`. Upstream
never kept them, because they follow `-test.gocoverdir` or
`-test.testlogfile`, or are only added with fuzzing or profiles. Other
arguments keep their order. Commands without such a flag, or with one only as
the last argument, are unchanged. This is a deliberate difference from the
pinned SDK, recorded in the [parity contract](../../../docs/ci-parity.md#comparison-contract);
dd-trace-go main at `d27b94332308` still has the greedy patterns.

Checks: `TestTestCommandRemovesOnlyVolatileFlags` compares both filters on
`go test` argument lists; `integration/TestAlignMiniTestCommandsRequiresMiniForm`
covers the Fuzz/Examples comparator's alignment of Mini's commands with the
SDK's.

## Source metadata parsing

[`civisibility/integrations/manual_api_sourcecache.go`](civisibility/integrations/manual_api_sourcecache.go)
adds `parser.SkipObjectResolution`. The cache reads declarations, source ranges,
function literals and ITR comments; it does not use identifier objects, scopes
or unresolved-identifier lists. `ParseComments` and `AllErrors` remain enabled.

If upstream adds semantic identifier inspection, reassess this flag. Preserve
the source-cache tests for named functions, adjacent literals, missing/invalid
files and ITR comments, and the CI matrix's source/coverage comparisons.

## Build information read once

[`civisibility/utils/source_paths.go`](civisibility/utils/source_paths.go)
reads the binary's main module path once, with `sync.OnceValue`. The SDK called
`debug.ReadBuildInfo` for every distinct test function, including those with
absolute source paths, and each call parses the binary's whole dependency list.
Build information cannot change while the process runs. Check:
`TestBuildInfoMainModulePathReadOnce` requires the same path, read without
allocating.

## GitHub Actions job ID from ddto

Without a numeric `JOB_CHECK_RUN_ID`, the SDK scans the runner's
`Worker_*.log` files in every process that reports GitHub Actions metadata. It
reads the newest file whole, up to 10 MB, then tries JSON and a regular
expression. [`civisibility/utils/githubjob`](civisibility/utils/githubjob/githubjob.go)
holds that scan, moved from `ci_providers.go` with exported names and a
debug-function parameter, so that ddto can run it once per invocation without
the CI metadata packages. ddto hands the result to Mini's test processes in
`DDTO_GITHUB_JOB_ID`, and `goenv/restore` removes the variable before `os`
initializes, as it restores the caller's Go settings. With a `-exec` program or a cross-compilation helper, which
would see the variable first, ddto hands nothing; it also stops waiting for a
scan that is still running a second after preparation. The scan now skips
Worker logs that are not regular files: reading a FIFO could block.

`getGithubActionsJobID` still reads `JOB_CHECK_RUN_ID` first. A handed value
is the diagnostics result: an empty one means none, so the process does not
scan again, and tests that disable the scan ignore it too. Processes that ddto
did not start, the SDK runtime and binaries run outside `ddto test` scan as
before. With a 2 MB Worker log, the scan took 0.44 ms and allocated 2.1 MB per
process, and 0.62 ms with 31 logs; a handed value takes 31 ns. When upstream
changes the scan, update `githubjob` and keep the precedence above.

Checks: `TestGithubActionsJobIDFromDDTO`, `TestGithubActionsJobIDFromDiagnostics`,
`TestFromDiagnosticsSkipsNonRegularFiles`, `TestHandGitHubJobID`,
`TestHandGitHubJobIDDoesNotWaitForAStalledScan`,
`TestReceiveRemovesHandedValues`, `TestMiniGitHubJobIDHandoff` and
`TestMiniGitHubJobIDNotHandedThroughExec`.

## Git work from ddto's CLI

Every test process used to read the checkout's Git metadata (four commands),
upload the repository's missing commits, and, with impacted tests, discover
the base branch and compute the diff. ddto now runs that work once per
invocation in a worker, [`civisibility/cligit`](civisibility/cligit/cligit.go),
started in the invocation's directory with the test processes' environment.
It writes its results to a state directory that ddto hands to Mini's test
processes in `DDTO_GIT_STATE`, which `goenv/restore` removes before `os`
initializes.

- [`civisibility/utils/cli_git_state.go`](civisibility/utils/cli_git_state.go)
  defines the record (`git.json`), the upload and impacted-tests results
  (`upload.json`, `impacted.json`), the `used` mark and atomic writes.
  `HandedCLIGitState` accepts the state only when the process's `.git` entry,
  found as `getParentGitFolder` finds it, is the same file as the worker's,
  and it can mark the state `used`. Commit dates are Unix seconds, so each
  process formats them in its own location as after `git log`.
  [`civisibility/utils/cli_state_files.go`](civisibility/utils/cli_state_files.go)
  opens the state's files with `syscall.Open`, so that the reads and writes of
  test processes stay out of Go's test log: with the state directory in the
  module, as with `TMPDIR` inside it, `go test` would otherwise check files that
  change on every run and cache no result. On Windows, it adds the long-path
  prefix to an absolute path of 248 characters or more and keeps extended and
  device paths, as `os` does (`extendedWindowsPath`).
- `CollectCLIGitRecord` changes nothing in the repository: `fetchCommitData`
  (`git.go`) became `readCommitData(sha, allowFetch)`, and without
  `allowFetch` it reports a CI head commit missing from a shallow clone
  instead of fetching it. The record then leaves the head commit out, and test
  processes fetch it as before. The worker's own CI tags skip that fetch
  (`environmentTags.go`), and `FetchCLIHeadCommit` fetches the commit once a
  test process uses the state, before the upload and the impacted-tests diff,
  which needs it. Both the worker's tags and those of the session that ddto
  sends after `go test`, which uses the same record, read the head commit
  without fetching it: the session gets the commit that the worker fetched.
- `createCITagsMap` (`environmentTags.go`) takes `getLocalGitData`'s and,
  for the same head commit, `fetchCommitData`'s results from the record; the
  CI provider's tags keep their precedence.
- The upload's steps moved from `integrations/civisibility_features.go` to
  [`civisibility/utils/gitupload`](civisibility/utils/gitupload/gitupload.go),
  unchanged except for exported names; `integrations` keeps thin wrappers and
  its test seams. `net.NewCLIClient` creates the worker's client.
- `ensureSettingsInitialization` waits for `upload.json` instead of uploading
  when the state is handed, and closes the same upload channel, so the
  `RequireGit` and impacted-tests waits are unchanged. Deferred delivery waits
  before admitting tests, as for its own upload; ordinary mode waits in the
  background. It no longer waits for the upload at exit: ddto waits for its
  worker before exiting.
- `NewImpactedTestAnalyzer` (`impactedtests`) builds the analyzer from
  `impacted.json` when the worker computed it for the process's current
  commit and pull-request base; otherwise it computes as before
  (`computeAnalyzer`). It waits for that file as long as ddto's
  `--git-upload-timeout` lets the worker fetch the base commit, and at least 30
  seconds (`impactedStateWait`): a shorter wait computed without that commit
  and found no modified files. Check: `TestImpactedStateWaitFollowsTheUploadTimeout`.

The worker writes the record before ddto starts go test. It searches the
backend's commits, a read-only request, and requests what test processes
request before their first test (see
[Requests from ddto's CLI](#requests-from-ddtos-cli)): while go test builds
when go test runs every test package, otherwise once a test process marks the
state `used`, so that a run whose results all come from Go's test cache sends
no request. It unshallows, packs and uploads only after a test process marks
the state `used`. Its settings
decide the impacted-tests state after the upload. When go test takes every
result from its cache, no process uses the state and the worker cancels its
commit search and requests, waits for them and exits without changing the
repository. The worker closes its standard output once it has written its
results, or failed to, and ddto then writes any missing result, so no process
waits for its timeouts.

ddto writes `exited` to the worker once `go test` exits and waits for it at
most `--git-upload-timeout` (default one minute), which also bounds the test
processes' wait for `upload.json` through the record's `upload_wait_ms`. Then,
or on a signal, it writes `stop` and a reason. The worker cancels the context
that `SetGitCommandContext` (`utils/git_stop.go`) gives its Git commands, which
receive SIGTERM, so that Git removes its lock files, and SIGKILL after three
seconds; later commands fail at once. The same context is the worker's
`RequestHandler.Context` (`utils/net/http.go`), which stops a request in
flight, its retries and the waits between them; `rh.backoff` replaced the
package-level `retryBackoff`, and the requests use `NewRequestWithContext`.
The upload returns, `RemovePackFiles` removes its pack files, `upload.json`
records the stop, and the worker logs it as telemetry and still sends
`app-closing`. ddto kills only a worker that has not exited ten seconds after
`stop`. The worker reads its input from its start, before its first Git
command: the input ending before `exited` stops it too, and `exited` before
`proceed`, as when ddto's preparation fails, stops it at once, even during
its first Git commands. The signals that
ddto forwards to go test also reach the worker from the terminal, and the
worker handles them as a `stop` with reason `signal`. Test processes, whose handler has no context and which never set the
Git context, keep their behavior.
Processes outside the worker's checkout, binaries run outside `ddto test`, the
SDK runtime, Bazel's offline modes, `-c` and `-n`, and commands with a `-exec`
program or a cross-compilation helper keep the per-process behavior.

Test processes that use the state no longer count the Git commands and
repository requests that the worker made; the worker reports them, see
[Telemetry from ddto's CLI](#telemetry-from-ddtos-cli). With 8 packages
against a local fake Agent, a `ddto test` run made one commit search and one
upload instead of eight each, and took 0.47 instead of 0.62 s; each test
process's bootstrap dropped from 12 to 5 ms.

`globalconfig.go` records whether a process started its root session, which
it hands to child processes in `_DD_ROOT_GO_SESSION_ID`, and the variable as
the process received it. Linking the worker gives ddto that package, so
`ddto`'s `main` calls `KeepCallerEnvironment` to restore the variable, absent,
empty or set, and hands its session only to the worker (`RootSessionEnv`): go
test, `-exec` programs and test processes see the caller's environment.

Checks: `TestMiniGitStateHandoff`, `TestMiniGitStateUploadsOnce`,
`TestMiniGitStateImpactedTests`, `TestMiniGitStateDeferredWaitsForUpload`,
`TestMiniGitStateCacheWithTemporaryDirectoryInModule`,
`TestMiniExecWrapperSeesCallerEnvironment`, `TestRunUploadsOnlyWhenTold`,
`TestRunClosesResultsOnWriteFailure`, `TestCLIGitWorkerFetchesHeadOnlyAfterUse`,
`TestHandedCLIGitStateNeedsUsedMark`, `TestCLIStateFilesLongPath`,
`TestExtendedWindowsPath`,
`TestGitWorkerRecordsMissingResults`,
`TestGitWorkerRecordsResultsWhenOutputCloses`, `TestRunStopsUploadOnRequest`,
`TestGitCommandStopsWithInterrupt`, `TestRequestHandlerStopsWithContext`,
`TestGitWorkerFinishStopsAfterUploadTimeout`,
`TestGitWorkerFinishKillsAWorkerThatDoesNotStop`,
`TestGitWorkerSignalStopsTheWorker`, `TestGitWorkerKeepsCIState`,
`TestGitWorkerFinishBoundsAStopBySignal`, `TestZipDirectoryKeepsLinks`,
`TestRunStopsDuringRecordCollection`, `TestRunWaitsForTheStoppedSearch`,
`TestRunStopsOnInterrupt`, `TestRunEndsWhenGoTestExitsWithoutTheState`,
`TestRunProceedsWhenGoTestExitedFirst`,
`integration/TestMiniGitStateStopsUploadCleanly`,
`integration/TestMiniGitWorkerStopsOnInterrupt` and `TestReceiveRemovesGitState`,
with the existing upload and impacted-tests tests in `integrations` and
`impactedtests`.

## Telemetry from ddto's CLI

ddto's Git worker owns the telemetry application of an invocation. The test
processes that use its state report under that application instead of
starting their own, so an invocation is one application, with ddto's runtime
ID, one `app-started` and one `app-closing`.

- [`telemetry/shared.go`](telemetry/shared.go) and
  [`telemetry/internal/shared.go`](telemetry/internal/shared.go) define
  `SharedApp`: the runtime ID, root session, application and host that a
  request reports. With `ClientConfig.Shared`, the writer
  (`telemetry/internal/writer.go`) puts them in the body and in the
  `DD-Session-ID` and `DD-Root-Session-ID` headers, and `SharedSeqID` numbers
  the requests instead of the writer's counter. A process that reports
  another's application fills in its own Go version.
- In `telemetry/client.go`, only the owner (`SharedOwner`) sends `app-started`
  and `app-closing`. Another process's client registers no product or
  configuration source, sends nothing at startup and adds no `app-closing`; it
  sends its metrics and logs at its periodic and final flushes. The owner's
  startup sends `app-started` alone and leaves its other data in its sources,
  so that it leaves with `app-closing` in one request. `SendFailed` reports
  each undelivered request.
- [`civisibility/utils/cli_telemetry_state.go`](civisibility/utils/cli_telemetry_state.go)
  records the application in `telemetry.json` and numbers the requests of all
  its processes. A number is a file in `seq/` that one process creates
  exclusively. Each process takes the number after the highest one that
  exists, found by a galloping search, so the numbers are 1 to n without
  repeats and a later request has a higher number. The owner reserves 1 for
  `app-started`. If the directory cannot be used, a process continues its own
  numbers. A test process records that fallback, and each undelivered
  request, in a `telemetry-failure-*` file, which the owner reports even when
  it happened in the test process's final request. The owner logs its own
  fallback. It takes the number of its final flush's first request before
  `StopApp` takes the final data, so that a fallback then is logged in that
  request; a fallback for a later request of that flush, sent only when an
  earlier request was undelivered, goes unreported. The sequence's files stay
  out of Go's test log, like the Git state's.
- In `civisibility/utils/net/client.go`, the telemetry start function takes
  the owner, if any. A process whose state ddto handed reports the handed
  application and registers no product or configuration: the owner's
  `app-started` carries them. `NewCLIClient` creates the worker's client and
  writes `telemetry.json` under ddto's runtime ID before the worker writes its
  Git record, which ddto waits for before it hands the state over.
- The worker ([`civisibility/cligit`](civisibility/cligit/cligit.go)) starts the
  application when a test process marks the state `used`, with the Go version
  that ddto read from `go env`. It closes it after `go test` exits, once it has
  logged the test processes' undelivered requests. It also logs the failures
  of its own work as telemetry: the upload, the settings request, the
  impacted-tests state and writing a result. A failed write closes its results
  at once, but `app-closing` still waits for `go test` to exit. Test processes
  log a missing or invalid application, a state that they could not mark used,
  and a missing upload or impacted-tests result. Messages are constant; tags
  hold only a reason, a file name or a status code.

Processes without a handoff, as for the Git state, report their own
application as before. A fully cached run sends no telemetry, as before. The
application's service and environment are the worker's, so test processes no
longer report a service per package, as `DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS`
selects for their events, in the telemetry configuration.

With 8 packages against a local fake Agent, a run made 10 telemetry requests
instead of 23: one per test process and two from the worker, instead of
`app-started` and two requests at exit per test process. With 100 ms per
request and `-p 2`, the run took 1.91 instead of 2.52 s; with all 8 test
processes in parallel, the run took the same time, because the worker's
`app-closing` follows the last test process.

When updating `telemetry`, keep the shared identity in `newBody` and
`preBakeRequest`, the external sequence in `setPayloadToBody`, and the
`sharedChild` checks in `AppStart`, `AppStop`, `flushStartup` and the data
sources.

Checks: `TestSharedAppReportsOwnerIdentity`, `TestSharedAppRequiresSequence`,
`TestCLITelemetrySequenceAcrossProcesses`, `TestCLITelemetrySequenceReserve`,
`TestCLITelemetryStopReportsSequenceFallback`, `TestCLITelemetryFailures`,
`TestRunOwnsTelemetryApplication` and `integration/TestMiniCLITelemetry`, which
checks one application, sequential `seq_id`s, the worker's Git metrics, no
telemetry from a cached run, reported failures and a binary run alone.

## Requests from ddto's CLI

Before its first test, each test process requests the settings and, as they
enable them, the known tests, skippable tests and test management data. The
read cache shared those responses between test processes, but the first one
waited for them after compilation, and the Git worker's own settings request,
made with ddto's toolchain and parent, matched no test process's. ddto's Git
worker now makes these requests while go test builds, as the test processes
will, and the read cache hands the responses to them.

- ddto writes `tests.json`
  ([`civisibility/utils/cli_tests_state.go`](civisibility/utils/cli_tests_state.go))
  before it tells the worker to proceed: the test binaries' `GOARCH`, from its
  `go env`, whether go test runs every test package (`RunsEveryTest`, below),
  and each test package's directory, module path and module directory, from
  its package query. `HandedCLITests` reads it in the worker
  and in test processes; `ResetHandedCLIGitStateForTesting` also forgets it.
- `cliReadCacheScopeIdentity` (`utils/net/read_cache.go`) gives the worker,
  and test processes that use its state, the scope of ddto's invocation, kind
  `cli`, identified by the state directory, a digest of the client's API key
  and its Agent's Unix socket, instead of their parent process. They keep a
  response that one credential or Agent received from another client; the
  cache never stores the key, and the base URL names a socket only lossily,
  `/` and `_` alike (`UnixDataSocketURL`). CI scopes, which both already
  share, keep their kind and lifetime but also mix in the same two digests:
  they outlive one invocation, and another `ddto` run in the same job with
  another API key, as for another organization, must not reuse its responses.
  So does a local scope outside ddto. The entries must outlast a build
  of minutes, so the scope's TTL is an hour, and they live in the scope's own
  directory, `cli/<hash>` of the state directory alone, in the shared one
  (`readCacheSubdirectory`,
  `readCachePathsIn`), which ddto removes once the worker has exited
  (`RemoveCLIReadCache`); the shared directory's cleanup skips directories.
  A kill or a reboot leaves an invocation's directory, so
  `RemoveCLIReadCache` also applies the shared directory's cleanup, which
  removes only expired entries, stale locks and old temporary files, to a
  bounded number of `cli/` directories (`removeExpiredCLIReadCaches`), and
  removes those that it emptied. A left directory's entries expire within the
  TTL, so it hides the others only for that long; a running invocation's
  entries do not expire. A process holds a lock file in the directory while it
  writes, so the directory cannot go under it, and `tryCreateReadCacheLock`
  creates the directory again if the cleanup removed it, empty, after the
  process computed its paths. The cleanup now removes an expired entry under
  its key's lock (`removeExpiredReadCacheEntry`, through
  `acquireReadCacheLock`, which replaces a stale lock), which writers hold
  while they publish, and removes it only while it still holds the lock, as
  `writeReadCacheEntry` checks before it publishes; it leaves the entry when
  a writer holds the lock. Upstream, a
  cleanup could remove an entry that a writer had just renewed, which this
  change would otherwise extend to other invocations' directories. One race
  remains in the SDK's own lock protocol, unchanged here: replacing a stale
  lock, which a process killed more than an hour earlier left, is not
  atomic, so two processes can both own the lock, and a cleanup that passed
  its owner check can still remove a fresh entry before the other replaces
  its lock. It costs a cache entry and a request, never wrong data; fixing it
  means changing the protocol upstream, for example stealing locks by rename.
  They stay in the user's cache directory, not the state directory: test
  processes may read them while Go's test log is active, and the state
  directory can lie in the module.
- `waitForReadCacheEntry` stops waiting for another process's request when the
  client's request context is done (`requestContext`), so a stopped worker
  does not wait up to two seconds; other clients have no context.
- `CLITestProcessClients` (`utils/net/client.go`) copies the worker's client
  with the test processes' Go version, from `proceed`, and architecture, and
  with the service that each test package reports: `DD_SERVICE`,
  `utils.ServiceFromCodeOwnersForDirectory`, or the repository's name
  (`repositoryServiceName`, extracted from the constructor). The copies' read
  cache keys and requests are the test processes'.
  [`civisibility/utils/service_name.go`](civisibility/utils/service_name.go)
  extracted the owner lookup into `codeOwnersServiceFor`, which
  `ServiceFromCodeOwners` and `ServiceFromCodeOwnersForDirectory` share.
- After `proceed`, the worker uses the copy for its own service
  (`asTestProcesses`), so its settings request for the impacted-tests state is
  the test processes' too, and `requestAhead` requests, for each service, the
  settings and then what `ensureAdditionalFeaturesInitialization` would:
  known tests with `known_tests_enabled`, skippable tests with
  `tests_skipping`, and test management with `test_management.enabled` unless
  `DD_TEST_MANAGEMENT_ENABLED` disables it. Settings that require Git are not
  cached and change after the upload, so the worker leaves the rest to the
  test processes. The requests run alongside the commit search and stop with
  it; the worker joins them before `app-closing`, so their metrics leave in it.
  Test processes request, and report, whatever is missing.

The test processes report `runtime.Version()`, which includes the experiments
that `GOEXPERIMENT` sets. ddto's `go env` reports the go command's own version,
with the toolchain's default experiments, and the raw setting; when they
differ, ddto takes the canonical list from `go tool compile -V`, about 6 ms, and
appends it as the linker does (`testRuntimeVersion` in
`internal/runner/environment.go`).

A run whose results all come from Go's test cache sends no request. ddto parses
go test's arguments as go test does (`internal/runner/options.go`) and records
in `tests.json` whether go test runs every test package (`RunsEveryTest`): in
local directory mode, or when a test binary gets an argument outside go test's
cacheable flags, from the command line or `GOFLAGS`: a flag such as `-count=1`,
an unknown flag, an argument after the package list, `--`, which go test hands
on itself, or after `-args` anything but `-test.<cacheable flag>=<value>`, as go
test checks the arguments that it hands on as they are (`noteTestArgument`).
Only then does the worker search the commits and request while go test builds.
Otherwise ddto adds its tool to go test in its `link` mode (`plan.linkSignal`),
and the tool marks the state `linking` (`utils.CLIGitLinkingFile`,
`toolwrap.NoteTestLink`) when go test links a test binary: go test checks its
cache just before the link and links only for a result outside it. The worker
does both once that mark, or the `used` one, appears (`waitFor`), so the
requests run while the binary links, and a run whose results all come from the
cache sends none. go test checks its cache again after the link, by the binary's
content, so a link does not always mean a test process: `-o`, which saves the
binary, and `-a`, which rebuilds everything, link even for cached results, so
ddto adds no signal with them (`linksCachedResults`) and the worker waits for a
test process. A change that leaves a test binary identical, such as a comment,
still makes go test relink it and take its result from the cache: the worker
then requests although no test process runs, an accepted cost of the earlier
requests. The tool is ddto's small wrapper executable, or ddto itself when that
executable is unavailable; it runs every tool after its fast check and keeps
Go's cache keys, because cmd/go takes each tool's identity from its `-V=full`
output through the wrapper. The cacheable flags are the newest supported Go's:
Go has only added to them, so a flag outside them disables the cache in every
version. Test processes outside the worker's checkout, binaries run outside
`ddto test` and the SDK runtime keep their own requests and scopes.

Checks: `TestCLIReadCacheScopeIdentity`, `TestCLITestProcessClients`,
`TestCLIReadCacheLastsTheInvocation`,
`TestRemoveCLIReadCacheRemovesExpiredEntries`,
`TestRemoveCLIReadCacheReachesEveryDirectory`,
`TestReadCacheLockRecreatesRemovedDirectory`,
`TestReadCacheCleanupLeavesAnEntryBeingRenewed`,
`TestReadCacheCleanupReplacesStaleLocks`,
`TestReadCacheCleanupRemovesOnlyAsLockOwner`,
`TestReadCacheWaiterStopsWithContext`,
`TestTestRuntimeVersionMatchesBinaries`, which builds a binary under each
`GOEXPERIMENT` case, `TestExperimentSeparator`,
`TestCodeOwnersPackageService`, `TestCLITestPackageModule`,
`TestRunRequestsAheadForTestProcesses`, `TestRunWaitsForItsStoppedRequests`,
`TestRunRequestsNothingWhileResultsMayBeCached`, `TestRunsEveryTest`,
`TestRunRequestsWhenGoTestLinks`, `TestNoteTestLink`, `TestLinksCachedResults`,
`TestToolModeWithLinkSignal`,
`TestWorkerRequestsAsTestProcesses`,
`integration/TestMiniCLIStateRequestsAhead`, which runs the worker and then
a test process that makes no request of its own, also with services from
CODEOWNERS, and `integration/TestMiniCLIStateSharedRequests`, which checks one
request per endpoint, or per service, in a `ddto test` run that runs every
test, also with `GOEXPERIMENT`, none in a run whose results all come from the
cache, also with cacheable flags after `-args` and with `-o`, a `linking` mark
only in a run with a result outside the cache, ddto's `tests.json` and the removed cache
directory.

With `DD_TRACE_DEBUG`, the worker, the read cache and test processes log
these decisions. In `worker.log`, `cligit.Run` logs what started the requests
ahead (`trigger:runs_every_test`, `linking` or `used`), with the test
processes' Go version and the number of clients, or that go test exited
before a test process used the state, or that ddto stopped the worker, with
the stop reason. `requestFeatures` logs, per client, a failed or
Git-requiring settings response, each failed feature request and the features
it requested with the duration; `redactError` hides a URL's user and password
in those errors, which `url.Error` repeats. `HandedCLITests` logs the test
description it read. The read cache logs the invocation's scope
(`cliReadCacheScopeIdentity`), each hit with the scope's kind, a hit after
waiting for another process's request with the wait, and a miss with the
request's duration and whether its response may be stored; a failed store
already logs its own line. `RemoveCLIReadCache` returns how
many left directories it removed, for ddto's log. Coverage logs whether the
module came from ddto's description or `go list`, with the latter's
duration. None of these lines holds a credential or a request body.
`integration/TestMiniCLIStateDebugLog` and
`integration/TestMiniCLIStateGivesModuleToCoverage` check them.

## Read-cache waiters

Upstream, a process that finds another one's lock on a read-cache key polls
for its entry after 20, 40, 80 and then every 100 ms, and after 2 s sends the
request itself, without the lock, and writes no entry. When the owner's
request takes longer, as a large paginated known-tests response or a slow
backend can make it, every waiting process pays the wait and a duplicate
request. A lock whose owner was killed blocks its key for an hour.

`waitForReadCacheEntry` (`utils/net/read_cache.go`) now:

- polls every 5, 10 and then 20 ms (`readCacheWaiterInitialBackoff`,
  `readCacheWaiterMaxBackoff`): a poll reads the small lock file and tries the
  entry, and the owner's response reaches its waiters up to 80 ms sooner;
- waits past `readCacheWaiterTimeout` while the lock's owner still runs and
  the lock is younger than `readCacheWaiterLiveOwnerTimeout`, two minutes,
  but never longer than that from its own start, so new owners that keep
  replacing the lock cannot extend it. Two minutes exceed a paginated
  response, so a slow owner serves its waiters; an owner slower than that
  costs them the wait and their own request. The waiter keeps the 2 s rule
  when it cannot tell whether the owner runs, and for a lock that is older or
  from the future, as after a clock change;
- returns `readCacheWaitOwnerGone` when the owner's process ended without
  releasing its lock. `readThroughShortLivedCache` then takes the lock and
  requests as its owner, writing the entry for the others.

`readCacheLockIsStale` also takes a lock whose owner's process ended for
stale, so a process that finds one takes it at once. `processAlive`
(`read_cache_process_unix.go`, `read_cache_process_windows.go`,
`read_cache_process_other.go`) checks the lock's PID: a signal 0 on Unix, and
on Windows a zero wait on the process handle, which does not depend on the
exit code; it reports unknown where it cannot tell. A zombie, which ended but
which its parent has not reaped, accepts a signal 0: on Linux,
`processEnded` (`read_cache_process_linux.go`) reads its state in
`/proc/<pid>/stat`; on other Unix systems a zombie owner costs its waiters
the wait for a running one. A PID that another process reused looks alive,
which costs at most the wait up to the bound. Replacing a dead owner's lock has the
non-atomic replacement race described above: two processes can both own the
key, which costs a duplicate request, never wrong data.

A waiter whose owner released its lock without an entry still requests
without the lock at once, as upstream: that owner failed or got a response
that the cache does not keep, and taking the lock in turn would serialize the
waiters' failures. The lock and entry formats are unchanged, so SDK binaries
that share the cache directory interoperate.

`SetReadCacheHooksForTesting` sets whether an owner runs to unknown, because
test PIDs name no real process: existing tests keep the upstream behavior.
`setReadCacheProcessAliveForTesting` overrides it.
`TestReadCacheWaiterWaitsForLiveOwner`,
`TestReadCacheWaiterBoundsTheWaitForLiveOwner`,
`TestReadCacheTakesOverDeadOwnersLock`, `TestProcessAlive`
(`utils/net/read_cache_waiter_test.go`) and `TestProcessAliveZombie`
(`read_cache_process_linux_test.go`) check these rules, including a 40 s
owner, replaced locks and a lock from the future.

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

Here the fallback is the repository's objects directory: git writes each
temporary pack there before it moves it, so the move succeeds, and the working
tree never receives a temporary directory. `git rev-parse --git-path objects`
follows `GIT_OBJECT_DIRECTORY` and runs without command telemetry, so git
metrics match the SDK. Git ignores the extra directory: `git fsck` is clean and
`git count-objects -v` reports no garbage. A failed attempt or an empty result removes its
directory, and `RemovePackFiles` removes the directory after the upload.

Before the first attempt, `packObjectsFolders` compares the filesystems of the
temporary directory and the objects directory: device numbers on Unix, volume
names on Windows. When they differ, git could not move its pack, so it packs
once, in the objects directory, instead of repeating the whole pack after a
failure. The
failed attempt also left git's temporary `tmp_idx_*` and `tmp_rev_*` files in
the repository's `objects/pack` directory on every run. Where the filesystems
cannot be compared, the temporary directory is still tried first. The objects
directory is looked up before every packing, which adds one `git rev-parse`
when the temporary directory works.

Checks: `TestRemovePackFilesRemovesTemporaryDirectory`,
`TestPackFilesFallBackToObjectsDirectory`, `TestPackFilesFallBackToMovedObjectsDirectory`, `TestFailedPackFilesLeaveNoDirectory`,
`TestPackObjectsSkipsTemporaryDirectoryOnAnotherFilesystem`
and the git upload parity case, where Mini must leave its temporary directory
empty. The fake git in these tests and in `TestCreatePackFilesMissingPackFile`
skips the options before the command, which cmd also splits at `=` for
`git.bat`, and the tests check that `pack-objects` ran, so Windows exercises the
same paths.

## Git test fixtures

Git sets `GIT_DIR`, `GIT_INDEX_FILE` and related variables for hooks. The
`civisibility/utils` and `cligit` tests create repositories and commits with
the inherited environment, so run from a hook their commits could reach the
caller's repository, and an inherited `GIT_CONFIG` would receive their
`git config` writes. A `TestMain` in each package, in
[`utils/git_fixture_env_test.go`](civisibility/utils/git_fixture_env_test.go)
and [`cligit/cligit_test.go`](civisibility/cligit/cligit_test.go), unsets the
variables that select a repository before any test runs. The integration tests
already pass an explicit environment to their fixtures.

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

Mini records those ends. ddto's testing overlay adds `ParallelStopHook` from
`internal/instrument/hooks.go`, only for Mini and only when testing declares
`parallelStop atomic.Int64`; the linker allows no other access to the counter.
While testing initializes, the hook sets `testingParallelStop` in
[`retry_attempt_parallel_stop.go`](civisibility/integrations/gotesting/retry_attempt_parallel_stop.go)
through a linknamed variable. It does not call Mini: testing initializes
before Mini, and a Mini function that `-coverpkg` instruments and that runs
first records its coverage counters without a package, so Go prints
`internal error in coverage meta-data tracking` when it writes the profile.
For the same reason, Mini's copy of the SDK's `Hooks` sets
`testingBuiltWithOrchestrion` instead of calling
`instrumentTestingBuiltWithOrchestrion`; the SDK runtime keeps the call.
Released versions of this module declare both variables with the same types,
so a client that requires one of them still links. In the other direction,
the testing overlays of ddto v0.1.0 and v0.1.1 call `registerTestingParallelStop`
and `instrumentTestingBuiltWithOrchestrion`, so Mini keeps both functions:
a client can select this runtime while running an older ddto. The in-memory coverage
hook keeps its call, because ddto installs it only when `-coverpkg` leaves
Mini's coverage package alone. The SDK mirror's hook also keeps its call:
Mini's tracer initializes before the SDK's in the tested graphs, so that call
never ran first.
`beginRootParallelSchedulerTransfer` marks attempts that go parallel after the
lease moved, and `finalizeFreshRetryAttempt` records their end before it
publishes the result, as `tRunner` does before it releases its caller. Both
edits replace single lines, so no line in a retry's error stack moves. The SDK
runtime keeps upstream behavior.

Checks: `TestSharedParallelLeaseAttemptsRecordTheirEnd`,
`TestSequentialAttemptsRecordNoParallelEnd`, `TestParallelStopHookOnlyForMini`,
`TestMiniHooksCallNoMiniFunctionAtInit`, `TestRuntimeKeepsFunctionsThatReleasedOverlaysLink`,
`TestToolchainTestingDeclaresParallelStop`,
`TestMiniCoverageOfMiniItselfHasNoCounterErrors` and
`TestMiniInProcessRetryKeepsAllocsPerRun`. This package's own tests run without
the overlay, so `TestSharedParallelLeaseAttemptsRecordTheirEnd` and
`TestProcessRetryParityFreshRunnerSharesRootParallelLeaseAcrossSequentialAttempts`
leave the accounting unbalanced for the rest of the binary. The package's
allocation checks therefore use its `allocsPerRun` helper instead of
`testing.AllocsPerRun`, which would panic with `-count` above one or with
`-shuffle`. `TestPackageTestsMeasureAllocationsWithHelper` keeps it that way.

## Forced exits

A test process can end before its tests finish: a signal, or a call to
`os.Exit` or `log.Fatal` from a test, a cleanup or another goroutine. Mini
sends the events of the tests that ran and closes the session as failed in
both cases, and the process ends as under go test.

### Signals that would end the process

Upstream registers SIGINT and SIGTERM with `signal.Notify` when CI Visibility
starts and, on either signal, closes CI Visibility and calls `os.Exit(1)`. Go
delivers a signal to every channel that wants it, so that handler also ends a
process whose test handles the signal itself, such as a server test that
sends itself SIGTERM to check its graceful shutdown. The process exits with
status 1 between two tests, and go test prints no reason. A signal that nothing handles also ends it with status 1
instead of the signal, so go test does not print `signal: terminated`.

Go has no API that tells whether another channel wants a signal, and its
runtime ends a process for an unwanted signal from its signal handler, without
running Go code. Mini therefore relies on an os/signal overlay that ddto adds
for Mini only. `TransformSignal` in `internal/instrument/signal.go` edits
`process`, the function that delivers signals to channels, so that Mini's
channel receives a signal only when no other channel wants it. Channels that
`Stop` is removing count too, as `process` still delivers to them, and so does
a channel that the test stops right after its delivery: the check runs under
the lock that `process` holds. It also edits `Reset` and `Ignore`, which
remove a signal from every channel, Mini's included, through `cancel`. `Reset`
passes `cancel` the hook's `__dd_civisibility_resetSignal` instead of
`disableSignal`: under the same lock, that action registers Mini's channel
again for a signal that Mini holds and keeps the signal enabled, so it never
ends the process without reaching Mini first; other signals are disabled as
before. `Ignore` passes `__dd_civisibility_ignoreSignal` instead of
`ignoreSignal`, which also makes Mini stop holding the signal for good: Go
keeps an ignored signal ignored after `Reset`, and for SIGINT and SIGHUP even
after `Notify` and `Stop`, where only the operating system remembers it. Mini
also leaves out, when it registers, the signals that the test ignored before
Mini started, and SIGINT when the process started with it ignored, as a
background job of a non-interactive shell does: the hook records that while
os/signal initializes, before any `Notify` can hide it from `Ignored`. The
hook keeps these sets in arrays indexed by signal number.
The edits stay on their lines, so os/signal keeps its positions.

`SignalHook` sets `signalNotify` and `signalStop` in
[`civisibility.go`](civisibility/integrations/civisibility.go) through
linknamed variables while os/signal initializes. It does not call Mini, because
os/signal initializes before Mini: a Mini function that runs first and that
`-coverpkg` instruments records its coverage counters without a package, and
Go reports `internal error in coverage meta-data tracking` when it writes the
profile. Mini's channel and its signals change under handlers' lock, after
`Notify` has created the channel map and started the signal loop. Under that
lock Mini holds its signals again, in case a `Reset` ran since `Notify`. Mini
stops its channel through `signalStop`, so that `Reset` does not register it
again afterwards.

On such a signal Mini closes CI Visibility as before, stops its channel, and
sends the signal to its own process again. With no channel left, the runtime
ends the process as under go test
([`civisibility_signal_exit_unix.go`](civisibility/integrations/civisibility_signal_exit_unix.go)).
Windows cannot send a console event to one process, so it keeps exit status 1.
A signal that arrives once the normal shutdown has started, as during the final
flush, or while a normal shutdown starts during the signal's own, is kept:
the handler records it, and `stopCIVisibilitySignalHandler` sends it again when
that shutdown ends, so the process does not end with the test binary's status
instead. A signal still queued for the handler when it stops, which its
goroutine did not take before the stop, counts too. Checks:
`TestCIVisibilitySignalHandlerExitsAfterNormalShutdownStarts`,
`TestCIVisibilitySignalHandlerExitsWhenNormalShutdownStartsDuringSignalShutdown`
and `TestCIVisibilitySignalHandlerExitsWithAQueuedSignal`.
Mini registers no signal that the process ignores, such as SIGINT in a
background job, because registering it would install a handler. A test that
ignores a signal removes Mini's channel for it, as for any channel. When the
toolchain's os/signal lacks what the hook uses, `prepareSignalHook` leaves
os/signal unchanged, and Mini handles no signal: a signal ends the process
without CI events, as it ends go test. A client that requires an older version
of this module runs that version's runtime, signal handling included.

### os.Exit and log.Fatal

Upstream loses every event of a process that a test ends with `os.Exit` or
`log.Fatal`, because the process ends without running deferred functions or
CI Visibility's shutdown. `ExitHook` in `internal/instrument/exit.go` is a
testing overlay file that registers `forcedExit` in
[`civisibility.go`](civisibility/integrations/civisibility.go) as a runtime
exit hook (`internal/runtime/exithook`, which coverage uses to write its
counters). `os.Exit` runs it before the process ends, with any status.
`forcedExit` runs CI Visibility's shutdown; after a normal end CI Visibility
is already closed, and it returns at once, and while another goroutine shuts
it down it waits for that shutdown. The shutdown runs on its own goroutine,
and `forcedExit` waits for it at most a minute (`forcedExitTimeout`): the
goroutine that called `os.Exit` can hold a lock that the shutdown needs, as
when user code that Mini calls during its own shutdown, such as a replaced
`http.DefaultTransport`, exits; Mini's own delivery no longer uses that one.
The bound also ends a delivery that still progresses after a minute, such as a
large deferred queue to a slow intake: an exit is never delayed longer. An
error says so, printed at once rather than with the errors that Mini prints
every minute. testing's `-timeout` alarm keeps running while the hook waits;
in the rare case that it expires then, testing's timeout panic ends the
process instead of `os.Exit`'s status. It does not join the signal handler's
goroutine: on Windows that goroutine calls `os.Exit` after a signal, and would
wait for itself. Another package's init can call `os.Exit` after testing
registered the hook and before Mini initializes; CI Visibility has not started
then, and the hook only reads its state. It recovers a panic, because the
runtime ends the process with a fatal error when an exit hook panics. The test
that was running is not reported, as after a signal. Mini closes a test when
its body returns, before testing runs the test's cleanups, so a test whose
cleanup exits reports the result of its body. `os.Exit(0)` during a test
already panics in testing, and signals and crashes run no exit hooks: see
[Crashes](#crashes) for the latter.
`prepareExitHook` adds the file only when the toolchain's exithook and the
selected runtime declare what it uses.

The SDK runtime keeps upstream behavior for signals and exits.

### Debug lines for process endings

We never see a project's code, so with `DD_TRACE_DEBUG=true` a test process's
log has to say how it ended. Mini logs:
- each test's start (`createTest`) and finish (`finalizeInstrumentedTestExecution`, with a panic's value);
- a panic that leaves `testing.M.Run` (`isAbnormalTestingMExit`);
- the signals it registers and those it leaves alone;
- each signal that reaches it, and each one that arrives while a channel of the test is registered for it;
- the stack that called `os.Exit`;
- the HTTP transport of its event delivery;
- a crash that it closes CI Visibility for, the running tests it fails and the close's duration;
- whether it reports in ddto's test session.

The os/signal hook reports a signal that arrived while other channels were
registered for it on a channel that `signalNotify` receives, without blocking,
and `signalNotify` returns the signals it registered. `testing.go` changes
on its line only, so its lines stay where the stack comparison expects them.
[CLI build diagnostics](../../../docs/cli-debug.md#why-a-test-process-ended)
explains each line.

Checks: `TestMiniSignalsKeepGoTestBehavior`, `TestMiniForcedExitKeepsEvents`,
`TestToolchainProvidesSignalHook`, `TestSignalHookTypeChecks`,
`TestSignalHookMatchesMiniRegistration`, `TestTransformSignalRejectsOtherShapes`,
`TestToolchainProvidesExitHook`, `TestExitHookTypeChecks`,
`TestExitHookMatchesMiniRegistration`, `TestPrepareSignalHook`,
`TestPrepareExitHook`, `TestForcedExitClosesCIVisibility`,
`TestForcedExitRecoversPanics`, `TestForcedExitFromTheSignalHandlerReturns`,
`TestForcedExitBeforeTheRuntimeInitializes`, `TestForcedExitBoundsItsWait`,
`TestCIVisibilitySignalHandlerNeedsTheOverlay`
and `TestCIVisibilitySignalHandlerKeepsIgnoredSignalsIgnored`. This package's
own tests run without the overlays and use `signal.Notify` and `signal.Stop`
in place of the hook's functions. Their shared cleanup waits for the
bootstrap's tracked background work first, which a shutdown that ends early,
as on a panic, can leave running.

## Crashes

A crash ends a test process without running deferred calls or exit hooks:
testing's `-timeout` alarm, an unrecovered panic in a goroutine other than the
test's own, a fatal error such as `concurrent map writes`, the race detector
with `GORACE=halt_on_error=1`, or a kill. Mini sends its events in batches,
so upstream and Mini without these hooks lose every event that the process
buffered, including the tests that passed. A panic in a test's own goroutine
already closes CI Visibility before it re-panics, as upstream. Mini keeps its
send cadence and closes CI Visibility where Go code still runs before two of
these crashes; ddto's test session reports the others (see below).

`CloseForCrash` in
[`civisibility_crash.go`](civisibility/integrations/civisibility_crash.go)
fails, with the crash, every test whose event is still open, then closes CI
Visibility as `forcedExit` does, at most a minute, and returns, and the crash
continues as without Mini. `createTest` records open tests, and `Close`
removes them, so Examples, benchmarks and manual tests count too; so does
`MarkTestFinished`, for a fuzz seed or root that completed and whose event the
fuzz queue closes later with its outcome. Quarantined and disabled tests keep
the final status skip. A test can end on its own while the crash fails it:
`failForCrash` runs under the test's lock, as `Close` does, and the span sets
the crash's status, error and final status as it finishes
(`Span.FinishWithTags`), so a tag that the test's own code sets meanwhile lands
before and is replaced, or after and is ignored; the event has one outcome. Other goroutines keep running
meanwhile: tests can finish, and their output can appear before the crash's.

### testing's -timeout

testing's alarm panics on a timer goroutine, which no test recovers. The
testing overlay wraps the argument of the alarm's single `panic`, on its line,
in `instrumentTestTimeout`, declared by `TimeoutHook` in
`internal/instrument/timeout.go`: it calls `CloseForCrash` with the panic's
message, error type `timeout`, and returns the message. Go's output and
stacks, which show `testing.go`'s line, do not change. ddto edits the alarm
only for a Mini runtime that declares the function, and leaves an alarm of
another shape unchanged.

### Unrecovered panics (--panic-hook)

Upstream has no hook before the runtime prints an unrecovered panic and exits.
With ddto's `--panic-hook`, `TransformPanic` in `internal/instrument/panic.go`
edits the runtime's `gopanic`: right after `preprintpanics`, on its line, it
calls the variable that `PanicHook` declares in the runtime, linknamed to
`unhandledPanic` here, with the panic's value as `preprintpanics` left it. The
runtime calls the value's `Error` or `String` method there for the last time
before it stops the other goroutines; `panicValueText` renders the result
without calling any method again. That also covers testing's timeout and a
`nil` dereference in a background goroutine. The runtime still runs no Go code
for fatal errors, the race detector's halt or a kill. The runtime prints a
fatal error's message before it starts copying its output to the crash output
(`runtime/debug.SetCrashOutput`), so that output starts with the goroutine;
with the hook, `fatal` and `throw` pass the message to `fatalthrow`, which takes
it as a new last parameter and writes it there right after `startpanic_m`:
after the secure-mode exit, and once the runtime keeps the crash output's
descriptor from being replaced or closed. Each crashing thread copies its own
message. Like `print`, the copy only writes. The hook is opt-in: every
package depends on the runtime, so the first build with it, per Go version and
build variant, compiles every package again, and a CI cache that is not saved
after the run pays that each time. ddto warns and builds without it when the
toolchain's `gopanic` has another shape or the runtime lacks the variable.

### The exit status during a crash

While `CloseForCrash` sends, M.Run can finish and call `os.Exit(0)`, so a
crashed test binary would exit 0 and go test would report `ok`.
`holdForCrash` makes `forcedExit` wait for the crash's close and then block,
so the crash ends the process with its own status, as without Mini. A second
crash meanwhile waits for the first one's close before it continues. ddto
installs the timeout and panic hooks only together with the exit hook, which
keeps that status.

## ddto's test session

Upstream reports one test session per test binary, which closes with the
binary's exit code. With ddto, the session belongs to the command:
`startTestSession` in `internal/runner/session.go` creates it, with a new ID,
ddto's command (`utils.TestCommand` of ddto's arguments) and its working
directory, writes them as `session.json` in a new directory and hands the
directory to go test's test processes as `DDTO_TEST_SESSION`, which
`goenv.Receive` removes while a test process initializes. A test process of a
runtime that supports it reports in that session:
- `HandedCLISession` in
  [`cli_session.go`](civisibility/utils/cli_session.go) joins it once it can
  create the process's record in the directory; otherwise the process reports
  its own session, as without ddto;
- the CI tags take ddto's command, so test events and the session name, and the
  module tags from which the backend computes the session fingerprint, match
  ddto's session;
- `CreateTestSession` uses the session's ID and working directory, sends no
  session event, and records the tags that it sets; each process keeps its
  session telemetry, as without ddto;
  `Close` closes the modules and appends those tags, with the capability and
  ITR correlation tags that the CI tags carry, and the exit code, to the
  record.

Modules and suites also append their start and end to the record, with their
IDs, names and services. The record holds no test event: each process sends
its own events. `internal/goenv/crashout`, which only the test runtime imports,
passes the crash output (`runtime/debug.SetCrashOutput`) to a file next to the
record while the process initializes, before any package that imports
testing. The record and every other session file are opened without Go's test
log, so they do not affect go test's cache.

Once go test exits, `FinishCLISession` in
[`cli_session_owner.go`](civisibility/integrations/cli_session_owner.go) sends
the session from ddto's process. It initializes only the CI tags, in go test's
directory, which `-C` can move from ddto's, from the Git worker's record of the
checkout when there is one, and otherwise with git commands that have ten
seconds in all, so a fetch that hangs does not keep ddto from exiting, with the
test processes' runtime version and architecture (`CLISessionEnd.GOARCH`, which
ddto's own can differ from, as under Rosetta), and the tracer, with the
bootstrap's service rules
(`ciVisibilityService`): no settings, Git upload, signal handler, logs or
telemetry. The
session starts with ddto, ends when go test exits, and has go test's exit code
and status, or is skipped when nothing ran (see
[empty test sessions](#empty-test-sessions)). Its tags merge the processes'
records: counts add up, `true` and
EFD's `faulty` win, and other values come from the first process. A process's
coverage covers only its own run, so the session takes it only when that
process is the only one that joined, and otherwise the statement total of the
command's `-coverprofile`, which counts a block once and reads file names
with spaces as Go does (`profileCoverage`). For a
process that ended without closing CI Visibility, it recreates, failed, the
modules and suites that the record shows open, with their IDs and services,
so the events that the process sent earlier keep their containers, and the
test that the crash output's crashed goroutine names: the
first frame of a top-level test function or a function literal in one, or else
the test function that created the goroutine, in a suite left open
(`crashedTest`); a `Benchmark` function's event is a benchmark's. Methods of
testify suites, `TestMain`, helpers and dynamic subtest names name no test. The session's error names the crash: a panic's
first line, or, for a fatal error, whose message the runtime prints before it
copies its output to the crash output, its first frame outside the runtime,
unless `--panic-hook` copied the message too. A
process without crash output was killed or stopped by a sanitizer. ddto sends
no session when no test process joined and go test succeeded, as when every
result came from Go's cache.

ddto does not own the session with the SDK runtime, `--session-per-binary`,
`-c` or `-n`, a launcher, which could run the test binaries where ddto's
directory is not, CODEOWNERS services, which give each package its own
service, a disabled CI Visibility, Bazel's offline modes, a tested package in
another Git checkout than go test's directory (`sameCheckout`; the session
takes its Git metadata and service from that directory, each test process from
its package; `utils.CheckoutGitFolder`), or a runtime whose
`civisibility/utils` lacks `HandedCLISession`, as the released ones. A test
binary run without ddto reports its own session. Tests that finished before a
fatal error, the race detector's halt or a kill are lost, since only that
process held them.

Checks: `TestMiniCrashKeepsEvents`, `TestMiniPanicHookKeepsExitStatus`,
`TestMiniTestSessionSpansTheCommand`, `TestCloseForCrashFailsOpenTestsAndCloses`,
`TestCloseForCrashOnce`, `TestCloseForCrashKeepsFinishedEvents`,
`TestFailForCrashIsAtomicWithClose`, `TestFinishCLISessionBoundsGit`,
`TestHoldForCrashReturnsWithoutACrash`,
`TestCrashedTestNamesOnlyUnambiguousTests`, `TestCrashMessage`,
`TestPanicValueTextCallsNoMethod`, `TestMergeCLISessionTags`,
`TestFinishCLISessionRecreatesOnlyOpenContainers`,
`TestFinishCLISessionSkipsAnEmptySuccess`, `TestToolchainTimeoutAlarmTakesTheHook`,
`TestTimeoutHookIsOptional`, `TestTimeoutHookMatchesMiniRegistration`,
`TestToolchainGopanicTakesTheHook`, `TestPanicHookMatchesMiniRegistration`,
`TestPanicHookRejectsOtherRuntimesAndToolchains`,
`TestSessionRuntimeErrorMatchesMini`, `TestCrashHooksNeedTheExitHook`,
`TestStartTestSessionRefusals` and `TestProfileCoverage`.

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
- `createTest` sets the hierarchy IDs, and `SetTestFunc` the source file and
  lines, through `setCIVisibilitySpanStringTag` (at the end of `meta.go`) and
  `Span.SetMetric`. Converting a string that is not a constant, or a number
  above 255, to `any` allocates; these setters take typed values, with
  `SetTag`'s semantics. Each call replaces the line of the original call, so no
  line moves.
- `createTest` starts its span with `tracer.StartTestSpan` instead of building
  an option list for each test. `StartTestSpan` takes the per-test values
  (resource, type, start, test name and service) and the shared lists:
  `constantOptions` (origin and manual keep, at the end of
  `manual_api_common.go`), `commonTagOptions()`, the CI metrics and the suite's
  tags, which `slices.Clip` marks as shared. It applies them in the order of the
  former list, so the event is the same. A test's `tags` field stays nil: no
  event inherits it. The new lines replace the list's lines one for one, so no
  line moves, and the `context` and `slices` imports keep their uses.
- Called without options, `GetOrCreateModule` and `GetOrCreateSuite` return an
  existing module or suite before they allocate options or read the clock,
  which only a new one uses; given options still run. `CreateTest` and
  `closeLocked` allocate their configuration only when options are given
  (`testStartOptions` and `testCloseOptions`, at the end of their files):
  options take its address, which moves it to the heap. An ordinary test
  starts without any, and closes without any when it passes and its close is
  not deferred for process retries. Examples, fuzz roots, benchmarks and
  deferred closes pass times, so they keep these allocations. These functions return before a test's body
  runs or start after it, so their frames are not in the error stacks that
  parity compares. `finalizeInstrumentedTestExecution` sets the final status
  through `finalStatusValue`, which returns the known statuses as constants
  instead of converting a string variable to `any`; its four calls replace the
  original lines. Checks: `TestExistingModulesAndSuitesAllocateNothing`,
  `TestTestOptionsWithoutOptionsAllocateNothing` and `TestFinalStatusValue`.
- `executeInternalTest`'s function calls `t.Helper()` only when an additional
  feature wrapper calls it: `directTestFunc` (at the end of
  `gotesting/testing.go`) records, at setup, whether testing runs it directly.
  The call allocates the test's helper map, and testing reads helper marks only
  to choose the frame that a log, failure or skip reports. When that search
  reaches the function that tRunner calls in a top-level test, it reports that
  function's frame, marked or not, so a direct function's mark changes no
  location, except in the case below; a wrapper's frames lie between it and
  tRunner, so a wrapped one keeps the call. The condition replaces the comment,
  the call and the blank line after it, the flag shares the known-test line of
  `commonInfo`, and the return line calls `directTestFunc` in place, so no line
  moves. Flaky test retries wrap every test while the retry count and the
  session's retry budget are positive, and a test that calls a helper allocates
  the map itself.

  One case changes, which we accepted for the cost. testing looks at no more
  than 50 frames (`maxStackLen`) when it chooses the frame. A test function that
  calls `t.Helper()` itself, with helpers nested exactly deep enough below it,
  makes Mini's function the last frame that testing looks at: in Go 1.27, 48
  nested helper calls. All the frames before it are helpers, so testing used to
  go past the marked function and report the line that logged; now it reports
  `testing.go:882`, as Mini already did for any shallower stack. `go test`
  reports the test function's line there, so neither matched it.
  Checks: `TestDirectTestFunc`, and `TestMiniHelperMarksKeepLocations`, which
  pins the locations of a test that calls `t.Helper()` itself, directly and
  with a retry wrapper.

CI string snapshots use the revision/escaped-map contract below; numeric CI
metrics remain fresh per call. Checks: `TestTestifyLookupMatchesReflection`,
`TestFindTestifyTestMatchesFinalElement`, `TestTypedSettersMatchSetTag` and
`TestStartTestSpanEncodesAsOptions` in Mini,
Testify and native parity. When
syncing, keep new per-test debug logs guarded without moving any line that can
appear in an error stack, and re-measure allocations per test with a
many-subtest fixture.

## Test execution metadata store

[`civisibility/integrations/gotesting/instrumentation.go`](civisibility/integrations/gotesting/instrumentation.go)
keeps each test's execution metadata in a `sync.Map` keyed by the
`*testing.T` or `*testing.B` pointer. Upstream uses one map behind a
process-wide `RWMutex`. Every test creates and deletes an entry, and every hook
(`T.Run`, `Error`, `Skip`, `Fail`) reads one, so in parallel suites each create
or delete held back the readers of all running tests. A mutex profile of 200
parallel tests with 50 parallel subtests each attributed most of Mini's lock
delay to this map.

The semantics are the map's: the pointer is the key, a second create replaces
the entry, deleting an absent key does nothing and only
`*testExecutionMetadata` values are stored. If per-test state later moves into
`testing`'s own structures, this keyed store remains the fallback for builds
without that overlay.

Measured on darwin/arm64 with 18 threads, medians of alternating runs: the
parallel fixture (10,200 tests, `-count=10`) went from 0.81–0.89 s to
0.66–0.71 s; a serial fixture of the same size stayed at 1.26–1.27 s. One
create, three lookups and a delete take 161 ns instead of 372 ns with 18
goroutines, and about 150 ns either way with one. `sync.Map` adds one 48-byte
entry per create. A 64-shard `RWMutex` map measured the same wall time.

Checks: `TestTestMetadataStoreKeepsMapSemantics`,
`TestTestMetadataStoreConcurrentTests` under `-race`,
`BenchmarkTestMetadataLifecycleParallel`, and the parity, retry and Testify
integration groups.

## Known-test lookups

Upstream `isKnownTest` in
[`civisibility/integrations/gotesting/testing.go`](civisibility/integrations/gotesting/testing.go)
scans the suite's list of known names, which includes subtests, for every
top-level test and benchmark while `M.Run` wraps them, before any test starts.
Wrapping a test asks twice: once for the known-test tag and again for Early
Flake Detection. A file with 1,000 tests and 10,000 known names compared about
20 million strings.

`matchKnownTest` keeps the scan for lists of up to 32 names. Longer lists are
indexed once into a set, keyed by the list's backing array and length; a
response is never changed after decoding, and a new response has new arrays.
`commonInfo` keeps the result, so the second question reuses it. Results are
unchanged, including the known-data flag: present but empty data, a missing
module or a missing suite still report data without a match.

Measured on darwin/arm64, looking up a new test: 0.93–0.97 µs with 1,000
names and 7.5–8.8 µs with 10,000 become 41–48 ns and 28–29 ns; lists of ten
names stay at about 25 ns. Building a 10,000-name set happens once per suite.

Checks: `TestMatchKnownTestKeepsSDKResults` compares the upstream function,
`TestMatchKnownTestIndexFollowsNewResponse`, `TestLookupKnownTestReusesResult`,
`TestMatchKnownTestConcurrentIndex` under `-race`, `BenchmarkMatchKnownTest`,
and the EFD and known-test parity cases.

## Error stacks captured once

Only a test's first formatted error is kept, but upstream built its stack
before the compare-and-swap that keeps it. Every `Error`, `Errorf`, `Fatal` or
`Fatalf` call walked and formatted the stack, and `instrumentSetErrorInfo`
captured another one that it then replaced with the formatted error's stack.
A failing test with many assertions paid for a stack per assertion.

`instrumentCaptureFormattedError` in
[`civisibility/integrations/gotesting/instrumentation_orchestrion.go`](civisibility/integrations/gotesting/instrumentation_orchestrion.go)
and `recordProcessRetryChildErrorInfo` in
[`civisibility/integrations/gotesting/retry_process.go`](civisibility/integrations/gotesting/retry_process.go)
now load the recorded error first and build a stack only when none is
recorded. The compare-and-swap still decides between concurrent first errors.
`instrumentSetErrorInfo` captures a stack only when no formatted error exists.
Stacks are still captured in the same functions, so their skipped frames do
not change, and no line moves in `instrumentation_orchestrion.go`, whose
closures appear in compared error stacks.

At 16 frames, a second or later formatted error took 5.4 µs, 15.5 KB and 40
allocations; it now takes 27 ns and none (darwin/arm64). The first failure of a
test saves one such capture in `instrumentSetErrorInfo`, and a process-retry
child saves one per error call.

Checks: `TestCaptureFormattedErrorKeepsFirstError`,
`TestCaptureFormattedErrorConcurrentFirstWins` under `-race`,
`TestRecordProcessRetryChildErrorInfoKeepsFirstError`,
`TestSetErrorInfoCapturesStackOnlyWithoutFormattedError`,
`BenchmarkCaptureFormattedErrorRepeated`, and the parity groups' error-stack
comparisons.

## Suites and modules of filtered tests

Each suite and module counts the workloads that have not finished, and closes
when its count returns to zero. Upstream counts every test, example and fuzz
target when `M.Run` starts, including those that `-test.run` or `-test.skip`
excludes. Those never start, so their suites and modules stayed open until the
close actions at exit, which run after the session closes: their end times were
the end of the process.

[`civisibility/integrations/gotesting/test_selection.go`](civisibility/integrations/gotesting/test_selection.go)
ports testing's matcher (`splitRegexp`, `rewrite`, simple and alternation
matches) and applies it as `matcher.fullName` does to top-level names: the
filter must match, and a skip pattern excludes a name only when it matches
completely, so `-test.skip=TestA/sub` still starts `TestA`. Slashes and bars
inside brackets or parentheses do not split. `instrumentInternalTests` and
`instrumentInternalExamples` count only selected names; fuzz targets are
counted when `-test.run` selects their seeds or whenever `-test.fuzz` is set.
Benchmarks are unchanged.

The flags are read at the start of `M.Run`, before it parses them: parsed
values are used when `TestMain` already called `flag.Parse`; otherwise the
arguments are parsed into copies of every registered flag, so no flag changes
and nothing is printed. Everything is counted, as upstream does, when the
binary does not match names with `testing/internal/testdeps`, a flag is
missing, the arguments do not parse or a pattern does not compile. In those
cases testing either matches differently or exits before running tests.
Everything is also counted when `-test.count` is not 1 or `-test.cpu` lists
several values. Tests then run in several rounds but are counted once, so
suites and modules close after the first round and upstream starts new suites
for later rounds; counting only the selected tests would change those
repeated-run events. Suites with filtered tests still stay open until exit in
such runs.

Checks: `TestTestingSelectionMatchesTestingBinary` compiles a fixture and
compares the selection with the tests, examples and fuzz seeds a real binary
runs for 27 `-test.run`/`-test.skip` pairs; `TestTestingFlagValuesMatchFlagParse`
compares the copies with `flag.Parse`; `TestMiniFilteredTestsFinishSuiteBeforeSession`
checks that a suite with filtered tests ends before the session. Re-check
testing's `match.go` when Go changes.

The suite counters themselves are keyed by module and suite
(`testSuiteCounterKey` in
[`civisibility/integrations/gotesting/testing.go`](civisibility/integrations/gotesting/testing.go)).
Upstream keys them by suite name only, which is a file's base name: two modules
of one binary with a file of the same name, such as subtest closures in helper
packages, shared a counter, so one of those suites closed only at exit.
`TestSuiteCountersAreKeyedByModule` closes two same-named suites of different
modules independently; the ported workload test counts by the same key.

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

That asynchronous feature initialization can still be running when the next
ported test starts. It runs `additionalFeaturesInitializationOnce` while it
holds `additionalFeaturesInitializationMu`. A test helper that replaces the
Once (`resetCIVisibilityStateForTesting`,
`disableAdditionalFeaturesForBootstrapTest`) takes that mutex first: replacing
the Once while the initialization runs zeroes the mutex that the
initialization then unlocks, and Go stops with "sync: unlock of unlocked
mutex".

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

## Removed APM code

The port keeps only what CI Visibility and Mini reach. Code that the general
tracer, AppSec, profiling or other products use was removed after a
reachability check over every entry point, including the `//go:linkname`
hooks that instrumented packages call. `excluded_features` in
[`SOURCE.json`](SOURCE.json) lists the removed areas. Removed files have no
manifest record, so `scripts/upstream.py diff` no longer reports their upstream
changes. Skip them during an update unless retained CI code starts to call one.

What each reduced package keeps, and how to merge upstream changes into it:

- `ddtrace/ext/tags.go` keeps the tags Mini sets: span name/type, service,
  resource, the error tags and manual keep. The other `ext` files are gone.
  Add a constant only when retained code uses it; do not restore whole files.
- `env` reads the environment directly. `Get` and `Lookup` keep one alias
  (`DD-API-KEY` for `DD_API_KEY`), and `IsSensitive` keeps `DD_API_KEY` and
  `DD_APP_KEY`. Do not copy the SDK's generated configuration registry
  (`supported_configurations.gen.go`, `supported_configurations.json`) or its
  test-mode writer. The registry made `env.Get` return `""` for any `DD_` or
  `OTEL_` key missing from it, and inside a test binary, which includes every
  user test run with Mini, it tried to write the JSON file beside its source.
  New keys need no registration. Port a new upstream alias or sensitive key only
  for a key the CI runtime reads or reports.
- `telemetry` keeps counts, distributions, logs, product start, app
  configuration and the app lifecycle. Rate and gauge metrics, integration
  reports, product stop/error reports, flush tickers and synchronous stack
  capture (`WithCaptureStacktraceNow`) are removed, together with their
  `Client` methods, payload types and `telemetrytest` recorder methods. Skip
  upstream changes to them. If upstream CI code calls one, port only that
  method and its test, and record it in `TESTS.json`.
  `TestStartupTelemetrySendsBeforeTests` injects its startup panic through a
  data source, in the flush step that used to run the flush tickers.
- `log` has no tracer log file. `stacktrace` keeps `SkipAndCaptureWithInternalFrames`,
  `CaptureRaw`, `SymbolicateWithRedaction` and `Format`; the AppSec capture
  APIs are removed. The contrib classification table stays, because telemetry
  log redaction uses it.
- Root `env.go` and `utils.go` keep the boolean/integer readers and tag parsing.
  `urlsanitizer` is removed.

Reachable APM-shaped code stays because it affects CI output: hostname cloud
probes (telemetry and CI log host), the known-metrics table (the `common`
flag) and the contrib classification table. SDK CI Visibility code that Mini
does not call, such as the manual `GetTest`/`GetBenchmark` wrappers, is also
kept for now to stay close to upstream.

Checks: `go vet ./...` with `GOOS` set to `linux`, `darwin` and `windows`,
`python3 scripts/upstream.py verify`, the full suite, and the reachability
procedure in the [maintenance guide](../../../docs/maintenance.md#removing-unused-upstream-code)
when an update adds or removes code paths.

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
5. Keep the [removed APM code](#removed-apm-code) out: skip upstream changes to
   removed files and APIs, and port a removed API only when retained CI code
   needs it. Remove any new APM-only code the update brings in.

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

`log/log.go` reads `DD_LOGGING_RATE` when the first error is reported
(`applyLoggingRate`, `loggingRateOnce`), where the rate applies, instead of at
initialization. ddto links this package and runs commands other than go test
unchanged; an invalid value made it print a warning before them. An invalid
value now warns at the first error, after `loggingRateOnce` returns:
`setLoggingRate` returns the warning instead of printing it, so a logger that
reports an error itself does not wait for the rate. `log/log_test.go` checks
the rate, the warning and such a logger.

## Subtests created by helper packages

dd-trace-go PR #5401 (`91a3d7a9d`) is newer than the recorded base, and its
subtest identity fix is ported; `feature_ports` records the PR's revision of
both files. A test can call a helper in an imported package that creates
subtests with `t.Run`. The callback's package then named the subtest's
module, so its known-tests, Early Flake Detection and test management lookups
used the helper's package. A subtest outside a Testify suite now takes its
parent's module when the parent's execution metadata has one
(`subtestModuleName`, at the end of `gotesting/instrumentation_orchestrion.go`);
the callback still gives the suite and source. In a quarantined child process,
`quarantinedSubtestIdentity` (at the end of `gotesting/quarantine_process.go`)
keeps a Testify suite's module and suite, and its method as the source when
known; another subtest takes its parent's module, or the selected root's from
the retry config. The PR's other half routes the SDK tracer around
`mocktracer` and `tracer.Start`/`Stop` in tests; Mini sends CI events through
its own tracer, which those calls do not reach, so it is not ported.

The port keeps line positions that error stacks show. In
`instrumentTestingTFuncWithSourceOptions`, the correction sets
`subtestIdentity.ModuleName` and later uses read it, instead of reassigning
`moduleName`: a reassigned name would be captured by reference and move to the
heap for every test. A shorter comment and one blank line make room for the
added lines, and `quarantinedSubtestIdentity` replaces the Testify block in
place. Checks: `TestSubtestModuleName`, `TestQuarantinedSubtestIdentity` and
`TestMiniHelperSubtestKeepsTheTestModule`, which fails without the port. The
pinned SDK that parity compares with predates the fix; no parity scenario
creates subtests from another package.

## Dynamic automatic test retries

dd-trace-go PR #5695 (`c5b8fe272`, open when ported) is newer than the recorded
base and is ported; `feature_ports` records its revision of each file. With
`DD_CIVISIBILITY_DYNAMIC_ATR_ENABLED`, a test's automatic retry count comes
from its first execution's duration, with inclusive bucket bounds: up to 5s,
10s, 30s, 5m, and slower. `DD_CIVISIBILITY_DYNAMIC_ATR_BUCKETS` sets the five
counts; otherwise they follow the backend's EFD slow test retries from before
`DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES` caps them, with 10, 5, 3, 2
and 1 for empty buckets. `FlakyRetriesSetting.DynamicRetryCounts` stays nil when
the feature is off. The port reads both variables directly, so the PR's env
registry entries do not apply. `dynamic_atr_retries.enabled` is added to
`known_metrics.common.go`, as upstream main lists it.

The port keeps line positions that error stacks show. In
`gotesting/instrumentation.go`, the new `executionOptions.initialDuration`
field takes the blank line after the struct, and the retry-wrapper check calls
`flakyRetryCountBeforeFirstExecution`, appended to `gotesting/final_status.go`
with the PR's other helpers, instead of adding lines and the `slices` import.
In `gotesting/retry_attempt_integration.go`, `recordFirstRetryAttemptDuration`
keeps the first execution's duration in the three lines that adjusted the
retry count. The PR's `efd_faulty_session_test.go`, `retry_process_test.go` and
`testcontroller_test.go` cases are not part of the port's tests, so
`gotesting/dynamic_flaky_retries_test.go` ports them and
`TestMiniDynamicFlakyRetries` checks retries with
`DD_CIVISIBILITY_FLAKY_RETRY_COUNT=0`, also with
`DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process`. It fails
without the port.

## Empty test sessions

dd-trace-go PR #5697 (`ee74b3d79`, open when ported) is ported; `feature_ports`
records its revision of each file. When `testing.M.Run` succeeds without
running a test, benchmark, fuzz target or example, as with a `-run` filter that
matches nothing, the session is reported as skipped with
`test.skip_reason:No tests were executed` and
`test.session.empty_reason:zero_tests`. The finalizer passes
`WithTestSessionSkipIfEmpty` only when `instrumentedAllWorkloads` finds that
every workload was wrapped to create a module. That method and
`testingMSessionCloseOptions` live in local `gotesting/testing_session_close.go`,
and `testing.go` takes the option in place of a blank line, so its lines keep
their positions.

ddto's own test session, which one `ddto test` command shares, has no modules
in ddto's process. A test process that joined it records the empty reason in
its close record instead, when `testing.M.Run` succeeded, and
`FinishCLISession` skips the session only when go test succeeded, no process
crashed, and every process that joined recorded it. `mergeCLISessionTags`
leaves the tag out. A binary with its own session, as with
`--session-per-binary`, follows the PR. Checks: `TestInstrumentedAllWorkloads`,
`TestCLISessionRecordsAnEmptyCloseOnlyOnSuccess`,
`TestFinishCLISessionReportsAnEmptySession` and `TestMiniEmptyTestSession`,
which fails without the port. The port does not include the files of the PR's
fuzz fixture scenario and `TestTestSessionSkipIfEmpty`:
`TestMiniEmptyTestSession` runs a fuzz target and an Example,
`TestFinishCLISessionReportsAnEmptySession` checks the cases without modules,
and a per-binary session that ran a test, as in
`TestMiniTestSessionSpansTheCommand`, still passes. The pinned SDK predates
the PR, so the tests that compare Mini with it expect its session-only events
as the PR reports them (`withPortedEmptySession`).

## Debug lines for reporting decisions

ddto adds the debug lines that
[`docs/cli-debug.md`](../../../docs/cli-debug.md#decisions-in-a-test-process)
lists where a test process decides what it reports: each test's dynamic
automatic retry count and its fallbacks from EFD (`gotesting/final_status.go`),
whether an empty session is reported as skipped
(`gotesting/testing_session_close.go`, `manual_api_ddtestsession.go`), where
the session's coverage comes from (`gotesting/coverage/test_coverage.go`), where
CODEOWNERS was searched (`utils/codeowners_discovery.go`), and why `git
pack-objects` moved to the objects directory (`utils/git.go`). Every line with
arguments is guarded by `log.DebugEnabled()`, including the once-per-process
and rare-path ones, so a disabled line costs one atomic load and allocates
nothing; an unguarded `log.Debug` call measured about 15 ns and one
allocation. Work that only a line needs, such as the directories that
CODEOWNERS discovery searched, happens inside the guard. No line that
error stacks show moves: `gotesting/instrumentation.go` changes one line in
place, to call `flakyRetryCountAfterSuppressedEFD`, and the deferred process
retry group passes its test's identity to the EFD transition for the test name.

## Panicking loggers

dd-trace-go PR #5582 (`b3ab4cb87`) is newer than the recorded base. Only its
`log.go` change is ported, and `feature_ports` records that file's revision.
`printMsg` releases the logger lock with `defer`, so a `Logger` that panics,
when its caller recovers, does not leave the lock held. A held lock would block
`UseLogger`, and then every log call. ddto installs no logger of its own
outside tests. The revision's `ParseLevel`, used for `OTEL_LOG_LEVEL`, and its
Feature Flags provider are APM and are not ported. Check:
`TestPanickingLoggerReleasesLock`, from the PR, which fails without the port.

## Test level in the settings request

dd-trace-go PR #5709 (`4f8d28427`, open when ported) is ported; `feature_ports`
records its revision of both files. The settings request in
`utils/net/settings_api.go` sends `test_level: "test"`, as the skippable tests
request already does: Go skips tests, not suites. The backend reduces per-test
coverage on branches that ITR excludes only for a settings request with a test
level, so Go collected full per-test coverage there. The request body is also
the key of the short-lived read cache, which ddto's Git worker and its test
processes share for up to an hour: entries that a ddto without the port stored
miss once and are stored again. Check: `TestSettingsApiRequest`, from the PR,
which fails without the port. The pinned SDK that parity compares with
predates the PR; parity compares events, not request bodies.

## Fuzz and executable Examples

SDK PR #5442 is integrated in the recorded `main` base. `feature_ports` keeps
the original feature revision for source history; ordinary file records and
differential fixtures now use the SDK base. `testingF.go`, `testingExample.go` and
`fuzz_events.go` retain the native lifecycle and original test assertions.
`instrumentation.go`, `instrumentation_orchestrion.go` and `testing.go` merge
that delta while preserving this port's coverage-aware shutdown.

At a forced exit, as `os.Exit` in a fuzz target, a root that still ran, with
no failure, is left out (`fuzzTestEvent.finish`), as a running test is,
instead of being passed. Check: the `fuzz target exit` case of
`TestMiniCrashKeepsEvents`.

The SDK installs a CI-only flush handler before publishing its tracer. Mini
owns its queue and flush contract, so that APM worker implementation is not
copied here. The compiler's SDK CI gates remain disabled when Mini is selected;
the SDK's lifecycle installer then follows its original APM path. Verify one CI
reporter and unchanged APM delivery with the SDK mirror and Orchestrion tests
after changing either side.

Tracer flushes route to `minitracer.Flush`. F roots hold deferred admission
through seeds and cleanups; examples hold it through output capture and event
finalization. The selective goleak wrapper names the owned fuzz waiter, both
example readers and the managed-example waiter. User leak negative cases must
remain failures. No sender/worker filters are widened to net/http or all testing.

`fuzz_offsets.go` joins the existing one-time testing layout discovery. It
validates common type/offset, F's own scheduler pointer and fuzzCalled bool.
`nativeResult` reads cached failure/skip fields under common.mu. Only the
post-M.Run drain reads duration; fatal drains use the captured finish time.
When the drain read Go's duration, `fuzzFinishTime` uses it even when it is
zero, clamping a negative accumulator to zero, so the event matches Go's
printed time. The root subtracts the wait for parallel seeds, which it drains,
from Go's accumulator; `completeParallelSubtestsWithState` measures that wait
with `startNativeClock`, testing's own clock: QueryPerformanceCounter on
Windows (`retry_attempt_time_windows.go`), the runtime's monotonic clock
elsewhere. On Windows, the runtime's clock is coarser, so a wait measured on it
could exceed the root's QPC-measured time and leave a zero or negative
duration; the event then fell back to the wrapper's own clock, which includes
the wait. `TestFuzzFinishTimeUsesKnownNativeDuration` and
`TestNativeClockNestsInTestingDuration` check both. dd-trace-go #5673
(`443dd2a55`) fixes the same skew upstream, still measuring the wait with
`time.Since`: `capFuzzParallelSeedsWait` caps it at the root's elapsed
QueryPerformanceCounter time, and `finish` reports a read zero or negative
duration as zero length. A wait measured on testing's clock already
stays within that time, and `fuzzFinishTime` already uses a read duration, so
neither change is ported. Go 1.28 replaces
`testing.common.start`'s highPrecisionTime with a time.Time, on Windows too:
`initializeRetryAttemptStart` and `addRetryAttemptElapsed` then set and read
it directly, and `startNativeClock` uses the runtime's clock on Windows, as
testing then does. Without that, a fresh retry attempt kept a zero duration on
Go tip. `TestRetryAttemptTimeHandlesATimeStart` covers a time.Time start on
every toolchain.
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
its resolver. It reads the CI workspace's CODEOWNERS with `DiscoverIn`, without
looking for an enclosing Git root, because paths from `-trimpath` builds are
relative to the repository. A workspace without the file has no rules, so the
Git worker and the test processes agree. An unreadable workspace is retried.
Only an unknown or missing workspace falls back to the current and binary
directories, the SDK base's last resort. The package implements GitHub and GitLab rules, including
inline-comment differences, ownerless GitHub rules, sections, defaults,
exclusions, roles and character classes. File selection follows each host's
location priority, including `docs/CODEOWNERS`.

Matching and owner classification use Go runes and standard Unicode tables.
Section names use `strings.ToUpper`. Keep these contracts when updating the
SDK; the package does not carry Unicode compatibility tables. Pattern and
matching limits apply per segment. File decoding accepts BOMs and common line
endings separately from the UTF-8 parser.

Results have private immutable state. Owner lists are deduplicated once. A
result encodes its tag on first use, because most rules never match a test's
file: this took about 30% of parsing time, and 2,000 rules now parse in 1.35 ms
instead of 2.09 ms (`BenchmarkParse`). `TestOwnershipTagOnFirstUse` checks the
laziness and concurrent first readers. File and directory patterns share compiled
tokens, ASCII literals use string equality, and rooted literal prefixes are
checked once before wildcard matching. A section without exclusions stops at
its winning rule. Sections with exclusions retain the scan needed for sticky
exclusions. Owner unions allocate only when another section adds an owner;
small lists use slices and large lists use a map, preserving declaration order.

Directory queries use an explicit target: a trailing slash includes the package
itself, terminal `/*` selects direct children (a rooted `/*` also the root
package), and other directory patterns can be inherited. No guessed filename determines the service. File and service
caches retain their process lifetimes. The [package guide](../../../docs/codeownership.md)
describes the contracts and Go tests to keep during an SDK update.

The CI bootstrap and `civisibility/utils/net/client.go` share the selected
service. Preserve that binding during SDK updates: settings and telemetry must
not name a different service from events or logs. The port reads environment
variables directly, so the new keys need no registration.
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
