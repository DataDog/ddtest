# JavaScript worker coverage collisions

Standalone reproductions for the six JavaScript frameworks supported by DDTest:
Jest, Mocha, Cypress, Playwright, Cucumber, and Vitest.

The default baseline launches two **real framework processes in one checkout**
and inspects coverage on disk without DDTest or dd-trace-js. The `--ddtest` mode
uses DDTest's real planner and framework adapters with the pinned tracer and a
localhost backend stub. See the validation commands below.

## Run

Requirements: Node.js 22 or newer, npm, and macOS or Linux. Cypress runs its bundled
Electron browser. On Linux, install Cypress's system dependencies and `xvfb-run`;
the harness uses a separate Xvfb display per Cypress process. Playwright's case
uses Node-side code and requires no browser download.

From this directory:

```sh
npm ci
npm run reproduce
npm run reproduce -- --framework jest
npm run reproduce -- --framework mocha,cucumber,playwright
npm run reproduce -- --repeat 2
```

All direct dependencies and the transitive dependency tree are pinned. These are
versioned reproduction fixtures, not a claim about every framework release.
No Datadog credentials are needed. Worker environments remove inherited Datadog,
NYC, and Node preload settings to keep tracing out of the reproduction.

A successful harness exit means every expected failure was reproduced **and**
every control preserved both covered source files. Framework startup errors,
missing reports, unrelated test failures, and failing controls cause a nonzero
exit. If an upgrade fixes a case, it also fails with an explicit
`Expected coverage loss was not reproduced` message; inspect before updating the
expectation.

## Cases

Each worker covers a different tiny source module: `src/alpha.cjs` or
`src/beta.cjs`. The modules return different values, checked by the tests. The
harness checks positive statement hits, so listing an unexecuted source file does
not count as coverage.

| Framework | Coverage mechanism | Shared-directory failure |
| --- | --- | --- |
| Jest | Built-in Babel/Istanbul coverage | Last report overwrites the other worker's report |
| Mocha | NYC wrapping Mocha and a real Node subprocess | A second NYC invocation deletes the first worker's persisted subprocess coverage |
| Cypress | `@cypress/code-coverage`, with source instrumented by NYC | Shared raw/report files lose the other worker's coverage |
| Playwright | NYC wrapping Playwright Test and a real Node subprocess | Same NYC cleanup loss; this is **Node-side coverage**, not browser page coverage |
| Cucumber | NYC wrapping Cucumber and a real Node subprocess | Same NYC cleanup loss |
| Vitest | Built-in V8 coverage | A worker removes the shared coverage temporary directory while the other still needs it |

Mocha, Cucumber, and Playwright do not acquire NYC coverage merely by running
under DDTest. Those cases explicitly model projects that use an NYC integration.

The modes are:

- `shared`: both workers use the same temporary and final report directories.
- `isolated`: each worker has its own temporary and report directories. After
  both exit, the harness merges the reports using `istanbul-lib-coverage` and
  requires positive coverage for both modules.
- `temp-only` (Cypress): temporary directories differ, but reports are shared.
  This models PR #151's proposed isolation and still loses coverage.
- `shared-no-clean` (NYC cases): shared directories, with `--no-clean`. Both
  subprocesses finish writing their target coverage before either report is
  generated. This control demonstrates that UUID-named raw files can coexist
  when cleanup and reporting are coordinated. It does **not** establish that
  arbitrary parallel NYC invocations are safe with this flag alone.

The complete matrix has 16 cases per repetition: seven affected configurations
and nine controls.

## Scheduling and evidence

The harness uses filesystem gates rather than timing sleeps to select the
interleaving:

1. Alpha runs its assertion, signals readiness, and waits inside its test.
2. Beta starts while Alpha's runner is still alive, runs its assertion, and waits.
3. Beta is released and exits, including coverage reporting.
4. Alpha is released and exits, including coverage reporting.
5. The final reports are inspected and, for isolated runs, merged.

