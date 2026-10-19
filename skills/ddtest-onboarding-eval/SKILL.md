---
name: ddtest-onboarding-eval
description: Evaluate ddtest onboarding on fresh open-source JavaScript or Ruby repositories using Codex or Claude CLI and a minimal customer prompt. Use for developer experiments and comparisons, not for onboarding a customer repository directly.
---

# ddtest onboarding evaluation

Measure what an uncoached coding agent accomplishes with a ddtest binary. Cover Jest, Vitest, Playwright, Mocha, Cypress, Cucumber.js, WebdriverIO, RSpec and Minitest; grade unsupported versions or configurations as unvalidated. This skill belongs to the
**evaluator**, not the agent being tested. It is developer tooling in this repo;
it is not embedded in the ddtest binary or installed in the target repository.

Use this skill by asking the evaluator to read this file, for example:

> Read skills/ddtest-onboarding-eval/SKILL.md and evaluate Luxon with Codex using
> /absolute/path/to/ddtest. Compare Claude at the same repository commit.

Keep the skill local to the ddtest/evaluator workspace. Do not install it in the
tested agent's global skill directories or target checkout.

## Prepare and run

1. Select a public repository that actually runs a supported framework. Read its CI, package
   scripts, lockfile, runtime requirements, and ordinary repository instructions.
   Choose a commit and dependency installation command. Record the intended test
   command and relevant Node, package-manager, and framework versions outside the
   target checkout. Use the same commit and environment when comparing agents.
2. Build or select the ddtest binary under evaluation. Record its source commit
   and whether it includes uncommitted changes; the runner records its SHA-256.
   Check its help rather than assuming newer prototype commands are available.
3. Use Python 3.10+, Git, the project's runtime/package manager, and an
   authenticated Codex or Claude CLI on macOS/Linux. Use a CLI profile without
   live Datadog credentials or a Datadog MCP connection. These are local-only
   experiments: no remote CI runs, publishing, or real Datadog intake.
4. Run [scripts/run_eval.py](scripts/run_eval.py) from the evaluator workspace:

   ```bash
   python3 skills/ddtest-onboarding-eval/scripts/run_eval.py \
     --repo https://github.com/moment/luxon.git \
     --ref <chosen-commit-sha> \
     --ddtest /absolute/path/to/ddtest \
     --agent codex \
     --setup-command 'npm ci --no-audit --no-fund' \
     --output /absolute/path/outside-the-target/luxon-codex-01
   ```

   Repeat with `--agent claude` and a **new output directory**. The runner always
   creates a new temporary clone, copies only the executable to a sibling `bin`
   directory, installs dependencies, and starts a fresh CLI session. It does not
   reset or clean an existing checkout. Repeat `--setup-command` when necessary;
   each value is split into arguments, without implicit shell evaluation.

   When a Codex trial needs a particular installed Node runtime, pass
   `--node /absolute/path/to/node` (currently Codex only). The runner verifies that
   executable and records its actual path/version in `run.json.node_runtime`,
   uses it for setup, and keeps the same PATH in Codex
   tools with process-local `allow_login_shell=false`, disabled shell snapshots,
   `shell_environment_policy.set.PATH` and an empty private `ZDOTDIR`. These avoid
   inherited shell snapshots or user profiles silently selecting a different
   Node version. `run.json.shell_isolation` records the effective settings. No
   global shell/profile/CLI files are edited; without `--node`, normal runtime
   and shell selection apply. Compare `run.json.ddtest_runtime` with the chosen
   runtime: a mismatch in ddtest's actual local command runtime is an evaluator
   environment failure, not evidence of a tracer regression. Framework-owned
   Node executables are recorded separately and do not silently replace this check.

   For Ruby, pass `--ruby /absolute/path/to/ruby` with either agent. This pins the
   setup PATH, gives Codex the same process-local shell settings, and uses private
   Bundler configuration, gem installation and cache directories. Record the
   resolved lockfile and framework/tracer releases. Compare `ruby_runtime` with
   `ddtest_ruby_runtime`; missing runtime evidence remains unobserved. Claude keeps
   its normal shell behavior, so inspect the actual ddtest runtime before grading.
   Use `--disable-mcp-server <name>` for each configured Codex Datadog server or
   `--strict-mcp` for an empty Claude MCP configuration. These flags affect only
   this trial and preserve normal permission checks and security hooks.

   The tested agent receives exactly:

   > Use ddtest in /temporary/trial/bin and onboard this repository to test-optimization.

   For a requested framework-only trial in a mixed repository, pass
   `--framework playwright` (or `jest`/`vitest`/`mocha`/`cypress`/`cucumber`/`webdriverio`/`rspec`/`minitest`). This adds only
   “for playwright only” to the prompt and records the scope in `run.json`.

   No skill, grading rubric, feature list, runbook, previous results, extra system
   prompt, or follow-up coaching is supplied. Let ddtest's own output guide it.
   Observe the live public messages and commands. If it asks for missing input or
   is blocked, record that outcome; a coached retry is a separate experiment.
   Respect requested one-shot or no-retry limits. When follow-ups are forbidden,
   record the outcome without answering or nudging the tested agent.

