# Onboarding and testdrive code map

`ddtest onboard` and `ddtest testdrive` share CI discovery and validation.
Start at the boundary that owns the behavior you need to change:

| Responsibility | Entry points |
| --- | --- |
| CLI flags, consent and output mode | `internal/cmd/onboard.go`, `internal/cmd/testdrive.go` |
| Shared CI flags and error rendering | `internal/cmd/ci_provider.go` (`ciOptions`) |
| Onboarding detection and setup handoffs | `internal/onboard/agent.go` |
| Shared agent protocol and rendering | `internal/onboard/agent_result.go` |
| Persistent executable lookup and follow-up commands | `internal/onboard/commands.go` |
| Execution plans and setup-facts schema | `internal/onboard/execution_plan.go` |
| CI provider selection and follow-up arguments | `internal/onboard/ci_selection.go` |
| Provider contract and registrations | `internal/onboard/ci_provider.go`, `ci_providers.go` |
| Workflow model, loading and shared test/setup classification | `internal/onboard/ci_workflows.go` |
| Provider configuration syntax and edits | `internal/onboard/ci_provider_<provider>*.go` |
| Shared setup model, support checks and setup wording | `internal/onboard/ci_setup.go` |
| User statements about CI runtimes and server configuration | `internal/onboard/ci_statements.go` |
| Language-specific discovery and setup | `internal/onboard/language.go`, `languages.go`, `<language>*.go` |
| Resolved test commands and validation scope | `internal/onboard/discovery.go`, `scope.go`, `scripts.go` |
| Literal command recognition without executing repository code | `internal/testcommand` |
| Setup plans and bounded source edits | `internal/onboard/execution_plan.go`, `apply*.go` |
| Validation preparation and execution | `internal/testdrive/testdrive.go` |
| JavaScript command selection, fallbacks and invocation parsing | `internal/testdrive/javascript_command.go` |
| Shared paired-execution engine and framework adapters | `internal/testdrive/paired_validation.go` |
| Shared preflight evidence and CI configuration checks | `internal/testdrive/preflight.go` |
| Native result model, outcome comparison and telemetry matching | `internal/testdrive/native_results.go` |
| Coordinated setup and validation | `internal/testdrive/workflow.go` |
| Attempt state, failure policy and retry handoffs | `internal/testdrive/workflow_attempt.go` |
| Shared CI questions and pinned tracer selection | `internal/testdrive/workflow_ci.go` |
| Checkpoint-bound facts and setup failure recovery | `internal/testdrive/workflow_setup.go` |
| Verified report completion and blocker handoffs | `internal/testdrive/workflow_completion.go` |
| Restart and resume commands | `internal/testdrive/workflow_commands.go` |
| Workflow tracer selection and metadata preflight | `internal/testdrive/workflow_tracer.go` |
| Saved workflow reports and blocker classification | `internal/testdrive/workflow_report.go` |
| Input fingerprints and accepted setup facts | `internal/testdrive/workflow_inputs.go` |
| Evidence, cleanup and checkpoints | `internal/testdrive/workflow_checks.go`, `workflow_checkpoint.go`, `workflow_receipt.go` |

Unqualified filenames in a row are in the same directory as that row's first file.

Providers parse their own syntax into `ciWorkflow`, `runtimeJob` and
`runtimeStep`. Shared discovery, setup and runtime checks consume those models;
language decisions stay behind `discoveryLanguage`. Providers normally use
`findTestWorkflows`' shared classification. GitHub manual instructions preserve
their conservative literal-command inspection and use the same GitHub file
loader as full discovery. Instructions decode only the fields they inspect;
full discovery decodes the complete workflow model and expands composite actions.

`internal/environment` detects the running CI environment and supplies runtime
tags and runner configuration. Repository CI inspection belongs here, in
`internal/onboard`, and is shared with testdrive.

`workflowAttempt` owns a single invocation's result, checkpoint and recovery
state. The coordinator sequences discovery, setup, execution and verification;
the attempt handles failure and completion handoffs. Standalone validation
shares the attempt's CI decisions and failure policy while keeping its own
execution and verdict rules.

After changing these paths, run `make test` and `make lint`. The end-to-end
Codex catalog is described in
[`skills/ddtest-onboarding-eval/SKILL.md`](../../skills/ddtest-onboarding-eval/SKILL.md).
