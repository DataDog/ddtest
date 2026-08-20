# Running DDTest

## Terminology

- A **runner** is a program that runs tests. DDTest is a runner, and DDTest can
  also produce file lists for other runners.
- A **CI node** is one CI execution environment, such as a GitHub Actions job
  executor, CircleCI parallel container, Kubernetes pod, VM, or local machine.
- A **worker** is a test process started by DDTest to execute tests. One CI node
  can run one worker or several workers.

`ddtest plan` decides how many CI nodes or local workers are useful and assigns
test files to them. `ddtest run --ci-node N` runs the files assigned to CI node
`N`; inside that CI node, `--ci-node-workers` controls how many worker processes
DDTest starts.

## Single CI Node

```bash
ddtest run --platform ruby --framework rspec
```

For Python/pytest:

```bash
ddtest run --platform python --framework pytest
```

For JavaScript/Jest:

```bash
ddtest run --platform javascript --framework jest
```

For JavaScript/Vitest:

```bash
ddtest run --platform javascript --framework vitest
```

For JavaScript/Mocha:

```bash
ddtest run --platform javascript --framework mocha
```

For JavaScript/Cypress:

```bash
ddtest run --platform javascript --framework cypress
```

For JavaScript/Playwright:

```bash
ddtest run --platform javascript --framework playwright
```

For JavaScript/Cucumber:

```bash
ddtest run --platform javascript --framework cucumber
```

On one CI node, the default `--min-parallelism` and `--max-parallelism` equal
the available physical CPU core count, so DDTest can start one worker per
physical core without defaulting to one worker per hyperthread.

## Multiple CI Nodes

Run `ddtest plan` once, share the `.testoptimization/` folder to all CI nodes,
then on each CI node run only its assigned files:

```bash
ddtest run --platform ruby --framework rspec --ci-node <CI_NODE_INDEX>
```

For Python/pytest:

```bash
ddtest run --platform python --framework pytest --ci-node <CI_NODE_INDEX>
```

For JavaScript/Jest:

```bash
ddtest run --platform javascript --framework jest --ci-node <CI_NODE_INDEX>
```

For JavaScript/Vitest:

```bash
ddtest run --platform javascript --framework vitest --ci-node <CI_NODE_INDEX>
```

For JavaScript/Mocha:

```bash
ddtest run --platform javascript --framework mocha --ci-node <CI_NODE_INDEX>
```

For JavaScript/Cypress:

```bash
ddtest run --platform javascript --framework cypress --ci-node <CI_NODE_INDEX>
```

For JavaScript/Playwright:

```bash
ddtest run --platform javascript --framework playwright --ci-node <CI_NODE_INDEX>
```

For JavaScript/Cucumber:

```bash
ddtest run --platform javascript --framework cucumber --ci-node <CI_NODE_INDEX>
```

In CI-node mode, DDTest uses one local worker by default so database and other
per-worker resources stay easy to isolate. To fan out within each CI node, set
`--ci-node-workers` to a positive integer, or use `--ci-node-workers ncpu` to
use the node's available physical CPU cores.

## Worker Environment

`--worker-env` supports `{{nodeIndex}}` and `{{workerIndex}}` placeholders.
`{{nodeIndex}}` is the CI node index from `--ci-node` or
`DD_TEST_OPTIMIZATION_RUNNER_CI_NODE`; in single-node runs, it is `0`.
`{{workerIndex}}` is the worker process index within the current CI node,
starting at `0`.

If a CI node uses multiple workers, each worker receives the same
`{{nodeIndex}}` value and a different `{{workerIndex}}` value.

```bash
ddtest run \
  --platform ruby \
  --framework rspec \
  --worker-env "DATABASE_NAME_TEST=app_test{{nodeIndex}}_{{workerIndex}}"
```

DDTest automatically sets `DD_TEST_SESSION_NAME` for each worker to
`<DD_SERVICE>-node-<nodeIndex>-worker-<workerIndex>` when the variable is not
already set. If you set `DD_TEST_SESSION_NAME` yourself, DDTest preserves it and
expands the same `{{nodeIndex}}` and `{{workerIndex}}` placeholders before
starting each worker.

