# Apply JavaScript CI setup

Plain `ddtest onboard` prints instructions without editing files. To review the
exact proposed changes for a recognized GitHub Actions setup, run:

```sh
ddtest onboard --framework vitest --apply
```

The preview inspects configuration and resolves tracer/runtime metadata, but does
not install a tracer, write source files or execute tests. Then apply the reviewed
diff and run full local validation:

```sh
ddtest onboard --framework vitest --apply --yes
```

Use the selected framework name for Jest, Mocha, Playwright, Cucumber or
WebdriverIO. Cypress hooks still require explicit setup. Python and Ruby retain
their existing instructions and testdrive behavior; automatic apply is JavaScript
only.

The editor preserves existing project and CI tracer selections, including tracers
installed in workspace packages. It resolves one exact release for the actual
commands, pins newly added Datadog actions to a verified v3 release commit, and
adds framework initialization and report exclusions. Existing compatible v3 tags
and verified v3 release SHAs are accepted. It does not rewrite lockfiles or create
an `.npmignore` that would change npm's `.gitignore` fallback.

Every workflow is parsed and checked again before writing. Unsupported YAML,
custom initialization, conflicting versions, Windows runners and unknown
commands produce review gaps instead of guessed edits. A reusable
(`workflow_call`) workflow is edited only when it declares
`on.workflow_call.secrets.DD_API_KEY` and every caller in the repository passes
it (`secrets: inherit` or `secrets: {DD_API_KEY: ...}`); callers in other
repositories can't be checked. npm's own options (`--if-present`, `--silent`,
`-w`/`--workspace`, `--prefix`) and a step's `working-directory` are resolved
like a `cd` into that package.
Runtime partitions use the Node version active at each test step. Legacy rows
keep their original command without instrumentation; ESM initialization also
requires Node's registration API (18.19 or 20.6 and later supported releases).

New actions allow installation failure without failing the test command. The
preload is conditional on the action exporting a tracer; otherwise the original
Node options remain. This keeps fork and Dependabot pull requests runnable when
secrets are unavailable. Those runs do not send Test Optimization data. Existing
explicit action failure policies require review instead of being overwritten.

Target files with uncommitted changes are refused. Commit or stash them first,
or explicitly use `--allow-dirty --apply --yes` after reviewing the diff. Only the
listed target files are checked. Unrelated local edits do not prevent apply.
All replacements are staged before writing. If a write fails, earlier writes
are rolled back; any restoration error names the affected file. A second apply
with unchanged inputs makes no source edits and reruns validation.

Validation failures leave successful setup edits in place and explicitly list
them. The output includes an executable `git apply -R` command for the exact
setup diff. It reverses the setup edits while preserving pre-existing changes;
review it before executing it if you have made further edits since apply.

## Coverage and local developer commands

Mocha CI commands without Istanbul coverage are wrapped with the project's NYC.
Cucumber and WebdriverIO initialization is applied only to the selected literal
package-script leaves, including lifecycle hooks. Forwarding aliases and other
frameworks remain unchanged.

Cucumber edits add NYC coverage and set
`DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false` in the selected scripts.
These changes also affect developers running those scripts locally, including
local `npm test`; the variable disables impacted-test detection on those runs.
Other feature controls and testdrive's isolated probes remain separate.

Cucumber ESM coverage also needs `@istanbuljs/esm-loader-hook`. Missing coverage
dependencies produce an exact install command for npm, pnpm or Yarn. Review and
run that command separately, then rerun onboarding. For example:

```sh
npm install --save-dev --no-audit --no-fund nyc@17.1.0
```

Windows runner jobs are never edited: the action's tracer installation and the
preload are unverified there, so they need manual initialization. Scoped
Cucumber/WebdriverIO package preambles also use POSIX shell syntax; Windows
developers using `cmd.exe` need portable hooks before using these edited scripts.

Keep `.testoptimization/testdrive.json` and `report.html`. They distinguish local
validation from actual CI execution and Datadog processing, which are not proven
by these local checks. `--site` selects the site for a new action (default
`DD_SITE`, then `datadoghq.com`); `--tracer-version` is a fallback when no project
or CI version is selected.

