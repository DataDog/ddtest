---
name: ddtest-onboarding-eval
description: Compare uncoached Codex or Claude onboarding trials in fresh repositories, independently grade their ddtest evidence, and clean up owned trials. Maintainer tooling; not customer onboarding instructions.
---

# ddtest onboarding evaluation

**Run untrusted repositories and auto-approved agents only with the container
runner below. Use a clean trusted image, no cloud/GitHub/npm credentials or MCP
servers, and explicitly authorized model authentication. A CLI permission mode
alone is not isolation.** The container has network access for public source,
packages and the selected model service. Do not publish, run real CI, or use a
real Datadog intake in these trials.

The runner disables instrumentation telemetry by default and maps the default
US1 test-cycle, coverage, API and instrumentation-telemetry intake hostnames to
container loopback. It records intake_guard in run.json and new frozen repeated-run
oracles. Evidence predating the guard remains identified as unguarded. The current
`us1-defaults-v2` also blocks `citestcov-intake.datadoghq.com`; the earlier
`us1-defaults-v1` did not block that coverage hostname and keeps its original identity. Changing the guard requires
a new run folder; do not pool guard regimes without disclosing that difference.
The guard covers those normal tracer hostnames; custom endpoints, other sites and
direct IP connections remain outside it.

**Egress is not restricted.** The container can reach the Docker host's bridge
gateway, the local network and any cloud instance metadata endpoint. Run trials on
a dedicated machine or VM with no local Datadog Agent, databases, other services
or instance credentials.

This skill belongs to the evaluator. Keep it outside the tested checkout and
agent configuration. The tested agent receives only the ddtest binary and the
simple prompt; do not give it this skill, the grading rubric, earlier results or
follow-up coaching. The runner does not mount evaluator code or evidence into
the container.

## Prepare

For pytest trials, use `--framework pytest --python /absolute/image/path/to/python`
with a trusted image containing Python and venv support, such as the
Dockerfile's `python` target (CPython 3.12, `--python /usr/local/bin/python3`;
select another with `--build-arg PYTHON_IMAGE=python:<version>-bookworm@sha256:<digest>`
and `--build-arg PYTHON_VERSION=<major.minor>`). The runner creates a
private environment before dependency setup, preserves it for the agent, and
checks the final report's Python version, interpreter location and environment
prefix. Current execution with missing or different environment evidence fails
the trial. Prepare the project's test dependencies;
editable installs of the tested checkout block testdrive isolation. Use
checkout-local `PYTHONPATH` or pytest `pythonpath` imports instead.

Use Python 3.9+ with PyYAML, Git, GnuPG for signed trims, Go for building ddtest, and Docker with the trial root
shared into its VM. On macOS the default root is `~/.dde`, created private:
Colima shares only `$HOME` by default and Docker Desktop shares `/Users`. On Linux
the system temporary root is used. To use another folder, set
`DDTEST_EVAL_TRIAL_ROOT` to an existing absolute directory the VM shares; with
Colima, add it under `mounts:` (with `writable: true`) in
`~/.colima/<profile>/colima.yaml` and restart Colima. A trial whose folder the VM
can't see fails before cloning, naming the folder. Keep the root short: Watchman's
socket lives inside each trial, and a root too long for it is refused. Select a public repository, exact commit/ref, dependency setup commands,
and project-compatible runtime/browser prerequisites. Compare agents at the same
commit, binary and image. Use the optional `browsers` image target or a trusted
derived image for browser suites. Select the project-compatible package manager
explicitly in setup. When the project pins a pnpm version (for example in its CI's
`pnpm/action-setup`), pass `--pnpm <version>`: the runner installs that pnpm
privately in the trial, verifies it, and puts it first on `PATH` for setup and
the agent, so lockfiles are not rewritten by the image's pnpm. The `browsers`
image sets `CHROMEDRIVER_PATH` to the distribution chromedriver matching its
Chromium and architecture; WebdriverIO otherwise downloads a driver, and Chrome
for Testing has no linux/arm64 build. `--readiness-command` runs a native browser/runner check after
setup and before the agent; failures are evaluator-environment failures. Temporary
storage is executable and the container mount uses `/trial` to avoid Unix socket
path limits. Record emulation and resource limits when interpreting timings. Record ddtest's source commit and dirty state outside
the target checkout.