The runner uses Codex's `--approve-for-me` or Claude's `--permission-mode auto`;
normal CLI configuration and repository instructions still apply. For Claude print
mode, it sets process-local `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` and Bash
timeouts up to 600 seconds, or 3600 seconds for Cypress multi-configuration runs
(bounded by the trial timeout), recorded in
`run.json.agent_environment`. These settings keep long validation commands
synchronous; they do not change repository test timeouts or the simple prompt.
Unfinished Bash/background tasks at CLI exit make the trial an infrastructure
interruption even when the CLI exits zero. Collect and grade it as incomplete
before cleanup or a permitted fresh retry. Do not infer completion from a
promise to wait for a later notification. Check these
flags in the installed CLI's help. It does not bypass permissions or isolate the
agent's account, filesystem, network or global skills. MCP isolation requires the
explicit process-local flags above. Record
relevant inherited settings and any exposure to evaluator instructions as a
limitation. It removes `DD_*` and `DATADOG_*` environment variables, which alone
does not guarantee isolation from credentials stored elsewhere.

## Interrupted trials and diagnostic follow-ups

The runner creates a short owned trial root (`/private/tmp/dde-*` on macOS), with
state outside the customer checkout to leave room for Unix socket paths. Each trial
has private npm, Yarn and browser caches and TMPDIR; pnpm uses a private
`XDG_CONFIG_HOME/pnpm/rc`, avoiding pnpm-only npm environment variables.
`run.json.private_caches` records the paths; reuse them for setup and diagnostics.
Supplied cache contents are reusable environment-owned data. Do not ask the tested
agent to scan, hash, prune individual cache entries or delete entries by timestamps.
The evaluator later removes the whole verified owned trial root, not shared caches.
The runner records
`failure_kind`/`retry_recommended` for recognized CLI service or transport failures.
The CLI version probe is capped at 15 seconds, before cloning or installing dependencies.
A startup timeout is recorded as `failure_stage: cli_startup` and an infrastructure
interruption. The runner records `agent_reporting` by comparing factual verdicts,
executed/selected scope, configuration counts, execution/source-identity counts and
absolute report links with the latest JSON. Contradictions fail; omitted or
ambiguous facts remain `review`, not an automatic pass. Natural wording equivalent
to `summary.final_response` is acceptable; byte-for-byte copying is not a grading
requirement. Independent CI, test evidence and artifact review are still required.

For an infrastructure interruption, retain and grade the original attempt, collect
its evidence and clean its clone. Unless the user requested a single attempt or no
retry, allow **one** retry from a fresh clone with
the same commit, binary, setup and simple prompt. Do not retry authentication or
permission denials automatically, coach the retry, or keep rerunning product
failures until they pass. Report both attempts separately.

If baseline/instrumented worker crashes vary, preserve the inconclusive verdict.
Run evaluator diagnostics separately without competing trials. Any change such as
Jest worker limits must apply identically to both modes and be recorded; it is not
an unassisted success. Equal existing test failures remain acceptable. Keep all
cache/manifests under that trial's private temporary directory; record agent
escapes from these paths as an isolation limitation before scoped cleanup.

## Evaluate independently

Read `run.json`, `activity.jsonl`, `changes.patch`, `status.txt`, and the retained
checkout. The activity file contains public agent messages and command results
with bounded output excerpts, not full CLI streams or model reasoning. Command
output can be truncated: inspect the original compact ddtest report in the clone
when available. Record commands launched *inside* ddtest from its own evidence;
the outer CLI command list alone does not prove which test runs occurred.

Grade each criterion **pass**, **fail**, **blocked**, or **not exercised**, citing
concrete commands, files, and observations. A zero agent exit code, an agent's
claim of success, or one instrumented test run is insufficient.

