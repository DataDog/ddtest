# Optimization backlog

These are the changes that could still make the POC faster or lighter: Mini,
the test runtime that `ddto` links into test binaries, and `ddto` itself.
Orchestrion is out of scope. Each item is its own experiment and pull request.
Check an item's figures on the current revision before implementing it,
measure as [performance and profiling](performance.md) describes, and keep the
behavior that [feature parity](ci-parity.md) defines.

Item numbers are stable, because the [optimization plan](optimization-plan.md)
and pull requests cite them. A missing number was completed or dropped. The
[version of this file before the 2026-10-15 rewrite](https://github.com/tonyredondo/dd-ci-testing-poc/blob/513beea/docs/optimization-backlog.md)
keeps those analyses, and the costs that earlier reviews checked and left
unchanged.

In the items, `civisibility/` and `telemetry/` paths are relative to
`internal/thirdparty/dd-trace-go/`, and `gotesting/` stands for
`civisibility/integrations/gotesting/`. A change to incorporated SDK code also
needs an entry in
[`ADAPTATIONS.md`](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md).

## Where the time goes

Measured on 2026-10-15 at `513beea`, on the benchmark host: Linux/amd64, AMD
Ryzen 9 5950X with 32 threads, Go 1.27.0. The project is Gin v1.12.0, six test
binaries with about 700 tests and subtests, against a local fake Agent. Figures
are medians of runs that alternate native and `ddto`; CPU is the user and
system time of the whole process tree.

| Run of Gin | Native | `ddto test` | Added | Items |
| --- | --- | --- | --- | --- |
| `./...` with `-count=1`, the Agent answers at once (7 runs) | 0.72 s, 2.7 CPU-s | 0.93 s, 4.0 CPU-s | +0.20 s, +1.2 CPU-s | 56 |
| `./...` with `-count=1`, 100 ms per Agent request (5 runs) | 0.71 s | 1.23 s | +0.52 s | 10 |
| `./...` with `-count=1`, no Agent listening (5 runs) | 0.71 s | 2.34 s | +1.63 s | 59, 28 |
| Five packages, every result from Go's test cache (9 runs) | 0.086 s, 0.48 CPU-s | 0.200 s, 0.98 CPU-s | +0.11 s | 56 |
| `./...` with an empty build cache (3 runs) | 11.0 s, 112 CPU-s | 11.7 s, 125 CPU-s | +0.7 s, +12.8 CPU-s | 57, in projects without HTTP |

Binaries with 1 and 2,000 empty tests, run directly 15 times each, give the
cost per test and per binary. Mini adds 15.4 µs per test: 2,000 tests took
52.7 ms against 13.3 ms natively. A binary run outside `ddto test` also pays
8.5 ms once, 10.3 against 1.8 ms, including its own startup requests to the
Agent. In Gin, 700 tests cost about 11 ms in total, spread over six binaries.
Pull requests #69 to #73 then halved the cost per test. On 2026-10-17, in
`scripts/minitracer_benchmark.py`'s binary of 10,000 empty tests, Mini added
7.24 µs of CPU and 3.96 µs of wall time per test, against 14.05 and 7.71 µs
before them
([data](results/minitracer-cumulative-20261017-linux-go1.27/README.md)).

With an Agent that answers at once, preparation, about 0.11 s, is most of what
`ddto` adds to a warm run. Request latency then adds about 0.3 s per run,
through round trips at each test process's exit and the worker's last request.
Without an Agent, retries cost more than everything else together. Cold builds
pay for compiling Mini's packages and, in projects that do not use HTTP
themselves, its HTTPS stack.

## Summary

Ordered by the expected effect on a run's wall time, then by cost.

| Item | Change | Expected saving | Complexity | Status |
| --- | --- | --- | --- | --- |
| 59 | Tell test processes at startup that the backend is unreachable | About 1 s of the 1.6 s that each test process pays without an Agent | Medium | Open |
| 28 | Skip the final retries after a failed session-close flush | 0.3 s at each test process's exit when delivery fails | Low | Needs the user's go-ahead |
| 10 | Send each test process's exit telemetry through the worker | About one request latency per run: 0.1 s of the 0.3 s that 100 ms of latency adds | Medium | Parked (2026-10-12) |
| 57 | Deliver events through the CLI | In a project without HTTP, up to about 10 of the 16 CPU-s that Mini adds to a cold build, and about 0.1 s and 5.6 MB per test binary link, by a probe; little in projects that use HTTP, such as Gin. Unmeasured for the change itself | High | Parked (2026-10-11) |
| 56 | Reuse the preparation plan between unchanged invocations | 60–80 ms of the 0.11 s in repeated local runs; nothing in a fresh CI job | Medium to high | Parked (2026-10-14) |
| 60 | Check the runtime's hook sources once | Up to the checks' 23 ms per invocation, of 0.211 s for a cached Gin build. Unmeasured for the change itself | Low to medium | Open |
| 39 | Parse the sources of selected tests concurrently at startup | 1–4.5 ms per test file with Early Flake Detection and impacted tests | Medium | Open |
| 51 | Store known tests per module in the read cache | 10–25 ms of CPU and several MB of heap per binary with 100,000 known tests | Medium to high | Open |
| 32 | Scope retry subtest-name cleanup to the attempt | Up to 9 ms per retry group with 100,000 subtests, during which `T.Run` blocks | Medium to high | Open |
| 49 | Stream read-cache expiry fields | 5–15 ms per cache miss with large entries | Low | Open |
| 50 | Keep idle connections during deferred startup | One to four TLS handshakes of 50–150 ms each, in Agentless deferred mode | Low to medium | Needs the user's go-ahead |
| 3 | Keep per-test state in `testing`'s structures | About 1 µs of the 7 µs of CPU per test, and the offset and reflection code | Medium to high | Open |

## 59. Test processes retry an unreachable backend

On 2026-10-15, Gin's `ddto test -count=1 ./...` with `DD_TRACE_AGENT_URL`
pointing at a closed port took 2.34 s, against 0.93 s with the fake Agent and
0.71 s natively, medians of 5 runs. A `DD_TRACE_DEBUG` run of `./render/...`
shows where each test process waits:

- 0.98 s for settings: 0.28 s waiting for the worker's request through the
  read cache, which failed, so the waiter falls back to its own request
  (`civisibility/utils/net/read_cache.go:499`); then 0.70 s for that request,
  four attempts refused at once with 0.1, 0.2 and 0.4 s of backoff between
  them.
- 0.30 s for the session-close flush, three attempts, and 0.30 s more for the
  final delivery of the same payload at exit, which item 28 covers.

ddto's Git worker has already found the backend unreachable by the time test
processes ask for settings. Record that result for the run, for example as a
short-lived entry that read-cache waiters accept. Test processes then give up
their startup requests at once and run with the defaults they reach today after
their retries. A process run alone keeps today's retries. Expect about 1 s per
test process, which is what a developer running `ddto test` without an Agent
pays, and what a misconfigured CI job pays in every package. The same signal
could also let delivery skip its retries, the other 0.6 s, but that changes
delivery like item 28, which waits for the user's go-ahead.

An Agent that is still starting refuses connections just as an absent one
does, so the shortcut also gives up on it. A test process that accepts the
entry keeps the default settings for its whole life, even if the Agent comes up
while its tests run; its delivery keeps its retries and reaches the Agent then,
and processes that start after the entry expires request settings again. Keep
the entry short-lived, on the order of the worker's own retry cycle, and limit
it to connection errors that the worker saw in this run, so that timeouts and
5xx responses keep their retries. The run must still finish
with the same test results and exit code, and the same telemetry about the
failure. Check that this trade-off is acceptable before implementing it.

## 28. Final retries after a failed session close

[#38](https://github.com/tonyredondo/dd-ci-testing-poc/pull/38) made the final
drain parallel. What remains: when delivery fails, the session-close flush and
the final delivery at exit each spend a retry cycle on the same payload, 0.30 s
each on 2026-10-15 with a refused connection (item 59). Once the session-close
flush has failed with a network error, the final delivery could skip its
retries; a black-holed intake would then also wait for one flush timeout
instead of two. The user has not approved this change yet.

## 10. A round trip at each test process's exit

Under `ddto test`, ddto's Git worker owns one telemetry application per
invocation ([#33](https://github.com/tonyredondo/dd-ci-testing-poc/pull/33)).
Test processes that use its state send no `app-started`, and at exit they send
their test cycle and then one telemetry `message-batch`, one after the other.

On 2026-10-15, with a fake Agent that answers each request after 100 ms,
Gin's `-count=1` run took 1.23 s, against 0.93 s with immediate answers and
0.71 s natively. On 2026-10-12, a `DD_TRACE_DEBUG` run showed where the latency
goes: each test process's exit took 200 ms, the test-cycle send and then the
telemetry request, and ddto waited about 100 ms after `go test` for the
worker's `app-closing`.

Hand the test process's telemetry to the worker, which sends it with
`app-closing`. Test processes then send no telemetry request, and each one's
exit loses a round trip, about 100 ms of such a run. The test process writes
its telemetry after its test cycle ends, so the data keeps the test cycle's own
metrics, `EndpointPayloadRequestsMs` and `EndpointPayloadRequestsErrors`;
sending telemetry concurrently with the session-close flush would lose them.
Keep `app-started` in its own request, and the payload order. Startup and
shutdown must respect deferred admission and goleak checkpoints, including
errors and cancellation. Check decoded telemetry and HTTP captures, and measure
the saving with a delayed receiver: a CPU benchmark cannot show it.

Binaries that report their own application, run alone or under a `-exec`
launcher, keep two more costs. When the read cache serves their settings, they
wait for `app-started` (`civisibility/utils/net/client.go`), and at exit they
make two `message-batch` sends after the test cycle. Starting telemetry before
the Git phase of `createCITagsMap`, when the service name does not depend on
Git, and combining compatible messages at exit would address them.

## 57. Events through the CLI

Every test binary links Mini's HTTP and TLS delivery. Phase 4 of the
optimization plan ([#44](https://github.com/tonyredondo/dd-ci-testing-poc/pull/44))
built a module with one empty test from an empty build cache: Mini added
16.3 CPU-s, and its HTTPS transport, the `crypto`, `net` and `net/http`
packages with cgo, accounted for 10.2 of them. On 2026-10-11, a one-test binary
grew from 4.8 to 16.7 MB and its link from 106 to 289 ms. A probe binary that
references only `http.DefaultClient.Do` and `tls.Client` linked in 210 ms and
was 5.6 MB larger, so the HTTP and TLS stack accounted for about 104 ms of the
183 ms that Mini adds to each link. Every edit rebuild links each affected test
binary again. These figures bound what the change could save; it has not been
measured.

Projects whose own tests use HTTP or TLS keep that stack. Gin's tests import
`net/http` and `crypto/tls`, so the +12.8 CPU-s of its cold build on
2026-10-15 come mostly from elsewhere, such as Mini's own packages, which took
3.8 CPU-s in phase 4; measure the marginal saving on representative projects.

For `ddto test` runs, test binaries could hand encoded batches to the CLI
through an inherited pipe or a local socket. The CLI would deliver them over
HTTP with one connection pool, batching across packages. A binary drops
`net/http` and `crypto/tls` only when settings, known tests and telemetry also
come from the CLI, with no request of its own as a fallback; item 10 and the
read cache still keep that fallback, so this is the transport they need. Builds
with `-c` and binaries run alone keep today's delivery through a different
generated import. The CLI then owns retries and the final flush, and must keep
each binary's event order, back-pressure, deferred admission and goleak
checkpoints, and not lose events when it is interrupted.

## 56. Preparation plan reused between invocations

On 2026-10-15, `ddto test` of five Gin packages whose results all came from
Go's test cache took 0.200 s and 0.98 CPU-s, against 0.086 s and 0.48 CPU-s
natively.
[Phase 5](optimization-plan.md#phase-5-preparation-per-invocation) measured the
preparation on 2026-10-14: 79 ms in Gin, mostly two `go list` queries in
sequence, the package query 39 ms and the library query 21 ms, then the
runtime probe and provisioning 12 ms.

Cache the plan in the user cache: overlay entries, generated files, tool
selection and library decisions. Key it on everything the queries read: the Go
environment and version, `go.mod`, `go.sum`, `go.work` and any `-modfile`, the
flags and build tags, the user's overlay, and the file names, build constraints
and imports of every Go file in the main modules and local replacements,
`ddto`'s version, and the contents of every source that preparation rewrites
or inspects, such as `testing`, Testify's `suite` package and goleak. An input
missing from the key would reuse a wrong plan, so validate the key with a debug
mode that also runs the queries and compares their results. The cache saves
60–80 ms only in repeated local runs, because CI jobs start without it. It
stays parked by the user's decision of 2026-10-14.

## 60. Hook checks on every invocation

On 2026-10-17, the benchmark refresh measured Mini's cached builds 19% slower
than on 2026-10-14 (median of 48 cells). Timing a cached `ddto test -c` of Gin
at 4 CPUs with the `ddto` of each merge since then, 15 runs each, gives
0.181 s at `97ec6e3` and 0.211 s at `bfcf898`:
- #60 (forced exits) added 8 ms;
- #64 (crash data loss) 16 ms;
- #66 (file coverage) 5 ms.

`ddto`'s debug timings at `bfcf898` show where most of it goes. Before it
rewrites `testing`, `timeoutHookRefusal` (`internal/runner/forced_exit.go`)
reads and checks the sources of three packages: `internal/runtime/exithook`,
Mini's `integrations` package and Mini's `gotesting` package. That takes
17 ms. `exitHookRefusal`, which checks the first two, runs again for the exit
hook, and the signal hook checks its own package: together about 6.5 ms more.

The checks cost about 23 ms of every invocation, including a fresh CI job.
How much of it a change saves is unmeasured:
- checking each package once per invocation removes only the repeated checks,
  part of the 6.5 ms;
- caching the results across invocations, keyed on the contents of the files
  that a check reads, also saves the first check in repeated local runs, but
  not in a fresh CI job, which starts without the cache;
- checking only the declarations that each hook needs could save most of it
  on every run, if it keeps the same refusals.

## 39. Source parsing at startup

Parsed sources are cached; a cached lookup takes 17 ns
(`civisibility/integrations/manual_api_sourcecache.go`). With Early Flake
Detection and impacted tests, however, `IsTestFuncModified`
(`gotesting/instrumentation.go:710`) parses the file of every known test
during `M.Run` setup, including tests excluded by `-run`, which CI test
splitting relies on. Otherwise the first test of each file pays for its parse:
1.08 ms for 2,300 lines and 4.5 ms for 8,300, measured on 2026-10-10. In Gin's
root package, 21 test files and 10,700 lines, the parse took about 7.5 ms of
CPU per run on 2026-10-11.

Prefetch the files of selected tests with a bounded worker pool, or restrict
the modified-test check to selected tests. Keep the source-root, CODEOWNERS and
source-range tags identical, including for generics, `//line` directives and
build-tagged files.

## 51. Known tests per module

The known-tests request covers the whole repository
(`civisibility/utils/net/known_tests_api.go:73`), and every binary that hits
the read cache decodes and keeps all of it, although it uses only its own
module (`gotesting/testing.go:1219`). On 2026-10-10, a 3.2 MB entry for 100,000
tests took 8.4 ms to decode, and its heap stays alive for the whole run.

Have the owner write a per-module index and a precomputed total, which
`countUniqueKnownTopLevelTests` still needs, so that cache hits decode only
their module. Expect 10–25 ms of CPU and several megabytes of heap less per
binary in a repository with 100,000 tests, and nothing measurable in small
repositories. SDK binaries share the cache directory, so the index needs its
own key or version.

## 32. Retry name cleanup scoped to the attempt

From the second attempt on, `beginAttempt`
(`gotesting/retry_attempt.go:151`), called from
`gotesting/retry_attempt_runner.go:115`, walks `testing`'s binary-wide
`matcher.subNames`, which holds every subtest name created so far, to snapshot
and delete the names below the test's prefix. `restore`
(`gotesting/retry_attempt.go:186`) walks it again. Both run under
`matcher.mu`, which every `T.Run` in the binary also needs. On 2026-10-10, a
10-attempt group, such as Early Flake Detection on a fast new test, took
0.15 ms with 1,000 names, 1.0 ms with 10,000 and 8.8 ms with 100,000, blocking
parallel `T.Run` calls throughout.

Scan once for the baseline, or not at all when names are tracked from the first
attempt. Later attempts delete only the names recorded through the `T.Run` hook
below the group's attempt root, including the deduplication keys that
`matcher.unique` adds. Preserve native subtest names, including `#NN` suffixes,
with `-count` above one, nested subtests and late parallel subtests.

## 49. Read-cache expiry fields

Before a cache miss sends its request, cleanup
(`readCacheGlobalCleanup`, `civisibility/utils/net/read_cache.go:944`) checks
up to 32 entries, and `readCacheJSONFileExpired` (`:1006`) unmarshals each JSON
entry only to read its first fields. The 5 ms budget is checked between files,
so one large file can exceed it. On 2026-10-10, a 3.2 MB known-tests entry for
100,000 tests took 4.2 ms with a complete `json.Unmarshal` and 17 µs with a
streaming decoder that stops after `created_at_unix_nano` and `ttl_seconds`,
which are serialized first.

Use the streaming decoder, or clean up after `live()` returns. Expect 5–15 ms
on CI hardware per cache miss and endpoint, which waiters wait for too. Under
`ddto test`, the worker usually makes these misses while `go test` builds, off
the critical path. They stay on it when the worker waits for a test process
(`-o` or `-a`), when the settings require Git, and in binaries without the
worker's state.

## 50. Idle connections during deferred startup

With `DD_CIVISIBILITY_DEFERRED_DELIVERY=true`, every `SendRequest` closes the
shared connection pool when it returns
(`civisibility/utils/net/http.go:250`), and telemetry uses the same pool.
Settings, commit search, packfile and the three parallel feature requests each
dial again, with another TLS handshake in Agentless mode. Close idle
connections once at test admission and at checkpoints, as `PrepareLeakCheck`
does for goleak, and keep none while tests run. Expect one to four handshakes
of 50–150 ms each less on the startup critical path, an estimate.

Under `ddto test`, the read cache serves the startup requests that the worker
made ahead. Deferred-mode binaries still dial again when the settings require
Git, when the worker waits for a test process, and without the worker's state.
[#49](https://github.com/tonyredondo/dd-ci-testing-poc/pull/49) and
[#51](https://github.com/tonyredondo/dd-ci-testing-poc/pull/51) already close
start-up connections before the first test and use one HTTPS connection per
host; keep both. The user has not approved this change yet.

## 3. Per-test state in `testing`'s structures

On 2026-10-15, Mini added 15.4 µs per test; after #69 to #73, on 2026-10-17,
it added 7.24 µs of CPU per test (see above). Execution metadata lives in a
`sync.Map` keyed by the testing object (`gotesting/instrumentation.go:256`).
Coverage shutdown markers and two Testify lookups use more maps keyed by
tests. Private `testing` fields are read through offsets computed by
reflection (`gotesting/reflection_offsets.go`), and retries copy them through
the same offsets. At `44c7fd8`, before the `sync.Map`, a metadata lookup took
12 ns alone and 28 ns with eight goroutines; creating and deleting an entry
took 215 ns and one allocation. Reading the private fields through offsets
takes 68 ns and 112 bytes per call, and the reflection fallback, used when
layout validation fails, 7.2 µs and 71 allocations.

The `testing` overlay already rewrites the package for every supported
toolchain. It could add Mini's state to `testing.common` and generate
accessors, including a retry copy, for the fields read today through offsets.
That removes most of these maps, the lock and the per-call allocations, and
turns layout assumptions into errors when the overlay is built. Expect about a
microsecond and several allocations less per test, less contention in large
parallel suites, and less offset and reflection code.

The standard library cannot import Mini. Added fields must use standard types,
such as an `any` slot, and Mini must reach the accessors through hooks that set
Mini's variables while testing initializes, as `ParallelStopHook` does, or
through interface assertions. Mini still builds
and runs without the overlay, through `testopt.RunM`, so the map path stays as
the fallback. Preserve cleanup lifetime, native cancellation, Testify scopes,
retry ownership, fuzz seeds and SDK-free tests, and validate every supported
toolchain, including tip, with both delivery modes.
