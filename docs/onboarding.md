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
WebdriverIO. Cypress hooks still require explicit setup. Python retains its
existing instructions and testdrive behavior. Ruby supports plain and coordinated
RSpec/Minitest validation as described below, and Java supports plain and
coordinated Maven validation as described in [Java validation](#java-validation);
automatic apply is JavaScript only. For Ruby and Java, `onboard --apply` returns
the same handoff as `onboard --agent`: the CI setup for the agent to apply, then
coordinated validation.

The editor preserves existing project and CI tracer selections, including tracers
installed in workspace packages. It resolves one exact release for the actual
commands, pins newly added Datadog actions to a verified v3 release commit, and
adds framework initialization and report exclusions. Existing compatible v3 tags
and verified v3 release SHAs are accepted. It does not rewrite lockfiles or create
an `.npmignore` that would change npm's `.gitignore` fallback.

Every workflow is parsed and checked again before writing. Unsupported YAML,
custom initialization, conflicting versions, Windows runners and unknown
commands produce review gaps instead of guessed edits. CI that discovery can't
resolve never blocks the configurations that are ready: `--apply` edits and
validates those, and leaves the unresolved entries unedited and reported as
blockers, so the result stays incomplete until they are reviewed. A test step
guarded only by matrix or env values (`if: matrix.node != '20'`) gets the same
guard on the inserted action. CI rows below Node 18 (such as 16 or 17), the
oldest Node any dd-trace major ddtest supports runs on, are ddtest's own
decision, not the user's:
they keep running uninstrumented, `--apply` guards them out of the inserted
action with a matrix condition or leaves a job that only runs them unedited,
validation counts them as excluded, and the result names them as "Not tried".
When CI rows on a supported Node need an older tracer major than the rest,
`--apply` stops and, for Mocha steps whose every row is affected, names the
exact `DD_CIVISIBILITY_ENABLED: 'false'` opt-out for the user to approve; it
never excludes those rows itself. A reusable
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

## CI provider

`onboard` and `testdrive` read one CI provider's configuration: GitHub Actions
(`--ci-provider github`, `.github/workflows`) or GitLab CI (`--ci-provider
gitlab`, `.gitlab-ci.yml`). The provider whose configuration exists is selected
automatically; any other value is rejected with the list of supported
providers. When several supported providers contain tests, ddtest asks which
one to configure before discovery, edits or validation (agents receive
`ci_provider_required` with `next_action: ask_user`), and follow-up commands
keep the selected `--ci-provider`. A language without setup support on the
selected provider gets `ci_provider_unsupported` instead of instructions:
JavaScript and Ruby support both providers, Python and Java GitHub Actions only.
Other CI files (Jenkins, CircleCI, Buildkite and others) are never parsed: when
they mention the framework they are listed as outside this validation, and they
never fail onboarding.

### GitLab CI

ddtest reads `.gitlab-ci.yml` and every local file it includes (`include:
local`, string includes and `*` patterns), with `extends`, `!reference`,
`default` and top-level defaults, `inherit`, `variables`, `parallel: matrix`,
hidden template jobs and `before_script`/`script`. Script entries share one
shell, so literal `export`, assignments and `cd` carry into later entries.
`parallel: matrix` entries that differ only by the variable selecting the
job's `image:` (such as `RUBY_IMAGE`) share one local configuration, and each
entry's runtime is still checked statically; an `if` on such a variable
(`if [ "$RUBY_IMAGE" = "ruby:3.0" ]; then …; fi`) is followed per entry.
`rules` (including `include` rules and `workflow: rules`, whose `variables`
apply when the deciding rule is certain) and `when: never` exclude a job (or one matrix entry) only when every
operand is known; `only`/`except`, `changes`, `when: manual` and conditions on
pipeline variables are possible CI paths. Variables that only locate a
`cache:paths` directory are runner state and are not replayed. GitLab's own scanning, review and release components and templates run no
tests and are reported, not blockers: the `sast`, `secret-detection`,
`dependency-scanning`, `container-scanning` and `code-quality` components
under `gitlab.com/components/`, `danger-review` and `gem-release` under
`gitlab.com/gitlab-org/components/`, and the `Jobs/` and `Security/` SAST,
secret detection, dependency, container and license scanning, code quality
and `Workflows/` pipeline templates (the list is `gitLabNonTestComponents`
and `gitLabNonTestTemplates`). A JavaScript job without the setup that never
runs the selected framework (nothing it runs, following package and
repository scripts, names it, and every other executable is a known file,
release, deploy, bundler or linter tool; package runners such as `npx`,
`npm exec` and `yarn dlx` count by their target) is reported as a caveat, not
a blocker, and the CI runtime and tracer agreement checks skip it; unknown
wrappers keep their job in scope. Inside a job that runs the framework, a
repository Node script that never mentions it stays preparation of the tests,
replayed after review (`--allow-reviewed-builds`). Agents may opt in without
asking when every reviewed step runs repository-local code and nothing
downloads, installs or writes outside the repository copy; the final response
then lists the steps it ran ("Reviewed preparation run (opted in with
--allow-reviewed-builds): …"). When `--apply` needs one
source edit first (such as wrapping a Mocha command with NYC), its result
asks for that edit and resumes with `onboard --apply`. Anything ddtest cannot read locally stays an
unresolved blocker: remote, project, template and
component includes, include inputs and `spec:` headers, includes whose rules
depend on the pipeline, triggers and child
pipelines, unknown `extends`/`!reference` targets, `rules:variables`, state
changed inside shell control flow, and tests in `after_script`.

CI setup uses Datadog's GitLab setup script
([test-optimization-gitlab-script](https://github.com/DataDog/test-optimization-gitlab-script)),
pinned to a release and sourced only after its SHA256 check, as its README
shows. ddtest wraps it in `if [ -n "$DD_API_KEY" ]`, because the script exits
the job when the key is missing; the plain README form is accepted too. It runs the same installer as the GitHub action and exports its
configuration into the job's shell. The setup is complete when, in every job
that runs the tests, the script runs before the first test command with
`LANGUAGES` naming the language, `API_KEY` from a CI/CD variable
(`"$DD_API_KEY"`), an exact `<LANGUAGE>_TRACER_VERSION` and a `SITE` matching
the selected site. A person adds `DD_API_KEY` as a masked CI/CD variable; jobs
without it (protected variables on other branches, forks) log that Test
Optimization is disabled and run their tests untraced. The entry needs Bash, so jobs on images known to lack it (Alpine
variants, BusyBox, distroless) are refused with that reason; other images are
assumed to have Bash. Runtimes come from the job's `image:`
(`node:22`, `ruby:3.3-slim`); floating tags such as `latest` stay
inconclusive.

`onboard --apply` inserts the script entry, as a block scalar, before the first
JavaScript test command of each test job, plus the `--import` loader export for
Vitest and other ESM tests. It keeps every other line, and refuses entries that
a template, `!reference` or anchor shares with a job that does not run the same
tests. An edited included file is listed in the preview, since other projects
may include it. Ruby, Cucumber and WebdriverIO setup is applied by the coding
agent from `onboard --agent` instructions: for Ruby the script adds datadog-ci
to the CI bundle (no Gemfile edit) and replaces `RUBYOPT`, so existing Ruby
options are re-added after it. Jobs whose image runs an engine datadog-ci does
not instrument (JRuby, TruffleRuby, Ruby head) stay excluded.

## Coverage and local developer commands

Mocha CI commands without Istanbul coverage are wrapped with the project's NYC.
Cucumber and WebdriverIO initialization is applied only to the selected literal
package-script leaves, including lifecycle hooks. Forwarding aliases and other
frameworks remain unchanged.

Cucumber edits add NYC coverage and set
`DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false` in the selected scripts.
These changes also affect developers running those scripts locally, including
local `npm test`; the variable disables impacted-test detection on those runs.
Manual setup (`onboard --instructions`) may set the variable on the CI test step
instead; either place disables impacted-test detection in CI.
Other feature controls and testdrive's isolated probes remain separate.

Cucumber ESM coverage also needs `@istanbuljs/esm-loader-hook`. Missing coverage
dependencies produce an exact install command for npm, pnpm or Yarn. Review and
run that command separately, then rerun onboarding. For example:

```sh
npm install --save-dev --no-audit --no-fund nyc@17.1.0
```

Windows runners are never instrumented: the action's tracer installation and
the preload are unverified there, so they need manual initialization. In a job
whose runner is one matrix value, the inserted action skips the Windows rows by
that value, so the other rows are instrumented and the Windows rows keep running
without the tracer; the report lists them as Windows runners, not as opt-outs. Scoped
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
`--format json` selects coordination. Supplying `--facts` also selects the coordinator. Structured execution requires `--yes` and never opens an interactive prompt. RSpec and Minitest use the shared
coordinator after the agent applies the Ruby CI setup (see [Ruby validation](#ruby-validation)).
Python instructions and plain execution remain unchanged; structured validation is
unsupported for Python and returns no next command.

The coordinator owns one resolved execution plan: actual commands, directories,
environments, builds, runtime/tracer selection, initialization and feature policy.
A run cannot omit unresolved configurations to earn a complete verdict. CI rows with a literal `DD_CIVISIBILITY_ENABLED=false` or a verified skipped conditional Datadog installer remain visible as `excluded`: their original CI tests are preserved, but local paired execution and feature validation are not claimed. These opt-outs are counted separately from passed configurations. Unsupported or unavailable variants without an explicit opt-out remain incomplete. Checks
include source preservation, Git/npm report exclusions, owned process/temp cleanup,
and current execution evidence. The npm package check uses
`npm pack --dry-run --ignore-scripts --json`; it neither publishes nor runs hooks.
Checks and source-edit hashes stay in the bounded JSON; full output stays in HTML.

`onboard --apply` selects a tracer every CI row it instruments can run: when the default release needs a newer Node than a CI job provides, it picks the verified release that supports all of them, or stops and names the rows that need different majors; every configuration it validates uses that one release, and a stop names the recommended release in its resume command. Configurations that can never run locally, such as WebdriverIO cloud grids and multiremote, don't constrain the tracer: a cloud grid whose credentials are CI secrets is instrumented and reported as a caveat, and other such configurations stay unedited and are reported as blockers. When a CI path, such as the Cypress action API, runs on a Node major the local host lacks, ddtest validates on the local Node if both the framework and the tracer support it, and records the substitution in `runtime_substitution`. That evidence completes only with `--accept-substitute-runtime`, a decision for the user; otherwise the result is `needs_input` (`substitute_runtime_unaccepted`) with the accepting resume command. When the local Node is unsupported, the result is `blocked_environment` (`local_runtime_mismatch`) naming the CI runtime. Either way, an existing installation of the CI runtime first on PATH validates without substitution; ddtest never downloads a runtime.

Only CI that can reach a selected test is in scope. Jobs whose steps can't start
one (system packages, coverage merging, linting, publishing, another framework)
are listed as not a test consumer, and unresolved steps after a job's last
possible test are listed as outside the test path. Before the test, a
check-only step (it only reads or prints, or upgrades the package manager
itself with `npm install -g npm@latest`) is listed rather than blocking, since
each step's shell ends with it. Unknown executables, actions, repository files
and package scripts whose body could start a test still block, and so do
`source`/`eval`, interpreter invocations (`bash -c`, a script file, `node -e`
code that spawns processes or loads a repository module) and npm settings that
change how scripts execute (`ignore-scripts`, `script-shell`). A Cucumber alias
chain such as `pnpm test:unit && pnpm test:bdd` resolves to its Cucumber leaf
when that leaf is the chain's only and last link. CI files of other providers
(`circle.yml`, `.gitlab-ci.yml` when GitHub Actions is selected, and the like)
that mention the framework are listed for review; they are not analyzed. A job guard that reads only
runtime context (`github.actor`, `github.event_name`, `github.run_number`,
`needs.<job>.outputs`) is validated as a possible CI path and reported as
unverified; only a guard that is false for every outcome excludes the job. Validation runs as
close to CI as it can; where a CI value can't be reproduced locally, the report
names it as a caveat instead of a blocker. A matrix axis computed at runtime
(`fromJson(...)`) that only a dependency install reads becomes one value: the
job's tests run with the locally installed dependencies, and the axis is a
caveat once every configuration of that job passed with a framework version the
tracer supports. Otherwise, or when anything else reads the axis, it blocks. An
accepted substitute runtime is reported as a caveat too. So is a WebdriverIO
configuration whose CI step reads a remote browser grid's credentials from CI
secrets (Sauce Labs, BrowserStack, LambdaTest, TestingBot): setup instruments it
like any other step, and it is reported as running only in CI once a local
configuration of the same suite passed its paired comparison; without one, it
blocks. Caveats never prevent COMPLETE LOCALLY. The handoff also names CI step
settings that disable a tracer integration (such as
`DD_TRACE_DISABLED_INSTRUMENTATIONS=router`) and lists earlier attempts of the
same workflow that stopped before the final run. Re-running `--apply` on its own
output makes no change: the guard it writes on the Datadog action (runtime
partition, the test steps' guard, Windows rows) is the guard it expects. Plain argv
commands under `shell: pwsh`, including the Windows default, are read as POSIX
commands, including npm package specs such as `@scope/name@version`; PowerShell
syntax, doubled quotes and multi-line test steps remain unresolved.

Automatic Mocha tracer recommendations use the verified major-5 and major-6 bootstrap contracts. Exact releases still come from registry tags; obsolete Node tags cannot win by covering more legacy CI rows. An explicitly selected or installed unverified major remains unvalidated instead of being silently replaced.

Agents edit CI workflow and ignore files where a result asks for it. They also
make the other edits ddtest prescribes for validation, exactly as given and without
stopping to ask: dev dependencies through the exact install command, package
scripts and coverage config. For Cypress without application coverage, a
recognized browserify, Vite or webpack build gets a test-only edit in the run's
`coverage.edit` (install command, files and how to serve the instrumented build),
gated by an environment variable so other builds stay uninstrumented. A
`replace` value names the exact existing line an edit replaces. When `tsc`
compiles to ignored JavaScript before browserify, the coverage build bundles the
tracked TypeScript sources with Babel, so coverage names tracked files. Babel
packages are pinned to the 7 line that babelify supports. Other builds
get instrumentation guidance, which needs the user's approval like any edit ddtest
did not prescribe. The final response lists every uncommitted change outside CI and
ignore files in `repository_changes`, with the commands that revert it, including
the reinstall a reverted lockfile needs. Agents never rename tests, rewrite CI or
package scripts so ddtest can parse them, disable an instrumentation ddtest did not
prescribe disabling, or download a runtime; they report the blocker.
A final result tells the agent to relay `final_response` verbatim. An incomplete or
failed verdict starts with `NOT COMPLETE:`, so a paraphrase cannot turn it into a
completion claim.

Before anything runs, a coordinated run checks that the selected configurations'
dependencies are installed. When some are missing, the result is `needs_input`
(`project_dependencies_missing`) with `next_action: run_command` and
`setup_commands`: the argv commands, in order and from the repository root, that
install them; `next_command` is the first. Run every entry once, then
`resume_command`. The check itself installs nothing and runs no tests.

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

## Java validation

Java projects build with Maven. `ddtest onboard --framework junit5` lists the
Maven CI test commands of the GitHub Actions workflows and prints their setup;
select the framework the project uses (`junit4`, `junit5`, `testng`, `spock`,
`cucumber-java`, `karate`, `scalatest` or `munit`). A repository that also
contains a JavaScript, Python or Ruby project keeps that platform by default;
select Java with `--platform java` or a Maven `--framework`.

JUnit 4 and JUnit 5 support plain `ddtest testdrive --all --check-only --yes`
and full `ddtest testdrive --all --yes` validation, or a focused
`ddtest testdrive --framework junit5 --command './mvnw -B verify' --yes`. Each
configuration runs in a disposable copy with fresh build output, without and with
dd-java-agent. Native Surefire/Failsafe results must match (identities, outcomes,
failure types and messages, exit code), each must match one reported test
execution, the reporting run must cover repository application sources, and all
six feature controls run with a probe test in a module of the selected framework.
Matching pre-existing failures are permitted; a build that stops for anything
other than native test failures stays inconclusive.

Validation runs only the Maven test lifecycle up to `verify`: a CI step such as
`mvn clean install` is validated as `mvn clean verify`, other goals are reported
and left out, and `--command` refuses `install`, `deploy` and other goals, as
validation does for goals in `MAVEN_ARGS` or `.mvn/maven.config`, so `~/.m2`
artifacts are never overwritten and nothing is published. A dd-java-agent that
`MAVEN_OPTS` or `JAVA_TOOL_OPTIONS` already attaches, as after the GitHub action,
is removed from both runs; the reporting run attaches only the selected agent.
Validation refuses a dd-java-agent in `.mvn/jvm.config`, which no run could
remove. One Maven
command runs every framework the reactor declares; modules of other frameworks
are reported.

Testdrive uses the dd-java-agent release every instrumented CI job pins
(`--tracer-version` selects another), or `DD_TRACER_FOLDER`'s agent. A CI matrix
row is compatible when this run validated its agent release on its JDK major;
the JDK comes from `JAVA_HOME`, else the `java` on PATH. A row of another JDK
major is validated on the local JDK as a substitute, recorded in
`runtime_substitutions`, which completes only with `--accept-substitute-runtime`,
a decision for the user, and is then reported as a caveat. Validate that JDK
instead by running testdrive with `JAVA_HOME` set to it. `ddtest plan` and `ddtest run` do
not support Java: the agent performs Test Impact Analysis inside the Maven build.
Other Maven frameworks run reporting-only checks.

Coding agents start with `ddtest onboard --agent`, as for Ruby: there is no
automatic Maven CI edit. Until every discovered JUnit test step runs after a
complete Datadog action, the result is `ci_setup_required` with the CI setup in
`agent_instruction` and `testdrive --framework <name> --all --agent --yes` as
`resume_command`; afterwards it is `ready` with that command. A complete action
is a supported v3 release with an `api_key`, an exact `java-tracer-version` and
the selected site; a JDK the agent cannot instrument is excluded with an `if:` on
the action, keeping its tests. The coordinator reports `COMPLETE LOCALLY` only
after every selected configuration, feature and Git report-exclusion check
passes, and no POM packages the reports as a resource; Maven needs no dependency
setup commands. The report is bound to the JDK that runs ddtest; changing it
requires fresh execution.

## Ruby validation

RSpec and Minitest support plain `ddtest testdrive --all --check-only --yes`
and full `ddtest testdrive --all --yes` validation. Native baseline and
reporting-only results, real application coverage and all six feature controls
are checked independently for each discovered command and Gemfile. Matching
pre-existing test failures are permitted; missing native results or telemetry
remain incomplete. Ruby validation uses disposable repository copies and
preserves the original Gemfile and lockfile, including fallback tracer installs.
Run `ddtest onboard --framework rspec` (or `minitest`) for the manual setup
steps, then run the supplied validation command and retain both reports.
Successful plain Ruby validation reports `PASSED`.

Coding agents start with `ddtest onboard --agent`. There is no automatic Ruby CI
edit, so the handoff depends on the workflows:

- When a discovered RSpec/Minitest test step does not yet run the Datadog action,
  the result is `needs_input` (`ci_setup_required`) with
  `next_action: apply_source_edit`. `agent_instruction` holds the discovered
  configurations and the CI setup steps, without any validation commands, and
  `resume_command` is `testdrive --framework <name> --all --agent --yes`. The
  agent applies the setup, then runs `resume_command` once.
- Once every discovered test step runs after the action, or explicitly skips it
  with at least one instrumented step left, the result is `ready` with that
  validation command as `next_command`. Repeating `onboard --agent` returns the
  same handoff.

`--site`, `--tracer-version`, `--facts`, `--platform`, `--timeout`,
`--allow-reviewed-builds` and `--accept-substitute-runtime` are forwarded to the
validation command; `--command` is rejected, because onboarding validates every
discovered configuration. The coordinator then works as for JavaScript: it may
return `project_dependencies_missing` with `setup_commands` for missing bundles,
and it reports `COMPLETE LOCALLY` only after every selected configuration,
supported feature, source-preservation check and Git/gem report-exclusion check
passes. Repository gemspecs are loaded in an owned copy to list their packaged
files; no gem is built or published. Finish with the exact `next_command`
(`testdrive --report`) and render `final_response` unchanged. The report is bound
to the Ruby on PATH (not the host's Node), to the selected manifests and their
lockfiles, including ignored ones such as `gems.locked`, and to the effective
Bundler configuration and `BUNDLE_*` environment; changing any of them requires
fresh execution.

Numeric CI Ruby selections are checked against the resolved tracer's RubyGems
requirements. Other engines and floating selections require separate validation
or explicit instrumentation exclusions that preserve their original test steps.
CI configurations select their own Gemfile; a `BUNDLE_GEMFILE` from the shell
applies only to a focused local `--command`.

Literal one-target Rake default aliases retain their original bare `rake` command.
Dynamic/default enhancements and custom tasks stay unresolved. Numeric matrix
comparisons follow GitHub expression coercions; existing matrix types need no
quoting workaround. A locally selected tracer dependency that excludes a target
Ruby leaves CI compatibility inconclusive until that target resolution is checked;
only the tracer release's own incompatible requirement is a definite rejection.

For reports with 500 or more results, repeated source excerpts and covered-file
lists are stored once in the standalone HTML and materialized when expanded.
Search and coverage pagination retain access to this evidence.

For Minitest's `ActiveSupport::Testing::Isolation`, keep fork/`exit!` isolation and
all existing tests. `onboard --framework minitest` supplies a CI initializer using
Datadog's public synchronous writer with the configured CI transport. Append it
after the Datadog action's preload on the existing test step. Testdrive preserves
that preload order and validates the same initializer; it does not silently add
a local-only delivery workaround. Synchronous delivery adds network overhead but
finishes each trace before the child exits. Temporary tracer installation clears
application preloads, since native gem compilation changes working directory.

For a native-platform dependency that excludes a CI Ruby, testdrive may inspect
its exact release's portable RubyGems specification. Compatibility requires the
same gem name/version, a portable platform, identical runtime dependency
requirements and RubyGems requirements admitting the complete Ruby selector.
This lookup queries `https://rubygems.org` directly with RubyGems' fetcher, which
honors the standard proxy environment variables but not Bundler mirrors or
private gem sources. Missing, offline, mismatched or still-incompatible metadata remains inconclusive;
this never certifies actual CI compilation/execution. A direct tracer requirement
still rejects an incompatible Ruby. Ruby 2.7 also needs a compatible installer:
`onboard` shows how to select RubyGems 3.4.22 for that matrix entry, preserving
both its tests and instrumentation. The old RubyGems 3.1 client can choose a native
ffi archive requiring Ruby 3.0 instead of the compatible portable release.
