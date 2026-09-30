# Validation contract

The runtime integration is the unchanged public dd-trace-go SDK, pinned to
[v2.11.0-rc.1](https://github.com/DataDog/dd-trace-go/tree/v2.11.0-rc.1).
The reference is Orchestrion commit
[5c24783fcd76](https://github.com/DataDog/orchestrion/commit/5c24783fcd76f00cd1ff21c418a6662785d6c811),
installed as `v1.13.2-0.20260917114356-5c24783fcd76`.

## Architecture and dependency boundary

The driver has no module dependencies. It performs one targeted `go list`,
parses native `testing` sources with `go/parser`, and writes an overlay containing
only changed standard-library files, the SDK linkname declarations, and an
external SDK import for each selected test package. Native Go performs the build.
There is no configuration loader, daemon, nested dependency build, or per-compiler
wrapper in the POC.

The nine SDK aspects are retained: M.Run, T.Run, B.Run, Fail, FailNow, formatted
errors, formatted skips, SkipNow and Parallel. Formatting wraps the already
formatted result, so String methods run once. The SDK's ownership marker retains
its existing name. Runtime retries, skip policies and finalization are owned by
the SDK. This POC depends on its private hook ABI and is intentionally version-pinned.

Virtual external test files anchor the public SDK import without editing project
sources. Temporary plans are invocation-local and removed after Go exits. There
is no additional persistent instrumentation cache. Test-result caching remains
Go's choice: explicitly select packages for result caching, and use `-count=1`
when fresh CI events are required. `-work` preserves Go's own work directory, but
the POC overlay is still removed; this POC does not provide retained overlay debugging.

## Runtime checks

The tests compile actual native, POC and Orchestrion binaries using the same
fixture and temporary module graph. Adding Orchestrion may upgrade transitive
modules through Go MVS; that graph is used by both instrumented variants, and
an assertion verifies the SDK remains the pinned release. The reference YAML is
read directly from the installed SDK. No SDK source is copied into this repository.

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
| Benchmark, Example and fuzz seed | Native execution and reference SDK event semantics; no full fuzz campaign |
| Process retries | First attempt fails, second passes; reference events and process exit |
| EFD, ITR, disabled, quarantine, attempt-to-fix | Real SDK requests against loopback policy responses and reference event equivalence |
| Panic, Goexit and timeout | Abnormal exit and diagnostic marker; enabled/disabled reference event equivalence |
| Unsupported input | Missing/ambiguous hooks, malformed source, double instrumentation, conflicting toolexec and ambiguous arguments rejected |

The backend responses are synthetic, but hooks, retry processes, serialization
and network requests come from the real SDK. Fixtures inherit a small whitelist
of tool/path environment variables; real API keys and CI credentials are excluded.
Events are captured over loopback using a synthetic key.

The comparison retains event type, name, resource, error flag, semantic test
attributes, error stacks and source positions. It excludes generated IDs,
durations, timestamps, execution order and invocation names. It does not prove
identity of binaries, generated source bytes, or the event ID parent graph.
Parallel child completion order and elapsed times may differ in native output;
line content and multiplicity are retained.

## Scope limits

Supported targets are module packages with the exact unreplaced SDK. Standard
library test targets, explicit Go file mode, `-C`, custom flags before `-args`,
testify/suite instrumentation and other APM integrations are outside the POC.
The AST transformer validates hook presence and ambiguity and selected shape
constraints; future Go source/ABI changes still require a new compatibility run.
Go 1.26 and 1.27 are selected from the current [official releases](https://go.dev/dl/).

The workflow is the source of evidence for the actual Linux/macOS/Windows and Go
versions it tests. This document describes its coverage, not an unconditional
compatibility guarantee. The local workstation uses a customized Go 1.27 toolchain.
Application-scale performance, CPU accounting for the full process tree, aggregate
peak memory, full fuzzing and arbitrary downstream modules remain unmeasured.