Build the trusted image (add project-specific OS/browser dependencies to a
reviewed derived image if needed). Never bake personal configuration or secrets
into an image. The supplied image pins Node, pnpm, Codex and Claude; unexpected
CLI versions fail before cloning. For `--framework rspec` or `minitest`, use a
trusted image with the project-compatible Ruby and Bundler installed: the
Dockerfile's `ruby` target adds Ruby 3.4 (select another with
`--build-arg RUBY_IMAGE=ruby:<version>-bookworm@sha256:<digest>`).
The runner records its Ruby version before cloning and checks the report against
that version. Install dependencies in the private trial HOME (for example,
`bundle config set --global path /trial/home/bundle`, then `bundle install`) and
use `bundle check` as the readiness command. For large Ruby matrices, use the
container-native private `/tmp/bundle` as Bundler's path to avoid bind-mount
filesystem overhead; it is removed with the container, while lockfiles and both
reports stay in the checkout until collection. Ruby's `TMPDIR` also uses the
container's private executable tmpfs; no host cache is mounted there.
A Bundler `path` puts gem executables off `PATH`, so a CI command such as bare
`rspec` fails where the CI image has it. Pass `--bundle-binstubs rspec-core`
(repeatable, one gem each): after setup, the runner writes Bundler binstubs to
a private `/trial/bundle-bin` on `PATH` for readiness and the agent, and records
them under `bundle_binstubs` in `run.json`. `/trial/bin` keeps only ddtest (the wrapper, and for an older build the real binary).
For `--framework junit4`, `junit5`, `testng`, `spock`, `cucumber-java`, `karate`, `scalatest` or `munit`, build `--target java`: it adds a
digest-pinned Temurin JDK 21 (select another with
`--build-arg JDK_IMAGE=eclipse-temurin:<version>-jdk@sha256:<digest>`) and a
checksum-verified Maven. The runner records the JDK's release version before
cloning and checks the report against it; local validation certifies only CI
rows of that JDK major. The container user has no passwd entry, so the JVM's
`user.home` is `?` whatever `HOME` says, and Maven or Gradle would create
`?/.m2` or `?/.gradle` in the checkout. Java trials therefore set
`MAVEN_OPTS=-Duser.home=/trial/home` and `GRADLE_USER_HOME=/trial/home/.gradle`
for setup, readiness and the agent (recorded under `private_caches` in
`run.json`; ddtest keeps them in both validation runs). Maven then downloads
dependencies into the private trial HOME, so no setup command is required;
`mvn -B -q dependency:go-offline` works as a readiness command. A setup or
readiness command that sets its own `MAVEN_OPTS` must keep `-Duser.home` or pass
`-Dmaven.repo.local=/trial/home/.m2/repository`. Readiness, like setup, must
leave the checkout clean, or the trial stops before the agent. Java's `TMPDIR` uses the
container's private tmpfs, like Ruby's, since validation copies the repository
per configuration.
Ruby multi-bundle and Java multi-configuration validation can
exceed ten minutes for one foreground `testdrive --all`. Claude's synchronous
Bash allowance is bounded by `--timeout`, up to one hour for Ruby/Java/Cypress; use
an adequate overall trial timeout for these matrices. This does not change
ddtest's separate per-subprocess timeout or permit background tasks.
The binary must also start inside the image;
its version and hash are recorded before a model session begins.

```sh
docker build --target agents -t ddtest-eval \
  -f skills/ddtest-onboarding-eval/scripts/Dockerfile \
  skills/ddtest-onboarding-eval/scripts
IMAGE=$(docker image inspect --format '{{.Id}}' ddtest-eval)
ARCH=$(docker image inspect --format '{{.Architecture}}' "$IMAGE")
GOOS=linux GOARCH="$ARCH" go build -o /absolute/path/to/ddtest-linux \
  -ldflags "-X github.com/DataDog/ddtest/internal/buildinfo.Version=$(git rev-parse --short HEAD)" .
```

A plain `go build` reports version `dev`; the `-ldflags` value above, as the
Makefile and release builds set it, makes `ddtest --version` (and so
`ddtest_version` in `run.json`) name the commit. Add `-dirty` to it for an
uncommitted tree. `--ddtest-label <text>` (run_eval.py and run_catalog.py)
records free-text provenance, such as `b7a9d2ba` or `b7a9d2ba + local fix`, as
`ddtest_label` in `run.json` and `results.json`; next steps and the catalog's
`expected.ddtest` use it in place of a `dev` version.

Use a separate test account when available. An explicitly authorized existing
CLI account works with `--existing-login`; no manual JSON export or new login is
needed for each trial. Codex uses its model-authentication file; Claude can use
its trusted managed model helper or OAuth file. Only model authentication and
model-service routing are selected, never settings, hooks, histories, OTEL or
MCP configuration. Do not forward cloud, GitHub, npm or SSH credentials.

The runner preserves refresh metadata, serializes evaluations sharing a login,
and writes refreshed authentication back only if the source has not changed.
Only a refresh made before the checkout exists (during the model preflight) is
written back: repository commands can rewrite the trial login, so a change
during the agent session is reported as `login_refresh` in the run record, not
persisted.
Do not use that same OAuth login concurrently in unrelated CLIs. A model-response
preflight runs before cloning/installing; an expired or revoked account stops
there, without repeated trial attempts or automatic login flows. `--login-json`
remains available for an explicitly selected file. Container trials run as the
host UID with dropped capabilities, a read-only root and one owned trial mount.
The selected login remains accessible to the tested agent. Use a separate account
when possible and existing account credentials only with explicit authorization.
Credential installation uses descriptor-based paths without following symlinks.
Environment values use a private `--env-file`, never the Docker command line;
multi-line values (such as managed model headers) are base64-encoded there and
restored inside the container.
The container has no access to the host CLI profile or its MCP configuration.

## Run

```sh
python3 skills/ddtest-onboarding-eval/scripts/run_eval.py \
  --image "$IMAGE" \
  --repo https://github.com/moment/luxon.git --ref <chosen-commit-or-ref> \
  --ddtest /absolute/path/to/ddtest-linux --agent codex --framework jest \
  --existing-login \
  --setup-command 'npm ci --no-audit --no-fund' \
  --output /absolute/path/to/new-luxon-codex-evidence
```

