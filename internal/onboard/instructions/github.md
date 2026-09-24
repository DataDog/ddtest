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

Keep the existing test command and unrelated workflow content unchanged. Add the Datadog action once per test job, not once per test step.

## 2. Try it locally

Identify the actual test command from the package scripts and CI, including custom config paths and required setup. Preserve that command in CI. For local validation, pass the Jest invocation and its config arguments through `--command`; do not silently drop a non-default config or necessary setup.

For Jest, start with `ddtest testdrive --check-only --yes` (and `--command` when needed). This executes Jest's `--showConfig`, but does not install a tracer or run tests. Review the detected Jest version, runner, Node version, and tracer selection. Fix known incompatibilities before the full run: older Jest may need a supported tracer major and a matching `jest-circus` runner. Avoid upgrading the framework or dropping CI coverage merely to use the newest tracer.

`--tracer-version <release-or-tag>` selects the fallback tracer. An existing project tracer takes precedence, which the report states explicitly. Otherwise ddtest resolves the selector once and installs that exact version in a temporary directory. Use the resolved version in the action's `js-tracer-version` input to validate the same tracer locally and in CI. There is no universal tracer version to copy into every repository.

Run the full local, credential-free validation with the same command and tracer selection. Run it sequentially: wait for testdrive to finish before starting lint, build, or another test run, because feature validation briefly creates a probe test in the repository:

```shell
ddtest testdrive
```

Testdrive reuses an existing project tracer. JavaScript and Python fallback installations are temporary and cleaned up afterward; do not add them to the project dependencies for local validation. Ruby fallback uses `bundle add datadog-ci` and retains its Gemfile and lockfile changes. It retains only `.testoptimization/testdrive.json`, replacing the previous report on each run. The compact report records overall success, verdicts, test commands, modes, exit codes, and aggregate counts; it does not retain raw output or events. Exclude `.testoptimization/` from source control and published packages; for npm projects, check `files`/`.npmignore` as well as `.gitignore`. Jest validation compares uninstrumented and reporting-only results, then checks features with temporary probe tests. Existing test failures are not automatically validation failures. Other frameworks are explicitly unvalidated.

For Jest, testdrive also checks GitHub Actions Node versions against the tracer selected by each Datadog action, using its explicit version or the default from that action ref. It reads public GitHub and npm metadata; it does not run CI. Fix known runtime incompatibilities. An inconclusive result caused by unsupported CI syntax, a dynamic matrix, or unavailable metadata is a tool limitation: preserve the workflow, explain the missing evidence, and leave that check unverified. Do not rewrite valid conditions merely to satisfy the checker, or inspect the binary to discover accepted syntax. Human/agent review may be reported separately; it does not turn an unverified programmatic check into a pass. After a CI-only edit, use `--check-only` to recheck configuration without repeating the suite; this replaces the report and explicitly marks test execution as not exercised. Run full validation again for a final combined report. Preserve existing test coverage: unsupported runtime entries can remain uninstrumented, or the user can choose a compatible runtime/tracer. Do not assume the action will resolve runtime incompatibilities.

After it finishes, share local compatibility, each feature result, static CI runtime compatibility, local/CI tracer agreement, and the Results JSON path separately. Actual CI execution and backend processing remain not exercised by testdrive. Preserve inconclusive results; do not describe receiving telemetry as proof of compatibility.

## 3. Ask a human to connect Datadog

The API key must be created and added to GitHub by a human. Ask the human to:

1. Follow the site-neutral [Datadog API key instructions](https://docs.datadoghq.com/account_management/api-app-keys/#add-an-api-key-or-client-token) and create the key in the selected Datadog site.
2. Add it to the GitHub repository as a secret named `DD_API_KEY`.
3. Tell you when the secret is ready without sharing the key itself.

Do not ask the human to paste the API key into chat, and do not try to create or read the secret yourself. After the human confirms it is ready, commit and push the workflow change. The GitHub Actions run verifies the real Datadog backend connection.
