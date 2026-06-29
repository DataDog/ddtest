# Settings

Every DDTest setting can be passed as a CLI flag or as an environment variable.
CLI flags take precedence over environment variables.

| CLI flag | Environment variable | Env alias | Default | What it does |
| --- | --- | --- | ---: | --- |
| `--platform` | `DD_TEST_OPTIMIZATION_RUNNER_PLATFORM` | | `ruby` | Language/platform. Currently supported: `ruby`, `python`. |
| `--framework` | `DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK` | | `rspec` | Test framework. Currently supported: `rspec`, `minitest`, `pytest`. |
| `--command` | `DD_TEST_OPTIMIZATION_RUNNER_COMMAND` | | `""` | Override the default base test command for supported framework modes. Currently used by RSpec and Minitest run/discovery, and Jest run; pytest ignores it. DDTest appends selected tests and framework-specific flags. For pytest, use `PYTEST_ADDOPTS` for pytest flags. |
| `--min-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM` | | physical CPU count | Minimum count DDTest considers when planning. Interpret it as CI nodes in CI-node mode, or workers in a single-node run. |
| `--max-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM` | | physical CPU count | Maximum count DDTest considers when planning. Interpret it as CI nodes in CI-node mode, or workers in a single-node run. |
| `--ci-job-overhead` | `DD_TEST_OPTIMIZATION_RUNNER_CI_JOB_OVERHEAD` | | `25s` | Modeled overhead for adding one more CI node. Accepts durations such as `25s`, `1m`, `1500ms`, or `0s` to disable this bias. Increase it to use fewer CI nodes; decrease it to prefer faster wall time. |
| `--ci-node` | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE` | | `-1` (off) | Restrict this run to files assigned to CI node **N** (0-indexed). |
| `--ci-node-workers` | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS` | | `1` | Number of workers to start on this CI node. Use a positive integer, or `ncpu` to use the node's available physical CPU cores. |
| `--worker-env` | `DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV` | | `""` | Template env vars per worker: `--worker-env "DATABASE_NAME_TEST=app_test{{nodeIndex}}_{{workerIndex}}"`. `{{nodeIndex}}` is the CI node index (`0` for single-node runs); `{{workerIndex}}` is the worker process index within that CI node. |
| `--tests-location` | `DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION` | `KNAPSACK_PRO_TEST_FILE_PATTERN` | `""` | Custom glob pattern to discover test files, such as `--tests-location "custom/spec/**/*_spec.rb"` or `--tests-location "tests/**/*_test.py"`. Defaults to `spec/**/*_spec.rb` for RSpec, `test/**/*_test.rb` for Minitest, and pytest config or `**/{test_*,*_test}.py` for pytest. |
| `--tests-exclude-pattern` | `DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN` | `KNAPSACK_PRO_TEST_FILE_EXCLUDE_PATTERN` | `""` | Glob pattern to exclude test files from discovery, such as `--tests-exclude-pattern "spec/system/**/*_spec.rb"`. |
| `--test-discovery-cache` | `DD_TEST_OPTIMIZATION_RUNNER_TEST_DISCOVERY_CACHE` | | `""` | Path to a restored test discovery cache file. DDTest imports it before planning and refreshes the internal discovery cache after successful full discovery. |
| `--force-full-test-discovery` | `DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY` | | `false` | Force full test discovery when the framework supports it, including in suite-level skipping mode. |
| `--runtime-tags` | `DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS` | | `""` | JSON string to override runtime tags used to fetch skippable tests. Useful for local development on a different OS than CI, such as `--runtime-tags '{"os.platform":"linux","runtime.version":"3.2.0"}'`. |
| | `DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED` | | `true` | Print human-readable plan and run reports. Set to `false` to disable them. |
