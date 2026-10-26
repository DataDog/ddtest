# Ruby onboarding improvements — 2026-10-26

This follows the [main rebase evaluation](ruby-main-rebase-2026-10-26.md). It implements the six improvements identified there: conservative CI discovery without rewriting commands/matrix types, shared Ruby coordination and canonical responses, bundle readiness before suites, exact telemetry diagnostics, careful target-runtime compatibility, and smaller lazy HTML details. Actual GitHub CI and Datadog backend processing remain not exercised.

## Implementation and automated validation

RSpec/Minitest now use the existing coordinator after manual Ruby CI setup. Every selected configuration still needs its own native baseline/reporting-only comparison, real application coverage and six independent feature controls. Git/gem report exclusions, source preservation and cleanup must also pass. Only a current source/Ruby/environment/lockfile-bound receipt can report `COMPLETE LOCALLY`; agents finish through `testdrive --report` and the saved `final_response`.

Literal single-target Rake defaults retain bare `rake`. GitHub numeric/string comparison and scalar function coercions follow the documented semantics; dynamic aliases, unknown contexts and aggregate identity comparisons remain unresolved. A locally chosen dependency that excludes another Ruby target makes that target inconclusive, rather than proving every possible target dependency resolution incompatible. Direct tracer Ruby requirements still reject incompatible targets. Missing telemetry stays a strict gate and reports native/received execution counts and identities; child-process flushing is a conditional investigation hint, not a proven root cause.

Large reports store repeated source/coverage evidence once and hydrate it on expansion, preserving search, pagination and escaped source text. The JSON evidence and counts are unchanged.

- Native RSpec/Minitest integration: passed, including full coordination, final receipt rendering and ignored-lockfile stale-report rejection, alongside the existing comparison/feature/failure/cleanup scenarios.
- Ruby/discovery/protocol/report regression checks: passed. The unchanged i18n workflow regression retains 31 matrix rows, six local bundle configurations and its original bare Rake command.
- A real-binary, network-disabled container check returned both missing bundle setup commands before any native suite or dependency install, with original lockfiles unchanged.
- Isolated Playwright UI: passed on 1,000 synthetic rows; deferred sources, source/coverage searches, 50+10 coverage pagination, filtered expansion, escaped script text, zero dialogs or browser errors. No user browser profile was used.
- `make lint`: 0 issues. Evaluator tests: 93 total, 84 passed, nine optional offline-image Docker cases skipped.
- `make test`: only the existing `TestMochaNestedGuardDisablesTracingOnlyForNestedCLIs` failure. Unchanged main reproduction is documented in the previous evaluation; no Mocha code was changed here.

## Controlled experiment

Final code source: `4f955ab10f89bb206c8fba40c6bbdc393b167201` (clean at build), Linux arm64 binary SHA-256 `5286db36dec6b05f845bc1129dee30fc6356be16e5856be2c08e37a6d3b5ef21`. Both agents used immutable image `sha256:004d012b985de5b49b052c7fbcc843ccdaae0c9101bc3d0c73cdf0c5dc38b52f`, Ruby 3.3.11, Bundler 2.5.22, Node 24.19.0, Codex 0.163.0 and Claude 2.1.220, with fresh CLI default models and explicitly authorized existing model logins. No host profiles, hooks, MCP servers, GitHub/cloud credentials or real Datadog intake were supplied.

The only prompt was `Use ddtest in /trial/bin and onboard this repository to test-optimization for FRAMEWORK only.` Each project/agent received a fresh pinned clone and private caches, with no coaching or follow-up prompt. Container root was read-only, capabilities dropped; four CPU/six GiB limits and a two GiB executable tmpfs were used on a dedicated local Colima VM. Pairs share the VM; timings are not a performance comparison. Egress is unrestricted as disclosed by the evaluator skill. Public activity, actual patches, final answers and both reports were reviewed independently.

