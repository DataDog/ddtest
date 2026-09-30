# Enable Datadog Test Optimization for __FRAMEWORK__ on GitHub Actions

Apply this setup to every GitHub Actions job that runs __FRAMEWORK__.

For Jest and supported Vitest projects, do the work in this order:

1. Inspect the actual test command and run `ddtest testdrive --check-only --yes` to resolve the framework, the local Node runtime and a tracer candidate. This does not install the tracer or run the suite. Use any generated recommendation instead of guessing older tracer majors.
2. Configure CI below, pinning the exact recommended/resolved release in `js-tracer-version` and using that same release with local `--tracer-version`. Review the recommendation's compatible, unsupported and unresolved CI runtimes. Keep unsupported matrix entries running their existing tests without instrumentation; unresolved entries remain unverified. Existing project dependencies take precedence and require an explicit dependency decision, not a silent replacement.
3. Complete workflow, dependency, Jest configuration and Git/package exclusion edits. Run `--check-only --yes` with the selected tracer and test command again. Fix concrete blockers before full validation; preserve and disclose unresolved tool limitations.
4. Run one full `ddtest testdrive --yes` with that same selection. Wait for completion, then clean up and report its generated verdict. Do not finish with another configuration-only check or edit validated inputs afterward; either requires a new full run.


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

Jest CI discovery follows ordinary package.json script aliases, including nested scripts, using the workflow, job, or step working directory and literal npm `--prefix` package directories. It inspects supported cache-output commands and commands executed by substitutions in metadata steps; echoed text alone is not evidence that a command is harmless. Discovery only reads these files; it never executes scripts. Identified Jest steps are checked. Unresolved build, publishing, and documentation entry points are listed separately for review; inspect them if they also run tests. Unknown test wrappers, dynamic commands, unsupported shells, and unresolved working directories remain inconclusive. Identify their actual test steps without rewriting valid commands just to satisfy discovery.

Keep the existing test command and unrelated workflow content unchanged. Add the Datadog action once per test job, not once per test step.

## 2. Try it locally

Identify the actual test command from the package scripts and CI, including custom config paths and required setup. Preserve that command in CI. For local Jest validation, testdrive automatically selects a unique, statically resolved root-level CI command. Supported literal && package scripts are expanded with options forwarded only to their single Jest invocation, preserving setup order, environment and Jest options. The executed command is recorded in the report. Recognized ESLint/Prettier commands still run in the full-suite comparison, but are skipped while the synthetic probe exists so its formatting does not block feature checks; those runs record `probe_lint_skipped`. Other setup remains in place. Check workflow environment such as TZ, LANG and test selection variables against the local environment; carry over required literal values consistently to both modes. Do not copy CI secrets or silently reduce the test suite. Unresolved commands elsewhere do not prevent a known invocation from running locally; they remain independent CI findings. If no known or unresolved CI invocation is found, it tries the `test:ci` and `test` scripts before the runner default. Ambiguous commands, lifecycle hooks, and scripts that cannot forward Jest options safely require an explicit `--command` after reviewing setup. An explicit command overrides automatic selection. Do not silently drop a non-default config or necessary setup.

For Jest, use `ddtest testdrive --check-only --yes` during the setup sequence above (and `--command` when needed). This executes Jest's `--showConfig`, but does not install a tracer or run tests. Review the detected Jest version, runner, Node version, and tracer selection. Fix known incompatibilities before the full run: older Jest may need a supported tracer major and a matching `jest-circus` runner. Avoid upgrading the framework or dropping CI coverage merely to use the newest tracer.

`--tracer-version <release-or-tag>` selects the fallback tracer. An existing project tracer takes precedence, which the report states explicitly. Otherwise ddtest resolves the selector once and installs that exact version in a temporary directory. Use the resolved version in the action's `js-tracer-version` input to validate the same tracer locally and in CI. There is no universal tracer version to copy into every repository.

After CI configuration checks and all intended edits, run the full local, credential-free validation with the same command and tracer selection. Run it sequentially: wait for testdrive to finish before starting lint, build, or another test run, because feature validation briefly creates a probe test in the repository:

```shell
ddtest testdrive --yes
```

