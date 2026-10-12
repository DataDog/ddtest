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
  `time.Local`, which coverage workers must not read while a test may change it. The parser accepts and
  skips the same lines as `strings.Fields`, `strings.Split` and
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
  which needs it.
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
  (`computeAnalyzer`).

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
  `/` and `_` alike (`UnixDataSocketURL`). CI scopes,
  which both already share, stay. The entries must outlast a build
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

A run whose results all come from Go's test cache sends no request. ddto
parses go test's arguments as go test does (`internal/runner/options.go`) and
records in `tests.json` whether go test runs every test package
(`RunsEveryTest`): in local directory mode, or when a test binary gets an
argument outside go test's cacheable flags, from the command line or
`GOFLAGS`: a flag such as `-count=1`, an unknown flag, an argument after the
package list, `--`, which go test hands on itself, or after `-args` anything
but `-test.<cacheable flag>=<value>`, as go test checks the arguments that it
hands on as they are (`noteTestArgument`). Only then does the worker search
the commits and request while go test builds. Otherwise ddto adds its tool to
go test in its `link` mode (`plan.linkSignal`), and the tool marks the state
`linking` (`utils.CLIGitLinkingFile`, `runner.NoteTestLink`) when go test
links a test binary: go test checks its cache just before the link and links
only for a result outside it. The worker does both once that mark, or the
`used` one, appears (`waitFor`), so the requests run while the binary links,
and a run whose results all come from the cache sends none. go test checks its
cache again after the link, by the binary's content, so a link does not always
mean a test process: `-o`, which saves the binary, and `-a`, which rebuilds
everything, link even for cached results, so ddto adds no signal with them
(`linksCachedResults`) and the worker waits for a test process. A change that
leaves a test binary identical, such as a comment, still makes go test relink
it and take its result from the cache: the worker then requests although no
test process runs, an accepted cost of the earlier requests. The tool, which
lets every tool through after its fast check, keeps Go's cache keys: cmd/go
takes each tool's identity from its `-V=full` output through the wrapper. The cacheable flags are the
newest supported Go's: Go has only added to them, so a flag outside them
disables the cache in every version. Test processes outside the worker's
checkout, binaries run outside `ddto test` and the SDK runtime keep their own
requests and scopes.

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

Here the fallback is the repository's common git directory: it holds the
objects, so the move succeeds, and the working tree never receives a temporary
directory. `git rev-parse --git-common-dir` runs without command telemetry, so
git metrics match the SDK. A failed attempt or an empty result removes its
directory, and `RemovePackFiles` removes the directory after the upload.

Before the first attempt, `packObjectsFolders` compares the filesystems of the
temporary directory and the git directory: device numbers on Unix, volume names
on Windows. When they differ, git could not move its pack, so it packs once, in
the git directory, instead of repeating the whole pack after a failure. The
failed attempt also left git's temporary `tmp_idx_*` and `tmp_rev_*` files in
the repository's `objects/pack` directory on every run. Where the filesystems
cannot be compared, the temporary directory is still tried first. The git directory is now looked up before every packing, which adds one
`git rev-parse` when the temporary directory works.

Checks: `TestRemovePackFilesRemovesTemporaryDirectory`,
`TestPackFilesFallBackToGitDirectory`, `TestFailedPackFilesLeaveNoDirectory`,
`TestPackObjectsSkipsTemporaryDirectoryOnAnotherFilesystem`
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

Mini records those ends. ddto's testing overlay adds `ParallelStopHook` from
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
the overlay, so `TestSharedParallelLeaseAttemptsRecordTheirEnd` and
`TestProcessRetryParityFreshRunnerSharesRootParallelLeaseAcrossSequentialAttempts`
leave the accounting unbalanced for the rest of the binary. The package's
allocation checks therefore use its `allocsPerRun` helper instead of
`testing.AllocsPerRun`, which would panic with `-count` above one or with
`-shuffle`. `TestPackageTestsMeasureAllocationsWithHelper` keeps it that way.

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

## Fuzz and executable Examples

SDK PR #5442 is integrated in the recorded `main` base. `feature_ports` keeps
the original feature revision for source history; ordinary file records and
differential fixtures now use the SDK base. `testingF.go`, `testingExample.go` and
`fuzz_events.go` retain the native lifecycle and original test assertions.
`instrumentation.go`, `instrumentation_orchestrion.go` and `testing.go` merge
that delta while preserving this port's coverage-aware shutdown.

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
`TestNativeClockNestsInTestingDuration` check both.
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
itself, terminal `/*` selects direct children, and other directory patterns can
be inherited. No guessed filename determines the service. File and service
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
