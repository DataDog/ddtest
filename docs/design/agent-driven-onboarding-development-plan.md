# Agent-driven onboarding: development plan and handover

Status: Milestone 0 complete; Milestone 1 preview implemented; Milestone 2 proposed

Last updated: 2026-09-17

Related design: [Agent-driven onboarding for DDTest](agent-driven-onboarding.md)

## How we will build this

We will build one thin path all the way through, try it on real repositories, and improve it from what we learn.

We will not design a general onboarding platform first. Output structures, error categories, configuration files, and extension points can emerge after the end-to-end flow works.

Milestones 0 and 1 deliberately used one narrow path:

- JavaScript
- Jest
- GitHub Actions
- repository dependencies already installed
- tracer configured through the Test Optimization auto-installation flow
- credential-free local testdrive

Anything else can initially return a clear “not supported yet” message.

## Handover: what exists today

The working preview is on `anmarchenko/agentic-onboarding-runbook` in draft PR #128. The implementation is intentionally direct and specific to the first repository shape.

### User flow

- `ddtest help` says to start with `ddtest onboard`.
- `ddtest onboard` detects a root JavaScript/Jest repository and the GitHub Actions workflows that run its tests.
- The onboarding output contains the concrete GitHub Actions edit, asks the agent to run `ddtest testdrive`, tells it to post every clickable report link to the user, and leaves API-key creation and GitHub secret setup to a human at the end.
- `ddtest testdrive` previews every command and filesystem change before it runs. Interactive terminals get one confirmation; non-interactive agents are told to review the preview and rerun with `--yes`.
- `make install` installs the current build to the current user's Go bin directory on macOS and Linux.

### Local testdrive

- Every run gets a unique directory and a kernel-assigned loopback port, so concurrent sessions remain independent.
- The pinned `dd-trace@6.15.0` package is installed inside the session and preloaded by absolute path. The customer's `package.json` and lockfile are untouched.
- The local intake answers the tracer settings and feature requests with Test Optimization, coverage, ITR, Early Flake Detection, flaky retries, impacted tests, failed-test replay, and test management enabled.
- Test events and test- or suite-level coverage are decoded. Stored intake traffic is JSON only; the complete test output is stored separately.
- The terminal and self-contained HTML report agree on failed tests, flaky tests, duration outliers, and unusually broad coverage. Flaky tests use `test.final_status` and are not also reported as failed.
- Problem cards appear only when there is a finding. They show all runs, timings, errors, retry information, source excerpts, and coverage at the granularity used by the tracer.
- Covered files are displayed one per line with 50 files per page. Suites and tests have separate paginated tabs.
- The report links to the saved JSON traffic and test output, and `testdrive` prints the report as a clickable absolute `file://` URL.

### Code map

- `internal/onboard/`: repository detection and embedded Markdown instructions.
- `internal/testdrive/`: session lifecycle, preview, runner, terminal result, and HTML report.
- `internal/testdrive/tracer/`: isolated tracer installation behind the small `Tracer` interface.
- `internal/testdrive/intake/`: local HTTP intake, JSON capture, event and coverage decoding, and findings.
- `internal/platform/` and `internal/framework/`: the existing platform/framework implementations that Milestone 2 should reuse instead of building a second test runner.

### Verification already in place

- A real pinned tracer runs against the tiny Jest fixture and sends test events and coverage.
- Two real fixture runs execute concurrently and prove port, traffic, and file isolation.
- Unit coverage exercises event decoding, final status, flaky tests, test- and suite-level coverage, medians, source excerpts, HTML rendering, and confirmation behavior.
- The flow was dogfooded on a large React Native Paper Jest suite: 1,363 events and coverage for all 680 logical tests were recognized, including Early Flake Detection retries.
- `make test` and `make lint` are the required checks for every change.

### Known limits

