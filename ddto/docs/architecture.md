# Architecture

The project has two jobs: insert the SDK's testing hooks during a native Go
build, and offer a small runtime that writes CI Visibility events. Separating
them lets us compare instrumentation with the full SDK and with Mini while
keeping the same testing entry points.

The CLI defaults to `sdk`. That backend requires the exact, unreplaced SDK
version in [`internal/version`](../internal/version/version.go). The `mini`
backend links this module's CI runtime, which imports no external module. The
module requires only Testify v1.7.5 for its own tests, deliberately old so a
consumer's newer Testify never changes.

## Build time and runtime

The CLI finishes preparing the overlay before Go starts compiling. The resulting
test binary contains the testing hooks and selected runtime; running that binary
does not need the CLI.

```mermaid
flowchart TB
    subgraph build["Build time"]
        CLI["ddtest test: choose<br/>sdk or mini"] --> Plan["Resolve packages and<br/>prepare overlay"]
        Plan --> Selection{"Testify, Mini goleak or<br/>covered testing sources?"}
        Selection -->|Yes| Tools["Selective<br/>compiler/coverage<br/>wrapper"]
        Selection -->|No| Go["Native go test:<br/>compile and link"]
        Tools --> Go
    end
    Go --> Binary["Instrumented test<br/>binary"]
    subgraph execution["When the binary runs"]
        Binary --> Testing["testing entry points"]
        Testing --> Hooks["CI testing hooks and<br/>policies"]
        Hooks --> Runtime["SDK or Mini, fixed<br/>when the binary was<br/>built"]
        Runtime --> Output["CI event delivery"]
    end
```

Orchestrion is the reference instrumenter in differential tests. The POC
implements only its testing advice. Its front-end prepares one overlay per
invocation, so compiler processes do not each load an instrumentation engine.

| Code | Owns |
| --- | --- |
| [`cmd/ddtest`](../cmd/ddtest/main.go) | CLI entry point, runtime selection, activation and interrupt handling |
| [`internal/runner`](../internal/runner/run.go) | Go flags, package discovery, overlay merging, temporary files and child exit status |
| [`internal/instrument`](../internal/instrument/transform.go) | Validation and source edits for native `testing` and the original Testify suite entry |
| [Extracted `gotesting`](../internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting) | Test callbacks, parallel ownership, retries and CI policies |
| [`internal/minitracer`](../internal/minitracer/README.md) | Event fields, identities, byte accounting and batching |
| [`internal/citransport`](../internal/citransport/transport.go) | Test-cycle delivery, retries, compression and request-body lifetime |
| [`propagation`](../propagation/context.go) and [`testopt`](../testopt/testopt.go) | Public context exchange and native-client API |
| [`internal/thirdparty`](../internal/thirdparty/README.md) | Maintained SDK, codec and platform subsets with source provenance |

## Preparing an overlay

An overlay maps a logical source path to a backing file. Go reads that backing
file during its build. The original files remain in their project, SDK or
toolchain directories.

```mermaid
sequenceDiagram
    participant CLI as ddtest / runner
    participant Go as Go tool
    participant AST as Transformer
    participant Plan as Overlay files
    CLI->>CLI: Parse flags and apply activation default
    CLI->>Go: Targeted go list for testing, runtime and client packages
    Go-->>CLI: Package metadata and test imports
    opt Library reachable or unknown test imports need resolution
        CLI->>Go: go list -find for known libraries, otherwise -deps
        Go-->>CLI: Testify and Mini goleak source and module metadata
        CLI->>AST: Validate versions and APIs
        CLI->>AST: Prepare library entry hooks
        AST-->>CLI: Source edits, hooks and content fingerprints
    end
    CLI->>AST: Effective testing sources
    AST-->>CLI: Validated source edits
    CLI->>Plan: Hooks, imports and overlay JSON
    CLI->>Go: go test -overlay=plan, with selective tools when needed
    Note over Go: Native build and cache decisions
    Go-->>CLI: Output and exit status
    CLI->>Plan: Remove files after Go exits
```

