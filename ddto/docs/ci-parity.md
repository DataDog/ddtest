# CI Visibility feature parity

Mini registers `testify/suite` at its original entry, including callers in external
modules. A 26-case Testify comparison checks the original runner, lifecycle,
policies and covered helpers. The native `testing` path has 64 scenarios against
the unmodified SDK instrumented by Orchestrion. Together with 16 deferred testing
and seven deferred Testify cases, these make 113 comparisons. Fuzz and executable
Examples add 64 comparisons and 14 atomic-coverage combinations against SDK PR #5442. Additional
fixtures check manual hierarchy calls, CI child spans, multiple package binaries,
Unix sockets, a bounded fuzz campaign and CI telemetry.
This evidence does not establish complete product parity.

The automatic goleak fixture checks both delivery modes, external helpers,
race, covered library inputs and deliberate test/HTTP leaks. See
[delivery and goleak](delivery.md) for its contract.

The SDK reference is `dd-trace-go/main` at
[`870449702d0a0cea26a6223eefe2f0a198069d79`](https://github.com/DataDog/dd-trace-go/tree/870449702d0a0cea26a6223eefe2f0a198069d79),
also the base of our incorporated CI source. The exact module version is owned by
[`internal/version`](../internal/version/version.go). Orchestrion is pinned to
`v1.13.2-0.20260917114356-5c24783fcd76`; the fixture loads `gotesting/orchestrion.yml`
from that SDK checkout, including its Testify rule. Comparing only the POC's
`sdk` and `mini` backends would hide a transformation missing from both.

```mermaid
flowchart LR
    Fixture["Same fixture and<br/>policy responses"] --> SDK["Orchestrion + pinned<br/>SDK"]
    Fixture --> Mini["POC + Mini"]
    SDK --> Capture["Loopback events and<br/>payloads"]
    Mini --> Capture
    Capture --> Compare["Counts, ancestry and<br/>CI attributes"]
    Compare --> Report["Runner JSON and<br/>Markdown evidence"]
    Testify["Testify policy and<br/>lifecycle fixtures"] --> Report
```

## Feature inventory

"Verified" means the named fixtures prove the described contract. "Partial"
identifies narrower evidence or a remaining integration boundary. The individual
policy combinations and counts are exported by every workflow run.

| Feature | Evidence and combinations | Status |
| --- | --- | --- |
| Session, module, suite and test lifecycle | Native `testing`, `TestMain`, two source suites, multiple package binaries; unique IDs, resolved hierarchy and event multiplicity | Verified |
| Pass, failure and skip | All six failure entrypoints, Helper stacks, three skip entrypoints/reasons, source locations and exit status | Verified |
| Subtests and cleanup | Nested children, Context cancellation, cleanup order; disabled/quarantined children and subtest feature gate | Verified |
| Parallel execution | Parallel children with count/shuffle; ATR in both execution modes; exact parallel coverage attribution under race | Verified |
| Automatic test retries (ATR) | Recovery/exhaustion, zero budget, environment on/off override, process and in-process execution; parallel tests and coverage | Verified |
| Early flake detection (EFD) | New/known tests, retry cap, faulty-session abort; ATR combination, impacted known tests and parallel retry execution | Verified for these decisions; long slow-test duration thresholds have unit evidence only |
| Test impact analysis / ITR | Skip, forced run, unskippable annotation, missing-line-coverage response; coverage, ATR/EFD, disabled/quarantined and attempt-to-fix combinations | Verified |
| Impacted tests | Actual two-commit Git diff; known-test EFD reruns, ITR interaction, environment disable | Verified |
| Test management | Disabled, quarantined, attempt-to-fix; overlapping flags, ATR/EFD, ITR, subtests, feature override and both retry modes | Verified for selected overlaps; not every permutation |
| Per-test coverage | Atomic/race coverage, pass versus skip, exact distinct-function bitmaps with count/shuffle and retries; initial-attempt-only behavior matches SDK | Verified |
| Cleanup coverage | Cleanup-only code, parallel children, retries, skip/failure and terminal panic; normal/deferred delivery | Mini correction checked directly; the pinned SDK omits cleanup-only lines |
| Coverage report upload | Gzip multipart LCOV and its complete event metadata; combined with ITR and per-test coverage | Verified |
| CI logs | Actual logs intake JSON, test/trace correlation and line multiplicity; retries and manual hierarchy logs | Verified |
| Git metadata and upload | Commit/base metadata, CODEOWNERS and actual Git pack upload when settings require it; original Git fixture assertions retained | Verified |
| CI providers, version and session name | GitHub wire fixture; ported SDK provider fixtures, `DD_VERSION`, custom tags and explicit/job/command session naming | Verified at those levels; other providers have unit evidence |
| Empty selection and list | Session-only events | Verified |
| Fuzz roots, seeds and executable Examples | SDK PR #5442: 16 scenarios, manual/automatic, normal/deferred, atomic coverage and goleak | See the [78-comparison feature matrix](fuzz-examples.md); active mutations and workers emit no events |
| Benchmarks | Same benchmark events, metric keys/types and run counts; values must be finite/nonnegative | Verified semantics; measured timings/allocations differ |
| Panic, Goexit and timeout | Existing reference fixtures compare diagnostics, exit status and abnormal finalization | Verified for those fixtures |
| CLI activation and Go flags | Parent default, explicit values and child inheritance; overlays, GOFLAGS, tags, JSON, selection and native result-cache semantics | Verified |
| Agent and Agentless | HTTP EVP v2, gzip Agentless test-cycle payloads, per-test coverage and ATR/ITR combinations; settings/test-cycle over Unix sockets on Unix CI | Verified loopback protocol; real Agent/intake unverified |
| Payload size, retries and failures | Native byte/count bounds, oversized rejection, failed-batch retention, gzip reuse, 403/429/5xx and cancellation tests | Verified Mini contracts; not an exhaustive SDK/Mini delivery stress comparison |
| Bazel | Manifest/read cache plus test, coverage and telemetry payload files; no HTTP requests | Partial: file/offline contracts verified; actual Bazel invocation unverified |
| Manual hierarchy API | Two modules/four suites/twelve tests; repeated lookup/close, statuses, error/custom tags/metrics, logs and child span | Verified internal API; no new public API added |
| Additional CI spans | Two explicitly CI-marked spans attached to the active test context, including test → parent → child identity; manual hierarchy child span | Verified internal span API; application weaving belongs to Orchestrion |
| SDK span copies | Independent APM/CI captures; final fields, original propagation/parentage, pooled contexts, parallel/retry isolation, Testify, seeds, benchmarks, goleak, coverage and Orchestrion | Verified for the [supported SDK layouts and contexts](sdk-span-mirror.md); isolated retry children and active mutation workers have no local Mini identity |
| Context propagation | W3C and Datadog carriers, 128-bit identity, extraction by SDK propagator | Verified carrier compatibility; Mini identity does not automatically reparent APM spans |
| CI telemetry | Original CI instrumentation/unit assertions; wire fixture compares semantic CI count/rate metrics and validates request counters against actual HTTP requests | Partial: representative wire counts verified; distributions/policy cross-product unverified; failure counters have known differences |
| Duplicate Testify methods | Two suite types under one parent, repeated invocations, nested children and ordinary siblings; covered library entry and race | Mini correction checked directly; the pinned SDK misattributes duplicate method names |
| `testify/suite` | 26 Mini cases against full SDK/Orchestrion, plus a POC SDK pass/skip control; version fixtures v1.10.0/v1.11.1/v1.12.1, aliases, helpers, lifecycle, retries, management and race/coverage | Verified for local and external-module callers; method-level ITR retains the SDK limitation |

The inventory follows the SDK's CI integrations, manual API, coverage, feature
selection, Git/settings clients, telemetry and testing YAML. The source and test
manifests identify the copied production files and which original tests were
ported. A copied file is implementation evidence, not a substitute for runtime
proof. A green run is a regression gate for this inventory, not a promise that
all combinations or downstream applications have been tested.

## Event counts

Counts below are representative Linux Go 1.27.1 observations. CI produces the
same table per runner from its own captured events. Retry attempts each produce
a test event, while their module/suite/session are closed once by the controller.

| Scenario | Sessions | Modules | Suites | Tests | Spans | SDK versus Mini |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Pass | 1 | 1 | 1 | 1 | 0 | Equal |
| Nested tests, cleanup and Context | 1 | 1 | 1 | 6 | 0 | Equal |
| Parallel tests, count=2, shuffle | 1 | 1 | 1 | 18 | 0 | Equal |
| ATR recovery / exhaustion | 1 | 1 | 1 | 2 / 3 | 0 | Equal in both execution modes |
| EFD new test | 1 | 1 | 1 | 3 | 0 | Equal in both execution modes |
| Two package binaries | 2 | 2 | 2 | 2 | 0 | Equal |
| Empty selection / list | 1 | 0 | 0 | 0 | 0 | Equal SDK limitation |
| Manual hierarchy | 1 | 2 | 4 | 12 | 1 | Equal |
| Test with CI parent/child spans | 1 | 1 | 1 | 1 | 2 | Equal |
| Testify suite with pass/skip methods | 1 | 1 | 2 | 3 | 0 | Equal, including hierarchy and method source metadata |

The 64 testing scenarios total **64 sessions, 62 modules, 63 suites and 142 test
events** in each runtime, with no extra spans. Additional span fixtures deliberately
create them. This aggregate is a fixture observation, not an expected count for
an arbitrary build; per-scenario counts and ancestry checks prevent equal totals
from hiding a missing or duplicated event.

## Comparison contract

The harness decodes actual MessagePack envelopes and preserves event kind/version,
service/resource/type, errors, custom tags, CI metadata and CI metrics. It compares
multiplicity and exit status. Every test resolves its session/module/suite; IDs
are unique and ancestry is consistent. Additional spans retain their test and
span parentage. Logs resolve their test IDs before random IDs are normalized.
Coverage bitmaps resolve their events and compare exactly. The multipart report
is decompressed and its full LCOV text and event JSON are compared.

Only declared differences are normalized:

- Generated IDs, timestamps, durations and execution order vary between runs.
  Their relationship is checked separately before comparing attributes.
- APM sampling, profiling, process enrichment, runtime identity and redundant
  Git/hierarchy aliases are excluded. Aliases must agree with native CI fields
  when present. Unknown CI attributes and custom tags remain in the comparison.
- Shared CI, Git, OS and runtime strings can come from event-kind envelope
  metadata instead of each event. This is an accepted change from the SDK. The
  comparator expands only those declared keys, and raw-payload assertions check
  their placement and overrides.
- Mini retains capabilities and ITR session metadata. Missing SDK session values
  may be inherited from consistent test values, following the approved contract.
  Session-only runs validate Mini's complete capabilities against the pinned
  declarations. Present but incorrect values and missing Mini fields fail.
- Benchmark measurement values may vary; names, types, run count and valid
  numeric values remain required. CI request counters are checked against each
  sender's requests because batching can differ. All other captured CI semantic
  counters compare exactly.
- Relocated SDK library stack paths are canonicalized. The exact internal
  function/line pairs below account for inserted hooks and helpers. Testify's
  embedded panic stack uses the same mappings; its `Error Trace` lists only
  file/line locations. Application frames and other library locations remain
  strict. For the six `testing` failure methods, Orchestrion's `<generated>:1`
  frame and the toolchain's `testing.go` frame are equivalent by method name.

| Mini location | SDK location | Internal function |
| --- | --- | --- |
| `testing.go:867` | `testing.go:864` | `(*M).executeInternalTest.func1` |
| `instrumentation_orchestrion.go:427` | `instrumentation_orchestrion.go:424` | `instrumentTestingTFuncWithSourceOptions.func1.1` |
| `instrumentation_orchestrion.go:433` | `instrumentation_orchestrion.go:430` | `instrumentTestingTFuncWithSourceOptions.func1` |
| `instrumentation.go:775` | `instrumentation.go:775` | `applyAdditionalFeaturesToTestFunc.func2` |
| `instrumentation.go:1011` | `instrumentation.go:1011` | `runTestWithRetry` |
| `instrumentation.go:1138` | `instrumentation.go:1138` | `runRetryAttemptCapabilityFallback` |

The [Fuzz/Examples comparator](fuzz-examples.md) keeps application
frames strict and canonicalizes incorporated source lines separately. Go 1.26
can number Mini's root and deferred closures `.func2`/`.func2.1`; the comparator
maps only those known F-root owners to `.func1`/`.func1.1` in `testingF.go`.

Mini corrections intentionally differ from the frozen SDK. Coverage includes
code executed only in `t.Cleanup`, and duplicate Testify method names retain the
client suite and source location. `TestMiniCoverageIncludesCleanup` and
`TestMiniTestifyDuplicateIdentity` assert those expected payloads directly;
the differential comparator does not hide either difference. They run with the
complete suite on every CI platform.

Mini also reports the declaration start of a confirmed named function, even
when its optimized entry PC points at the closing brace. The constant-sum
regression requires lines 5–10. The multi-package fixture requires 5–9 for
`TestOther` in Mini; its SDK start may be an instruction within that range.
After checking both contracts and the exact end, that one SDK start is adjusted
in a copy for comparison. Raw captures and all other source fields are retained.

The span fixture explicitly marks both runtimes' spans as `ciapp-test`. Mini
adds that origin automatically; SDK public span creation does not automatically
copy it to each span. This is an intentional CI-only enrichment difference.
The fixture does not stand in for an external APM integration.

The manual fixture waits through the SDK's existing `GetKnownTests` initialization
barrier before creating modules and tests. Without it, the SDK can omit capability
metadata from the entire hierarchy on fast runners. Mini deliberately has no
barrier; a delayed settings response verifies its static capabilities are already
available. This stabilizes the reference without changing the SDK or filling
missing test attributes in the comparator.

## Run and maintain the gate

Run from this checkout with the frozen reference available:

```sh
go -C testdata/orchestrion build -mod=readonly -o "$(go env GOPATH)/bin/orchestrion" github.com/DataDog/orchestrion
mkdir -p artifacts
ORCHESTRION_BIN="$(go env GOPATH)/bin/orchestrion" \
  PARITY_REPORT_PATH="$PWD/artifacts/parity.json" \
  go test -v -count=1 -timeout=30m ./...
python scripts/parity_report.py artifacts/parity.json --output artifacts/parity.md
```

On Windows set `ORCHESTRION_BIN` to `orchestrion.exe` and use `-timeout=55m`.
Linux race validation uses `PARITY_TEST_MODE=race`, `go test -race -timeout=40m`
and a distinct `parity-race.json` prefix. The parallel/retry
coverage test explicitly compiles the fixture with `-race -covermode=atomic`;
the outer harness's `-race` alone would not make every child binary a race build.
Windows Testify reference builds use `-work` with a fixture-owned
`GOTMPDIR`. The pinned Orchestrion job server can keep its stderr log open while
Go tries to remove its build directory. Both the shared matrix and the independent
plain-coverage case use the same build helper. The matrix retains the directory
until both delivery modes finish and cleans it through `TestMain`; the independent
case uses `testing.TempDir` cleanup after its assertions. Both owners retry
transient Windows locks for up to two seconds. Compilation and persistent cleanup
errors still fail; event comparisons and the SDK reference are unchanged.
Without the Orchestrion variable, the matrix can run against the POC SDK backend,
including Testify, but the report renderer rejects a full parity report without
the independent Orchestrion reference.

[`compatibility.yml`](../.github/workflows/compatibility.yml) runs the suite on
Linux Go 1.26/1.27 and macOS/Windows Go 1.27. The Linux Go 1.27 race suite runs
Mini first; the normal suites run the SDK first. Each differential configuration
runs the complete suite once, split into test groups. Go 1.25 runs native Mini
in normal/race modes. A nightly native job builds a recorded Go tip revision and
also checks manual SDK span copies. The frozen Orchestrion reference fails on
tip, so this job does not establish Orchestrion parity. The matrix runs on pull requests,
pushes to `main`, the nightly schedule and manual dispatch; feature branch pushes use the pull request
run instead of launching a second matrix, and [validation](validation.md#compatibility-workflow)
describes when a run is skipped. Artifact names include the mode.
Each group uploads JSON counts, supplemental evidence and logs; the final job
renders a Markdown table per configuration, which also appears in the GitHub job summary. Artifacts are retained for seven
days; download them before expiry to keep a run beyond that period. Missing
evidence, a failed comparison or an omitted reference fails the report step. The Testify fixture
requires every Testify policy case, timing and count comparison to pass. Reports
record their source revision and the inputs that were actually tested.

The normal and deferred `testing` matrices share one covered set of SDK, Mini
and Orchestrion binaries. Testify shares its own race/coverage set. Both sets
keep their source directories through the test process, including `-count`
repetitions, and `TestMain` removes them afterward. Selecting only a deferred
test still builds the required set. A failed build also fails later consumers.
This reuse ends with the process; it adds no persistent build cache.
On Windows, cleanup retries sharing violations and access-denied errors for up
to two seconds, as `testing.TempDir` does. Removing the URL file requests
Orchestrion shutdown; its log handles may close slightly later. A persistent
cleanup error still fails the harness.

Each scenario starts a new child process with its own receiver and retry
state. Each report retains the cases, comparisons and execution order.
Builds that deliberately alter flags, sources, overlays, workspaces or library
versions keep their independent fixtures. Do not add those variants to the
shared sets without checking their inputs and cleanup ownership.

The [recorded repeated comparison](results/20261009-linux-go1.27.1/parity/README.md)
ran 115 scenarios at its frozen source revision. The current schema-4 gate
requires the 113 ordinary/deferred/Testify cases plus Fuzz/Examples and
supplemental evidence. The per-round `harness.json`
records total harness time, including fixture compilation and comparison;
that clock does not measure either backend's runtime or GitHub CI duration.

### Per-case duration records

The [recorded Linux Go 1.27.1 run](results/20261009-linux-go1.27.1/parity/README.md)
includes six rounds at each of 4/32 CPUs. Its per-round JSON files retain
scenario durations, event counts and supplemental fixtures; `manifest.json`
records the execution order, reference versions and harness input hashes.
The summary keeps medians and ranges without imposing a speed threshold.

The current JSON report uses schema 4, which requires the complete Fuzz/Examples
and coverage evidence as well as the ordinary testing matrix. Each scenario and additional fixture
records `timing.sdk_wall_ns` and `timing.mini_wall_ns` as integer monotonic-clock
nanoseconds. Markdown shows seconds and the signed change `(Mini / SDK - 1) * 100`.
Missing or invalid timing observations fail report validation. `execution_block`
and `execution_order` record continuous blocks separately from per-child clocks.

The matrix times the prebuilt child binary from process start to exit. This
includes initialization, settings requests, tests/retries and shutdown/flush.
It excludes compilation, fixture setup and the comparator. The multiple-package
fixture times the CLI and includes preparation and compilation; its JSON scope
and separate table identify that difference. The manual fixture deliberately
uses a 100 ms settings delay. Testify times the full Orchestrion reference against
Mini for every Testify combination; the POC SDK pass/skip time is also kept in
that JSON.

Each invocation records one observation per variant. The default matrix runs SDK first
and Mini second; Testify cases run the reference SDK, then Mini, with a separate
POC SDK check for pass/skip.
These are diagnostic durations from compatibility tests, with no speed threshold.
A performance claim needs repeated, balanced runs with the same inputs and
enough context to separate host load, compilation and SDK work. The
[compile-only cold/incremental benchmarks](benchmarks.md) measure another contract.

### Repeated whole-matrix timing

The [recorded Linux comparison](results/20261009-linux-go1.27.1/parity/README.md)
keeps every input report, the run manifest and separate continuous clocks for
the 65-case testing and 17-case deferred SDK/Mini blocks. It has six measured
rounds at each CPU count, three in each execution order. The 26 Testify and seven
deferred Testify cases retain paired SDK-first order and race-runtime shutdown
delays. All supplemental fixtures run each round and have separate clocks.
The sum of the 115 individual case durations is not a continuous wall clock.

Set `PARITY_EXECUTION_ORDER=sdk-first` or `mini-first` to use grouped execution.
Each variant runs all matrix cases before their differential comparisons. The
block timer includes receiver setup, child-process execution and shutdown/flush;
it excludes compilation and comparison. This is a measured interval, not the
sum of the individual child clocks. Schema 4 exports `execution_block` and
`execution_order`. The normal job uses SDK first; Linux race validation uses
Mini first so both paths are exercised by CI. Both commands use `-count=1` to
regenerate their evidence instead of reusing a Go test-result cache entry.

For repeated timing, run an unmeasured warmup, then six fresh invocations with
three in each order. Use the same Go version, build cache, flags, fixture and
GOMAXPROCS for every round. Keep output prefixes distinct:

```sh
# ORCHESTRION_BIN and the pinned SDK must be configured as above.
PARITY_EXECUTION_ORDER=sdk-first PARITY_REPORT_PATH="$PWD/artifacts/warmup.json" \
  go test -count=1 -run '^TestCIVisibility' ./integration
for round in 1 2 3 4 5 6; do
  order=sdk-first
  if [ $((round % 2)) -eq 0 ]; then order=mini-first; fi
  PARITY_EXECUTION_ORDER="$order" \
    PARITY_REPORT_PATH="$PWD/artifacts/round-$round.json" \
    go test -count=1 -run '^TestCIVisibility' ./integration || exit 1
done
python scripts/parity_series.py artifacts/round-*.json \
  --output artifacts/series.md --json-output artifacts/series.json
```

The summary checks all matrix and supplemental evidence, identical references
and scenario contracts, distinct report paths and balanced execution orders.
It shows median, mean, range, coefficient of variation (standard deviation over
mean), every block observation and the paired percentage differences. The local
manifest also preserves the elapsed time of each complete SDK-and-Mini harness
invocation, including compilation and comparison; that combined clock cannot
be interpreted as either variant's runtime. The seven supplemental fixtures
retain their existing SDK-first order, except the separate Testify reference.

### Completing CI telemetry parity

The incorporated CI distribution helpers match all 24 definitions in the pinned
SDK except their import path. Mini's native writer also calls the test-cycle
size, event-count, serialization-time and request-time helpers. The current wire
fixture reads only count/rate metrics; it does not prove distribution delivery
or every policy/error interaction. Collection concurrency already has unit/race
checks, which do not replace that differential wire evidence.

| Work | Required evidence |
| --- | --- |
| Metric inventory | Map each CI metric to its kind, tags, unit and emission site in the pinned SDK and Mini. Preserve count versus rate and distribution schema fields; compare only the `civisibility` namespace. |
| Distribution capture | Decode `distributions` inside telemetry message batches and save the actual samples for each variant. Assert required metric/tag sets and finite, nonnegative values, including valid zero millisecond measurements. |
| Semantic comparisons | Compare deterministic samples such as returned test/file counts against fixture responses. For payload bytes, event counts and request latency, validate each sender against its own captured payloads and attempts. Batching and retry serialization differ, so latency or payload-size samples must not be required to be identical. |
| Policy interactions | Enable telemetry on selected existing ATR/EFD/ITR, test-management, coverage/report, impacted-test and Git-upload combinations, including both retry modes and parallel execution. Route Agent and Agentless telemetry to local capture endpoints; keep real credentials out of fixtures. |
| Delivery failures | Exercise successful retry, exhausted retry, permanent HTTP failure, network error, cancellation and oversized rejection. Distinguish one logical batch from its HTTP attempts and one discarded batch from its events. |
| Aggregation and shutdown | Replay deterministic metric samples, submit concurrently with collection, and verify final flush sends each accepted sample once. Add missing differential proof alongside the existing collector race tests. |
| Offline output | Decode Bazel telemetry files with the same assertions and prove telemetry-disabled runs emit none. This covers the file contract; an actual Bazel invocation remains a separate gate. |
| Regression gate | Export metric inventories/samples and outcomes per case. Fail on missing Mini metrics or changed CI kinds/tags; run on the existing Linux/macOS/Windows matrix and Linux race harness. |

The payload-drop unit matches the SDK. One known production
difference remains before a complete telemetry-parity claim:

- SDK `ciVisibilityTransport.send` returns network failures before incrementing
  `endpoint_payload.requests_errors`; Mini records them with `error_type:network`.
  The fixture must expose this difference rather than discard it as an APM metric.

SDK and Mini increment `endpoint_payload.dropped` once per abandoned batch.
Mini keeps a failed `Flush` batch for recovery while open; final `Close` failure
abandons it. HTTP attempts and the number of events do not multiply that count.
Rejected or oversized individual events increment `DroppedEvents()` only. Tests
cover retry exhaustion, later recovery, buffered and in-flight cancellation,
concurrent repeated close/flush, post-close rejection and oversized rejection.
This verifies the Mini contract and unit; the full differential failure-policy
cross-product remains open.

Both implementations increment `endpoint_payload.requests` once per logical
send, outside their retry loops. The current fixture can compare it with HTTP
requests because there are no transport retries in that fixture. A retry fixture
must check logical batches and attempts separately. The SDK also serializes and
emits payload-size/event-count samples on each attempt; Mini encodes a batch once
and reuses it. Validate those samples against the actual serialization/send
operations without removing that optimization.

This work concerns CI telemetry only. APM sampling, heartbeats, security and
remote configuration remain outside its scope. Implementation differences and
missing proof remain separate entries until their contracts and tests are resolved.

When updating the SDK, follow [maintenance](maintenance.md#updating-the-sdk-base).
Review the upstream testing YAML and CI production/test inventory as well as the
source diff. Add fixtures for new settings, tags and precedence rules, then run
this matrix against the same exact upstream version. Keep APM exclusions specific;
a broad prefix filter could hide a new CI attribute. Do not normalize a new
mismatch before determining whether it is a bug or an intentional contract.

## Boundaries that remain open

Real Datadog intake acceptance and UI grouping have not been tested. Actual Bazel
execution and long fuzz campaigns need their own environment and proof. CI runs
native Linux, macOS and Windows; other cross-linked targets still have build
evidence only. The current suite samples feature interactions rather than
enumerating their unbounded flags, environments and failure timings.

[Testify support](testify.md) adds suite registration in the instrumentator for
both runtimes, including callers in external modules. Method-level ITR retains
the pinned SDK limitation.
