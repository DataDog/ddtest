# DDTest

DDTest helps you scale CI workloads down when Datadog Test Optimization skips
tests: it plans how to run the remaining test files across CI nodes or local workers in the most efficient way.

Use `ddtest plan` once to create a reusable `.testoptimization/` plan, then use
`ddtest run` in each CI job to run only the files assigned to that job.

DDTest can also write file lists for another runner, such as Knapsack Pro or
`parallel_tests`.

Currently supported:

- Ruby with RSpec or Minitest.
- Python with pytest.
- JavaScript with Cucumber, Cypress, Jest, Mocha, Playwright, or Vitest.

## Prerequisites

Before using DDTest, you must have **Datadog Test Optimization** already set up and enabled with a Datadog Test Optimization library for your language and framework. DDTest relies on this integration to discover your tests and plan test execution accordingly.

Minimum supported library and runtime requirements:

- Building DDTest from source requires Go **1.26.5**.
- Ruby requires the `datadog-ci` gem **1.31.0** or higher.
- Python requires the `ddtrace` package **4.11.0** or higher and `pytest`.
- JavaScript requires the `dd-trace` package **5.111.0** or higher and Node.js.
  Cucumber support is tested with `@cucumber/cucumber` 7 through 13; Cypress
  support requires Cypress 12 or higher; Mocha support requires Mocha 8 or higher;
  Playwright support requires Playwright 1.18 or higher; Vitest support requires
  Vitest 1.6 or higher.

For instructions on setting up Test Optimization, see the [Datadog Test Optimization documentation](https://docs.datadoghq.com/tests/setup/).

## Usage

DDTest ships as a CLI tool `ddtest` with two primary sub-commands: `plan` and `run`.

Use `plan` to create a reusable `.testoptimization/` plan without running tests.
Use `run` to execute that plan locally or in CI. If a plan is missing, `run` will generate it on the fly.

DDTest is meant to run in CI. Local runs are possible when you want to reuse
CI's skippable tests on your machine; see
[Running locally with CI skippable tests](docs/local-ci-skippable-tests.md).
For planning-step performance tips and framework-specific setup notes, see
[Best practices](docs/best_practices.md).

### Available commands

#### ddtest plan

Creates a reusable execution plan under `.testoptimization/` without running
tests. The plan contains the runnable test files, the selected CI node or worker
count, and any per-node file lists needed by `ddtest run` or another runner.

**Example:**

```bash
ddtest plan \
  --platform ruby \
  --framework rspec \
  --min-parallelism 8 \
  --max-parallelism 32
```

For Python/pytest:

```bash
ddtest plan \
  --platform python \
  --framework pytest \
  --min-parallelism 8 \
  --max-parallelism 32
```

For JavaScript/Jest:

```bash
ddtest plan \
  --platform javascript \
  --framework jest \
  --min-parallelism 8 \
  --max-parallelism 32
```

For JavaScript/Vitest:

```bash
ddtest plan \
  --platform javascript \
  --framework vitest \
  --min-parallelism 8 \
  --max-parallelism 32
```

For JavaScript/Mocha:

```bash
ddtest plan \
  --platform javascript \
  --framework mocha \
  --min-parallelism 8 \
  --max-parallelism 32
```

For JavaScript/Cypress:

```bash
ddtest plan \
  --platform javascript \
  --framework cypress \
  --min-parallelism 8 \
  --max-parallelism 32
```

For JavaScript/Playwright:

```bash
ddtest plan \
  --platform javascript \
  --framework playwright \
  --min-parallelism 8 \
  --max-parallelism 32
```

For JavaScript/Cucumber:

```bash
ddtest plan \
  --platform javascript \
  --framework cucumber \
  --min-parallelism 8 \
  --max-parallelism 32
```

This prepares the plan and writes it to `.testoptimization/` folder for later reuse.
Copy `.testoptimization/` to any CI job that runs `ddtest run` or reads DDTest's
plan file lists. For the full file layout and formats, see
[Plan file layout](docs/layout.md).

#### ddtest run

Runs tests using the framework you specify. If `.testoptimization/` exists,
DDTest uses its precomputed plan; otherwise it first runs `plan` and then
executes.

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

For CI-node mode, worker environment variables, custom commands, and
parallelism details, see [Running DDTest](docs/running.md).

### Common settings

| CLI flag | What it does |
| --- | --- |
| `--platform` | Language/platform. Currently supported: `ruby`, `python`, `javascript`. |
| `--framework` | Test framework. Currently supported: `rspec`, `minitest`, `pytest`, `cucumber`, `cypress`, `jest`, `mocha`, `playwright`, `vitest`. |
| `--command` | Override the default base command for supported framework modes. Used by RSpec and Minitest run/discovery, Cucumber, Cypress, Jest, Mocha, Playwright, and Vitest run/discovery, and pytest run/discovery (since 1.7.0). For ddtest versions prior to 1.7.0 with pytest, the command cannot be changed. Pass extra flags with `PYTEST_ADDOPTS`. |
| `--min-parallelism` | Minimum CI node or worker count DDTest considers when planning. |
| `--max-parallelism` | Maximum CI node or worker count DDTest considers when planning. |
| `--target-time` | Target wall time DDTest tries to satisfy when selecting parallelism. |
| `--ci-node` | Run only the files assigned to CI node **N**. |
| `--tests-location` | Override the default test file discovery glob. |
| `--tests-exclude-pattern` | Exclude matching test files from discovery. |
| `--strict-discovery` | Fail planning when full test discovery fails. |

For all flags, environment variables, and defaults, see
[Settings](docs/settings.md).

## Installation

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

## CI configuration examples

- [GitHub Actions](docs/examples/github-actions.md)
- [CircleCI](docs/examples/circleci.md)

## More documentation

- [Running DDTest](docs/running.md)
- [Settings](docs/settings.md)
- [Plan file layout](docs/layout.md)
- [Third party test runners](docs/third-party-runners.md)
- [GitHub Actions example](docs/examples/github-actions.md)
- [CircleCI example](docs/examples/circleci.md)
- [Best practices](docs/best_practices.md)
- [Running locally with CI skippable tests and runtime tags](docs/local-ci-skippable-tests.md)
- [DDTest 1.0 upgrade guide](docs/upgrade-1.0.md)
