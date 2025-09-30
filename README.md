# DDTest

DDTest is a single‑binary CLI that plans and runs your tests in parallel alongside your existing test commands — or as a drop‑in runner. It discovers the tests in your repo, fetches Datadog Test Optimization data, configures your CI to use the right number of workers, and distributes test execution across workers.

You need it when Test Impact Analysis shrinks the test workload but CI still launches too many nodes, and skipped tests leave splits wildly uneven. Start by generating a plan (`ddtest plan`) you can feed to any runner, or let DDTest execute the tests for you with `ddtest run` in CI.

Currently supported languages and frameworks:

- Ruby (RSpec)

## Installation

### From Source

```bash
git clone https://github.com/DataDog/datadog-test-runner.git
cd datadog-test-runner
make build
```

This will create the `ddtest` binary in the current directory. It requires Go 1.24+.

## Usage

DDTest ships as a single CLI with two primary sub‑commands: `plan` and `run`.

Use `plan` to discover tests, fetch Datadog Test Optimization data once, and compute a parallelization plan you can reuse on any CI node; use `run` to execute that plan locally or in CI. If a plan is missing, `run` will generate it on the fly.

### Available commands

#### ddtest plan

Creates a reusable execution plan under `.testoptimization/` without running tests. During the planning phase DDTest:

- Fetches Test Optimization data from Datadog (settings, known tests, skippable tests) and caches it.
- Discovers tests in your repo and writes them to `.testoptimization/tests-discovery/tests.json`.
- Determines which test files are not skipped by Test Impact Analysis
- Computes the parallelism and a split of test files across workers.
- Saves artifacts you can distribute to CI nodes or feed to another runner.

**Example:**

```bash
ddtest plan \
  --platform ruby \
  --framework rspec \
  --min-parallelism 8 \
  --max-parallelism 32
```

This prepares the plan and writes it to `.testoptimization/` folder for later reuse.
The folder can be copied to to any runner and will be used by Datadog's test optimization
library automatically.

The folder contents are:

```
.testoptimization/
  cache/                     # Datadog responses cached locally
    settings.json
    known_tests.json
    skippable_tests.json
    test_management_tests.json
  tests-discovery/
    tests.json               # JSON stream of discovered tests
  test-files.txt             # All non-skipped test files to execute
  parallel-runners.txt       # Chosen worker count (single integer)
  skippable-percentage.txt   # % tests skipped by TIA
  tests-split/
    runner-0                 # File list for worker 0
    ...
    runner-(N-1)             # File list for worker N-1
  github/
    config                   # (GHA only) matrix JSON with ci_node_index entries
```

You can use `test-files.txt` or `tests-split/runner-X` files to feed them directly to
your existing test runner.

Example for Knapsack Pro:

```bash
KNAPSACK_PRO_TEST_FILE_LIST_SOURCE_FILE=.testoptimization/test-files.txt bundle exec rake knapsack_pro:queue:rspec
```

#### ddtest run

Runs tests using the framework you specify. If .testoptimization/ exists, DDTest will use its precomputed split; otherwise it first runs plan and then executes.

**Single machine, multiple CPUs example:**

```bash
ddtest run --platform ruby --framework rspec
```

The default --min-parallelism and --max-parallelism equal the machine’s vCPU count, so you get full CPU utilization without extra flags.

**Multiple CI nodes (static split per node)**

First run `ddtest plan` once, share `.testoptimization/` folder to all nodes (put it into the folder where you run your tests from), then on each node run only its slice:

```bash
ddtest run --platform ruby --framework rspec --ci-node <CI_NODE_INDEX>
```

In CI‑node mode, DDTest also fans out across local CPUs on that node and further splits the assigned files between them.

### Settings (flags and environment variables)

| CLI flag            | Environment variable                          |    Default | What it does                                                                                                          |
| ------------------- | --------------------------------------------- | ---------: | --------------------------------------------------------------------------------------------------------------------- |
| `--platform`        | `DD_TEST_OPTIMIZATION_RUNNER_PLATFORM`        |     `ruby` | Language/platform (currently supported values: `ruby`).                                                               |
| `--framework`       | `DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK`       |    `rspec` | Test framework (currently supported values: `rspec`).                                                                 |
| `--min-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM` | vCPU count | Minimum workers to use for the split.                                                                                 |
| `--max-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM` | vCPU count | Maximum workers to use for the split.                                                                                 |
| `--ci-node`         | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE`         | `-1` (off) | Restrict this run to the slice assigned to node **N** (0‑indexed). Also parallelizes within the node across its CPUs. |
| `--worker-env`      | `DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV`      |       `""` | Template env vars per local worker (e.g., isolate DBs): `--worker-env "DATABASE_NAME_TEST=app_test{{nodeIndex}}"`.    |

### GitHub Actions integration example

The plan job computes the split and emits a matrix; the run job downloads the artifacts and executes only its slice.

```yaml
name: CI with DDTest

on: [push]

env:
  DD_TEST_OPTIMIZATION_RUNNER_PLATFORM: ruby
  DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK: rspec

jobs:
  dd_plan:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.matrix.outputs.matrix }}
    steps:
      - uses: actions/checkout@v4
      - name: Setup Ruby
        uses: ruby/setup-ruby@v1
        with:
          bundler-cache: true
      - name: Configure Datadog Test Optimization
        uses: datadog/test-visibility-github-action@v2
        with:
          languages: ruby
          api_key: ${{ secrets.DD_API_KEY }}
          site: datadoghq.com
      - name: Plan test execution with DDTest
        run: ddtest plan
        env:
          DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM: 1
          DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM: 8
      - id: matrix
        run: cat .testoptimization/github/config >> $GITHUB_OUTPUT
      - uses: actions/upload-artifact@v4
        with:
          name: dd-artifacts
          path: .testoptimization
          include-hidden-files: true

  dd_test:
    runs-on: ubuntu-latest
    needs: [dd_plan]
    strategy:
      fail-fast: false
      matrix: ${{ fromJson(needs.dd_plan.outputs.matrix) }}
    env:
      DD_TEST_OPTIMIZATION_RUNNER_CI_NODE: ${{ matrix.ci_node_index }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/download-artifact@v4
        with:
          name: dd-artifacts
          path: .testoptimization
      - name: Setup Ruby
        uses: ruby/setup-ruby@v1
        with:
          bundler-cache: true
      - name: Configure Datadog Test Optimization
        uses: datadog/test-visibility-github-action@v2
        with:
          languages: ruby
          api_key: ${{ secrets.DD_API_KEY }}
          site: datadoghq.com
      - name: Run tests
        run: ddtest run
        env:
          DD_TEST_SESSION_NAME: ddtest-runner-${{ matrix.ci_node_index }}
```

DDTest automatically writes the matrix file at `.testoptimization/github/config` that looks like:

```
matrix={"include":[{"ci_node_index":0},{"ci_node_index":1},{"ci_node_index":2}]}
```

You can cat it to `$GITHUB_OUTPUT` to make it available for the test job.

## Development

### Prerequisites

- Go 1.24.5 or later

### Building

```bash
make build
```

### Testing

```bash
make test
```

### Formatting and Vetting

```bash
make fmt
make vet
```

### Running from Source

```bash
make run
```
