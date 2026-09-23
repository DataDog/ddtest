# Keep JavaScript test discovery behavior unchanged

DDTest keeps fast and native discovery side by side. JavaScript skipping still
operates on whole test files. Test execution continues to use your framework's
configuration and command.

| Mode | How to select it | Behavior |
| --- | --- | --- |
| Automatic Jest discovery | Default, with no `--tests-location` | Analyze supported config in Go, then walk the selected paths. No Node process on the fast path. Unsupported analysis logs its reason and falls back to native Jest. |
| Explicit filesystem discovery | Set `--tests-location` | Use your complete include glob and `--tests-exclude-pattern`; no config loading or Node process. Available for all six JS frameworks. |
| Native framework discovery | `--force-full-test-discovery` | Use the original framework adapter. This takes precedence over fast discovery and retains native config, plugin, and runtime selection behavior. |

Vitest, Mocha, Cypress, Playwright, and Cucumber currently use filesystem globs
by default. Automatic AST config analysis is implemented for **Jest**. For the
other frameworks, use equivalent explicit globs or force native discovery when
your configuration changes the default file set.

The no-Node guarantee applies to fast file discovery. The CLI still runs its
existing Node/dependency prerequisite checks and runtime-tag collection during
planning, and test execution still starts the framework.

## What customers need to do

- **Jest with supported configuration:** keep your current configuration and
  command. Verify the file lists once. No duplicate glob settings are needed.
- **Jest with dynamic/unsupported configuration:** the default automatically uses
  native discovery. The warning explains why the fast path was unavailable.
  You can keep this behavior, or supply an equivalent explicit glob for speed.
- **Any JS framework with a discovery mismatch:** set the force flag, regenerate
  the plan, and retain your existing framework command. You do not need to
  downgrade DDTest or rewrite the configuration.
- **Other JS frameworks seeking VM-free planning:** use the default glob only
  when it describes the same file set as native discovery. Otherwise supply
  equivalent explicit globs using the examples below.

```sh
ddtest plan --framework jest \
  --command 'node_modules/.bin/jest --config jest.unit.js' \
  --force-full-test-discovery
```

The same setting can be shared through CI configuration:

```sh
export DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY=true
```

This existing flag now selects native file discovery for Jest, Vitest, Mocha,
Cypress, Playwright, and Cucumber. It does not turn suite skipping into
individual-test skipping. Native discovery errors fail planning; forced native
Vitest discovery does not silently return guessed globs if its legacy API fails.

Keep the planning and execution commands, working directory, dependencies,
generated files, and environment consistent. `ddtest run` consumes the saved
plan; changing discovery options requires another `ddtest plan`.

## What Jest config analysis understands

The analyzer reads `package.json`'s Jest configuration, JSON configs, and
JavaScript/TypeScript config syntax without running Node. Supported expressions
include static objects and arrays, constants, spreads, local imports/requires,
pure bounded config factories (including async factories without asynchronous
work), environment conditionals, and common `node:path` operations.

Discovery preserves `rootDir`, `roots`, `testMatch` order and re-inclusions,
`testRegex`, `testPathIgnorePatterns`, `modulePathIgnorePatterns`, module file
extensions, static presets, independent project configs, and supported CLI
config/path overrides. Defaults follow the detected installed Jest major
version. The analyzer uses its own discovery plan, so customers do not have to
translate native regexes or ordered glob arrays into DDTest's two glob flags.

Unsupported package code, mutations, getters, arbitrary calls, complex regexes,
negative extglobs, custom sequencers/crawlers, changed-file selection, and
unrecognized command options use native discovery. This is a deliberately
bounded analyzer, not an implementation of all JavaScript semantics. For
example, the pinned Immer fixture's `ts-jest` preset currently uses the native
fallback; the Day.js and Redux fixtures use static analysis.

Config and imported files are analyzed afresh on every planning invocation.
There is no persisted config cache to become stale after an environment,
configuration, or dependency change. Test bodies are not parsed by the fast
path. Native discovery may load test bodies, as before.

An explicit `--tests-location` bypasses Jest config analysis and defines the
complete candidate file set. A broad glob is therefore different from an extra
filter on native discovery. With the force flag, DDTest instead filters the
native result using your include/exclude options.

## Compare fast and native discovery

Use the same checkout, generated files, dependencies, environment, command, and
Datadog credentials for both plans. The artifact below contains files **before
Datadog skipping**, so backend skip changes do not obscure discovery differences.

