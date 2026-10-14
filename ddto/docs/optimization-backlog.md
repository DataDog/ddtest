# Optimization backlog

Each item is a separate experiment and change. Validate an item's figures on
the current revision before implementing it. Use
[performance and profiling](performance.md) for measurement and ownership
rules, and [feature parity](ci-parity.md) for the behavior to preserve.

Items 1–10 come from earlier reviews. Items 11–53 come from a review of
`063c50a` on 2026-10-10, mostly measured on darwin/arm64 (Apple M5 Pro,
Go 1.27.2) on a loaded host; their line numbers refer to `b776593`. Items 54–58
come from a review of `2d0059a` on 2026-10-11, measured on the
[benchmark](benchmarks.md) host: Linux/amd64, AMD Ryzen 9 5950X with 32
threads, Go 1.27.0, against a local fake Agent, with medians of alternating
runs. Their line numbers refer to `2d0059a`.

Item numbers do not change. A missing number is either a completed item,
whose pull request records its results, such as
[#25](https://github.com/tonyredondo/dd-ci-testing-poc/pull/25),
[#29](https://github.com/tonyredondo/dd-ci-testing-poc/pull/29),
[#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31),
[#32](https://github.com/tonyredondo/dd-ci-testing-poc/pull/32),
[#34](https://github.com/tonyredondo/dd-ci-testing-poc/pull/34),
[#38](https://github.com/tonyredondo/dd-ci-testing-poc/pull/38) and
[#42](https://github.com/tonyredondo/dd-ci-testing-poc/pull/42), or an item
listed under [Evaluated and dropped](#evaluated-and-dropped).

In the items, `civisibility/`, `hostname/`, `osinfo/` and `telemetry/` paths
are relative to `internal/thirdparty/dd-trace-go/`, and `gotesting/` stands for
`civisibility/integrations/gotesting/`. Items that change incorporated SDK code
also need an entry in
[`ADAPTATIONS.md`](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md).

## Where the time goes

The backlog was evaluated on 2026-10-12 at `b7d8275`, on the benchmark host,
with Gin v1.12.0: six test binaries and about 700 tests and subtests.
`ddto test` adds about 0.1–0.2 s to a run without network latency, and its
remaining costs come in three sizes.

Round trips cost the most. With 100 ms per request, `-count=1` took 1.16 s
against 0.67 s natively. The worker's requests ahead overlap the build
([#34](https://github.com/tonyredondo/dd-ci-testing-poc/pull/34)), but each
test process still ends with two serial round trips, and ddto with the worker's
`app-closing`.

Preparation comes next. Every invocation spends about 95 ms before `go test`
starts, and more in larger modules: a run whose results all came from Go's
test cache took 184 ms against 78 ms natively.

Per-test and per-event work is the smallest. Mini adds about 35 µs per test;
item 58 measured its `t.Helper()` at 0.7 µs, 2% of that cost, with 2,000 empty
tests at `2d0059a`. The dropped micro-optimizations save at most about a
microsecond per test or event each, about 3% of that estimate, and a few
milliseconds of CPU for Gin's 700 tests, spread across six binaries. A test
emits one test event; tests that create many SDK spans emit more, which is
where items 22 and 24 would count. Revisit them with a profile of such a
suite, or of one large enough for Mini's per-test cost to dominate.

## Summary

### Next

Ordered by the expected effect on a `ddto test` run's wall time, then by cost.

| Order | Change | Where it can help | Complexity |
| --- | --- | --- | --- |
| 15 | Overlap the Orchestrion tool lookup with preparation | 0.5–2 s in Orchestrion modes of large modules | Low to medium |
| 39 | Parse selected sources concurrently at startup | Parsing the files of every known test with EFD and impacted tests | Medium |
| 51 | Store known tests per module in the read cache | 10–25 ms of CPU and megabytes per binary with 100,000 tests | Medium to high |
| 32 | Scope retry subtest-name cleanup to the attempt | Up to 9 ms per retry group with 100,000 subtests, blocking `T.Run` | Medium to high |
| 28 | Skip the final retries after a failed session-close flush | About 0.3 s at exit with a refused connection | Low |
| 49 | Stream read-cache expiry fields | 5–15 ms per cache miss when a test process requests itself | Low |
| 50 | Keep idle connections during deferred startup | One to four TLS handshakes at startup in deferred mode | Low to medium |
| 3 | Keep per-test state in `testing`'s structures | About 1 µs per test; mainly removes offset and reflection code | Medium to high |

### Parked

| Item | Change | Decision |
| --- | --- | --- |
| 10 | Remove a round trip from each test process's exit; about 100 ms of a run with 100 ms requests | Not now (2026-10-12) |
| 55 | Test source locations from the CLI; extends 39 | Parked on 2026-10-11: it needs a cache location |
| 56 | Reuse the preparation plan between unchanged invocations; about 90 ms of a cached Gin run's 184 ms | Not yet (2026-10-12); kept parked on 2026-10-14 after [phase 5](optimization-plan.md#phase-5-preparation-per-invocation) |
| 57 | Deliver events through the CLI; extends 10 | Not now (2026-10-11) |

### Evaluated and dropped

Dropped on 2026-10-12, with the figures that decided it. Their analyses remain
in this file's history.

| Item | Change | Why it was dropped |
| --- | --- | --- |
| 1 | Represent span options as values | Allocation shape per event, well under a microsecond |
| 2 | Cache common numeric CI options | Option construction per span, well under a microsecond |
| 4 | Reduce remaining provisioning commands | Two `go mod` commands take 6 ms in Gin; the temporary workspace that runs more is uncommon |
| 7 | Store small metric sets without a map | Allocations per metric-bearing event, well under a microsecond |
| 16 | Write and remove generated files concurrently | Gin's seven generated files take well under a millisecond; 600 packages took about 60 ms on macOS |
| 17 | Start the package query alongside `go env` | `go env` takes 3 ms on Linux |
| 18 | Parse fewer SDK sources, in parallel | 5–7 ms per invocation, only when the SDK's CI packages are reachable |
| 19 | Walk the vendor tree once | Tens of milliseconds, only with about 20,000 vendored files |
| 21 | Skip the common-metadata override scan | 0.25–0.6 µs per event at flush |
| 22 | Project common tags without cloning `Meta` | 5 µs per projected event, only SDK spans and mixed batches |
| 24 | Store the event inside `Span` | One 208-byte allocation per event |
| 25 | Keep gzip compressors across garbage collections | 80 µs per Agentless batch |
| 26 | Remove the remaining producer-side costs | Tens of nanoseconds per event |
| 27 | Encode straight into the payload buffer | About 20 ns per event |
| 29 | Reuse deferred-mode connections | A handshake per extra batch, only beyond eight batches at a deferred checkpoint |
| 35 | Stop rewriting the instrumentation map on every `T.Run` | 3–4% of a parallel synthetic run of empty subtests |
| 36 | Return the hook lease without allocating | 13–65 ns per hook |
| 37 | Look up modules and suites before building options | 100–200 ns per test |
| 38 | Match test identity and management data by slicing | About 0.2 µs per subtest |
| 40 | Resolve module and suite counters once | No wall-time change measured |
| 41 | Stop the Testify parent walk when no suite is active | Not measured; read locks per `T.Run`, only after a suite ran |
| 46 | Overlap telemetry round trips | Merged into item 10; its startup part only affects binaries without the worker's state: run alone, or under a `-exec` launcher |
| 58 | Drop the per-test `t.Helper()` in the wrapper | 0.7 µs per test, and dropping it can move the lines that `testing` reports |

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

## 10. A round trip at each test process's exit

Under `ddto test`, ddto's Git worker owns one telemetry application per
invocation ([#33](https://github.com/tonyredondo/dd-ci-testing-poc/pull/33)).
Test processes that use its state send no `app-started`, and at exit they send
their test cycle and then one telemetry `message-batch`, one after the other. On
Gin at `b7d8275`, with a fake Agent that answers each request after 100 ms,
`ddto test -count=1 ./...` took 1.16 s, the median of 10 runs. In a run with
`DD_TRACE_DEBUG`, each test process's exit took 200 ms: the test-cycle send,
then the telemetry request. ddto then waited about 100 ms after `go test` for
the worker's `app-closing`. The last 300 ms of the run were these round trips.

Hand the test process's telemetry to the worker, which sends it with
`app-closing`: test processes then send no telemetry request, and each one's
exit loses a round trip, about 100 ms of such a run. The test process writes
its telemetry after its test cycle ends, so the data keeps the test cycle's
own metrics: `EndpointPayloadRequestsMs` and `EndpointPayloadRequestsErrors`,
which `internal/citransport/transport.go` records after the request returns.
Sending the telemetry concurrently with the session-close flush would lose
them. Keep `app-started` in its own request under its mapper contract, and keep
the payload order. Startup and
shutdown must respect deferred admission and goleak checkpoints, including
errors and cancellation. Use decoded telemetry and actual HTTP captures to check
values and logical payload/attempt counts. Network waits need delayed-receiver
experiments; CPU microbenchmarks cannot establish that saving.

Binaries that report their own application, run alone or without a handoff,
keep two more costs (formerly item 46). When the read cache serves their
settings, they wait for `app-started`
(`civisibility/utils/net/client.go:226-255`), and at exit they make two
`message-batch` sends after the test cycle. Starting telemetry before the Git
phase of `createCITagsMap`, when the service name does not depend on Git, and
combining compatible messages at exit would address them. Those binaries run
alone or, under `ddto test`, with a `-exec` launcher, which keeps ddto from
handing them the worker's state.

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

## 28. Final retries after a failed session close

[#38](https://github.com/tonyredondo/dd-ci-testing-poc/pull/38) made the final
drain parallel in ordinary mode too. What remains: with a refused connection,
the session-close flush and the final delivery at exit each spend a retry cycle
of about 0.3 s on the same payload. Once the session-close flush has failed
with a network error, the final delivery could skip its retries; a black-holed
intake would then also wait for one flush timeout instead of two.

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

Under `ddto test`, the worker usually makes these misses while `go test`
builds, off the critical path. They stay on it when the worker waits for a
test process (`-o` or `-a`), when the settings require Git, and in binaries
without the worker's state: run alone, or under a `-exec` launcher.

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

Under `ddto test`, the read cache serves the startup requests that the worker
made ahead. Deferred-mode binaries still dial again when the settings require
Git, whose requests the worker does not make ahead, when the worker waits for a
test process, and without the worker's state: run alone, or under a `-exec`
launcher.

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
2.5 ms. [#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31)
removed the build-information read of every distinct test, about 2 µs per
test. The rest costs about 1–1.5 µs per test, much of it
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

At `b7d8275`, `ddto test` of Gin's five packages whose results all came from
Go's test cache took 184 ms against 78 ms natively, medians of 10 runs. In a
run with `DD_TRACE_DEBUG`, which took 197 ms, preparation took 93 ms: the
package query 47 ms, the library query 28 ms, the runtime probe 5 ms, two
`go mod` commands 6 ms, `go env` 3 ms and the `testing` transformation 3 ms.
`go test` itself took 102 ms.

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
queries and compares their results. Item 4, since dropped, ruled out a persistent
cache for provisioning commands; this cache needs its own decision.

On 2026-10-14, [phase 5](optimization-plan.md#phase-5-preparation-per-invocation)
of the optimization plan measured Gin at 165 ms against 80 ms natively, with
Mini absent from `go.mod` and 79 ms of preparation. The cache would save
60–80 ms only in repeated local runs: CI jobs start without it. The item stays
parked.

## 57. Events through the CLI

This extends [#34](https://github.com/tonyredondo/dd-ci-testing-poc/pull/34) and
item 10. Every test binary links Mini's HTTP and TLS delivery. A one-test binary
grows from 4.8 to 16.7 MB, and its link from 106 to 289 ms, medians of 12. A
probe binary that references only `http.DefaultClient.Do` and `tls.Client` links
in 210 ms and is 5.6 MB larger, so the HTTP and TLS stack accounts for about 104
ms of the 183 ms Mini adds per link. `encoding/json`, `go/parser`, and
`compress/gzip` with `log/slog` add 7–12 ms each. These figures come from an
empty fixture and a standalone probe: a binary whose own code already uses HTTP
or TLS, such as tests with `httptest.NewTLSServer`, keeps that stack, so measure
the marginal saving on representative binaries. Every edit rebuild and every
cold build links each affected test binary again: in a cold Gin build, the six
links took 2.24 s of CPU, against 1.78 s natively. The larger binary also starts
about 0.9 ms slower.

For `ddto test` runs, test binaries could hand encoded batches to the CLI
through an inherited pipe or a local socket. The CLI would deliver them over
HTTP with one connection pool, batching across packages. The binary drops
`net/http` and `crypto/tls` only when settings, known tests and telemetry also
come from the CLI with no request of its own as a fallback;
[#34](https://github.com/tonyredondo/dd-ci-testing-poc/pull/34) and item 10
still keep that fallback, so this is the transport they need.
[#32](https://github.com/tonyredondo/dd-ci-testing-poc/pull/32) already gave
the CLI the SDK's HTTP client, in its Git worker. Builds with `-c` and
standalone binaries keep today's delivery through a different generated
import. The CLI then owns retries and
the final flush, and must keep each binary's event order, back-pressure,
deferred admission and goleak checkpoints, and not lose events when it is
interrupted.

## Checked without a change

The 2026-10-10 review also checked these costs and found nothing worth changing:

- Package initialization on the tool path takes about 0.2 ms.
- Running a tool through `tool-overlay` costs about 3.5 ms more than
  `/usr/bin/true` on macOS, about 0.7 s of CPU per 240 tool calls in an edit
  build. That cost comes with `-toolexec`, and overlays cannot replace
  module-cache sources. Since
  [#43](https://github.com/tonyredondo/dd-ci-testing-poc/pull/43), go runs a
  small wrapper executable instead. On Linux it starts in 1.2 ms against
  ddto's 2.4 ms.
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
  build information. Items 1, 7, 22–24, 27 and 38 covered the others and were
  dropped as too small;
  [#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31) removed the
  build-information reads.

Implementing items 53 and 54 in
[#31](https://github.com/tonyredondo/dd-ci-testing-poc/pull/31) also checked
these:

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

Phases 4–6 of the [optimization plan](optimization-plan.md) remeasured these
costs on 2026-10-14 and changed nothing. In
[Mini's package graph](optimization-plan.md#phase-4-minis-package-graph-in-builds),
the HTTPS transport took 10.2 of the 16.3 s of CPU that Mini adds to a cold
build, and building in another order did not help, because builds are
CPU-bound. In
[per-invocation preparation](optimization-plan.md#phase-5-preparation-per-invocation),
two sequential `go list` queries took most of the 60–79 ms. Under `ddto test`,
[a test process](optimization-plan.md#phase-6-test-binary-startup-and-memory)
bootstrapped in 0.56 ms, and Gin's root binary used 3.4 MiB more, mostly span
batches bounded at 1,000 events.

## Experiment record

Keep the baseline and candidate SHAs, inputs, commands, raw repetitions and
limits in a task-scoped artifact directory. Alternate execution order and
retain slow or failed samples. Record CPU, allocations, complete wall time,
wire bytes and peak memory when they apply. A hot function's profile share
does not predict the percentage that a change will save.

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
