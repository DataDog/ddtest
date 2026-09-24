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

## Try Test Optimization locally

From a supported repository with its test dependencies installed:

```sh
ddtest onboard
ddtest testdrive
```

`onboard` prints GitHub Actions setup instructions. `testdrive` previews its
commands, asks for confirmation, and runs your own tests against a local intake.
It needs no Datadog account, API key, or Agent. Non-interactive callers can review
the preview, then use `ddtest testdrive --yes`.

All nine frameworks listed above can collect local telemetry. Compatibility and
feature validation currently support Jest; other frameworks explicitly remain
unvalidated. If a repository contains several
runners, select one with `--framework`. Use `--command` to select a custom entry
point or a small representative part of a large suite:

```sh
ddtest onboard --framework playwright
ddtest testdrive --framework playwright --command 'npm run test:e2e -- --project=chromium' --yes
```

Testdrive prints a terminal summary and retains one self-contained report:
`.testoptimization/testdrive.json`. It includes an explicit `success` flag,
local compatibility, CI runtime and feature verdicts, and the working directory. Each validation run
records its exact shell-quoted test command, instrumentation/probe mode, exit code,
and aggregate test/event counts. Raw command output, individual test results, and
telemetry payloads are discarded after validation. Mismatch examples are limited
to ten, with a total count; diagnostic text is capped at 1,024 characters. Each run
atomically replaces this file; reports do not accumulate. It does not generate HTML.

JavaScript/Python fallback tracers, captured requests, and runner files live in a
private OS temporary directory. Ruby fallback installation uses the project bundle
and retains its dependency changes. Testdrive removes that directory on success, failure, and
handled cancellation. Temporary probe files inside the project are also removed.
Bounded setup errors and summaries of attempted runs are preserved when the report
can be written. Other `.testoptimization` data is preserved.

Jest validation has two stages:

1. Run the same command without instrumentation and with reporting-only
   instrumentation. Compare test identities, outcomes, failure details, and
   process exit codes; ignore timings, result ordering, and stack frames.
   Existing failures are acceptable when both runs match. If results differ,
   repeat the pair: changing results are inconclusive, while a repeatable
   difference is a suspected regression. Matching results require matching
   telemetry before compatibility can be reported. Setup failures before tests
   execute are inconclusive.
2. Place a temporary probe beside an existing test, preserving the project's
   Jest configuration. Establish passing and failing controls, then enable one
   feature per run: auto retries, early flake detection, skipping, quarantine,
   disabled tests, and attempt-to-fix. Check actual execution, exit outcome, and
   feature-specific telemetry. Remove the probe when finished, including on
   errors. If the command/configuration cannot select the probe, its checks are
   inconclusive. These checks validate the probe under the selected configuration;
   they do not establish compatibility for every test environment in a monorepo.

`testdrive.json` separates compatibility (`compatible`, `suspected regression`,
`inconclusive`) from each feature (`passed`, `failed`, `inconclusive`, `unvalidated`).
For Jest repositories with detected GitHub Actions test jobs, `ci_runtime` records
whether each instrumented Node runtime satisfies its workflow-selected tracer's
`engines.node` requirement. The checker reads public GitHub action metadata at the
configured action ref and npm package metadata, honoring `js-tracer-version` when
set. No tracer version or minimum Node version is pinned in the checker. This can
differ from the tracer installed for the local testdrive.

The check supports literal `actions/setup-node` versions, static matrix axes, and
static `include`/`exclude`, and matrix equality/inequality, boolean values, `startsWith`, `!`, `&&`, `||`, and parentheses.
Explicitly excluded entries remain visible as uninstrumented. Dynamic matrices,
version files, LTS aliases, other conditions, mixed-type comparisons, unsupported engine
ranges, and unavailable metadata are inconclusive. Currently engine comparison
supports minimum requirements such as `>=22` or `>=22.2.0`; it does not approximate
other ranges. This checks the declared setup-node runtime, not arbitrary shell
commands that might later change Node. It does not execute CI or prove bootstrap
correctness, remote instrumentation, or Datadog backend connectivity.

The command exits successfully only when local compatibility, all feature checks,
and the applicable CI runtime check succeed. A runtime incompatibility or unknown
configuration makes `success` false even if every local test matches. Repositories
without detected GitHub Actions test jobs can still pass local validation; their
CI runtime result is explicitly `not applicable`. The suite's own failures do not
automatically fail validation.
Other frameworks collect reporting-only telemetry and return an inconclusive
validation result until adapters exist.

