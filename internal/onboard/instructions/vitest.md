# Enable Datadog Test Optimization for Vitest on GitHub Actions

Keep the existing tests, CI matrix, setup and coverage options. Work in this order:

1. Review the required configurations and CI consumers printed above, including Release jobs and their build prerequisites. Use `ddtest testdrive --all --check-only --yes` to inspect every configuration. For a focused diagnostic only, select an explicit command:

   ```shell
   ddtest testdrive --command "pnpm vitest --coverage" --check-only --yes
   ```

   Preserve the original test options; `vitest bench` is a separate, unvalidated mode and must not be instrumented on the strength of these test results. Preflight resolves the framework configuration, local Node and an exact tracer release without installing it or running tests. If metadata fails, fix registry/sandbox access and retry; do not guess an older version from cached web pages or the action default.
2. Add or update the action before the first Vitest step in every test job. Pin the exact resolved release in `js-tracer-version` and use it with local `--tracer-version`. The action default may lag npm. Existing project dependencies take precedence; changing them requires an explicit dependency decision.

   ```yaml
   - name: Configure Datadog Test Optimization
     uses: datadog/test-visibility-github-action@v3
     with:
       languages: js
       api_key: ${{ secrets.DD_API_KEY }}
       site: datadoghq.com
       js-tracer-version: <resolved-release>
   ```

   Confirm the customer's Datadog site before connecting CI; US1 is the default above. Merge **both** loaders into the existing Vitest step, preserving any current Node options:

   ```yaml
   env:
     NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}
   ```

   Check the tracer against every affected CI Node version. Prefer the preflight recommended candidate covering the whole existing CI matrix before excluding runtimes. Recommendations use current release metadata and framework prerequisites; they do not change dependencies or prove feature support until the live scenarios pass. Local Node compatibility alone is insufficient. Keep unsupported matrix entries running their original tests without instrumentation and report that scope. Preserve valid CI syntax; unresolved static checks remain unverified even after human review.
3. Finish all intended edits, including excluding `.testoptimization/` from Git and published packages (`files`/`.npmignore`). Rerun `--check-only --yes` with the same `--command` and `--tracer-version <resolved-release>`. Fix concrete blockers before the full run.
4. Finish with `ddtest testdrive --all --tracer-version <resolved-release> --yes`. Listed preparation runs in separate temporary copies; setup failure blocks that configuration. `--check-only` skips execution. All configurations share one HTML/JSON pair. Use `--command` only for focused diagnostics. Wait for completion before other commands. Further edits require a full rerun; do not finish with check-only or copy reports into separate folders.

## Validation scope

Version support follows the selected dd-trace release; ddtest does not impose a separate major-version allowlist. Known tracer incompatibilities are checked before execution. Configuration discovery, paired execution and controlled scenarios must still succeed; a preflight pass alone is not validation. This adapter currently handles one Node project using forks or threads, without Vitest typecheck mode. Multiple projects, browser mode and other pools remain explicitly unvalidated; do not change the customer's configuration merely to pass validation. If ddtest cannot inspect the configuration or read runner results, report that specific tool limitation.

Testdrive compares baseline and reporting-only outcomes with behavior-changing features disabled. Matching existing test failures are acceptable. It then checks retries, early flake detection, skipping, quarantine, disabled tests and attempt-to-fix using temporary probes under the project configuration. Only probe coverage thresholds are relaxed; full-suite thresholds stay intact. Tests use a local mock backend and require no real Datadog credentials. A local feature failure is not fixed by adding an API key.

## Cleanup and final response

Testdrive uses temporary tracer installations, probes and coverage storage; it reuses an existing tracer without changing dependencies. Keep `.testoptimization/report.html` and `.testoptimization/testdrive.json`, including after failures. No raw event archive is needed. Preparation copies are removed automatically. Remove only outputs created by your own commands; preserve pre-existing files and customer edits. Keep scratch files/caches under a unique temporary directory and remove that exact directory afterward. Verify Git and package exclusions and report anything that could not be cleaned up.

Start your final reply by copying `summary.final_response` from the latest JSON verbatim, including its absolute report links. Then list workflow edits and cleanup. Do not add a preamble such as “Onboarding is done.” INCOMPLETE means onboarding is incomplete even if workflow edits are finished. Preserve failed-check explanations; do not invent a cause or attribute them to missing credentials. Check-only evidence is historical. Unvalidated configurations remain untested.

## Ask a human to connect Datadog

After local validation, ask the human to follow the [Datadog API key instructions](https://docs.datadoghq.com/account_management/api-app-keys/#add-an-api-key-or-client-token), create a key in the selected site, add it as the GitHub secret `DD_API_KEY`, and confirm it is ready without sharing the key itself. Do not create/read the secret or request it in chat. After confirmation, commit and push the workflow change. Actual CI execution and Datadog backend processing remain untested until that workflow runs.
