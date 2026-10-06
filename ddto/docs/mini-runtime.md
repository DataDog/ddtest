# Native CI runtime

For implementation details, read [architecture](architecture.md),
[maintenance](maintenance.md) and [performance](performance.md).

The experimental `mini` backend keeps the testing hooks and CI policies extracted
from `dd-trace-go/main` at `96aedb31048c07e29e7a20a4333dc3b8d289c52d`,
replacing its general tracer with a native CI
event client. Mini is the default; `--runtime=sdk` uses the original SDK module.
Neither the original SDK nor target sources are edited.

## Use the local POC

Build the driver from this checkout:

```sh
go build -o bin/ddtest ./cmd/ddtest
```

Then run it from the target module. No changes to the module are needed:

```sh
/path/to/ddtest test --runtime=mini -count=1 ./...
```

When the module does not require the runtime, `ddtest` adds it to a temporary
copy of `go.mod` and `go.sum` that Go reads through `-modfile`; the module's own
files stay unchanged, and `go mod tidy` cannot break a later run. Mini honors
the client's `replace` directive first. Otherwise, it uses the sources recorded
in the CLI when they are still available: either a local checkout or the module
cache used by `go install`. A local checkout supplies its current contents,
including edits made after building the CLI.

The toolchain must be Go 1.26 or newer; CI covers 1.26 and 1.27. The client's
`go.mod` may declare an older version. For a local runtime, the temporary
module uses at least the Go version required by that runtime's `go.mod`,
including when the client supplies a `replace`. A newer client Go directive
is retained. This needs no proxy request and leaves the client's files intact.

For a `-trimpath` build or a missing source directory, `ddtest` checks
`GOMODCACHE` for the exact version recorded in the CLI. Only if no usable local
copy exists does it fetch that version with `go get`. It does not search for
other checkouts or substitute a different cached version. A development binary
without source paths or a published version needs an explicit client `replace`.

The SDK backend uses `go get` to provide its pinned version; a different
required SDK version is still an error. Before any runtime `go get`, the CLI
prints a status line and streams Go's download messages and diagnostics to
`stderr`. This keeps `go test -json` output valid. Go may be quiet while querying
module metadata, even when the module files are already cached.

With `-mod=mod`, including from `GOFLAGS`, `ddtest` resolves the runtime
read-only, so it is never added to the module's files. The module's own packages
can still update `go.mod` and `go.sum`, as `go test` would. When `ddtest`
provides the runtime, requirements needed only by test files go to the temporary
copy instead.

To pin the runtime in `go.mod` instead, require it and keep the requirement with
a file that `go mod tidy` and `go mod vendor` see but builds ignore:

```go
//go:build tools

package tools

import _ "github.com/tonyredondo/dd-ci-testing-poc/testopt"
```

A vendored module or a Go workspace must require the runtime this way: `ddtest`
does not provide it through `-modfile` in those modes.

`--runtime=mini` and `--runtime mini` can appear before or after Go flags and
package names. Put them before custom test flags, `-args` or `--`, which belong
to the test binary. Omitting the option selects Mini; use `--runtime=sdk` for
the reference runtime. Mini does not require `dd-trace-go`. Existing application
imports of that SDK still link it normally; switching the instrumentator does not remove those imports.
Both backends retain the CLI's `DD_CIVISIBILITY_ENABLED=parent` default when absent.
Native Go controls compilation, test-result caching, flags and exit status.

## Portable test context

```go
ctx := testopt.Context(t)
identity, ok := propagation.FromContext(ctx)
if ok {
    headers := http.Header{}
    if err := propagation.Inject(identity, headers, propagation.W3C); err != nil {
        t.Fatal(err)
    }
    // Attach headers to an integration-test request.
}
```

Import `testopt` and `propagation` from this module. `testopt.Context` retains the
native testing cancellation lifetime. Subtests have their own event identity.
An uninstrumented test has no identity. Isolated retry children also have no local
identity: their events are reconstructed by the controlling parent, following the
original SDK contract.