- Public `onboard` supports JavaScript/Jest/GitHub Actions only.
- Public `testdrive` supports JavaScript/Jest only and currently names Jest in a few output fields and filenames.
- The local intake is not a complete Datadog backend. It implements the endpoints exercised by the pinned JavaScript tracer.
- The report is local only. There is no upload, share service, Datadog forwarding, stable JSON contract, or savings calculation.
- The onboarding instructions are copied from the onboarding MCP source rather than shared with it.
- The normal `plan` and `run` commands have richer platform/framework support, but `onboard` and `testdrive` do not use it yet. That is the next milestone.

### Picking up the work

Start with the first pair in Milestone 2, keep the existing Jest path working, and run:

```shell
make test
make lint
make install
ddtest help
```

Then run `ddtest onboard` and `ddtest testdrive` in a real repository for the pair being added. Do not start by designing a general result schema or configuration system; extract a shared piece only when the second real implementation needs it.

## What the user should experience

```text
$ ddtest onboard

Found Jest and the GitHub Actions test job.
Datadog Test Optimization is not configured yet.

Add this setup to .github/workflows/test.yml:
  ...small, concrete snippet...

Then run:
  ddtest testdrive
```

After the human or agent makes the edit:

```text
$ ddtest testdrive

🐕 Preparing dd-trace for this testdrive...
🐕 Running your Jest suite with Test Optimization...

Test Optimization is ready.
3 findings.

Failed tests (2):
  - checkout.test.js › rejects an expired card · Fail · 84ms
  - cart.test.js › removes an item · Fail · 31ms

Flaky tests (1):
  - login.test.js › refreshes a session · Flaky · 146ms

Tests slower than the others (3):
  - search.test.js › ranks results · Pass · 2.4s
  - checkout.test.js › submits an order · Pass · 1.8s
  - reports.test.js › builds a summary · Pass · 1.3s

Run details:
  Test events: 43
  Tests with coverage: 41 / 41
  Jest: Failed
  Tracer: dd-trace@6.15.0 · isolated

Open report: file:///project/.testoptimization/testdrive/2026-09-10-abc123/report.html
```

There is no separate plan, approval file, checksum, or second execution command. Running `ddtest testdrive` is the user's decision to run the test suite.

## Milestone 0: prove the loop — complete

Milestone 0 is a technical spike. It is successful when the whole local loop works, even if the code and output are still rough.

### 0.1 Bring the smallest useful local intake into DDTest

Start from Shepherd's `tools/mockdog`, but take only what the JavaScript/Jest experiment needs:

- bind to a kernel-assigned loopback port;
- answer the settings request with Test Optimization reporting and coverage enabled;
- receive test events and coverage;
- decode them into enough information to count tests and coverage;
- wait for pending payload processing before printing the result.

Do not build every Test Optimization endpoint or a generic protocol framework. Add an endpoint when the real tracer asks for it.

Likely home: `internal/testdrive/intake`.

### 0.2 Run one real tracer against it

Add a developer-only command or integration test that:

1. creates a unique run directory;
2. starts the local intake;
3. installs one pinned `dd-trace` version into that run directory;
4. points the tracer at the local intake with a dummy API key;
5. preloads the tracer using its absolute `ci/init` path;
6. runs a tiny Jest fixture;
7. prints test and coverage counts.

Use simple feature-local Go structs. Do not define a public result schema or blocker taxonomy yet.

### 0.3 Make simultaneous runs independent

This is worth doing immediately because ports and `.testoptimization` files otherwise collide during development and agent use.

Each run gets:

- a unique directory under `.testoptimization/testdrive/`;
- its own loopback listener on port `0`;
- its own tracer installation for the spike;
- its own logs and report files;
- cleanup that only touches that run's files.

Run two fixture testdrives concurrently in a test and prove they do not share events or delete one another's files.

### Milestone 0 is done when

- the real pinned Node tracer reports a real Jest fixture test to the embedded intake;
- code coverage is received and associated with the test;
- two testdrives can run concurrently;
- the test requires no Datadog credentials or Agent;
- `make test` and `make lint` pass.

Then stop and review what the spike taught us before cleaning up or generalizing it.

## Milestone 1: ship the delightful path — preview implemented

