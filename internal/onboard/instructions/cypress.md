## Cypress validation

These are instructions for the coding agent. Inspect the repository and apply
the CI setup, then validate with the customer's actual headless command.

1. Record the initial Git status, including ignored files. Inspect all Cypress
   workflows, package aliases, startup commands, browser selections, config and
   support files. Keep existing tests, hooks, reporters, matrix entries and
   Cypress Cloud settings. Do not replace a server/test alias with bare Cypress.
2. Run ddtest testdrive --check-only --yes. Resolve one exact compatible tracer
   release and pin it in both CI and the final testdrive. Check the repository's
   Node engine as well as the tracer's. Local Node compatibility does not prove
   that a different CI or Cypress-bundled Node runtime is compatible. For the
   action API path, testdrive reads the action's runs.using Node major. Run with
   a compatible release of that major, satisfying the application's engines too.
   Use an existing runtime manager or an isolated runtime; preserve CI versions.
3. Initialize dd-trace through NODE_OPTIONS, as for other Node.js frameworks:
   `NODE_OPTIONS='-r dd-trace/ci/init' <existing test command>` when dd-trace is
   installed in the test project's dependencies. For the GitHub action's isolated
   installation, use its exported initializer path as shown below; the bare
   package name does not resolve an isolated action installation.
   Preserve tests on fork pull requests where Datadog secrets are unavailable:
   guard tracer installation and its added preload together, without guarding
   or removing the existing test step. Keep existing NODE_OPTIONS in the
   uninstrumented branch. Non-recording Cypress jobs often run on forks too.
   Supported tracers automatically install browser instrumentation through
   defineConfig and supported CLI config loading. Prefer exporting the existing
   config through Cypress's defineConfig for programmatic runners such as
   cypress-io/github-action. Preserve existing setupNodeEvents, event callbacks
   and supportFile. Do not add or generate tracer plugin/support wrappers as
   part of onboarding. Preserve an existing manual integration. If the preload
   does not install browser instrumentation, report the concrete diagnostic as
   a blocker before proposing a manual integration.
4. Cypress Test Impact Analysis needs application coverage: the tested page must
   expose Istanbul's window.__coverage__ (babel-plugin-istanbul or
   vite-plugin-istanbul in the build served to Cypress). Testdrive requires that
   coverage in the real suite and a test-level ITR skip of its probe. Preserve an
   existing coverage setup; @cypress/code-coverage is not required. Version 3 of
   @cypress/code-coverage fails when the tracer skips a test; it needs version 4
   or later, which requires Cypress 15.10 or later.
5. Finish ALL repository edits before final validation, including CI, coverage
   instrumentation, README/documentation, ignored-file rules and lint/format fixes.
   Run the repository checks appropriate to any config or startup source files
   you edited, including their existing lint rules. Validation is a read-only
   phase for the agent: after launching testdrive, only wait with the host tool
   until it exits. Do not edit any file or run parallel cleanup, lint fixes or
   documentation updates while it runs. Such edits invalidate the snapshot and
   require a new full run; ddtest stops remaining browser launches when detected.
   Run ddtest testdrive --agent --all --tracer-version <exact-release>
   --yes after all edits. A final run overriding one alias is insufficient for
   onboarding when other CLI or API configurations were discovered.
   Record/parallel workflows do not require a Cloud key locally: testdrive disables
   Cloud recording/grouping while replaying their test commands and API options.
   Testdrive uses one unambiguous declared invocation; it never invents a fallback command.
   Use --command '<original alias>' for another explicit configuration.
   Without --command, one unambiguous CI invocation (or a unique package script) is selected with its
   declared environment and setup. Multiple invocations require --command or --all. --command is a local override; use --all
   for CI configurations rather than repeatedly overriding one alias.
   Run --all to exercise every discovered local CLI configuration with its
   declared CI environment. The Cypress action is replayed through Cypress.run with its actual options;
   its build/start/wait-on inputs are retained. Background startup followed by
   the CLI is managed without evaluating arbitrary shell syntax. Do not replace
   API steps with CLI commands to obtain a passing verdict. Allow enough tool
   execution time for all paired comparisons and feature controls; wait for the
   complete run before further checks. Preserve selected browsers. Missing local
   browsers and unavailable OS variants stay explicitly unvalidated; do not
   remove matrix rows or substitute Electron to hide them.
   Keep startup inside that command, so each disposable copy serves its own
   sources and cleans up its servers. Do not run a shared manual server against
   the customer checkout. The local observer disables Cypress Cloud recording
   and parallel grouping, keeps the reporter and browser options, and records
   native after:run results. No real Datadog or Cypress Cloud key is needed.
   Cypress component testing and Windows process supervision are not supported by this validation. Unsupported commands/configurations remain incomplete; preserve their scope.
6. Review both native runs, the telemetry comparison, real-suite coverage and the six
   required controlled feature results (automatic retries, early flake detection,
   skipping, quarantine, disabled tests and attempt-to-fix). Use the report's compatibility.status; matching counts alone
   cannot establish compatibility. Existing failures can pass when test identities,
   results, failure details and exit codes match. Preserve an inconclusive verdict.
   Fix configuration or instrumentation errors and rerun the complete command.
   Browser/OS variants not executed locally must remain explicitly unvalidated.

Keep only report.html and testdrive.json under .testoptimization as generated
validation outputs. Remove only your own manual diagnostics and temporary files,
including generated Cypress fixtures, screenshots and videos identified by the
initial ignored-file inventory. Preserve customer files and onboarding edits.
Start your response with the latest report's verdict and blockers, including
absolute Markdown report links and any INCOMPLETE verdict.

If validation is INCOMPLETE, do not say "onboarding is complete up to human
action". Unknown CI guards, an unvalidated execution path or a failed required feature are
validation gaps; adding a Datadog key does not resolve them. Name those gaps
separately from the later human credential setup, and keep the generated verdict
as the opening sentence.

## Configure GitHub Actions

After dependency/browser setup and before each identified test step, add:

```yaml
- name: Configure Datadog Test Optimization
  uses: datadog/test-visibility-github-action@v3
  with:
    languages: js
    api_key: ${{ secrets.DD_API_KEY }}
    site: datadoghq.com
    js-tracer-version: <resolved-release>
```

Use the exact resolved local tracer in CI too; the action default may lag npm.
Confirm the site before connecting CI; the example above is provisional US1.
Merge the action's preload into the original test step's existing Node options:

```yaml
env:
  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }}
```

The action's DD_TRACE_PACKAGE resolves dd-trace/ci/init in its isolated install.
Cypress does not need the ESM --import loader merely because its config uses ESM.
Do not add manual dd-trace plugin/support imports to the Cypress config or support
file. Application Istanbul coverage for TIA is a separate build requirement.

Keep installation and its added preload under the same existing fork/key guard.
For a new key guard, assign the secret to a job-level environment variable and
check `env.<name>` in the step condition. GitHub forbids `secrets.*` directly in `if`.
Preserve the test step in the uninstrumented branch. Exclude `.testoptimization/`
from Git and published packages, including any package `files`/`.npmignore` rules.
Actual CI execution and Datadog/Cypress Cloud processing are not tested locally.

## Ask a human to connect Datadog

After local validation, ask the human to follow the
[Datadog API key instructions](https://docs.datadoghq.com/account_management/api-app-keys/#add-an-api-key-or-client-token),
create a key in their selected site, and add the GitHub secret `DD_API_KEY`.
Do not read/create the secret or request the key in chat. Commit and push the
onboarding edits only after their confirmation. Local validation needs no key.