Repeat with `--agent claude`, its selected login and a new output directory.
The runner mounts storage at the short container path `/trial`, records the
requested ref's resolved SHA, and creates a fresh checkout and private caches every
time. Remote sources fetch only that ref with `--depth=1 --no-tags`, without
downloading the default branch first; setup can explicitly fetch more history.
Local sources copy their available Git objects, history and tags without hardlinks before
their temporary mirror is removed. Setup can use that data without a reachable original
remote, including partial clones whose requested snapshot is already available. Submodules are opt-in with
`--submodules`. Setup arguments use `shlex` without an implicit shell; setup must
leave tracked/untracked Git state clean; it may commit, and `changes.patch`
starts from HEAD after setup and readiness (`agent_baseline` in `run.json`;
`commit` stays the fetched SHA). Optional `--node` selects an absolute
**image** executable for Codex, not a host binary. The recorded final ddtest
runtime must match it. Optional `--model` selects a model; otherwise the fresh
CLI default applies. `run.json` records the CLI version, the requested model and
`resolved_model`, the model the CLI reports in its session events (Claude's
init event; `null` for Codex, which reports none; `resolved_models` lists more
than one if a resumed turn changed it).

The tested prompt is exactly:

> Use ddtest in /trial/bin and onboard this repository to test-optimization.

`--framework <name>` adds only “for <name> only”. Let ddtest guide the agent.

### Scripted user answers

Some ddtest results ask a question only the user can answer (`next_action:
ask_user`, such as `ci_runtime_required`, `ci_setup_confirmation_required` or
`ci_provider_required`). A correct agent stops there, so a trial ends at the
question unless it has scripted answers:

```sh
  --user-answer 'ci_runtime=ruby=3.4' \
  --user-answer 'ci_confirm=jenkins-plugin' \
  --user-answer 'ci_confirm=jenkins-plugin-mode=agentless:datadoghq.com' \
  --user-answer 'ci_confirm=jenkins-agent-runtime-matches'
```

Forms: `ci_runtime=<runtime>=<version>` (every asked job) or
`ci_runtime=<workflow / job>:<runtime>=<version>`; `ci_confirm=<fact>` (yes),
`ci_confirm=<fact>=no` or `ci_confirm=<fact>=<value>`; `ci_provider=<provider>`;
`substitute_runtime=accept` accepts only `substitute_runtime_unaccepted`, including
a result that asks for that decision in `required_input` with `report_result`.
`--user-answers FILE` takes the same values, one per line (`#` comments).
Invalid values stop before cloning: versions are numeric as ddtest takes them
(Ruby with a minor version, such as 3.4), runtime names follow `--ci-runtime`
(`js` is `node`), and a `<value>` answers a question
only in one of the forms it lists, such as `agent` or `agentless:<site>`.

