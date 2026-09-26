# Agent-driven onboarding

Status: Milestones 0, 1, and 2 implemented; Milestones 3 and 4 proposed

Last updated: 2026-09-24

## Validation prototype update

The prototype based on PR #147 now uses the validation contract described in
[the README](../../README.md): paired Jest compatibility runs, separate controlled
feature probes, terminal output and one self-contained report at
`.testoptimization/testdrive.json`, and project-tracer reuse or a resolved fallback installation.
The compact report retains success, verdicts, validation commands, modes, exit codes, and aggregate counts; raw output and events are discarded. Each run removes its scratch files and updates the same report. Configuration-only, unsupported-framework, and setup-failure runs preserve the latest paired Jest execution as historical evidence, without reusing its verdict for the current invocation. A new paired Jest execution supersedes that evidence. Onboarding targets the v3 GitHub Action without tracer
version pins in the universal template. The agent selects a compatible release for the repository and aligns the CI input with the locally checked version. Jest preflight checks the effective configuration before suite execution; `--check-only` reruns configuration checks without tests. Static matrices support includes/excludes and common boolean conditions. Unknown checker syntax remains unverified and must not cause workflow rewrites. Local compatibility, features, CI runtime compatibility, tracer agreement, and actual CI execution are reported separately. Other frameworks can collect telemetry but remain explicitly
unvalidated. Receiving events alone is not proof of compatibility.

The milestone narrative below describes the earlier implementation and its
historical evidence, including the removed HTML report and pinned versions.

## Goal

A user should be able to give a coding agent one prompt:

> Onboard test optimization using ddtest.

The agent discovers the flow through `ddtest help`, makes the small CI edit suggested by `ddtest onboard`, runs `ddtest testdrive`, and gives the user a clickable report showing their own tests.

The local testdrive requires no Datadog account, API key, or Agent. Credentials come last, are configured by a human in CI, and are never shown to the coding agent.

## Product rules

- Use the customer's real test suite, not a demo.
- Keep the main flow to `ddtest onboard` and `ddtest testdrive`.
- Detect the repository instead of asking setup questions.
- Show commands and filesystem changes before running them.
- Keep JavaScript and Python tracer installations isolated; disclose that Ruby fallback uses bundle add and updates project dependency files.
- Explain what worked, what did not, and the next useful action.
- Give the user clickable local and CI links.
- Dogfood a thin end-to-end slice before adding abstractions.

For now, do not build a generic onboarding framework, YAML editor, public result schema, compatibility matrix, or exhaustive error taxonomy.

## What exists today

The working preview is in draft PR #128 on `anmarchenko/agentic-onboarding-runbook`.

### User flow

- `ddtest help` points to `ddtest onboard`.
- `ddtest onboard` detects all nine supported platform/framework pairs and candidate GitHub Actions test workflows; the coding agent inspects the actual jobs. Repositories with several frameworks use `--framework`.
- It prints repository-owned Markdown instructions containing the concrete workflow edit, asks the agent to run `ddtest testdrive`, and tells the agent to post every report link to the user.
- The local testdrive comes before the human API-key and GitHub-secret steps.
- `ddtest testdrive` previews its commands and file changes. Interactive terminals ask once for confirmation. Non-interactive callers must review the preview and rerun with `--yes`.
- `make install` builds and installs DDTest into the current user's Go bin directory on macOS and Linux.

### Local testdrive

- Each run has a unique directory and kernel-assigned loopback port, so sessions can run concurrently.
- Every language reuses the project tracer when its standard platform check succeeds. If it fails, install latest or a release/Git revision selected with `--tracer-version`. JavaScript and Python install inside the session; Python keeps the selected interpreter. Ruby runs `bundle add datadog-ci` and tests against the project bundle, updating its Gemfile and lockfile.
- The local intake supports the endpoints exercised by that tracer and enables Test Optimization, coverage, Intelligent Test Runner, Early Flake Detection, Auto Test Retries, Impacted Tests, failed-test replay, and Test Management.
- Test events and test- or suite-level coverage are decoded. Raw multipart or msgpack payloads are not retained; saved traffic is JSON only.
- Complete test output is saved separately.

The terminal and self-contained HTML report answer:

- Did any tests fail?
- Are any tests flaky because they passed on retry?
- Are any tests unusually slow compared with the median?
- Do any tests or suites cover unusually many files compared with the median?

Problem cards appear only when the answer is yes. Affected tests are always visible and expand to show attempts, timings, errors, retry information, source excerpts, and coverage. Flaky tests use `test.final_status` and are not also reported as failed. Covered files appear one per line and are paginated 50 at a time. Separate paginated tabs list all suites and tests.

