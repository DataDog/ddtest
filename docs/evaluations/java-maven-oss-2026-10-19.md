# Maven Java onboarding: open-source agent evaluation

Date: 2026-10-19. This is an evaluation record, not a claim of production/CI certification.

Four public projects were evaluated with real Claude and Codex CLIs. Each trial used a fresh pinned clone, the same binary and runtime within its paired experiment, and only the runner's minimal onboarding prompt. Failed attempts are retained. No test source was removed or rewritten to make a suite pass.

## Inputs

| Project / pinned commit | Framework | Java | Intended local CI-suite command |
| --- | --- | --- | --- |
| [JavaPoet](https://github.com/square/javapoet/tree/b9017a9503b76e11b4ad4c1a9f050e2d29112cb0) | JUnit 4.13.2 | Zulu 8.0.504 | `mvn --no-transfer-progress verify source:jar javadoc:jar` |
| [java-otp](https://github.com/jchambers/java-otp/tree/479c9b47576788935b7a35b15f3a12edf7cd4897) | JUnit 5.14.4 | Temurin 17.0.20.1 | `./mvnw test -B --file pom.xml` |
| [Failsafe](https://github.com/failsafe-lib/failsafe/tree/ed3f92739669769274cf5c9567fd422929c20ad2) | TestNG 6.9.10; qualified diagnostic upgrade to 7.5 | Temurin 17.0.20.1 | `mvn -B test` |
| [jsoup](https://github.com/jhy/jsoup/tree/4cf664cdb2e9d2cc2d40fdf3bb9c0be0be22f865) | JUnit 5.14.4 | Temurin 17.0.20.1 | `mvn -X verify -B --file pom.xml` (Claude omitted Maven debug verbosity) |

macOS ARM64; Maven 3.9.16, including java-otp's pinned wrapper; dd-java-agent 1.67.1. Codex CLI 0.163.0 used its default model (not emitted in public events). Claude Code 2.1.220 reported `claude-opus-5`. No model override was requested.

| Binary | Product source | SHA-256 |
| --- | --- | --- |
| v1 | `25ee9f87776cddc80cc3aa667f4dcb067a682812` | `074756f35658bed7275b5e2dfcc89f4249d6cce36bd941acef5bfee2d92c0ccd` |
| v2 | `ac366de6b9b95246017d9819b5bec3c0914b3d97` | `e8a441820c2fd75d29381b1a706dcadfbaf43881ce92a27b6e6643005ec8d765` |

The evaluator gained Java runtime/cache isolation separately from the product. Each trial had private Maven dependencies and temporary directories; v2 also isolated wrapper downloads with `MAVEN_USER_HOME`. Codex ignored user CLI configuration; Claude used project settings and empty strict MCP configuration. DD/DATADOG environment variables were removed. These settings do not isolate account-level CLI state or the host filesystem.

## Trial outcomes

“Native parity” compares both modes' identities/outcomes and exit codes. It does not by itself establish telemetry identity agreement. Counts include existing skipped tests, not distinct tests across repeated configurations. Feature checks require controls, body counters, native outcomes and telemetry.

| Trial | Native results in each mode | Telemetry reconciliation | Feature checks | Qualification |
| --- | --- | --- | --- | --- |
| failsafe-claude-v1 | 324 passed, 0 failed, 0 skipped | inconclusive | 0/6 | POM unchanged |
| failsafe-claude-v2 | 324 passed, 0 failed, 0 skipped | compatible | 15/18 | TestNG upgraded to 7.5; interrupted before final response |
| failsafe-codex-v1 | 324 passed, 0 failed, 0 skipped | inconclusive | 0/6 | POM unchanged |
| failsafe-codex-v2 | 324 passed, 0 failed, 0 skipped | inconclusive | 0/6 | POM unchanged |
| java-otp-claude-v2 | 100 passed, 0 failed, 0 skipped | compatible | 6/6 | POM unchanged |
| java-otp-codex-v2 | 100 passed, 0 failed, 0 skipped | compatible | 6/6 | POM unchanged |
| javapoet-claude-v1 | 388 passed, 0 failed, 1 skipped | compatible | 6/6 | Renamed Maven display name; not an unchanged-build pass |
| javapoet-claude-v2 | 388 passed, 0 failed, 1 skipped | compatible | 6/6 | POM unchanged |
| javapoet-codex-v1 | 388 passed, 0 failed, 1 skipped | inconclusive | 0/6 | POM unchanged |
| javapoet-codex-v2 | 388 passed, 0 failed, 1 skipped | compatible | 6/6 | POM unchanged |
| jsoup-claude-v1 | 2447 passed, 0 failed, 0 skipped | inconclusive | 0/6 | POM unchanged |
| jsoup-claude-v2 | Not completed | not exercised | Not exercised | interrupted; no completed agent outcome claimed |
| jsoup-codex-v1 | 2447 passed, 0 failed, 0 skipped | inconclusive | 0/6 | POM unchanged |
| jsoup-codex-v2 | 2447 passed, 0 failed, 0 skipped | inconclusive | 0/6 | POM unchanged |

The v2 JavaPoet and java-otp pairs demonstrate compatible full suites and all six supported feature checks with their original POMs. JavaPoet preserves its one pre-existing skipped test. The overall ddtest onboarding verdict remains **INCOMPLETE** because this Java adapter does not certify CI Java/tracer selection, other profiles, operating systems or backend processing.

## Findings and fixes

- Maven tracer module labels use the project display name, which can differ from the artifact ID and contain spaces. Added display-name mapping with ambiguity rejection. Claude's v1 JavaPoet workaround changed `<name>JavaPoet</name>` to `javapoet`; v2 preserves the original name.
- Framework dependencies can be inherited from local reactor parents. Added coordinate-checked local parent resolution, respecting disabled lookup and rejecting cycles. Remote parents/BOM/property/profile resolution remains limited.
- Older Surefire/TestNG XML can lose method names. TestNG's own native XML restores them only when its per-class outcomes agree with Maven's duplicate report. This restores 324 Failsafe native source identities; it cannot create missing tracer names.
- Default JUnit 5 parameterized methods use different native and tracer names. Reconcile their invocation indexes, retaining multiplicity and rejecting a duplicated event that substitutes for a missing index. java-otp exercises this with a 100-test suite. jsoup's custom names such as `fragment=true` omit the index; these remain inconclusive rather than accepting equal aggregate counts.
- Original Failsafe/TestNG 6.9.10 events omit `test.name`; a preserved raw event has resource `dev.failsafe.DelayablePolicyTest.`. Codex retains this dependency and reports incomplete. Claude independently upgraded TestNG to 7.5, retaining the 324 tests. That is a qualified dependency-change result, not certification of 6.9.10.
- Modern TestNG quarantine uses `SUCCESS_PERCENTAGE_FAILURE`, which Surefire treats as success. The first reader rejected it. Added the tolerated status and a regression test. A separate TestNG 7.11 controlled fixture then demonstrated compatibility and all six checks. This post-v2 fix is separate from the unassisted agent binaries.

A fresh Failsafe clone with only the disclosed TestNG 7.5 dependency upgrade, evaluated with the final quarantine parser fix, reconciled all 324 results/events and passed all 18 feature checks across core, okhttp and retrofit. This is an evaluator diagnostic, not an upgrade of either unassisted agent result.

Additional evaluator diagnostics are identified separately in the retained evidence. They do not upgrade an unassisted trial's outcome. Interrupted trials are preserved without claiming completion. The extra jsoup/Claude v2 run stalled in Maven's HTTPS artifact resolver; its JVM stack and partial evidence were retained before stopping its owned processes. Both v1 agents and v2 Codex had completed the full 2,447-test jsoup comparison. No narrowed diagnostic subset is counted as full-suite validation. The extra Failsafe/Claude v2 session reached its 1,800-second limit while exploring a Surefire workaround after the completed 324-test run and 15/18 feature checks; its final response and scratch cleanup were not completed.

## Independent review and limits

Workflow diffs preserve existing test commands and matrices. Review conditional Windows instrumentation separately. java-otp/Codex instruments both the test and deploy workflows; java-otp/Claude only instruments the test workflow, leaving repository-wide CI coverage incomplete. Action v3's official definition supports Java and pins tracer 1.67.1; actual action execution was not performed.

Final responses are checked against JSON for verdicts and counts. A matching INCOMPLETE verdict is truthful reporting, not a product pass. POM/dependency workarounds, interrupted sessions and untested CI scope remain explicit. The runner's generic configuration-coverage review requires manual interpretation because Java does not emit the JavaScript matrix fields.

ddtest session cleanup is checked separately from agent scratch cleanup and evaluator teardown. Exact owned trial roots are removed only after collecting reports, patches, workflow files, ignored-file status, activity, final responses and assessments. Shared/user caches are not pruned. HTML contains the real reporting-only suites, not the probe classes; browser visual review was unavailable because no browser connection was exposed.

`make test`, `make lint` and the evaluator's 30 offline tests were run after code changes. These do not replace the real-project observations above. This evaluation does not certify every supported framework/version, every Maven profile, remote CI, production optimization or Datadog backend processing.

## Reproduce

Build this PR, select the pinned repository/ref and matching JDK above, and invoke the evaluator from the ddtest workspace with authenticated CLIs. Put Maven on PATH. Use a new output directory per agent and trial:

```sh
python3 skills/ddtest-onboarding-eval/scripts/run_eval.py \
  --repo https://github.com/square/javapoet.git \
  --ref b9017a9503b76e11b4ad4c1a9f050e2d29112cb0 \
  --ddtest /absolute/path/to/ddtest --agent codex --framework junit4 \
  --java-home /absolute/path/to/jdk8 \
  --setup-command 'mvn -q -DskipTests test-compile' \
  --output /absolute/path/outside-target/javapoet-codex
```

Repeat with `--agent claude` and a new output path. For the other projects change the repo/ref/framework/JDK using the input table. Dependency compilation is setup, not suite-execution evidence. Give no coaching or additional rubric to the tested agent.

The compact [machine-readable record](java-maven-oss-2026-10-19.json) includes commands, binary hashes, counts, verdicts and qualifications. Full local evidence is retained outside the source checkout at `/tmp/ddtest-java-oss-results/`: each trial has `run.json`, `activity.jsonl`, `changes.patch`, statuses, an assessment, final response when available and preserved HTML/JSON artifacts. Temporary evidence is not durable hosted storage; copy it before cleaning that directory.
