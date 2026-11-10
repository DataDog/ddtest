# CI fixture reuse

PR branches, main and releases use the same CI workflow and the same onboard and
testdrive scenarios. The Go suite reports through `ddtest instrument`; only
ddto's comparison tests still build with Orchestrion. Datadog Test Impact
Analysis owns test skipping; this workflow does not maintain a separate
changed-file test selector.

## Go shards and compiler caches

The Go shards split by package with `go list ./...`. `-run` would keep
`ddtest instrument` from backfilling the coverage of the tests that Test Impact
Analysis skips, so no shard narrows its tests. `scripts/go_test_shards.py` pins
the slowest packages to different shards and spreads the others by hash.
`internal/testdrive/pytestpaths` holds the real pytest validations in two
packages because they take most of `internal/testdrive`'s time.

`-coverpkg` names the module's packages instead of `./...`. `ddto` lies below the
repository root, so `./...` makes `ddtest instrument` fall back from in-memory
per-test coverage to text profiles, which are too slow for this suite once Test
Impact Analysis is on.

The Go shards keep their own compiler cache. Keys include OS, architecture, Go
version, dependencies and the source revision. A new revision restores
compatible previous output and saves updated compiler artifacts, rather than
repeatedly restoring an immutable dependency-only cache.

## Shared CLI executable

The `test-binary` job builds static release executables for Linux and Windows,
including the runtime sources and wrapper needed by `ddtest instrument`.
Dedicated root Go test jobs use the `setup-test-optimization` action to download
that run's CLI, add it to `PATH` and set `DDTEST_TEST_BINARY` to its absolute path.
Their Go tests run through `ddtest instrument`, with session names identifying
the job, runtime versions and shards. A configured Datadog API key enables
agentless reporting; without one, the same commands run with reporting disabled.
Only reporting setup and instrumented Go test steps receive the API key;
dependency installation runs in separate steps without it.
Child framework fixtures retain their own tracing and local intake settings.
The outer Go tracer infers its service from the repository; exporting
`DD_SERVICE` would also change the module identity of child ScalaTest fixtures.
The Go test helper uses the shared executable
across temporary customer checkouts. Outside CI, it builds the CLI once per test
process; the test process's isolated temporary directory owns its cleanup.

## Locked Node fixtures

The local `node-test-fixture` action prepares a lockfile and reuses installed
dependencies only when the OS, architecture, exact Node/npm versions and complete
lockfile match. Cache misses run `npm ci`; there are no partial restore keys.
Dependencies are saved before tests run, keeping runtime-generated caches out of
the reusable fixture.
Jest matrix jobs and Cucumber workers use this action. Browser fixtures with
additional external installation state retain their existing setup.

The Cucumber planner resolves `latest` once before expanding the matrix and
creates one lockfile per resolved version. Labels share a job only when the
complete dependency graph, runtime, test filter and shard are identical. All
distinct compatibility cases remain. When `latest` resolves to pinned `13.3.0`,
13 requested lanes become 9 executions; a distinct latest version keeps its lanes.

Inspect the resolved configurations with Node 24 and npm registry access:

```sh
python3 scripts/ci_cucumber_matrix.py --output /tmp/cucumber-ci-fixtures \
  --summary /tmp/cucumber-ci-matrix.md
```

The output directory must not exist. Inspect `matrix.json`, the summary and the
version directories' lockfiles; remove the directory afterwards.

## Python prerequisites

The Python version-check subprocess disables Datadog reporting and telemetry;
it only imports dependencies and checks their versions. The actual testdrive
journeys keep their original reporting, local intake, configuration variants and
six feature assertions. No validation scenario is removed or moved to a release
only workflow.

## Test helper isolation

Crash-recovery helpers run with their own CI reporting disabled, while their
parent tests keep reporting and checking process ownership and cleanup. Jenkins
allocation budgets run in an isolated helper so background SDK allocations do
not count against the parser. The original memory limits remain, and the parent
also exercises every behavioral assertion to retain instrumentation coverage.
