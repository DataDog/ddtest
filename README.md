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

For Go, `ddtest instrument` reports a project's tests to Test Optimization
directly; see [Go tests with ddtest instrument](#go-tests-with-ddtest-instrument).

## Try Test Optimization locally

Install ddtest, which the install script checks against the release's
`SHA256SUMS` and puts in `~/.local/bin` (in PowerShell, use
`irm https://github.com/DataDog/ddtest/releases/latest/download/install.ps1 | iex`;
see [Installation](#installation) for versions and other directories):

```sh
curl -fsSL https://github.com/DataDog/ddtest/releases/latest/download/install.sh | bash
```

Then, with your project's test dependencies and any required browsers/services
ready:

```sh
ddtest onboard    # CI setup and local validation
ddtest onboard --framework vitest --apply       # Preview exact setup edits
ddtest onboard --framework vitest --apply --yes # Apply reviewed setup and validate
ddtest testdrive  # Preview, confirm, and run tests locally
```

For agent-driven execution, use `ddtest onboard --framework vitest --apply --yes`
or `ddtest testdrive --framework vitest --all --agent --yes`. The shared coordinator
returns a resolved plan, precise blockers and bounded resume commands; it checks
source preservation, report exclusions and cleanup. Finish with
`ddtest testdrive --report` to verify freshness and render the factual verdict.
`--format json` is available to hosts. Plain testdrive retains its existing
execution behavior; `onboard --instructions` prints manual guidance. See
[agent onboarding](docs/onboarding.md#agent-coordination-and-fresh-results).

For Jest, Vitest, Mocha, Playwright and Cypress, testdrive compares the original test suite without and with instrumentation, then validates supported advanced features using temporary probes. Playwright reports TIA skipping and per-test coverage as unsupported. Cypress TIA requires application coverage from the tested page (Istanbul `window.__coverage__`) and a test-level skip of the probe. Matching existing failures are acceptable. Other frameworks keep their existing reporting checks.

No Datadog account or Agent is needed. Testdrive links to an HTML report under
`.testoptimization/report.html` with a compact `.testoptimization/testdrive.json`. Use `--yes` after reviewing the preview in
non-interactive sessions, `--framework` to select a runner, and `--command` for
package scripts or custom setup.

For Jest, start with [testdrive configuration checks](docs/running.md#testdrive-configuration-checks).

For repositories with several Jest configurations, use `ddtest testdrive --framework jest --all --yes` to validate discovered CI commands and their prerequisites. Unknown setup stays explicitly unvalidated; `--command` selects a focused command. See [configuration validation](docs/running.md#validate-all-discovered-jest-configurations).

Testdrive reuses a detected project tracer; otherwise it installs the release
every instrumented CI job pins, or the latest release (`--tracer-version`
selects a fallback release or `git:<ref>`).
JavaScript/Python installs are isolated. Ruby runs `bundle add datadog-ci`,
updating the project's Gemfile and lockfile.

## Go tests with ddtest instrument

`ddtest instrument` reports a Go project's tests to Datadog Test Optimization
with nothing added to the project: no SDK, no Orchestrion and no change to its
code or `go.mod`. Install ddtest as in [Installation](#installation), then put it in front of the
command that runs the tests:

```sh
ddtest instrument go test ./...                  # as go test, and the tests report
ddtest instrument go tool gotestsum -- ./...     # gotestsum's go test reports too
ddtest instrument --go-shim make test            # the go test that a command runs
ddtest instrument go build ./...                 # any other command runs unchanged
```

The command keeps go test's output, environment and exit status, and Go's test
cache: a package whose result comes from the cache reports nothing, as with
plain `go test`. It needs Go 1.25 or newer, while the project keeps its own
language version, and runs on Linux, macOS and Windows. Results go through a
Datadog Agent (`localhost:8126`, or `DD_TRACE_AGENT_URL`), or without one with
`DD_CIVISIBILITY_AGENTLESS_ENABLED=true`, `DD_API_KEY` and `DD_SITE`:

```sh
DD_SERVICE=my-service DD_ENV=ci ddtest instrument go test ./...
```

It supports automatic retries, early flake detection, intelligent test
skipping, impacted tests and test management, according to the backend's
settings, plus per-test coverage, subtests, fuzz seeds, examples and Testify
suites. The release executable carries the runtime that it adds to the test
binaries, so a CI job downloads and builds nothing else. See the
[Go instrumentation documentation](ddto/README.md) for its settings and
limits, and [ddto/AGENTS.md](ddto/AGENTS.md) for coding agents that set it up.

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
  Vitest 1.6 or higher and `dd-trace` **5.125.0** or higher. Vitest uses a direct
  Node API integration; see [configuration and migration instructions](docs/running.md#vitest-integration).

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
| `--framework` | Test framework. Currently supported: `rspec`, `minitest`, `pytest`, `cucumber`, `cypress`, `jest`, `mocha`, `playwright`, `vitest`, `webdriverio` (`wdio`). |
| `--command` | Override the default base command for supported framework modes. Used by RSpec and Minitest run/discovery, Cucumber, Cypress, Jest, Mocha, and Playwright run/discovery. Vitest `plan`/`run` reject this option; testdrive accepts an explicit Vitest CLI or supported `ddtest run` command. |
| `--vitest-config` | Vitest config file used by both planning and execution; defaults to Vitest config discovery. See [migration instructions](docs/running.md#vitest-integration). |
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

Install the release executable for your platform, checked against the
release's `SHA256SUMS`, into `~/.local/bin`:

```bash
curl -fsSL https://github.com/DataDog/ddtest/releases/latest/download/install.sh | bash
```

In PowerShell, on Windows or elsewhere:

```powershell
irm https://github.com/DataDog/ddtest/releases/latest/download/install.ps1 | iex
```

`--version v1.2.3` picks a release and `--dir` another directory (`-Version`
and `-Dir` in PowerShell, or `DDTEST_INSTALL_VERSION` and `DDTEST_INSTALL_DIR`
for both). An installed executable of the same version stays. Outside GitHub
Actions, the scripts say when another `ddtest`, such as an older one from
`go install`, comes first on `PATH`; in GitHub Actions they add the directory
to `PATH` for the next steps:

```yaml
- name: Install ddtest
  run: curl -fsSL https://github.com/DataDog/ddtest/releases/latest/download/install.sh | bash
```

To install from a checkout:

```sh
make install                          # GOBIN, or the first GOPATH entry's bin
make install GOBIN="$HOME/.local/bin"  # Custom destination; add it to PATH
```

Each release lists its executables, `ddtest-<os>-<arch>` for Linux, macOS and
Windows on `amd64` and `arm64`, with the install scripts and `SHA256SUMS`, on
the [release page](https://github.com/DataDog/ddtest/releases/latest).

## CI configuration examples

- [GitHub Actions](docs/examples/github-actions.md)
- [CircleCI](docs/examples/circleci.md)

## More documentation

- [Running DDTest](docs/running.md)
- [Go tests with ddtest instrument](ddto/README.md)
- [Settings](docs/settings.md)
- [Plan file layout](docs/layout.md)
- [Third party test runners](docs/third-party-runners.md)
- [GitHub Actions example](docs/examples/github-actions.md)
- [CircleCI example](docs/examples/circleci.md)
- [Best practices](docs/best_practices.md)
- [Running locally with CI skippable tests and runtime tags](docs/local-ci-skippable-tests.md)
- [DDTest 1.0 upgrade guide](docs/upgrade-1.0.md)

Vitest testdrive supports baseline/reporting comparison and controlled retries,
early flake detection, skipping, quarantine, disabled tests and attempt-to-fix.
Use `ddtest testdrive --framework vitest --all --check-only --yes` before
`ddtest testdrive --framework vitest --all --yes`. For focused diagnostics,
replace `--all` with `--command "npx vitest run --config ci.ts"` or a supported
`ddtest run` invocation. `plan`/`run` retain their Node API behavior and reject
their own `--command`. See [Vitest validation](docs/running.md#vitest-testdrive-validation).

### Mocha testdrive validation

See [Mocha validation](docs/running.md#mocha-testdrive-validation) for commands, coverage requirements and limitations.

Cucumber testdrive also compares native scenario outcomes and exercises all six
controls, requiring real application source coverage through the project's NYC
setup. See [Cucumber validation](docs/running.md#cucumber-validation).

### WebdriverIO validation

Use `ddtest onboard --framework webdriverio` for setup instructions, then
`ddtest testdrive --framework webdriverio --all --check-only --yes` for the
configuration inventory. Execute a focused command with `--command 'npm test'`
without `--check-only`. The local runner with Mocha or Jasmine is supported;
standalone automation, custom adapters and browser runners remain unvalidated.
Five controlled features are required; TIA skipping and coverage are unsupported.
See [WebdriverIO validation](docs/running.md#webdriverio-validation).

### Developer onboarding experiments

The [ddtest onboarding evaluation skill](skills/ddtest-onboarding-eval/SKILL.md)
compares uncoached Codex/Claude onboarding in fresh repositories. It is maintainer
tooling, separate from customer onboarding and the ddtest binary.

**Use the container runner with a clean trusted image, no cloud/GitHub/npm
credentials or MCP servers, and an explicitly selected throwaway agent login.**
Repository install scripts and auto-approved agents must not run on the host with
personal credentials. CLI permission modes alone do not isolate a trial.

The evaluator reads the skill; the tested agent receives only a simple prompt
pointing to the binary. Evidence includes redacted public tool activity, current
product report verdicts and owned cleanup. The runner pins/checks CLI versions,
isolates configuration and removes the container on interruption or timeout.
See the skill for setup, independent grading and marker-verified cleanup. CI uses
fake CLIs and offline fixtures, never real model sessions.