### Agent coordination and fresh results

Use the same short request for a coding agent:

> Use ddtest to onboard this repository to test-optimization for vitest only.

JavaScript `onboard` shows a short next command in a terminal and returns a versioned next action when piped or requested with `--agent`/`--format json`.
Output format rule: plain `onboard` is the only command that looks at the terminal, so a piped `ddtest onboard` gives a coding agent its JSON handoff.
`testdrive --report` always renders the saved result as structured output (`--format agent` by default, or `--format json`). Plain `onboard --apply --yes`
and plain `testdrive` print text only; the agent's final response is in `summary.final_response` of the JSON report and in `--agent` results, not in plain output.
`onboard --apply --yes` applies recognized setup and runs the shared coordinator.
For existing setup, use `testdrive --framework vitest --all --agent --yes`.
Plain testdrive keeps its earlier execution behavior; explicit `--agent` or
`--format json` selects coordination. Supplying `--facts` also selects the coordinator. Structured execution requires `--yes` and never opens an interactive prompt. Python/Ruby instructions and plain execution
remain unchanged; structured validation is unsupported and returns no next command.

The coordinator owns one resolved execution plan: actual commands, directories,
environments, builds, runtime/tracer selection, initialization and feature policy.
A run cannot omit unresolved configurations to earn a complete verdict. CI rows with a literal `DD_CIVISIBILITY_ENABLED=false` or a verified skipped conditional Datadog installer remain visible as `excluded`: their original CI tests are preserved, but local paired execution and feature validation are not claimed. These opt-outs are counted separately from passed configurations. Unsupported or unavailable variants without an explicit opt-out remain incomplete. Checks
include source preservation, Git/npm report exclusions, owned process/temp cleanup,
and current execution evidence. The npm package check uses
`npm pack --dry-run --ignore-scripts --json`; it neither publishes nor runs hooks.
Checks and source-edit hashes stay in the bounded JSON; full output stays in HTML.

`onboard --apply` selects a tracer every CI row it instruments can run: when the default release needs a newer Node than a CI job provides, it picks the verified release that supports all of them, or stops and names the rows that need different majors. When a CI path, such as the Cypress action API, runs on a Node major the local host lacks, ddtest validates on the local Node if both the framework and the tracer support it, and records the substitution in `runtime_substitution`. That evidence completes only with `--accept-substitute-runtime`, a decision for the user; otherwise the result is `needs_input` (`substitute_runtime_unaccepted`) with the accepting resume command. When the local Node is unsupported, the result is `blocked_environment` (`local_runtime_mismatch`) naming the CI runtime. Either way, an existing installation of the CI runtime first on PATH validates without substitution; ddtest never downloads a runtime.

Only CI that can reach a selected test is in scope. Jobs whose steps can't start
one (system packages, coverage merging, linting, publishing, another framework)
are listed as not a test consumer, and unresolved steps after a job's last
possible test are listed as outside the test path; unknown executables, actions,
package scripts and repository files still block, and so do interpreter
invocations (`bash -c`, `node -e`, a script file) and npm settings that change
how scripts execute (`ignore-scripts`, `script-shell`). A job guard that reads only
runtime context (`github.actor`, `github.event_name`, `github.run_number`,
`needs.<job>.outputs`) is validated as a possible CI path and reported as
unverified; only a guard that is false for every outcome excludes the job. A matrix axis computed
at runtime (`fromJson(...)`) that only step commands read becomes one unverified
value, so the job's tests run with the locally installed dependencies. Plain argv
commands under `shell: pwsh`, including the Windows default, are read as POSIX
commands; PowerShell syntax and multi-line test steps remain unresolved.

Automatic Mocha tracer recommendations use the verified major-5 and major-6 bootstrap contracts. Exact releases still come from registry tags; obsolete Node tags cannot win by covering more legacy CI rows. An explicitly selected or installed unverified major remains unvalidated instead of being silently replaced.