| Criterion | Evidence required |
| --- | --- |
| CI installation | Review the actual workflow diff: the tracer is installed and initialized before the selected framework, runtime requirements match every affected matrix entry, and existing jobs/test coverage are preserved. Check the referenced action and tracer release rather than assuming their behavior. Grade this as a static configuration review; actual CI installation/execution remains **not exercised** in a local-only trial. |
| Baseline versus instrumented execution | Both commands ran against the same suite and environment, with only instrumentation differing. Behavior-changing features must be disabled for this comparison. Compare discovered tests, results, failures, exit codes, and hangs/crashes. Matching pre-existing test failures can pass compatibility; unexplained differences fail. If missing setup prevents a meaningful comparison, mark blocked. |
| Advanced features | Assess each separately: automatic test retries, early flake detection, test skipping, quarantine, disabled tests, and attempt-to-fix. Require an appropriate control and an observed change for a known test (attempt counts, execution/skip decisions, and expected failure/exit behavior). Enabling flags or receiving telemetry is insufficient. |
| Agent verdict reporting | Compare the factual conclusion and overall claims with the latest report's `summary.status`, blockers, and `summary.final_response` (or `summary.onboarding_response` on older versions). Check whether counts and explanations match the latest report and whether any later claims contradict its verdict. Grade an upgraded verdict as a failure even if caveats appear later. Use `last_execution.current` and `configuration_check` when present to distinguish the last full execution from the latest configuration check. Historical passes do not certify changed inputs; a check-only final invocation still requires a full rerun before completion. |

For every framework, assess `summary.executed_validation` and
`summary.configuration_coverage` separately from repository-wide `summary.status`.
Passing executed configurations do not validate unavailable browsers, operating
systems or other untested configurations. For an explicit `--command`, grade the
current `summary.selected_validation`: a selected pass requires that command's
paired execution, supported feature controls, current static CI checks, HTML and
cleanup. Its zero exit code does not upgrade an INCOMPLETE repository verdict.
Check-only, stale execution or a failed required control cannot establish either
selected or executed success. Report test executions across configurations as
such; do not present repeated runs of the same tests as distinct test identities.
When present, use `reporting_counts.test_executions` and
`reporting_counts.source_test_identities`; a null identity count means native
identities are missing or ambiguous. A shared source identity does not establish
equivalent configuration or environment, so keep every configuration's validation.

For Playwright, Test Impact Analysis skipping and per-test coverage are not
supported by dd-trace-js. Record them as **not supported**, never passed; require
the other five feature checks. Preserve the original Playwright projects, browser
settings and web-server setup, and check that validation-owned servers stop.

For Cypress, require a native baseline and instrumented comparison plus five
feature controls: retries, early flake detection, quarantine, disabled tests and
attempt-to-fix. TIA skipping and application coverage validation are deferred;
record them as deferred, never passed or unsupported by the tracer. Do not require
agents to add Istanbul dependencies or change application builds for this trial.
Preserve existing coverage setup. Retry/EFD/attempt-to-fix checks must reconcile
independent probe-body counts with telemetry; the native report can show one final
result. Preserve reporters, setupNodeEvents/support hooks and startup commands.
Verify that Cypress GitHub Action consumers execute through the Node API with
preserved inputs and the action's Node major. Review this path separately from CLI
commands; testdrive may execute both. Retain unavailable browser/OS configurations
as untested. Keep
Cloud recording disabled locally, use a private CYPRESS_CACHE_FOLDER, and inspect
ignored generated fixtures/screenshots as part of cleanup.

For WebdriverIO, require the actual WDIO command, native worker results and the
five supported feature controls. TIA skipping and code coverage are not supported;
record them as such, not passed. Preserve services, capabilities, reporters, loaders
and worker initialization. Inspect every selected spec/capability for a completed
native report and reconcile controlled attempts with independent body counters.
Keep browser-runner, standalone automation and unavailable browser/remote variants
explicitly unvalidated. Preserve unrelated services occupying local ports; record
any consistent local port override. Collect generated logs, owned servers and browser
caches during cleanup. CI edits or an empty Node preload check are not completed
onboarding. Start with a focused configuration before the full matrix.
For an explicitly focused WDIO evaluation, apply the common selected-validation
contract above and retain all other configurations as unvalidated. Check that
workflow/package/config edits preceded the full run;
real CI execution does not clear a stale local static check.

For Cucumber, preserve profiles, tags, support imports and build prerequisites.
Check scenario results against telemetry, and reconcile probe-body counters for
all six feature controls. A mixed npm test command must initialize only its
Cucumber leaf when that is the requested scope. Istanbul suite coverage must name
real application files; config, support and feature files alone are insufficient.
Suite coverage is not independent per-scenario coverage. Check that CI enables
the same NYC/ESM instrumentation and that its tracer supports every selected
Node runtime. New ignored build outputs still require cleanup.

