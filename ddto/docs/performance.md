# Performance and profiling

Build performance and event-runtime performance have different causes. The
overlay front-end reduces instrumentation work during compilation. Mini reduces
the linked runtime graph and the cost of recording and delivering CI events.
Measure those paths separately.

## Where time goes

```mermaid
flowchart TB
    Start["ddto invocation"] --> Prepare["Front-end<br/>preparation"]
    Prepare --> Compile["Native compilation<br/>and linking"]
    Compile --> Run["Test execution,<br/>unless -c"]
    Run --> Finish["Event creation and<br/>finalization"]
    Finish --> Send["Serialization,<br/>compression and<br/>delivery"]
    Prepare -.-> P["Inspect go list,<br/>source parsing and<br/>file writes"]
    Compile -.-> C["Inspect compiler and<br/>linker processes"]
    Finish -.-> F["Inspect allocations,<br/>map work and<br/>contention"]
    Send -.-> S["Inspect codec, gzip,<br/>network and flush<br/>latency"]
```

Runtime work can overlap test execution and can happen when a batch fills.
The diagram names the costs; it is not a timing scale. Compile-only measurements
use `go test -c`, which excludes test execution and event delivery.

| Measurement | Tells you |
| --- | --- |
| Wall time | How long the caller waits, including scheduling and I/O |
| Total CPU time | Work across all participating processes and threads |
| Profile flat time | Samples spent in a function itself |
| Profile cumulative time | Samples in that function and its callees; rows overlap |
| `alloc_space` / B/op | Total bytes allocated, including objects already collected |
| Live heap / aggregate memory peak | Retained memory or simultaneous process memory, depending on the tool |

Profile percentages describe where samples landed. Removing work in a function
can change CPU consumption without producing the same wall-time reduction, and
parallel workers can consume many CPU-seconds during one wall-clock second.
Allocation totals are not resident memory. For Orchestrion,
include its daemon and nested builds in CPU and memory accounting; direct-child
process accounting can miss them.

## Front-end optimizations in the code

[`Transform`](../internal/instrument/transform.go) uses the standard Go AST,
skips parser object resolution and applies source-position edits. There is no
DST decoration or type-checking pass in this front-end. Files are sorted for a
deterministic transformation, and unchanged files do not become overlay outputs.
The same AST identifies `testing.parallelStop` for the optional shutdown hook;
hook selection does not parse the sources again.

[`PrepareRuntime`](../internal/runner/run.go) requests only the package fields
it needs from a targeted `go list -deps`, which lists the named packages and
each of their dependencies once. Reachability follows `Imports` through that
graph. Go's per-package `Deps` field would repeat a package's complete closure
for every named package: in a 2000-package fixture whose packages import 990
others on average, that output was 56 MB, and dropping it reduced preparation
from 2.65 to 2.08 s, peak memory from 175 to 88 MB and CPU from 2.55 to 1.60 s
(medians of seven). A small module lists more records instead: about 7 ms more
for 61 packages. Testify discovery can add one metadata or dependency query, as
described below. Both queries decode JSON directly from Go's stdout, and the
front-end keeps decoded package records without a second complete JSON buffer.
It drains and waits for the subprocess even after malformed output; command
failures and their stderr take precedence over partial JSON.

Rewritten-source buffers reserve the final size, including line directives and
inserted advice. Testify preparation parses each selected library file once and
uses that AST for its API/collision checks and entry rewrite. Covered compiler
inputs have different bytes and receive their own validation.

Generated files are immutable and shared by identical content within one plan.
Many packages with the same package name can share an import-only backing file.
That file joins the package's internal tests when it has no external test
package, as is usual. An external test package of its own would be one more
compile and vet per test package whenever Go cannot reuse it, for example with
an empty build cache. Building 61 test packages, 59 of them with only internal
tests, took 180 compiles and 120 vets that way, and takes 121 and 61 now. The
logical overlay entry still exists for every selected package, so metadata and
conflict checks remain correct, and memory, overlay entries and package
discovery still grow with package count.

This sharing ends when the plan is removed. The POC relies on Go's caches and
keeps no persistent cache of instrumented binaries or prepared overlays.

