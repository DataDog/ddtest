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

### Planning-job correlation

`ddtest plan` records the planning job's CI identity in
`.testoptimization/plan-metadata.json`. Upload and download the complete
`.testoptimization` artifact as usual to carry these values between jobs.
This file is written in every planning mode, independently of the HTTP cache.

`ddtest run` sets `_DD_TEST_PARALLELIZATION_ENABLED=true` and an absolute
`_DD_TEST_PARALLELIZATION_METADATA_FILE` path for every test worker, including
single-worker runs. The file locator points at the generated planning metadata,
independently of any custom `TEST_OPTIMIZATION_MANIFEST_FILE` setting. DDTest
preserves custom manifests for their existing HTTP-cache use.

Supporting tracing libraries read the explicit metadata file. When no metadata
locator is supplied, they look for `plan-metadata.json` alongside the manifest
identified by `TEST_OPTIMIZATION_MANIFEST_FILE` (or its
`DD_TEST_OPTIMIZATION_MANIFEST_FILE` alias), falling back to `.testoptimization/`
in the current working directory when no manifest location is supplied.
DDTest does not inject individual planning metadata environment variables.
Retired `_DD_TEST_PARALLELIZATION_PLAN_*` values supplied through the process or
worker environment are cleared before execution, so older tracing libraries
cannot use a stale planning identity.

> **Internal interface:** These runtime parameters and the metadata file are
> managed by DDTest and are documented for reference only. They are not part of
> the public configuration API and may change or be removed without notice. Do not
> set, modify, or depend on them in application code or CI configuration.

The marker produces `test.parallelization.enabled:true`. DDTest maps the planning
CI identity to final tracer tags in the file's `tags` object. Libraries apply these
string-valued `test.parallelization.plan.*` tags directly:

| Planning CI tag | Artifact / tracer tag |
| --- | --- |
| `ci.provider.name` | `test.parallelization.plan.provider.name` |
| `ci.job.id` | `test.parallelization.plan.job.id` |
| `ci.job.name` | `test.parallelization.plan.job.name` |
| `ci.job.url` | `test.parallelization.plan.job.url` |
| `ci.pipeline.id` | `test.parallelization.plan.pipeline.id` |
| `ci.pipeline.name` | `test.parallelization.plan.pipeline.name` |
| `ci.pipeline.display_name` | `test.parallelization.plan.pipeline.display_name` |
| `ci.pipeline.number` | `test.parallelization.plan.pipeline.number` |
| `ci.pipeline.url` | `test.parallelization.plan.pipeline.url` |
| `ci.stage.name` | `test.parallelization.plan.stage.name` |

These tags apply to sessions, modules, suites, and individual tests. The execution
job retains its normal `ci.*` tags. Missing fields are omitted. If the file is
missing, unreadable, or corrupt, supporting libraries still set the enabled tag
and omit the planning identity. Older artifacts without this file are supported.

## Custom Commands

Use `--command` to override the framework's default base test command where
supported. DDTest applies this override to RSpec run and full
discovery, Minitest run and full discovery, Cucumber, Cypress, Jest, Mocha,
and Playwright run and file discovery, and pytest run and discovery
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

