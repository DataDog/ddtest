# Optimization backlog

These optimizations were identified in the 2026-10-06 review of `main`. They are
deliberately deferred: correctness work comes first, and each item below needs
its own design and validation. Measurements used Linux, Go 1.27.0, a local fake
Agent and a fixture of trivial subtests; see [how to measure](#how-to-measure).

## 1. Compute per-invocation data once in the CLI

Every test binary starts CI Visibility on its own. In `go test ./...` much of
that work repeats once per package. Some inputs can differ across modules or
services, so sharing must use the effective request configuration.

- **Git metadata.** Startup runs four `git` subprocesses in sequence:
  `rev-parse --show-toplevel`, `ls-remote --get-url`,
  `rev-parse --abbrev-ref HEAD` and `log -1`. They account for most of Mini's
  startup overhead (about 12 ms per binary against 3 ms native; the SDK takes
  about 49 ms). `ddtest` could run them once and pass the results to every
  binary, for example through the `DD_GIT_*` variables the SDK already honors.
  Check which fields still trigger a subprocess when those variables are set.
- **Backend requests.** Each binary requests settings and, depending on the
  enabled features, known tests, test management data and skippable tests. Each
  costs one round trip; against the remote intake, this dominates small
  packages. `ddtest` could fetch them once and point the binaries at the
  incorporated manifest mode (the Bazel path), which reads these inputs from
  files. Validate that the manifest data is identical for all packages of one
  invocation and that results still upload through the network.

### Shared settings and initial telemetry

Investigate having `ddtest` coordinate settings and initial feature requests
before running package binaries. The same design could coordinate the initial
telemetry `app-started` requests. This is a backlog item; the runtime currently
owns those requests and its application lifecycle.

A macOS/arm64 observation on Go 1.27.1 with two trivial test packages showed
`app-started` taking 422–431 ms per binary. Settings initialization overlapped
with that request and waited for it. This identifies startup network latency as
a candidate, but one run does not establish a saving. Package starts also
already overlap under `go test`.

The experiment must preserve:

- Settings keys: repository URL, commit, service, environment and all effective
  test configurations. Share only matching requests within one invocation;
  keep independent invocations and different module configurations separate.
- Feature responses, errors and fallback behavior for ITR, retries, EFD,
  impacted tests and test management. Reusing manifest inputs must still allow
  network delivery of events.
- Telemetry runtime IDs and application lifecycle. One `app-started` per CLI
  would change today's per-process identity; define how parent/child identity,
  configurations, metrics, timestamps, request sequence and `app-closing` work
  before reducing that count. Preserve child telemetry throughout the experiment.
- Session, module, suite, test and span event counts and hierarchy. Shared
  initialization does not combine their sessions.
- Compile-only `go test -c` behavior: building must not start CI sessions or send
  runtime telemetry. Standalone binaries, process retries and interrupted
  parents must still initialize safely without a live CLI coordinator.
- Deferred delivery and goleak admission: initial HTTP work must finish before
  tests begin. Any shared state must have invocation-scoped ownership and
  cleanup, without secrets in logs or persistent caches.

Measure first-test admission and complete-command wall time across one and many
packages, with both local and delayed HTTP receivers. Compare cold and cached
builds separately. Check telemetry and feature parity before claiming a saving.
Remote requests can dominate a trivial package, but sharing only helps the
command's elapsed time when it removes work from its critical path.

## 2. Cache the provided runtime

When the module does not require the selected runtime, `ddtest` prepares a
temporary `go.mod` and runs a second full `go list`. Mini uses local sources or
its exact cached version when available. The SDK, uncached Mini versions and
versioned client replacements use `go get`, whose metadata queries can still
need the proxy even with cached downloads. The CLI streams that command's
diagnostics to `stderr`.

- Cache the resulting `go.mod`/`go.sum` in the user cache directory (outside the
  repository), keyed by the module's `go.mod` and `go.sum` contents, the runtime,
  its version and the Go version.
- Provisioning queries `GOMOD`, `GOWORK` and `GOMODCACHE` together with `go env -json`.
  Avoiding that remaining command would require obtaining both values from
  information already collected during preparation.
- List only the runtime package with the provided modfile instead of repeating
  the full package list.

## 3. Deeper per-test changes

After the per-test allocation work in the SDK port, Mini adds about 44
allocations per test to `testing`'s own 17.

- **Span options as values.** `StartSpanOption` is a closure, so every
  `ResourceName`, `SpanType`, `StartTime` and `Tag` allocates. A value type
  would save about five allocations per test.
- **Metrics without a map.** Most events carry a few metrics; a small slice
  instead of a map would save about two allocations per test. The MessagePack
  encoding must stay identical.
- **Per-test metadata in `testing.T`.** Instrumentation stores per-test metadata
  in a global `sync.Map`, with one store and one delete per test. Under
  `t.Parallel` this shows measurable contention. The `testing` overlay could add
  a field to `testing.common` instead.
- **CI metric options.** Numeric CI metrics build new options for every span by
  design ("fresh per call"); caching them by revision needs the same decision.

## 4. Coverage per test

Per-test coverage writes and parses a complete coverage profile of the package
for every test, and `InitializeCoverage` runs `go list` as a subprocess in every
binary. The CLI could pass the module path and directory. In-memory counter
snapshots would need a hook in a standard-library overlay; this is the largest
remaining cost when per-test coverage is enabled.

## How to measure

1. Build a fixture with one test that starts `N` trivial subtests (and a
   parallel variant), and compile it with `ddtest test --runtime=mini -c`.
2. Run it against a local HTTP receiver that answers settings with
   `Content-Type: application/json` and accepts test-cycle payloads; add latency
   to the test-cycle endpoint to emulate the remote intake.
3. Compare wall time against the native binary, and use `-test.memprofile` with
   `-test.memprofilerate=1` for exact allocation counts per subtest.
4. For startup, run the binary with one subtest and count subprocesses by putting
   logging wrappers for `git` and `go` first in `PATH`.

## Mini provisioning at module roots

When the effective module file does not mention Mini, the CLI probes module
selection using temporary module and checksum files. If Go reports that Mini
is unknown, provisioning precedes the full package query. The query then uses
the final module, so it observes dependency-version changes caused by adding
the runtime. A declared Mini keeps the ordinary single-query path.

The probe is conservative. Subdirectory runs without an explicit modfile,
vendor mode, escaped module text and files that mention Mini use normal package
resolution. Workspace and transitive selections remain Go's decision. `-mod=mod`
also retains native package resolution before runtime provisioning, preserving
Go's own authorized module edits. Review these guards before extending the
optimization; a filename or missing direct `require` does not prove that the
runtime is unavailable.

Checks: `TestPreprovideMiniRespectsEffectiveModuleAndWorkspace`,
`TestCLIDebugEarlyLocalProvisioning`, module-overlay and `-mod=mod` regressions.
Compare Mini already required, a client replacement and CLI-local provisioning;
preparation gains must not add queries to the common required-runtime path.