Both W3C `traceparent`/`tracestate` and Datadog headers carry the complete 128-bit
trace ID and active span ID. W3C vendor state is preserved. Generated span IDs use 63 bits,
like dd-trace-go, and so do the CI session, module and suite IDs derived from them. Sampling priority is
propagation metadata; the mini client records every CI event. Invalid identifiers
return an error. This is trace identity propagation, without an OpenTelemetry SDK,
baggage API, or an APM adapter.

A private `context.Context` value is not automatically visible to another tracer.
The receiving tracer must explicitly extract a carrier. The integration suite
checks extraction by the original `dd-trace-go` propagator. A future dd-trace-go
helper can bridge the portable value in the same process; that shim is outside
this implementation.

## Native events and delivery

The client writes the CI test-cycle MessagePack protocol directly: envelope
version 1, test events version 2, and session/module/suite events version 1. CI
hierarchy IDs are separate from distributed trace identity. The runtime has its
own experimental version, `0.0.0`. Its diagnostic log prefix is
`TestOptimization Tracer v0.0.0`; the SDK backend retains its original prefix.

Agentless delivery uses gzip and `/api/v2/citestcycle`. Agent delivery uses the EVP
v2 proxy and its intake header. Coverage continues through the extracted native
CI coverage client. No APM tracer, profiler, security engine, remote configuration
client, OTLP stack, or agent statistics pipeline is imported by the mini runtime.
CI configuration, diagnostics and CI telemetry helpers are retained. The native
runtime reads `DD_TRACE_DEBUG` and `DD_CIVISIBILITY_LOGS_ENABLED` from the
environment only: APM YAML/Fleet configuration is intentionally unsupported.
CI telemetry excludes SDK heartbeats, SCA dependency inventory and REST endpoint
inventory. Actual CI metrics, delivery diagnostics and lifecycle telemetry
remain; their shared intake URL contains `apmtelemetry`, but no APM tracer runs.
`DD_VERSION` sets the service `version` tag on CI events. Session naming reuses
the SDK algorithm: an explicitly defined `DD_TEST_SESSION_NAME` wins, including
an empty value; otherwise use `<CI job name>-<test command>`, or the test command
when no job name is available. The name is sent in envelope metadata.

Test-cycle batches use the CI writer's 5 MiB uncompressed MessagePack limit and
2.5 MiB flush threshold. Accounting includes the envelope and uses schema
`Msgsize()` upper bounds, so batching is conservative. A single oversized event
is rejected explicitly. Successful gzip compression never bypasses the byte
limit. The event-count bound remains in force, failed batches retain their byte
accounting, and request failures, serialization and drops report CI telemetry.
Delivery retains bounded retries, permanent-error handling and cancellation.

