# Maven Java onboarding: rebase and OSS evaluation

Date: 2026-10-26. PR #194 was rebased directly onto main at `c2782fd0af360ba2fb4baa24b907276c37e248b1`. This record separates real agent trials, evaluator-owned framework fixtures, and automated checks. It does not certify remote CI or Datadog backend processing.

## Reproducible inputs

Every final trial used clean product source `596c4e2cc2479e2ac801aa346b739e3cab63f4a1` and the same Linux ARM64 binary, SHA-256 `5ee7043e7b0e6868d5ad3d67e9ec66f0172daad4d6ceee8d885b39776cb6f90f`. Go 1.27.3 cross-compiled it with `CGO_ENABLED=0`; later evaluation-documentation commits do not change that product source.

| Project / exact source commit | Framework | Image JDK | Full suite command |
| --- | --- | --- | --- |
| [JavaPoet](https://github.com/square/javapoet/tree/b9017a9503b76e11b4ad4c1a9f050e2d29112cb0) | JUnit 4.13.2 | Temurin 8u504-b01 | `mvn --no-transfer-progress verify source:jar javadoc:jar` |
| [java-otp](https://github.com/jchambers/java-otp/tree/479c9b47576788935b7a35b15f3a12edf7cd4897) | JUnit 5.14.4 | Temurin 17.0.20.1+1 | `./mvnw test -B --file pom.xml` |
| [Failsafe](https://github.com/failsafe-lib/failsafe/tree/ed3f92739669769274cf5c9567fd422929c20ad2) | TestNG 6.9.10 | Temurin 17.0.20.1+1 | `mvn -B test` |

Maven was 3.9.16, including java-otp's wrapper. The real tracer selected by the completed reports was `dd-java-agent@1.67.1`. Codex CLI 0.163.0 and Claude Code 2.1.220 used their fresh CLI defaults, with no model override; Codex did not emit its model identity in public events. Claude reported `claude-opus-5[1m]`.

| Dockerfile target | Immutable evaluated image ID |
| --- | --- |
| `java8` | `sha256:627a9512cfa6d7174cd6db727a4bb4f5a8eca69ffb33819bd2882b85b224a536` |
| `java17` | `sha256:50efcf83f300839ad3555bf41caac5bc5d32de89f12da759ef41207b83d48ed9` |

The evaluator used the repository's [onboarding-eval skill](../../skills/ddtest-onboarding-eval/SKILL.md), a dedicated Colima VM, Linux 6.8.0-117-generic ARM64 without emulation, and four CPUs / 6 GiB per container. Each agent got a new pinned clone, private dependency caches, the same project-compatible image and binary, and only this prompt:

> Use ddtest in /trial/bin and onboard this repository to test-optimization for &lt;framework&gt; only.

The container had a read-only root, dropped capabilities, one owned trial mount and selected model authentication. Host CLI profiles, SSH/cloud/GitHub/Datadog credentials and MCP servers were not mounted. Network egress was unrestricted; the dedicated VM had no service credentials or local Datadog intake. Model-account trials were serialized where they shared a login. Dependency compilation and Java/Maven readiness checks happened before the agent; these are setup, not suite-execution evidence.

## Final agent outcomes

| Trial | Native results per mode | Compatibility | Feature controls | Qualification |
| --- | --- | --- | --- | --- |
| javapoet-claude-final | 388 passed / 0 failed / 1 skipped | compatible | 6/6 | POM/test sources unchanged |
| javapoet-codex-final | 388 passed / 0 failed / 1 skipped | compatible | 6/6 | POM/test sources unchanged |
| java-otp-claude-final | 100 passed / 0 failed / 0 skipped | compatible | 6/6 | POM/test sources unchanged |
| java-otp-codex-final | 100 passed / 0 failed / 0 skipped | compatible | 6/6 | POM/test sources unchanged |
| failsafe-claude-final | 305 passed / 1 failed / 0 skipped | inconclusive | 0/6 | POM/test sources unchanged; timing failure stops reactor; later modules not exercised |
| failsafe-codex-final | 305 passed / 1 failed / 0 skipped | inconclusive | 0/6 | POM/test sources unchanged; timing failure stops reactor; later modules not exercised |

All final Java onboarding verdicts remain **INCOMPLETE**: the adapter validates a selected local command but does not certify CI Java/tracer selection or every Maven profile. A successful local suite and six feature controls do not complete repository-wide onboarding. An actual CI run does not update this local report.

## Changes and failed experiments retained

The rebase ports Maven validation to main's preparation, locking/recovery, command deadlines, run roles and feature policy. Main's reporting-only intake optimization discarded capability event tags required by Java probe controls. A real JavaPoet experiment exposed the regression: its full suite reconciled but all six controls were inconclusive. The fix retains events explicitly for those controls without enabling an optimization feature; a real-intake regression test and the final matrix exercise it.

Earlier attempts remain separate evidence:

| Source / attempt | Observed result |
| --- | --- |
| `021ad6d`, JavaPoet/Failsafe Codex | Setup rejected an untracked `?/` Maven cache created because numeric container UIDs had no Java home. No onboarding model session started. |
| `021ad6d`, java-otp Codex | Setup failed resolving the publishing plugin. This is not a Java compatibility result. |
| `021ad6d`, JavaPoet Claude | Model transport rejected a legitimate multiline managed model header before onboarding. |
| `60e7700`, JavaPoet Codex v2 | Account lock busy; no onboarding session started. |
| `60e7700`, JavaPoet Claude v2 / Codex v3 | Full native suite and events compatible, but six capability-dependent checks inconclusive; prompted the capture fix above. |
| `60e7700`, java-otp Claude v2 / Codex v3 | Interrupted when the JavaPoet product regression was found. No completed agent outcome claimed. |

The evaluator now sets private Java/Maven homes and transports legitimate multiline model headers through a private env file and trusted container shim. Header values never appear on Docker argv, and the transport variable is removed before the model CLI starts. These harness fixes have independent offline regression coverage. No authentication failures were retried or bypassed; the busy Codex login was allowed to become available.

Agent-selected repeated testdrive invocations belong to their individual trials and are retained in activity logs; they are not additional independent trials. Claude's JavaPoet trial first shortened the CI command, later ran the complete command, and disclosed a transient file-descriptor failure and retry. It also used manual background jobs and long sleeps; container teardown stopped all descendants. Only the final retained report is counted in the result table.

## Independent assessment and scope

Both agents preserve the full JavaPoet CI command. Codex instruments both java-otp test and deploy workflows; Claude only instruments the test workflow and explicitly defers the detected deploy test lifecycle, leaving repository-wide configuration coverage blocked. Existing Maven commands and JDK matrices are retained. Local Temurin validation does not certify JavaPoet's CI Zulu distribution or the other matrix JDKs.

Claude's completed JavaPoet and java-otp handoffs correctly separate local PASSED from overall INCOMPLETE and name the structural CI tracer blocker. Codex's JavaPoet counts and qualified verdict are accurate, with imprecise CI-blocker wording. Three handoffs fail the reporting criterion. Two are Codex: java-otp implies a real CI run can finish this local verdict; Failsafe attributes inconclusive compatibility to the test failure, although matching failures are allowed and telemetry identity is the blocker. The Failsafe reactor stopped before the later modules, so its 306 executions are not full-reactor coverage. The runner flagged all paraphrased handoffs for review; independent grading evaluates their actual claims rather than treating missing canonical text as an automatic failure.

Claude's Failsafe response discloses three full-suite attempts: 305 passed / 1 failed in both modes; then baseline 305/1 versus reporting 304/2 (**suspected regression**, local FAILED); then 305/1 in both modes again (**inconclusive**). The first two JSON/HTML pairs were also preserved as observations before the final report replaced them. The extra instrumented-only `RateLimiterImplTest.testAcquirePermit` failure remains unresolved: eight passing uninstrumented subset runs do not demonstrate a matching instrumented outcome or clear that failed comparison. The final response fails the reporting criterion by attributing inconclusive compatibility to the existing red test and calling equal event totals working instrumentation. Existing failures can be compatible; source-test identity reconciliation is still required. Failsafe's original TestNG 6.9.10 dependency and all test sources remained unchanged, and the final stopped reactor does not validate the later modules.

Report HTML contains the real reporting-only suite classes and no generated `DdtestValidationTest` class. JSON/native counts and telemetry identities were checked separately. Original POMs and test sources stayed unchanged in the final trials. Static workflow edits were inspected from retained files because generic patch redaction also masks the literal API-secret reference; no real API secret was supplied.

The fixture matrix independently exercised JUnit 4, JUnit 5, TestNG, Spock, Cucumber JVM, Karate, ScalaTest and Scala MUnit with the final source and real tracer. All eight comparisons passed: **46 supported feature controls passed**, and MUnit skipping/disable remained **not supported**. This is evaluator-owned fixture evidence, not eight additional open-source model trials.

Automated validation:

- `make lint`: passed with the repository-pinned golangci-lint 2.14.0.
- `make test`: the macOS host fails `TestMochaNestedGuardDisablesTracingOnlyForNestedCLIs`; the same failure reproduced on unchanged main. The complete suite excluding exactly that test passed, and that test passed in a Linux container. This inherited host failure remains explicit.
- Evaluation runner: 83 offline tests passed, nine Docker cases skipped in that invocation; all nine Docker-boundary cases then passed separately using fake CLIs. They do not substitute for the six real model trials.

Each completed trial has an independent `assessment.md`, public activity, final response, status/patch, a report pair and relevant workflow/POM snapshots with SHA-256 hashes. ddtest session cleanup, agent artifacts at CLI exit, container removal and owned scratch deletion were assessed separately. The final tracked statuses and report pairs were reviewed, but a complete independent inventory of ignored agent build artifacts at CLI exit was not collected, so agent scratch cleanup is not graded as passed. Evaluator teardown removed every ownership-verified trial after evidence preservation; no shared caches or unrelated trials were pruned. The dedicated Java VM was then removed.

The compact [machine-readable record](java-maven-oss-2026-10-26.json) records identities, results and qualifications. Full local evidence is outside the checkout at `/tmp/ddtest-java-oss-2026-10-26/`; this temporary directory is not durable hosted storage.

## Reproduce with the current runner

Follow the eval skill's dedicated-VM and model-authentication requirements. Build the appropriate image target from this branch, resolve its immutable ID, and build the Linux binary for that image's architecture:

```sh
docker build --target java8 -t ddtest-eval-java8 \
  -f skills/ddtest-onboarding-eval/scripts/Dockerfile \
  skills/ddtest-onboarding-eval/scripts
IMAGE=$(docker image inspect --format '{{.Id}}' ddtest-eval-java8)
ARCH=$(docker image inspect --format '{{.Architecture}}' "$IMAGE")
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build \
  -ldflags "-X github.com/DataDog/ddtest/internal/buildinfo.Version=$(git rev-parse HEAD)" \
  -o /absolute/path/to/ddtest-linux .

python3 skills/ddtest-onboarding-eval/scripts/run_eval.py \
  --image "$IMAGE" \
  --repo https://github.com/square/javapoet.git \
  --ref b9017a9503b76e11b4ad4c1a9f050e2d29112cb0 \
  --ddtest /absolute/path/to/ddtest-linux --agent codex --framework junit4 \
  --existing-login \
  --setup-command 'mvn -q -DskipTests test-compile' \
  --readiness-command 'java -version' --readiness-command 'mvn -version' \
  --timeout 1800 --output /absolute/path/to/new-javapoet-codex-evidence
```

Repeat with Claude and a new evidence directory. For java-otp and Failsafe, build `--target java17` and use the table's repository/ref/framework. java-otp setup is `./mvnw -q -DskipTests test-compile`; Failsafe setup is `mvn -q -DskipTests test-compile`. Give no coaching, rubric or earlier results to the tested agent. Preserve both reports and relevant configuration, independently grade the result, then run `run_eval.py --cleanup <evidence-directory>`. Inconclusive results and dependency changes must stay qualified.
