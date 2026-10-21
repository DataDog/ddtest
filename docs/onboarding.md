# Onboarding and local validation

Coding agents can use the same short request for any selected framework:

> Use ddtest to onboard this repository to test-optimization for vitest only.

`onboard` returns a versioned result with the next command and required local
capabilities. `onboard --apply --yes` selects an exact compatible tracer,
applies recognized GitHub Actions initialization/report exclusions, and runs the
shared testdrive coordinator. Ordinary Jest, Vitest, Mocha and Playwright Node
CLI jobs share the editor, including ordinary inherited environments. Known
Cucumber NYC/ESM and WDIO local-runner package leaves receive scoped initialization;
other framework consumers keep their original commands. Missing Cucumber coverage
packages return an exact npm install command followed by the automatic resume
command. Unknown hooks or ambiguous CI return a named blocker. Static legacy Node
matrices retain every row and original legacy command; only compatible rows receive
installation/preload. Unsupported runtimes remain explicitly uninstrumented. `--instructions` retains manual guidance.
For a new CI action, `--site` overrides `DD_SITE` and the default `datadoghq.com`;
local validation requires no API key and does not verify the CI secret/site.

For an already configured repository, run
`ddtest testdrive --framework vitest --all --agent --yes`. Both entry points use
one coordinator: source snapshot, execution, Git/npm package exclusion checks,
owned cleanup and the final verdict. The package check is
`npm pack --dry-run --ignore-scripts --json`: it creates no archive and does not
publish or run lifecycle scripts. Checks/commands and edit receipts stay in the
existing JSON; full test output stays in the HTML. A failed workflow check keeps
onboarding incomplete even when the native comparison passes. An OS lock rejects
concurrent coordinator runs; reusable lock metadata lives outside the checkout.

Agent results distinguish `ready`, `running`, `blocked_environment`,
`needs_input`, `unsupported`, `validation_failed`, `selected_passed`, and
`complete_locally`. A blocker names its phase, code, location, required fact or
capability, and next/resume command. Only a recognized transient registry
transport failure may retry once before execution; permission/authentication
failures and feature assertions are never automatically retried by the coordinator.
Unchanged saved results can be rendered without running tests:

```sh
ddtest testdrive --report               # compact state and factual text
ddtest testdrive --report --format json # direct host consumption; no LLM needed
```

The renderer refuses a passing handoff when source/configuration, the observed
framework installation or Node runtime changed. `--format json` also makes
onboarding/execution return structured results without the rendered text. Hosts
can display `final_response` directly; agent wording is not the source of truth.
Successful execution returns the exact `--report` command as the final action.
Run it after all edits; an edit afterward invalidates completion, including a
formatting-only edit. A standalone binary cannot prevent a later agent edit;
hosts requiring that guarantee must render/check the result at final delivery.

Routine responses contain `plan_summary`, the current blocker/input template and
recovery argv rather than the complete CI matrix. Use the returned
`details_command` to inspect the full plan without running tests. Replace its
`--detail plan` with `--detail configuration-ID` for one configuration. Interrupted
plans are read from the existing private checkpoint via `--resume`; completed
plans live in the existing JSON report. No extra repository report is created.

Jest validation uses a private Watchman socket/state when Watchman is installed.
Cleanup stops that instance and verifies exit before removing its storage on
success, failure or cancellation. Shared daemons and caches are preserved;
unverified owned cleanup keeps the result incomplete.

Browser descendants hold live ownership connections; cleanup does not kill PIDs
read from old files. Detached groups retain a live owned member until cleanup.
After a crash, the next workflow acquires the repository lock and removes only
probes whose exact paths and unchanged contents match private ownership records.
Edited probes are preserved and reported as a blocker. Probe records survive in
the private `.testoptimization/.probes/` directory until cleanup; a successful run
leaves only the two reports. Cypress probes use configuration-selected modes, so
there is no source rewrite between scenarios and abandoned probes are skipped.

Probe publication requires hard-link support and a test directory on the same
filesystem as `.testoptimization/.probes/`. A cross-filesystem mount or a
filesystem without hard links leaves feature checks inconclusive with
`probe_publication_unsupported`; ddtest preserves existing files and does not
fall back to a non-atomic write. Run validation from a checkout on one supported
filesystem, including its test directories, then retry.
Source fingerprints include submodules and untracked nested repositories.