The `ddtest` in the prompt's folder is a wrapper that runs the real binary
(`/trial/harness/ddtest`) unchanged and copies its standard output to a record
outside the checkout. Setup, readiness and the agent get
`DDTEST_SELF_PATH=/trial/bin/ddtest`, so every `next_command` and
`resume_command` ddtest prints runs the wrapper. Before setup, the runner
checks this with an unconfirmed `testdrive --agent` in an empty repository. A
build that ignores the variable instead gets the real binary beside the wrapper
as `ddtest (deleted)`: it prints its path from `/proc/self/exe`, and Go trims
that suffix. `run.json` `ddtest_self_path` records the `mode`
(`DDTEST_SELF_PATH` or `deleted suffix`), the path the check printed and
`wrapper_printed`; when that is false, ddtest calls made through printed
commands are not recorded. The
runner moves each completed record out of the trial when the agent reports a
tool result, into `ddtest-results.jsonl` in the evidence (`turn` and the
redacted ddtest result, or null). So a result the agent redirected to a file or
reformatted still counts; without a record, the agent's tool output is used.
When the agent's turn ends and the last ddtest result of that turn asks the user,
the runner reads that result's `required_input` and, only if the script covers
every item, replies in the same conversation (`claude --resume`, `codex exec
resume`) as the user's next message. The reply states only the asked facts in
plain words (`Jenkinsfile / Run Tests runs Ruby 3.4.`, `jenkins-plugin: yes.`):
no flags, commands or other guidance, and nothing that was not asked. Otherwise
the trial stops as without a script. `run.json` `user_answers` records the
scripted values, `status` (`not requested`, `answered` or `unanswered`) and each
turn: the ddtest question and its `source` (`ddtest log` or `tool output`), the
agent's message, the answers used, the reply, the
missing items or the reason it stopped, and timestamps. `activity.jsonl` has
`user_question`, `user_reply` and `user_question_unanswered` events in order.
`--max-user-turns` (default 5) bounds the replies; `--timeout` applies to each
agent turn. A question the agent asks without such a ddtest result is not
answered. Scripted answers stand in for the user, so grade the trial as
uncoached only for what follows each reply, and note the answers in the
assessment.
Codex uses an empty MCP table in a private `CODEX_HOME`. The mandatory container
provides its isolation; the CLI runs without a second namespace sandbox, which
cannot start under the container's dropped capabilities. Never use that CLI mode
for untrusted repositories on the host.
Claude uses automatic permission mode, no setting sources, strict empty MCP
configuration and disabled auto-memory; any memory files an agent still writes
are listed under `agent_artifacts` in `run.json`. Its Bash tasks remain synchronous with framework-scoped timeouts.
Neither inherits the evaluator's CLI profile. Flag references:
[Codex](https://developers.openai.com/codex/cli/reference),
[Claude](https://code.claude.com/docs/en/cli-reference).

## Case catalog

`catalog/cases.yaml` is the standard set of cases: one pinned open-source
repository per supported framework, covering every CI provider, with its image,
setup, readiness, harness flags and answers, trims, measured timings, test
counts, prepared-image notes and risks. Its header documents each field.
`scripts/run_catalog.py` turns cases into `run_eval.py` arguments, runs them one
after another, copies each trial's `.testoptimization` reports and small
untracked files to `<evidence>/retained/` (the reports in the hidden
`<evidence>/retained/.testoptimization/`, such as `testdrive.json`; untracked paths come from
`status.txt`, with Git's path quoting decoded; Git never runs on the host in the
trial checkout), then runs `run_eval.py --cleanup`. Each trial's output goes to
`<id>-<agent>.log`. Trial code can replace any folder with a symlink, so
retention opens the checkout directly inside the trial folder named by
`run.json` and every path below it one folder at a time, never following a
symlink: a symlinked file or folder in the checkout is skipped (and listed),
and a checkout or trial folder replaced by a symlink retains nothing and is a
`harness_failure` (`retention`). `run_eval.py` reads the report the same way.
`<evidence>/retained/RETENTION.json` records the outcome (`started`, then `ok`
or `failed` with the error, plus what was skipped), and `catalog_check.py`
fails any status but `ok`, also on a later `--evaluate-only`; evidence from
before it existed has none, which is not a failure.
If copying fails (such as a full disk), cleanup still runs, the case is a
`harness_failure` (`retention`) and the batch goes on. A failed write to the
log is listed under the case's `notes` and never skips cleanup. An error in the
runner itself on one case makes it a `harness_failure` (`runner`, with the
exception and a traceback excerpt) after its cleanup, and the lane goes on; if a
lane stops anyway, each case it did not finish is a `harness_failure`
(`not_run`), so the batch can't exit 0.
Each trial runs in its own session: the first Ctrl-C (or SIGTERM/SIGHUP) is
forwarded once, and the runner waits for the trial to finalize `run.json` and
remove its container, retains, cleans up and stops the batch with a non-zero
exit. A second Ctrl-C kills the trial; cleanup still runs, and the runner
prints the `--cleanup` command to retry it.

Build the images that the selected cases name (`images` in the catalog lists
each key's target and build arguments), then map them. `--dry-run` needs no
`--ddtest`, images or output: it prints the exact `docker build` command for
each image key the selection needs (tagged `ddtest-eval-<key>`), a shell line
that writes their IDs to `images.json`, an `--image-file` template, and every
case's `run_eval.py` arguments, with `<ddtest>`, `<image:KEY>` and `<output>`
placeholders for what was not given. It builds nothing.

```sh
S=skills/ddtest-onboarding-eval/scripts
python3 $S/run_catalog.py --dry-run E01 E02    # image builds and each case's arguments
# run the printed docker build commands and the images.json line, then:
python3 $S/run_catalog.py E01 E02 --image-file images.json \
  --ddtest /absolute/path/to/ddtest-linux --ddtest-label "$(git rev-parse --short HEAD)" \
  --existing-login --output /absolute/path/to/new-evidence-folder
```

`--image-file` (or `DDTEST_EVAL_IMAGES`) takes a JSON or YAML mapping of image
key to image instead. `--timeout` defaults to 5400 seconds. `--agent` is
`codex` (the default for trials), `claude` or `both`. One agent is one lane: its cases run one
after another, because `--existing-login` locks that agent's login for a whole
trial. `both` runs a Codex lane and a Claude lane at the same time, each in
catalog order, so the same case can run in both lanes at once
(`<id>-codex` and `<id>-claude`). The two logins don't contend: Codex locks
`~/.codex/auth.json`, Claude locks `~/.claude/.credentials.json`, or takes no
lock when it uses a managed model helper. Trial folders, caches and containers
are per trial. Two lanes double the Docker load: on a small VM (4 CPUs, 8 GB)
timings get noisier, and heavy cases (browsers, JVM builds) may need more CPUs
or memory. A case whose evidence folder exists is not run and fails the batch
(`harness_failure`, `exists`: check that folder with `--evaluate-only`).
Status lines start with `[codex]` or `[claude]` and reach stdout as each case
starts and ends, also when it is redirected; each lane also writes them to
`<output>/lane-<agent>.log`. During a long run, follow that file
(`tail -f <output>/lane-claude.log`) and the running case's
`<output>/<id>-<agent>.log`. The first Ctrl-C reaches every running trial once;
both finalize, retain and clean up, and no lane starts another case.

Claude with `--existing-login` needs one of: a managed model helper
(`apiKeyHelper`, plus `ANTHROPIC_*` routing in `env`, in `~/.claude/settings.json`
or `/Library/Application Support/ClaudeCode/managed-settings.json`), or an OAuth
file at `~/.claude/.credentials.json`. On macOS, Claude Code keeps its own OAuth
login in the Keychain, which the harness never reads: without a helper, run that
agent's trials with `run_eval.py --login-json <exported file>`, or run Codex only. The model
preflight fails before cloning when neither is available.

Trims are setup commands that edit or `git rm` files; `run_catalog.py` signs and verifies
each trim inside the trial container using a disposable key and a scratch identity
(`ddtest-eval-scratch`), so setup still leaves `git status` clean. The trial's
private HOME has no Git configuration, and nothing changes signing settings.
`changes.patch` diffs against `agent_baseline`, the HEAD after the trim
commits and setup, so it holds only the agent's changes.
`trims.required` (the suite can't pass without it on the image, such as expired
certificates) always applies; `trims.optional_speed` applies only with
`--speed-trims`, and changes what the agent sees, so compare like with like.

### Faster repeated runs

Keep the full suite as the default. For frequent iteration, the existing
`--speed-trims` selects the catalog's reviewed smaller fixtures. For example,
E10's WebdriverIO fixture keeps five specs / 40 tests: a same-container
measurement took 139.2 seconds full and 12.8 / 11.4 seconds trimmed. A direct
trimmed testdrive passed paired 40/40 native results and all five supported
feature checks; skipping remained unsupported. This preserves the observed
DDTest checks, not every upstream assertion. Keep the same configurations,
runtime scope, supported/unsupported features and expected blockers, and use
the full catalog for release and fixture-change acceptance. A normal testdrive
runs the real suite twice and selects controlled probes afterward; do not
multiply a native suite saving by the total number of controls.

Dependency-cache images avoid repeating public package downloads without
reusing test results or compiled project output. Build one from the same
immutable base image that the case normally uses:

```sh
S=skills/ddtest-onboarding-eval/scripts
BASE=$(python3 -c 'import json; print(json.load(open("images.json"))["java17"])')
python3 "$S/prepare_dependencies.py" E17 --image "$BASE" \
  --ddtest /absolute/path/to/ddtest-linux --tag ddtest-eval-e17-cache \
  --output /absolute/path/to/new-cache-preparation
# Read the printed --dependency-image argument, or dependency-image.json:
CACHED=$(docker image inspect --format '{{.Id}}' ddtest-eval-e17-cache)
python3 "$S/run_catalog.py" E17 --image-file images.json \
  --dependency-image "E17=$CACHED" --ddtest /absolute/path/to/ddtest-linux \
  --existing-login --output /absolute/path/to/new-evidence
```

Preparation uses the normal container boundary and does not load model
credentials or start an agent. It runs setup/readiness, then the case's optional
`prepared_image.warmup_commands` in container-native throwaway copies. Java
warmups resolve compiler/provider/plugin dependencies a `go-offline` or
`testClasses` alone can miss. `<output>/evidence/run.json` says `status: prepared`;
it does not certify onboarding. The preparation trial is cleaned up even if building the
local image fails. Nothing is published.

Each base keeps a local `ddtest-eval-base:<image-id>` tag, recorded as `base_tag`
in `dependency-image.json`. This supports BuildKit and preserves an untagged
caller image after build failure. Repeated preparation reuses that base tag;
remove it only when retiring the base image itself.

The cache contains only allowlisted package caches, Maven/Gradle user-home
data, Ruby's private bundle and its generated Git-ignored lockfile. It does
not snapshot the checkout, `node_modules`, venv, CLI profiles, credentials,
tracers, reports or project build output. Every evaluation still clones the
pinned source and runs every trim, setup and readiness command before the
unchanged agent prompt. In particular, `npm ci` still creates a fresh install.
The `prepared_image.bake` notes also describe possible complete-install
snapshots; this command implements dependency caching only.

The cache fingerprint binds the public source commit, immutable base image,
harness settings, setup/readiness/warmup commands and selected trims. Supply
the original base `--image-map`/`--image-file` as well as each cached image.
Use `--speed-trims` for both preparation and evaluation, or neither. A stale
or mismatched cache is an environment failure before setup or the agent;
rebuild it instead of changing expected outcomes. Caches are copied into each
trial's private storage and never shared writable between agent lanes. E12's
repository-local Maven cache remains a distinct CI behavior: a warmed HOME
does not warm the `.m2/repository` its CI environment selects.

`run.json` now records a `phase` and monotonic `duration_seconds` for commands,
`timings_seconds` totals by command phase, container start/cleanup times and
model preflight time when used. Public activity events have `observed_at`
timestamps. The `agent` phase includes both model time and tool execution;
public event timestamps help distinguish them but are observation times,
not native test timers. Docker image builds/pulls happen outside trial time;
the runner still uses `--pull=never`. Both agent lanes keep their existing
one-trial-at-a-time behavior.

### Expected outcomes

`expected` is checked after every trial (`scripts/catalog_check.py`); the
catalog header documents its fields. `verdict` is the `summary.status` a correct
trial reaches. For INCOMPLETE and FAILED, optional `blockers` lists the
blockers the verdict may have, as identifiers derived from
`summary.blocking_checks`: `feature <name>: <status>`, `compatibility:
<status>`, `runtime fidelity: substitute Java`, `unresolved CI scope: <file> /
<job> / step <n>: <cause class>` (one per step; the class is the cause's
leading clause, such as `CI command expression was not resolved`, without the
step's command or ddtest's "Runs before the job's ... tests" context),
`configuration: <status>`, or a headline such as `Cypress real-suite coverage
was not observed`. `fnmatch` patterns such as `feature *: inconclusive` are
allowed, but prefer exact identifiers: a broad pattern also matches a new real
blocker. So INCOMPLETE for the known reason passes, and INCOMPLETE with a new
blocker fails. `expected.agents.<codex|claude>` overrides verdict, blockers or
reason for one agent; ddtest decides the verdict, so use it only for a known
agent difference. Every case's blockers are confirmed by the retained reports
of eval-claude-2026-11-04.

Each trial gets one outcome:

| outcome | meaning | fails the batch |
|---|---|---|
| `pass` | verdict and blockers match | no |
| `verdict_mismatch (worse)` | a regression; `STOPPED` means no report and no verdict in ddtest's last result | yes |
| `verdict_mismatch (better: update expected)` | an improvement | no, exit 3 |
| `unexpected_blockers` | the expected verdict with a blocker not allowed, or none of the expected ones | yes |
| `harness_failure` | container, clone, setup, readiness, login, timeout, interruption, retention, cleanup, a runner error, or missing or malformed evidence (such as a `testdrive.json` that is not valid JSON or lacks `summary.status` or `summary.blocking_checks`, or no report although ddtest's last result claims a verdict) | yes |
| `agent_failure` | no ddtest result or report, no final answer, or `run.json` `agent_reporting` is not `pass` | yes |

Expected blockers that are no longer reported only add a note. Source edits receive a conservative trajectory check; unmatched hunks require
independent review as described below.

At the end the runner prints a table (case, agent, provider, framework,
expected, actual, outcome), `NOTE <id>: agents disagree` when both agents ran a
case to different verdicts (agent behavior, not ddtest), and one block per case
that did not pass, `ERROR` (or `IMPROVEMENT`), with what failed, expected and
actual, ddtest's last result and blocking checks, the failing harness step
and its output, the evidence paths, and the next step. For a regression or
unexpected blocker the next step offers three choices: (1) a ddtest bug:
investigate, starting from the `ddtest-results.jsonl` row where each new
blocker first appears (often ddtest's own advice before the agent's edit); keep
`expected` so the regression stays visible; (2) an intentional ddtest change:
update the shared `expected`; (3) agent-specific: add `expected.agents.<agent>`.
A trial whose last ddtest result asks the user a question no `--user-answer`
form answers (such as `scope_decision_required`) gets the note `question not scriptable: <code>`,
`question` in its record and an entry in `questions_not_scriptable`. It writes the same data
to `<output>/results.json` (or `--results PATH`): `exit_code`, `outcomes`,
`agents_disagree` and, per case and agent, `outcome`, `direction`, `expected`,
`actual` (verdict, blocker identifiers, blocking-check excerpts, ddtest's final
reason, run status, ddtest version and label), `problems`, `notes`, `evidence`
paths and `next_step`, plus `ddtest_label`, `questions_not_scriptable` and
`not_evaluated`. Exit status: 0 when every trial passed, 1 when any did not, 3
when the only differences are improvements, 128+N when interrupted.

An existing `results.json` for the same output folder is merged into, not
replaced: each new record replaces the entry with the same case and agent,
other entries stay, and `outcomes`, `agents_disagree` and `exit_code` are
recomputed over all of them (`merged_entries_kept` counts the kept ones). With
`--all`, the batch replaces every entry of the agents it covers. A
`results.json` written for another output folder is refused; pass a new
`--results`. The process exit status covers only the cases of this invocation. The score and
baseline comparison also cover only its selected trial slots (including missing
ones); retained entries from other plans remain visible without entering that
sample.

`--evaluate-only` checks existing evidence and runs nothing: `run_catalog.py
--evaluate-only E02 --output <folder holding E02-pipeline-schema-codex>`.
Without `--agent`, each case's agents are the `<id>-<agent>` folders that exist.
A requested agent without a folder is listed as `no <agent> evidence (found:
...)` and is not evaluated; for a named case with `--agent codex` or `claude`,
or a named case with no evidence at all, the exit status is 1. With `--all` (or
`--agent both`), cases without a folder are only listed.

After an intentional ddtest change, rerun the affected cases before updating
the catalog. Copy what the block's next step names into the case: `verdict`, `blockers` (the
identifiers listed under actual, without the `configuration:` lines), `reason`,
`run` (the output folder or round, such as `eval-claude-2026-11-04`) and
`ddtest` (the `--ddtest-label` you passed, such as the commit; a `dev` version
names no build). A single-trial calibration can be rechecked with
`--evaluate-only` against the updated expectation; this is calibration, not an
independent score. A repeated run keeps its original expectation and oracle:
retain a copy of the catalog used before execution and pass it with `--catalog`
when reevaluating those reports. Update expectations for future fresh output
folders; improvements stay visible in the original repeated-run score.

Oracle schema 3 freezes `trials_per_case_agent` and whether `--case-image` was selected. Reevaluation and comparison
require the original trial count, including when requesting a single trial in
a repeated-run folder. Older oracles without a planned count remain unchanged;
use their original evaluator for historical reevaluation, and start a new output
folder for a new experiment rather than rewriting the old oracle.

To add or replace a case:

- Keep one case per framework in `run_eval.py`'s `--framework` list, and keep
  every provider (GitHub, GitLab, CircleCI, Buildkite, Jenkins) covered. The
  current cases predate Java on Jenkins, so no JVM case runs on Jenkins yet;
  prefer one when replacing a JVM case.
- Pin a full commit whose own CI is green and whose suite passes on the image,
  outside any agent. Prefer small, active, public repositories without services,
  secrets or live network calls in tests.
- Add a trim to `required` only when the suite fails without it; put
  speed-only trims in `optional_speed`. Run every trim through `shlex.split`
  with no shell on a fresh clone: it may only edit or remove tracked files,
  which the trim commit (`git commit -S -a`) records.
- Measure clone, install, browser, build and test times on the image and record
  the conditions. Add answers only for questions only the user can answer.
- Run `python3 -m unittest test_run_catalog test_catalog_check` in `scripts/`;
  they validate the schema, the expectations and the framework and provider
  coverage.

## Prepared case images

Use one immutable image per case. Build the checkout at its catalog commit,
apply its required trims, and install only the project's dependencies and caches.
Do not install ddtest, the tracer, or dependencies ddtest will prescribe. Do not
bake credentials, personal settings, model configuration or evaluator evidence
into an image. The ddtest binary is still mounted separately for every trial.

Generate and build them with the same base-image mapping as ordinary trials:

```sh
python3 scripts/prepare_case_images.py E04 E07 E14 E17 --image-file images.json --output ~/ddtest-work/case-images --build
```

Run this from the skill directory. Omit `--build` to generate Dockerfiles and
print their commands. The builder applies required trims with signed commits,
checks readiness and clean status, and refuses an installed tracer. It keeps a
content-addressed local base tag for BuildKit; the manifest still records the
requested immutable image ID. Its credential-free Docker configuration permits
the installed Buildx executable without copying registry credentials or builder
state, so personal registry credentials are not consulted.
`case-images.json` preserves earlier cases when adding more images.
Prepared case images and dependency-cache images are separate modes; do not
combine `--case-image` with `--dependency-image` or cache preparation/restoration.

Put the prepared checkout (including its Git history and ignored project
dependencies) at `/opt/ddtest-case/repo`, and this JSON at
`/opt/ddtest-case/manifest.json`:

```json
{"schema_version":1,"repository":"<catalog repo URL>","commit":"<full source SHA>","baseline":"<full HEAD SHA after signed trims>","framework":"<catalog framework>"}
```

The baseline must descend from the source commit and have clean Git status.
Record required trims in signed commits; never disable signing. Cache payloads
may be placed under `/opt/ddtest-case/caches`, `tools`, `venv`, `bundle-bin`,
`home/.m2`, `home/.gradle`, `home/bundle` and `home/.bundle`. Prepare absolute
paths for `/trial` (Python environments use `/trial/venv`), where the runner
restores them. Model authentication and configuration are never seeded.

Pass `run_eval.py --case-image --image <immutable ID> --repo <URL> --ref <full SHA>`
with the usual binary, framework, agent, runtime and output flags; omit setup
commands. The runner verifies the running image ID, manifest, baseline, source
ancestry, origin and clean status. It skips cloning, dependency installation,
Python environment creation and binstub generation, then runs readiness and
the uncoached trial. `run.json.prepared_image` retains the verified provenance.
There is one prepared form per case; no cold/cached variants.

For a catalog batch, use `run_catalog.py --case-image` and map case IDs, rather
than shared runtime image keys, to immutable IDs in `--image-file`.

## Repeated trials and version comparisons

Use `run_catalog.py E04 E14 E17 --agent both --trials 3` with the usual image,
binary, login and output flags. Every trial has a fresh checkout, private
caches and a separate `<case>-trial-<nn>-<agent>` evidence folder. The two agent
lanes remain independent. Before execution, `oracle.json` freezes the source
commits, setup, images, scripted answers, an explicit `--model` selection and
expected verdicts/blockers. A
changed oracle requires a new output folder; do not revise expectations after
observing the same trials and then call their score independent.

`results.json.score` includes sample counts and pass rates per case/agent and
across trials. `expected_pass_rate` measures agreement with the predeclared
outcome (including expected FAILED/INCOMPLETE outcomes); `rigorous_pass_rate`
also requires a passed trajectory check. Reevaluating a selected slot replaces
its previous result, including when its evidence is now missing. Missing trials
and failures stay in the denominator. `complete_locally` is reported separately. These small-sample
rates are descriptive, not evidence of statistical significance. Use explicit
model selection for version comparisons; service defaults can change and some
CLIs do not expose their resolved model.

Pass `--compare-results <earlier results.json>` to compare two ddtest versions.
The oracle and case/agent sets must match, including the immutable images. Before
scoring a frozen run, reevaluation checks every selected trial manifest against its
planned repository, source commit, image, intake guard, agent, requested model and
prepared-image mode. Missing or mismatched
provenance stops reevaluation without rewriting the frozen oracle or evidence.
Oracles that predate preparation-mode provenance require their original evaluator;
do not infer the missing mode or rewrite historical manifests.
The result records both labels and changes in the two pass rates.

Each trial's `trajectory.json` checks the source diff, including retained
untracked source files. Exact hunks from ddtest's recorded patch prescriptions
pass automatically. Patch capture preserves carriage returns and undecodable
bytes before grading. Tracked patches that are not valid UTF-8 require independent
review; replacement characters cannot authorize different raw bytes. Metadata
phrases inside text hunks are ordinary text; actual binary, mode, rename and copy
headers still require review. Hunk identities preserve content carriage returns and other
line separators: an LF prescription does not authorize a CRLF edit. Preserve
historical results with their original evaluator rather than rewriting frozen
evidence to match new identities. Other hunks require an independent evaluator's
review; mentioning a filename in instructions never authorizes all edits to it. Added or
deleted regular text files use the same checks, with their file mode bound to
each hunk. Retention preserves untracked files' executable bits and records them
per file in `retained/RETENTION.json`; their review IDs bind the path, contents and
executable bits. A permissions change requires a fresh capture and review. Older
retained copies without executable-mode provenance stay unverified under the new
checker: preserve their historical results with the original evaluator instead
of inventing permission evidence. Binary, mode-only or unavailable changes stay unverified. A repeated batch exits nonzero
while any trajectory is unverified. A trajectory parsing or read error leaves
that trial unverified and records its reason; evaluation continues for the other
trials and saves the batch results. After inspecting a hunk against the original
prescription, save `trajectory-review.json` beside it:

```json
{"patch_sha256":"<trajectory.json value>","prescriptions_sha256":"<trajectory.json value>","hunks":[{"id":"<hunk id>","row":1,"evidence":"<literal excerpt of that ddtest result>","verdict":"prescribed"}]}
```

The two hashes bind the review to the current patch and prescription log; each
row and literal evidence excerpt must exist. Non-JSON ddtest instructions are
retained as `output` in the prescription log. For older evidence where those
instructions exist only in activity.jsonl, a reviewed hunk may instead name
`activity_event` (a 1-based event number), with `activity_sha256` on the review.
The independent reviewer must verify that event is the output of ddtest itself. Reevaluate with the same case,
agent, image, `--trials` and output options plus `--evaluate-only`. The independent
reviewer must check the meaning of the change; matching a cited excerpt alone
is not a semantic proof.

When the automatic reporting check requests review only because a faithful
paraphrase lacks the unchanged canonical text, the independent evaluator may
save `reporting-review.json` with `verdict: "pass"`, a concrete `rationale`,
`final_response_sha256` (of final-response.md) and `report_sha256` (of the retained
.testoptimization/testdrive.json). Reevaluate afterward. Explicit contradictions,
unsupported claims and CLI errors cannot be overridden this way; run.json's
automatic assessment stays unchanged. This review belongs to the evaluator outside the
container, and is never supplied by the tested agent.

## Grade independently

Read `run.json`, `activity.jsonl`, `ddtest-results.jsonl`, `changes.patch`, `status.txt`, the final answer,
and both retained ddtest reports, which ddtest keeps in the checkout root's
`.testoptimization` also for a package below the root; the reporting check reads
that pair and fails links to any other. Public tool activity includes file, web and MCP
targets; reasoning is excluded. Text is redacted before bounded excerpts: each
keeps its start and end, up to `--excerpt-limit` characters (default 64000), and
4000 once `activity.jsonl` reaches 64 MiB (`activity_excerpts` in `run.json`).
Variable references such as `${DD_API_KEY:-}`, `$DD_API_KEY`,
`${{ secrets.DD_API_KEY }}` or `credentials('id')` are kept; literal values are
redacted. Every shell parameter expansion of a secret-named variable is kept
(`${DD_API_KEY:+set}`, `${DD_API_KEY:?message}`, `${DD_API_KEY#prefix}`,
`${#DD_API_KEY}` and so on) except a literal default (`-`, `:-`, `=`, `:=`),
the only operand that supplies the value; recognized token shapes are redacted
anywhere. Values that are clearly not secrets stay: an object or array opener
(`"token": {`), a boolean or null, a version or range (`^9.0.2`), or a package
path or specifier. A lockfile package key such as `"node_modules/jsonwebtoken"`
is not a secret name. Treat
redaction as a precaution: inspect evidence before sharing it, and do not archive
credentials, raw telemetry streams or dependency trees.

Use [product validation documentation](../../docs/onboarding.md) for framework
rules and supported/deferred features. Do not maintain another framework policy
in this skill. Grade each criterion pass, fail, blocked or not exercised, with
concrete evidence:

- **CI configuration:** inspect the actual diff and runtime/tracer agreement.
  Actual CI execution remains not exercised.
- **Compatibility:** require the same suite with and without tracing, with
  behavior-changing features disabled. Equal existing failures can be compatible.
- **Feature controls:** require the product's supported controls and independent
  outcomes. Deferred/unsupported checks never count as passed.
- **Reporting:** prefer the copyable `summary.final_response`, but accept factual paraphrases;
  compare additional claims against `summary.status`, `local_validation`,
  `blocking_checks`, selected/executed validation, configuration coverage,
  `reporting_counts` and `last_execution.current`. Historical counts must remain
  historical. Missing canonical text needs review; direct contradictions fail.
  "Unique"/"distinct" test counts and success claims on a report that isn't
  `COMPLETE LOCALLY` need review even when the numbers match. Success claims
  include a bare "Onboarded."/"Done."/"All set.", the repository or Test
  Optimization said to be onboarded or set up, and "I've onboarded…"; a stated
  onboarding verdict ("Onboarding is done") that contradicts the report fails.
  Report text the agent quotes (code, blockquotes, quotation marks or a
  sentence the report contains), and negated or conditional statements, are not
  claims.
- **Cleanup:** assess ddtest session cleanup, agent artifacts at CLI exit and
  evaluator teardown separately. A clean Git diff alone is insufficient.

The automated reporting check is only one criterion. An exit code or polished
completion claim cannot establish successful onboarding. Write a concise
`assessment.md` beside `run.json` with repository/commit, binary/source identity,
agent/CLI/model/image, prompt, criterion verdicts and supporting commands,
product/agent/environment failures, cleanup and local-only limitations.

## Interruptions and teardown

SIGINT/SIGTERM/SIGHUP and deadlines finalize evidence and remove the whole
container, including detached descendants. The controller's input connection also
ends the container if the controller dies. Setup failures never start a model
session. Recognized transport failures have `failure_kind: infrastructure`; only
then, and only if the user allows it, preserve the first result and make at most
one fresh retry with unchanged inputs. Do not automatically retry authentication,
permission, product or reporting failures.

Preserve the report pair and small relevant untracked configuration files before
teardown; `changes.patch` does not include untracked contents. Private caches are
owned trial data: remove them together, not by scanning or hashing their entries.

```sh
python3 skills/ddtest-onboarding-eval/scripts/run_eval.py --cleanup /absolute/path/to/evidence
```

Cleanup checks the output's `run.json`, its independent ownership record and the
trial marker, expected trial root and `dde-` prefix. Symlinks, mismatched markers
and unrelated paths are refused. Containers are created with `--rm`, so a runner
killed with SIGKILL leaves no container. Its trial folder stays, with
`ownership.json` but no `run.json`; `--cleanup` then works from the ownership
record. Whenever container removal isn't confirmed, cleanup first removes the
recorded container, and only if it mounts that trial; otherwise it refuses. Evidence,
other trials, original repositories and shared caches remain untouched. Never
reuse a modified clone for the next trial.

For model-free QA, build the Dockerfile's `offline` target instead: it contains
fake `codex`/`claude` executables that record argv/environment and public tool
activity. Omit `--login-json`. The CI tests use only these fakes, local Git trees
and current Go-generated reports; they never run real model sessions.
