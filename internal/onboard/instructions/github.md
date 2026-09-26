# Enable Datadog Test Optimization for __FRAMEWORK__ on GitHub Actions

Apply this setup to every GitHub Actions job that runs __FRAMEWORK__.

## 1. Instrument the test job

Add this step after checkout and dependency installation, immediately before the first test step:

```yaml
- name: Configure Datadog Test Optimization
  uses: datadog/test-visibility-github-action@v3
  with:
    languages: __LANGUAGE__
    api_key: ${{ secrets.DD_API_KEY }}
    site: datadoghq.com
```

If the organization uses a Datadog site other than US1, replace `datadoghq.com` with that site.

__BOOTSTRAP__

Jest CI discovery follows ordinary package.json script aliases, including nested scripts, using the workflow, job, or step working directory. Discovery only reads these files; it never executes scripts. Identified Jest steps are checked. Unresolved build, publishing, and documentation entry points are listed separately for review; inspect them if they also run tests. Unknown test wrappers, dynamic commands, unsupported shells, and unresolved working directories remain inconclusive. Identify their actual test steps without rewriting valid commands just to satisfy discovery.

Keep the existing test command and unrelated workflow content unchanged. Add the Datadog action once per test job, not once per test step.

## 2. Try it locally

Identify the actual test command from the package scripts and CI, including custom config paths and required setup. Preserve that command in CI. For local Jest validation, testdrive automatically selects a unique, statically resolved root-level CI command, preserving its script and options. If none is found, it tries the `test:ci` and `test` scripts before the runner default. Ambiguous commands, lifecycle hooks, and scripts that cannot forward Jest options safely require an explicit `--command` after reviewing setup. An explicit command overrides automatic selection. Do not silently drop a non-default config or necessary setup.

For Jest, start with `ddtest testdrive --check-only --yes` (and `--command` when needed). This executes Jest's `--showConfig`, but does not install a tracer or run tests. Review the detected Jest version, runner, Node version, and tracer selection. Fix known incompatibilities before the full run: older Jest may need a supported tracer major and a matching `jest-circus` runner. Avoid upgrading the framework or dropping CI coverage merely to use the newest tracer.

`--tracer-version <release-or-tag>` selects the fallback tracer. An existing project tracer takes precedence, which the report states explicitly. Otherwise ddtest resolves the selector once and installs that exact version in a temporary directory. Use the resolved version in the action's `js-tracer-version` input to validate the same tracer locally and in CI. There is no universal tracer version to copy into every repository.

Run the full local, credential-free validation with the same command and tracer selection. Run it sequentially: wait for testdrive to finish before starting lint, build, or another test run, because feature validation briefly creates a probe test in the repository:

```shell
ddtest testdrive
```

Testdrive reuses an existing project tracer. JavaScript and Python fallback installations are temporary and cleaned up afterward; do not add them to the project dependencies for local validation. Ruby fallback uses `bundle add datadog-ci` and retains its Gemfile and lockfile changes. It retains only `.testoptimization/testdrive.json`, updating the latest invocation and retaining at most one earlier paired Jest execution under `retained_execution.result`. Later configuration-only, unsupported-framework, or setup-failure runs preserve that evidence; a new paired Jest execution supersedes it. Retained evidence includes its original timestamp, tracer, commands, counts, and feature verdicts. It is explicitly historical and is never reused to declare the current invocation successful. The compact report records overall success, verdicts, test commands, modes, exit codes, and aggregate counts; it keeps a bounded failure-output excerpt for diagnosis, but no full logs or raw events. Keep this one report after cleanup, even when validation fails; do not delete `.testoptimization/testdrive.json`. Exclude `.testoptimization/` from source control and published packages; for npm projects, check `files`/`.npmignore` as well as `.gitignore`. Jest validation compares uninstrumented and reporting-only results, then checks features with temporary probe tests. Only the isolated probe commands override Jest coverage thresholds; full-suite comparisons retain the original thresholds, and coverage collection stays enabled for feature validation. The report records this adjustment. Generated Jest coverage is redirected to temporary session storage and cleaned up; existing customer coverage is preserved. Do not remove thresholds from the project configuration. Existing test failures are not automatically validation failures. Other frameworks are explicitly unvalidated.