## Custom Commands

Use `--command` to override the framework's default base test command where
supported. DDTest currently applies this override to RSpec run and full
discovery, Minitest run and full discovery, and Cucumber, Cypress, Jest, Mocha,
Playwright, and Vitest run and file discovery:

```bash
ddtest run --platform ruby --framework rspec --command "bundle exec rspec --profile"
```

For JavaScript/Jest, DDTest automatically appends `--listTests` during planning
and `--runTestsByPath <files>` during execution:

```bash
ddtest run --platform javascript --framework jest --command "pnpm jest --runInBand"
```

For JavaScript/Mocha, the command must invoke Mocha directly. DDTest loads its
effective configuration, discovers files without loading test modules, and
replaces configured `spec` entries with each worker's assigned files:

```bash
ddtest run --platform javascript --framework mocha --command "pnpm exec mocha --parallel"
```

For JavaScript/Vitest, the command must invoke Vitest directly. During planning,
DDTest uses `list --filesOnly --json` on Vitest 2.0 and newer and the config-aware
discovery API on Vitest 1.6. It appends selected files during execution:

```bash
ddtest run --platform javascript --framework vitest --command "pnpm exec vitest run --project unit*"
```

For JavaScript/Cypress, the command must invoke Cypress directly. DDTest keeps
configuration options such as `--project`, `--config-file`, `--config`,
`--component`, and `--e2e` during discovery and execution, and replaces any
configured `--spec` value with each worker's assigned specs:

```bash
ddtest run --platform javascript --framework cypress --command "pnpm exec cypress run --component"
```

For JavaScript/Playwright, the command must invoke `playwright test` directly.
DDTest keeps configuration and selection options during native discovery and
replaces positional file filters with each worker's assigned files:

```bash
ddtest run --platform javascript --framework playwright --command "pnpm exec playwright test --config apps/web/playwright.config.ts --project chromium"
```

For JavaScript/Cucumber, the command must invoke `cucumber-js` directly. DDTest
uses its profiles, configuration, paths, tags, and name filters for discovery,
then replaces positional feature paths with each worker's assigned files:

```bash
ddtest run --platform javascript --framework cucumber --command "pnpm exec cucumber-js features/v1/*.feature --profile ci"
```

When using `--command`, do not include the `--` separator. Except for Cucumber,
do not include test files in the command. DDTest automatically appends selected
tests and framework-specific flags; the Cucumber adapter also recognizes and
replaces positional feature paths and rerun files.

Incorrect:

```bash
# DDTest appends test files itself
ddtest run --command "bundle exec rspec -- spec/models/"

# The -- separator is not supported in --command
ddtest run --command "bundle exec my-wrapper --"
```

If your command contains `--`, DDTest will emit a warning and automatically
remove the `--` separator and anything after it.

For pytest, use `PYTEST_ADDOPTS` for pytest flags. DDTest currently runs
`python -m pytest` and appends the selected test files. DDTest appends
`--ddtrace` to `PYTEST_ADDOPTS` so the `ddtrace` pytest plugin loads
automatically.

## Pytest Discovery

For Python/pytest, DDTest discovers test files using this priority:

1. `--tests-location` when set.
2. Pytest configuration from `pytest.ini`, `pyproject.toml`, `tox.ini`, or
   `setup.cfg`, using `testpaths` and `python_files`.
3. The built-in pattern `**/{test_*,*_test}.py`.

Pytest does not have an equivalent to RSpec's pattern flag, so DDTest resolves
the pattern to explicit file paths before invoking `python -m pytest`.

## Jest Discovery And Instrumentation

For JavaScript/Jest, DDTest discovers test files with Jest's own `--listTests`
command. It uses this priority:

1. `--command` when set, with `--listTests` appended.
2. The local executable `node_modules/.bin/jest` when present.
3. `npx jest`.