Milestone 1 turns the spike into something a human or coding agent can use on a conventional repository.

### 1.1 Ship a rough end-to-end `onboard` and `testdrive`

Implement the two public commands as early as possible so we can dogfood the real interaction.

`ddtest onboard` does a small amount of static detection:

- confirm this looks like a root JavaScript/Jest project;
- find the GitHub Actions workflow and likely test job;
- notice whether the setup DDTest knows how to create is already present;
- print the exact small CI edit and the next command.

The first implementation supports the setup it generates. It uses one pinned `dd-trace` version kept as a normal implementation constant, not a versioned compatibility system.

`ddtest testdrive`:

- creates a unique session;
- uses the repository's tracer when the supported setup already makes it available locally;
- otherwise installs the same pinned tracer into the session directory;
- starts the embedded local intake;
- runs the detected Jest suite once with Test Optimization enabled;
- prints whether events and coverage arrived;
- leaves a simple local report.

Before doing any of that, `testdrive` completes its read-only detection and prints what it is about to do:

- directories and files it will create;
- the tracer and version it will install, if any;
- the external commands it will execute;
- confirmation that it will not change the project's package manifest or lockfile.

It then uses one lightweight confirmation:

- `ddtest testdrive --yes` always prints the preview and continues without prompting;
- when standard input is a terminal, `ddtest testdrive` asks `Continue? [y/N]` and defaults to no;
- when standard input is not a terminal, it never waits for input. It exits and tells the caller to review the preview and run `ddtest testdrive --yes`;
- the interactive prompt also tells coding agents to rerun with `--yes` after reviewing the actions.

Do not try to identify particular coding agents from environment variables. A process attached to a terminal is indistinguishable from a human terminal, so the useful distinction is whether it is safe to prompt. Piped input does not count as confirmation; unattended execution requires `--yes`.

The `testdrive` command owns this interaction. The intake, tracer installer, and other lower-level components never prompt. Detection and command construction happen before confirmation; session creation, tracer installation, server startup, and test execution happen afterward.

This is not a return to execution plans or approval files. There is no persisted plan, checksum, or separate execution command: the preview, confirmation, and run are one interaction.

Do not run an uninstrumented baseline. If customer tests fail but test events arrive, say both things plainly: Test Optimization setup works, and some tests failed.

### 1.2 Try it on real repositories and fix what hurts

Before settling interfaces, try the rough flow on at least five representative repositories:

- no tracer in the project and no CI setup;
- CI-only auto-instrumentation;
- `dd-trace` already installed in the project;
- a suite with failing tests;
- a suite that reports tests but not coverage.

For each run, record:

- what the agent or human had to guess;
- confusing or noisy output;
- incorrect detection;
- tracer traffic the intake did not yet understand;
- how long it took to reach a useful result.

Fix the repeated problems directly. Only introduce shared types or categories when at least two real parts of the flow need them.

### 1.3 Make the result feel good

Polish the terminal output and generate a small self-contained HTML report showing:

- whether Test Optimization instrumentation loaded;
- whether any tests failed;
- whether any tests are flaky because they failed and then passed on retry;
- whether any tests are clear duration outliers;
- whether any tests or suites cover an unusual number of files.

Only render cards for problems that were actually found. Each card always lists the affected tests, and each test expands to show every observed run and duration, retry reasons, errors and stacks, coverage, and a syntax-highlighted source excerpt bounded by the tracer's source lines. Under the problem cards, show a short run-details row with the number of test events, tests with coverage, the Jest result, and the pinned tracer's isolated installation. Link to the saved JSON traffic and complete Jest output instead of spilling either into the report. Add separate paginated tabs for all observed suites and tests. Print the report itself as a clickable absolute `file://` terminal link.

The report does not calculate TIA savings or parallelization. This milestone is a satisfying setup proof, not a performance calculator.

Keep the command surface tiny:

```text
ddtest onboard
ddtest testdrive
```

Add an option only after dogfooding shows that users actually need it. Agent-readable JSON can be added when the real agent loop demonstrates which data is useful; we should not freeze that format upfront.

