# DDTest

DDTest is a CLI tool that plans and runs your tests in parallel alongside your existing test commands - or as a drop‑in runner. It discovers the tests in your repo, fetches Datadog Test Optimization data, configures your CI to use the right number of workers, and distributes test execution across workers.

You need it when Test Impact Analysis shrinks the test workload but CI still launches too many nodes, and skipped tests leave splits wildly uneven. Start by generating a plan (`ddtest plan`) you can feed to any runner, or let DDTest execute the tests for you with `ddtest run` in CI.

Currently supported languages and frameworks:

- Ruby (RSpec, Minitest)

DDTest requires that your project is correctly set up for Datadog Test Optimization with the native library for your language. Minimum supported library versions:

- Ruby: `datadog-ci` gem **1.23.0** or higher

## Installation

### From Source

```bash
git clone https://github.com/DataDog/ddtest.git
cd ddtest && make build
```

This will create the `ddtest` binary in the current directory. It requires Go 1.24+.

## Prerequisites

Before using DDTest, you must have **Datadog Test Optimization** already set up and enabled with a Datadog Test Optimization library for your language and framework. DDTest relies on this integration to discover your tests and plan test execution accordingly.

For instructions on setting up Test Optimization, see the [Datadog Test Optimization documentation](https://docs.datadoghq.com/tests/setup/).

## Usage

DDTest ships as a CLI tool `ddtest` with two primary sub‑commands: `plan` and `run`.

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

| CLI flag            | Environment variable                          |    Default | What it does                                                                                                                                                                   |
| ------------------- | --------------------------------------------- | ---------: | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `--platform`        | `DD_TEST_OPTIMIZATION_RUNNER_PLATFORM`        |     `ruby` | Language/platform (currently supported values: `ruby`).                                                                                                                        |
| `--framework`       | `DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK`       |    `rspec` | Test framework (currently supported values: `rspec`, `minitest`).                                                                                                              |
| `--min-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM` | vCPU count | Minimum workers to use for the split.                                                                                                                                          |
| `--max-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM` | vCPU count | Maximum workers to use for the split.                                                                                                                                          |
|                     | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE`         | `-1` (off) | Restrict this run to the slice assigned to node **N** (0‑indexed). Also parallelizes within the node across its CPUs.                                                          |
| `--worker-env`      | `DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV`      |       `""` | Template env vars per local worker (e.g., isolate DBs): `--worker-env "DATABASE_NAME_TEST=app_test{{nodeIndex}}"`.                                                             |
| `--command`         | `DD_TEST_OPTIMIZATION_RUNNER_COMMAND`         |       `""` | Override the default test command used by the framework. When provided, takes precedence over auto-detection (e.g., `--command "bundle exec custom-rspec"`).                   |
| `--tests-location`  | `DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION`  |       `""` | Custom glob pattern to discover test files (e.g., `--tests-location "custom/spec/**/*_spec.rb"`). Defaults to `spec/**/*_spec.rb` for RSpec, `test/**/*_test.rb` for Minitest. |

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

## GitHub Actions example usage

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

## Circle CI example usage

In `.circleci/config.yml`:

```yaml
version: '2.1'
setup: true

orbs:
  node: circleci/node@7
  ruby: circleci/ruby@2
  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@1
  continuation: circleci/continuation@0.2.0

jobs:
  plan:
    docker:
      - image: cimg/ruby:3.4.1-node
    environment:
      RAILS_ENV: test
      DD_ENV: ci
      BUNDLE_PATH: vendor/bundle
      BUNDLE_JOBS: 4
    steps:
      - checkout
      - ruby/install-deps
      - node/install-packages:
          pkg-manager: yarn
      - test-optimization-circleci-orb/autoinstrument:
          languages: ruby
          site: datadoghq.eu
      - run:
          name: Download ddtest latest release
          command: |
            set -euo pipefail
            mkdir -p bin
            curl -fsSL https://github.com/DataDog/ddtest/releases/latest/download/ddtest-linux-amd64 -o bin/ddtest
            chmod +x bin/ddtest
      - run:
          name: Plan tests with ddtest
          command: ./bin/ddtest plan --platform ruby --framework minitest
          environment:
            DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM: 1
            DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM: 4
      - save_cache:
          key: ddtest-plan-{{ .Revision }}
          paths:
            - .testoptimization
            - bin/ddtest
      - run:
          name: Determine parallelism
          command: |
            set -euo pipefail
            cat .testoptimization/parallel-runners.txt
            desired=$(cat .testoptimization/parallel-runners.txt 2>/dev/null || echo 1)
            if ! echo "${desired}" | grep -Eq '^[0-9]+$'; then
              echo "Invalid parallelism value '${desired}', defaulting to 1"
              desired=1
            fi
            if [ "${desired}" -lt 1 ]; then
              echo "Parallelism must be at least 1, defaulting to 1"
              desired=1
            fi
            printf '{"parallelism": %s}\n' "${desired}" > pipeline-parameters.json
            cat pipeline-parameters.json
      - continuation/continue:
          configuration_path: .circleci/test.yml
          parameters: pipeline-parameters.json

workflows:
  plan:
    jobs:
      - plan
```

In `.circleci/test.yml`:

```yaml
version: '2.1'

parameters:
  parallelism:
    type: integer
    default: 1

orbs:
  node: circleci/node@7
  ruby: circleci/ruby@2
  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@1

jobs:
  test:
    parallelism: << pipeline.parameters.parallelism >>
    docker:
      - image: cimg/ruby:3.4.1-browsers
    environment:
      RAILS_ENV: test
      DD_ENV: ci
      BUNDLE_PATH: vendor/bundle
      BUNDLE_JOBS: 4
    steps:
      - checkout
      - restore_cache:
          keys:
            - ddtest-plan-{{ .Revision }}
      - ruby/install-deps
      - node/install-packages:
          pkg-manager: yarn
      - test-optimization-circleci-orb/autoinstrument:
          languages: ruby
          site: datadoghq.eu
      - run:
          name: Precompile assets
          command: |
            bundle exec rails assets:precompile
      - run:
          name: Run tests with ddtest
          command: |
            NODE_INDEX=${CIRCLE_NODE_INDEX:-0}
            export DD_TEST_SESSION_NAME="quotes-rails-ci-${NODE_INDEX}"
            export DD_TEST_OPTIMIZATION_RUNNER_CI_NODE="${NODE_INDEX}"
            ./bin/ddtest run --platform ruby --framework minitest

workflows:
  test:
    jobs:
      - test
```

## Best practices

### Optimize planning step

When using ddtest, you need to add a planning step that performs test discovery (e.g., RSpec dry‑run) before execution. This stage adds overhead: you can optimize it with the practices below.

#### Preinstall system dependencies via Docker

Bake OS packages (e.g., ImageMagick) into a base image so they’re cached in layers and not installed on every run.

```dockerfile
# ci/Dockerfile.test
FROM ruby:3.3
RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get install -y --no-install-recommends imagemagick libpq-dev \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
```

#### Cache project dependencies

Use your CI’s dependency cache. For GitHub Actions + Bundler:

```yaml
- uses: ruby/setup-ruby@v1
  with:
    ruby-version: 3.3
    bundler-cache: true
```

#### Disable seeds/fixtures during discovery

Discovery (planning) does not execute tests; you don't have to setup DB, migrations, or seeds. Set a flag only for the planning job (determined by `DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1`) and guard DB‑related code.

RSpec / Rails example:

```ruby

# in seeds.rb
return if ENV["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"].present?
# your seeds here

# in rails_helper.rb
ActiveRecord::Migration.maintain_test_schema! unless ENV["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"].present?

RSpec.configure do |config|
  unless ENV["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"]
    config.use_transactional_fixtures = true
  else
    config.use_transactional_fixtures = false
    config.use_active_record = false
  end
end
```

After these changes the tests discovery will be faster and will not fail when database is not present.
You can skip database setup for planning step completely and save a lot of time.l

### Minitest support in non-rails projects

We use `bundle exec rake test` command when we don't detect `rails` command to run tests.
This command doesn't have a built in way to pass the list of files to execute, so we pass
them as a space-separated list of files in `TEST_FILES` environment variable.

You need to use this environment variable in your project to integrate your tests with
`ddtest run`. Example when using `Rake::TestTask`:

```ruby
Rake::TestTask.new(:test) do |test|
  test.test_files = ENV["TEST_FILES"] ? ENV["TEST_FILES"].split : ["test/**/*.rb"]
end
```

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