Jest uses its own configuration and default test matching for `--listTests`.
When `--tests-location` or `--tests-exclude-pattern` is set, DDTest filters the
file list returned by Jest after discovery; it does not pass `--tests-location`
as Jest's `--testMatch`.

DDTest prepends `-r dd-trace/ci/init` to `NODE_OPTIONS` for worker processes
unless `NODE_OPTIONS` already loads `dd-trace/ci/init`.

## Cucumber Discovery And Instrumentation

DDTest is tested with `@cucumber/cucumber` 7 through 13. It discovers feature files
using Cucumber's own dry-run planner and stable Messages formatter. This honors
`cucumber.js`, `cucumber.cjs`, `cucumber.mjs`, JSON/YAML configuration on
versions that support it, selected profiles, positional paths and rerun files,
Gherkin dialects, `.feature.md`, and tag/name filters. DDTest selects only the
feature URIs referenced by planned test cases, so a file whose scenarios are
all filtered out is not added to the execution plan.

Discovery forces Cucumber's internal parallelism to zero, does not execute step
bodies, disables report publishing through `CUCUMBER_PUBLISH_ENABLED`, and
removes `-r dd-trace/ci/init` from `NODE_OPTIONS`. Cucumber still loads its
configuration and support code as part of a normal dry run.

DDTest uses this command priority:

1. `--command` when set; it must invoke `cucumber-js` directly.
2. The local executable `node_modules/.bin/cucumber-js` when present.
3. `npx cucumber-js`.

During execution, DDTest removes positional feature paths, globs, line filters,
and rerun files from the base command and appends the current worker's assigned
feature files. Other supported Cucumber CLI options are preserved. Worker
processes retain `-r dd-trace/ci/init` for Test Optimization instrumentation.
Because DDTest plans at feature-file granularity, scenario line selectors and
rerun files narrow discovery but are not retained as scenario-level selectors
during worker execution. Use Cucumber tag or name filters when that scenario
scope must remain active in every worker.

## Mocha Discovery And Instrumentation

DDTest supports Mocha 8 and newer. It uses Mocha's own option loader and file
collector, so discovery honors `.mocharc.*`, the `mocha` property in
`package.json`, `MOCHA_OPTIONS` on versions that support it, `spec`,
`extension`, `recursive`, `ignore`, and `sort` without loading test modules or
running hooks. Files configured with `--file` are treated as shared setup and
are loaded by every worker rather than being partitioned.

Mocha normally adds positional files to configured `spec` patterns. During a
DDTest run, the adapter replaces that merged list with the worker's assigned
files while preserving the rest of the effective Mocha configuration. This
prevents every worker from running the entire configured suite.

DDTest uses the local `node_modules/.bin/mocha` when present and otherwise
expects Mocha to be resolvable from the current project. Discovery removes
`-r dd-trace/ci/init` from `NODE_OPTIONS`; test runs retain it for Test
Optimization instrumentation.

## Vitest Discovery And Instrumentation

For JavaScript/Vitest 2.0 or higher, DDTest discovers test files with Vitest's
native `list --filesOnly --json` command. It uses this priority:

1. `--command` when set, replacing its Vitest subcommand with `list` and
   appending `--filesOnly --json`.
2. The local executable `node_modules/.bin/vitest` when present.
3. `npx vitest`.

Vitest resolves its own Vite/Vitest configuration, projects, and default test
matching. When `--tests-location` or `--tests-exclude-pattern` is set, DDTest
filters the file list returned by Vitest after discovery.

Vitest 1.6 does not support `list --filesOnly`. When DDTest detects that specific
unsupported-option error, it uses the `vitest/node` discovery API instead. This
loads the project's Vitest configuration and discovers files for its configured
projects, include and exclude patterns, and CLI filters without executing tests.
If that API is unavailable, DDTest falls back to its own filesystem glob using
`--tests-location` or the default Vitest test-file pattern.

DDTest prepends both `--import dd-trace/register.js` and
`-r dd-trace/ci/init` to `NODE_OPTIONS` for Vitest worker processes unless they
are already present. Discovery removes these options to avoid instrumenting the
file-listing process.

