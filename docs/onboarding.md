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