Browser comparisons use native-result observers and process supervision in both
modes. Baseline disables dd-trace, but is not an unmodified shell invocation; the
JSON records this in `execution_context`. Supervision failures invalidate the run
instead of being treated as pre-existing test failures.

When a missing local build prerequisite needs repository knowledge, the agent
may supply `--facts /outside/checkout/setup.json` with `--all`. For example:

```json
{
  "schema_version": 1,
  "input_fingerprint": "<fingerprint from the current result>",
  "prerequisites": [{
    "for_command": "vitest run",
    "argv": ["npm", "run", "prepare:test"],
    "expected_files": ["dist/index.js"]
  }]
}
```

Facts must match the current source snapshot and exactly one discovered root
command. The prerequisite must reference an existing package script or a local
Node script. It runs in the isolated configuration workspace, and its successful
exit plus repository-local output files are checked before either comparison.
Facts record an agent-supplied setup relationship; they cannot remove commands,
erase unresolved CI, bypass coverage/feature assertions, or upgrade a failed
verdict. Fresh facts are required after source/configuration changes. Preserve
supplied files; ddtest removes only storage it creates. Framework-owned native
application profiles remain subject to their adapter's cleanup support; the
workflow receipt does not certify unrelated profiles or shared caches.


Every coordinator result includes a resolved `plan`: configuration IDs, original
CI locations and commands, working directories, environments, prerequisites,
services, runtime/tracer selection and feature scenarios. An unresolved entry
remains visible and keeps onboarding incomplete. Configuration IDs distinguish
environment and package variants and remain stable when CI line numbers move.

Schema version 1 facts above remain supported. Prefer schema version 2 when
commands repeat or setup belongs to a subpackage:

```json
{
  "schema_version": 2,
  "input_fingerprint": "<current fingerprint>",
  "configurations": [{
    "configuration_id": "<ID from plan.scope.commands>",
    "directory": ".",
    "prerequisites": [{
      "argv": ["npm", "run", "build"],
      "expected_files": ["dist/index.js"]
    }],
    "services": [{
      "argv": ["npm", "run", "start"],
      "directory": ".",
      "readiness_url": "http://127.0.0.1:8080/",
      "timeout_seconds": 60,
      "port_environment": "APP_PORT"
    }]
  }]
}
```

Only supply setup that the configuration actually needs. Expected build output
paths are relative to the repository root. Working directories must agree with
the discovered CI directory and remain inside the checkout. Services run in the
isolated configuration workspace; ddtest checks HTTP readiness, cancels validation
if startup exits early, and stops owned processes before removing the workspace.
`port_environment` is optional and must already be supported by both application
and test configuration. It passes the selected port consistently to both; it does
not rewrite config files. Without an override, an occupied port is a blocker and
the existing listener is preserved. Existing framework-managed servers stay in
their original command/adapter; do not duplicate them as service facts.

For one action-owned Cypress application, use a configuration fact with
`"port_override": {"environment": "PORT", "source_file": "serve.js"}` instead of
adding another service. The source must already read that environment variable
with its existing default. If a source edit is needed, obtain a fresh fingerprint
afterward. ddtest preserves the action's startup command and API path, chooses a
free local port, and applies it to startup, `baseUrl`, and readiness. The resolved
plan records the local override; CI files and default ports are not rewritten.
Custom config strings or complex readiness/startup arrangements require review.

A blocked coordinator returns `run_id`, a typed `input_template` when applicable,
and `resume_command`. After filling the template outside the checkout, append
`--facts /absolute/path/setup.json` to that command. Both onboarding and agent
validation accept `--resume <run-id>`. A private checkpoint retains only the
bounded plan and successful preflight selection for up to 24 hours. Selection is
recomputed if source, runtime, facts, requested tracer or ddtest binary changed;
all baseline/instrumented pairs and feature controls always execute fresh.
Completed checkpoints are removed. Expired checkpoints are rejected and removed
when resumed. Three identical failures without changed inputs stop suggesting a
retry. No checkpoint can supply a passing test verdict.

With your project's test dependencies and any required browsers/services ready:

```sh
ddtest onboard    # Detect setup and return the next onboarding action
ddtest testdrive  # Preview, confirm, and run tests locally
```