## Cypress Discovery And Instrumentation

DDTest supports Cypress 12 and newer. During planning it runs Cypress with a
temporary config wrapper and a guaranteed-missing `--spec` value. Cypress loads
the project's real JavaScript, TypeScript, ESM, or CommonJS config, applies CLI
and environment overrides, and runs `setupNodeEvents`; the wrapper reports the
resolved `projectRoot`, testing type, `specPattern`, and `excludeSpecPattern`.
DDTest then discovers matching files without opening a browser or executing a
spec. For component testing it also excludes specs matched by the E2E pattern,
matching Cypress's own behavior.

DDTest uses this command priority:

1. `--command` when set; it must invoke Cypress directly.
2. The local executable `node_modules/.bin/cypress` when present.
3. `npx cypress`.

During execution DDTest invokes `cypress run --spec` with the files assigned to
the worker. Cypress Test Optimization instrumentation must already be configured
in the project's Cypress plugin and support files as documented by `dd-trace`.
Discovery removes `-r dd-trace/ci/init` from `NODE_OPTIONS`; test runs retain the
configured platform environment.

## Playwright Discovery And Instrumentation

DDTest supports Playwright 1.18 and newer. During planning it invokes
`playwright test --list` with a temporary custom reporter. Playwright itself
loads the effective configuration and collects tests, so discovery honors
`testDir`, string and regular-expression `testMatch` and `testIgnore` values,
projects, project dependencies, grep filters, `.only`, positional filters, and
other supported selection options. The reporter returns source file paths;
DDTest deduplicates files that occur in multiple projects and leaves dependency
and teardown projects out of the partition because Playwright runs that shared
lifecycle automatically with each selected primary project. Discovery never
launches a browser or executes a test.

DDTest uses this command priority:

1. `--command` when set; it must invoke `playwright test` directly.
2. The local executable `node_modules/.bin/playwright` when present.
3. `npx playwright`.

During execution DDTest passes exact file-path regular expressions for the
files assigned to each worker. It removes original positional filters so they
cannot add unassigned files. It also removes Playwright's `--shard` because
DDTest owns the file partition, and removes interactive `--ui` options. Other
options, including `--config`, `--project`, `--grep`, reporters, retries, and
workers, are retained. When `--tests-location` or
`--tests-exclude-pattern` is set, DDTest applies that additional filter to the
native file list.

Playwright Test Optimization instrumentation is provided by `dd-trace` through
`NODE_OPTIONS`, as with other JavaScript frameworks. Discovery temporarily
removes `-r dd-trace/ci/init` so listing is not reported as a test session; test
runs retain it. Check the `dd-trace` compatibility range for the Playwright
version in the project; current `dd-trace` 6 releases require Playwright 1.38
or newer.

## Parallelism Selection

DDTest chooses parallelism by estimating the runnable duration of each test file,
then trying counts between `--min-parallelism` and `--max-parallelism`. In
CI-node mode, the selected count is the number of CI nodes. On a single CI node,
the selected count is the number of workers.

Duration estimates come from Datadog test suite p50 timings when available and
fall back to local discovery weights otherwise. Each candidate count is scored
as expected slowest-worker time plus the count multiplied by
`--ci-job-overhead`. Increase `--ci-job-overhead` to use fewer CI nodes, or
decrease it to prefer faster wall time. Use duration values such as `25s`, `1m`,
or `1500ms`; set `0s` to disable this overhead bias. When scores tie, DDTest
prefers fewer CI nodes or workers, then lower wall time, then lower imbalance
between workers.

Set `--target-time` to make DDTest first choose among splits whose expected
wall time is at or below that target. Use the same duration format, such as
`10m`, `300s`, or `1500ms`; the default `0s` disables the target. If no split
within `--min-parallelism` and `--max-parallelism` can meet the target, DDTest
logs a warning and selects the split with the lowest expected wall time,
ignoring CI job overhead, to get as close as possible to the target.