Testdrive reuses an existing project tracer. JavaScript and Python fallback installations are temporary and cleaned up afterward; do not add them to the project dependencies for local validation. Ruby fallback uses `bundle add datadog-ci` and retains its Gemfile and lockfile changes. It retains `.testoptimization/report.html` using the upstream HTML renderer, plus `.testoptimization/testdrive.json`, updating the latest invocation and retaining at most one earlier paired execution under `retained_execution.result`. Later configuration-only, unsupported-framework, or setup-failure runs preserve that evidence; a new paired execution supersedes it. The top-level `last_execution` and `configuration_check` identify the evidence for the local and configuration statuses separately. Retained evidence includes its original timestamp, tracer, commands, counts, and feature verdicts. It is explicitly historical and is never reused to declare the current invocation successful. The compact report records overall success, verdicts, test commands, modes, exit codes, and aggregate counts; it keeps a bounded failure-output excerpt for diagnosis, but no full logs or raw events. Keep both reports after cleanup, even when validation fails. The HTML shows the latest instrumented suite findings; synthetic feature probes do not replace it. Use the JSON for compatibility, feature checks and the overall verdict. Configuration-only checks leave any previous HTML unchanged, so do not present it as a new execution. Do not delete either report. Exclude `.testoptimization/` from source control and published packages; for npm projects, check `files`/`.npmignore` as well as `.gitignore`. Jest validation compares uninstrumented and reporting-only results, then checks features with temporary probe tests. Only the isolated probe commands override Jest coverage thresholds; full-suite comparisons retain the original thresholds, and coverage collection stays enabled for feature validation. The report records this adjustment. Generated Jest coverage is redirected to temporary session storage and cleaned up; existing customer coverage is preserved. Do not remove thresholds from the project configuration. Existing test failures are not automatically validation failures. Frameworks without a validation adapter are explicitly unvalidated.

For Vitest, use the actual Vitest executable and its CI options with `--command` (for example `--command "pnpm vitest --coverage"`). Run any separate required setup first; do not pass a combined lint/typecheck/test script. Run `--check-only --yes` before editing and again after configuration changes, then finish with full validation. Pin the exact resolved tracer in both `js-tracer-version` and `--tracer-version`; the action default may lag npm. Version support follows the selected dd-trace release, with checks for known incompatibilities and no separate major-version allowlist. The adapter currently handles a single Node project using forks or threads, without Vitest typecheck mode. Multiple projects, browser mode and other pools remain explicitly unvalidated; preserve their configuration. Configuration discovery and live checks must succeed; report specific tool limitations if they cannot. The adapter compares native JSON results, verifies matching telemetry, and runs the six controlled feature scenarios under the original project configuration. Probe files and coverage reports are temporary; keep both final reports. CI initialization for Vitest requires both the action's `--require` preload and `--import` ESM loader.

For Jest projects, testdrive discovers a probe separately under each uniquely named project and records per-project feature results. A project whose probe cannot be discovered or selected remains unvalidated; do not generalize another project’s passing features to it. Preserve the existing Jest configuration when validation exposes a tracer limitation.

For Jest, testdrive also checks GitHub Actions Node versions against the tracer selected by each Datadog action, using its explicit version or the default from that action ref. It follows repository-local composite actions, including nested setup steps and literal/default inputs, while preserving their order and conditions. Static numeric matrix comparisons such as `matrix.node >= 22` and string membership such as `contains(fromJSON('["18.x", "20.x", "22.x"]'), matrix.node-version)` are supported. Unknown expressions remain inconclusive. A finding with code `missing_instrumentation` is a concrete configuration error: instrument the named workflow/job/step. It is not an unsupported-syntax limitation. Recheck every Jest entry point, including release workflows; keep intentionally excluded unsupported matrix entries explicit. Recognized other test frameworks and build tools are listed separately from Jest; opaque scripts that may run tests require review. It reads public GitHub and npm metadata; it does not run CI. setup-node LTS aliases (such as `lts/*`) resolve to a major using the public setup-node version manifest, with the source and resolution time recorded. Cached LTS patch versions remain unknown, so patch-specific requirements can remain inconclusive. The aliases `current`, `latest`, and `node` resolve to the newest release for the hosted runner platform and architecture using the public Node distribution index; the selected version, platform, source, and time are recorded. Unknown platforms or unavailable metadata remain inconclusive. Literal JSON packaging transformations and npm publication without known lifecycle hooks are listed for separate review; hooks that may run tests remain unresolved. Other CI entry points listed for review are outside this Jest check. Fix known runtime incompatibilities. An inconclusive result caused by unsupported CI syntax, a dynamic matrix, or unavailable metadata is a tool limitation: preserve the workflow, explain the missing evidence, and leave that check unverified. Do not rewrite valid conditions merely to satisfy the checker, or inspect the binary to discover accepted syntax. Human/agent review may be reported separately; it does not turn an unverified programmatic check into a pass. Use `--check-only` during setup to recheck configuration. Complete workflow, dependency, configuration and ignore-file edits before the final full testdrive. Finish with full validation, not another `--check-only`. The report separates `last_execution` (the most recent paired run, its timestamp and whether it is current) from `configuration_check` (this invocation). A later check-only run preserves the earlier execution as passed or incomplete earlier, but not rerun; it cannot certify changed inputs and leaves onboarding incomplete. Run full validation again after further edits before claiming current compatibility or feature success. Prefer a preflight recommended tracer covering the whole existing CI matrix before excluding runtimes. Preserve existing test coverage: unsupported runtime entries can remain uninstrumented, or the user can choose a compatible runtime/tracer. Do not assume the action will resolve runtime incompatibilities.

