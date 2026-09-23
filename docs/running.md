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

DDTest streams test stdout and stderr, but test execution is non-interactive.
Worker stdin is connected to the null device, so accidental reads receive EOF.
Prompts, interactive debuggers, and other commands that require stdin are not
supported.

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
supported. DDTest applies this override to RSpec run and full
discovery, Minitest run and full discovery, Cucumber, Cypress, Jest, Mocha,
Playwright, and Vitest execution, and pytest run and discovery
(since 1.7.0):

```bash
ddtest run --platform ruby --framework rspec --command "bundle exec rspec --profile"
```

For JavaScript/Jest, DDTest appends `--runTestsByPath` and the worker's assigned
files during execution. An explicit `--tests-location` selects filesystem
discovery without invoking the configured command, unless native discovery is
forced. Without that glob, Jest config analysis can fall back to native discovery:

```bash
ddtest plan --platform javascript --framework jest --tests-location 'tests/**/*.test.js'
ddtest run --command 'npx jest --runInBand'
```

For JavaScript/Mocha, the command must invoke Mocha directly. DDTest loads its
effective configuration during execution and replaces configured `spec` entries with each worker's assigned files:

```bash
ddtest run --platform javascript --framework mocha --command "pnpm exec mocha --parallel"
```

For JavaScript/Vitest, the command must invoke Vitest directly. DDTest selects
its `run` subcommand and appends the assigned files during execution. Use
`--tests-location` to select custom paths during planning:

```bash
ddtest plan --framework vitest --tests-location 'checks/**/*.check.ts'
ddtest run --command 'npx vitest --config vitest.unit.ts'
```

For JavaScript/Cypress, the command must invoke Cypress directly. DDTest keeps
configuration options such as `--project`, `--config-file`, `--config`,
`--component`, and `--e2e` during execution, and replaces any
configured `--spec` value with each worker's assigned specs:

```bash
ddtest run --platform javascript --framework cypress --command "pnpm exec cypress run --component"
```

For JavaScript/Playwright, the command must invoke `playwright test` directly.
DDTest preserves configuration options during execution and replaces positional file filters with each worker's assigned files:

```bash
ddtest run --platform javascript --framework playwright --command "pnpm exec playwright test --config apps/web/playwright.config.ts --project chromium"
```

For JavaScript/Cucumber, the command must invoke `cucumber-js` directly. DDTest
preserves profiles, configuration, tags, and name filters during execution,
and replaces positional feature paths with each worker's assigned files:

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

For pytest, DDTest runs `python -m pytest <files>` by default. Since 1.7.0,
set `--command` to override the base command. For example, `--command pytest`
runs the `pytest` console script instead of `python -m pytest`. DDTest runs
`<command> <files>` and does not add `-m pytest`. To pass extra pytest flags
without changing the base command, use `PYTEST_ADDOPTS`. DDTest appends
`--ddtrace` to `PYTEST_ADDOPTS` so the `ddtrace` pytest plugin loads
automatically.

## Pytest Discovery

For Python/pytest, DDTest discovers test files using this priority:

1. `--tests-location` when set.
2. Pytest configuration from `pytest.ini`, `pyproject.toml`, `tox.ini`, or
   `setup.cfg`, using `testpaths` and `python_files`.
3. The built-in pattern `**/{test_*,*_test}.py`.

Pytest does not have an equivalent to RSpec's pattern flag, so DDTest resolves
the pattern to explicit file paths before invoking the configured pytest
command. The default is `python -m pytest`. Since 1.7.0, `--command` overrides
it.

## JavaScript File Discovery

For the customer rollout steps and behavior comparison, see the
[JavaScript discovery migration guide](javascript-discovery-migration.md).

JavaScript skipping operates on test files. Jest first analyzes supported config
in Go, preserving its native selection rules without starting Node. Unsupported
config analysis logs a reason and uses native Jest discovery. The other JS
frameworks currently use filesystem globs by default. An explicit
`--tests-location` selects filesystem discovery for any JS framework.

Use `--force-full-test-discovery` to select the original native discovery adapter
for any of the six JS frameworks. It takes precedence over fast discovery;
include/exclude options then filter the native result. Native discovery can
start Node, load test modules, and run framework discovery hooks.

Filesystem discovery prunes dependency and VCS directories and does not follow
directory symlinks. Generate tests before planning. Jest's automatic mode follows
its installed version's defaults; the table below describes filesystem patterns.

The default include globs are:

| Framework | Default files |
| --- | --- |
| Jest | `**/__tests__/**/*.{js,jsx,ts,tsx,mjs,mts,cjs,cts}` and `**/*.{spec,test}.{js,jsx,ts,tsx,mjs,mts,cjs,cts}` |
| Vitest | `**/*.{test,spec}.{js,jsx,ts,tsx,mjs,mts,cjs,cts}` |
| Mocha | `test/**/*.{js,cjs,mjs}` |
| Cypress | `cypress/e2e/**/*.cy.{js,jsx,ts,tsx}` |
| Playwright | `**/*.{spec,test}.{js,jsx,ts,tsx,mjs,mjsx,mts,mtsx,cjs,cjsx,cts,ctsx}` |
| Cucumber | `features/**/*.{feature,feature.md}` |

For custom layouts, set `--tests-location` to the complete include glob and
`--tests-exclude-pattern` to exclude helpers, generated copies, shared setup,
or other suites that should not be partitioned. Paths are relative to the
working directory, including when `--command` selects a nested project or
configuration file. Use brace alternatives to combine patterns. These are
DDTest filesystem globs, not JavaScript regular expressions or minimatch
extglobs; translate `*.@(spec|test).ts` to `*.{spec,test}.ts`.

```bash
ddtest plan --framework jest --tests-location 'packages/*/checks/**/*.check.ts' \
  --tests-exclude-pattern '**/{fixtures,helpers}/**'
ddtest plan --framework cypress --tests-location 'apps/web/src/**/*.cy.ts'
ddtest plan --framework playwright --tests-location 'apps/web/tests/**/*.spec.ts' \
  --tests-exclude-pattern '**/{setup,teardown}.spec.ts'
```

For Jest, retain your existing command/config for automatic analysis; custom
configs do not generally need duplicate globs. For the other JS frameworks,
mirror custom file selection in the glob options or use the force flag. In glob
mode, Cucumber tag/name filters and Playwright grep/project filters still apply
during execution; files filtered out by those options may remain in the plan.
Exclude shared Mocha setup and Playwright dependency/teardown files from globs;
native discovery retains their original treatment.

During execution DDTest preserves the configured command and framework
configuration. It assigns Jest files with `--runTestsByPath`, replaces Mocha's
merged spec list using its adapter, passes Cypress files via `--spec`, and
passes exact file-path regular expressions to Playwright. Cucumber receives
assigned feature paths and Vitest receives assigned test files. Empty
assignments never start a test process.

DDTest prepends `-r dd-trace/ci/init` to `NODE_OPTIONS` for JavaScript worker
processes unless already present. Vitest also loads
`--import dd-trace/register.js`. Cypress instrumentation must be configured in
the project's plugin and support files. The fast path starts no JavaScript process. Native discovery strips
Datadog discovery preloads as before. Platform prerequisite checks and
runtime tag collection are separate from file discovery and may invoke Node.

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
