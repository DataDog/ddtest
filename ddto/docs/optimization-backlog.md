# Optimization backlog

These optimizations were identified in the 2026-10-06 review of `main`. They are
deliberately deferred: correctness work comes first, and each item below needs
its own design and validation. Measurements used Linux, Go 1.27.0, a local fake
Agent and a fixture of trivial subtests; see [how to measure](#how-to-measure).

## 1. Compute per-invocation data once in the CLI

Every test binary starts CI Visibility on its own. In `go test ./...` that work
repeats once per package, although the results are the same for all of them.

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

Expected gain: roughly the startup overhead times the number of packages, plus
one round trip per avoided request. Risks: stale data if an invocation spans
commits or services, and differences between manifest and network modes.

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
