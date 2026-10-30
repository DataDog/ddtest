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
(`--ci-provider github`, `.github/workflows`), GitLab CI (`--ci-provider
gitlab`, `.gitlab-ci.yml`), CircleCI (`--ci-provider circleci`,
`.circleci/config.yml`) or Buildkite (`--ci-provider buildkite`,
`buildkite.yml` and `.buildkite/`). The provider whose configuration exists is selected
automatically; any other value is rejected with the list of supported
providers. When several supported providers contain tests (documentation, lint and
release tooling, such as `bundle exec yard doc`, does not count), ddtest asks which
one to configure before discovery, edits or validation (agents receive
`ci_provider_required` with `next_action: ask_user`), and follow-up commands
keep the selected `--ci-provider`. A language without setup support on the
selected provider gets `ci_provider_unsupported` instead of instructions:
JavaScript and Ruby support every provider, Python and Java GitHub Actions only.
Other CI files (Jenkins and others) are never parsed: when
they mention the framework they are listed as outside this validation, and they
never fail onboarding.

### Runtimes and facts only the user can state

Some CI jobs select no runtime ddtest can read: Buildkite steps on the agent
host, GitLab jobs without an `image`, CircleCI `machine` and `macos`
executors, GitHub self-hosted runners (a `self-hosted` label in any `runs-on`
form, including `{group, labels}` and a runner a matrix row selects, such as
`runs-on: ${{ matrix.os }}` with `os: [self-hosted, ubuntu-latest]`) without
an `actions/setup-node` or `ruby/setup-ruby` step that runs before their
tests and selects a version, and jobs whose image (such as
`ubuntu:22.04`) names no Node or Ruby version. Finding these jobs reads only
the repository; a Cypress GitHub action job's Node is the action's, not a
question for the user. For JavaScript and Ruby, `onboard` and `testdrive` then
return `needs_input` with `code: ci_runtime_required`, `required_input:
["ci_runtime:<workflow> / <job>", …]` and `next_action: ask_user` before any
setup or test runs. The agent asks the user and never guesses the version.

The answer comes back as `--ci-runtime [<job>:]<runtime>=<version>`,
repeatable: `node` (or `js`) with `22`, `22.11` or `22.11.0`, or `ruby` with
`3.3` or `3.3.6`. Without `<job>`, it applies to every job whose runtime is
unknown; `<job>` is the printed key (`.buildkite/pipeline.yml / :jest: Tests`)
or the job name alone, and wins over a statement without one. Two statements that name the same job (by
either key) with different versions are `invalid_options`; the same
statement twice is accepted once. A matrix job
takes one value for all its rows that run on a host whose runtime ddtest
cannot read; in a matrix that mixes self-hosted and GitHub-hosted runners, it
applies to the self-hosted rows only. A runner ddtest cannot resolve (such as
`runs-on: ${{ inputs.runner }}`) is read as written: without a `self-hosted`
label it is not asked about, its unknown runtime stays inconclusive, and a
statement naming only that job is reported as unused. A statement only fills
an unknown runtime:

- A statement for a job whose configuration selects a different version (its
  image, including a floating tag such as `node:lts`, a runtime installer step,
  `actions/setup-node` or nvm) stops with `ci_runtime_conflict`. A statement
  without a job never conflicts. An installer counts only when it runs on
  every matrix row the job runs, by the job's first test step: one whose
  condition skips it (`if: false`, or on some rows) or that runs after the
  tests leaves the host's runtime in place for them, so the statement fills
  it. So does an `actions/setup-node` step with neither `node-version` nor
  `node-version-file` (for example one that only sets `cache: npm`): it
  installs nothing, and the Node on PATH stays. A job ddtest recognizes no
  test step in keeps every installer.