Bazel payload-file mode converts native test-cycle MessagePack to JSON before
writing to `TEST_UNDECLARED_OUTPUTS_DIR/payloads/tests/`. The extracted coverage
and CI telemetry writers use their corresponding directories. Manifest mode
uses the original offline read-cache rules. File mode does not require HTTP
credentials for the native event client and does not send event HTTP requests.
The SDK-derived settings loader still requires a nonempty API key to consume
a settings cache; the offline differential fixture supplies a synthetic key.
The test verifies zero HTTP requests with all three payload types present.
Mini has **no external runtime module dependencies**. A fresh consumer's
`go get` and `go mod tidy` add only `github.com/tonyredondo/dd-ci-testing-poc`;
the consumer also builds offline. The module requires only Testify v1.7.5, for
its own tests: a consumer with that version or newer keeps its own, and one
without Testify gains no requirement. Repository test dependencies do not enter
`go list -deps ./testopt` or the CLI's imports. Runtime UUIDv4 generation uses
`crypto/rand`. The [maintenance checks](maintenance.md#verification-before-publication)
show how to audit these dependency boundaries.

CI telemetry metric registries use the standard library's `sync.Map`; only first
registration is serialized. Global metric references use `sync.OnceValue` so
concurrent registration cannot duplicate startup replay or client installation.
Diagnostic logs use a mutex-protected map. Collection detaches that map under
the same lock, then formats the immutable batch outside it, preserving counts
when additions overlap collection. No `xsync` source is incorporated. The SDK
comparison fixture still uses its original dependency graph.

`testopt.New` also creates an explicit client without reading API credentials
from the environment. Span tags follow dd-trace-go's `SetTag` value
rules: pointers to basic values are dereferenced, exact numbers become metrics,
`fmt.Stringer`, byte slices and slices use the SDK's text forms, and `error`
tags record the message, type and handling stack (`error.stack` only for
`fmt.Formatter` errors). A nil error clears the flag. Its configuration controls endpoint, tags, batch capacity
and timeout. `Finish` seals an event once and shares its private tag/metric maps read-only
with delivery. Setters become no-ops and getters remain available. Native CI
hierarchy IDs are stored separately, so serialization never deletes tag entries.
Metadata maps are sized up front; metrics maps are allocated only when needed.
CI/Git/system tags use an owned immutable base with local per-span overrides.
Compatible event kinds share that base in payload metadata. Numeric overrides
and mixed snapshots use a local fallback; generic child spans retain their own
tags. Getters resolve the same values before and after `Finish`. See the
[common metadata contract](delivery.md#payload-level-common-metadata).
Events encode directly into one reusable, bounded payload buffer; the queue
capacity is reused after successful delivery. Failed flushes retain their events
while the client remains open.
Agentless gzip compressors use `gzip.BestSpeed` and reuse bounded output buffers.
This remains standard gzip, trading compression ratio for CPU time. Request bodies
are sealed before those buffers can be reused, including asynchronous HTTP errors
and replay readers. APM `process_id` enrichment is omitted.
`Flush` and `Close` report delivery errors. A failed `Flush` keeps its batch for a
later attempt while the client is open. `Close` seals the client; if final delivery
fails, it abandons that batch and reports one `endpoint_payload.dropped` sample,
regardless of the number of events or HTTP attempts. Rejected events before
batching or after closure do not increment that payload metric. `DroppedEvents`
counts rejected events and events in an abandoned batch. Hook-driven shutdown
logs delivery failures and lost event counts without changing test results.
An ambiguous HTTP failure can lead to a repeated delivery; this is not a durable
or exactly-once delivery mechanism.

Mini can defer delivery until no instrumented test is active. Reachable goleak
receives its automatic integration in both delivery modes. See
[delivery checkpoints and goleak](delivery.md) for configuration and limits.

## Verification and boundaries

See the [feature parity inventory](ci-parity.md) for policy combinations, event
counts, Testify compatibility and CI evidence. Fast manual hierarchy calls publish static
capabilities before asynchronous settings loading, so emitted events retain them.


The local suite checks real loopback payloads against the SDK, preserving test
attributes, statuses, error messages, stack frames and source lines. Mini stack
comparisons canonicalize the relocated library namespace/root and map the known
test-wrapper location from line 840 to the pinned SDK's line 838, plus the
two subtest calls at `instrumentation_orchestrion.go:321/327` to SDK lines
319/325. Stack mappings check the function and exact line. Testify's `Error Trace`
lists only source locations, so its entries use the exact file and line. Application
frames and other library locations remain strict. The expanded comparator keeps CI
metadata, metrics, service/resource/type, custom tags, capability tags and ITR
correlation. It explicitly excludes APM sampling/profiling/process enrichment
and process-local identity/order. Duplicate SDK Git aliases and hierarchy IDs
are checked for agreement before exclusion. Native MessagePack hierarchy IDs
and complete envelope metadata are tested separately. Benchmark timing metrics
are observational and retain their existing semantic benchmark checks. Synthetic policy servers and their read caches are isolated so port reuse cannot reuse another policy response.

Coverage checks link each payload to its test event and verify the executed
application line belongs to `TestPass`, with no attribution to `TestSkip`.
Race builds exercise parallel and nested tests. A second race/atomic-coverage
fixture executes distinct functions in concurrent tests with shuffle/count,
then retries a failing test against another distinct function. Exact coverage
bitmaps are checked against the SDK. Each parallel test owns only its function
blocks. Retry coverage follows the SDK policy: the initial attempt is uploaded,
and subsequent attempts must not contaminate its bitmap.
Additional checks cover CLI
activation and inheritance, TestMain, all failure/skip methods, retries, EFD,
ITR, quarantine, attempt-to-fix, benchmarks, examples, fuzz seeds, panic, Goexit,
timeouts, overlays, JSON, build tags, multiple packages and Go's result cache.

This is a POC. Real Datadog intake acceptance and UI behavior are unverified.
Agent tests use a loopback EVP implementation, not a deployed agent. Bazel
manifest/payload-file contracts are checked against the SDK using a real offline
manifest, read cache and output files; an actual Bazel build/toolchain invocation
has not been run. Full fuzz campaigns remain unverified. The
[Testify comparison](testify.md) checks suite entry registration, external callers,
lifecycle, policies and coverage against the original SDK/Orchestrion runner. The
[compatibility workflow](validation.md#compatibility-workflow) runs natively on
Linux, macOS and Windows. Inspect its current PR jobs for platform results.
[Mini runtime contracts](validation.md#mini-runtime-contracts) describe the
checks and their limits.

Both backends need a toolchain outside `GOMODCACHE`: Go rejects overlays targeting
files beneath that cache. If Go downloaded your toolchain there, select an
installation outside the cache before invoking `ddtest`. The CLI does not relocate
it for you.

## Source ownership

Extracted CI code is grouped in `internal/thirdparty/dd-trace-go/`, retaining
upstream package paths below the top `internal/` level. All origins carry a README,
exact commit, original license and source/local hashes; see the
[update procedure](../internal/thirdparty/README.md).

Extracted CI code and helper files retain their upstream Apache 2.0 copyright
headers. Adaptations change internal import paths, bind span calls to the native
client, replace private metadata linknames with direct access, and remove APM
mock, security, profiling and process-tag dependencies. The hook execution and
CI policy logic are retained. See `NOTICE` and the generated MessagePack schema
in `internal/minitracer/events.go`.

## Build and runtime measurements

The [latest comparison](benchmarks.md) covers Native, Orchestrion, POC SDK and
Mini at 4/32 CPUs on Gin, Chi and direct/external Testify callers. It separates
compile-only cold/cache/link/edit timings from execution of prebuilt binaries,
including default/deferred delivery and aggregate memory peaks. Its detailed
reports retain coverage/race combinations and failed application cases.
The repeated 115-case SDK/Mini comparison records event counts and durations
separately.

## Reused upstream CI tests

60 complete CI test files from the pinned SDK are ported, along with
Bazel mode and telemetry-recorder tests and selected concurrency assertions.
They cover configuration, CI providers and session naming, source/trimpath and
CODEOWNERS metadata, Git fixtures, impacted tests, networking, coverage writers
and profiles, retry runtime ownership/lifecycle, feature selection, ITR
backfill, offline cache, HTTP lifecycle, signals and CI telemetry.
[Source hashes and exact adaptations](../internal/thirdparty/dd-trace-go/TESTS.json) distinguish
complete files from selected tests/helpers and list what is not fully ported.
The port does not claim to reproduce every SDK test: APM mock/span-count and
Orchestrion-specific harnesses have different prerequisites. Actual native
spans and wire integration tests supply the corresponding runtime evidence.

### Compatibility harness portability

Fixture preparation accepts LF and CRLF checkouts. Parallel/retry coverage
checks use the current Go toolchain's independent `go tool cover` ranges rather
than Go 1.27-specific byte constants, retaining exact per-test bitmap checks.
The copied backend-count/read-cache test runs its complete assertions in a
fresh process so mock bootstrap workers from preceding tests cannot reuse its
package globals.

The SDK starts feature discovery asynchronously. Its initial session event can
omit capabilities or ITR correlation while its test events contain them. Mini
retains these session attributes. The comparator fills only missing session
values from consistent attributes of that session's actual test events; it
never drops a CI attribute, changes a present value, crosses session identities
or fills a missing test attribute. Unit checks reject conflicts and omissions.
This is semantic inheritance parity, not byte-identical event placement.

Sessions without test events (including listing, examples and fuzz seeds)
validate Mini's complete capabilities against the frozen SDK declarations and
permit only missing SDK capabilities. This applies only when both runtimes
emit sessions without tests. Wrong values, unknown capabilities, missing test
events and all other CI attribute differences remain failures.

## Internal MessagePack runtime

`msgp` v1.6.4 and `fwd` v1.2.0 are incorporated under `internal/thirdparty/`, preserving
upstream runtime implementations, original tests, copyright notices and licenses.
This is source incorporation: consumers do not depend on their external modules,
and a library-local `vendor/` directory is not required. It moves maintenance
responsibility here; it does not itself reduce codec instructions or linked bytes.
The canonical records are the [msgp manifest](../internal/thirdparty/msgp/SOURCE.json)
and [fwd manifest](../internal/thirdparty/fwd/SOURCE.json). The
[codec maintenance procedure](maintenance.md#codecs-and-generated-files) explains
when to recopy sources and when to regenerate serializers.
Regenerate reproducibly with:

```sh
go run ./scripts/vendor-msgpack
go generate ./internal/thirdparty/msgp/msgp ./internal/minitracer ./internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage
```

The generator runs in an isolated, pinned tool module. Its dependencies are
build-time tooling and never enter `go list -deps ./testopt`. Runtime source
changes are limited to import relocation and the test generator directive.
The module cache must contain those pinned tools for offline regeneration.

## Internal platform support

The runtime no longer requires `golang.org/x/sys`. Linux and AIX use the standard
library's `syscall.Uname`. BSD platforms read the pinned numeric sysctl keys
through `syscall.Syscall6` into Uname's fixed buffers, retaining partial bytes on
`ENOMEM` and the original whitespace formatting. Solaris retains the small upstream
libc/runtime trampoline. Windows uses a selected internal subset of native Job
Object, thread, timer and read-only registry operations. Retry ownership and
process-containment policy are unchanged.

Windows DLL loading remains restricted to the system directory through the same
standard-runtime hook used by `x/sys`; application-controlled DLL paths are not
searched. This hook and the Solaris trampoline must be rechecked when supported
Go versions change. The upstream BSD license is retained in
`internal/thirdparty/xsys/LICENSE`; the
[platform extraction record](../internal/thirdparty/xsys/EXTRACTION.json)
records pinned sources, adaptations and destination hashes.

Platform tests cover registry decoding, buffer growth, BSD formatting and error
paths. Windows ABI checks compare Job Object/thread structure sizes, offsets and
constants with the pinned `x/sys` source on amd64, 386 and arm64. Native platform
jobs and cross-compilation serve different purposes: the
[compatibility workflow](validation.md#compatibility-workflow) runs Linux, macOS
and Windows; cross-linking the other Unix targets checks build compatibility.
A successful build does not establish native execution on those targets.

BSD metadata reads do not use the two-step `syscall.Sysctl` size/read API. They
retain Uname's single fixed-buffer read and partial-data error behavior. Numeric
MIB keys were checked against all five pinned upstream targets; injected tests
cover partial buffers, bounds and whitespace. Native execution is still required
to establish full cross-platform equivalence.

## Source locations

Mini uses the parsed declaration for a named function's source start and end.
The runtime entry line identifies the declaration, including same-named methods,
but can point at the closing brace when Go optimizes a test body away. If source
cannot be read or the declaration cannot be confirmed, Mini retains the
runtime start as its fallback. Anonymous functions retain their separate source
matching rules.

The pinned SDK uses the runtime entry line as the start of named functions.
For an optimized test declared on lines 5–10 it can therefore report 10–10.
Mini reports 5–10. Source regression tests assert the original file's lines
rather than relying only on equality with the SDK. The resolved range also
feeds impacted-test classification and process-retry metadata.
