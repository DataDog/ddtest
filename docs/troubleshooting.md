# Automatic troubleshooting telemetry

When `onboard` or `testdrive` finishes with a failure, blocker or unsupported
result, a build with a configured public telemetry token attempts to send one
allowlisted `ddtest.troubleshooting` log. It shares the random per-invocation
`attempt_id` with the existing start/finish usage events. There is no upload
prompt, separate command, agent handoff or temporary diagnostic JSON file.
Agent approval modes and `--yes` do not control telemetry.

The event is constructed in memory from fixed command metadata, typed error
observations and an allowlisted projection of the current validation report.
The original `.testoptimization/testdrive.json`, findings JSON, captured
traffic and test output are never uploaded. Only a report observed/written in
this invocation can supply evidence; saved reports are not discovered as a
fallback. Successful runs, cancelled runs, execution-consent prompts, invalid
options and saved-report rendering send no troubleshooting event. Their
existing coarse usage reporting is unchanged, except build versions are now
normalized as described below.

## Controls and delivery

All telemetry, including diagnostics, is disabled before invocation with any
one of these settings:

```sh
export DDTEST_TELEMETRY_DISABLED=1
# Alternatively: DO_NOT_TRACK=1 or DD_INSTRUMENTATION_TELEMETRY_ENABLED=false
```

A build without an embedded public token sends nothing. The destination uses
the existing DDTest release token and receiving organization, independently of
the customer's keys, `DD_SITE` or local Agent. There is no arbitrary-file upload
command. The final event must pass the fixed schema and normalization checks
before delivery and is limited to 64 KiB. The source report must be a regular
file no larger than 32 MiB.

Delivery uses the same background sender as usage events: a two-second request
timeout, at most three seconds waiting for pending requests at exit, no
redirects, no automatic retries and no persistent queue. Delivery errors do
not change the command result or write to stdout/stderr. A hard interruption,
network failure or opt-out can leave no diagnostic event.

## What the JSON contains

| Data | Detail |
| --- | --- |
| Correlation | Same random per-invocation `attempt_id` as usage logs; command, mode, outcome, allowlisted phase/code, timestamp, DDTest version, OS and architecture. |
| Validation context | Framework, numeric tracer/runtime version components when available, check-only/all-configurations/stopped-before-execution flags, configuration count. Custom refs and version suffixes are discarded; unknown values become `unknown`. |
| Failure observation | Fixed operation and typed error category, with up to eight DDTest-owned function/source-file/line entries. `stack_origin: observation` identifies the call stack where DDTest observed the failure, not the original throw/crash stack. No error message or customer frame is retained. |
| Version observations | Runs include numeric framework and library versions reported by tracer events, separately from the selected `tracer_version`. Effective event metadata overrides payload defaults. `ddtest_commit` identifies the build revision when Go embeds it for the DDTest main module; it is absent when unavailable. |
| Tracer observations | Instrumented runs include local settings/skippable/known-tests/test-management request counts and per-feature advertised capability state. Executed feature scenarios name their requested feature. Unavailable counters are `null`; absent capabilities mean unknown, malformed advertisements are `unknown`, and conflicting advertisements are `mixed`. |
| Feature checks | Each named entry includes its status, tracer error categories, and structured `evidence`: fixed failure category, expected/observed counts and flags. Known limitations retain their original observed code and evidence. Customer project names and textual reasons are excluded. |
| Runs | Fixed role (`baseline`, `reporting-only`, `repeat`, `probe-control`, `probe-feature`), instrumentation flag, exit code, timeout/error presence, passed/failed/skipped/other counts, suite error count, tracer test-event count, and numeric runtime versions. |
| Completeness | `report_status` says whether the current local report could be read. Lists are limited to 32 features and 32 runs, eight runtime versions per run, and ten counts/three flags per feature. Entries are also trimmed to keep the event within 64 KiB; `truncated` identifies omissions. Local reports over 32 MiB are not read. |

### Per-feature evidence

`diagnostics.features` contains named entries for auto-retries, early flake
detection, skipping, quarantine, disabled tests and attempt-to-fix. A feature's
`evidence.counts` records each numeric assertion with `name`, `comparison`
(`equal` or `at_least`), `expected` and `observed`. `evidence.flags` records
boolean expectations and observations, such as a quarantine tag being present.
These are the assertions that determine the feature verdict, not inferred
measurements reconstructed from an error message.

| Feature | Evidence checked |
| --- | --- |
| `auto-retries` | At least two telemetry attempts, at least one matching retry and passing event, exactly one failing event, a native passing result and no native failures. |
| `early-flake-detection` | At least two passing telemetry attempts and one matching retry, no failures, and native repetition evidence under the framework's reporting rules. |
| `skipping` | Skip evidence, no executed native tests or passing/failing telemetry events, and no unexpected test events. Planner skipping records its planner decision instead. |
| `quarantine` | Quarantine tag, exactly one failing telemetry event, no passing events or matching retries, and one native result. |
| `disabled` | Disabled/skip tag, no native body execution, and an acceptable skipped or empty native result under that framework's rules. |
| `attempt-to-fix` | Attempt-to-fix tag, at least two passing telemetry attempts and one matching retry, no failures, and native repetition evidence. |

Executed feature scenarios also require exit code zero. Frameworks with a probe
body counter compare it with the number of matching passed/failed telemetry
events. A missing body count is `null`, not zero. Minimum retry/attempt counts
are lower bounds: different tracers can repeat a probe more times.

For example, a failed auto-retries entry can contain this excerpt:

