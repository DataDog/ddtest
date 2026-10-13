# Optimization plan

This plan was written on 2026-10-13 from the benchmark refresh at `137a912`
([#39](https://github.com/tonyredondo/dd-ci-testing-poc/pull/39)). It covers
six workstreams: the GOROOT mirror and the five costs that
[the comparison](benchmarks.md) shows for Mini against Native. Each phase is
its own branch and pull request. Items keep their
[backlog](optimization-backlog.md) numbers where they have one.

## Summary

Gains are estimates at 4 CPUs from the 2026-10-13 data. Each phase measures
its own before deciding.

| Phase | Work | Evidence | Expected gain | Size |
| --- | --- | --- | --- | --- |
| 1 | GOROOT mirror for toolchains in `GOMODCACHE` | `ddto test` aborts after a `GOTOOLCHAIN` switch | A failure becomes a working run | Medium |
| 2 | Per-test coverage in memory (item 8) | Gin `-coverpkg=./... -covermode=atomic`: 0.25 → 6.5 s, 0.39 → 13.6 CPU-s | About 6 s of Gin's 6.5 s | High |
| 3 | ddto's startup as the compiler wrapper | 1.4 → 2.4 ms per start since #32–#34; +2.1% cold, +3.4% relink in Testify | 2–3% of wrapped builds | Low to medium |
| 4 | Mini's package graph in builds | Cold +2.5–3.0 s (+14–44%); Gin forced link +0.9 s | Part of those seconds | Medium to high |
| 5 | Preparation per invocation | Cached builds +50–110 ms; preparation about 50 ms | Up to about 50 ms per invocation | Medium; item 56 needs a decision |
| 6 | Test binary startup and memory | Testify binary 2.7 → 9.5 ms; Gin runtime memory 36.6 → 50.9 MiB | Depends on the profile | Medium |
| 7 | Benchmark refresh and backlog | — | Published, comparable data | Low |

Order: phase 1 was approved first and fixes a failure. Phase 2 is the largest
gain. Phase 3 reverses a regression and is small. Phases 4–6 start with
investigation and end with a decision.

## Rules for every phase

- **Branch and merge.** One branch and pull request per phase. Squash-merge
  only when the user asks, with a custom subject and body and no attribution
  lines.
- **Measurement.** Measure before and after with an interleaved A/B on the
  benchmark host. Build each side from its own worktree: ddto compiles Mini from
  its own checkout, and the benchmark subjects replace Mini with that checkout.
  Report medians and ranges. Never attribute a difference between collection
  days to code without an A/B.
- **Profiling.** Profile the chosen workload before designing: CPU, and
  allocations or heap where memory matters. Record the profile breakdown in the
  pull request.
- **Tests.**
  - Unit tests, plus integration tests for anything the CLI or the runtime does
    end to end.
  - Check every guard by mutation.
  - No flaky tests: reproduce failures deterministically and fix the cause.
  - Do not edit a worktree while a suite runs from it.
- **Verification.** The full suite on the final head: race, full, and
  Orchestrion integration with the pinned `ORCHESTRION_BIN`. Then CI on Linux,
  macOS and Windows, Go 1.25 to 1.27 and tip.
- **Audit.** Codex auditor rounds on the plan for phases 1, 2 and 4 before
  coding, and on the change until SHIP. Corrections only; design changes go
  back to the user.
- **Compatibility.**
  - Transparency with `go test`: exit codes, output, test-visible environment
    and files.
  - Optional integrations warn and continue.
  - No traces in user repositories.
  - [Feature parity](ci-parity.md) with the SDK, including binaries built with
    `ddto test -c` and run elsewhere.
- **Incorporated code.** Changes to `internal/thirdparty` update
  `ADAPTATIONS.md` and `SOURCE.json`, and `scripts/upstream.py verify` passes.
- **Logging and docs.** New paths get `DD_TRACE_DEBUG` lines, documented in
  [CLI debug](cli-debug.md). Documentation and the backlog change in the same
  pull request.

## Phase 1: GOROOT mirror

**Problem.**
- When the installed `go` is older than the module's `go` or `toolchain` line,
  `GOTOOLCHAIN=auto` runs a toolchain downloaded into `GOMODCACHE`, so GOROOT
  lies inside the module cache.
- Go rejects overlays beneath `GOMODCACHE` (`cmd/go/internal/work/init.go`),
  and ddto overlays `testing`'s sources in every runtime mode. `ddto test`
  therefore aborts with Go's error and runs no test.
- [Mini runtime](mini-runtime.md) documents the limitation, but no test covers
  it: the integration tests set `GOTOOLCHAIN=local`.

**Design.**
1. Read `GOROOT` with the existing `go env -json` call in `environment.go`.
   Mirror only when it lies inside `GOMODCACHE`.
2. Mirror that GOROOT once per toolchain module version, which is immutable,
   into ddto's user cache, for example
   `<UserCacheDir>/ddto/goroot/<toolchain@version>`.
   - Use hard links; copy when linking fails, such as across file systems.
   - Build into a temporary directory and rename it into place.
   - Serialize creators with a lock.
   - Write a completion marker. A partial directory is never used.
3. Run every `go` invocation of that `ddto test` with the mirror's `bin/go`:
   - the exec sites in `internal/runner`;
   - the final `go test`;
   - `go tool -n orchestrion`;
   - launchers that resolve `go` through `PATH`, such as Orchestrion.

   The mirror is the toolchain the original `go` selected, so it does not
   switch again under `GOTOOLCHAIN=auto`.
4. On any failure, warn and run native `go test` unchanged.

**Build cache.** Go leaves the GOROOT directory out of the action ID of GOROOT
packages (`p.Goroot` in `buildActionID`). The compiler's identity is its build
ID, which is the same binary in the mirror. So the mirror reuses what the
original toolchain compiled; only `testing` and its dependents differ, as they
do today. Overlay contents enter the key through `fsys.Actual`, and module-cache
packages through the existing `-I=ddto-…` markers.

**Verify.**
- `go test` prepends `$GOROOT/bin` to `PATH` for test binaries. Tests that run
  `go`, or read `runtime.GOROOT()` or `go env GOROOT`, must keep behaving like
  native runs.
- Windows: hard links on NTFS and the `.exe` suffix.
- `go clean -modcache` while a mirror exists.
- Concurrent `ddto test` processes, disk use, and that nothing writes into
  hard-linked files. Module-cache files are read-only, but assert it.

**Tests.**
- Unit:
  - hard link, and copy as fallback;
  - concurrent creators;
  - a stale temporary directory;
  - a missing marker;
  - a GOROOT outside `GOMODCACHE`, which takes no action.
- Integration: a real switch, with a toolchain placed in a test `GOMODCACHE`
  and an older `go` first in `PATH`. Run Mini and an Orchestrion mode, and a
  test that runs `go`.

**Measure.** The first-run cost of creating the mirror, and that later runs pay
nothing; Gin with a switched toolchain against native.

**Done.** `ddto test` works after a toolchain switch, with no visible
difference from native, and the documented limitation is removed.

## Phase 2: per-test coverage in memory (item 8)

**Evidence.**
- At 4 CPUs, Gin with `-coverpkg=./... -covermode=atomic` takes 6.5 s against
  0.25 s natively. Orchestrion pays the same.
- The same Mini binary takes 6.30 s with per-test coverage and 0.265 s without.
- The CPU profile, 11.1 CPU-s:

  | Where | CPU | What |
  | --- | ---: | --- |
  | Mini's per-test snapshots: `tearDown` → `internal/coverage/cfile.ProcessCoverTestDir` | 5.84 s | Twice per test, under one global mutex |
  | ↳ `cformat.(*Formatter).EmitTextual` | 4.18 s | Writes a textual profile of every coverable unit, one unbuffered `fmt.Fprintf` per line: the profile's 4.7 s of write syscalls |
  | ↳ counter decoding | 1.39 s | Writes counter files and reads them back |
  | Gin's `readWriteKeys` | 2.48 s | Goroutines that a Gin test leaves spinning; they burn CPU while the process lives |

**Design.**
- Add a hook to ddto's `testing` overlay. `testing` is a standard package, so
  it can import `internal/coverage`.
- The hook copies the counters in memory before and after each attempt. It
  decodes the metadata once per process to map counters to files and lines.
- Mini then builds the same per-test coverage payload from the difference, with
  no counter files, textual profile or parsing.
- Register the hook through a pushed linkname, as `ParallelStopHook` does,
  only when the toolchain's `internal/coverage` provides what it needs.
  Otherwise today's path runs.

**Preserve** everything item 8 lists:
- cleanup-only code and parallel attribution;
- the initial-attempt retry policy;
- impacted-test inputs and aggregate percentages;
- normal and deferred processing;
- shutdown on errors;
- count and atomic modes, with `-race`.

**Steps.**
1. Prototype outside the main path. On Gin, Chi and both Testify cases,
   compare decoded `citestcov` payloads between today's path and the in-memory
   path test by test. They must be identical.
2. Implement, with a debug mode that runs both paths and compares them.
3. Run parity tests against the recorded SDK behavior in [feature parity](ci-parity.md).
4. Measure wall time, CPU, allocations, temporary disk and peak memory per
   cell, on Go 1.25, 1.26, 1.27 and tip.

**Risks.**
- `internal/coverage` changes between Go releases. The hook compiles against
  the toolchain's own packages, so feature detection and a fallback are
  required, plus a toolchain test like `TestToolchainTestingDeclaresParallelStop`.
- Reading counters while parallel tests run follows today's semantics, which
  the tests must pin down.

**Expected.** From about 13 ms to under 1 ms per test: Gin from about 6.3 s to
under 0.5 s, with less time under the global mutex.

**Done.**
- Identical per-test payloads.
- Every coverage cell in the benchmark improves or holds.
- The fallback is tested on a toolchain without the hook.

## Phase 3: ddto's startup as the compiler wrapper

**Evidence.**
- With Testify, goleak, coverage, SDK guards or the link signal, `go` runs
  `ddto tool-overlay` for every tool invocation.
- Since #32–#34, ddto links SDK packages for the Git worker and telemetry:
  - `cligit`, `gitupload`, `telemetry` and the `hostname` providers, among
    42 non-standard packages;
  - 106 initialized packages against 41;
  - 13.9 against 6.3 MB.
- Each start went from 1.4 to 2.4 ms.
- An A/B with each revision's own CLI and Mini measured Testify External at
  +2.1% cold and +3.4% on relink. Gin, which uses no wrapper there, measured
  +0.7% cold.

**Investigate.** Split the extra millisecond into package initialization
(`GODEBUG=inittrace`, about 0.5 ms more) and binary size: mapping, page faults
and runtime setup. Use `perf stat` and `strace -c` on the bypass path, and
measure a build without the worker's packages.

**Options**, chosen by measurement:
- (a) A small wrapper executable from its own `main` package, built and cached
  by ddto per version, the way ddto already builds Mini from its sources.
- (b) The Git worker as a separate executable.
- (c) Less package-level initialization in the linked SDK packages.

Options (a) and (b) change what ddto installs or caches: confirm with the user
before choosing them.

**Tests.** Wrapper dispatch, the bypass, plan modes and the link signal through
the new entry point. Every tool mode keeps the same exit codes, output and
environment.

**Done.** A wrapper start at or below the 20caa45 level (1.4 ms), and the A/B
regression gone.

## Phase 4: Mini's package graph in builds

**Evidence.**
- Cold builds take 2.5–3.0 s more at 4 CPUs: Gin +14%, Chi +33%, Testify
  +35–41%.
- Forced links: Gin +0.9 s over six binaries, others +0.1–0.2 s.
- Mini's runtime (`testopt`) adds 49 non-standard packages and 142 standard
  packages beyond `testing`'s graph, among them `net/http` and `crypto/tls`.
- The 2026-10-11 review measured 3.0 s of CPU to compile Mini's packages and
  1.2 s for vet facts, both cached afterwards.

**Investigate.**
- `go build -debug-trace` for compile, vet and link time per package.
- Binary size per package with `go tool nm -size`.
- The reason each package is linked, from `go list -deps` with import paths.
- Candidates to examine:
  - the cloud `hostname` providers (Azure, EC2, ECS, GCE);
  - the telemetry stack;
  - `gitupload`, which the CLI's Git worker already runs under `ddto test`;
  - the logs integration, `impactedtests` and `codeownership`;
  - the standard packages they pull in.

**Constraint.** A test binary built by `ddto test -c` and run elsewhere still
needs everything the SDK does there: no package can depend on the CLI being
present. Removed or replaced SDK code goes into `ADAPTATIONS.md`.

**Done.** Each removal or replacement is measured on the cold and forced-link
cells, with no change in parity tests.

## Phase 5: preparation per invocation

**Evidence.**
- With everything cached, Mini takes 0.11–0.20 s against 0.06–0.10 s natively.
- Testify's preparation takes about 50 ms: the package query 22 ms and the
  library query 22 ms.
- On 2026-10-12, Gin with every result from Go's test cache took 184 ms against
  78 ms natively, with 93 ms of preparation.

**Options.**
- Item 56 reuses the preparation plan between unchanged invocations. The user
  parked it on 2026-10-12 ("Todavía no se hace"), so it needs a new decision.
- Without a cache:
  - overlap the remaining steps with the package query;
  - avoid the library query whenever the package query already answers it. Item
    14 did this for standard imports; the next step is external imports whose
    closures are known.

  Merging both queries into one was measured slower in dd-go.

**Investigate first.** Phase timings with `DD_TRACE_DEBUG` on Gin, both Testify
cases and a large module, then present the options with numbers.

## Phase 6: test binary startup and memory

**Evidence.**
- A Testify binary takes 9.5 ms against 2.7 ms natively.
- Gin's runtime memory is 50.9 MiB against 36.6 MiB.
- With per-test coverage, Gin's binaries reach 78 MiB.
- The benchmark runs binaries directly, so each one makes its own requests.
  Under `ddto test`, the Git worker's requests ahead (#34) already remove part
  of that.

**Investigate.**
- Measure under `ddto test` and directly.
- Startup: `GODEBUG=inittrace`, `runtime/trace` up to the first test, and the
  settings and session requests.
- Memory: a heap profile at exit and `memory.peak` per binary, separating
  per-process fixed costs (buffers, caches, goroutines) from per-test ones.

**Done.** A profile-backed list of changes. Each is implemented only after
measurement shows a gain under `ddto test`.

## Phase 7: refresh the benchmarks and the backlog

- Refresh Mini's benchmarks after phases 1–6 land, following
  [Run and regenerate the benchmarks](build-benchmarks.md) with Go installed
  outside `GOMODCACHE`, or through the mirror once phase 1 lands.
- Decide whether to also remeasure Native and Orchestrion on the same day, about
  3–4 more hours, so the percentages share collection conditions.
- Update the [backlog](optimization-backlog.md): remove completed items, and
  record what each phase measured and dropped.

## Decisions pending

| Decision | Phase |
| --- | --- |
| Merge [#39](https://github.com/tonyredondo/dd-ci-testing-poc/pull/39), the refreshed data this plan uses | Before 1 |
| An extra executable or cache entry for the wrapper or the Git worker, if measurements choose option (a) or (b) | 3 |
| Item 56, the preparation plan cache, parked on 2026-10-12 | 5 |
| Same-day Native and Orchestrion remeasurement | 7 |
