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
3. Initialize the action's tracer in every Cypress test step using NODE_OPTIONS.
   Preserve tests on fork pull requests where Datadog secrets are unavailable:
   guard tracer installation and its added preload together, without guarding
   or removing the existing test step. Keep existing NODE_OPTIONS in the
   uninstrumented branch. Non-recording Cypress jobs often run on forks too.
   Supported tracers automatically install browser instrumentation through
   defineConfig and supported CLI config loading. Prefer exporting the existing
   config through Cypress's defineConfig for programmatic runners such as
   cypress-io/github-action. Preserve existing setupNodeEvents, event callbacks
   and supportFile. Do not add a second plugin/support wrapper when automatic
   instrumentation works. Keep an existing manual integration; if browser
   instrumentation is missing, report the concrete diagnostics and review the
   documented manual Cypress plugin/support setup.
4. Cypress Test Impact Analysis (TIA skipping and application coverage validation)
   is deferred in testdrive. ddtest does not check Cypress TIA or application
   coverage yet; preserve any existing coverage setup. A successful run does not
   validate Cypress TIA; report it as deferred, not passed or unsupported by the tracer.
5. Before final validation, run the repository checks appropriate to any config
   or startup source files you edited, including their existing lint rules.
   Run ddtest testdrive --framework cypress --all --tracer-version <exact-release>
   --yes after all edits. A final run overriding one alias is insufficient for
   onboarding when other CLI or API configurations were discovered.
   Record/parallel workflows do not require a Cloud key locally: testdrive disables
   Cloud recording/grouping while replaying their test commands and API options.
   Testdrive selects a supported package alias with startup where available.
   Use --command '<original alias>' for another explicit configuration.
   Without --command, discovered Cypress CI configurations are selected with their
   declared environment and setup. --command is a local override; use --all
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
   Unsupported commands/configurations remain incomplete; preserve their scope.
6. Review both native runs, the telemetry comparison and the five required controlled
   feature results (automatic retries, early flake detection, quarantine, disabled
   tests and attempt-to-fix), with TIA explicitly deferred. Use the report's compatibility.status; matching counts alone
   cannot establish compatibility. Existing failures can pass when test identities,
   results, failure details and exit codes match. Preserve an inconclusive verdict.
   Fix configuration or instrumentation errors and rerun the complete command.
   Browser/OS variants not executed locally must remain explicitly unvalidated.

Keep only report.html and testdrive.json under .testoptimization as generated
validation outputs. Remove only your own manual diagnostics and temporary files,
including generated Cypress fixtures, screenshots and videos identified by the
initial ignored-file inventory. Preserve customer files and onboarding edits.
Use the final report's
summary.final_response verbatim at the start of your response, including its
absolute report links and any INCOMPLETE verdict.

If validation is INCOMPLETE, do not say "onboarding is complete up to human
action". Unknown CI guards, an unvalidated execution path or a failed required feature are
validation gaps; adding a Datadog key does not resolve them. Name those gaps
separately from the later human credential setup, and keep the generated verdict
as the opening sentence.