- An `actions/setup-node` version is compared as setup-node reads it: a
  statement agrees when the selector can pick a release of the stated line, so
  `20.18.0` or `20` agrees with `20.x`, `20.X`, `20.*`, `^20`, `~20.18`, `v20`
  or `=20.18.0`, and `22` conflicts with `20.x`. A selector ddtest cannot
  evaluate (a range such as `>=18 <21`, `lts/*`, `node`, `latest`, `current`,
  `*`) is not claimed as a conflict: the statement is accepted and reported as
  unused, naming that selector. Image tags, other Node installers (a runtime
  installer step, nvm) and Ruby installers are compared by their numeric
  components, as before.
- GitHub-hosted runners and job containers, and images ddtest cannot resolve,
  keep their own findings: their runtime is in the configuration.
- The stated version goes through every check an image tag's goes through:
  the tracer's Node range (a major such as `22` resolves to its latest release,
  as `node:22` does), the ESM loader's Node range, the Ruby tracer's RubyGems
  requirements, and the RubyGems update Ruby 2.7 and 3.0 need.
- On the agent or runner host, the Bash and curl or wget the setup script needs
  are not checked, as before; `final_response` says so next to the statement.

`--ci-confirm <fact>[=<value>]`, repeatable, states a fact the selected
provider defines because its configuration cannot show it (such as a plugin
installed on the CI server). No provider defines one yet; any other key is
rejected with the provider's list.