```sh
ddtest plan --framework jest \
  --command 'node_modules/.bin/jest --config jest.unit.js' \
  --force-full-test-discovery
cp .testoptimization/runner/discovered-test-files.txt /tmp/native-files.txt

# Ensure the force setting is disabled, including any CI environment override.
ddtest plan --framework jest \
  --command 'node_modules/.bin/jest --config jest.unit.js' \
  --force-full-test-discovery=false
diff -u /tmp/native-files.txt .testoptimization/runner/discovered-test-files.txt
```

Expected: no differences. Check the discovery log too: an automatic native
fallback preserves the result but does not demonstrate fast-path coverage.
The plan report labels native discovery as `full`. For another framework, use
its name and command, adding equivalent globs to the second invocation.

Do not use `test-files.txt` as the reference for discovery: it already excludes
Datadog-skipped suites. Apply the same positional and include/exclude selection
to both runs.

Then validate execution with no skipping, partial skipping, all suites skipped,
and `@datadog unskippable` markers. Compare suite names, test counts, outcomes,
and snapshots; verify setup and lifecycle hooks. Empty assignments must not
start a test process. Compare optimized plans only with identical backend skip
data. If results differ, force native discovery and regenerate the plan while
the fast-path mismatch is investigated.

## Automated parity coverage

CI compares static and native Jest file lists directly across supported Jest
versions, config fixtures, and generated filename/pattern combinations. A
static-analysis error is visible to these tests: automatic fallback cannot hide
a mismatch. Pinned Day.js, Immer, and Redux checkouts also execute their original
tests with no, partial, and complete skipping. The original native adapter tests
remain alongside fast-path tests for the other JS frameworks.

## Optional: translate selection to explicit globs

| Framework | Previously evaluated during discovery | Explicit glob mode | What stays in execution config |
| --- | --- | --- | --- |
| Jest | `roots`, `rootDir`, `testMatch`, `testRegex`, `testPathIgnorePatterns`, `moduleFileExtensions`, presets, projects, CLI path filters | Include the selected projects' test files, using working-directory-relative paths; mirror ignores and extension restrictions. | Transforms, environments, setup files, globals, mocks, runner options, and selected projects. |
| Vitest | `root`, `dir`, `include`, `exclude`, workspace/projects, `includeSource`, CLI filters | Include ordinary and in-source test files that native discovery selects; exclude ignored/generated trees. | Vite plugins, aliases, setup, environments, projects, test-name filters. |
| Mocha | `spec`, `extension`, `recursive`, `ignore`, `--file`, package config, CLI paths | Include only runnable test modules; match recursive/nonrecursive scope and extensions; exclude shared `--file` setup. | Requires/loaders, hooks, `--file` setup, grep, retries, reporters. |
| Cypress | `specPattern`, `excludeSpecPattern`, testing type, project root, `setupNodeEvents` overrides | Use the final spec set for that testing type; component jobs must also exclude E2E specs where the old discovery did. | Plugin/support setup, browser options, testing type, project/config options. |
| Playwright | `testDir`, `testMatch`, `testIgnore`, selected projects, dependency/teardown projects, grep and `.only` | Include primary test files; exclude shared dependency/teardown files and reproduce the selected project scope. | Project dependencies, teardown, fixtures, browser settings, grep, retries, reporters. |
| Cucumber | Profiles, paths, tags/names, line selectors, rerun files, Gherkin collection | Include the same selected feature files, including `.feature.md` if used; match profile-specific directories. | Profiles, step loading, tags/names, language, formatters. |

Framework configs remain authoritative during execution. Choosing a path with
DDTest does not override a framework that refuses that path. Set both sides to
the same scope and verify execution, not just planning.

### Jest: manual override for roots and regex selection

Suppose Jest has `rootDir: 'packages/api'`, `roots: ['<rootDir>/checks']`,
`testRegex: '\\.check\\.ts$'`, and ignores `checks/fixtures`. From the repository
root, use:

```sh
ddtest plan --framework jest \
  --tests-location 'packages/api/checks/**/*.check.ts' \
  --tests-exclude-pattern 'packages/api/checks/fixtures/**'
```

Do not copy `<rootDir>` or JavaScript regex syntax into the glob. If planning
runs inside `packages/api`, use `checks/**/*.check.ts` instead. For several
projects, combine their patterns, for example
`'{packages/api/checks/**/*.check.ts,packages/web/tests/**/*.test.tsx}'`.