Preparation uses one `go env -json` call to check the toolchain and obtain the
module, workspace and cache paths, and the platforms that select Go's exec
helper. Runtime provisioning, including the temporary workspace, reuses that
result. Local Mini provisioning reads the selected runtime's Go requirement
without changing the client's language version, and older client languages use
a temporary workspace that keeps Mini's Go 1.25 source separate from the
client's. Temporary module files read the user's effective overlay contents,
including replacements or deletion of the sum file, also with an explicit
`-modfile`.

Child commands derive `PWD` from their working directory before adding
environment overrides, so `-C` through a symlink keeps the same paths during
preparation and compilation. With `-mod=mod`, a separate read-only `go list`
resolves the runtime, so the package query cannot add it to the module's
requirements.

## Testify discovery and tool overhead

The [Testify contract](testify.md) selects `-toolexec` from actual
reachability; a module requirement alone does not count. Plain tests and
assert-only targets omit it unless another selected integration needs it: Mini
goleak, SDK guards and span copies, Orchestrion composition, coverage of
rewritten `testing` sources or the Git worker's link signal.

Libraries that the named packages import already appear in the package query,
with their metadata, and unknown test imports use `-deps` so external helpers
remain covered. Only libraries absent from both, such as SDK packages that
Orchestrion can add while compiling, use `go list -find`. The
selected-version/API check stays in preparation, before a warm cache can skip
the compiler. The dependency query excludes selected packages and dependency
closures already inspected by the first query. Unknown test-only imports stay
in the query, including standard packages outside those known closures and
helpers in other modules, because runtime dependencies say nothing about a
client's test-helper graph. Suite reachability is checked before pruning, so a
known suite still receives its metadata and version validation.

