# CI fixture reuse

PR branches, main and releases use the same CI workflow and the same onboard and
testdrive scenarios. Both existing Go instrumentation paths and their coverage
remain enabled. Datadog Test Impact Analysis owns test skipping; this workflow
does not maintain a separate changed-file test selector.

## Go test discovery and compiler caches

The Go shard selector runs `go run ./scripts/testnames`. The helper asks
`go list -race -json ./...` for the files selected by the current platform and
build tags, then uses Go's parser and example discovery to list tests, examples
and fuzz targets. Discovery builds only the small helper; it does not build
every test executable or run their initializers and `TestMain` functions.

The native Orchestrion and `ddtest instrument` jobs retain separate compiler
caches. Keys include OS, architecture, Go version, dependencies and the source
revision; the Orchestrion cache also includes its pinned version and executable.
A new revision restores compatible previous output and saves updated compiler
artifacts, rather than repeatedly restoring an immutable dependency-only cache.
The Datadog action uses this cache instead of restoring another overlapping
module cache. Its configuration script still runs to resolve compatible SDK
versions and set up the current job's instrumentation and reporting.

## Shared CLI executable

The `test-binary` job builds one static executable for the Linux public CLI journeys,
including jobs in browser containers.
Every framework job downloads that run's `integration-cli` artifact and sets
`DDTEST_TEST_BINARY` to its absolute path. The Go test helper uses that executable
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