| Project | Pin | Framework |
| --- | --- | --- |
| RubyMoney/money | `1d60efeaeea009cfc69374421561b23b851bd36e` | RSpec |
| rails/globalid | `3155f04179c59bc79a6c60b01a40ee296905f069` | Minitest |
| ruby-i18n/i18n | `547917dd8d41fab781a81880f22687fc4eac5d85` | Minitest |

## Final fresh-trial results

Native counts below are per reporting/baseline mode and refer to a current selected execution, not unique tests or cumulative retries.

| Project / agent | Product verdict | Configurations | Native executions per mode | Features | Independent review |
| --- | --- | --- | --- | --- | --- |
| Money / Codex | COMPLETE LOCALLY | 1/1 | 502 passes | 6/6 | Passed; exact canonical response |
| Money / Claude | COMPLETE LOCALLY | 1/1 | 502 passes | 6/6 | Passed; exact canonical response plus accurate limits |
| GlobalID / Claude | INCOMPLETE | 0/5; all five paired | 874 passes, one existing failure | 30/30 | Native scope preserved; telemetry/target runtime blocked; extra reporting claims need correction |
| GlobalID / Codex | INCOMPLETE; agent timed out | 0/5; all five paired | Initially 874 passes, one existing failure | 30/30 | Failed scope review: changed a test isolation hook and excluded Ruby 2.7 without target proof; no final handoff |
| i18n / Codex | COMPLETE LOCALLY | 6/6 | 11,047 passes | 36/36 | Passed; original command/matrix types and canonical response retained |
| i18n / Claude | COMPLETE LOCALLY | 6/6 | 11,047 passes | 36/36 | Passed after reviewing the exact canonical text rendered as a blockquote; one earlier shell timeout retained |

Money preserves `bin/rspec` and its original eight Ruby entries; the five numeric versions are statically compatible and head/two JRuby entries are explicit instrumentation opt-outs. Both agents changed only the CI action and report ignore.

i18n preserves the original bare `bundle exec rake`, numeric matrix types and every include/exclude. All 31 combinations remain: 22 compatible instrumented rows and nine head/JRuby instrumentation opt-outs. Root runs 1,607 tests per mode; each of five Rails bundles runs 1,888. Both agents changed only CI instrumentation and Git report exclusions. All six selected checkout lockfiles are byte-identical between agents. Real CI remains unexecuted.

GlobalID's original-input runs receive 859 telemetry executions for 875 native executions (172 in each of four bundles, 171 on Rails main). All five configurations remain incomplete despite 30 passing controls and observed application coverage among received events. The equal existing Rails-main failure is retained in both native modes. Ruby 2.7 remains unverified because the locally chosen native dependency does not certify another target resolution; it is not proven unsupported by the exact tracer.

Claude preserved GlobalID's test/matrix scope and accurately reported the overall incomplete verdict and one native failure. Its extra prose incorrectly generalized every received count to 172 and overstates child-process flushing as a confirmed sole cause; the report only supplies a conditional investigation hint. Its statement that Rails main cannot load must not replace the actual 175-test execution evidence.

Codex investigated multiple workarounds, then added `Datadog.shutdown!` inside `RailtieTest#run_in_isolation` and excluded numeric Ruby 2.7 based on local resolution. These changes are not accepted as validation of the unchanged pinned project. Its last complete modified-input report still receives only 849 events for 875 native executions (169–171 per bundle), with all 30 controls passing. A later explicit-flush run did not complete before the 1,800-second agent deadline. The evaluator labels the timeout infrastructure, but that label does not establish a transport outage or justify a success claim. No evaluator-created fresh retry was made. Public activity and the first full protocol preserve the earlier original-input blocker; the final report pair is explicitly labeled as later inputs.

### What the new runs still teach us