### 1.4 Package the preview

- make the normal release produce macOS and Linux binaries;
- run the packaged binary against the Jest fixture;
- document the one supported path and its limitations honestly.

### Milestone 1 is done when

A human or agent can start in a conventional Jest repository and complete this flow without a Datadog account:

```text
ddtest onboard
# apply the small suggested CI edit
ddtest testdrive
```

The final screen shows their own test suite, confirms whether Test Optimization events and coverage work, and links to a pleasant local report. The testdrive does not add `dd-trace` to the project or change its lockfile.

## Milestone 2: every supported platform/framework pair

Milestone 2 takes the proven two-command experience to every pair already supported by DDTest's normal `plan` and `run` flow:

| Platform | Frameworks |
| --- | --- |
| JavaScript | Jest, Mocha, Cypress, Playwright, Cucumber, Vitest |
| Python | pytest |
| Ruby | RSpec, Minitest |

This is still a basic, shippable experience. It does not add new frameworks, new CI providers, parallelization, backend forwarding, or a general onboarding engine. GitHub Actions remains the CI setup supported by `onboard` for this milestone.

### 2.1 Detect the pair and print the right setup

Replace the hard-coded JavaScript/Jest detection with a small ordered list of the platform/framework pairs above. Reuse each existing platform and framework's `Detect` method.

`ddtest onboard` should:

- identify the root platform and framework without requiring flags;
- find GitHub Actions jobs that run that framework;
- print one short, repository-owned Markdown instruction for that pair;
- preserve unrelated workflow content and tell the agent exactly where the Test Optimization setup belongs;
- put the local testdrive before the human API-key handoff;
- tell the agent to post every `Open report:` link to the user.

If no pair or more than one pair can be selected safely, say what was found and stop with a useful next step. Do not solve monorepo orchestration in this milestone.

### 2.2 Add one isolated tracer bootstrap per platform

Keep tracer installation outside the customer's dependency files:

- JavaScript pairs share the existing pinned `dd-trace` installer.
- Python/pytest gets one pinned, isolated `ddtrace` installation.
- Ruby/RSpec and Ruby/Minitest share one pinned, isolated Ruby tracer installation.

Each installer only needs to make the tracer's Test Optimization bootstrap available to the test process. Evolve the existing `Tracer` interface when the Python implementation proves what the second platform needs; do not design a generic contract upfront. Do not introduce a version compatibility matrix; one known-good version per platform is enough for this milestone.

### 2.3 Run the framework's real test command

Use the existing framework implementation to build the test command instead of adding a second command table inside testdrive. The testdrive wrapper remains responsible for the session, preview, confirmation, tracer environment, local intake, saved output, and report.

For every pair:

- preview the exact install and test commands;
- enable all Test Optimization features supported by that tracer;
- run the repository's existing test entry point once;
- treat received test events as proof that instrumentation works even when the suite exits non-zero;
- report missing coverage honestly rather than failing an otherwise instrumented run;
- never edit a dependency manifest or lockfile.

### 2.4 Make the result framework-neutral

Remove Jest-specific labels and filenames from the shared path:

- show the detected framework name in run details;
- save output under a neutral name such as `test-output.txt`;
- keep source excerpts readable even when syntax highlighting is not available;
- keep test- versus suite-level coverage attached only to the level used by that tracer;
- keep the same four problem questions, terminal summary, tabs, pagination, artifacts, and clickable report link.

Do not require identical event metadata from every tracer. Add the smallest decoder branches required by real payloads and preserve the same user-facing facts where the data exists.

### 2.5 Prove every pair with a real tracer

Add one tiny fixture for each supported pair. Each integration test must install the pinned tracer, run one real test through the local intake, receive at least one test event, save JSON traffic, and produce the HTML report. Assert coverage at the granularity the tracer emits when coverage is supported.

Dogfood at least one real repository per platform before calling the milestone complete. Fix repeated detection, installation, and reporting problems directly; do not add knobs for hypothetical repository shapes.

### Milestone 2 is done when

