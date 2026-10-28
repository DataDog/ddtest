# Enable Cucumber Test Optimization

Validate Cucumber only. Preserve other frameworks, all existing CI matrix entries,
feature paths, profiles, support imports and test commands.

1. Run `ddtest testdrive --all --check-only --yes`. Review the discovered Cucumber
   commands, build prerequisites, runtime compatibility and tracer recommendation.
   This command installs nothing and executes no tests or builds. Choose one exact
   recommended release covering the original CI matrix; pin it in both
   `js-tracer-version` and local `--tracer-version`. The action default can differ
   from npm latest. Do not drop Node versions or upgrade Cucumber just to pass.
   Cucumber Test Impact Analysis needs Istanbul coverage of application source.
   If NYC is missing, add it as a development dependency. For native ESM, also
   add `@istanbuljs/esm-loader-hook`. Configure the Istanbul loader in the existing ESM command or scoped Node
   options. Testdrive preserves that setup in both modes; it does not inject a
   loader merely because it is installed, or add NYC around an existing c8 script. Preserve an existing
   NYC invocation and its configuration. Config/test-file coverage alone cannot
   complete validation.
2. Add `datadog/test-visibility-github-action@v3`, with `languages: js`, the exact
   `js-tracer-version`, `api_key: ${{ secrets.DD_API_KEY }}` and the chosen `site`,
   after dependencies and before each Cucumber consumer. Initialize before Cucumber
   loads using both `-r ${{ env.DD_TRACE_PACKAGE }}` and
   `--import ${{ env.DD_TRACE_ESM_IMPORT }}`. Preserve existing Node options.

   If `npm test` also runs Vitest or another framework, do not set its global
   NODE_OPTIONS. Scope initialization to the existing Cucumber leaf script:

   ```json
   "test:bdd": "NODE_OPTIONS=\"${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-}\" nyc cucumber-js features"
   ```

   Preserve the original script's arguments, paths and other environment values.
   A script that sets its own source loader, such as
   `cross-env NODE_OPTIONS="--import tsx" cucumber-js`, keeps it after the
   inherited options: `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-} --import tsx" cucumber-js`.
   When `ddtest onboard --apply` can edit the job's CI, it makes this edit for
   well-known loaders (tsx, ts-node, @babel/register); when it reports a
   blocker instead, make exactly this edit. Other modules set in NODE_OPTIONS
   still need review.
   On its existing CI test step set:

   ```yaml
   env:
     DD_CUCUMBER_NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }} --experimental-loader @istanbuljs/esm-loader-hook
     DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED: 'false'
   ```

   The Istanbul loader is needed for native ESM source. Omit that loader for a
   CommonJS-only project. Keep NYC on the Cucumber leaf, including in CI; local
   validation cannot enable CI coverage by itself. Respect existing coverage
   thresholds for the full suite; ddtest disables thresholds only for probes.

   The impacted-test workaround is separate from Test Impact Analysis skipping.
   Keep it disabled for Cucumber while validating the existing tracer integration.
   `ddtest onboard --apply` sets it in the selected package scripts instead of the
   step; either place disables it for CI, and the scripts also cover local runs.
3. Finish CI and Git/package report exclusions, then run
   `ddtest testdrive --all --tracer-version <exact-release> --check-only --yes`.
   Fix concrete runtime, missing initialization and mixed-framework scope errors.
   Preserve unsupported syntax as unverified; do not rewrite valid CI conditions
   merely to satisfy the parser. Local validation needs no credentials.
4. Run `ddtest testdrive --all --tracer-version <exact-release> --yes` and wait for
   completion. It replays known builds in a temporary repository copy, compares
   native scenarios without and with reporting-only instrumentation, checks event
   agreement and runs six controlled features. Matching pre-existing test failures
   are acceptable. An inconclusive comparison or feature is still INCOMPLETE.
   Isolated probes use an empty generated config, reuse the resolved support
   imports, World and unconditional hooks, and exclude customer step definitions
   so catch-all steps cannot intercept controls. Tagged hooks remain checked by
   the actual paired suite, rather than inheriting customer tag filters in probes.
   Cucumber testdrive requires version 8 or later; preserve older frameworks and
   report that validation limit. Parallel worker coverage/features that the
   selected tracer does not report remain explicitly unvalidated.
   Use `--command` only for a focused diagnostic, preserving the profile and setup.
5. Keep `.testoptimization/report.html` and `.testoptimization/testdrive.json`.
   ddtest removes its temporary copy, build outputs, tracer, native messages and
   probes. Do not manually build just for ordinary --all validation. If a separate
   diagnostic needs a build, inspect whether its outputs exist first and remove
   only outputs you create. An ignored directory is not evidence it pre-existed.

Complete edits before the final full run. A check-only invocation or further
edits after execution requires another full run. Open the latest JSON report and preserve its validation verdict in the final reply.
Include the absolute `Open report:` links printed by ddtest. Then describe workflow edits and independently verified
cleanup. Never upgrade INCOMPLETE or dismiss missing local checks as missing real
Datadog credentials. Real CI and Datadog backend processing were not exercised.

## Ask a human to connect Datadog

After local validation, ask the human to create a key following the
[Datadog API key instructions](https://docs.datadoghq.com/account_management/api-app-keys/#add-an-api-key-or-client-token),
add the GitHub secret DD_API_KEY in the chosen site, and confirm without sharing
the key. Do not read or create the secret. Commit/push onboarding edits only after
that confirmation.
