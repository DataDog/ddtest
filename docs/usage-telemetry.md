# Onboarding usage telemetry

DDTest can report how often `onboard` and `testdrive` are invoked and how each
invocation ends. This uses a public client token belonging to the Datadog
organization receiving the telemetry. It requires no customer authentication,
API key, or local Datadog Agent.

**Reporting is disabled in builds without a configured client token.** This
repository supplies no token. Release owners can inject the public token using
`-ldflags -X github.com/DataDog/ddtest/internal/usagetelemetry.ClientToken=...`
and select the receiving site with
`-X github.com/DataDog/ddtest/internal/usagetelemetry.IntakeSite=...`.
The default receiving site is `datadoghq.com`. Supported destinations also include
US3, US5, EU, AP1, AP2 and UK1. Unsupported destinations disable reporting.
The receiving site is independent of the customer's `DD_SITE` or `--site`.

## Events and outcomes

Each top-level invocation attempts to send `ddtest.command.started` and
`ddtest.command.finished`, linked by a random `attempt_id` generated for that
invocation. Retries and resumes are new invocations with new IDs. Help and version
requests produce no usage events. Internal validation called by `onboard --apply`
does not create a second `testdrive` attempt.

Both events contain `schema_version`, `service` (`ddtest-cli`), `ddsource`
(`ddtest`), `message`, `event_name`, `attempt_id`, `command`, `mode`, DDTest
`version`, `os`, `arch`, and a `date` timestamp. Finished events additionally
contain `exit_code`, `duration_ms`, `outcome`, `phase`, and an optional `code`.
Phase and code are explicitly allowlisted; unrecognized values become `unknown`.

| Mode | Meaning |
| --- | --- |
| `discovery` | Plain `onboard`, including setup handoffs |
| `apply` | `onboard --apply`, including previews and validation |
| `instructions` | `onboard --instructions` |
| `validation` | A standalone `testdrive` invocation |
| `configuration_check` | `testdrive --check-only`; no full validation |
| `report` | `testdrive --report`; reads saved evidence |

Outcomes are `succeeded`, `handoff`, `blocked`, `unsupported`, `cancelled`, or
`failed`. They are separate from the process exit code: a user declining execution
can exit zero with `cancelled`, and missing consent can exit nonzero with `blocked`.
An onboarding handoff does not establish completed onboarding. `complete_locally`
and `selected_passed` are successful local results; neither establishes successful
customer CI execution or Datadog backend processing.

For an attempts dashboard, count start events grouped by command and mode. For
full validation attempts, select `testdrive` with mode `validation`; exclude
`configuration_check` and `report`. Onboarding's internal validation remains part
of its `apply` invocation. Use finished events for outcomes and durations. Keep
blocked and unsupported attempts separate from execution failures.

## Data and controls

The client sends no customer organization ID, persistent installation or user ID,
API/application/OAuth key, hostname, repository identity, file path, command line,
source code, test name, free-text error, output, report, or diff. Customer
attribution is not implemented.

Set any of these to disable reporting before invoking DDTest:

```shell
export DDTEST_TELEMETRY_DISABLED=1
# Alternatively: DO_NOT_TRACK=1 or DD_INSTRUMENTATION_TELEMETRY_ENABLED=false
```

Delivery runs in the background, with a two-second request timeout and at most
three seconds of waiting before process exit. Events are not retried or persisted.
Delivery errors and HTTP rejections do not affect command results or write to
stdout/stderr. The client does not follow redirects or use customer Agent/intake
configuration; testdrive's mock intake is independent.

These are best-effort usage counts. A missing finish is unknown: interruption,
process termination, or delivery loss may explain it. Offline and opted-out usage
will not appear. Public-token events are not authenticated customer identities.

## Local verification

Run `go test ./internal/usagetelemetry ./internal/cmd -run Usage` and
`go test ./internal/usagetelemetry -run 'Attempt|Disabled'`. The tests use a local
HTTP server and a dummy token, covering lifecycle events, outcomes, opt-outs,
payload allowlisting, early errors, cancellation, HTTP rejection, redirects and
timeouts. They never send telemetry to Datadog. Run `make test` and `make lint`
for the full repository checks.