### Vitest: custom include and in-source tests

For `test.include: ['checks/**/*.check.ts']` and
`test.includeSource: ['src/**/*.ts']`, with an excluded `src/generated` tree:

```sh
ddtest plan --framework vitest \
  --tests-location '{checks/**/*.check.ts,src/**/*.ts}' \
  --tests-exclude-pattern 'src/generated/**'
```

If only some source files contain in-source tests, narrow the include pattern
to those files. The scanner does not inspect `import.meta.vitest`; including
all source files is not exact parity unless all are native test candidates.

### Mocha: nonrecursive tests and shared setup

For `spec: ['test/*.js']`, `recursive: false`, and `file: ['test/setup.js']`:

```sh
ddtest plan --framework mocha \
  --tests-location 'test/*.js' --tests-exclude-pattern 'test/setup.js'
```

Keep `file: ['test/setup.js']` in Mocha's config. Every worker still loads it.
For recursive suites, use `test/**/*.js`. DDTest's default Mocha glob is
recursive, so projects relying on Mocha's nonrecursive default need the explicit
`test/*.{js,cjs,mjs}` pattern.

### Cypress: component tests in a subproject

```sh
ddtest plan --framework cypress \
  --tests-location 'apps/web/src/**/*.cy.{js,jsx,ts,tsx}' \
  --tests-exclude-pattern 'apps/web/src/e2e/**'
ddtest run --framework cypress \
  --command 'pnpm exec cypress run --component --project apps/web'
```

Use the resolved component selection from your own config. If a directory is
symlinked, plan against its physical paths and ensure Cypress's configuration
accepts those same paths. A symlink-only layout requires adjustment; simply
copying the old symlink glob does not preserve discovery.

### Playwright: primary suites and shared lifecycle

```sh
ddtest plan --framework playwright \
  --tests-location 'apps/web/tests/**/*.spec.ts' \
  --tests-exclude-pattern '**/{setup,teardown,ignored}.spec.ts'
ddtest run --framework playwright \
  --command 'pnpm exec playwright test --config apps/web/playwright.config.ts --project chromium'
```

Keep the setup/teardown projects and their dependency declarations in Playwright
config. They run as shared lifecycle, not independent DDTest assignments. If
projects select different files, give each planning job the matching file scope.

### Cucumber: profile and tag selection

```sh
ddtest plan --framework cucumber --tests-location 'features/api/**/*.{feature,feature.md}'
ddtest run --framework cucumber --command 'pnpm exec cucumber-js --profile api --tags "not @slow"'
```

Tags still filter scenarios during execution. A feature whose scenarios are all
filtered out may now appear in the plan. For identical discovery, exclude those
feature files explicitly. DDTest partitions whole feature files, not individual
scenario lines; do not expect filename globs to implement line or rerun selectors.

## Glob syntax and cases that need more than a copied setting

- Quote globs so the shell does not expand them.
- Use `**` for directories, `*` within one path segment, and `{a,b}` for alternatives.
- Translate minimatch `*.@(spec|test).ts` to `*.{spec,test}.ts`, and
  `*.?(c|m)js` to `*.{js,cjs,mjs}`. JavaScript regexes, `<rootDir>`, and
  minimatch extglobs are not DDTest glob syntax.
- Move negated include patterns into `--tests-exclude-pattern`. There is one
  exclusion glob; combine exclusions with braces. An ordered pattern list that
  excludes and then re-includes files must be rewritten as an equivalent final
  include/exclude set; copying its order will not work.
- Mirror default framework exclusions too, such as generated/dist trees or
  extension restrictions, when those files exist in your repository.
- Retain environment-dependent selection as environment-specific CI globs.
  Discovery does not execute config functions, presets, or plugin hooks.
- In explicit glob mode, framework collection errors surface during execution. A syntax error in a
  test cannot be detected by reading its filename.

**There is no automatic parity guarantee for arbitrary executable selection.**
`--onlyChanged`, related-test dependency analysis, `.only`, grep/tags that remove
whole suites, dynamically generated test lists, and custom framework runners may
need a job-specific file set. If that set cannot be supplied without evaluating
the framework, VM-free discovery cannot reproduce its selection automatically.
Use `--force-full-test-discovery` for that job to retain native selection. Do not substitute a broad glob and assume that
passing tests prove equivalent behavior.