- The completion receipt proves validation of the current inputs. The source-preservation check covers a validation command, not every earlier agent edit. Independent diff review is still required to catch changed test behavior or unjustified instrumentation exclusions.
- A terminal `report_result` does not guarantee an agent stops investigating. Codex repeated full GlobalID runs and timed out; no successful modified-suite result is promoted to unchanged-project evidence.
- Canonical reporting improves the successful journeys: both Money agents and Codex/i18n retain it exactly. Claude/i18n retains every word in a blockquote, which the raw automated matcher marks review; independent normalization confirms it is unchanged. Extra prose can still introduce inaccurate counts or unjustified causal claims.
- Check-only inventories do not certify execution and can retain an unresolved `latest` selector until an exact tracer is supplied. Both agents corrected the Ruby CI package pin to `datadog-ci` 1.42.0 rather than the separate core `datadog` 2.47.0 release.

### Report size and UI

The current i18n HTML is 54,689,047 bytes (Codex) / 54,687,976 bytes (Claude), compared with 78,302,478 bytes in the prior six-configuration report: about **30.2% smaller**. Money drops from 3,204,357 to 2,615,604 bytes, about **18.4% smaller**. These are separate executions with the same project journeys, not a byte-identical performance benchmark. The standalone report retains complete details; it remains large, so this does not claim that all rendering costs are solved.

### Evaluator timeout correction

Claude/i18n's first full invocation was killed at its explicit/evaluator ten-minute shell allowance (exit 143), before publishing a current aggregate report. The same uncoached agent issued another full invocation, completed all six configurations and verified the final receipt. Both commands remain in evidence; the timed-out invocation is not counted successful.

The evaluator now gives Ruby the same longer synchronous shell allowance as Cypress: bounded by the overall `--timeout`, with a one-hour ceiling. Background tasks stay disabled and ddtest's own per-subprocess timeout is unchanged. This evaluator-only fix is signed separately from the tested ddtest binary and passes all 93 evaluator tests (nine optional Docker cases skipped). No new real-model trial was started merely to exercise the larger allowance; the completed Ruby results above use the recorded binary and original evaluator cap.

## Retained first-attempt failure

Clean source `17d8ad9bd9cce4d45383ff53703c0c9c8eda9633`, binary SHA-256 `5305c752e957e1b05ee5d831ed849c63ef795a5d74e1d85baa598e4278d0b5c0`, exposed a new packaging-check defect in both Money agents: the owned repository copy already contained a Git index, while the inventory check tried to create it again. Native 502/502 comparisons and all six features passed, but the product correctly remained `INCOMPLETE`. Codex copied that canonical verdict correctly. Claude kept investigating/repeating the known failure and was deliberately interrupted with SIGTERM before its final response; reporting for that interrupted attempt is not exercised, not relabeled successful. Remaining projects in that first queue were never started.

The fix replaces copied Git metadata, including linked-worktree pointers, with an owned Git repository before copying only the original file index. Regression tests now cover a populated index, Git-derived gem file lists and a worktree-style metadata pointer, and verify the original index remains byte-for-byte unchanged. The user explicitly approved fresh corrected trials. Both failed Money report pairs and assessments were retained separately.

## Evidence and cleanup

Private local evidence is retained at `/tmp/ddtest-ruby-improvements-20261026.1tezeziv`; corrected trials are under `corrected/`. It contains source/binary/image identity, public activity, actual patches/status, final responses, both report files, per-trial `assessment.md`, automated/native/UI logs and the i18n lockfile comparison. The two failed first Money attempts are separate from the six corrected trials. Small selected checkout lockfiles are retained where collected; dependency trees, private caches and raw telemetry streams are not archived. Dependencies were resolved fresh, not assumed hermetically identical across every trial.

All eight owned trial checkouts/caches were removed with `run_eval.py --cleanup` after retaining report pairs and assessments. Evaluator containers were removed, including interrupted/timed-out processes. The dedicated synthetic UI server/directory and model-free readiness fixture were removed. Unrelated trials, shared caches, Docker images and the original main checkout were left in place. Nothing was published to a real Datadog intake or hosted report service.
