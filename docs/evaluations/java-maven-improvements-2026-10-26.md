# Maven diagnostics and Java agent handoff: OSS evaluation

Date: 2026-10-26. Follow-up to the [earlier Java OSS evaluation](java-maven-oss-2026-10-26.md). This record distinguishes local product validation, agent reporting, evaluator-owned diagnostics and untested CI/backend scope.

## Inputs and method

The final product source is `6587518f1e9bd2d174e23eee9882c01f09b835e5`, clean at build. The Linux ARM64 binary SHA-256 is `1ccf8b37d579ee84234a0b637e8b81f88a9fd3df63ca989025201cd58a190b0c`; Go 1.27.3, CGO disabled. Later evaluation-documentation commits do not change that product source.

| Project / pinned source | Framework | JDK | Intended full CI-suite command |
| --- | --- | --- | --- |
| [JavaPoet](https://github.com/square/javapoet/tree/b9017a9503b76e11b4ad4c1a9f050e2d29112cb0) | JUnit 4.13.2 | Temurin 8u504-b01 | `mvn --no-transfer-progress verify source:jar javadoc:jar` |
| [java-otp](https://github.com/jchambers/java-otp/tree/479c9b47576788935b7a35b15f3a12edf7cd4897) | JUnit 5.14.4 | Temurin 17.0.20.1+1 | `./mvnw test -B --file pom.xml` |
| [Failsafe](https://github.com/failsafe-lib/failsafe/tree/ed3f92739669769274cf5c9567fd422929c20ad2) | TestNG 6.9.10 | Temurin 17.0.20.1+1 | `mvn -B test` |

The baseline and follow-up use the same immutable images, Maven 3.9.16, Codex CLI 0.163.0 and Claude Code 2.1.220. Both CLIs use their fresh defaults, without a model override. Java 8 image: `sha256:627a9512cfa6d7174cd6db727a4bb4f5a8eca69ffb33819bd2882b85b224a536`; Java 17 image: `sha256:50efcf83f300839ad3555bf41caac5bc5d32de89f12da759ef41207b83d48ed9`. Completed reports identify the tracer version independently.

Every agent gets a fresh pinned clone, private caches, and only the eval runner's prompt:

> Use ddtest in /trial/bin and onboard this repository to test-optimization for &lt;framework&gt; only.

No rubric, earlier results or coaching is supplied. The [evaluation skill](../../skills/ddtest-onboarding-eval/SKILL.md) governs container isolation and model-only authentication. Trials use the dedicated Colima VM with four CPUs / 6 GiB per container, a read-only root, dropped capabilities, one owned trial mount, and no host profiles, SSH/cloud/GitHub/Datadog credentials or MCP servers. Egress is unrestricted; no Datadog Agent, service credentials or other services are configured in the VM. An empty Docker client configuration avoids unrelated registry credential helpers. Evaluations sharing an OAuth login are serialized.

## Final real-agent results

| Trial | Final dependency scope | Native outcomes in both modes | Telemetry | Feature controls | Reporting |
| --- | --- | --- | --- | --- | --- |
| javapoet-claude-final | unchanged | 388 pass / 0 fail / 1 skip | java_identities_match | 6/6 | pass |
| javapoet-codex-final | unchanged | 388 pass / 0 fail / 1 skip | java_identities_match | 6/6 | pass |
| java-otp-claude-final | unchanged | 100 pass / 0 fail / 0 skip | java_identities_match | 6/6 | pass |
| java-otp-codex-final | unchanged | 100 pass / 0 fail / 0 skip | java_identities_match | 6/6 | fail |
| failsafe-claude-retry-final | TestNG 7.5; core only | 305 pass / 1 fail / 0 skip | java_identities_match | 6/6 | pass |
| failsafe-codex-final | TestNG 6.9.10; core only | 305 pass / 1 fail / 0 skip | java_test_name_missing | not run | pass |

JavaPoet and java-otp complete the original pinned full commands under both agents, without POM/test-source changes: JavaPoet has 388 passes / 1 skip per mode; java-otp has 100 passes. Each trial passes six supported controls. Both agents now instrument java-otp's detected test **and deploy** workflows while retaining the original commands and JDK matrix. Static action presence does not validate that matrix or execute deployment.

Three of these four handoffs pass independent factual review. Both Claude handoffs quote the canonical text; their literal blockquote formatting triggers automated review but adds no contradiction. JavaPoet/Codex uses an accurate paraphrase and keeps CI verification external. java-otp/Codex fails reporting: it says “CI tracer agreement remains inconclusive until GitHub Actions runs,” although actual CI cannot update or clear this local verdict. Its correct local test results and INCOMPLETE opening do not erase that contradiction. Human reporting review remains necessary.

Failsafe/Codex preserves TestNG 6.9.10 and completes the original full command: 305 passes / 1 failure in both modes, but 305 unnamed telemetry events leave identity reconciliation inconclusive. Feature probes are correctly not run; okhttp/retrofit are not reached. Its handoff passes independent review and does not blame matching existing failures for the telemetry blocker.

Failsafe/Claude's authorized retry also completes the original TestNG 6.9.10 full command with matching 305 passes / 1 failure and missing names. Removing the custom listener changes no such outcome. It then upgrades the root dependency: 7.4.0 resolves names and advertises two controls; 7.5 passes all six supported controls. Its final setup keeps TestNG 7.5 and pins tracer 1.67.1, so that final PASSED local result is **a dependency-changed, core-only result**, not validation of the original SDK or full reactor. Both unreached modules and Java 8/11 remain explicitly untested. The handoff passes factual review with that scope; “the upgrade broke nothing” is accepted only for the matching selected-command outcomes immediately described. [TestNG's requirements](https://testng.org/#_requirements) document Java 8 for the 7.5 line and Java 11 from 7.6; this does not replace execution of the CI matrix or recommend an exact patch release.

The initial final-source Failsafe/Claude attempt used the earlier evaluator environment. It first encountered the original missing-name condition, then changed TestNG to 6.14.3. Its final full command matches 305 passes / 1 failure and passes the two controls advertised by that selected integration; two reactor modules remain unreached. An earlier instrumented-only timing failure remains in the historical report pair. It properly discloses the upgrade and unresolved failure history, but reporting fails for generalizing four missing capabilities to the TestNG integration and calling them “not a version problem”: the report scopes capability discovery to the selected integration, and modern TestNG fixture results pass all six controls. This dependency-specific result does not establish framework-wide unsupported features.

The initial Failsafe/Codex attempt failed during dependency compilation with “Too many open files in system,” before onboarding started. The fresh resource-fixed Claude attempt then failed before onboarding on Maven Central HTTP 502. Both setup failures are retained separately, with no compatibility or agent-success claim. The human explicitly authorized one unchanged-input Claude retry after the HTTP 502; no further retry was made.

The principal comparison has one uncoached trial per project/agent; initial Failsafe attempts with the earlier evaluator environment are disclosed separately. Model generation, service defaults, scheduling and timing-sensitive tests vary; this is a reproducible case comparison, not a statistical success-rate estimate. Counts include skipped executions and do not prove identity agreement or repository-wide coverage.

## What the runs changed

The Java agent entry now distinguishes manual CI setup from supported local validation. Reports separate native parity from telemetry identity reconciliation, show bounded differences and stable codes, and mark gated probes as not run. Selected-command local status does not reuse CI configuration counts. Module execution and static workflow action presence remain independently visible. One previous suspected regression is retained with a checksummed JSON/HTML pair; a later matching run does not explain it.

Preliminary trials on source `04fccc2388808a01fcb5c150b1b6fc1ee76aa37c` are preserved separately. JavaPoet/Claude completed a compatible full suite and six controls but prefaced the correctly qualified result with “Onboarding is done”; reporting failed. java-otp/Claude completed 100 matching results and six controls and instrumented both test and deploy workflows. Failsafe/Claude was interrupted after exposing an intake gap: missing names had been replaced with generated display labels, so the verifier could only report a generic identity mismatch. Its completed paired command had 305 passes / 1 failure in both modes, with later modules unreached; manual SDK subsets are not full-suite evidence. No final outcome is claimed for that interrupted agent session. The preliminary Codex controller was stopped while queued; no Codex model trial started with those inputs.

The correction preserves missing-name provenance through intake and keeps unrelated anonymous spans separate, preventing manufactured retries/flakes. A real MessagePack intake regression exercises that transformation before Maven reconciliation. The handoff also explicitly conditions completion language on `summary.status`, addressing the observed false completion preface. Preliminary evidence and earlier fixture results remain retained without being upgraded to final-source results.

## Independent product checks

All eight framework comparisons pass with the final source and real dd-java-agent 1.67.1: JUnit 4, JUnit 5, TestNG, Spock, Cucumber JVM, Karate, ScalaTest and Scala MUnit. **46 supported controls passed; MUnit skipping/disable remained explicitly unsupported.** These are evaluator-owned fixtures, not eight additional OSS agent trials.

A deterministic public-CLI fixture first failed only under instrumentation. The report correctly recorded `native_outcome_mismatch` and local FAILED. After restoring the original test, native/telemetry comparison and all six controls passed; the prior failed comparison remained linked in its historical JSON/HTML archive. The HTML navigation pointed to the historical JSON and its checksum matched. A subsequent check-only run retained both earlier executions without claiming current validation. The fixture's original checkout had no generated probe left behind.

An evaluator-owned diagnostic subset of Failsafe's original TestNG 6.9.10 source used `mvn -q -pl core test -Dtest=AsyncExecutionTest`. Its seven native results matched in both modes; the actual SDK payload then produced `java_test_name_missing`, identifying seven missing names and leaving feature probes not run. This exercises intake's generated display-label fallback and is **not a full-reactor or model-trial pass**. The separate real MessagePack regression also verifies that two unnamed spans with different outcomes do not manufacture a flaky test.

The exact final-source Go/Python protocol and reporting review suites passed all 22 tests. The earlier complete offline evaluator invocation passed 84 tests with nine Docker-only cases skipped (93 total); no real model session is counted as a fake-CLI unit test.

`make lint` passes with repository-pinned golangci-lint 2.14.0. `make test` has the inherited macOS `TestMochaNestedGuardDisablesTracingOnlyForNestedCLIs` failure, reproduced previously on unchanged main; the current Linux control passes. Other executed Go tests pass. One earlier concurrent run hit the existing five-second TypeScript compiler-test deadline; it did not recur in sequential verification. The evaluator's 93-test offline suite passes with nine Docker-boundary tests skipped in that invocation. These checks do not substitute for real model trials.

## Evidence and reproduction

The [machine-readable record](java-maven-improvements-2026-10-26.json) records project refs, source/binary/image identities, suite outcomes, diagnostic codes, workflow/dependency changes and independent criteria. Full evidence is retained outside the checkout under `/tmp/ddtest-java-improvements-final-2026-10-26/`; preliminary evidence is under `/tmp/ddtest-java-improvements-2026-10-26/`. Temporary local storage is not durable hosting.

Follow the [container reproduction procedure](java-maven-oss-2026-10-26.md#reproduce-with-the-current-runner) with the same pinned projects, matching Java image and a new output directory per trial. JavaPoet setup is `mvn -q -DskipTests test-compile`; java-otp uses `./mvnw -q -DskipTests test-compile`; Failsafe uses `mvn -q -DskipTests test-compile` after the resource setup below. Setup compilation is not suite-execution evidence. Preserve current and historical report pairs and relevant configuration, grade independently, then use `run_eval.py --cleanup <evidence-directory>` for each ownership-verified trial.

For the resource-fixed Failsafe pair, add this identical trusted setup command before dependency compilation. It writes outside the tested checkout and changes no tracked project file. The wrapper raises Maven's soft limit within the existing container hard limit; the private repository lives on container tmpfs. This is an explicit environment change from the initial attempts, not an unchanged-input retry. The two resource changes are not an isolated causal test of the file-limit failure.

```sh
FAILSAFE_SETUP=$(python3 - <<'PYTHON'
import shlex
source = r'''from pathlib import Path
import os
maven=Path('/trial/bin/mvn')
maven.write_text('#!/bin/sh\nulimit -n 8192\nexec /usr/local/bin/mvn "$@"\n')
maven.chmod(0o700)
settings=Path(os.environ['MAVEN_USER_HOME'])/'settings.xml'
settings.parent.mkdir(parents=True,exist_ok=True)
settings.write_text('<settings><localRepository>/tmp/ddtest-maven-repository</localRepository></settings>\n')
'''
print('python3 -c ' + shlex.quote(source))
PYTHON
)
```

Pass `--setup-command "$FAILSAFE_SETUP"` before `--setup-command 'mvn -q -DskipTests test-compile'` to each Failsafe invocation.

All 12 owned final-source trial/diagnostic roots were independently checked absent after normal ownership-verified cleanup. Report pairs, historical archives, relevant configurations, patch/status, public activity and model handoffs remain outside the trials. The dedicated VM had no remaining containers and was deleted with its owned container data; the unrelated default profile was not operated on. Preliminary attempts remain retained separately and their owned trial roots were also removed.

DDTest's per-session cleanup passes where completed. Agent scratch cleanup is **not exercised as a complete inventory**: retained Git status and configuration review do not account for every ignored build artifact at CLI exit. Evaluator teardown is therefore assessed separately from agent cleanup. No real CI run, publication or Datadog backend request was performed.

Actual CI execution, other JDK/profile combinations and Datadog backend processing remain unverified. No SDK upgrade, narrowed subset or matching aggregate total is counted as an unchanged full-project pass.