For Ruby, preserve Gemfiles, lockfiles, Rake/Rails tasks, test arguments, reporters
and existing Ruby options. Require native RSpec/Minitest results for the complete
selected suite in both baseline and reporting-only runs, actual application-file
coverage, and all six controlled features with independent probe-body counters.
Do not replace a custom/default Rake task with a guessed command to manufacture a
repository-wide pass. Review what it invokes; explicitly selected local commands
leave unresolved CI configurations unvalidated. Preserve legacy Ruby/JRuby entries
and grade incompatible or floating CI versions separately from the local runtime.
Collect owned bundle installations, generated lockfiles and ignored coverage
outputs. Tracer installation in ddtest's disposable copy must not change the
customer bundle. Record ddtest cleanup separately from agent and evaluator cleanup.

If the tested ddtest version lacks paired runs or controlled feature scenarios,
mark the missing checks **not exercised** and identify the product gap. Do not
silently add those capabilities to the agent's prompt, target repository, or
ddtest build to turn the trial into a pass. Evaluator follow-up diagnostics may
explain a result, but must be labeled separately from the unassisted trial.

Inspect temporary fixtures, tracer installations, lockfiles, workflow edits and
dedicated scratch directories the agent created. Preserve supplied/pre-existing
package and browser caches; do not enumerate, hash or prune their contents to grade
agent cleanup. ddtest owns its nested session/run TMPDIR. Agents own additional
unique scratch directories and processes they create, and should keep exact paths
and process handles in their notes.

A clean Git diff or ddtest's session-cleanup result does not prove those additional
artifacts are gone. Record ddtest cleanup, agent cleanup at CLI exit and later
evaluator teardown separately. Native applications such as Cypress can write profile
logs outside supplied cache variables; record exact before/after paths or an ownership
manifest when evaluating those escapes. Timestamps, glob matches and filenames alone
do not establish ownership. Do not delete shared profile logs by creation time;
preserve uncertain files and report the uncertainty. An owned escaped artifact left
by the agent remains a cleanup omission even if the evaluator later removes it.
Report cleanup separately from technical validation. Report unsupported runtime combinations, removed matrix
coverage, cleanup failures, or a need for human intervention even if tests pass.

## Report and clean up

Write one concise `assessment.md` alongside `run.json`, outside the target:

```markdown
# Onboarding evaluation
Repository / commit:
ddtest source commit / dirty state / binary hash:
Agent / CLI version / model / relevant configuration:
Runtime / package manager / framework versions:
Prompt and command evidence: run.json, activity.jsonl

| Criterion | Verdict | Evidence or missing check |
| --- | --- | --- |
| CI configuration (static review) | | |
| CI execution | not exercised | Local-only experiment |
| Baseline versus instrumented execution | | |
| Advanced features (one row per feature) | | |
| Factual agent reporting | | |
| ddtest session cleanup | | |
| Agent artifact cleanup at CLI exit | | Include ignored files and scratch paths outside the clone |
| Evaluator teardown | | Retain evidence, then remove only owned trial artifacts |
| Presentation | pass / warning | Verdict placement and report-path omissions |

Overall: complete locally / incomplete / blocked
Problems and required ddtest improvements:
Cleanup and experiment limitations:
```

Declare the experiment complete locally only when CI configuration review, the compatibility
comparison, all scoped feature checks, accurate factual reporting, and agent artifact
cleanup pass. Distinguish a technical validation pass with an agent cleanup omission
from a fully successful experiment; evaluator teardown does not erase that omission.
A preamble before a correct verdict or a missing report path is a presentation warning, not a technical failure. Stale counts, dismissed blockers and upgraded verdicts remain factual reporting failures. Always retain the CI execution
limitation. Distinguish product defects, agent omissions, and environment blocks.
State whether another person could repeat the run using the recorded commands.

Keep concise commands, verdicts, test counts, and relevant configuration changes.
The retained HTML findings report and compact JSON validation report are both
expected artifacts. Check that the HTML opens and describes the instrumented
suite, without synthetic probes or broken raw-artifact links; use the JSON for
compatibility and feature verdicts. Do not archive every telemetry payload, test
event or node_modules tree. Preserve the two reports when collecting evidence. Before deleting the temporary trial directory listed in `run.json`, copy
any small, relevant new workflow/configuration files that `changes.patch` cannot
contain (untracked files are listed in `status.txt`). Preserve enough evidence for
each verdict in the assessment, then remove that disposable directory, including
local tracer installations and generated artifacts. Leave the original source
repo and other trials untouched. Never reuse a modified clone for the next trial.