No Datadog account or Agent is needed. Testdrive links to an HTML report at
`.testoptimization/report.html`. Use `--yes` after reviewing the preview in
non-interactive sessions, `--framework` to select a runner, and `--command` for
package scripts or custom setup.

The frameworks listed above can collect local telemetry. JavaScript compatibility
and feature validation covers Jest, Vitest, Playwright, Mocha, Cypress, Cucumber
and WebdriverIO. Other frameworks and unsupported configurations remain
explicitly unvalidated. If a repository contains several
runners, select one with `--framework`. Use `--command` to select a custom entry
point or a small representative part of a large suite:

```sh
ddtest onboard --framework playwright
ddtest testdrive --framework playwright --command 'npm run test:e2e -- --project=chromium' --yes
```

For Cypress, testdrive compares native results and telemetry, then requires five
controls: automatic retries, early flake detection, quarantine, disabled tests and
attempt-to-fix. **TIA skipping and application coverage validation are deferred**,
not passed or unsupported by the tracer. Missing application Istanbul coverage
alone does not block a local pass. ddtest does not check Cypress TIA or application
coverage yet; preserve any existing coverage setup. The
plan, HTML and JSON disclose the deferral. A local pass does not validate Cypress
TIA, actual CI execution or Datadog backend processing.

For WebdriverIO, use `--framework webdriverio` or `--framework wdio`:

```sh
ddtest onboard --framework wdio
ddtest testdrive --framework wdio --command 'npm run test:e2e' --tracer-version <resolved-release> --yes
```

WebdriverIO validation preserves the actual WDIO command, services, capabilities
and reporters. It compares native worker results with and without tracing, then
checks retries, early flake detection, quarantine, disabled tests and attempt-to-fix
per capability. TIA skipping and code coverage are **not supported** by the tracer
for WebdriverIO. The local runner with Mocha or Jasmine requires WebdriverIO >=9
and dd-trace >=5.121.0 on v5 or >=6.10.0 on v6. These are capability minimums;
select a current compatible release rather than using them as installation pins.
Browser runner, standalone automation, custom adapters and unavailable browser
variants remain unvalidated. A focused command does not validate the whole CI
matrix; review `--all --check-only` before executing additional configurations.
The focused result appears in `summary.selected_validation`; a `PASSED` selected
command exits zero after its comparison, five feature controls, static CI checks
and cleanup pass. Whole-repository onboarding remains separate and can still be
`INCOMPLETE` while other configurations are untested. Rerun the full focused
testdrive after any workflow/package/config edit; CI execution does not update
a local report. Mixed commands can scope the preload through
`DD_WDIO_NODE_OPTIONS` on the CI step and the WDIO leaves described by `onboard`.


Testdrive prints a terminal summary and retains two reports:
`.testoptimization/report.html` uses the upstream HTML renderer to show the latest
instrumented suite's tests, findings, attempts, source excerpts, coverage and full
command output. With `--all`, output is labeled by configuration.
`.testoptimization/testdrive.json` records the validation verdict: an explicit
`success` flag, compatibility, CI runtime and feature verdicts, and the working
directory. Each validation run records its exact shell-quoted command,
instrumentation/probe mode, exit code and aggregate counts. Mismatch examples are
limited to ten and diagnostic text is capped at 1,024 characters in the JSON.

For each validated JavaScript framework, an explicit `--command` has its own
`summary.selected_validation`. It passes and exits zero only after the selected
command's current comparison, all supported features, static CI checks and
cleanup pass. Other configurations can keep overall onboarding `INCOMPLETE`.
`summary.executed_validation` describes the comparisons and features actually
executed, while `summary.configuration_coverage` counts validated, blocked and
unexecuted configurations. These do not override the overall verdict: a partial
`--all` run still fails when any required configuration remains unvalidated.

Aggregate HTML and JSON `reporting_counts` distinguish reported test executions
from source-test identities. For example, the same 32 native file/name identities
reported by three configurations produce 96 executions. Different commands and
environments remain separately validated. Project/browser/repeat identity is
preserved when reported; missing or ambiguous native identities leave the source
identity count unavailable. JSON retains aggregate counts, not every identity.
The compact `summary.final_response` carries these counts alongside the executed
and repository verdicts, configuration coverage, feature results and report links.