The report also shows event and coverage counts, framework result, project-tracer reuse or the fallback tracer selection, saved JSON traffic, and test output. `testdrive` prints it as an absolute clickable `file://` link.

### Code map

- `internal/onboard/`: repository detection and embedded Markdown instructions.
- `internal/testdrive/`: session lifecycle, preview, execution, terminal output, and HTML report.
- `internal/testdrive/intake/`: local intake, JSON capture, event and coverage decoding, and findings.
- `internal/platform/` and `internal/framework/`: platform-owned tracer detection and installation, platform/framework detection, and test commands.

### Evidence and limits

- Existing integration tests run the public CLI with real tracers against fixtures for all nine frameworks. Coverage is reported only when supplied by the tracer.
- A concurrent integration test proves port, traffic, and file isolation.
- Unit tests cover decoding, final status, flaky tests, both coverage granularities, medians, source excerpts, HTML rendering, and confirmation behavior.
- Dogfooding on React Native Paper recognized 1,363 events and coverage for all 680 logical tests, including Early Flake Detection retries.

Public support covers all nine pairs in Milestone 2, with GitHub Actions onboarding. The intake is not a complete Datadog backend. There is no upload service, Datadog forwarding, stable JSON contract, or savings calculation. Onboarding instructions are copied from the onboarding MCP source rather than shared with it.

Every change must pass:

```shell
make test
make lint
```

## Milestone 0: prove the local loop — complete

Milestone 0 proved the risky path before designing the product around it:

- start a minimal local Test Optimization intake on port `0`;
- install and run a real pinned JavaScript tracer;
- receive a real Jest test event and coverage;
- save each run in its own session directory;
- run two sessions concurrently without collisions;
- require no Datadog credentials or Agent.

## Milestone 1: delightful Jest preview — implemented

Milestone 1 turned the spike into the current `onboard` and `testdrive` flow described above.

Its important interaction contract is:

- detection and preview happen before any write or external command;
- `ddtest testdrive --yes` is the explicit non-interactive path;
- running the command is one decision—there is no persisted plan, checksum, approval file, or second execution command;
- instrumentation success is independent of whether customer tests pass;
- the project manifest and lockfile are never changed;
- the agent posts the report link to the user.

Keep dogfooding Jest while later milestones are built. Fix repeated real problems directly; extract shared types only when another working implementation needs them.

## Milestone 2: every supported platform/framework pair — implemented

Extend the same basic onboarding and testdrive to the pairs already supported by DDTest:

| Platform | Frameworks |
| --- | --- |
| JavaScript | Jest, Mocha, Cypress, Playwright, Cucumber, Vitest |
| Python | pytest |
| Ruby | RSpec, Minitest |

Implemented in vertical slices:

1. Reuse each platform and framework's `Detect` method and test command. Remove Jest-specific names from the shared report.
2. Add the remaining JavaScript frameworks using the existing isolated `dd-trace` installation.
3. Add one pinned isolated `ddtrace` installation for pytest.
4. Use bundle add for Ruby tracer installation shared by RSpec and Minitest.
5. Add a tiny real-tracer fixture for every pair and dogfood at least one real repository per platform.

For every pair, `onboard` finds the relevant GitHub Actions job and prints one small setup. `testdrive` previews and runs the detected framework's normal command (or the explicit `--command` entry point), treats received events as proof even when tests fail, reports missing coverage honestly, and preserves the same terminal and HTML experience where the tracer supplies the data.

Do not solve monorepos, new CI providers, or cross-platform tracer abstractions here. One known-good tracer version per platform is enough.

All nine pairs have completed the credential-free flow with JSON traffic and local reports. Manifests and lockfiles are checked for preservation. The validation record distinguishes full-suite runs from representative browser/RSpec subsets and records the pinned Cucumber tracer workaround.

## Milestone 3: guided Test Parallelization onboarding

Starting prompt:

> Onboard test parallelization using ddtest.

Add `ddtest onboard parallelization` as the obvious next step after Test Optimization works. If Test Optimization is not configured, point back to `ddtest onboard`; do not combine both migrations.

The command detects the platform, framework, and GitHub Actions test job, then tells the coding agent how to make one concrete transformation using the existing product:

1. A plan job installs the project as CI already does and runs `ddtest plan`.
2. The plan job exposes DDTest's generated matrix and uploads `.testoptimization/`.
3. A matrix job downloads the artifact and runs `ddtest run --ci-node ${{ matrix.ci_node_index }}`.
4. The old command is removed so CI does not run the full suite twice.

Start with one worker per CI node, `fail-fast: false`, explicit minimum and maximum parallelism, and the existing CI-job overhead model. Preserve runtime setup, environment, services, caches, permissions, timeouts, and artifacts. DDTest prints instructions; the agent edits the workflow. Do not build a YAML rewriting engine or another planner.

