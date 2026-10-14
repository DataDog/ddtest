## Onboard this Mocha repository

These are instructions for the coding agent: inspect the repository and make the
CI changes, then validate them locally. `onboard` itself only prints guidance.

1. Before any manual diagnostics, record `git status --short --ignored` so you
   can distinguish existing files from outputs you create. Prefer testdrive's
   disposable preparation; do not manually build or run tests in the customer
   working tree to prepare it. If independent diagnostics are necessary, use a
   disposable copy and a private temporary directory, then remove them. Do not
   write logs to shared paths such as `/tmp/build.log`.
   Inspect every discovered Mocha CI command, including CJS/ESM variants and
   coverage wrappers, npm pre/post hooks and sequential run-s aliases. A build command can run Mocha in postbuild and must be instrumented too. Preserve the test files, Mocha version, runtime matrix,
   reporters, requires, hooks, and coverage thresholds. Do not replace the real
   command with bare `mocha` to make validation pass.
2. Run `ddtest testdrive --all --check-only --yes`. Use its resolved tracer
   version and Node compatibility checks to choose one exact published release
   for both CI and local validation. The action default can lag npm latest;
   explicitly set `js-tracer-version`. Prefer the recommended compatible major
   before considering any runtime changes. Never remove matrix entries or test steps to satisfy tracer requirements. If the maintained tracer cannot cover old runtimes, preserve their runtime/test coverage in uninstrumented steps with `DD_CIVISIBILITY_ENABLED: 'false'`, instrument supported entries with conditions, and report the coverage limitation. Do not stop local validation for this CI limitation: run the full comparison and features on a supported local runtime using the same maintained release. No hardcoded tracer patch is required.
3. Add `datadog/test-visibility-github-action@v3` before every Mocha test consumer,
   using `api_key: ${{ secrets.DD_API_KEY }}`, the intended Datadog site,
   `languages: js`, and the exact `js-tracer-version`. Initialize the tracer in
   each affected test step with `NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }}`.
   Preserve existing Node options. ESM also needs `--import ${{ env.DD_TRACE_ESM_IMPORT }}`
   on a Node runtime supporting --import. Do not add secrets or contact a real backend locally.
4. Mocha per-test coverage uses Istanbul/NYC; c8 alone does not provide that
   instrumentation. If NYC is absent, add it as a project dev dependency using
   the existing package manager and lockfile convention. First follow package aliases: if the test command already uses NYC, keep that wrapper and do not add another. Only wrap commands lacking NYC; wrap the original CI
   test command with `npx --no-install nyc -- <original command>` (or the package
   manager's local executable equivalent), preserving existing c8/reporters and
   thresholds. Testdrive also uses project-installed NYC for its paired runs.
   Apply the printed CI coverage command to every consumer, including a build
   alias that invokes Mocha in postbuild. Wrapping that existing command with NYC
   is CI setup, not rewriting the build. Missing CI wrappers and missing real
   ESM coverage are separate findings; fix the wrapper even if coverage remains blocked.
   Installing or wrapping with NYC is not proof of coverage: inspect the real-suite
   coverage counts in JSON. `instrument: false`, include/exclude patterns, ESM
   sources, or generated bundles can leave the actual source uninstrumented.
   NYC's require hook alone does not instrument native ESM; dd-trace's ESM loader
   enables tracing, not Istanbul coverage. Synthetic skipping probes prove skip behavior only. Do not rewrite a build,
   change the test scope, or weaken coverage thresholds just to obtain a pass;
   report missing real-suite coverage as incomplete.
   Do not install dd-trace permanently just for validation: testdrive installs
   the selected version in temporary storage and removes it afterwards.
5. Exclude `.testoptimization/` from Git and published packages, respecting the existing `files`/`.npmignore` convention. After all edits, run `ddtest testdrive --all --tracer-version <exact-release> --yes`.
   It compares native Mocha results with and without tracing for each CI command,
   reconciles telemetry, and runs pass/fail controls plus retries, early flake
   detection, skipping, quarantine, disabled tests, and attempt-to-fix. The
   original command/configuration is preserved; only controlled probes select
   a temporary test file and relax full-suite coverage thresholds. For a chained package script, probes use its resolved Mocha entry point; companion lint and auxiliary Node checks still execute in both full-suite runs. Both full
   runs keep the original thresholds. Matching existing test failures are
   acceptable; missing results or changed outcomes are not a pass.
6. Address real setup or instrumentation failures and rerun the full command.
   Do not remove commands, weaken configurations, guess unsupported tracer
   versions, or substitute manual checks for an incomplete report. When a step mixes local Mocha with a separate cloud/browser command and secrets,
   review whether the credentials belong only to the cloud command. If so, split
   the step while preserving command order, conditions and cloud credentials;
   instrument the Mocha consumer and keep cloud execution outside local validation.
   Never silently drop an unresolved environment value. Opaque custom
   runners/configurations remain unvalidated when their scope cannot be resolved.

Testdrive runs Mocha in disposable copies, then removes probes, tracer installs,
coverage output and raw events. Keep only `.testoptimization/report.html` and
`.testoptimization/testdrive.json` as generated validation artifacts. Clean up any
additional files created manually, preserving CI/dependency edits. Before the
final response, compare `git status --short --ignored` with the starting state:
ordinary `git status --short` hides build and coverage output. Remove only your
new generated files and temporary logs, never pre-existing customer outputs.
A successful testdrive cleanup covers its own workspaces, not manual diagnostics. The HTML keeps
full command output; JSON contains bounded commands, counts and verdicts.

Use the latest full execution's `summary.final_response` verbatim for your final
verdict and copy its absolute Markdown report links, each on its own line.
Never claim completion if the report says INCOMPLETE. A final --check-only does
not certify execution after edits; finish with a full run. State that CI was
reviewed statically and real CI/Datadog backend processing was not exercised. Reports describe inputs at execution time, not later edits. If you edit CI after a run, its verdict is stale: rerun the full validation before your final response.

## Ask a human to connect Datadog

After local validation, ask the human to add `DD_API_KEY` as a GitHub secret and
confirm the intended Datadog site without sharing the key. Do not create/read
secrets or push the customer repository as part of local validation.