The HTML describes the instrumented suite, not the overall validation verdict;
synthetic feature probes never replace its findings. It links to the compact
JSON rather than discarded traffic or output files. Each executed suite replaces
the HTML at the same path. Configuration-only checks leave earlier HTML unchanged
and do not claim that tests were rerun. Raw output and telemetry payload files
are discarded after validation; report directories do not accumulate.

JavaScript/Python fallback tracers, captured requests, and runner files live in a
private OS temporary directory. Ruby fallback installation uses the project bundle
and retains its dependency changes. Testdrive removes that directory on success, failure, and
handled cancellation. Temporary probe files inside the project are also removed.
Bounded setup errors and summaries of attempted runs are preserved when the report
can be written. Other `.testoptimization` data is preserved.
Each JavaScript baseline, reporting-only and probe execution receives a temporary
directory inside its owned session through `TMPDIR`, `TMP` and `TEMP`, so child
application scratch shares the same cleanup. Existing or supplied package/browser
caches remain reusable; agents only need to remove dedicated extra scratch and
processes they create, preserving artifacts whose ownership cannot be established.

Jest validation has two stages:

1. Run the same command without instrumentation and with reporting-only
   instrumentation. Compare test identities, outcomes, failure details, and
   process exit codes; ignore timings, result ordering, and stack frames.
   Existing failures are acceptable when both runs match. If results differ,
   repeat the pair: changing results are inconclusive, while a repeatable
   difference is a suspected regression. Matching results require matching
   telemetry before compatibility can be reported. Setup failures before tests
   execute are inconclusive.
2. Place a temporary probe beside an existing test, preserving the project's
   Jest configuration. Establish passing and failing controls, then enable one
   feature per run: auto retries, early flake detection, skipping, quarantine,
   disabled tests, and attempt-to-fix. Check actual execution, exit outcome, and
   feature-specific telemetry. Remove the probe when finished, including on
   errors. If the command/configuration cannot select the probe, its checks are
   inconclusive. These checks validate the probe under the selected configuration;
   they do not establish compatibility for every test environment in a monorepo.

Jest skipping uses the control's `test.source.file` to identify the file to skip;
test-management scenarios keep the reported suite and test names. Some tracer
versions report a different suite name when a file is skipped. The report retains
both names and notes the difference separately from whether skipping worked.
A missing source path makes the skipping check inconclusive.

`testdrive.json` separates compatibility (`compatible`, `suspected regression`,
`inconclusive`) from each feature (`passed`, `failed`, `inconclusive`, `unvalidated`).
For Jest repositories with detected GitHub Actions test jobs, `ci_runtime` records
whether each instrumented Node runtime satisfies its workflow-selected tracer's
`engines.node` requirement. The checker reads public GitHub action metadata at the
configured action ref and npm package metadata, honoring `js-tracer-version` when
set. No tracer version or minimum Node version is pinned in the checker. This can
differ from the tracer installed for the local testdrive.

The check supports literal `actions/setup-node` versions, static matrix axes, and
static `include`/`exclude`, and matrix equality/inequality, boolean values, `startsWith`, `!`, `&&`, `||`, and parentheses.
Explicitly excluded entries remain visible as uninstrumented. Dynamic matrices,
version files, LTS aliases, other conditions, mixed-type comparisons, unsupported engine
ranges, and unavailable metadata are inconclusive. Currently engine comparison
supports minimum requirements such as `>=22` or `>=22.2.0`; it does not approximate
other ranges. This checks the declared setup-node runtime, not arbitrary shell
commands that might later change Node. It does not execute CI or prove bootstrap
correctness, remote instrumentation, or Datadog backend connectivity.

For supported JavaScript frameworks, the command exits successfully only when local compatibility, all feature checks,
and the applicable CI runtime check succeed. A runtime incompatibility or unknown
configuration makes `success` false even if every local test matches. Repositories
without detected GitHub Actions test jobs can still pass local validation; their
CI runtime result is explicitly `not applicable`. The suite's own failures do not
automatically fail validation.
Python and Ruby retain their existing exit contract: exit 0 means the test
command succeeded and test events reached the local intake. A failed command,
missing events, or a setup/reporting/cleanup failure returns a nonzero exit code.
Onboarding instructions remain identical to main for pytest, RSpec and Minitest.
Their plain testdrive reports command execution and received events without adding
a compatibility/feature onboarding verdict. `--format json` and `--agent` return
`unsupported` with no `next_command`; full Python/Ruby validation belongs to the
separate language changes. The one intentional user-visible change for these
languages is the shared report location: `.testoptimization/report.html` and
`.testoptimization/testdrive.json` replace per-run `.testoptimization/testdrive/<id>/`
report directories.