Both flags are forwarded into every follow-up command, like `--ci-provider`,
and are part of the input fingerprint, so `testdrive --report` must repeat
them. `final_response` lists each used statement on a "Stated by the user, not
read from CI configuration or verified" line, naming the jobs; a statement no
validated job used is a caveat.

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
pipeline variables are possible CI paths. Tool cache settings (variables
named like `npm_config_cache` or `PIP_CACHE_DIR`, or a language's package install directory such as `BUNDLE_PATH`) that point into a
`cache:paths` directory are runner state and are not replayed; other
variables stay. GitLab's own scanning, review and release components and templates run no
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
Optimization is disabled and run their tests untraced. The entry needs Bash and curl, so jobs on images known to lack them (Alpine
variants, BusyBox, distroless; Docker's official `-slim` images, which drop
curl, unless an earlier entry runs `apt-get install -y curl`) or on images
ddtest cannot resolve (`image: $NODE_IMAGE`) are refused with that reason;
other images are assumed to have both. Runtimes come from the job's `image:`
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

### CircleCI

ddtest reads `.circleci/config.yml` (versions 2 and 2.1): every job a workflow
runs becomes one job, named `<workflow>/<job>`, and a configuration without
workflows runs its `build` job. It expands `executors` (`docker`, `machine`,
`macos`, named executors and their parameters; the first `docker` image is the
job's image), reusable `commands`, job and command `parameters` (including
parameters of type `steps`), `<< parameters.x >>`, `<< pipeline.parameters.x >>`
(their defaults) and `<< matrix.x >>`, inline orbs, YAML merge keys,
`environment` (executor, image, job and step), `working_directory` (the job's
directory is the checkout), `run` steps (string or map with `command`, `shell`,
`environment`, `working_directory`, `when`), `when`/`unless` steps and workflows
(`and`, `or`, `not`, `equal`, `matches` over known values), workflow `matrix`
(one job per combination without `exclude`d ones, named `<job>-<values>` or by
its `name`), `override-with` and `pre-steps`/`post-steps`. Conditions follow
CircleCI's rules: `matches` must match the whole value, and a value's type
decides its truth (the string `"false"` is true, the boolean `false` and `0`
are false). `type: approval` jobs are skipped,
`when: on_fail` steps never run on a passing path, and cache, workspace,
artifact and test-result steps run nothing ddtest replays. Run steps don't
share a shell: only what a step appends to `$BASH_ENV` with `echo` (`echo
'export NAME=value' >> "$BASH_ENV"`) reaches later Bash steps, overriding their
own `environment`; a plain assignment there is a shell variable later
commands expand, which ddtest substitutes into them. A step run with
`/bin/sh` reads none of it, so a test step run with sh after the Datadog orb
is unresolved, and `$BASH_ENV` changes under a condition ddtest cannot decide
make later steps unresolved. Test splitting is resolved to the command each container runs
(`circleci tests glob … | circleci tests run --command="xargs <command>"`,
`… | circleci tests split | xargs <command>`, and a variable holding the
split files passed to the command), which local validation runs once on every
test; other `circleci tests` uses stay unresolved. Orbs that only notify, scan
or upload coverage run no tests and are reported, not blockers:
`circleci/slack`, `circleci/jira`, `snyk/snyk`, `sonarsource/sonarcloud`,
`codecov/codecov`, `coveralls/coveralls` and `qltysh/qlty-orb` (the list is
`circleNonTestOrbs`). CircleCI's own Node.js, Ruby and browser-tools orbs are
modeled from their source for the releases configurations use
(`circleci/node` 4.x–7.x, `circleci/ruby` 1.x–4.x, `circleci/browser-tools`
1.x–2.x), through each language's `ciComponents`: `node/install` and
`ruby/install` select the runtime, `node/install-packages` and
`ruby/install-deps` run the install command they run (respecting `app-dir`,
`pkg-manager`, `override-ci-command`), browser installs are reported, the
`node/test` and `node/run` jobs run `<package manager> run <script>` in
`app-dir` (with the reporters `test-results-for` adds), `ruby/rspec-test`
runs RSpec as its script does (1.x–3.x; 4.x's test suites stay unresolved)
and `ruby/rubocop-check` only lints. A command or release the models don't
describe stays unresolved.
Anything ddtest cannot read locally stays an unresolved blocker: every other
registry orb's commands, jobs and executors, dynamic configuration (`setup:
true`), jobs the configuration does not define, pipeline values such as `<<
pipeline.git.branch >>` in commands, background steps, non-Bash shells,
working directories outside the checkout and checkouts into another path.
Conditions on pipeline values keep their steps and workflows as possible CI
paths.

CI setup uses Datadog's Test Optimization orb
([test-optimization-circleci-orb](https://github.com/DataDog/test-optimization-circleci-orb)),
declared as `datadog/test-optimization-circleci-orb@2` (the moving v2 major;
an exact `@2.x.y` is accepted, `@1`, `dev:` and `volatile` are not). Its
`autoinstrument` step runs the same installer as the other setup mechanisms
and appends its exports (`NODE_OPTIONS` with the dd-trace preload, the ESM
loader path, `RUBYOPT`) to `$BASH_ENV`. The setup is complete when, in every
job that runs the tests, the step runs unconditionally before the first test
step with `languages` naming the language, an exact `<language>_tracer_version`
(the orb's own default changes with `@2` releases) and a `site` matching the
selected site (`--site` or `DD_SITE`; default `datadoghq.com`). CircleCI
rejects orbs in a `version: 2` configuration, which is reported; apply moves
it to `version: 2.1` (which reads the rest the same way) unless it holds
`<< >>` text 2.1 would substitute. A person adds
`DD_API_KEY` in the CircleCI project settings or a context; the orb is used as
is, so a job without the key fails at that step (forked pull requests without
passed secrets, jobs outside a restricted context), and the human action says
so. A `DD_API_KEY` written into the configuration is refused. The orb needs
Bash, because CircleCI runs steps with `/bin/sh` in images without it and only
Bash reads `$BASH_ENV`, and curl or wget: jobs on images known to lack them
(Alpine variants, BusyBox, distroless; Docker's official `-slim` images unless
an earlier step installs curl), Windows machines, a non-Bash job `shell`, or
executors ddtest cannot read (registry orb executors, unresolved parameters)
are refused with that reason. Runtimes come from the job's image (`cimg/node:22.11`,
`cimg/ruby:3.3`, `ruby:3.3`).

`onboard --apply` declares the orb when the configuration does not (a new
entry of `orbs`, or a new `orbs` mapping after `version`) and inserts the
`autoinstrument` step before the first JavaScript test step of each test job,
plus a step appending the `--import` loader registration to `$BASH_ENV` for
Vitest and other ESM tests. It keeps every other line. A test step in a
reusable command is edited there only when every job running it is a test job
needing the same setup; steps shared through anchors, steps in `pre-steps` or
inline orbs, and commands a job runs twice are refused, as is an existing
declaration of an unsupported orb release; a test in a modeled orb job (such
as `node/test`) takes the Datadog orb step through that job's steps
parameter (`post_install_steps`), which ddtest asks for instead of editing.
The inserted step follows the configuration's indentation. Applying again changes nothing.
Ruby, Cucumber and WebdriverIO setup is applied by the coding agent from
`onboard --agent` instructions: for Ruby the orb adds datadog-ci to the CI
bundle and replaces `RUBYOPT`, so existing Ruby options are re-added after it
through `$BASH_ENV`, and a `rake RUBYOPT=…` argument that would replace the
preload is reported with the form that keeps it; Ruby 2.7 and 3.0 images get a `gem update --system` step,
guarded by a `when` on the image where a matrix shares the job.

### Buildkite

Which repository file a Buildkite pipeline uploads is configured in the
Buildkite UI, so ddtest reads every pipeline file: `buildkite.yml` (`.yaml`,
`.json`), `buildkite/pipeline.yml` (`.yaml`, `.json`) and the YAML files under `.buildkite/` and `.buildkite/pipelines/`
that define `steps` (in the mapping or the legacy list form; Docker Compose
files, recognized by top-level `services` or `version` without `steps`, hooks
and anchor-only files are not pipelines). Each command step
becomes one job per matrix combination, named by its `label` (or `key`) under
its `group`s, and each top-level shell statement of its `command`/`commands`
(a string, a list or a block) one step. A step's commands share one shell, so
literal `export`, assignments and `cd` carry into later statements of the
step; every Buildkite step starts a fresh shell, possibly on another agent, so
nothing carries across steps. ddtest reads the pipeline and step `env`, the
`image` attribute, `matrix` (a list of values for `{{matrix}}`, or `setup`
dimensions for `{{matrix.name}}`, with `adjustments` that add or `skip`
combinations; values reach labels, commands, env and plugins), `if`
(decided only when every operand is a literal: `false`, `"a" == "b"`; build
values, `build.env()`, regular expressions and `includes` keep the step as a
possible CI path), `skip`, `branches` (a possible path), groups, YAML anchors
and merge keys; `wait`, `block` and `input` steps only pause the pipeline.
`buildkite-agent pipeline upload` interpolates the file before any step runs:
`$$NAME` and `\$NAME` are read when the step runs, the top-level `env` is known,
variables that describe the build (`$BUILDKITE_BRANCH`, `$BUILDKITE_COMMIT`,
`$BUILDKITE_BUILD_NUMBER`, `$BUILDKITE_PIPELINE_SLUG` and the like) read the same
value in every job, and any other `$NAME` (including the step's own `env` and
agent or job variables such as `$BUILDKITE_BUILD_CHECKOUT_PATH` or
`$BUILDKITE_PARALLEL_JOB`) is replaced with a value of the upload step that ddtest cannot know, so a
command reading one is unresolved and an env value reading one is replayed as
unknown. For a known variable, `${NAME:-default}` reads the default when the
value is empty and `${NAME-default}` keeps an empty value, as upload does; an
unknown variable stays unknown with either. Upload interpolates the file
once, so a pipeline `image: $$NAME` stays the literal `$NAME`, an image ddtest
cannot resolve. A container receives only the environment its plugin passes: the
plugin's `environment` (`NAME=value` as written, a bare `NAME` with the
pipeline's value) over the Compose service's `environment` (for `DD_API_KEY`
too: a plugin listing it decides whether the key passes), plus, with
`propagate-environment: true`, the pipeline's `env`. Windows agents are read
from the effective `agents` (the step's keys override the pipeline's), after
upload interpolation; a target reading a variable ddtest cannot know may
select Windows agents, so the step is unresolved. The
docker plugin runs the commands in its `image` with `/bin/sh` (or its
`shell`) at the mounted checkout, the docker-compose plugin in its `run` service's image from
the Compose file (`config`, default `docker-compose.yml`, with matrix values;
`${NAME:-default}` in the image reads the step's env, and a variable it does not
set, which the agent or a `.env` file may supply, leaves the image unknown; a
service with `build` (a context, or `context`, `dockerfile`,
`dockerfile_inline`, `args` and `target`) runs in the image its Dockerfile's
final stage, or `target`, builds `FROM`, followed through stage aliases, which
then selects the runtime and is checked for the setup like a written image; a
`FROM` reading a build argument without a literal default, or one the
Compose `args` or the plugin's `args` override, `FROM scratch`, a remote
context or a Dockerfile outside the repository leaves the image unknown with
that reason; its
`working_dir`, or else the Dockerfile's last `WORKDIR`, maps back into the
checkout through the volume mounting a repository directory (relative to the
Compose file, as Compose reads it) or, without one, through the
Dockerfile's `COPY . <destination>` of the build context, and one outside a
mounted checkout is unresolved), and steps without either run on
the agent host, whose runtime ddtest cannot know: onboarding asks the user for
it (`ci_runtime_required`, answered with `--ci-runtime`) instead of certifying
it. Plugins that only fetch secrets,
artifacts or caches, log in to registries or annotate results run no tests
and are reported, not blockers: `artifacts`, `cache`, `docker-login`, `ecr`,
`junit-annotate`, `test-collector`, `secrets`, `vault-secrets`,
`aws-assume-role-with-web-identity`, `seek-oss/aws-sm` and
`bugcrowd/test-summary` (the list is `buildkiteNonTestPlugins`), as are
docker-compose steps that only build or push images (whose commands, if any,
run on the agent host, not in a container) and `buildkite-agent
artifact`, `annotate` and `meta-data` commands without command substitutions.
An upload of pipeline files ddtest reads (`buildkite-agent pipeline upload` with
literal paths, or without arguments, where exactly one of Buildkite's default
files must exist), resolved from the step's working directory, is reported, since those files are read as pipelines. Anything
ddtest cannot read locally stays an unresolved blocker: `trigger` steps,
every other `buildkite-agent pipeline upload` (generated or piped steps,
files ddtest does not read, `--no-interpolation`), every other plugin (or one
named by a variable), a step that runs a container's own command, the docker
plugin's or Compose service's `entrypoint` (other than an emptied one or a
known passthrough such as `tini --` or `docker-entrypoint.sh`), `shell:
false` and `mount-checkout: false`, matrices it
cannot expand or that exceed 200 jobs, and Windows agents (also when a matrix
value selects them).

There is no Datadog Buildkite plugin: CI setup runs Datadog's installer
(`install_test_visibility_v15.sh` from `install.datadoghq.com`, the one the
GitLab script and the CircleCI orb wrap), pinned with its SHA256 in
`ci_provider_buildkite_setup.go`. ddtest's setup snippet, written as a block
scalar with every `$` escaped as `$$`, runs in the test step itself, before
the test command: it downloads the installer with curl or wget, checks its
SHA256 (`sha256sum`, or `shasum` on macOS agents), runs it with Bash with
`DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES`, an exact
`DD_SET_TRACER_VERSION_<LANGUAGE>`, `DD_SITE` and
`DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER=buildkite`, and exports the
`KEY=VALUE` lines it prints (`NODE_OPTIONS` with the dd-trace preload and the
ESM loader path, or `RUBYOPT`, plus `DD_CIVISIBILITY_ENABLED`,
`DD_CIVISIBILITY_AGENTLESS_ENABLED` and the installed versions) into the
step's shell. It is guarded by `if [ -n "${DD_API_KEY:-}" ]`, and every command
that may fail is a condition, so a step without the key or whose install
fails logs that and runs its tests untraced. The setup is complete when, in
every step that runs the tests, the snippet (guarded, or its two statements
without the guard) runs before the first test command in exactly ddtest's
shape, with the pinned installer and checksum, a language token naming the
language, an exact tracer release and a `DD_SITE` matching the selected
site. A statement that runs the installer in any other shape stays an
unresolved command; an unescaped `$`, a `DD_API_KEY` written into the pipeline
or a plugin's `environment`, one set to an empty value (`DD_API_KEY=`), and a
container that does not receive `DD_API_KEY` (only a bare name, or a Compose
`${DD_API_KEY}` reference, passes it through; `propagate-environment` passes
only a key set in the pipeline's environment, such as in the Buildkite UI,
never one an agent hook or secrets plugin exports, which is noted), and an
earlier command of the step that clears or writes `DD_API_KEY` are reported. A person provides `DD_API_KEY` from an agent
environment hook, a secrets plugin or the pipeline's environment in the
Buildkite UI. The installer needs Bash and curl or wget, so steps on images
known to lack them (Alpine variants, BusyBox, distroless; official `-slim`
images unless an earlier command installs curl; for an image built from a
Dockerfile, unless one of its `RUN` instructions installs bash, or curl or
wget, with `apt-get`, `apt`, `apk`, `yum`, `dnf`, `microdnf` or `zypper`) or
on images ddtest cannot resolve are refused with that reason, which names
the Dockerfile and the fix (install curl in it, or build from the full
image); the setup instructions name those steps before the snippet is
written, since changing an image needs the person's approval. The agent host
is assumed to have them, and `final_response` says so next to its stated
runtime. A job whose Ruby or Node is unknown because its image
cannot be resolved is reported with that cause, and the release of manual
setup edits for steps ddtest could not resolve is the newest one every known
CI Node runs (`DD_SET_TRACER_VERSION_JS="5.133.0"` on `node:18`).

`onboard --apply` inserts the snippet before the first JavaScript test command
of each test step: a new commands entry before a listed command, lines inside
a `command: |` block before the test statement, or a list holding the snippet
and the command for a single `command:` string. It adds `DD_API_KEY` to the
docker or docker-compose plugin's `environment` list (or adds the list) so the
key reaches the container, and keeps every other line. Commands shared through
anchors (or a plugin configuration shared that way), folded commands, a test
statement after another in one string, and commands a step that does not run
the same tests also runs are refused; matrix combinations share one insertion.
Applying again changes nothing. Ruby, Cucumber and WebdriverIO setup is applied
by the coding agent from `onboard --agent` instructions; for Ruby the installer
adds datadog-ci to the CI bundle and replaces `RUBYOPT`, so existing Ruby
options are re-added after it, and Ruby 2.7 and 3.0 images get a `gem update
--system` command, guarded on the image where a matrix shares the step.

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
or CI version is selected. Without either, when the latest release can't run
on every instrumented CI row's Node, setup selects the newest release that
can, says so ("Selected dd-trace@…"), and reports any framework support it
lacks for that release; with no such release it stops and names the rows.

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
when that leaf is the chain's only and last link. An unquoted pathname pattern
in a JavaScript test command, such as `"test": "mocha *.test.js"`, is expanded
as `sh` expands it in that command's directory (no globstar, so `**` matches
like `*`; no dot files) and probes receive those files, including Jest probes
of an explicit `--command`, while full-suite runs keep the original alias; a
quoted pattern stays a runner argument. A pattern with a variable, braces, `~`
or an escape, one matching no repository files, one matching a file that
resolves outside the repository through a symlink and one expanding to more
than 1000 files stay unresolved, except in a companion tool such as Prettier
that starts no tests. On a Windows runner, npm runs package scripts with
`cmd.exe`, which leaves the pattern to the runner, so a pattern inside a package
script stays unresolved there, per matrix row: the Linux rows of a mixed-OS
matrix still resolve. A pattern inside a package script also stays unresolved
when the project's `.npmrc` (at the repository root or in the package
directory) sets `script-shell` to a shell other than `sh`, `bash` or `dash`;
ddtest can't see a user or global npmrc, so a `script-shell` set there goes
unnoticed. A PowerShell step's own unquoted pattern stays unresolved too, while
a quoted one resolves. CI files of other providers
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
blocks. A GitHub Actions variable that carries only a credential secret, such
as `GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}` or a `SONAR_TOKEN` set on the test
step, its job or an earlier setup step, is a caveat as well: local validation
runs without it (a value the host exports is unset too) and names it. A
credential is a variable whose name ends with TOKEN, SECRET, PASSWORD,
PASSPHRASE, API_KEY, ACCESS_KEY, SECRET_KEY, PRIVATE_KEY, CREDENTIALS or AUTH.
A command that reads such a variable, a secret used as a setting (such as
`AUTH_MODE` or a URL) and a remote grid's key outside WebdriverIO still
require review. Caveats never prevent COMPLETE LOCALLY. The handoff also names CI step
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

Every Maven framework supports plain `ddtest testdrive --all --check-only --yes`
and full `ddtest testdrive --all --yes` validation, or a focused
`ddtest testdrive --framework junit5 --command './mvnw -B verify' --yes`. Each
configuration runs in a disposable copy with fresh build output, without and with
dd-java-agent. Native results (Surefire/Failsafe reports, the
scalatest-maven-plugin's `target/scalatest-reports` and Karate's
`target/karate-reports` feature reports) must match (identities, outcomes,
failure types and messages, exit code), each must match one reported test
execution, the reporting run must cover repository application sources, and all
six feature controls run with a probe test in a module of the selected framework
(a Groovy specification for Spock, compiled by the project's GMavenPlus setup,
and a Scala suite for ScalaTest and MUnit, compiled by its scala-maven-plugin).
For Cucumber JVM the probe is a feature with a step in the glue package of the
module's runner class (`@RunWith(Cucumber.class)` or a JUnit Platform suite of the
`cucumber` engine), so the project's own glue, hooks and object factory load;
the runner is selected with the probe feature in place of the project's. A
module without such a runner, or a suite that pins its own features or tags,
leaves the features inconclusive. For Karate the probe is a feature that the
module's runner class (`@Karate.Test`, `@RunWith(Karate.class)` or a JUnit test
calling `Runner.path`) runs through `karate.options`, so `karate-config.js` and
`karate.env` still apply; the runner's own JUnit result is left out. MUnit's
runner is not one dd-java-agent can skip tests in, so its skipping and disabled
features are reported as not supported. A ScalaTest suite that Surefire runs
through ScalaTest's `JUnitRunner` gets a probe with the same runner; dd-java-agent
1.67.1 and 1.68.0 fail every test of such a suite, which validation reports as a
suspected regression. Probe builds stop at the `test` phase, so
checks after the tests, such as license headers, do not judge the probe.
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

Coding agents start with `ddtest onboard --agent`, as for Ruby: there is no
automatic Maven CI edit. Until every discovered test step of the framework runs after a
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

A CI step may set up its tests in the same script: literal `export`s before
exactly one recognized test command become that configuration's environment,
a `rake RUBYOPT="$RUBYOPT <options>"` argument adds its options to the
preload (discovery keeps reads of the variables the installer exports, such
as `$RUBYOPT`, as written instead of asking for their CI value), and commands after the tests that only create directories, move or
copy files, or print (such as moving a coverage report) are outside the test
path. Several test commands, control flow or computed values keep the step
unresolved.

Failure messages compare without object addresses (`#<Foo:0x000…>`), so a
failure every run repeats is not reported as varying. Tests whose names
change between runs, such as one-line examples whose generated description
embeds a temporary file name, are reported as `unstable_test_identities` with
the offending examples: give them static descriptions, because Test
Optimization cannot track them across CI runs either.

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