For each of the nine pairs in the table, a human or coding agent can run:

```text
ddtest onboard
# apply the suggested GitHub Actions edit
ddtest testdrive
```

The flow requires no Datadog credentials locally, does not change project dependency files, receives real test events, produces the local report, and tells the agent to post its link to the user. All pair fixtures, `make test`, and `make lint` pass.

## Suggested pull requests

Keep the PR sequence short and vertical:

1. **Local intake spike:** minimal Shepherd-derived intake plus golden payload tests.
2. **Real fixture testdrive:** pinned `dd-trace`, Jest fixture, unique session directories, and concurrent-run test.
3. **Public walking skeleton:** rough `onboard` and `testdrive` working on one real repository shape.
4. **Dogfood fixes:** only changes justified by trying the walking skeleton on real repositories.
5. **Delight and release:** terminal polish, local HTML, packaged-binary test, and preview release.

For Milestone 2, keep the sequence vertical and independently demonstrable:

1. **Shared detection and neutral report:** reuse the existing platform/framework detection and remove Jest-only presentation names without changing behavior.
2. **Remaining JavaScript frameworks:** reuse the JavaScript tracer installer and ship Mocha, Cypress, Playwright, Cucumber, and Vitest end to end.
3. **Python/pytest:** isolated Python tracer, onboarding instructions, fixture, and one real-repository dogfood.
4. **Ruby/RSpec and Minitest:** isolated Ruby tracer, onboarding instructions, fixtures, and one real-repository dogfood.
5. **Matrix polish:** run every pair, fix only observed rough edges, and package the shippable preview.

Every PR must run `make test` and `make lint`. The real-tracer integration test should use a pinned dependency so it is reproducible, but normal unit tests should not require a Datadog account.

## Future ideas kept out of Milestone 2

These ideas remain valuable, but none should delay the nine-pair onboarding and testdrive milestone.

### Distribution

Publish a Homebrew installation path after the preview is already useful. Homebrew packaging should not delay dogfooding the commands or shipping binaries.

### One source for onboarding instructions

The walking skeleton keeps a small Markdown copy of the JavaScript/Jest/GitHub Actions instructions in DDTest, derived from the Test Optimization onboarding MCP instructions in `dd-source`. After Milestone 1, investigate making both products embed the same versioned Markdown fragments at build time so fixes do not have to be copied between repositories. A shared build artifact looks like the simplest direction because DDTest must still work offline; do not hold up the first shippable flow to design it now.

### Agent evals

We also need a large `ddeval` suite that asks coding agents to follow these instructions across many real repository and CI shapes. The evals should catch incorrect workflow edits, unrelated changes, bad setup detection, and failures to leave credential creation to the human. Build that coverage from the cases we discover while dogfooding instead of trying to enumerate every case upfront.

### Fully local TIA

Explore fully local TIA. DDTest can keep the coverage reported by each test or suite in a small SQLite database and use it on later testdrives to make the skip/no-skip decision locally. The changed-file input should include both the committed history normally provided through Git upload and the developer's current staged, unstaged, and untracked files, so TIA is useful while code is still being written rather than only after a commit. Start with the conservative rule: run when coverage is missing or intersects a changed file; otherwise skip. This is a post-Milestone 1 experiment, not a dependency of the first onboarding experience.

### Real Datadog mode and deeper analysis

- Let `testdrive` use the real Datadog backend when `DD_API_KEY` is present while keeping the local credential-free mode as the default.
- Detect CI OS and runtime tags and reproduce them locally where that makes TIA decisions representative.
- Use the Datadog API to add historical test performance and known-flake context after the local setup proof works.

### Other later directions

- `ddtest doctor` as a separate command;
- stable public JSON schemas and detailed error taxonomies;
- more CI providers and monorepo orchestration;
- multiple tracer-version support policy;
- local savings estimates;
- historical replay;
- test splitting and parallelization;
- a small hosted Testdog page that can visualize an encoded or uploaded report without requiring a Datadog account.

We will choose the next slice from what people struggle with or ask for after using Milestone 2.