The transformer parses Go's `testing` package and applies edits at source
positions. It returns only changed files and retains logical filenames through
line directives. A generated file declares the private SDK hooks with
`go:linkname`. For Mini, when testing declares its parallel-test counter, the
file also registers the function that in-process retries use to record the end
of a parallel attempt; nothing outside testing can reach that counter. Each
selected package with tests receives a virtual external test file that
blank-imports the chosen runtime.

Existing overlays are merged before transformation, and every user `-overlay`
spelling is replaced by the merged plan. Generated-path collisions, missing or
ambiguous hooks and already instrumented sources fail during preparation. The
[flag parser](../internal/runner/options.go) follows `go test`'s algorithm: GOFLAGS
first, `-C` only as the first flag, `--flag` spellings, and unknown flags or
everything after `-args`/`--` for the test binary. `TestFlagTableMatchesToolchain`
compares its flag table with the toolchain's help. When the plan needs the
selective tool, a user `-toolexec` (from arguments or GOFLAGS) runs every tool
after ours. Help and explicit Go-file mode run native `go test`.

[Testify preparation](testify.md) detects the suite package in the actual test
import graph and prepares a registration call at its original `Run` entry.
One private `-toolexec` hook substitutes compiler inputs for that package,
including covered sources. It also bridges covered rewritten `testing` sources
when needed. Unrelated calls bypass plan loading and replace the wrapper with
the native tool on Unix. Mini also prepares a reachable goleak `Find` entry;
the same graph lookup and wrapper serve both libraries. Builds needing none
of these features omit `-toolexec`.
The suite fingerprint travels through `testing` export data; compiler and
linker identities remain native, so unrelated packages share Go's cache. The
selected version and API are checked before Go can reuse a cached suite. A
coverage bridge has its own version contract. Its dispatch, cache invalidation
and source ownership are described in [Testify instrumentation](testify.md).
Goleak's package-only cache marker, delivery pause and exact worker filters are
described in [delivery and goleak](delivery.md).

The plan lives for one invocation. Identical generated content shares a backing
file within that plan. Go still owns its build cache and test-result cache; use
`-count=1` when a test must run again and emit fresh events. With `-c`, Go builds
the instrumented binary and leaves execution to the caller. `-work` preserves
Go's work directory, but the CLI still removes its own overlay plan.
Interrupt, termination and hangup signals are forwarded to Go instead of
ending the CLI first. The plan is removed after Go exits, and `ddtest` exits
with Go's status; if a signal terminated Go, `ddtest` re-raises it.

## The testing boundary

The advice covers `M.Run`, `T.Run`, `B.Run`, failure methods, formatted errors,
formatted skips, `SkipNow` and `Parallel`. The ownership marker keeps the SDK's
original Orchestrion name because the runtime uses that symbol to recognize
woven testing code.

