# DDTest

DDTest is a CLI tool that plans and runs your tests in parallel alongside your existing test commands, or as a drop-in runner. It discovers tests in your repo, fetches Datadog Test Optimization data, and chooses how many CI nodes or local workers to use.

You need it when Test Impact Analysis shrinks the test workload but CI still launches too many CI nodes, and skipped tests leave work unevenly distributed. Start by generating a plan (`ddtest plan`) you can feed to another runner, or let DDTest execute the tests for you with `ddtest run` in CI.

Currently supported languages and frameworks:

- Ruby (RSpec, Minitest)

## Prerequisites

Before using DDTest, you must have **Datadog Test Optimization** already set up and enabled with a Datadog Test Optimization library for your language and framework. DDTest relies on this integration to discover your tests and plan test execution accordingly.

Minimum supported library versions:

- Ruby: `datadog-ci` gem **1.31.0** or higher

For instructions on setting up Test Optimization, see the [Datadog Test Optimization documentation](https://docs.datadoghq.com/tests/setup/).

## Installation

### Download precompiled binary

This project uses GitHub Releases for distribution.

Use `gh` command line tool to download the latest release in GitHub actions:

```yaml
- name: Download ddtest binary
  run: |
    mkdir -p bin
    gh release download --repo DataDog/ddtest --pattern "ddtest-linux-amd64" --dir bin
    mv bin/ddtest-linux-amd64 bin/ddtest
    chmod +x bin/ddtest
  env:
    GH_TOKEN: ${{ github.token }}
```

...or use `curl`:

```bash
mkdir -p bin
curl -fsSL https://github.com/DataDog/ddtest/releases/latest/download/ddtest-linux-amd64 -o bin/ddtest
chmod +x bin/ddtest
```

The list of available precompiled artifacts is on [release page](https://github.com/DataDog/ddtest/releases/latest).

### Compile from source

```bash
git clone https://github.com/DataDog/ddtest.git
cd ddtest && make build
```

This will create the `ddtest` binary in the current directory. It requires Go 1.26.2+.

## Usage

DDTest ships as a CLI tool `ddtest` with two primary sub-commands: `plan` and `run`.

Use `plan` to discover tests, fetch Datadog Test Optimization data once, and compute which test files each CI node or local worker should run; use `run` to execute that plan locally or in CI. If a plan is missing, `run` will generate it on the fly.

DDTest is meant to run in CI. Local runs are possible when you want to reuse
CI's skippable tests on your machine; see
[Running locally with CI skippable tests](docs/local-ci-skippable-tests.md).
For planning-step performance tips and framework-specific setup notes, see
[Best practices](docs/best_practices.md).

### Terminology

- A **runner** is a program that runs tests. DDTest is a runner, and DDTest can also produce file lists for other runners such as Knapsack Pro or `parallel_tests`.
- A **CI node** is one CI execution environment, such as a GitHub Actions job executor, CircleCI parallel container, Kubernetes pod, VM, or local machine.
- A **worker** is a Ruby process started by DDTest to execute tests. One CI node can run one worker or several workers.

`ddtest plan` decides how many CI nodes or local workers are useful and assigns
test files to them. `ddtest run --ci-node N` runs the files assigned to CI node
`N`; inside that CI node, `--ci-node-workers` controls how many worker processes
DDTest starts.

### Available commands

#### ddtest plan

Creates a reusable execution plan under `.testoptimization/` without running tests. During the planning phase DDTest:

- Fetches Test Optimization data from Datadog (settings, known tests, skippable tests) and caches it.
- Discovers tests in your repo and writes them to `.testoptimization/tests-discovery/tests.json`.
- Determines which test files are not skipped by Test Impact Analysis.
- Assigns runnable test files to CI nodes or local workers.
- Saves plan files you can distribute to CI nodes or feed to another runner.

**Example:**

```bash
ddtest plan \
  --platform ruby \
  --framework rspec \
  --min-parallelism 8 \
  --max-parallelism 32
```

This prepares the plan and writes it to `.testoptimization/` folder for later reuse.
Copy `.testoptimization/` to any CI job that runs `ddtest run` or reads DDTest's
plan file lists. For the full file layout and formats, see
[Plan file layout](docs/layout.md).

#### ddtest run

Runs tests using the framework you specify. If `.testoptimization/` exists, DDTest will use its precomputed plan; otherwise it first runs plan and then executes.

**Single CI node, multiple workers example:**

```bash
ddtest run --platform ruby --framework rspec
```

On one CI node, the default `--min-parallelism` and `--max-parallelism` equal the available physical CPU core count, so DDTest can start one worker per physical core without defaulting to one worker per hyperthread.

**Multiple CI nodes**

First run `ddtest plan` once, share the `.testoptimization/` folder to all CI nodes, then on each CI node run only its assigned files:

```bash
ddtest run --platform ruby --framework rspec --ci-node <CI_NODE_INDEX>
```

In CI-node mode, DDTest uses one local worker by default so database and other per-worker resources stay easy to isolate. To fan out within each CI node, set `--ci-node-workers` to a positive integer, or use `--ci-node-workers ncpu` to use the node's available physical CPU cores.

`--worker-env` supports `{{nodeIndex}}` and `{{workerIndex}}` placeholders. `{{nodeIndex}}` is the CI node index from `--ci-node` or `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE`; in single-node runs, it is `0`. `{{workerIndex}}` is the worker process index within the current CI node, starting at `0`. If a CI node uses multiple workers, each worker receives the same `{{nodeIndex}}` value and a different `{{workerIndex}}` value.

DDTest automatically sets `DD_TEST_SESSION_NAME` for each worker to `<DD_SERVICE>-node-<nodeIndex>-worker-<workerIndex>` when the variable is not already set. If you set `DD_TEST_SESSION_NAME` yourself, DDTest preserves it and expands the same `{{nodeIndex}}` and `{{workerIndex}}` placeholders before starting each worker.

### Integrating with third party test runners

You can use `.testoptimization/runner/test-files.txt` or
`.testoptimization/runner/tests-split/runner-X` files to feed DDTest's plan into
another test runner.

Example for Knapsack Pro:

```bash
KNAPSACK_PRO_TEST_FILE_LIST_SOURCE_FILE=.testoptimization/runner/test-files.txt bundle exec rake knapsack_pro:queue:rspec
```

### Settings (flags and environment variables)

| CLI flag            | Environment variable                          |    Default | What it does                                                                                                                                                                                                         |
| ------------------- | --------------------------------------------- | ---------: | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--platform`        | `DD_TEST_OPTIMIZATION_RUNNER_PLATFORM`        |     `ruby` | Language/platform (currently supported values: `ruby`).                                                                                                                                                              |
| `--framework`       | `DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK`       |    `rspec` | Test framework (currently supported values: `rspec`, `minitest`).                                                                                                                                                    |
| `--min-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM` | physical CPU count | Minimum count DDTest considers when planning. Interpret it as CI nodes in CI-node mode, or workers in a single-node run.                                                                                             |
| `--max-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM` | physical CPU count | Maximum count DDTest considers when planning. Interpret it as CI nodes in CI-node mode, or workers in a single-node run.                                                                                             |
| `--ci-job-overhead` | `DD_TEST_OPTIMIZATION_RUNNER_CI_JOB_OVERHEAD` | `25s` | Modeled overhead for adding one more CI node. Accepts durations such as `25s`, `1m`, `1500ms`, or `0s` to disable this bias. Increase it to use fewer CI nodes; decrease it to prefer faster wall time.              |
| `--ci-node`         | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE`         | `-1` (off) | Restrict this run to files assigned to CI node **N** (0-indexed).                                                                                                                                                    |
| `--ci-node-workers` | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS` |        `1` | Number of workers to start on this CI node. Use a positive integer, or `ncpu` to use the node's available physical CPU cores. Tests assigned to a CI node are distributed among this many workers.                  |
| `--worker-env`      | `DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV`      |       `""` | Template env vars per worker: `--worker-env "DATABASE_NAME_TEST=app_test{{nodeIndex}}_{{workerIndex}}"`. `{{nodeIndex}}` is the CI node index (`0` for single-node runs); `{{workerIndex}}` is the worker process index within that CI node. |
| `--command`         | `DD_TEST_OPTIMIZATION_RUNNER_COMMAND`         |       `""` | Override the default test command used by the framework. When provided, takes precedence over auto-detection (e.g., `--command "bundle exec custom-rspec"`).                                                         |
| `--tests-location`  | `DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION`  |       `""` | Custom glob pattern to discover test files (e.g., `--tests-location "custom/spec/**/*_spec.rb"`). Defaults to `spec/**/*_spec.rb` for RSpec, `test/**/*_test.rb` for Minitest.                                       |
| `--runtime-tags`    | `DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS`    |       `""` | JSON string to override runtime tags used to fetch skippable tests. Useful for local development on a different OS than CI (e.g., `--runtime-tags '{"os.platform":"linux","runtime.version":"3.2.0"}'`).             |
|                     | `DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED`  |     `true` | Print human-readable plan and run reports. Set to `false` to disable them.                                                                                                                                           |

#### Note about the `--command` flag

When using `--command`, do not include the `--` separator or test files in your command. DDTest automatically appends test files and framework-specific flags to the command you provide.

**Incorrect usage:**

```bash
# This will fail - test files should not be included
ddtest run --command "bundle exec rspec -- spec/models/"

# This will also fail - the -- separator causes issues
ddtest run --command "bundle exec my-wrapper --"
```

**Correct usage:**

```bash
# Just provide the command - DDTest handles test files
ddtest run --command "bundle exec rspec"

# You can include flags for your wrapper
ddtest run --command "bundle exec my-wrapper --profile"
```

If your command contains `--`, DDTest will emit a warning and automatically remove the `--` separator and anything after it.

## CI configuration examples

- [GitHub Actions](docs/examples/github-actions.md)
- [CircleCI](docs/examples/circleci.md)

## Parallelism selection

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
