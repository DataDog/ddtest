# Settings

Supported DDTest settings can be passed as a CLI flag or as an environment variable
(except environment-only settings noted below).
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
| `--force-full-test-discovery` | `DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY` | | `false` | Force full test discovery when the framework supports it, including in suite-level skipping mode. |
| `--strict-discovery` | `DD_TEST_OPTIMIZATION_RUNNER_STRICT_DISCOVERY` | | `false` | Fail planning when full test discovery fails. Cancelled full discovery still uses fast test file discovery fallback. |
| `--runtime-tags` | `DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS` | `DD_TEST_OPTIMIZATION_RUNTIME_TAGS` | `""` | JSON string to override runtime tags used to fetch skippable tests. Useful for local development on a different OS than CI, such as `--runtime-tags '{"os.platform":"linux","runtime.version":"3.2.0"}'`. |
| | `DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED` | | `true` | Print human-readable plan and run reports. Set to `false` to disable them. |

## Pytest static discovery

Set `DD_TEST_OPTIMIZATION_RUNNER_PYTEST_STATIC_DISCOVERY=true` to
try discovering individual pytest tests without starting pytest or importing test
modules. This is opt-in, disabled by default, with no CLI flag.
It still starts a lightweight Python standard-library helper; it does not remove
normal platform/prerequisite checks or change how tests execute.

```bash
DD_TEST_OPTIMIZATION_RUNNER_PYTEST_STATIC_DISCOVERY=true \
  ddtest plan --platform python --framework pytest \
  --tests-location 'tests/**/test_*.py' \
  --tests-exclude-pattern 'tests/integration/**' \
  --command 'pytest -q --ddtrace'
```

Supply accurate file include/exclude patterns. Supported commands are `pytest`
(console scripts with a Python shebang) and `python -m pytest`, including
versioned Python executables. Existing positional arguments must be selected
Python files, not directories or node IDs. Wrapper commands fall back to pytest.

The initial subset supports:

- Plain `test*` functions and methods in `Test*` classes without inheritance or
  custom constructors, including nested classes.
- `@pytest.mark.parametrize` with literal lists/tuples of values, multiple
  arguments, stacked decorators, literal `ids`, and unmarked `pytest.param`
  entries with literal IDs. Values can be strings, numbers, booleans, `None`,
  lists, tuples, and dictionaries. Test names, duplicate IDs, and parameter
  serialization follow the tracer's identities.
- Presentation options in the command, `PYTEST_ADDOPTS`, and config `addopts`:
  `-q`/`-v` (including repeats), `-s`, `-r` summary flags, `--ddtrace`, and common
  color, capture, traceback, and warning-display options. Unknown options,
  selectors such as `-k`/`-m`/`--lf`, and malformed quoting trigger fallback.
- A small allowlist of configuration in the working directory, including
  `testpaths`, `python_files`, and safe `addopts`. Static discovery with a
  `pyproject.toml` requires Python 3.11+.

`conftest.py`, non-ddtrace auto-loaded plugins, `PYTEST_PLUGINS`, fixtures,
collection hooks, imported tests, other decorators/marks (including skip marks),
dynamic parameter values or IDs, indirect parametrization, ambiguous roots, and
unsupported module-level code trigger normal pytest collection instead. No
partial inventory is used. A warning explains the fallback; successful static
discovery logs `Pytest static discovery succeeded`.

This is not a general Python/pytest evaluator: use it only when imported
application/helper modules do not mutate pytest collection. Normal discovery
caches are neither read nor refreshed while static discovery is enabled, so
eligibility is rechecked on every invocation and static inventories cannot
be reused after disabling it.