Agents edit only CI workflow and ignore files, where a result asks for it. Any other
change ddtest needs, such as coverage instrumentation, loaders or dependencies, is
returned as an exact edit for the user to approve. For Cypress without application
coverage, a recognized browserify, Vite or webpack build gets that edit in the run's
`coverage.edit` (install command, files and how to serve the instrumented build);
other builds get the instrumentation guidance. Agents never rename tests or
rewrite CI or package scripts so ddtest can parse them; they report the blocker.
A final result tells the agent to relay `final_response` verbatim. An incomplete or
failed verdict starts with `NOT COMPLETE:`, so a paraphrase cannot turn it into a
completion claim.

When the host runs out of open file descriptors while isolating the checkout,
the result is `blocked_environment` (`file_descriptors_exhausted`) with
`next_action: run_command`: free descriptors and rerun the same command.

Results distinguish ready, needs_input, blocked_environment, unsupported,
validation_failed, selected_passed and complete_locally. Follow the returned argv
without guessing a different framework, tracer or suite. Missing capability and
setup facts are distinct from failed test assertions. Registry transport may retry
once; authentication, permissions and feature failures are not silently retried.

A checkpoint caches successful preparation only. `--resume <run_id>` always runs
fresh tests and invalidates preparation after source, runtime, tracer, facts or
binary changes. Three identical failures stop with a reportable blocker. Temporary
coordination lives outside the checkout, with a lock preventing concurrent runs. Locks are released and removed; expired private checkpoints and stale owned sessions are reclaimed at startup. Repeating the same failed input/scope shares one bounded attempt counter even without `--resume`, whichever failure code each attempt returns. A rejected `--facts` file, stale facts or an invalid `--resume` runs no tests and leaves an earlier report untouched. A `supply_input` or `report_result` response does not offer a blind retry command.

When repository-specific setup needs help, save the returned input template outside
the checkout and use `--facts /absolute/path/setup.json` with `--all`. Facts bind to
an input fingerprint covering the whole Git checkout (including nested package changes) and a configuration ID, can add validated local build argv,
expected output files, services/readiness and documented port overrides, and cannot
remove commands or change assertions. A template for a pending build prerequisite pre-fills `expected_files` with the git-ignored files its package.json entry points name; ddtest never runs the prerequisite to find them, so review the list before supplying it. Services run in disposable copies with bounded
readiness and cleanup. Their lifetime follows the configuration context, while each test/build command retains its own timeout. Live ownership leases stop declared services if the coordinator is killed. `--report --detail plan` or `--detail <configuration_id>`
shows details capped at 1 MiB; add the returned resume ID for an unfinished plan.
Unknown local Node build scripts in CI, including `npm run build && npm test`
and separate build steps, produce a template naming the original prerequisite.
Facts must declare its expected outputs; the command stays visible in the plan.
Unknown test runners, shell conditions and unrelated commands cannot be erased.
Known and fact-declared prerequisites preserve their original CI order. Rejected replacement facts do not overwrite accepted setup. Only facts schema 2 is accepted. Result schema 2 and plan schema 1 are separate
formats with independent versions.

Owned instrumentation edits carry validated facts forward. A dependency-install
handoff changes manifests/lockfiles, so ddtest returns `facts_refresh_required`
with a template for the new fingerprint. Review and save it outside the checkout,
then resume. Arbitrary source changes never silently inherit a previous binding.

After execution, follow `next_command` to `testdrive --report`. It verifies current
source/runtime inputs, installed tracer and a private result digest bound to this ddtest binary, then renders `final_response`, including retained absolute
report links. Both `--agent` and `--format json` emit exactly one JSON object on stdout, including errors. Only ordinary verbose execution prints progress/logs. `--format json` lets a host display the response directly. Changed inputs,
stale results cannot produce a fresh completion claim. A later check-only attempt retains the full run’s binding; it does not create new execution evidence. Editing the public report cannot certify it or inject a saved recovery executable. Saved command options and `--report --detail plan` from the report are used only after receipt verification. Missing/expired private receipts require full validation again. A plain `testdrive` run replaces the report without the workflow checks or binding, so `--report` returns `plain_report` with the workflow command to run; a check-only run keeps the earlier binding and plan.
Do not run extra cache audits or rewrite the verdict. Actual CI execution and
Datadog backend processing remain untested.