The local intake supports agentless traffic; Agent/EVP routing is not supported.
No results are sent to Datadog. Temporary installations and run files are removed;
only the compact JSON report remains as a validation artifact. Keep it out of source control.

Testdrive reuses the project's tracer when the platform's tracer check succeeds.
If the check fails, it attempts a fallback installation (latest by default).
`--tracer-version` selects a release or Git revision for that fallback installation;
JavaScript and Python installations leave project dependency files unchanged;
Ruby uses `bundle add datadog-ci`, which updates the project Gemfile and lockfile:

```sh
ddtest testdrive --tracer-version <selected-release-or-tag> --yes
ddtest testdrive --tracer-version 'git:<commit-sha>' --yes
```

The same option works for JavaScript, Python, and Ruby. Git selections also accept
branches and tags; they require Git and the tracer's source-build prerequisites.

Local testdrive prerequisites:

- JavaScript: Node.js 22+, npm if `dd-trace` needs to be installed,
  and the project's package manager and dependencies. Vitest/ESM loading and
  Cypress config/support wrappers are supplied automatically.
- Python: an activated project environment with pytest, and pip if `ddtrace` is
  absent. Fallback installation uses a session-owned directory; the active
  environment is unchanged.
- Ruby: Ruby/Bundler. If `datadog-ci` is unavailable, testdrive runs
  `bundle add datadog-ci` in the project, honoring `BUNDLE_GEMFILE` and the
  existing bundle settings. Bundler updates the project Gemfile and lockfile;
  tests use that same bundle. Native gem builds need build tools.
- Browser suites: install the project's browsers and start any required services
  first, or use its existing test command that manages them. Testdrive does not
  install browsers or start applications on its own.

Jest preflight resolves the fallback selection once before installation and inspects
`--showConfig` for the effective Jest version, runner, and configuration. It checks
known dd-trace 5/6 Jest requirements and the tracer's Node engine minimum before
running the suite. Unknown combinations remain unverified. Use `--command` with
the project's actual Jest arguments when it uses a custom config. Preflight loads
project JavaScript; detection and preview do not execute it.

`ddtest testdrive --check-only --yes` performs those configuration checks and static
CI checks without installing a tracer or running tests. It replaces the same JSON
report, with `check_only: true`, `checks_passed`, and test execution marked not
exercised. A successful configuration check does not set validation `success`.

Full validation records `local_success` separately from the combined `success`.
CI Node compatibility and exact local/CI tracer agreement are separate findings;
actual CI execution is always `not exercised`. Moving CI selectors are valid only
for the version resolved during this check. Pin the selected release in the
workflow to retain that agreement. Unsupported checker syntax requires review,
not a workflow rewrite. Manual review does not override a programmatic verdict. Onboarding uses
`datadog/test-visibility-github-action@v3` and leaves tracer-version inputs unset.

Project dependency manifests and lockfiles are not edited by testdrive. Testdrive
uses the framework’s normal command; pass `--command` to run a package script and
its lifecycle hooks or other custom setup. Tracer downloads require network access. This release covers root projects and GitHub Actions onboarding;
monorepo orchestration and other CI providers are outside this scope.

See the [Milestone 2 validation record](docs/testing/onboarding-milestone-2.md)
for tested repository commits, commands, and limitations.

## Prerequisites

Before using `ddtest plan` or `ddtest run`, you must have **Datadog Test Optimization** already set up and enabled with a Datadog Test Optimization library for your language and framework. DDTest relies on this integration to discover your tests and plan test execution accordingly.

Minimum supported library and runtime requirements:

- Building DDTest from source requires the Go version specified in [go.mod](go.mod) or later.
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

Both commands accept files, directories, or quoted globs to narrow the configured
test discovery. When a saved plan exists, use `plan` to change the selection;
`run` rejects positional arguments.

```bash
ddtest plan spec/models spec/requests
ddtest run
```

DDTest is meant to run in CI. Local runs are possible when you want to reuse
CI's skippable tests on your machine; see
[Running locally with CI skippable tests](docs/local-ci-skippable-tests.md).
For planning-step performance tips and framework-specific setup notes, see
[Best practices](docs/best_practices.md).

Platform and framework are detected from the current directory when `--platform`
and `--framework` are omitted, including for `plan` and `run`. Projects with
multiple platforms or test frameworks must select one explicitly. Flags and
`DD_TEST_OPTIMIZATION_RUNNER_PLATFORM` / `DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK`
override detection; an explicit framework also identifies its platform. Detection
does not execute project scripts or change the test command. Use `--command`
when your project requires a custom invocation.

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
