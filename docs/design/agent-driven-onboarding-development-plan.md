# Agent-driven onboarding: milestones 0 and 1

Status: proposed

Last updated: 2026-09-10

Related design: [Agent-driven onboarding for DDTest](agent-driven-onboarding.md)

## How we will build this

We will build one thin path all the way through, try it on real repositories, and improve it from what we learn.

We will not design a general onboarding platform first. Output structures, error categories, configuration files, and extension points can emerge after the end-to-end flow works.

The first path is deliberately narrow:

- JavaScript
- Jest
- GitHub Actions
- repository dependencies already installed
- tracer configured through the Test Optimization auto-installation flow
- credential-free local testdrive

Anything else can initially return a clear “not supported yet” message.

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

Setup works.
  428 tests reported
  426 tests produced coverage
  2 tests failed
  18.4s total test time

Open the report:
  .testoptimization/testdrive/2026-09-10-abc123/report.html
```

There is no separate plan, approval file, checksum, or second execution command. Running `ddtest testdrive` is the user's decision to run the test suite.

## Milestone 0: prove the loop

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

## Milestone 1: ship the delightful path

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
- tracer version and whether it came from the project or testdrive;
- tests passed, failed, skipped, and reported;
- suites and durations;
- tests with and without coverage;
- obvious instrumentation problems;
- one useful next action.

The report does not calculate TIA savings or parallelization. This milestone is a satisfying setup proof, not a performance calculator.

Keep the command surface tiny:

```text
ddtest onboard
ddtest testdrive
```

Add an option only after dogfooding shows that users actually need it. Agent-readable JSON can be added when the real agent loop demonstrates which data is useful; we should not freeze that format upfront.

### 1.4 Package and release the preview

- make the normal release produce macOS and Linux binaries;
- publish a Homebrew preview installation path;
- run the packaged binary against the Jest fixture;
- document the one supported path and its limitations honestly.

### Milestone 1 is done when

A human or agent can start in a conventional Jest repository and complete this flow without a Datadog account:

```text
brew install ...ddtest preview formula...
ddtest onboard
# apply the small suggested CI edit
ddtest testdrive
```

The final screen shows their own test suite, confirms whether Test Optimization events and coverage work, and links to a pleasant local report. The testdrive does not add `dd-trace` to the project or change its lockfile.

## Suggested pull requests

Keep the PR sequence short and vertical:

1. **Local intake spike:** minimal Shepherd-derived intake plus golden payload tests.
2. **Real fixture testdrive:** pinned `dd-trace`, Jest fixture, unique session directories, and concurrent-run test.
3. **Public walking skeleton:** rough `onboard` and `testdrive` working on one real repository shape.
4. **Dogfood fixes:** only changes justified by trying the walking skeleton on real repositories.
5. **Delight and release:** terminal polish, local HTML, packaged-binary test, and Homebrew preview.

Every PR must run `make test` and `make lint`. The real-tracer integration test should use a pinned dependency so it is reproducible, but normal unit tests should not require a Datadog account.

## Deliberately later

These may be good ideas, but they are not milestones 0 or 1:

- `ddtest doctor` as a separate command;
- stable public JSON schemas and detailed error taxonomies;
- generalized language, framework, and CI abstractions;
- multiple tracer-version support policy;
- Datadog backend forwarding;
- local TIA simulation and savings estimates;
- historical replay;
- test splitting and parallelization;
- hosted Testdog sharing;

We will choose the next slice from what people struggle with or ask for after using milestone 1.