Copy `summary.final_response` from the latest JSON verbatim as the opening of your reply, without a completion preamble. Copy its absolute Markdown report links verbatim, each on its own line; do not shorten them to relative paths or replace them with `file://` URLs. Start with its verdict, with no cleanup preamble; keep the counts, clickable HTML/JSON links and cleanup status. Add workflow changes and your own cleanup checks afterward. Its opening paragraph is also stored in `summary.onboarding_response`. It includes the generated verdict, local status and runtime, CI status and runtime scope (instrumented, excluded and unverified entries), and blockers. Keep this scope in the opening response; a local pass does not mean every CI runtime is instrumented or exercised. Explain workflow changes and any historical execution evidence afterward. Do not replace `INCOMPLETE` with an opening claim that onboarding is complete, even when the edits or local tests succeeded. These local feature checks use a mock backend and do not require a real Datadog API key: do not dismiss a failed local scenario as missing backend connectivity or promise that adding credentials will fix it. A skipping path mismatch is diagnostic evidence to investigate, not permission to change Jest roots or test discovery merely to make validation pass.

Use the latest report's `summary.facts` for counts, repeated runs, concrete CI blockers and the report path. Do not mix earlier counts into the current verdict or claim that a crash is unrelated to instrumentation when the comparison remains inconclusive. A successful repeat does not erase an earlier unexplained crash.

Also share local compatibility, each feature result, static CI runtime compatibility, local/CI tracer agreement, and the HTML (`Open report`) and `Results JSON` paths separately. Actual CI execution and backend processing remain not exercised by testdrive. Preserve inconclusive results; do not describe receiving telemetry as proof of compatibility. Do not declare validation complete while any required check is failed or inconclusive.

## Cleanup after local validation

Avoid building the project unless its test setup requires it. Before any build or
other command that generates files, inspect existing output paths in command
stdout; do not write before/after inventory files. If you need scratch files,
create a unique directory under the supplied TMPDIR (or the system temporary
directory) and remove that exact directory with a shell trap or finally block.
Keep your caches and manifests there; avoid shared /tmp filenames. After
validation, remove only the outputs newly created by your commands (for
example new dist/ or lib/ files), plus your temporary manifests and caches. Do
not delete pre-existing customer outputs, restore over customer edits, or use a
blanket git clean. ddtest already cleans its own probes, tracer and coverage.
Keep the intended onboarding edits, .testoptimization/report.html and
.testoptimization/testdrive.json. Verify Git and package exclusions for both reports even when validation
is incomplete. Confirm cleanup before the final response; disclose anything that
could not be safely removed. Record a concise cleanup conclusion, not an inventory
of every dependency or telemetry event.

## 3. Ask a human to connect Datadog

The API key must be created and added to GitHub by a human. Ask the human to:

1. Follow the site-neutral [Datadog API key instructions](https://docs.datadoghq.com/account_management/api-app-keys/#add-an-api-key-or-client-token) and create the key in the selected Datadog site.
2. Add it to the GitHub repository as a secret named `DD_API_KEY`.
3. Tell you when the secret is ready without sharing the key itself.

Do not ask the human to paste the API key into chat, and do not try to create or read the secret yourself. After the human confirms it is ready, commit and push the workflow change. The GitHub Actions run verifies the real Datadog backend connection.
