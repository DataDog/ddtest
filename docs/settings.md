# Settings

Every DDTest setting can be passed as a CLI flag or as an environment variable.
CLI flags take precedence over environment variables.
When neither supplies a platform or framework, DDTest detects it from the current
directory. An explicit framework also selects its platform. Multiple matching
platforms or frameworks require explicit selection.

| CLI flag | Environment variable | Env alias | Default | What it does |
| --- | --- | --- | ---: | --- |
| `--platform` | `DD_TEST_OPTIMIZATION_RUNNER_PLATFORM` | | auto-detected | Language/platform. Currently supported: `ruby`, `python`, `javascript`. |
| `--framework` | `DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK` | | auto-detected | Test framework. Currently supported: `rspec`, `minitest`, `pytest`, `cucumber`, `cypress`, `jest`, `mocha`, `playwright`, `vitest`. |
| `--command` | `DD_TEST_OPTIMIZATION_RUNNER_COMMAND` | | `""` | Override the default base test command for supported framework modes. Used by RSpec and Minitest run/discovery, Cucumber, Cypress, Jest, Mocha, and Playwright run/discovery. DDTest appends selected tests and framework-specific flags. Vitest rejects this option; use `--vitest-config` and configure options in Vitest. |
| `--vitest-config` | `DD_TEST_OPTIMIZATION_RUNNER_VITEST_CONFIG` | | `""` | Vitest config file used by both planning and execution; defaults to Vitest config discovery. See [migration instructions](running.md#vitest-integration). |
| `--min-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM` | | physical CPU count | Minimum count DDTest considers when planning. Interpret it as CI nodes in CI-node mode, or workers in a single-node run. |
| `--max-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM` | | physical CPU count | Maximum count DDTest considers when planning. Interpret it as CI nodes in CI-node mode, or workers in a single-node run. |
| `--ci-job-overhead` | `DD_TEST_OPTIMIZATION_RUNNER_CI_JOB_OVERHEAD` | | `25s` | Modeled overhead for adding one more CI node. Accepts durations such as `25s`, `1m`, `1500ms`, or `0s` to disable this bias. Increase it to use fewer CI nodes; decrease it to prefer faster wall time. |
| `--target-time` | `DD_TEST_OPTIMIZATION_RUNNER_TARGET_TIME` | | `0s` | Target wall time for the selected split. Accepts durations such as `10m`, `300s`, `1500ms`, or `0s` to disable the target. DDTest first considers splits at or below this wall time; if none are possible within the min/max parallelism range, it warns and selects the split with the lowest expected wall time, ignoring CI job overhead, to get as close as possible to the target. |
| `--ci-node` | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE` | | `-1` (off) | Restrict this run to files assigned to CI node **N** (0-indexed). |
| `--ci-node-workers` | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS` | | `1` | Number of workers to start on this CI node. Use a positive integer, or `ncpu` to use the node's available physical CPU cores. |
| `--worker-env` | `DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV` | | `""` | Template env vars per worker: `--worker-env "DATABASE_NAME_TEST=app_test{{nodeIndex}}_{{workerIndex}}"`. `{{nodeIndex}}` is the CI node index (`0` for single-node runs); `{{workerIndex}}` is the worker process index within that CI node. |
| `--tests-location` | `DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION` | `KNAPSACK_PRO_TEST_FILE_PATTERN` | `""` | Custom glob pattern to filter discovered test files, such as `--tests-location "custom/spec/**/*_spec.rb"`, `--tests-location "tests/**/*_test.py"`, or `--tests-location "packages/**/__tests__/**/*.test.ts"`. Defaults to `spec/**/*_spec.rb` for RSpec, `test/**/*_test.rb` for Minitest, pytest config or `**/{test_*,*_test}.py` for pytest, and each JavaScript framework's configured/default test matching for Cucumber, Cypress, Jest, Mocha, Playwright, and Vitest. |
| `--tests-exclude-pattern` | `DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN` | `KNAPSACK_PRO_TEST_FILE_EXCLUDE_PATTERN` | `""` | Glob pattern to exclude test files from discovery, such as `--tests-exclude-pattern "spec/system/**/*_spec.rb"`. |
| `--test-discovery-cache` | `DD_TEST_OPTIMIZATION_RUNNER_TEST_DISCOVERY_CACHE` | | `""` | Path to a restored test discovery cache file. DDTest imports it before planning and refreshes the internal discovery cache after successful full discovery. |
| `--test-skipping-mode` | `DD_TESTOPTIMIZATION_TIA_TEST_SKIPPING_MODE` | | `test` | TIA skipping granularity for Ruby and Python: `test` or `suite`. Python suite mode discovers test files without running pytest collection. Invalid values fall back to `test`. JavaScript uses suite mode. |
| `--force-full-test-discovery` | `DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY` | | `false` | Force full test discovery when the framework supports it, including in suite-level skipping mode and when TIA or test skipping is disabled, so the discovery cache is still written for later runs. |
| `--strict-discovery` | `DD_TEST_OPTIMIZATION_RUNNER_STRICT_DISCOVERY` | | `false` | Fail planning when full test discovery fails. Cancelled full discovery still uses fast test file discovery fallback. |
| `--runtime-tags` | `DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS` | `DD_TEST_OPTIMIZATION_RUNTIME_TAGS` | `""` | JSON string to override runtime tags used to fetch skippable tests. Useful for local development on a different OS than CI, such as `--runtime-tags '{"os.platform":"linux","runtime.version":"3.2.0"}'`. |
| | `DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED` | | `true` | Print human-readable plan and run reports. Set to `false` to disable them. |

## Pytest suite-level discovery

Use suite mode to plan from test files without starting pytest collection or
importing test modules or conftest files:

```bash
export DD_TESTOPTIMIZATION_TIA_TEST_SKIPPING_MODE=suite
ddtest plan --platform python --framework pytest
ddtest run --platform python --framework pytest
```

Alternatively, pass `--test-skipping-mode suite` to both commands. The default
remains `test`. DDTest configures ddtrace's suite mode for test execution as well
as requesting suite-level skippables from the backend. In suite mode, DDTest
owns TIA skipping: it disables ddtrace's additional runtime TIA skipping with
`_DD_CIVISIBILITY_ITR_PREVENT_TEST_SKIPPING=true`. Files retained in the plan
therefore execute even if the tracer's backend response lists them as skippable.
Tracing and coverage collection remain enabled; normal pytest skip marks and
other execution-time selection still apply. Test mode retains tracer skipping.

Selectors such as `-k` and `-m` are applied by pytest during execution, so a
suite-mode worker may receive files with no matching tests. DDTest treats pytest
exit code 5 (no tests collected) as a successful empty batch in suite mode and
logs it. This also means an entirely deselected suite-mode run succeeds; a CI
node cannot determine whether matching tests execute on another node. Test mode
retains pytest's exit-code-5 failure behavior. Test failures, collection errors,
invalid arguments, and interruptions still fail the run in either mode.

File discovery uses the existing pytest configuration/default file patterns,
or `--tests-location`, filters to `.py` source files, and respects exclusions and
positional selection. It excludes files under `.venv`, `venv`, `.tox`,
`__pycache__`, `node_modules`, `site-packages`, and `.git` directories,
including explicit file selections. It does not enumerate test functions,
classes, fixtures, or parametrized cases. A file is omitted only when its
backend suite is skippable and the backend suite metadata maps it to the same
selected source file. Missing or stale mappings
keep the collected file runnable. This mapping requirement holds in
both discovery modes: forcing full test discovery restores collection and
the discovery cache but never changes which files suite mode may skip.
New files and suites without source metadata remain runnable; a dotted module
name or matching filename alone is not enough to skip a file. Source metadata
is obtained with suite durations, but a valid duration value is not required
to apply the skip.

Files containing `datadog_itr_unskippable`, or having an ancestor `conftest.py`
containing it, remain runnable. This is a conservative text check: comments also
keep files runnable, and read errors prevent skipping. Markers constructed
dynamically or imported from other helpers cannot be detected by this check;
add a literal marker comment to the affected test file or ancestor conftest to
keep it runnable under DDTest suite mode. These comments are DDTest planning
guards, not pytest markers: when running pytest directly with tracer TIA
skipping, use a real `@pytest.mark.skipif(False, reason="datadog_itr_unskippable")`
marker. Use accurate `--tests-location`
patterns for custom configuration locations or collectors that the existing
file discovery does not understand. Custom collectors that group multiple
suites into one file should use test mode.

Suite mode trades test-level skipping precision for cheaper planning: if a
suite is not skippable, its file remains in the execution plan. Individual test
counts are unavailable during file discovery. Planning still performs normal
platform checks, runtime-tag detection, and backend requests.

Suite mode does not read or refresh the full-discovery cache because collection
is unnecessary. `--force-full-test-discovery` explicitly restores normal pytest
collection and cache behavior, including in suite mode; it is useful for
diagnostics or refreshing a discovery cache. `--strict-discovery` alone does not
force collection. To opt out, unset the environment variable or select
`--test-skipping-mode test` and regenerate the plan.

Python test files sharing a basename are assigned to separate runners when
there are enough runners. If a group has more files than runners, DDTest keeps
all files in the plan and warns that pytest import collisions remain possible.
This also applies to a single runner. Splitting does not resolve collisions
during full test collection, which runs before distribution. Add `__init__.py`
packages, rename the files, or configure pytest's `--import-mode=importlib` to
resolve collisions that runner distribution cannot avoid.

## Testdrive-only configuration check

See [testdrive configuration checks](running.md#testdrive-configuration-checks) for command flags, result fields and limitations.
Vitest testdrive supports native CLI commands and supported `ddtest run` commands.
Use `--all --check-only --yes` before `--all --yes`, or select a focused command
with `--command "npx vitest run --config ci.ts"`. Testdrive translates `--vitest-config` (including its environment setting) into
the corresponding selected-command option; an explicit command config wins. See
[Vitest validation](running.md#vitest-testdrive-validation) for scope and evidence.

### Mocha testdrive validation

See [Mocha validation](running.md#mocha-testdrive-validation) for commands, coverage requirements and limitations.

Playwright testdrive accepts a simple original CLI command through `--command`, or discovers CI commands through `--all`. The supported controls and browser supervision are described in [Playwright validation](running.md#playwright-validation). No browser or project options are guessed.

Cypress `testdrive --all` preserves discovered CLI and action-API selections. Use `--command` for an explicit original headless alias, including its server setup. Cloud recording/grouping is disabled locally, and TIA/application coverage validation is deferred. See [Cypress validation](running.md#cypress-validation).
