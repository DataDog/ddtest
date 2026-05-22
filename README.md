# DDTest

DDTest is a CLI tool that plans and runs your tests in parallel alongside your existing test commands, or as a drop-in runner. It discovers tests in your repo, fetches Datadog Test Optimization data, and chooses an efficient worker split.

You need it when Test Impact Analysis shrinks the test workload but CI still launches too many nodes, and skipped tests leave splits wildly uneven. Start by generating a plan (`ddtest plan`) you can feed to any runner, or let DDTest execute the tests for you with `ddtest run` in CI.

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

DDTest ships as a CLI tool `ddtest` with two primary sub‑commands: `plan` and `run`.

Use `plan` to discover tests, fetch Datadog Test Optimization data once, and compute a parallelization plan you can reuse on any CI node; use `run` to execute that plan locally or in CI. If a plan is missing, `run` will generate it on the fly.

DDTest is meant to run in CI. Local runs are possible when you want to reuse
CI's skippable tests on your machine; see
[Running locally with CI skippable tests](docs/local-ci-skippable-tests.md).

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
Copy `.testoptimization/` to any CI job that runs `ddtest run` or reads DDTest's
runner file lists. For the full file layout and formats, see
[Plan file layout](docs/layout.md).

#### ddtest run

Runs tests using the framework you specify. If .testoptimization/ exists, DDTest will use its precomputed split; otherwise it first runs plan and then executes.

**Single machine, multiple CPUs example:**

```bash
ddtest run --platform ruby --framework rspec
```

The default --min-parallelism and --max-parallelism equal the machine's available physical CPU core count, so you get CPU utilization without defaulting to one worker per hyperthread.

**Multiple CI nodes (static split per node)**

First run `ddtest plan` once, share `.testoptimization/` folder to all nodes (put it into the folder where you run your tests from), then on each node run only its slice:

```bash
ddtest run --platform ruby --framework rspec --ci-node <CI_NODE_INDEX>
```

In CI-node mode, DDTest uses one local worker by default so database and other per-worker resources stay easy to isolate. To fan out within each CI node, set `--ci-node-workers` to a positive integer, or use `--ci-node-workers ncpu` to use the node's available physical CPU cores.

`--worker-env` supports `{{nodeIndex}}` and `{{workerIndex}}` placeholders. `{{nodeIndex}}` is the machine number: in CI-node mode, it is the exact CI node index from `--ci-node` or `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE`; in single-machine runs, it is `0`. `{{workerIndex}}` is the process number within the current machine, starting at `0`. If a CI node is split across multiple local workers, each worker receives the same `{{nodeIndex}}` value and a different `{{workerIndex}}` value.

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
| `--min-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM` | physical CPU count | Minimum worker count DDTest considers when choosing the split.                                                                                                                                                       |
| `--max-parallelism` | `DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM` | physical CPU count | Maximum worker count DDTest considers when choosing the split.                                                                                                                                                       |
| `--ci-job-overhead` | `DD_TEST_OPTIMIZATION_RUNNER_CI_JOB_OVERHEAD` | `25s` | Modeled overhead for adding one more CI job / parallel runner. Accepts durations such as `25s`, `1m`, `1500ms`, or `0s` to disable this bias. Increase it to use fewer CI jobs; decrease it to prefer faster wall time. |
| `--ci-node`         | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE`         | `-1` (off) | Restrict this run to the slice assigned to node **N** (0-indexed).                                                                                                                                                   |
| `--ci-node-workers` | `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS` |        `1` | Number of parallel workers per CI node. Use a positive integer, or `ncpu` to use the node's available physical CPU cores. Tests assigned to a CI node are further split among this many local workers.                |
| `--worker-env`      | `DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV`      |       `""` | Template env vars per worker: `--worker-env "DATABASE_NAME_TEST=app_test{{nodeIndex}}_{{workerIndex}}"`. `{{nodeIndex}}` is the machine number (`0` for single-machine runs); `{{workerIndex}}` is the process number within that machine. |
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

Discovery (planning) does not execute tests; you don't have to setup DB, migrations, or seeds.
You could guard DB‑related code when running in discovery mode determined by `DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1`.

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
You can skip database setup for planning step completely and save a lot of time.

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

## Parallelism selection

DDTest chooses parallelism by estimating the runnable duration of each test file,
then trying worker counts between `--min-parallelism` and `--max-parallelism`.
Duration estimates come from Datadog test suite p50 timings when available and
fall back to local discovery weights otherwise.

Each candidate split is scored as expected slowest-worker time plus the runner
count multiplied by `--ci-job-overhead`. Increase `--ci-job-overhead` to use
fewer CI jobs, or decrease it to prefer faster wall time. Use duration values
such as `25s`, `1m`, or `1500ms`; set `0s` to disable this runner-overhead bias.
When scores tie, DDTest prefers fewer runners, then lower wall time, then lower
imbalance between workers.