For the NYC cases, each assertion executes its source module in a real Node
subprocess. NYC automatically instruments that subprocess. Alpha's child exits
and persists coverage **before** Beta starts, while Alpha's parent runner remains
alive. The harness snapshots raw coverage before and after Beta starts. With
NYC's default cleanup, the Alpha snapshot disappears. This models a legal
schedule for suites that exercise subprocesses; it is not a claim that every
Mocha/Cucumber/Playwright suite uses them.

For Jest and Cypress, controlling report completion makes lost coverage
reproducible without requiring writes to overlap. This suite checks data loss;
it does not require the intermittent `Unexpected end of JSON input` exception
from Shepherd's original CI failure to occur.

For Vitest 3.2.4, the shared case accepts only the specific observed nonzero exit
with `ENOENT` for `coverage/.tmp/coverage-*`. Unexpected failures are errors.
The isolated case must exit successfully and preserve both modules.

Every invocation creates a fresh `.runs/<timestamp>-<pid>/` directory. Each case
has its own fixture copy, worker logs, commands, exit codes, coverage artifacts,
and `result.json`. The root `summary.json` records dependency versions and every
case result. Generated evidence and `node_modules` are gitignored. No existing
playground, tracer clone, or coverage output is modified. Remove this suite's
`.runs/` directory when the evidence is no longer needed.

Timeouts terminate the fixture process groups. An interrupted run leaves its
artifacts for diagnosis; the next run uses fresh state.

## Background

- [Shepherd PR #151](https://github.com/ddoghq/shepherd/pull/151)
- [Cypress parallel coverage merging](https://docs.cypress.io/app/tooling/code-coverage#combining-code-coverage-from-parallel-tests)
- [NYC combining multiple runs](https://github.com/istanbuljs/nyc#combining-reports-from-multiple-runs)
- [Jest coverage directory](https://jestjs.io/docs/configuration#coveragedirectory-string)
- [Vitest coverage cleanup](https://vitest.dev/config/coverage#coverage-clean)

## Validate DDTest's coordinated coverage mode

From this repository's root, build the branch with `make build`. Then, from this
fixture directory, install dependencies with `npm ci` and install the pinned
Playwright browser with `npx playwright install chromium` (`--with-deps` on Linux).
The pinned tracer's Playwright integration requires that browser even though the
coverage target is Node-side code. Linux also needs Cypress prerequisites and
`xvfb-run`.

```sh
# Independent-process baseline: seven affected cases, nine passing controls.
npm run reproduce
# Actual DDTest planning, splitting, workers, and merging; six frameworks.
npm run reproduce -- --ddtest /absolute/path/to/ddtest --repeat 2
# CI-node mode: one node, two workers; six frameworks.
npm run reproduce -- --ddtest /absolute/path/to/ddtest --execution ci-node
# Exit-status and incomplete-report handling.
npm run reproduce -- --ddtest /absolute/path/to/ddtest --framework jest --fail-worker alpha
npm run reproduce -- --ddtest /absolute/path/to/ddtest --framework jest --report-error missing
npm run reproduce -- --ddtest /absolute/path/to/ddtest --framework jest --report-error malformed
```

The DDTest cases use the same source/test fixtures and gated completion order as
the baseline. DDTest discovers and distributes the files itself. The harness
starts a localhost backend stub returning disabled TIA/retry settings and empty
duration data; no production service or credentials are used. Each case requires
DDTest to exit successfully, two isolated worker reports, and a final report with
positive statement hits for both source modules. A small command wrapper records
when each real framework exits so the controller can release the other worker.

The intentional test failure must remain a failed DDTest run while retaining
both workers' merged coverage. Missing/malformed reports must produce the stable
`run_coverage_merge_failed` error and no combined report. These cases return a
successful **harness** exit only when DDTest rejects the expected failures.
