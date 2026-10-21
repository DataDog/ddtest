## Configure WebdriverIO Test Optimization

DEFAULT WORKFLOW: inspect the configuration inventory, finish CI/local setup edits, run one full focused testdrive, report its result, and STOP. Do not execute `--all` unless the human explicitly requests full-matrix execution and its browser prerequisites are available. A repository-level INCOMPLETE verdict after a passing focused command is expected; it is not a reason to broaden the run.

Use WebdriverIO's actual `wdio run <config>` command, preserving services, capabilities, reporters, hooks, test selection and build prerequisites. Mocha inside WDIO is not standalone Mocha. dd-trace supports WebdriverIO >=9 with Mocha or Jasmine and the local runner. Standalone browser automation, the browser runner, custom adapters and other versions must remain explicitly unvalidated until supported by the tracer and checked.

1. Run `ddtest testdrive --all --check-only --yes`. Inspect discovered commands, effective config and Node compatibility. Use its recommended resolved tracer release; do not guess an installation version or drop existing CI matrix entries. Framework capability minimums are dd-trace 5.121.0 or 6.10.0; these are not installation pins.
2. Finish CI and package edits BEFORE the full testdrive. Configure `datadog/test-visibility-github-action@v3` before the existing test step with `languages: js`, `js-tracer-version: <resolved exact release>`, `api_key: ${{ secrets.DD_API_KEY }}` and the appropriate site. Keep the original Node/test matrix. The action installs only in CI; local testdrive installs its own temporary tracer unless the project already has one.
3. Scope tracing to supported local-runner WDIO leaves, especially when `run-s` also runs standalone automation. Preserve the mixed CI command. Set this on its CI step:

```yaml
env:
  DD_WDIO_NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}
```

Prefix each supported WDIO package leaf with exactly `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_WDIO_NODE_OPTIONS:-}" `, keeping the rest of the command unchanged. For example, `wdio run ./wdio.conf.js --suite smoke` becomes `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_WDIO_NODE_OPTIONS:-}" wdio run ./wdio.conf.js --suite smoke`. Do not add this prefix to standalone scripts or the browser runner. Testdrive verifies consumption of the scoped variable. Preserve existing worker execArgv/loaders; do not invent an onWorkerStart hook merely to make CI detection pass. A CI step that exclusively runs supported WDIO can instead merge both preloads into NODE_OPTIONS directly.

For a mixed matrix, guard the Datadog action to exclude unsupported browser-runner entries, while keeping their original tests running. Do not instrument or remove those entries to satisfy the tool. Standalone automation remains listed for separate review; it is not a local-runner test.

4. Run the repository's first local-runner command: `ddtest testdrive --command '<actual npm/WDIO command>' --tracer-version <same exact release> --yes`. Use the selected framework flag. This is the default onboarding validation scope. Finish with its report; do not proceed to `--all` or start background validation to turn the overall label green. Unexecuted variants stay INCOMPLETE. One recipe is not proof of the whole matrix. Do not invent or replace test commands with a clean unrelated suite.
5. Testdrive compares native outcomes without tracing and with reporting-only tracing, then controls retries, EFD, quarantine, disabled tests and attempt-to-fix using the original configuration. WebdriverIO TIA skipping and code coverage are NOT SUPPORTED; never claim those passed. Existing matching failures are acceptable for compatibility. Missing browser/server setup or worker telemetry remains a blocker.

A focused `--command` run reports `summary.selected_validation` separately from whole-repository onboarding. `Selected WebdriverIO testdrive: PASSED` means this actual command, its five supported features, current static CI checks and cleanup passed; it exits zero for that scope. Other configurations remain explicitly unvalidated and can keep overall onboarding INCOMPLETE. Report both verdicts faithfully. `--all --check-only` is inventory/configuration inspection, not permission to execute the full matrix.

If testdrive reports an unavailable static-server port, fix local setup before running browser tests: preserve the unrelated process, add an optional environment override to both service port and baseUrl with their original default, and use that override for every validation command. Record the difference from CI. Install the ordinary browser prerequisites. Do not kill unrelated processes or narrow the suite to manufacture a pass.

Finish edits before a final full validation. `--check-only` never certifies a prior execution as current. Keep `.testoptimization/report.html` and `.testoptimization/testdrive.json`, exclude them from Git/published packages, and remove owned probes, temporary configs, tracer installs, logs/caches and server processes. Preserve customer files. No real Datadog API key is required for local checks.

After ANY subsequent workflow/package/config edit, rerun the full testdrive with the same resolved tracer before answering. An earlier JSON still describes the old files. A CI run never updates or clears a local static validation result; only rerunning ddtest against the edited files does. Do not stop after adding the action or reading an earlier report.

FINAL ONBOARDING RESPONSE: open with the latest JSON's `summary.final_response` verbatim, including the absolute report links. Do not claim completion after only installing dd-trace or loading it in an empty Node command. If there is no full execution/report, say INCOMPLETE; do not present manual CI setup as validation. Add workflow edits and cleanup afterward.

Ask a human to connect Datadog after local validation: request the site and DD_API_KEY secret setup. Do not read, create or print real keys. Actual CI execution and Datadog backend processing are not exercised by local validation.