The first real GitHub Actions run is the test drive. The agent gives the user its link and reports:

- whether the plan job and every node passed;
- selected node count;
- estimated full-suite and parallel wall time;
- modeled CI overhead and imbalance;
- dedicated slow-suite runners, if any;
- the smallest corrective edit for a concrete setup failure.

Selecting one node is a valid success when extra nodes would not help enough. Support the same nine pairs as Milestone 2. Stop clearly when an existing matrix or parallel runner cannot be combined safely.

Milestone 3 ships when an agent can discover the flow from the starting prompt, make a reviewable GitHub Actions edit, run the existing planner and runner, and return a clickable CI link with the important plan facts.

## Milestone 4: `dd-trace-js` runbook parity

Match the useful conclusions of the validation runbook, not its internal architecture. Keep `ddtest onboard` and `ddtest testdrive`; do not copy its manifest, execution-plan, checksum, approval-file, persisted-lock, or exit-code machinery.

The terminal and local report show five independent conclusions:

- **Basic Reporting:** the tracer reports a real project test.
- **CI configuration:** the selected job visibly initializes Test Optimization and configures transport.
- **Early Flake Detection:** a new passing test is retried with the expected reason.
- **Auto Test Retries:** a fail-once test passes on retry with the expected reason.
- **Test Management:** a configured test is matched and tagged as quarantined.

Each conclusion is simply works, needs attention, or could not be checked. Name the exact missing prerequisite, first useful action, and cleanup status. Keep this validation scope separate from code-coverage counts.

Implementation order:

1. Add all five conclusions to the simplest Jest repository and dogfood the complete flow before generalizing it.
2. Keep the normal happy path: the customer's instrumented suite proves Basic Reporting.
3. After Basic Reporting succeeds, run small DDTest-owned tests for the three advanced features. The local intake supplies settings, known tests, and managed tests; emitted attempts and events must prove behavior.
4. If Basic Reporting is inconclusive, compare one representative test without and with instrumentation and debug logging. Do not double every successful testdrive.
5. Audit the selected GitHub Actions job without executing it. Resolve the setup DDTest generates, direct commands, and simple local package scripts; report dynamic or remote wrappers as inconclusive.
6. Extend the proven Jest slice to Mocha, Cypress, Playwright, Cucumber, and Vitest.

Temporary tests appear in the preview, are created only after confirmation, and are removed after the run. Their decoded JSON events and output remain in the session. Browser- or application-backed checks may be inconclusive with the missing prerequisite named; DDTest does not start applications or install browsers implicitly.

Python and Ruby retain Milestone 2's Basic Reporting and suite analysis until their tracer behavior and real dogfood cases justify equivalent advanced checks.

Milestone 4 ships when all six JavaScript frameworks report the five conclusions without Datadog credentials, preserve project dependencies and concurrent-session isolation, clean up temporary tests, and provide clickable local and CI links.

## Next work

Milestone 2 now has public-CLI real-tracer fixtures for every pair and independent open-source dogfood runs. See [the validation record](../testing/onboarding-milestone-2.md). The next planned feature is Milestone 3; keep the existing nine-pair flow green.

For each slice:

1. add one real-tracer fixture;
2. run it in a real repository;
3. record what the human or agent had to guess;
4. fix observed friction;
5. run `make test`, `make lint`, and the installed binary.

## Later ideas

These should not delay Milestones 2–4:

- Homebrew distribution.
- One offline source for the Markdown instructions currently duplicated with the onboarding MCP implementation in `dd-source`.
- A broad `ddeval` suite built from real repository and CI shapes.
- Fully local TIA backed by SQLite coverage history, considering committed, staged, unstaged, and untracked changes.
- Real Datadog mode when `DD_API_KEY` is present.
- Local reproduction of CI operating-system and runtime tags.
- Historical test analysis through the Datadog API.
- `ddtest doctor` as a reusable diagnostic command if the integrated flow proves it is needed.
- More CI providers and monorepo orchestration.
- Multiple tracer-version support.
- Local savings estimates and historical replay.
- A hosted Testdog page for sharing a report without a Datadog account.

Choose the next slice from what users struggle with after Milestone 4, not from a speculative architecture.

## References

- `~/p/shepherd/tools/mockdog`: local Test Optimization intake precedent.
- `~/p/dd-trace-js/ci/runbook.md`: validation conclusions and JavaScript adapter behavior.
- `~/p/test-visibility-install-script`: isolated JavaScript tracer installation precedent.
- The onboarding MCP instructions in `~/dd/dd-source`: current source material for CI setup instructions.