Unrelated tools dispatch without opening the plan. On Unix the wrapper replaces
itself with the native tool, and Windows delegates through a child. The wrapper
is a small executable that starts in about 1.2 ms, against 2.4 ms for ddto
itself ([phase
3](optimization-plan.md#phase-3-ddtos-startup-as-the-compiler-wrapper)). The
[latest build matrix](benchmarks.md) measures the complete invocation, including
preparation and tool processes.

Vendor workspaces hard-link the client's files instead of copying them, at a
stable path in the user cache. Go's compile cache keys include each package
directory, so a per-run location would recompile every vendored package on every
run. `TestMiniVendorWorkspaceLinksAndReusesBuildCache` checks that an unchanged
vendored package is not compiled again. Each run reads the names, sizes and
times of the vendored files and checks that the workspace still shares them;
with 20,000 files that took about 80 ms on Linux. Creating a workspace for them
took about 250 ms with hard links, against 400 ms with symbolic links and
700 ms or more with copies. Copies are made only where hard links fail, as
across filesystems.

Selected-version validation must run before compilation: cached archives can
remain usable when vendor metadata changes but source bytes do not.
[`TestTestifyVersionGuardWithWarmVendoredSources`](../integration/vendor_cache_test.go)
checks that case. Testify and goleak share the lookup; both libraries' versions
and APIs are validated even on an unchanged build.

Compiler/linker version identities stay native. The exported suite fingerprint
in `testing` carries instrumentation inputs into Go's package keys. Go owns
compiled-object and test-result caching. Coverage of rewritten `testing` sources
has its own versioned bridge; ordinary client-only coverage needs no bridge.

Goleak, and each SDK package that Mini guards, gets a package-scoped compiler
flag marker instead, because a marker carried through `testing` is not enough
for them. Goleak's worker filters and connection checkpoint are described in
[delivery and goleak](delivery.md). Unqualified `-gcflags` apply when Go
resolved goleak as a command-line target, including an absolute directory
argument. The marker keeps those flags, and package selection is not inferred
with the pattern rules used by qualified compiler flags. The CLI regression
tests in `integration/runner_transparency_test.go` cover this distinction,
module overlays and the symlink case.

## Enqueue and delivery

Enqueueing takes only the client mutex: it never waits for the delivery token,
creates no timer and performs no network I/O. Network work happens in up to eight
background senders when batches fill, and at the end of the session. A batch
can seal below the event-count limit when it reaches the 2.5 MiB byte threshold.
Coverage, logs and telemetry have separate writers, so an incomplete test-cycle
batch does not establish that the process is free of background CI work.

Mini's test-cycle gzip pool uses `gzip.BestSpeed`. Agentless payloads remain gzip
and obey the same uncompressed intake limit. Coverage and diagnostic-log
compressors retain their SDK settings. Compression ratio depends on payload
shape; compare both CPU and wire size when changing the level.

Deferred mode isolates delivery from admitted test groups: it delays delivery
until idle and lets the queue grow across parallel groups. The
[delivery contract](delivery.md) describes that tradeoff.

The source rewriter preserves comment bytes without constructing comment ASTs;
it handles line directives separately. Preparation runs sequentially. The
[build comparison](benchmarks.md) measures its cost as part of the complete CLI
invocation.

## Mini runtime optimizations

| Change | Cost removed or reduced | Constraint to retain |
| --- | --- | --- |
| CI-only runtime graph | Compilation/linking of excluded APM components | Retain CI policies, coverage, metadata and telemetry |
| Events sealed in their span | Metadata and metric snapshots, and a copy of the event, on `Finish` | Setters must never change a finished span's event; getters read only fields that sealing leaves unchanged |
| Small metadata and metric sets | Four allocations for the map of a test event's 22 entries | Up to 32 entries in a slice, searched linearly; a larger set switches to a map, so many tags cost no quadratic time; `omitzero` keeps an absent set off the wire |
| One allocation for the span's context | A context and a boxed identity for each span | propagation's key lives in `internal/propctx`; the carrier answers it with a pointer, and `FromContext` reads both forms; other keys and cancellation go to its parent; it is not part of the span, so a kept context retains no tags |
| Test events without option lists | About ten allocations for each test: a new option list, a closure for each of its values and a boxed test name | `StartTestSpan` applies the shared option lists in place and sets each test's values directly, in the order that the option list had: `TestStartTestSpanEncodesAsOptions` checks that the events encode to the same bytes |
| Per-test lookups and options without allocations | An allocation each to find the test's module and suite, to start it, to close it and to set its final status | Without options, an existing module or suite returns at once; configurations are allocated only for options; known statuses are constants |
| `t.Helper()` only for wrapped tests | Two allocations for each test's helper map | A top-level test that testing runs directly reports the same locations without the mark, except at one stack depth that the SDK port's `ADAPTATIONS.md` describes; `TestMiniHelperMarksKeepLocations` pins them, with and without a retry wrapper |
| Typed tag setters | An interface conversion for each tag value that is not a constant | `SetStringTag` and `SetMetric` keep `SetTag`'s semantics; the SDK port's call sites keep their lines |
| Shared CI tag snapshots and cached options | Per-span closures, common map entries and repeated wire strings | Preserve late updates, getters, option precedence, numeric overrides, child spans and Bazel filtering |
| Metadata capacity from `Tag` options | Repeated growth while applying common tags, and room for options that add no entry | Count every `Tag` option and the entries that `newSpan` adds itself; options that set fields or shared defaults reserve nothing |
| Overrides recorded at enqueue | Searching every event's keys when lifting shared defaults into payload metadata | `Client.add` stores `eventSize`'s result on the event; only those events can remove a default |
| Shared-tag key filter | Snapshot lookups for keys that cannot be shared | A snapshot holds only keys that `IsSharedCITag` selects |
| Finish configuration only with options | One allocation for every `Finish` | Options take the configuration's address, which moves it to the heap; `Finish` creates it only when options are given, and applies them outside the span's lock |
| Appended close-action lists | Copying all prior actions on every registration | Execute in LIFO order, with pre-close barriers before ordinary actions |
| Lazy metric sets | An allocation for events without numeric metrics | Tag type transitions must retain their existing semantics |
| Direct event encoding | An intermediate encoded event array and its payload copy | Preserve field names, event versions and byte limits |
| Queue and payload reuse | Fresh backing storage after every successful batch | Retryable flush failures retain events; final closure discards a failed batch once |
| Gzip writer and buffer pooling | New compression state for each agentless batch | Seal all request readers before returning storage to a pool |
| Bounded buffer retention | Long-lived oversized buffers after large payloads | Payload and gzip capacities above 2.5 MiB are discarded |
| Standard-library telemetry maps | An external concurrent-map module | Preserve registration, startup replay and log counts under concurrency |
| Bound ordinary CI counters | Repeated tag slices, key joining and registry lookup | Preserve startup replay, client swaps, disabled telemetry and feature-tag fallback |
| Inline metric points | A heap allocation on every count submission | Collect each value/timestamp together; retain zero, NaN and reset semantics |
| Completed coverage workers | A worker blocked on an unbuffered shutdown notification per covered test | Close the completion channel after processing, including profile errors; shutdown still waits for outstanding work |
| Coverage duration clock | A wall-clock read from background serialization | Use monotonic elapsed durations; retain event and metric wall timestamps |
| Literal Testify prefix check | Compiling `^Test` for each suite method | Match exactly the same method names |
| Source parser without object resolution | Unused identifier objects in metadata lookup | Retain ITR comments, function ranges and parse errors |
| Direct high trace-ID hex encoding | General-purpose integer formatting | Retain 16 lowercase hex digits, including leading zeros |
| `math/rand/v2` trace and span IDs | A `crypto/rand` read, a system call on some platforms, for every event; parallel tests serialized on it | Unique nonzero 63-bit span IDs; a root span ID is the trace ID's low half; a child differs from its parent |
| Lazy classification tries | Eager construction of stack-prefix tables | Preserve internal filtering, third-party matching and redaction; publish immutable tries once |
| Internal codec/platform subsets | External runtime module requirements | Preserve original semantics, licenses and source provenance |

Clock isolation also has costs. Ordinary coverage processing is synchronous
when telemetry or debug logging is enabled. Deferred mode retains captured
coverage until its idle checkpoint, so parallel groups can use more memory, or
more temporary disk space where text profiles stay. Startup telemetry overlaps
the settings request in both modes, and initialization waits for both before
the first test, including request failures and cached settings. A slow endpoint
still delays admission, although no test's duration includes it. The recorded
build/runtime benchmark tables do not measure these scheduling changes. Read
[delivery](delivery.md) and the SDK port's
[adaptation record](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md) before
changing this scheduling during an upstream update.

Copying a codec into the repository does not make its encoder faster. The
dependency reduction comes from changing the runtime graph, and the allocation
changes come from event ownership and buffer reuse. Mini's runtime module has
no external requirements; compatibility fixtures declare their dependencies
in separate modules.

The [runtime comparison](benchmarks.md#runtime-of-the-original-test-binaries)
includes startup, test execution and delivery, and its detailed report records
event counts and wire bytes. It measures the complete implementation, so use
focused profiles and controlled comparisons to attribute a change to one
optimization.

The SDK-specific implementation rules are recorded in
[ADAPTATIONS.md](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md). Common
lifecycle counters keep bound handles only for the existing framework/hierarchy
tag combinations. Events with retry, EFD, quarantine or other extra tags use the
general registry path. `MockClient` resets invalidate a binding; normal client
swaps retain the existing swappable handle.

Counts keep their value/timestamp inline under a short mutex. A flush
detaches the whole point under that mutex, then encodes it after releasing the
lock, which removes per-submission snapshots while preserving collection
boundaries.

### Checking metadata capacity

`newSpan` reserves a metadata entry for each `Tag` option, and for the entries
it adds itself: `_dd.origin`, `_dd.p.tid` and the client's tags, `env` and
`version`. Options that set fields, timestamps or the shared CI snapshot reserve
nothing. A test event of an instrumented binary starts with the equivalent of 27
options, 22 of them tags, and ends with 22 entries. With `DD_ENV` set, its estimate of 25 reserves
a slice of 25 entries, 800 bytes. Sets of up to 32 entries are slices, so an
estimate above 32 starts a map instead, and a slice that grows beyond 32
entries switches to one. When the metadata was a map, the earlier estimate of
30 gave a test event 64 slots, 2.25 kB. An option is now twice the size of a
function value, which adds 216 bytes to the slice of a test's 27 options. `testopt`'s public options remain
functions; its client turns each into an option that reserves one entry.

Text-only spans reserve exactly the entries that their tags add. A fixed
capacity of eight, including a version limited to small option lists, regresses
the text-heavy cases. `BenchmarkSpanMetadata` covers CI-shaped and text-only
options with 0, 4, 12, 32 and 128 additional tags, and
`BenchmarkTestEventLifecycle` a test event's options:

```sh
go test ./internal/minitracer -run '^$' -bench '^(BenchmarkSpanMetadata|BenchmarkTestEventLifecycle)$' -benchmem -count=5
```

Compare allocation counts as well as time before changing the estimate. Keep
the option constructors inlinable: a `Tag` that classified its value was not,
and the closures that callers had kept on the stack moved to the heap.

## The ownership rules behind reuse

Read [the event lifetime](architecture.md#native-event-ownership) before changing
`Finish`. The client copies caller-owned configuration at construction. A span
then owns its event and its private metadata and metric sets until it seals
them; getters continue to work after finalization. Hierarchy IDs are stored
outside those sets so serialization can write the CI fields without deleting
tags.

Delivery owns one reusable payload buffer under `sendMu`. The transport borrows
its bytes only for the duration of `Send`. In agentless mode it borrows a gzip
compressor and buffer too. Request and replay readers must all become unable to
read before either backing buffer is reusable; closing only the initial reader
is not enough.

In ordinary delivery, the queue is bounded: eight sealed batches, all of which
can be in flight, plus the open batch, each limited by event count and byte
estimates. At the bound a finisher waits for a sender while deliveries succeed,
and is rejected and counted only after a delivery has failed. A throughput change
must therefore check waiting, errors and drops as well as ns/op; the slow-intake
test checks that no event is lost.

Deferred delivery can buffer more than one batch while tests are active. Its
pending queue has no total size limit; each outgoing payload still obeys the
intake bounds. Include peak memory when assessing that mode. Its checkpoints
send up to eight payloads at once and join them before the next test is admitted.
Measure both throughput and memory with the payload size and intake delay fixed.

Each background or checkpoint delivery of a sealed batch is bounded by
`FlushTimeout` (10 seconds by default). Explicit `Flush` and `Close` use their
caller's context.

[Sealed-span tests](../internal/minitracer/sealed_span_test.go),
[batching tests](../internal/minitracer/batching_test.go),
[queue/concurrency tests](../internal/minitracer/client_test.go) and
[transport reuse tests](../internal/citransport/reuse_test.go) exercise these
contracts. Preserve them when changing synchronization or pooling.

Common CI/Git/OS/runtime strings live in an immutable snapshot shared by spans.
Each span stores only its own tags, metrics and overrides. Getters check local
values before the snapshot. A numeric metric masks a shared string with the same
key; later text can replace that metric. Delivery puts compatible defaults in
payload metadata. Mixed snapshots and overridden keys use local values without
changing the effective CI attributes. See the
[metadata contract](delivery.md#payload-level-common-metadata) for fallback and
byte-accounting rules.

The pooled test-cycle compressor uses `gzip.BestSpeed`. It produces standard
gzip with less compression work and potentially more wire bytes. Coverage and
log writers keep their own SDK compression settings. Compare CPU and transmitted
bytes when changing this level; decoded content and intake limits must remain
unchanged.

## CI tag snapshot lookup

Internal readers use `GetCITagsReadOnly`, so a published snapshot can be reused
without comparing every string on every span. Explicit updates invalidate it.
A caller that requests the mutable `GetCITags` map opts that map back into full
content checks, preserving late sequential edits.

Exercise the read-only and mutable paths together:

```sh
GOMAXPROCS=4 go test ./internal/thirdparty/dd-trace-go/civisibility/utils \
  -run='^$' -bench='^BenchmarkCITagsSnapshot$' -benchtime=300ms -count=3
```

Update and concurrent-reader tests protect the snapshot's correctness. Keep
raw samples with a specific optimization experiment; the repository's retained
[comparison dataset](benchmarks.md) measures complete builds and test groups.

## Profiling the native event path

`BenchmarkTestEventLifecycle` reproduces a test event of an instrumented
binary: started as `createTest` does, with `StartTestSpan` and the options that
the integrations share, then the tags set while the test runs, 22 metadata
entries and 3 metrics, with the CI tags in a shared snapshot. `BenchmarkEventLifecycle` creates simpler events with twelve
client tags and no snapshot, so it does not exercise the snapshot code. Both
start a telemetry client, as a test binary does: before startup, every counter
submission allocates an entry to replay. Their HTTP transport consumes
requests in memory. The agentless variant still performs gzip, but no variant
measures real network latency, CI policy startup or full test execution.

From the repository root, choose a new directory for the artifacts:

```sh
PROFILE_DIR="$(mktemp -d /var/tmp/dd-ci-event-profile.XXXXXX)"

GOMAXPROCS=1 go test ./internal/minitracer -run '^$' \
  -bench '^BenchmarkEventLifecycle/gzip=false$' -benchtime=10s -count=1 -benchmem \
  -cpuprofile "$PROFILE_DIR/serial.cpu.pprof" \
  -memprofile "$PROFILE_DIR/serial.heap.pprof" \
  -o "$PROFILE_DIR/minitracer.test"

go tool pprof -top "$PROFILE_DIR/minitracer.test" "$PROFILE_DIR/serial.cpu.pprof"
go tool pprof -top -cum "$PROFILE_DIR/minitracer.test" "$PROFILE_DIR/serial.cpu.pprof"
go tool pprof -top -sample_index=alloc_space \
  "$PROFILE_DIR/minitracer.test" "$PROFILE_DIR/serial.heap.pprof"
```

Repeat with `gzip=true` and different artifact names to isolate the compression
path. This benchmark runs a serial loop, and raising `GOMAXPROCS` does not
turn it into a parallel producer benchmark: concurrent finish behavior needs a
workload that starts concurrent producers. The race and concurrency tests
establish correctness only.

For an optimization experiment, keep the baseline and candidate SHAs, inputs,
commands, raw repetitions and limits in a task-scoped artifact directory.
`scripts/minitracer_benchmark.py` does this for a base and a candidate worktree.
It runs these microbenchmarks in both trees, with the candidate's benchmarks,
then a test binary of many empty tests, built with `go test` and with each
tree's ddto, and checks that every test event arrives. Its
[first results](results/minitracer-20261016-linux-go1.27/README.md) measure four
rows of the optimization table above: metadata capacity from `Tag` options,
overrides recorded at enqueue, the shared-tag key filter and the finish
configuration, which then lived in the event. The
[second results](results/minitracer-allocs-20261016-linux-go1.27/README.md)
measure the events sealed in their span, the small sets, the span's context in
one allocation and the typed tag setters. They also count the objects and bytes that the test
process allocates per test, from its start until testing writes the memory
profile, before Mini's final delivery. With `--own-benchmarks`, each tree runs its own
benchmarks, for a candidate whose benchmarks use internals that the base lacks.
The [third results](results/minitracer-test-span-20261016-linux-go1.27/README.md)
measure test events started without option lists, and the
[fourth](results/minitracer-test-path-20261016-linux-go1.27/README.md) the
per-test lookups and options without allocations, and the
[fifth](results/minitracer-test-helper-20261017-linux-go1.27/README.md)
`t.Helper()` only for wrapped tests. The
[cumulative results](results/minitracer-cumulative-20261017-linux-go1.27/README.md)
compare `d6fd1c3`, before the first of these changes, with `bfcf898`, after
the last: Mini's CPU added per test went from 14.05 to 7.24 µs, its wall time
added per test from 7.71 to 3.96 µs, and the objects that it
adds over `go test` from 51.2 to 20.7 per test, with identical payloads. Every run also compares the
two trees' payloads: each event with its envelope's version and the metadata
that it inherits, counting IDs, timestamps and other values that differ between
runs by presence and type, and hierarchy IDs by what they reference.
`--same-wire` stops it when they differ: a change that must keep the wire
contract runs with it.
Alternate execution order and retain slow or failed samples. Record CPU,
allocations, complete wall time, wire bytes and peak memory when they apply. A
hot function's profile share does not predict the percentage that a change will
save. Two benchmarks need care:

- `BenchmarkEventLifecycle` at the default `GOMAXPROCS` shows gzip enabled and
  disabled at nearly the same speed, 1.5 and 1.6 µs, because delivery runs on
  other cores. At `GOMAXPROCS=1`, the gzip case takes 4.3 µs. Report CPU time,
  or run it with `GOMAXPROCS=1`.
- `BenchmarkCommonMetadataLifecycle` calls `utils.GetCITags()`, which opts into
  the mutable tag path. `maps.Equal` then takes 13.6% of its CPU, although the
  runtime never calls it.

A CPU profiler inside the test binary does not profile Go's compiler and linker.
For build diagnosis, capture the Go subprocesses and their process tree, or use
`-x` and a Go build trace where available. `go test -cpuprofile` profiles test
execution and should not be used as evidence about a `-c` build's CPU cost.

## Compile-only comparisons

Use four variants: native Go, Orchestrion with the pinned testing aspects,
POC SDK and POC Mini. Fix the project revision, module graph, SDK reference,
toolchain, flags and output layout before timing. Build the CLI and reference
instrumentator outside the timed commands.

For example, after preparing each target module and output directory:

```sh
GOMAXPROCS=4 go test -p=4 -c -o ./out/native/ -ldflags=-w ./...
GOMAXPROCS=4 /path/to/ddto test --runtime=sdk -p=4 -c -o ./out/sdk/ -ldflags=-w ./...
GOMAXPROCS=4 /path/to/ddto test --runtime=mini -p=4 -c -o ./out/mini/ -ldflags=-w ./...
GOMAXPROCS=4 go test -p=4 -c -o ./out/orchestrion/ \
  -toolexec="/path/to/orchestrion toolexec" -ldflags=-w ./...
```

For a controlled comparison, give the Mini fixture an explicit POC requirement
and the SDK fixtures the pinned SDK. This keeps provisioning out of the timed
builds; ordinary CLI use can provide Mini automatically. Orchestrion must load
the SDK's testing-only configuration. Match the native project's effective
dependency inputs across variants and record any graph additions made for
comparison tooling.

Define the scenario before interpreting its result:

| Scenario | Cache and output condition |
| --- | --- |
| Cold build | Independent empty build cache per variant and no output binary; downloaded modules and OS page cache may still be warm |
| Unchanged build | Reuse that variant's cache and existing output binary |
| Forced link | Reuse compiled dependencies and set a fresh linker `-buildid` for each repetition |
| Real edit | Change the same executed test or application body for every variant, retaining its own cache |

Forcing a link measures more than the linker: Go planning, CLI preparation and
generated-main compilation can still contribute. A real-edit scenario must
change an executed test or application body; comment and unused-constant edits
belong in separate diagnostic scenarios. Keep separate output paths so one
variant cannot overwrite another's reusable binary.

An empty `GOCACHE` alone does not establish a cold link: an existing output
binary can let Go skip it. Remove only the experiment's output before each cold
observation, then inspect `-x` for actual compiler and linker invocations. Check
unique edits also compile; keep comment/unused-constant diagnostics distinct
from reachable body edits.

Alternate order, retain every sample and add native-versus-native controls.
Record CPU affinity, `GOMAXPROCS`, `-p`, SMT use and other host workloads.
Report medians with ranges and repetition counts; do not delete slow runs.
Agree on time/run/disk limits before a large matrix.

For a POC table cell, put the signed percentage relative to Orchestrion first,
then the overhead relative to native:

```text
100 * (POC / Orchestrion - 1)
100 * (POC / Native - 1)
Example format: 1.20 s (-40.0% vs Orchestrion; +20.0% vs Native)
```

The example only shows the arithmetic. Compute percentages from unrounded
medians. A negative first value means the POC took less time than
Orchestrion; a positive second value means it took more time than native.

The [build benchmark guide](build-benchmarks.md) documents
[`scripts/build_benchmark.py`](../scripts/build_benchmark.py), which runs
Native, Orchestrion and Mini across the five scenarios, assigns affinity,
measures exclusive cgroups and qualifies the outputs. Its `report` command regenerates the
[recorded build tables](results/20261016-linux-go1.27.1/build/README.md)
from every retained CSV observation, without Go or network access.

## Evidence and remaining work

The [recorded comparison](benchmarks.md) covers Native, Orchestrion and Mini,
Gin/Chi, direct and external Testify, race and coverage at 4/32 CPUs. It keeps
raw durations, aggregate cgroup memory, variation and event counts, and
separates the unused-constant diagnostic from the reachable test-body edit.
Every build cell qualified: Mini was measured and qualified again on
2026-10-14, and Native and Orchestrion keep their 2026-10-04 observations and
qualification. The runtime report distinguishes
execution failures from missing SDK event references and keeps both kinds of
observations. The 115-case parity data keeps its own revision and date, since
the performance refresh did not repeat that suite. Current regression checks
live in [validation](validation.md).

Further profiling can examine MessagePack encoding, tag construction, telemetry
lookups and time spent waiting for `sendMu`. A proposed change needs before/after
measurements on its affected path, plus the compatibility tests that protect its
ownership and wire behavior. Linker experiments and a persistent binary cache
are not part of the current implementation.