For Jest projects, testdrive discovers a probe separately under each uniquely named project and records per-project feature results. A project whose probe cannot be discovered or selected remains unvalidated; do not generalize another project’s passing features to it. Preserve the existing Jest configuration when validation exposes a tracer limitation.

For Jest, testdrive also checks GitHub Actions Node versions against the tracer selected by each Datadog action, using its explicit version or the default from that action ref. It follows repository-local composite actions, including nested setup steps and literal/default inputs, while preserving their order and conditions. Unknown expressions remain inconclusive. Recognized other test frameworks and build tools are listed separately from Jest; opaque scripts that may run tests require review. It reads public GitHub and npm metadata; it does not run CI. setup-node LTS aliases (such as `lts/*`) resolve to a major using the public setup-node version manifest, with the source and resolution time recorded. Cached LTS patch versions remain unknown, so patch-specific requirements can remain inconclusive. The aliases `current`, `latest`, and `node` resolve to the newest release for the hosted runner platform and architecture using the public Node distribution index; the selected version, platform, source, and time are recorded. Unknown platforms or unavailable metadata remain inconclusive. Literal JSON packaging transformations and npm publication without known lifecycle hooks are listed for separate review; hooks that may run tests remain unresolved. Other CI entry points listed for review are outside this Jest check. Fix known runtime incompatibilities. An inconclusive result caused by unsupported CI syntax, a dynamic matrix, or unavailable metadata is a tool limitation: preserve the workflow, explain the missing evidence, and leave that check unverified. Do not rewrite valid conditions merely to satisfy the checker, or inspect the binary to discover accepted syntax. Human/agent review may be reported separately; it does not turn an unverified programmatic check into a pass. After a CI-only edit, use `--check-only` to recheck configuration without repeating the suite; this marks current test execution as not exercised while preserving the earlier paired Jest run in the same report. Report its earlier verdict and blockers separately. Retained evidence is not revalidated: after changes to the test command, configuration, source, dependencies, runtime, or tracer, run full validation again before claiming current compatibility or feature success. Preserve existing test coverage: unsupported runtime entries can remain uninstrumented, or the user can choose a compatible runtime/tracer. Do not assume the action will resolve runtime incompatibilities.

After it finishes, reproduce the generated `Validation verdict` and blocking checks without upgrading their status. The JSON `summary` comes from the same verdicts. These local feature checks use a mock backend and do not require a real Datadog API key: do not dismiss a failed local scenario as missing backend connectivity or promise that adding credentials will fix it. A skipping path mismatch is diagnostic evidence to investigate, not permission to change Jest roots or test discovery merely to make validation pass.

Also share local compatibility, each feature result, static CI runtime compatibility, local/CI tracer agreement, and the Results JSON path separately. Actual CI execution and backend processing remain not exercised by testdrive. Preserve inconclusive results; do not describe receiving telemetry as proof of compatibility. Do not declare validation complete while any required check is failed or inconclusive.

## 3. Ask a human to connect Datadog

The API key must be created and added to GitHub by a human. Ask the human to:

1. Follow the site-neutral [Datadog API key instructions](https://docs.datadoghq.com/account_management/api-app-keys/#add-an-api-key-or-client-token) and create the key in the selected Datadog site.
2. Add it to the GitHub repository as a secret named `DD_API_KEY`.
3. Tell you when the secret is ready without sharing the key itself.

Do not ask the human to paste the API key into chat, and do not try to create or read the secret yourself. After the human confirms it is ready, commit and push the workflow change. The GitHub Actions run verifies the real Datadog backend connection.
