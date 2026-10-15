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
4. Test Impact Analysis also needs Istanbul coverage from the application loaded
   in the browser. Inspect real-suite coverage counts. API-only tests may have no
   browser source coverage. If the application is not instrumented, add or reuse
   test-only instrumentation in its existing startup/build path, preserving
   production behavior, source paths and coverage thresholds. Use the same app
   coverage setup in the baseline and instrumented comparison. Synthetic probes
   validate feature behavior; they do not prove coverage of the real application.
   This coverage setup is part of onboarding: after a zero-coverage result,
   inspect the application build/server and implement a suitable test-only path
   before the final run. Use its existing Istanbul bundler integration, or for
   a static JavaScript app, instrument application scripts with
   istanbul-lib-instrument in a temporary served copy. Keep the ordinary server
   behavior when coverage is off. Instrument with the original repository
   source filename, not the temporary destination filename: coverage pointing
   to a temporary directory cannot map back to changed repository files.
   Enable this coverage path in CI's existing build/start step too; a local-only
   coverage alias is insufficient. Prefer an opt-in environment flag that keeps
   the existing CI commands and Cloud recording options. Pass that flag
   identically to both local modes. Include those flags in final verification
   of every discovered configuration. If setup is blocked, state the prerequisite you could not
   satisfy instead of leaving coverage as an unexplored follow-up.
   Set environment flags before ddtest, for example COVERAGE=true ddtest testdrive
   --framework cypress --all --tracer-version <exact-release> --yes. An explicit
   --command is for targeted diagnostics and takes an executable with arguments,
   not shell variable assignments; it does not validate the other CI paths.
   When adding a temporary coverage server, use asynchronous spawn and forward
   termination signals to the child, cleaning the copy when it exits. Signal
   callbacks cannot interrupt an execSync server; adding those callbacks can
   hang shutdown checks. Prefer verification through testdrive's managed startup
   and cleanup instead of a separate background preview server.
5. Run ddtest testdrive --framework cypress --all --tracer-version <exact-release>
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
6. Review both native runs, the telemetry comparison and all six controlled
   feature results. Use the report's compatibility.status; matching counts alone
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
action". Unknown CI guards, an unvalidated execution path or invalid coverage are
validation gaps; adding a Datadog key does not resolve them. Name those gaps
separately from the later human credential setup, and keep the generated verdict
as the opening sentence.