The local intake supports agentless traffic; Agent/EVP routing is not supported.
No results are sent to Datadog. Temporary installations and run files are removed;
only the HTML findings and compact JSON validation reports remain. Keep both out of source control.

Testdrive reuses the project's tracer when the platform's tracer check succeeds.
If the check fails, it attempts a fallback installation (latest by default).
`--tracer-version` selects a release or Git revision for that fallback installation;
JavaScript and Python installations leave project dependency files unchanged;
Ruby uses `bundle add datadog-ci`, which updates the project Gemfile and lockfile:

```sh
ddtest testdrive --tracer-version <selected-release-or-tag> --yes
ddtest testdrive --tracer-version 'git:<commit-sha>' --yes
```

The same option works for JavaScript, Python, and Ruby. Git selections also accept
branches and tags; they require Git and the tracer's source-build prerequisites.

Local testdrive prerequisites:

- JavaScript: Node.js 22+, npm if `dd-trace` needs to be installed,
  and the project's package manager and dependencies. Vitest/ESM loading and
  Cypress config/support wrappers are supplied automatically.
- Python: an activated project environment with pytest, and pip if `ddtrace` is
  absent. Fallback installation uses a session-owned directory; the active
  environment is unchanged.
- Ruby: Ruby/Bundler. If `datadog-ci` is unavailable, testdrive runs
  `bundle add datadog-ci` in the project, honoring `BUNDLE_GEMFILE` and the
  existing bundle settings. Bundler updates the project Gemfile and lockfile;
  tests use that same bundle. Native gem builds need build tools.
- Browser suites: install the project's browsers and start any required services
  first, or use its existing test command that manages them. Testdrive does not
  install browsers or start applications on its own.

Jest preflight resolves the fallback selection once before installation and inspects
`--showConfig` for the effective Jest version, runner, and configuration. It checks
known dd-trace 5/6 Jest requirements and the tracer's Node engine minimum before
running the suite. Unknown combinations remain unverified. Use `--command` with
the project's actual Jest arguments when it uses a custom config. Preflight loads
project JavaScript after confirmation. Preview runs a read-only installed-tracer probe
(with a 10-second timeout) to show whether the tracer will be reused or installed;
it does not load Jest configuration, install dependencies, or run tests.

`ddtest testdrive --check-only --yes` performs those configuration checks and static
CI checks without installing a tracer or running tests. It updates the same JSON
report, with `check_only: true`, `checks_passed`, and test execution marked not
exercised. A successful configuration check does not set validation `success`.
The latest paired Jest execution is preserved under `retained_execution.result`,
including its timestamp, tracer, commands, counts, and feature verdicts. Later
configuration checks, unsupported-framework runs, and setup failures retain this
historical evidence; a new paired Jest execution supersedes it. There is no growing
report history. Retained evidence never changes the current invocation's verdict:
after changing the command, configuration, source, dependencies, runtime, or tracer,
rerun full validation before claiming current compatibility or feature success.

Full validation records `local_success` separately from the combined `success`.
CI Node compatibility and exact local/CI tracer agreement are separate findings;
actual CI execution is always `not exercised`. Moving CI selectors are valid only
for the version resolved during this check. Pin the selected release in the
workflow to retain that agreement. Unsupported checker syntax requires review,
not a workflow rewrite. Manual review does not override a programmatic verdict. Automatic onboarding uses
`datadog/test-visibility-github-action@v3` and pins the validated tracer release.

Project dependency manifests and lockfiles are not edited by testdrive. Testdrive
automatically selects a unique, statically resolved root-level Jest CI command,
including its package script and options. Without a CI command, it tries `test:ci`
and then `test`; otherwise it uses the runner default. Ambiguous commands, lifecycle
hooks, and scripts that cannot forward Jest options safely require review and an
explicit `--command`. An explicit command always takes precedence. Generated Jest
coverage goes into temporary session storage and is removed afterward; existing
customer coverage is preserved. Tracer downloads require network access. Recognized GitHub Actions commands can be validated across packages; unknown
orchestration and other CI providers require explicit review.