`plan` and `run` use Vitest’s direct Node API integration and do not accept `--command`.
For validation, `testdrive --command "npx vitest run --config ci.ts"` preserves
the actual CLI path. To validate ddtest’s runner instead, use
`testdrive --command "ddtest run --framework vitest --vitest-config ci.ts"`.
Testdrive records the execution path and verifies each worker’s exact file
assignment in a disposable copy; customer plans are preserved.
Use `--vitest-config` to select a configuration file; see
[Vitest integration](#vitest-integration) for migration instructions.

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

## Vitest Integration

DDTest launches its own Node adapter using the project's installed `vitest/node`
API. Install Vitest **1.6 or higher**, `dd-trace` **5.125.0 or higher**, and any
configured coverage or environment packages before running DDTest. Run from the
project directory where Node can resolve those packages. DDTest does not install
Vitest through `npx`.

Both planning and execution load the normal Vitest/Vite configuration. To use a
non-default file, pass the same `--vitest-config` value to both commands:

```bash
ddtest plan --platform javascript --framework vitest --vitest-config vitest.ci.config.ts
ddtest run --platform javascript --framework vitest --vitest-config vitest.ci.config.ts
```

Alternatively, set
`DD_TEST_OPTIMIZATION_RUNNER_VITEST_CONFIG=vitest.ci.config.ts` for both steps.
Paths are relative to the working directory. The option selects a config file;
it does not change the working directory.

Vitest owns configuration, project discovery, setup/teardown, reporters, and
coverage. DDTest runs once with `run: true` and `watch: false`, and executes only
the discovered project/pool specifications whose canonical file paths belong to
the worker's assignment. The same file can still run in multiple configured
projects. Vitest 3–5 use the public specification API; Vitest 1.6–2 use a separate
legacy adapter. Configuration and discovery errors fail the operation rather
than falling back to a filesystem glob.

### Migrating from a Vitest command wrapper

`--command` and `DD_TEST_OPTIMIZATION_RUNNER_COMMAND` are rejected for Vitest,
including direct `vitest`, `node .../vitest.mjs`, `npx`, `pnpm`, and npm-script
invocations. Remove the command override and move Vitest options to its config:

| Previous command option | Configuration or replacement |
| --- | --- |
| `--config vitest.ci.config.ts` | DDTest's `--vitest-config vitest.ci.config.ts` |
| `--project 'unit*'` | `test.project: ['unit*']` |
| `--testNamePattern smoke` | `test.testNamePattern: 'smoke'` |
| `--reporter json --outputFile results.json` | `test.reporters: ['json']`, `test.outputFile: 'results.json'` |
| `--coverage` | `test.coverage.enabled: true` plus an installed coverage provider |
| `--passWithNoTests` | `test.passWithNoTests: true` |
| Test-file arguments | Positional selections to `ddtest plan`, then `ddtest run` |

For example, replace `--command 'pnpm exec vitest run --config
vitest.ci.config.ts --project unit*'` with `--vitest-config vitest.ci.config.ts`
and add `project: ['unit*']` under that file's `test` configuration. Regenerate
any saved plan after changing the configuration or selection.

Shell setup and npm lifecycle scripts are not executed by the adapter. Run
preparation steps before DDTest, and export required environment variables to
DDTest. Keep Node flags and project loaders in `NODE_OPTIONS`; DDTest preserves
them. For Yarn Plug'n'Play, make its loaders available to Node in that environment.
Choose the Node version on `PATH` before starting DDTest.

Use Vitest directly for watch mode, UI, benchmarks, report merging, and other
CLI-only workflows. For snapshot updates, run Vitest with `--update`, or
explicitly enable `test.update` in a dedicated config. Missing snapshots in CI
still fail by default. Prefer DDTest's worker/CI-node settings for splitting;
a configured Vitest `test.shard` additionally shards each assigned batch.

### Tracing

DDTest adds the Datadog CI require and register import to worker `NODE_OPTIONS`.
It preserves an existing Datadog register import; otherwise it uses
`DD_TRACE_ESM_IMPORT`, the `register.js` next to an external tracer's `ci`
directory, or the project-local `dd-trace/register.js`, in that order.
The GitHub action exports the paths, so manually setting Datadog `NODE_OPTIONS`
is unnecessary. The tracer's require follows existing project loaders.
Discovery removes both Datadog options to avoid instrumenting file listing.
The adapter itself is a directly executed script, not a `NODE_OPTIONS` preload.

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


## Testdrive configuration checks

Before running a Jest suite, use `ddtest testdrive --framework jest --check-only --yes`.
Jest must already be installed and resolvable through the project, including Yarn
Plug'n'Play workspace packages using an ancestor loader. Nested package checks
remain incomplete when checkout-level workflows have not been validated. This mode loads configuration, reads registry/runtime metadata and
writes `.testoptimization/testdrive.json`. It installs no tracer, runs no tests
and edits no workflows. Unapplied prerequisites and unresolved CI environments
make the checks incomplete. Run from the package directory; use `--command` only
after reviewing required setup.

`--tracer-version` selects a fallback only: an existing project tracer takes
precedence. npm metadata uses the customer's configured registry, proxy and auth.
Runtime selectors and version files resolve to a timestamped exact release from
Node metadata. CI may use a different cached patch when setup-node does not set
`check-latest`; static checks do not prove which binary ran in CI.

Exit 0 and `checks_passed: true` certify configuration checks only. The versioned
JSON contains `schema_version: 1`, `compatibility.status: "not exercised"`, feature
statuses of `"not exercised"`, and `ci_execution.status: "not exercised"`. It omits
execution-only success and run fields. Full testdrive is still required to check
behavior and telemetry; neither local mode proves real Datadog backend processing.
Other frameworks do not support check-only in this step. These flags have no
environment-variable binding.

For Jest, full testdrive compares native results without and with instrumentation, then checks advanced features with temporary tests under the original configuration. Matching existing failures do not alone fail compatibility. It writes compact JSON and an HTML report; local checks do not execute CI or prove backend processing.

Jest compatibility uses two execution pairs when the first pair differs. A recurring difference remains a suspected regression even if another test varies. Two pairs cannot rule out coincidental or repeatable flakiness; investigate the recorded differences before attributing their cause. Reporting counts include the primary instrumented suite once; repeated comparisons and synthetic probes have separate roles. Probes isolate customer selection, coverage thresholds and native retry settings while retaining the original configuration.

Frameworks without a controlled adapter retain their ordinary feature-enabled telemetry check and captured diagnostics under `.testoptimization/testdrive/`. Their stable HTML/JSON reports identify the result as telemetry-only; a passing exit does not certify behavioral compatibility or advanced features.

## Validate all discovered Jest configurations

Use `ddtest testdrive --framework jest --all --yes` to validate every discovered Jest CI configuration. Known build prerequisites run in disposable copies. Use `--all --check-only` to inspect configurations without builds or tests. `--command` selects one command, without validating unrelated CI configurations. Both modes retain one HTML/JSON report pair.


### Configuration execution and preparation

A full controlled Jest testdrive exits 0 exactly when its JSON `success` is true;
failed or incomplete validation exits 1. Usage errors, such as invalid flags or
`--all` with `--command`, also exit 1. Build-tool exit codes remain in each
preparation result. Check-only exits 0 for `checks_passed: true`, without claiming
execution success. A focused `--command` pass certifies that command; the repository
summary can remain INCOMPLETE for other configurations. Default execution and
`--all` additionally require the discovered scope to be complete.

Matching default/focused commands use a single unambiguous CI environment.
Conflicting environments stay visible in `environment_differences`; use `--all`
to validate them independently. Commands keep package-local dependencies and
explicit Node executables. An arbitrary executable path still leaves static CI
runtime compatibility unverified when its version cannot be inferred safely.

Preparation runs customer code. Bundlers marked for review, package build lifecycle
scripts and dependency changes require `--allow-reviewed-builds`; they may use the
network. Missing tools are reported as `tool_not_installed`. Literal substitutions
are applied by Go rather than system sed; regex/backreference patches require
review. `--timeout` defaults to ten minutes per subprocess and kills its ordinary
process group on expiry. The limit also applies to each full-suite run, baseline
and reporting-only alike, so raise it for suites that take longer; the error names
the command that hit the limit. Other frameworks retain their existing
execution contract until their controlled adapter is added.

Only preparation and `ddtest run` validation require a copy. The preview estimates
the logical bytes and execution checks free space before using clonefile/reflink,
with an independent byte-copy fallback. Copies preserve timestamps, dependencies and
links, and retain Git metadata for source identities. A copy with preparation steps
omits unrelated Git-ignored output/cache trees, which those steps rebuild; a copy
without them keeps Git-ignored inputs such as a locally compiled `dist/`.
PnP runtime/dependency and local environment inputs remain available. Declared
config files and patch targets cannot resolve outside the checkout. Windows
symlink creation can require Developer Mode or additional privilege.

Marked abandoned configuration copies and sessions are recovered under the
repository lock on the next invocation. On macOS and Linux the lock lives in
`/tmp`, like process-ownership storage, whatever `TMPDIR` is, so two invocations
of one checkout with different `TMPDIR` values still exclude each other.
Unmarked files and other repositories' storage are preserved. Failed full attempts replace old JSON evidence and remove
stale HTML; check-only can retain one explicitly historical execution and leaves
its HTML unchanged. HTML keeps every instrumented reporting snapshot, including
repeat pairs, while aggregate execution counts include each primary run once.
Configuration runs remain sequential while platform detection uses a scoped
process working directory; changing all adapters to explicit working directories
belongs to the after-stack refactor.
### Vitest testdrive validation

Run `ddtest testdrive --framework vitest --all --check-only --yes` to inspect
all statically discovered CI configurations without running tests or installing
a tracer. Then run `ddtest testdrive --framework vitest --all --yes` for separate
baseline/reporting comparisons and six controlled feature scenarios per
configuration. Both native CLI invocations and supported `ddtest run` commands
are recognized. Unknown setup and runner options remain unvalidated.

For a focused check, use `--command "npx vitest run --config ci.ts"` instead of
`--all`. Preserve CI's configuration and selection options. Testdrive requires
one Node project with the forks or threads pool; browser, typechecking and
multiple-project configurations are explicitly inconclusive. Known version
constraints follow dd-trace capabilities; a preflight pass alone does not prove
support. Matching pre-existing test failures are acceptable when outcomes and
telemetry agree. A failed feature scenario keeps validation incomplete.

The JSON report includes `execution_path` (`vitest-cli` or
`ddtest-run-node-api`), native counts and worker evidence. Skipping on the runner
path requires a successful backend negotiation and an empty persisted plan,
with no worker started. Keep `.testoptimization/testdrive.json` and
`.testoptimization/report.html`; raw events and temporary probes are removed.
Tests use a local mock backend. Real CI and Datadog processing are not exercised.

Vitest package-script shorthand (`pnpm vitest` or `yarn vitest` when that script
exists) must use `--all` for setup-aware discovery, or an explicitly reviewed
`exec vitest` command. Unsupported CI runner environment settings and preloads
are reported as unvalidated. Probe selection uses the installed CLI parser;
validation-owned scalar options are replaced rather than duplicated.

Vitest validation replaces customer reporters and their output destinations with
private native results, recording the replaced reporter names. It preserves
full-suite filters, shards and coverage gates. Only synthetic probes drop those
selections and relax all global/per-glob thresholds; coverage collection remains
on. Both paths always disable coverage `autoUpdate`, so validation cannot rewrite
the customer configuration. Unhandled errors participate in outcome comparison,
even when test JSON and a pre-existing failing exit code are unchanged.

Without `--command`, Vitest selects a single understood CI invocation or the
package test script. Setup that cannot safely be separated requires `--all` or
review. Only `run` and no-subcommand invocations are accepted; watch, benchmark,
list, related and typecheck modes remain explicitly unvalidated. Supported
`DD_TEST_OPTIMIZATION_RUNNER_*` settings become recorded command options on the
Node API path; saved-plan, CI-node and custom-worker modes still require review.
The runner report records the binary path/version used locally and warns about
recognized different CI release pins. It does not certify execution of that
remote binary. The controlled CI matrix exercises Vitest 3, 4 and 5; unknown
future versions still require effective configuration and live feature evidence.

### Mocha testdrive validation

`ddtest testdrive --framework mocha --all --check-only --yes` resolves the local
Mocha/tracer and reviews CI initialization and Istanbul/NYC wrappers without
running tests. Follow it with `ddtest testdrive --framework mocha --all --yes`.
The full run compares native results and telemetry for every discovered command,
then checks six controlled features. CLI and statically identified programmatic
Mocha runners retain their requires, hooks, reporters and options. Known npm
lifecycle hooks and literal sequential scripts are preserved; ambiguous setup
stays unvalidated. Focused diagnostics can use `--command 'npm test'` instead of
`--all`.

Use the project's installed NYC for per-test coverage; c8 alone is insufficient.
Do not duplicate an existing NYC wrapper. Full-suite coverage thresholds stay
intact; only synthetic probes relax thresholds and replace test selection.
Missing coverage from real application source remains incomplete even when all
feature probes pass. Native ESM requires actual Istanbul instrumentation; the
tracer's ESM loader alone does not supply coverage.

Mocha execution uses disposable copies so build, coverage and companion-tool
outputs stay outside the original checkout. Keep the JSON/HTML report pair.
Matching existing test failures are acceptable; missing results, changed
outcomes and failed feature controls are not success. Real CI and Datadog backend
processing are not exercised.

Validation preserves the original CI command; installing NYC does not add a
wrapper automatically. To introduce Istanbul coverage, install `nyc` first and
change the CI command explicitly (for example `npx --no-install nyc -- npm test`),
then validate that same command. Existing Babel/Istanbul instrumentation should
be checked before adding another wrapper. Native ESM is not covered by NYC's
require hook; feature results remain separate from this coverage limitation.
Reports show the covered-test ratio, which describes observed coverage rather
than proof that every application file was instrumented.

Probes replace file/name selection, disable native retries/watch/delay, preserve
requires/root hooks/UI/reporters, and override nyc/c8 coverage checks with
`--check-coverage=false`. Full-suite runs preserve the original coverage gates.
Watch commands are rejected; other hanging commands hit `--timeout`, which
terminates the process group. Place TMPDIR outside the checkout.

The following conservative discovery exceptions apply to Mocha only. Shared
Jest/Vitest discovery continues to require review where lifecycle semantics
cannot be represented safely; it is intentionally not relaxed by this PR.

| Rule | Reason and boundary |
| --- | --- |
| `mochify` or a repository script importing Puppeteer | Browser execution is outside Node Mocha validation; never used as proof of a Node test. |
| Browser shell wrappers containing only `set`, `echo`, `printf`, `mochify` | Inspectable metadata plus one browser runner. Unknown commands and substitutions remain unresolved. |
| Literal `$(which X)` in browser-only steps | Executable lookup metadata only; no arbitrary command substitution is accepted. |
| An opaque Node helper named `build` | Retained as an explicit package build requiring review, never inferred to be safe. |
| npm pre/post hooks and sequential `run-s`/`npm-run-all` | Preserve the actual npm lifecycle command, not a guessed runner. Wildcards/concurrent orchestration remain unresolved. Yarn lifecycle hooks still require review, including Yarn v1 hooks. |
| `npx --no-install` | An explicit local executable boundary. It does not authorize downloading an unknown tool. |

The preview lists retained lifecycle hooks and warns that any may upload or
publish, including through indirect scripts. Those hooks run in the disposable
copy for both comparisons; synthetic probes omit them while retaining the
runner's scoped environment and working directory.

### Playwright validation

Use `ddtest testdrive --framework playwright --all --check-only --yes` to list the discovered test configuration and check runtime/tracer compatibility without installing a tracer or executing tests. Install the repository dependencies and required Playwright browsers first. Configuration loading executes project JavaScript under the same process ownership supervision. The Unix supervisor uses Bash and `ps`; idle guards exit after their group empties and never load the tracer.

Run `ddtest testdrive --framework playwright --all --yes` for all discovered commands, or replace `--all` with `--command 'npm test'` for a simple package alias. Testdrive runs in a temporary checkout, preserving the Playwright configuration, browser projects and native retries. It adds the same builtin summary/native-result reporter through `PW_TEST_REPORTER` alongside the customer reporters and uses the same process supervisor in both modes, redirects artifacts, compares test identities/attempts/failures and exit codes, and checks local telemetry. Matching existing failures are acceptable. This baseline disables dd-trace but is not an unmodified shell execution. Asynchronous non-IPC children run in private process groups so browser and server descendants can be stopped even after their launcher exits. Node IPC children retain their IPC channel and receive the ownership preload even with a custom environment. Feature probes replace the actual CLI’s file, project, shard and repeat selectors after package aliases expand and disable dependency/teardown projects with `--no-deps`; the full comparison retains those original selections. Probe runs record both the launcher command and the resolved native command.

Each executed project gets separate retries, EFD, quarantine, disabled-test and attempt-to-fix controls. Playwright TIA skipping and per-test coverage are unsupported; the report records that exception without counting it as a passing check. Opaque setup, incomplete native results or unsupported detached process options keep validation incomplete, with a structured compatibility code for supervision failures. Literal leading environment assignments in `--command` use the shared command parser. Owned server/browser processes stop when validation ends or the owning connection closes; reused customer servers are preserved.

Keep `.testoptimization/report.html` and `testdrive.json`. HTML retains full test-command output; JSON retains bounded diagnostics and aggregate evidence. Actual CI and Datadog backend processing remain untested locally.

### Cypress validation

Install the repository dependencies and the required Cypress browser first. Run `ddtest testdrive --framework cypress --all --check-only --yes` to resolve the declared CI commands, runtimes and tracer metadata. Run without `--check-only` to compare native Cypress outcomes without dd-trace and with reporting-only instrumentation. Matching existing failures are acceptable. Both modes use the same native observer and process supervisor; Cloud recording and parallel grouping are disabled locally.

`--all` retains CLI/package aliases and supported `cypress-io/github-action` API options, including build/start/readiness inputs, browsers, configs, hooks and reporters. The action API uses the action's declared Node runtime; run locally with that same major and a compatible tracer. Unknown wrappers, unrecognized setup and unavailable browser/OS variants stay unvalidated. A focused `--command 'npm test'` checks only that command. Without an override, ddtest selects one unambiguous declared CI invocation, or a unique package script when CI has none. Multiple invocations require `--command` or `--all`; no Cypress command is guessed. A successful focused command exits 0 and certifies only that selected validation; other configurations and actual CI execution remain unvalidated. Failed required checks exit nonzero. `--check-only` exits 0 when its checks pass, while execution remains incomplete (without claiming successful execution). Cypress component testing and Windows process supervision are explicitly unsupported by this testdrive.

Five controlled checks validate retries, early flake detection, quarantine, disabled tests and attempt-to-fix. **Cypress TIA skipping and application coverage validation are deferred**. They are neither passed nor unsupported by the tracer, and missing application coverage does not block those five controls. Preserve existing coverage instrumentation. Real Datadog/Cypress Cloud processing and actual CI execution are not exercised locally.

Execution uses disposable copies and removes owned browser/server processes and artifacts. Keep the report pair in `.testoptimization/`; JSON records commands, observed counts and bounded diagnostics, while HTML retains full test output. Unknown environment-printing utilities remain setup review items rather than receiving repository-specific exemptions.
