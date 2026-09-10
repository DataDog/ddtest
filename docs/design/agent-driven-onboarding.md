# Agent-driven onboarding for DDTest

Status: proposed

Working name for the experience: **Testdog**

Last updated: 2026-09-10

## The idea

DDTest should make Datadog Test Optimization fun to try.

A developer should be able to say:

> Make my tests go brr with the Datadog thing.

Then their coding agent can do this:

```text
brew install ddtest
ddtest onboard
# make the small suggested CI edit
ddtest testdrive
```

Within a few minutes the developer sees their own tests in a useful local report. They do not need a Datadog account, API key, or local Datadog Agent.

This is the Lapdog idea applied to Test Optimization: install something small, point it at a real workload, and see personal value immediately.

## What makes it delightful

- It uses the customer's real test suite, not a demo project.
- The first useful command needs no flags.
- It explains what it found in normal language.
- It fixes the awkward case where the Datadog tracer exists only in CI.
- It shows a clean visual result, not protocol logs.
- A human can use it directly and an agent can follow the same commands.
- Problems are described with one useful next step.

The product should feel like a test tool with a little personality, not a setup questionnaire.

## The first experience

### `ddtest onboard`

`onboard` looks at the repository and CI configuration and answers:

- What test framework is this?
- Where does CI run the tests?
- Is Test Optimization already configured?
- What small edit should be made next?

For the first release, it only needs to understand a conventional JavaScript/Jest repository using GitHub Actions.

Example:

```text
$ ddtest onboard

🐕 Found Jest
   Test command: node_modules/.bin/jest
   CI job: .github/workflows/test.yml → test

Test Optimization is not configured yet.

Add this small setup before the test step:
  ...concrete GitHub Actions snippet...

I will use dd-trace <pinned-version> for both CI and the local testdrive.

Next:
  ddtest testdrive
```

The command does not need to understand every possible CI expression. If it cannot recognize the repository, it should say what narrow shape is supported today.

The coding agent makes the edit. We do not need a generic YAML editing engine in DDTest.

### `ddtest testdrive`

`testdrive` proves the setup using the customer's tests.

It:

1. creates a unique session directory;
2. makes the selected Datadog tracer available locally;
3. starts a small Datadog-shaped intake on loopback;
4. points the tracer to it with a dummy API key;
5. runs the Jest suite once;
6. turns the received test events and coverage into a terminal summary and local HTML report.

Example:

```text
$ ddtest testdrive

🐕 Preparing dd-trace for this testdrive...
🐕 Running your Jest suite with Test Optimization...

Setup works.
  428 tests reported
  421 passed, 2 failed, 5 skipped
  426 tests produced coverage
  18.4s total test time

Slowest suites
  checkout.test.js       4.2s
  recommendations.test.js  2.8s
  search.test.js         2.1s

Open the report:
  .testoptimization/testdrive/2026-09-10-abc123/report.html
```

If the tests fail but events arrive, DDTest should still say that instrumentation works and report the test failures separately. Setup validation does not require a green suite.

Running `ddtest testdrive` is enough. There is no generated execution plan, approval file, checksum, or follow-up command.

## Solving CI-only tracer installation

Many auto-instrumented users do not have `dd-trace` in `package.json`. The tracer is installed only inside CI. The `dd-trace-js` runbook cannot bootstrap that repository locally because the runbook itself expects the package to be present.

DDTest closes that gap:

- The first release uses one known-good, pinned `dd-trace` version.
- `onboard` puts that version into the suggested CI setup.
- `testdrive` installs the same version into its own session directory when the project does not already provide it.
- The test process preloads the absolute `dd-trace/ci/init` path from that directory.
- The project's `package.json` and lockfile are not changed.

This is the same basic shape used by the Test Optimization installation script: install the JavaScript tracer into a separate prefix and preload it by absolute path.

We do not need a generalized compatibility database yet. When we add a second runtime or need to support multiple tracer lines, we can introduce the smallest mechanism that the real cases require.

