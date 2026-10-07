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

## Planning Prerequisites

`ddtest plan` does not require a Datadog tracer on Ruby, Python, or JavaScript.
Runtime tag probes use the language's built-in APIs. The Ruby probe mirrors the
Datadog library's CRuby expressions, with compatibility tests comparing the
values against the library.

Ruby and Python can use an installed tracer for full test discovery. Without
one, normal planning falls back to file discovery. If you enable
`--strict-discovery`, a failure of full discovery remains an error.
`ddtest run` still validates the tracer before executing tests.

To supply the execution environment's exact tags, provide all five OS and
runtime tags with `--runtime-tags`. A complete set skips the runtime tag probe;
partial overrides continue to merge onto detected values:

```bash
ddtest plan --runtime-tags '{"os.platform":"linux","os.architecture":"x86_64","os.version":"6.8.0","runtime.name":"ruby","runtime.version":"3.4.1"}'
```

Use values from the environment that will execute the tests. Framework discovery
may still need that framework and its runtime installed in the planning job.

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
Playwright, and Vitest run and file discovery, and pytest run and discovery
(since 1.7.0):

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
discovery API on Vitest 1.6. During execution, it selects assigned files by exact
canonical path through Vitest's Node API, preserving the command's configuration,
project filters, and reporters:

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

Keep framework options in `--command`; DDTest supplies the selected test files
and discovery flags. A wrapper's separator before the framework executable is
preserved, for example `--command "npx -- jest --runInBand"`.

A framework's own `--` ends its options. Discovery flags are inserted before
that marker; during execution, DDTest replaces the positional file selection
after it with the selected files. For example:

```bash
ddtest run --command "bundle exec rspec --tag smoke -- spec/models/"
```

The Cucumber adapter also replaces positional feature paths and rerun files
without requiring a separator. This does not add support for arbitrary scripts
or shell command chains.

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

## JavaScript Tracer Preloads

For `ddtest run`, DDTest selects the JavaScript tracer in this order:

1. Preserve an existing Datadog CI require in `NODE_OPTIONS`.
2. If no require is present, append `-r` with the path in `DD_TRACE_PACKAGE`.
3. If that variable is unset, append `-r dd-trace/ci/init`, resolved from the
   project, including loaders such as Yarn Plug'n'Play.

The selected preload is validated with Node.js before execution. An invalid
explicit preload or `DD_TRACE_PACKAGE` produces an error; DDTest does not
silently switch to another tracer. Planning does not validate or require it.

Runtime checks and test-file discovery remove the Datadog preload from
`NODE_OPTIONS` so those processes do not start tracing. Other Node options
and project loaders remain in place. Test workers receive the selected preload.
An action-installed tracer does not need to be added to `package.json` or
exposed through `NODE_PATH`.

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

DDTest appends the selected tracer require to `NODE_OPTIONS` for worker processes
unless a package-name or absolute CI preload is already present. Existing
project loaders, such as Yarn Plug'n'Play, run before a preload DDTest adds.

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
removes the Datadog CI preload from `NODE_OPTIONS`. Cucumber still loads its
configuration and support code as part of a normal dry run.

DDTest uses this command priority:

1. `--command` when set; it must invoke `cucumber-js` directly.
2. The local executable `node_modules/.bin/cucumber-js` when present.
3. `npx cucumber-js`.

During execution, DDTest removes positional feature paths, globs, line filters,
and rerun files from the base command and appends the current worker's assigned
feature files. Other supported Cucumber CLI options are preserved. Worker
processes retain the Datadog CI preload for Test Optimization instrumentation.
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
the Datadog CI preload from `NODE_OPTIONS`; test runs retain it for Test
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

DDTest adds the CI require described above and a `--import` for Vitest worker
processes. It preserves an existing Datadog register import; otherwise it uses
`DD_TRACE_ESM_IMPORT`, the `register.js` next to an external tracer's `ci`
directory, or the project-local `dd-trace/register.js`, in that order.
The GitHub action exports the paths, so manually setting `NODE_OPTIONS` is
unnecessary.
The tracer's `--require` option follows existing project loaders. Discovery
removes both Datadog options to avoid instrumenting the file-listing process.

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
Discovery removes the Datadog CI preload from `NODE_OPTIONS`; test runs retain the
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
removes the Datadog CI preload so listing is not reported as a test session; test
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
