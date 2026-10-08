# Validation contract

Mini is the default runtime. Explicit `--runtime=sdk` selects the unchanged
public dd-trace-go SDK, pinned to
[main at 96aedb31048c](https://github.com/DataDog/dd-trace-go/tree/96aedb31048c07e29e7a20a4333dc3b8d289c52d).
The reference is Orchestrion commit
[5c24783fcd76](https://github.com/DataDog/orchestrion/commit/5c24783fcd76f00cd1ff21c418a6662785d6c811),
installed as `v1.13.2-0.20260917114356-5c24783fcd76`.

## Architecture and dependency boundary

The driver imports only the Go standard library and its own packages. A targeted
`go list` supplies native `testing`, runtime and selected client-package metadata.
Preparation parses `testing` with `go/parser` and writes an overlay containing
changed sources, private hook declarations and an external runtime import for
each selected test package. Native Go performs the build.

Testify and goleak share library discovery. Known reachability without unknown
test imports uses `go list -find`; unknown imports require `-deps` to discover
libraries through helpers. Version and API validation happen before the native
build-cache lookup. A selective compiler wrapper transforms `testify/suite`
and, in Mini, reachable `go.uber.org/goleak`; a coverage bridge handles rewritten
`testing` sources when coverage includes them.
[Combined Orchestrion builds](orchestrion.md) use the same selective wrapper
to preserve application weaving and a single CI reporter. Mini also selects it
when the test graph reaches the full SDK, to guard its
CI reporter and install span-copy hooks. Builds needing none of these features
omit `-toolexec`. The POC has no configuration engine or build daemon. Selecting a
module-declared Orchestrion tool lets Go build/cache that tool before the test
build; Orchestrion owns any additional builds it needs for application weaving.

The nine SDK aspects are retained: M.Run, T.Run, B.Run, Fail, FailNow, formatted
errors, formatted skips, SkipNow and Parallel. Formatting wraps the already
formatted result, so String methods run once. The SDK's ownership marker retains
its existing name. Runtime retries, skip policies and finalization are owned by
the selected runtime. This POC depends on its private hook ABI and is intentionally version-pinned.

Virtual external test files anchor the public SDK import without editing project
sources. Temporary plans are invocation-local and removed after Go exits. There
is no additional persistent instrumentation cache. Test-result caching remains
Go's choice: explicitly select packages for result caching, and use `-count=1`
when fresh CI events are required. `-work` preserves Go's own work directory, but
the POC overlay is still removed; this POC does not provide retained overlay debugging.

## CI feature parity

The [feature inventory and differential matrix](ci-parity.md) compare Mini against
the full SDK/Orchestrion testing configuration. CI exports per-scenario counts
and supplemental hierarchy/span/telemetry evidence. [Testify validation](testify.md)
adds 26 policy/lifecycle cases plus selected-version, workspace, covered-library,
fast-bypass and cache-invalidation tests. Both local and external-module callers
reach the same original runner.

## Runtime checks

The tests compile actual native, POC and Orchestrion binaries using the same
fixture and temporary module graph. Adding Orchestrion may upgrade transitive
modules through Go MVS; that graph is used by both instrumented variants, and
an assertion verifies the SDK remains the pinned release. The reference YAML is
read directly from the installed SDK. The SDK backend reads the original module; the mini backend contains adapted CI source. See [mini runtime validation](mini-runtime.md).

| Contract | Proof |
| --- | --- |
| CLI activation | Unset enables parent-only mode; explicit true/false/parent/empty/custom values, real child processes and SDK-managed retries checked |
| TestMain, success, logs and exit codes | Native output with CI disabled; real SDK events versus Orchestrion when enabled |
| Subtests, nested tests, parallel children, cleanup and Context | Fixture asserts completion, callback order and cancellation before cleanup |
| Fail, FailNow, Error, Errorf, Fatal, Fatalf and Helper | Negative cases compare exit codes, error messages, stacks and source locations; formatting occurs once |
| Skip, Skipf, SkipNow | Native output and SDK skip semantics |
| Count, shuffle, list, JSON, tags and multiple packages | Native flags and selected test execution |
| Test-result cache | Second package-mode run is cached; count=1 runs again |
| Existing overlay | Both command flag and GOFLAGS preserve an intentional test failure |
| Race and atomic coverage | Actual instrumented fixture builds/runs; SDK events compared to Orchestrion |
| Benchmarks | Native execution and reference SDK event semantics |
| Fuzz and executable Examples | Exact SDK PR #5442 workloads/assertions, 64 event comparisons plus 14 atomic coverage combinations; normal/deferred and goleak |
| Process retries | First attempt fails, second passes; reference events and process exit |
| EFD, ITR, disabled, quarantine, attempt-to-fix | Real SDK requests against loopback policy responses and reference event equivalence |
| Panic, Goexit and timeout | Abnormal exit and diagnostic marker; enabled/disabled reference event equivalence |
| Unsupported input | Missing/ambiguous hooks, malformed source and double instrumentation rejected |
| Mini + Orchestrion | Explicit wrappers, GOFLAGS and toolexec; injected APM span and HTTP delivery; one CI hierarchy, Testify, goleak, retries, coverage and race |
| SDK span copies under Mini tests | Independent APM/CI HTTP captures; original propagation and parentage, final fields, pooling, parallel/retry isolation, Testify, seeds, benchmarks, goleak, coverage and Orchestrion |
| Command line | Go's own package/flag classification, `-C`, `--flag` spellings, custom test flags, overlay precedence and chained `-toolexec` |

The backend responses are synthetic, but hooks, retry processes, serialization
and network requests come from the real SDK. Fixtures inherit a small whitelist
of tool/path environment variables; real API keys and CI credentials are excluded.
Events are captured over loopback using a synthetic key.

The comparison retains event type, name, resource, error flag, semantic test
attributes, error stacks and source positions. It excludes generated IDs,
durations, timestamps, execution order and invocation names. It does not prove
identity of binaries or generated source bytes. The added parity matrix validates
the event ID hierarchy and additional CI span parentage independently.
Parallel child completion order and elapsed times may differ in native output;
line content and multiplicity are retained.

## Scope limits

The default Mini backend uses this module. The SDK backend requires the exact
unreplaced SDK. Standard library test targets are unsupported; explicit Go file mode runs native `go test`
without instrumentation. Testify callers in client and external modules use the original
selected runner; dedicated version fixtures cover v1.10.0, v1.11.1 and v1.12.1.
Application weaving belongs to Orchestrion. Mini's SDK integration copies
context-associated spans while retaining their APM behavior.
The AST transformer validates hook presence and ambiguity and selected shape
constraints; future Go source/ABI changes still require a new compatibility run.
The [compatibility workflow](../.github/workflows/compatibility.yml) defines the
supported test matrix. Adding a Go or SDK version requires exercising its private
hooks and runtime behavior. Long-running campaigns and arbitrary downstream modules need separate validation.
[Fuzz and Examples](fuzz-examples.md) records the bounded campaign and feature cases.

## Mini runtime contracts

The current mini/SDK wire contract compares CI attributes and metrics while
explicitly excluding APM sampling, profiling and process enrichment. It also
checks complete envelope metadata, native hierarchy IDs, duplicate aliases,
service version and all session-name fallback cases. The unmodified SDK remains
the oracle; fewer APM fields is intentional.

| Contract | Proof |
| --- | --- |
| Service version and session name | `TestMiniCIConfigurationWireParity`: explicit `DD_VERSION`, custom tags, automatic command/job name, explicitly empty session name |
| CI payload byte limits and gzip | `TestCIByteBatchingAndCompression`: multiple real decoded batches, all events delivered below 5 MiB in both agent/agentless modes |
| Oversized events and delivery failures | `TestLargeBatchFailureRetainedAndOversizedEventRejected`: single-event rejection, failed batch retention and recovery |
| Slow and failing intake | `TestSlowIntakeDeliversEveryEvent`: concurrent senders and waiting finishers lose no event behind a slow intake; `TestFailingIntakeDoesNotStallFinishers`, `TestCloseReleasesWaitingFinisher` and `TestDeferredDeliveryNeverWaitsForSpace`: no finisher waits on a failing intake, after closure or in deferred mode; `TestDeferredCheckpointSendsConcurrently` and `TestDeferredCheckpointStopsAfterFailure`: checkpoints send up to eight batches at once, finish every sender before returning and start no batch after a failure |
| Temporary files | Every Mini run of the parity matrix, the deferred matrix and the Testify parity uses its own `TMPDIR`, which must be empty afterwards; mutating pack-file cleanup fails the git upload case |
| Retried parallel tests | `TestMiniInProcessRetryKeepsAllocsPerRun`: under `-count=2`, `testing.AllocsPerRun` still works after a parallel test that passed at once and one retried in process |
| Bazel output and offline mode | `TestMiniBazelOfflineAndPayloadFiles`: real manifest/cache, test/coverage/telemetry JSON files versus SDK, zero HTTP requests; native file writer error propagation tested separately |
| Parallel and retry coverage attribution | `TestMiniParallelAndRetryCoverageAttribution`: both runtimes compiled with `-race -covermode=atomic`, exact distinct-function bitmaps and initial-attempt-only retry policy |
| Coverage and global local-zone changes | `TestMiniCoverageWithGlobalTimeChanges`: Mini mutates `time.Local` under race/atomic coverage at 4/32 CPUs, ordinary/deferred delivery and telemetry off/on; exact bitmaps and CI attributes compared with a safe SDK run |
| Coverage processing lifetime | `TestCoverageProcessingFinishesBeforeShutdown` and deferred group/error checks: profiles captured at test boundaries, processing outside active deferred groups, completion also on profile errors |
| Telemetry startup and response lifetime | `TestStartupTelemetrySendsBeforeTests` and `TestWriterFlushWaitsForResponseCompletion`: `StartApp` returns only after its startup request; configuration, retry, concurrent close and failed initialization checked in both modes; response EOF handshake joined for 200/503 responses |
| CI product metadata | Expanded pass/error/policy comparison retains capability tags and ITR correlation; delayed session enrichment is checked |
| Original CI assertions | Ported SDK tests, including retry runtime/parallel ownership, coverage writer/profile, ITR backfill, source metadata and lifecycle; [exact provenance](../internal/thirdparty/dd-trace-go/TESTS.json) |

Actual Bazel compiler invocation and real Datadog intake/UI acceptance remain
unverified. Loopback and payload-file fixtures prove their local contracts.

Mini and the CLI import only this module and the Go standard library. Consumer
fixtures check that adding `testopt` requires no external runtime modules and
that the resulting program builds offline. The repository's test dependencies
never enter the consumer's packages; its module graph lists only them, at the
old versions this module requires, and the check rejects a newer Testify. The
[maintenance checks](maintenance.md#verification-before-publication) verify this
boundary after source updates.

## Compatibility workflow

The [workflow](../.github/workflows/compatibility.yml) has six native jobs:

| Platform | Go | Suite |
| --- | --- | --- |
| Linux | 1.26, 1.27 | Separate normal and `-race` jobs for each version |
| macOS | 1.27 | Normal suite |
| Windows | 1.27 | Normal suite |

Every job audits incorporated sources and licenses, verifies module inputs,
runs `go vet` and executes the complete compatibility suite. The report step
requires the independent Orchestrion reference and exports feature outcomes,
counts and timings. Logs and JSON reports are uploaded even when a test fails.

For a PR, inspect the jobs for its current head and merge revision. The workflow
configuration describes what runs; successful execution must be checked on that
revision. Cross-compilation proves a target builds, while a native job checks
that target's runtime behavior. The [latest local comparison](benchmarks.md)
records Linux measurements separately from GitHub Actions.

## Selective-tool validation

The [recorded Linux Go 1.27.1 dataset](results/20261005-linux-go1.27.1/parity/README.md)
repeats all 26 Testify cases and seven deferred Testify cases against the full
SDK/Orchestrion reference, within the 115-case matrix. Six rounds pass at each
of 4/32 CPUs. This is local protocol and runtime evidence; native macOS/Windows
execution is checked separately by the compatibility workflow.

[`TestTestifyVersionGuardWithWarmVendoredSources`](../integration/vendor_cache_test.go)
warms the build cache with supported vendored sources, then changes consistent
replacement metadata to an unsupported release while keeping source bytes
identical. Preparation must reject it even if Go would reuse the suite archive.
Other [selective-tool tests](../integration/selective_tools_test.go) check native
compiler/linker identities, bypass exit status, Unix process replacement,
coverage, workspaces and fingerprint invalidation. The compile-only matrix
records unchanged cache hits and cross-strategy reuse separately from runtime
feature comparisons.

The source-range regression fixture checks an optimized constant-sum test against
its literal lines 5–10. Normal, unoptimized and race builds must all send that
range. CLI checks cover default Mini selection, both runtime option forms after
Go flags, an unchanged Go flag value that resembles `--runtime`, and invalid
runtime values rejected before a Go process can start. The compiled default
binary must contain Mini symbols and no SDK runtime symbols.