## Local Test Optimization intake

Shepherd already has a useful local implementation in `tools/mockdog`. DDTest should bring over the smallest subset needed for the first JavaScript tracer:

- settings response;
- test events;
- per-test coverage;
- any Git or telemetry endpoint the real tracer actually calls;
- a short summary of what arrived.

The server listens on a kernel-assigned loopback port. The local tracer uses agentless mode pointed at that address with a dummy API key. Nothing needs to emulate a full Datadog Agent unless a real tracer limitation forces us there.

We should learn the required protocol by running the real pinned tracer, not by implementing every endpoint in advance.

## Concurrent sessions

Every testdrive gets its own directory and listener:

```text
.testoptimization/testdrive/<session-id>/
  report.html
  result.json
  test-output.log
```

The session ID is only for uniqueness. Two humans, agents, or terminals can run testdrive at the same time without sharing ports, events, or cleanup.

This is a small foundation worth keeping from the start.

## What we show in the first report

The first report answers setup questions:

- Did the tracer load?
- Did Test Optimization test events arrive?
- How many tests, suites, passes, failures, and skips were reported?
- Did per-test coverage arrive?
- Which tests or suites were slow?
- Were there obvious missing events or coverage?
- What should the developer do next?

It does not claim what the real Datadog TIA backend would skip. It does not estimate savings or recommend parallelization.

The first terminal and HTML formats can evolve freely while we dogfood them. We should add a stable agent JSON contract only after using the feature with real coding agents and learning which fields matter.

## Human and agent use the same product

We do not need MCP to make this agent-friendly. The CLI owns detection and the local run; the agent reads the output, edits the CI file, and explains the result.

The DDTest binary can include a short runbook so an agent starting from `ddtest help` knows the intended sequence. It should be a page of practical instructions, not a second specification.

## Milestones

### Milestone 0: prove it works

- Bring the minimum Shepherd intake into DDTest.
- Run one real pinned `dd-trace` against a tiny Jest fixture.
- Receive test events and coverage with no Datadog credentials.
- Give every run a unique directory and listener.
- Prove two sessions can run concurrently.

The output can be ugly and the code can be specific. The purpose is to learn.

### Milestone 1: let people play with it

- Ship `ddtest onboard` for conventional JavaScript/Jest/GitHub Actions.
- Ship `ddtest testdrive` with isolated tracer installation.
- Run the real suite once and produce a useful terminal summary.
- Add a pleasant local HTML report.
- Dogfood it on real repositories and fix the repeated rough edges.
- Publish a Homebrew preview.

The detailed implementation sequence lives in [the milestone development plan](agent-driven-onboarding-development-plan.md).

## Later, if users pull us there

- `ddtest doctor` as a separate reusable diagnostic command;
- Python, Ruby, more JavaScript frameworks, and more CI providers;
- a stable JSON contract for agents and integrations;
- real Datadog forwarding when `DD_API_KEY` is present;
- real TIA settings and skippables;
- local savings estimates and historical analysis;
- test splitting and parallelization;
- a Testdog share link.

Those are directions, not requirements for the first useful release.

## How we know milestone 1 worked

A new user can install DDTest, run two memorable commands, and see their own test suite represented locally without creating a Datadog account. If their tracer only exists in CI, the local experience still works without changing the project dependencies.

The useful question after the first dogfood sessions is not “did we finish the architecture?” It is:

> Did someone show the report to a teammate because it was cool?

## Source notes

- The `dd-trace-js` repository ships its Test Optimization validation runbook with the tracer, which is a useful precedent for agent instructions but assumes the tracer is already installed.
- `~/p/test-visibility-install-script` demonstrates installing `dd-trace` under a separate npm prefix and preloading its absolute `ci/init` path.
- `~/p/shepherd/tools/mockdog` demonstrates receiving and interpreting Test Optimization traffic locally.
- Lapdog demonstrates the broader product loop: show a developer their own data before asking for a Datadog account.