The extracted runtime owns retries, early flake detection (EFD), skipping through
the Intelligent Test Runner (ITR), quarantine and attempt-to-fix decisions. The
[feature inventory](ci-parity.md#feature-inventory) describes these policies.
The CLI forwards test arguments and starts the build. Formatted error and skip
hooks receive the already formatted text, so an argument's `String` method runs
once.

These hooks are a private ABI. Their signatures, native `testing` layouts and
source locations must be reviewed when Go or the SDK changes. A successful AST
match alone cannot establish compatibility with a new toolchain.

## Native event ownership

CI session, module, suite and test identifiers describe the CI hierarchy.
The edges below mean "contains":

```mermaid
flowchart TB
    Session["Test session"] --> Module["Test module"]
    Module --> SuiteA["Test suite A"]
    Module --> SuiteB["Test suite B"]
    SuiteA --> TestA["Test A"]
    SuiteA --> TestB["Test B"]
    SuiteB --> TestC["Test C"]
```

Distributed trace identity is separate: the propagation package carries a
128-bit trace ID and active span ID. Test-cycle serialization writes hierarchy
IDs as native fields, with the SDK's event-specific ID rules. End events identify
their session, module or suite; test events retain their own trace identity.
Retries and parallel-test ownership are managed by the extracted testing hooks,
which also control when a session closes and flushes.

For a span with a client, the lifetime is:

```mermaid
stateDiagram-v2
    [*] --> Mutable
    Mutable --> Mutable: SetTag or SetError
    Mutable --> Sealed: First Finish
    Sealed --> Queued: Client accepts event
    Sealed --> Waiting: Pending bound while delivery succeeds
    Waiting --> Queued: A sender frees space
    Waiting --> Rejected: Delivery fails or client closes
    Sealed --> Rejected: Client rejects event
    Queued --> Sending: Flush or batch threshold
    Sending --> Queued: Failure while client is open
    Sending --> Abandoned: Delivery fails on Close
    Queued --> Abandoned: Cannot send on Close
    Sending --> Delivered: Delivery succeeds
    Rejected --> [*]
    Delivered --> [*]
    Abandoned --> [*]
```

`Finish` seals the span under its mutex. It copies the event's scalar fields and
shares the private metadata and metric maps with the event. Setters then become
no-ops; getters remain usable. Repeated `Finish` calls do not enqueue again.
A span without a client can still carry context, but it is not queued.

CI/Git/system strings have a separate immutable snapshot. Spans store local
overrides and read the snapshot through their getters. Delivery projects shared
values into event-kind metadata without changing either map. Mixed snapshots
or numeric overrides use local strings for the affected kind/key. The
[metadata diagram and rules](delivery.md#payload-level-common-metadata) explain
that boundary; ordinary child spans receive no CI defaults.

The client has two synchronization boundaries. `mu` protects the open batch,
sealed batches, sender, closure, error and drop state. `sendMu` is a
context-aware token that serializes explicit flushes and idle-checkpoint
deliveries, including ownership of their payload buffer; background senders and
the additional deferred-mode senders encode into their own reusable buffers.
`Finish` never takes the token and never
performs network I/O: it appends to the open batch and seals it once it reaches
the event-count or byte threshold.

In ordinary mode, up to eight background senders deliver sealed batches
concurrently, like the SDK's concurrent flushes, each bounded by
`FlushTimeout`. At most eight sealed batches, including those in flight, plus
the open batch are retained. A finisher that reaches that bound waits for a
sender while the intake accepts payloads, so a slow intake loses no events.
After a failed delivery, until the next success, it is rejected and counted
instead, so an unavailable intake cannot stall tests; a blackholed one can delay
finishers at the bound until the first delivery fails, at most `FlushTimeout`
(ten seconds by default). A failed
batch returns to the front of the queue, and background retries back off from
one to ten seconds; any successful delivery clears the backoff and the failure
state. Deferred mode starts no background sender and never waits: idle
checkpoints deliver sealed batches, up to eight at once, and finish every send
before the next test starts; the open batch waits for `Close` or an explicit
`Flush`. Its pending queue can exceed the bound, while each outgoing payload
keeps the intake limits.

Ordinary CI telemetry updates use bound handles for their existing tag
combinations. The global swappable handle retains startup replay and follows
client replacement; a test registry reset invalidates the binding. A counter's
value and timestamp stay together under a short mutex, and collection detaches
that point before encoding. The [SDK adaptation record](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md)
describes those lifetimes and the checks needed for an upstream update.

The default event-count limit is 1,000. Byte accounting includes the envelope
and uses generated `Msgsize()` upper bounds plus shared-tag/envelope accounting.
The flush threshold is 2.5 MiB and
the maximum uncompressed test-cycle payload is 5 MiB. A single event that cannot
fit is rejected. Small batches flush at explicit or CI lifecycle boundaries;
the Mini client has no periodic flush worker.

`Close` prevents new events, performs a final flush and releases idle HTTP
connections. Explicit clients report errors through `Flush`, `Close`,
`LastError` and `DroppedEvents`. Hook-driven shutdown logs failures while
preserving the test result. A final delivery failure abandons the pending batch,
releases its events and increments `endpoint_payload.dropped` once. Transport
attempts, event count and rejected events do not multiply that payload counter.
`DroppedEvents` separately includes rejected events and those in abandoned
batches. Repeated close/flush calls cannot resend or recount an abandoned batch.

## Delivery and compression

Test-cycle events use the native client and transport. Coverage and CI telemetry
keep their extracted writers and endpoints. They are separate payload streams;
changing `citransport` does not update those writers.

```mermaid
sequenceDiagram
    participant Batch as Mini batch
    participant Transport
    participant Gzip as Gzip pool
    participant HTTP as HTTP client
    Batch->>Batch: Encode into reusable payload
    Batch->>Transport: Send immutable bytes
    alt Bazel payload-file mode
        Transport->>Transport: Write JSON test payload
    else HTTP delivery
        opt Agentless
            Transport->>Gzip: Acquire, reset and compress
            Gzip-->>Transport: Compressed buffer
        end
        loop Retry while allowed
            Transport->>HTTP: POST with owned reader
            HTTP-->>Transport: Response or error
        end
        Transport->>Transport: Seal all request readers
        opt Agentless
            Transport->>Gzip: Return compressor
        end
    end
    Transport-->>Batch: Delivery result
    Batch->>Batch: Reuse queue or restore failed batch
```

Agentless delivery uses gzip and `/api/v2/citestcycle`. Agent delivery uses
`/evp_proxy/v2/api/v2/citestcycle` and the EVP subdomain header, with uncompressed
MessagePack. Bazel file mode writes JSON under
`TEST_UNDECLARED_OUTPUTS_DIR/payloads/tests/`. Coverage and telemetry use their
corresponding output directories.

HTTP retries reuse the same immutable bytes. The defaults are three attempts,
100 ms initial exponential backoff and a 10-second HTTP timeout. Network errors,
429 and 5xx responses are retryable. Other non-success responses stop delivery;
429 can provide an integer `Retry-After` between zero and 60 seconds. Context
cancellation bounds requests and backoff. Redirects are refused. As in the SDK,
errors keep their cause: a network failure wraps the `net/http` error, and an
HTTP failure reports up to 1000 bytes of the response with its status.

An HTTP implementation may keep reading after `Do` returns. Each reader shares
an owner lock; sealing that owner waits for an active read and makes subsequent
reads return EOF. The payload or gzip buffer can then be reused safely. Delivery
can repeat after an ambiguous network failure, so consumers must not assume
exactly-once transport.

## Propagation into an integration test

`testopt.Context(t)` exposes the active test context. An instrumented test can
read it with `propagation.FromContext`, then inject W3C or Datadog headers into
an outbound request.

```mermaid
flowchart LR
    Test["Test"] --> Context["Active Mini context"]
    Context --> Headers["W3C or Datadog<br/>headers"]
    Headers -->|HTTP| Extract["APM extracts parent"]
    Extract --> Span["Service span"]
```

Inside the same process, another tracer must explicitly extract the Mini
identity through its own API or through a header carrier. Its private
`context.Context` value is different. A future dd-trace-go helper could expose that bridge;
the current API provides context values and header carriers. Sampling priority is carried as propagation
metadata; Mini has no APM sampling engine.

## Runtime scope

Mini retains CI policies, coverage, Git/CI metadata, diagnostic logs and CI
telemetry. It reads the supported configuration from environment variables.
APM YAML/Fleet configuration, telemetry heartbeats, dependency inventories,
profiling, security and the general APM tracer are excluded from the port.

Public-intake acceptance, actual Bazel toolchain execution, full fuzz campaigns
and arbitrary downstream projects require separate evidence. See the
[validation contract](validation.md) for exercised cases and
[maintenance guide](maintenance.md) for upgrade requirements.
