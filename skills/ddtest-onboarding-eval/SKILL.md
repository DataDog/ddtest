---
name: ddtest-onboarding-eval
description: Evaluate ddtest onboarding on fresh open-source Jest or Vitest repositories using Codex or Claude CLI and a minimal customer prompt. Use for developer experiments and comparisons, not for onboarding a customer repository directly.
---

# ddtest onboarding evaluation

Measure what an uncoached coding agent accomplishes with a ddtest binary. Cover Jest and Vitest; grade unsupported versions or configurations as unvalidated. This skill belongs to the
**evaluator**, not the agent being tested. It is developer tooling in this repo;
it is not embedded in the ddtest binary or installed in the target repository.

Use this skill by asking the evaluator to read this file, for example:

> Read skills/ddtest-onboarding-eval/SKILL.md and evaluate Luxon with Codex using
> /absolute/path/to/ddtest. Compare Claude at the same repository commit.

Keep the skill local to the ddtest/evaluator workspace. Do not install it in the
tested agent's global skill directories or target checkout.

## Prepare and run

1. Select a public repository that actually runs Jest or Vitest. Read its CI, package
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

   The tested agent receives exactly:

   > Use ddtest in /temporary/trial/bin and onboard this repository to test-optimization.

   No skill, grading rubric, feature list, runbook, previous results, extra system
   prompt, or follow-up coaching is supplied. Let ddtest's own output guide it.
   Observe the live public messages and commands. If it asks for missing input or
   is blocked, record that outcome; a coached retry is a separate experiment.

The runner uses Codex's `--approve-for-me` or Claude's `--permission-mode auto`;
normal CLI configuration and repository instructions still apply. For Claude print
mode, it sets process-local `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` and Bash
timeouts up to 600 seconds (bounded by the trial timeout), recorded in
`run.json.agent_environment`. These settings keep long validation commands
synchronous; they do not change repository test timeouts or the simple prompt.
Unfinished Bash/background tasks at CLI exit make the trial an infrastructure
interruption even when the CLI exits zero. Collect and grade it as incomplete
before cleanup or a permitted fresh retry. Do not infer completion from a
promise to wait for a later notification. Check these
flags in the installed CLI's help. It does not bypass permissions or isolate the
agent's account, filesystem, network, global skills, or MCP configuration. Record
relevant inherited settings and any exposure to evaluator instructions as a
limitation. It removes `DD_*` and `DATADOG_*` environment variables, which alone
does not guarantee isolation from credentials stored elsewhere.

## Interrupted trials and diagnostic follow-ups

The runner creates private package caches and TMPDIR for each trial and records
`failure_kind`/`retry_recommended` for recognized CLI service or transport failures.
The CLI version probe is capped at 15 seconds, before cloning or installing dependencies.
A startup timeout is recorded as `failure_stage: cli_startup` and an infrastructure
interruption. The runner flags explicit completion claims against INCOMPLETE
reports in `agent_reporting`; independent factual review is still required.

For an infrastructure interruption, retain and grade the original attempt, collect
its evidence and clean its clone, then allow **one** retry from a fresh clone with
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

If the tested ddtest version lacks paired runs or controlled feature scenarios,
mark the missing checks **not exercised** and identify the product gap. Do not
silently add those capabilities to the agent's prompt, target repository, or
ddtest build to turn the trial into a pass. Evaluator follow-up diagnostics may
explain a result, but must be labeled separately from the unassisted trial.

Inspect temporary fixtures, tracer installations, lockfiles, workflow edits, and
leftover artifacts, including agent-created inventories or caches outside the clone. Establish their provenance before scoped removal; never delete arbitrary shared temporary files. Report cleanup separately from technical validation. Report unsupported runtime combinations, removed matrix
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
| Artifact cleanup | | |
| Presentation | pass / warning | Verdict placement and report-path omissions |

Overall: complete locally / incomplete / blocked
Problems and required ddtest improvements:
Cleanup and experiment limitations:
```

Declare the experiment complete locally only when CI configuration review, the compatibility
comparison, all scoped feature checks, accurate factual reporting, and artifact cleanup pass. A preamble before a correct verdict or a missing report path is a presentation warning, not a technical failure. Stale counts, dismissed blockers and upgraded verdicts remain factual reporting failures. Always retain the CI execution
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