```json
{
  "failure_category": "retry_not_observed",
  "counts": [
    {"name": "matching_retries", "comparison": "at_least", "expected": 1, "observed": 0},
    {"name": "telemetry_attempts", "comparison": "at_least", "expected": 2, "observed": 1}
  ]
}
```

`failure_category` identifies a primary unmet assertion, such as
`management_tag_missing`, `native_result_mismatch` or `nonzero_exit`; all
assertions remain available to inspect other differences. It is not a claim
about root cause. An unavailable or unsupported check carries a fixed category
without invented observations. Evidence can be absent when no feature scenario
was reached or the report was generated by an older binary.

Only these fields are projected from the local report. No repository/customer
identity, persistent ID, hostname, local path, command line, environment value,
source code, diff, test name, HTTP payload, free-text error, stdout/stderr, stack
trace text, or crash dump is included. Error and timeout strings are reduced to
booleans. There is no arbitrary-file attachment or upload entry point.

Typed error categories include invalid JSON, unexpected EOF, filesystem,
subprocess exit, network, timeout and cancellation. Unclassified errors remain
`unknown`; semantic failure results without an underlying error use
`reported_failure`. These categories do not identify the party at fault.
Observation frames come from the running DDTest binary, never by parsing an
error string. Only DDTest package symbols and corresponding relative published
source filenames survive; customer frames, absolute paths, arguments, local
variables and raw library stacks are excluded. A frame can identify the failed
workflow branch, but does not claim to identify the original exception site.

`tracer_state.initialization` is `events_observed` only when test events reached
the local intake, otherwise `unknown`. This does not prove every integration
initialized. Request counts show contact with local control endpoints, not that
the tracer accepted or applied a response. Capabilities summarize positive,
zero or conflicting numeric advertisements from captured test events; an
`available` advertisement does not replace the feature's behavior assertions.
Ordinary runs retain only the allowlisted capability states and version fields,
without retaining customer event tags for those observations.
Version observations are deduplicated as each payload is read; full event/tag
copies are reserved for probe scenarios that explicitly collect them.

Raw framework assertions and tracer crash stacks can be valuable to engineers,
but replacing paths alone does not make them safe: messages and stack frames
can contain secrets, request bodies, URLs, and source snippets. This first
version keeps them local. A hard crash that prevents DDTest from writing a
result cannot produce this diagnostic summary.

## Privacy assessment

The JSON contains no customer/organization identity, repository name/URL/path,
customer commit/branch, persistent installation ID, hostname, user information,
credentials or free-text errors. Framework names, feature names, verdicts,
error categories and capability states are fixed allowlists. Version strings
retain numeric components only (`dev` is retained for development builds);
custom refs, URLs and suffixes are discarded or become `unknown`. Java 8 update
versions such as `1.8.0_442-b06` become `1.8.0.442`, retaining the numeric update
and discarding the build suffix. The same normalization applies to build
versions in the start/finish events. `ddtest_commit` refers to the
DDTest binary's source revision, never the project's Git checkout. Frame
locations are DDTest-owned symbols and relative source filenames, never customer
or third-party stack traces.

The retained versions, feature behavior, exact test/event counts, flags and
failure timing are still technical information about a user's execution.
Combinations can distinguish an unusual environment even without an explicit
identity. The intake also necessarily receives network connection metadata,
such as the sender's source IP; it is not a JSON field added by DDTest. This is
a data-minimization boundary, not a claim that the data cannot be sensitive or
that its collection has received a particular privacy approval. Receiving-org
access and retention remain part of the collection's privacy assessment.

## How engineers use it

The diagnostic event is a log named `ddtest.troubleshooting`, with service
`ddtest-cli`, sent through the same public-token intake and receiving
organization as [usage telemetry](usage-telemetry.md). It does not use the
customer's keys or site and carries no customer attribution. The existing
`DDTEST_TELEMETRY_DISABLED`, `DO_NOT_TRACK`, and
`DD_INSTRUMENTATION_TELEMETRY_ENABLED=false` controls disable diagnostics too.
Builds without an embedded public token cannot send telemetry.

In Logs Explorer, use `service:ddtest-cli @event_name:ddtest.troubleshooting`
and filter by `@attempt_id` to join the start and finish logs. A missing diagnostic
event does not establish that the run passed: the run may have been ineligible,
opted out, interrupted or unable to deliver telemetry.

| Scenario | Evidence and limits |
| --- | --- |
| Baseline exits 0; instrumented run exits 1 with suite errors | Suggests an instrumentation-related difference. Runtime/tracer versions and feature checks help reproduce it; the summary cannot identify the exception or prove the tracer caused it. |
| Both runs fail with the same test counts | Existing failing tests may explain the exits. Counts alone cannot establish that the same tests failed. |
| Auto retries fail with `retry_not_observed` and `tracer_control_not_applied` | Shows the mismatch directly: at least one matching retry was expected but none was observed, with fewer telemetry attempts than required. Versions and other assertions help reproduce and distinguish telemetry, native-result and exit-code problems. |
| A subprocess times out or exits 137 | Reports `timed_out` and the exit code. It does not infer OOM, SIGKILL, or a native tracer crash from an exit code alone. |
| Discovery/preparation fails before a validation report exists | Only the allowlisted failure metadata and any known context are available; `report_status` is `not_available`. |
| Telemetry disabled or token absent | No troubleshooting log; disabling telemetry before invocation also suppresses start/finish events. |

This is triage evidence. Reproductions or separately reviewed support material
may still be required for root-cause debugging.
