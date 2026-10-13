# Enable Datadog Test Optimization for Playwright on GitHub Actions

Validate Playwright only. Keep other test frameworks and their workflows unchanged unless separately requested. A blocked Jest or Vitest suite does not prevent independent Playwright validation.

## Configure and validate

1. Review the Playwright CI commands and configurations listed above. Install the repository's dependencies and required Playwright browsers using its normal setup. Preserve the original Playwright config, browser projects, tests, CI matrix and test commands. Run `ddtest testdrive --all --check-only --yes`. Preflight lists tests without executing them and checks the local and CI runtimes against current tracer metadata. Resolve network/metadata failures rather than guessing a tracer version.
2. Add the action after dependency/browser setup and before the Playwright test step in each identified job:

   ```yaml
   - name: Configure Datadog Test Optimization
     uses: datadog/test-visibility-github-action@v3
     with:
       languages: js
       api_key: ${{ secrets.DD_API_KEY }}
       site: datadoghq.com
       js-tracer-version: <resolved-release>
   ```

   Prefer the recommended release covering the existing CI runtime matrix. Use the exact same release in the action and local `--tracer-version`; the action default may lag npm. An existing project tracer takes precedence and must not be silently replaced. Keep unsupported CI entries running their original tests and disclose exclusions. Confirm the Datadog site before connecting CI; US1 above is provisional.

   Merge initialization into the existing Playwright test step, preserving current Node options:

   ```yaml
   env:
     NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}
   ```

3. Finish workflow and ignore-file edits. Exclude `.testoptimization/` from Git and published packages; check `files`/`.npmignore` as well as `.gitignore`. Recheck with `ddtest testdrive --all --tracer-version <resolved-release> --check-only --yes`. Fix concrete blockers. Preserve unsupported CI syntax and describe it as unverified rather than changing valid workflows to satisfy the parser.
4. Run `ddtest testdrive --all --tracer-version <resolved-release> --yes` and wait for completion before other repository commands. The report aggregates all discovered Playwright commands. For an explicit local command, use `--command 'pnpm test:e2e'` instead of `--all`; this requires a single package alias that safely forwards Playwright options. Review separate required setup before execution.

## What validation proves

Full validation runs the existing Playwright configuration in a temporary repository copy. It compares native test identities, browser projects, statuses, failure details, attempts and exit codes without instrumentation and with reporting-only instrumentation. Matching existing failures are acceptable. It verifies local telemetry against native attempts. The collector replaces the CLI reporter equally in both modes, redirects test artifacts to temporary storage, and preserves the customer's other execution options.

Controlled probes check automatic retries, early flake detection, quarantine, disabled tests and attempt-to-fix separately under each selected project. Native retries remain intact for the full comparison; only probes use zero native retries so the tracer must supply them. Features without usable controls remain inconclusive. **dd-trace-js does not support Test Impact Analysis skipping or per-test coverage for Playwright.** Report those as unsupported, not failed or passed. This does not block completion of the supported checks. A local feature failure is not explained by missing real Datadog credentials: all checks use a local mock intake.

## Cleanup and final response

The temporary copy, probes, fallback tracer, browser artifacts and tracked server process groups are removed after validation. Existing files and reused customer servers are preserved. Do not install dd-trace in package.json just for local validation. Remove only outputs created by your own extra commands; use private temporary directories for scratch work.

Keep `.testoptimization/report.html` and `.testoptimization/testdrive.json`, including after failures, and verify their Git/package exclusions. The HTML describes the instrumented customer suite; JSON records commands, counts, comparisons and each feature verdict. No raw telemetry archive is needed.

Start the final response with `summary.final_response` from the latest JSON verbatim, including its absolute Markdown report links. Then describe workflow edits and cleanup. Preserve INCOMPLETE and all blockers; do not claim that receiving events proves compatibility. Report unsupported skipping explicitly. Complete any edits before the final full validation; a later check-only invocation or changed input requires a full rerun before claiming current success. Actual CI execution and Datadog backend processing remain not exercised locally.

## Ask a human to connect Datadog

After local validation, ask the human to follow the [Datadog API key instructions](https://docs.datadoghq.com/account_management/api-app-keys/#add-an-api-key-or-client-token), create a key in their chosen site, add it as the GitHub secret `DD_API_KEY`, and confirm without sharing the key. Do not read/create the secret or request it in chat. Commit and push the onboarding edits only after that confirmation. Local validation does not require credentials.
