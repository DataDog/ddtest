# Mini runtime configuration and API

Mini is the default CI Visibility runtime used by `ddto`. It retains the
SDK's testing policies and writes native CI events without importing the APM
tracer. Use the [README](../README.md) for installation and common commands.

`--runtime=sdk` selects the unchanged SDK at the version pinned in
[`internal/version`](../internal/version/version.go). Mini can coexist with an
application's SDK imports; those imports still contribute their normal build
dependencies. [SDK span copies](sdk-span-mirror.md) explain how the two runtimes
associate operations without changing APM parentage.

## Configuration

Set these variables in the environment of `ddto`, or of a compiled test binary:

| Variable | Behavior |
| --- | --- |
| `DD_CIVISIBILITY_ENABLED` | The CLI sets `parent` only when absent. Explicit values, including empty and `false`, are retained. Standalone test binaries need activation explicitly. |
| `DD_CIVISIBILITY_AGENTLESS_ENABLED` | Defaults to `false`. Set `true` to send directly to the CI intake. |
| `DD_API_KEY` | Required for Agentless HTTP delivery. Supply it through your secret configuration. |
| `DD_SITE` | Datadog site for Agentless endpoints; defaults to `datadoghq.com`. |
| `DD_TRACE_AGENT_URL` | Explicit Agent address, including supported Unix socket URLs. Without an override, host/port settings take precedence over the default socket, then `http://localhost:8126`. [Agent settings](../README.md#reporting-and-delivery) |
| `DD_SERVICE` | Explicit test service. A nonempty value takes precedence over automatic naming. |
| `DD_ENV`, `DD_VERSION`, `DD_TAGS` | Environment, service version and custom tags on CI events. |
| `DD_TEST_SESSION_NAME` | Explicit session name, including an explicitly empty value. Otherwise use the CI job name plus test command, or the command alone. The test command is the binary name and its arguments without `-test.v` and the paths that `go test` injects (`-test.gocoverdir`, `-test.testlogfile`, `-test.coverprofile`, `-test.outputdir` and `-test.fuzzcachedir`); see the [difference from the SDK](ci-parity.md#comparison-contract). |
| `DD_GIT_REPOSITORY_URL`, `DD_GIT_COMMIT_SHA` | Override Git identity. Both are needed to fetch settings when the checkout/CI environment cannot supply them. |
| `DD_TRACE_DEBUG` | Build and runtime diagnostics with phase and request timings. [Log guide](cli-debug.md) |
| `DD_CIVISIBILITY_LOGS_ENABLED` | Enable CI log delivery. |
| `DD_CIVISIBILITY_DEFERRED_DELIVERY` | Default `false`; send queued data between completed test groups when `true`. [Delivery contract](delivery.md) |
| `DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS` | Default `false`; derive package services when there is no explicit service. [Configuration](codeowners-service.md) |
| `DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS_FORMAT` | Default `service-$(owner)`; quote the literal token in shell commands. |

Feature requests determine retries, EFD, ITR, impacted tests, coverage upload
and test-management policies. Their environment overrides and combinations are
checked in the [feature matrix](ci-parity.md). Mini reads configuration from the
environment; APM YAML/Fleet configuration is excluded.

Each test binary initializes its own CI runtime, including settings, then
flushes at session close. A multi-package command can therefore have several
sessions and independent network startup/shutdown costs. Under `ddto test`,
test binaries report telemetry under the application of
[the Git worker](#the-git-worker). Settings
and initial telemetry overlap and finish before tests are admitted. Delivery
failures are logged without replacing the Go command's result.

## Runtime provisioning

An ordinary module needs no persistent runtime requirement. When the runtime
is absent, `ddto` supplies it through temporary copies of the effective
`go.mod` and `go.sum`, passed to Go with `-modfile`, and it respects explicit
modfiles and their overlays. Provisioning never edits the client's files,
although `-mod=mod` still lets Go's own package query make its usual edits.

Mini honors a client `replace` first. Otherwise it uses the source location
recorded when the CLI was built: a checkout or the exact cached module used by
`go install`. A checkout supplies its current contents, including edits made
after building the CLI. Keep the CLI and runtime at the same revision because
their private testing hooks must agree.

A [release executable](maintenance.md#releases) carries the runtime's sources
instead. It extracts them once per version into `ddto/runtimes` in its user
cache and uses that directory as it would use a checkout. The rules of the
[toolchain mirrors](#toolchains-in-the-module-cache) apply: a run holds the
directory, one unused for 14 days is removed, and a copy that misses a file is
extracted again. Without a usable user cache, such as one inside the project,
the run's temporary directory holds them until `go test` ends.

With `-trimpath`, or when no such source directory is usable, the CLI checks the
module cache for its exact published version. It fetches that version only when
no usable local copy exists. Required `go get` commands announce themselves and
stream Go's diagnostics on `stderr`. The CLI never searches other checkouts or
substitutes a different cached version. A development binary with neither
sources nor a published version needs a client replacement.

Mini requires Go 1.25 or newer, and preparation checks the toolchain that Go
selects. A client keeps its own language version: one declaring Go 1.21 keeps
that version's loop-variable semantics and timer defaults, and a module without
a `go` directive keeps Go 1.16 semantics and GODEBUG defaults, as under native
Go. For a client language older than Go 1.25, Mini becomes a separate main
module in a temporary workspace. The client's module files stay unchanged,
apart from the updates that its own `-mod=mod` imports would require under
native Go.

The temporary `go.work`, and the `GOFLAGS` adapted for it, apply only to the
`go test` command that `ddto` starts, including the build tools it runs or
chains, such as a user's `-toolexec`. In the test process, Mini's
`internal/goenv/restore` package restores the caller's `GOWORK` and `GOFLAGS`,
and with a [toolchain mirror](#toolchains-in-the-module-cache) the native
`GOROOT` and `PATH`. The package imports only `syscall`, through `goenv`, and
its path sorts before `os`, so Go initializes it before `os` and therefore
before any package that starts commands through `os/exec`, dependencies that do
not import `testing` included. Commands started by tests or by those
dependencies see the same module, workspace and flags as under native
`go test`. Go keys cached test results on its own environment, which holds the
temporary workspace, so each test binary also records a fingerprint of the
caller's values: a cached result is reused only for the same caller
environment.

One case escapes the restore. A dependency that imports `syscall` but not `os`
can initialize before the restore package when its import path sorts first; if
it reads `GOWORK`, `GOFLAGS`, `GOROOT` or `PATH` through `syscall` during
initialization, it sees `ddto`'s values. Ordinary code reads them through `os`,
and starts commands through `os/exec`, after the restore.

A `go test -exec` program, or the `go_$GOOS_$GOARCH_exec` helper that Go uses
when cross-compiling, starts before the test binary. `ddto` runs it through
`ddto test-exec`, which restores the caller's values first; Go already
disables result caching for such runs. `ddto` looks a program name up as Go
does, on the caller's `PATH` from Go's working directory. For a program that Go
could not run, or would run through a path relative to that directory, as
`GODEBUG=execerrdot=0` allows, `ddto` warns and runs native `go test`, which
reports or runs it. Arguments reach the program as Go parsed them, including an
unquoted argument with both quote characters. On Windows, where the wrapper
cannot replace itself, the program runs in a job that ends it if Go terminates
the wrapper on timeout, as Go would end the program itself. Processes that the
program leaves running after it exits are not ended.

Mini uses native Go 1.25 APIs. `internal/compat` contains only `AsType` and
`Pointer`, which adapt Go 1.26 helpers used by the incorporated sources. CI
checks Go 1.25, 1.26 and 1.27; scheduled and manual runs also build Go tip and
record its revision. The full SDK reference requires Go 1.26. Go 1.25 and tip
have a native Mini feature suite, and tip also checks manual SDK span copies.
The frozen Orchestrion reference fails on tip, so its complete differential
comparisons run on Go 1.26 and 1.27.

The SDK backend provides its pinned version with `go get`. A different selected
or replaced SDK is rejected. Mini's application-SDK integration has separate
[private-layout checks](sdk-span-mirror.md#maintenance-and-checks).

Mini's runtime, CLI and ported SDK test helpers use only the standard library
and this module, so the distributed module has no external requirements, test
requirements included. [Consumer checks](maintenance.md#verification-before-publication)
verify that `go mod tidy` adds no external modules and that old client
dependency versions are retained.

### Toolchains in the module cache

A toolchain that `GOTOOLCHAIN` downloads lives in `GOMODCACHE`, where Go rejects
every overlay entry, including the rewritten `testing` sources. `ddto test` then
runs go from a mirror of that toolchain in the user cache, under `ddto/goroots`.
The mirror has the same files, hard-linked where the filesystem allows and
copied otherwise. Windows always gets copies, because deleting a read-only hard
link there would clear the module cache file's attribute. Copies write the
bytes: on btrfs, a reflinked GOROOT made the next `go list` wait about 25 s.

The first run with a toolchain builds its mirror, in about 0.2 s with links and
a few seconds with copies. It also relinks its test binaries, because link
cache keys include GOROOT, while standard packages keep their compiled
archives. Gin's first run took 3.4 s with links and 6.8 s with copies. Later
runs took 1.0 s, the same as with a toolchain outside `GOMODCACHE`: they reuse
the mirror and check a few of its files. A mirror unused for 14 days is
removed, and a run holds a shared lock on the mirror it uses.

Mini's test runtime restores the `GOROOT` and `PATH` that tests see natively,
with the toolchain's `bin` first in `PATH`, so tests that run `go` get the same
toolchain. `runtime.GOROOT()` and standard-library paths in stack traces still
name the mirror, and tests of the SDK runtime see its `GOROOT` and `PATH`. A
launcher such as Orchestrion finds the mirror's `go` first in `PATH`, so
`ddto` passes it a `-exec` program by the path that `go test` would use. When
no mirror is possible, for example because the user cache lies in
`GOMODCACHE`, the toolchain, or the module or its checkout, `ddto test` warns
and runs `go test` without CI Visibility.

### The Git worker

On GitHub Actions without a numeric `JOB_CHECK_RUN_ID`, each test process would
read the runner's `Worker_*.log` files to find the job ID. `ddto` reads them
once while it prepares the build and hands the result to Mini's test processes
in `DDTO_GITHUB_JOB_ID`. The restore package removes that variable when it
restores `GOWORK` and `GOFLAGS`, before `os` initializes, so client code that
reads the environment through `os` never sees it. An empty value means the
files name no job, and the process does not read them again. `ddto` hands
nothing when a `-exec` program or a cross-compilation helper would see the
variable before the test binary, or when the scan is still running a second
after preparation; each process then reads the files itself.

Each test process also used to read the checkout's Git metadata, upload the
repository's missing commits and, with impacted tests, discover the base branch
and compute the diff. Now `ddto` starts a worker, `ddto ci-git-worker`, in the
invocation's directory while it prepares the build. The worker reads the Git
metadata before `go test` starts and hands it, through `DDTO_GIT_STATE`, to the
test processes of the same checkout, which then run no Git command for it.

When `go test` runs every test package, as with `-count=1`, the worker compares
the local commits with the backend's while `go test` builds; that request only
reads. Otherwise the worker waits until `go test` links a test binary or a test
process starts, so a run whose results all come from the cache sends no
request. Go links a test binary only for a result outside its test cache, and
`ddto` adds its tool to `go test` to tell the worker when that happens. Each of
those tool calls starts ddto's small wrapper first, about 1.2 ms
([phase 3](optimization-plan.md#phase-3-ddtos-startup-as-the-compiler-wrapper)).
A change that leaves a test binary identical, such as an edited comment, still
relinks it, so the worker sends its request. With `-o` or `-a`, which link even
for cached results, the worker waits for a test process instead.

Once a test process starts, the worker uploads the missing commits once and, if
the settings enable impacted tests, computes the base commit and the modified
files once. Test processes wait for those results where they used to wait for
their own work. When every result comes from Go's test cache, no test process
starts, and the worker neither changes the repository nor uploads.

`ddto` waits for the worker before it exits, as test processes waited for their
own uploads, for at most `--git-upload-timeout` (one minute by default) after
`go test` exits; test processes also wait at most that long for its result.
Then, or on a signal, `ddto` stops the worker. The worker interrupts its Git
command, so that Git removes its lock files, and it stops its requests, removes
its pack files, records the stopped upload and closes the telemetry
application.
Only a worker that does not stop within ten seconds is killed. `--keep-ci-state`
keeps the shared state for debugging; see
[CLI build diagnostics](cli-debug.md#keeping-the-ci-state). The exceptions for
the job ID apply here too, and test processes outside the worker's checkout, or
started outside `ddto test`, keep doing this work themselves.

The worker also owns the invocation's telemetry application. Test processes
that receive its state send their metrics and logs under the worker's runtime
ID, numbered in one sequence with its requests, and send neither `app-started`
nor `app-closing`. The worker sends `app-started` once a test process starts,
and `app-closing`, with its own metrics, after `go test` exits. It also logs,
as telemetry, its failures and the requests that test processes could not
deliver. A run whose results all come from the cache sends no telemetry.

When the worker starts its requests, it also makes the ones that each test
process makes before its first test: the settings and, as the settings enable
them, the known tests, skippable tests and test management data. It sends them
with the test processes' Go version and architecture, for each service that
their packages report. The read cache, scoped to the invocation for the worker
and its test processes, hands them the responses, so the first test starts
without waiting for the network: the requests overlap the link of its test
binary. A test process whose request differs makes its own. `ddto` also hands
each test package's module to the test processes, which then start no
`go list` for per-test coverage, LCOV reports or backfill.

### Vendor mode and workspaces

Mini can be supplied without a persistent requirement in these modes too. A
workspace run uses a temporary `go.work` with absolute `use` and local
replacement paths, and the effective Mini sources become a main module in that
workspace. Existing requirements and local or remote replacements keep their
selected sources; absent runtimes use the CLI sources or its exact published
version. The original workspace, checksums and module files stay unchanged.
When the workspace does not select Mini, a `use` module's own replacement of
Mini is read with `go mod edit -json`. Only module files that mention Mini's
path, or contain a backslash that could escape it, need that command; those
commands run concurrently, and their results apply in workspace order.

`ddto` uses a vendor directory only where Go would: with `-mod=vendor`, or by
default when the `go` directive is at least 1.14 and `modules.txt` was written
for the same mode. A `go mod vendor` tree beside `go.work` belongs to its module,
so workspace builds ignore it, and a `go work vendor` tree is ignored with
`GOWORK=off`, as `go test` does. Runtime provisioning applies the same rule.

For a module using `vendor`, a temporary workspace contains the client and Mini.
Go reads a workspace's vendor directory next to its `go.work`, and reads
`vendor/modules.txt` outside the overlay. That manifest is the only copied file:
a module manifest gains the workspace header. Go canonicalizes a module's
relative local replacement against the `go.work` directory, so those paths are
rewritten for the temporary workspace; replacements declared in the caller's
`go.work` become absolute, like that workspace's copy. Each manifest entry is
classified as Go looks it up: a `go.work` replacement of one version does not
affect other versions of that module.

Go splits manifest lines on whitespace, so a moved path that would contain the
spaces of the project's directory is replaced by a link inside the workspace,
such as `./replacements/0`, and the temporary `go.work` declares that link as
the module's replacement. Go reads `go.work` replacements before any `go.mod`
replacement, so when the link replaces every version, the temporary `go.work`
also declares the module's other versioned replacements, as the manifest
records them.

The client's vendor directories are recreated, and each file is a hard link to
the client's file, so no contents are copied and edits in place stay live. Go
sees regular files, as `//go:embed` requires: it rejects symbolic links. Real
directories keep vendored packages visible to wildcard patterns such as
`example.com/helper/...`; Go ignores linked directories there. Links inside the
client's vendor tree stay links. Like Go, `ddto` follows a `vendor` directory
that is itself a link. Where hard links fail, as when the user cache is on
another filesystem, files are copied with their times. Linking updates the
client file's change time, so Git may check its contents once. Alternate
modfiles keep working in any spelling, including `GOFLAGS`.

The workspace lives in the user cache, under `ddto/vendor-workspaces`, at a
path derived from its `go.work`, manifest, and the names, sizes and times of the
vendored files, which each run reads without reading file contents. Go's build
cache keys include each package directory, so this stable path lets unchanged
vendored packages reuse compiled archives between runs. A changed manifest or
workspace selects a new path, as does adding, removing or changing a vendored
file; the next run then compiles vendored packages again.

Each run checks that the workspace's files are still the client's files, or
copies with the same size and time, and that its links still point to the
client's entries. A run holds a shared lock on its workspace until `go test`
exits; waiting for that lock stops when preparation is canceled or interrupted.
When a new workspace is created, entries unused for 14 days are removed, but
only while their lock can be taken exclusively without waiting, so a workspace
in use is never removed. A run that waited while another removed its workspace
rebuilds it. Removing a workspace deletes only its own links and copies, and
deleting the directory by hand is safe when no `ddto` run is active.

Without a usable user cache, the links live in the run's temporary directory.
Where the client's own links cannot be recreated, as on Windows without the
symbolic-link privilege, the files they point to are hard-linked or copied
there instead. The original vendor tree is never edited. Native Go still
reports inconsistent vendor metadata instead of silently selecting other
versions, and an already vendored Mini can keep using its existing sources.

## Test context and propagation

`testopt.Context(t)` returns the active Mini context with native testing
cancellation and values. Each subtest has its own identity. Uninstrumented tests
and isolated process-retry children have no local Mini identity; the retry
controller reconstructs those children's test events.

```go
import (
    "net/http"
    "testing"

    "github.com/tonyredondo/dd-ci-testing-poc/propagation"
    "github.com/tonyredondo/dd-ci-testing-poc/testopt"
)

func TestRequest(t *testing.T) {
    headers := http.Header{}
    if identity, ok := propagation.FromContext(testopt.Context(t)); ok {
        if err := propagation.Inject(identity, headers, propagation.W3C); err != nil {
            t.Fatal(err)
        }
    }
    // Attach headers to the integration-test request.
}
```

W3C `traceparent`/`tracestate` and Datadog headers carry the full 128-bit trace ID
and active span ID. Generated span IDs use 63 bits and come from `math/rand/v2`,
as in the SDK, so they are unique but not secret. Sampling priority is
propagation metadata; Mini records every CI event.

A receiving APM tracer has its own private context value, so extract the
carrier with that tracer's API to use the identity. In a combined binary,
`t.Context()` also carries Mini identity when SDK mirror hooks are installed.
Passing it to `tracer.StartSpanFromContext` creates an independent CI copy while
the original APM span keeps its own trace; see
[SDK span association](sdk-span-mirror.md).

## Explicit clients and manual instrumentation

`testopt.New(Config)` creates an independent client with explicit service,
tags, transport, batch capacity and timeout. It does not read API credentials
from the environment. `Client.StartSpan` accepts a context and event options;
`ResourceName`, `SpanType`, `StartTime` and `Tag` set its fields. Numeric tags
become metrics, and errors retain the SDK's tag semantics.

`Finish` seals a span once. Setters then become no-ops, getters remain available
and repeated finishes do not enqueue again. `Flush` and `Close` return delivery
errors; `LastError` and `DroppedEvents` expose delivery state. A failed flush
keeps its batch while the client is open, and a final close can abandon the
batch and report that failure. The caller owns an explicit client's lifetime
and must close it.
[Event ownership](architecture.md#native-event-ownership) documents the limits.

For manual testing entrypoints, `testopt.RunM(m)` instruments `TestMain`, and
`testopt.GetFuzz(f).Fuzz(callback)` wraps a native fuzz callback. Automatic
builds recognize these hooks and avoid duplicate events;
[manual fuzz usage](fuzz-examples.md#manual-entrypoints) shows the code. The API
is experimental and can change before a release.

## Delivery and offline output

Test-cycle events use MessagePack envelope version 1, test-event version 2,
and session/module/suite version 1. Batches have a 5 MiB uncompressed limit and
a 2.5 MiB flush threshold. Agentless delivery uses standard gzip at
`gzip.BestSpeed`; Agent delivery uses the EVP v2 proxy. Coverage and CI telemetry
use their own SDK-derived writers. [Delivery](delivery.md) explains retries,
queue bounds, deferred groups, leak checks and payload metadata.

Bazel payload-file mode writes JSON under
`TEST_UNDECLARED_OUTPUTS_DIR/payloads/tests/`, with corresponding coverage and
telemetry directories. Manifest mode uses the SDK's offline read cache. The
test-cycle file writer needs no HTTP credentials and sends no event requests;
the settings-cache reader still requires a nonempty API key. The offline
comparison uses a synthetic key and checks that no HTTP request is sent. It
checks the file contract; no real Bazel build runs.

Mini retains CI telemetry and diagnostics. It excludes APM sampling, profiling,
security, remote configuration, heartbeats and dependency/endpoint inventories.
A telemetry URL containing `apmtelemetry` is shared infrastructure and does not
mean Mini has started an APM tracer.

## Source locations and compatibility

Mini uses a confirmed named function's parsed declaration for its source range.
The runtime entry line identifies the function but can point at its closing
brace after optimization. If source cannot be read or matched, that runtime line
remains the fallback. Anonymous functions have separate matching rules.

The pinned SDK can report an optimized test declared on lines 5–10 as 10–10;
Mini reports 5–10. Regression tests check original source lines directly.
Cleanup-only coverage and duplicate Testify method identity also have direct
Mini regression tests. The [comparison contract](ci-parity.md#comparison-contract)
records these deliberate differences and its normalization rules.

Per-test coverage, in count or atomic mode, reads the coverage counters in
memory through a hook that `ddto` adds to `testing`. Each test's covered lines
are the same as with the two text profiles per test that the SDK writes and
reads back. Without those, Gin's packages with `-coverpkg=./...
-covermode=atomic` run in 0.55 s instead of 6.3 s at 4 CPUs, and test
processes print native go test's `coverage:` lines instead of one per capture.
The [optimization plan](optimization-plan.md#phase-2-per-test-coverage-in-memory-item-8)
has the measurements.

Text profiles stay in some cases:

- the toolchain or the selected Mini lacks what the hook uses;
- the test binary links `plugin`;
- the coverage selection could instrument a standard or Mini package, such
  as `-coverpkg=testing`;
- Mini finds blocks whose text-profile result is ambiguous.

The [adaptation record](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md#per-test-coverage-counters-in-memory)
lists every case.

When `-coverpkg` covers `testing` itself, coverage reports lines of ddto's
rewritten `testing.go`: `go tool cover` ignores `//line` directives, so lines
after ddto's hooks appear shifted, in per-test coverage and in the
`-coverprofile` that `go test` writes. Stack traces keep the original lines,
because the compiler applies the directives. The coverage of `testing`'s own
code is also attributed by timing: `T.Run` continues in the parent goroutine
while the attempt it started takes its first snapshot.

With `-race`, `ddto` overlays Mini's sources with copies that mark each
function declaration `//go:norace`, so the race detector does not instrument
Mini's own memory accesses: with 2,000 empty tests, about 22 µs less per test.
Client code and `testing` stay instrumented, and Mini's locks, channels and
atomics still order client accesses, so client races are reported as before.
Races inside Mini's functions are not, except in function literals, which cannot
be annotated and stay instrumented. A `/*line*/` directive keeps every source
position.

Some of Mini stays fully instrumented. Packages that `-coverpkg` covers do,
because Go's cover tool reads their original sources. When client code imports
Mini's packages, directly or through a helper package, as tests that use
`testopt` do, Mini keeps full instrumentation, so memory that the client passes
to Mini's API, such as a pointer tag value, stays checked as under
`go test -race`. Mini's own tests also keep it, with `go test -race` or when the
named packages belong to Mini. Compiling Mini with `-race=false` would remove
more, but the compiler would then make Mini's atomics invisible to the
detector, which would report races that do not exist.

The [validation guide](validation.md) lists loopback wire checks, imported SDK
assertions, concurrency and platform coverage. Workflow results are evidence
for their exact inputs only. Live intake/UI acceptance, real Bazel execution
and long-running fuzz campaigns need separate validation. The
[benchmark report](benchmarks.md) retains timings for its recorded revision.

For source revisions, licenses, codecs, platform subsets and update commands,
read [source maintenance](maintenance.md) and the
[incorporated-source records](../internal/thirdparty/README.md).
