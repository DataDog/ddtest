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

Use Python 3.9+, Git, Go for building ddtest, and Docker with the trial root
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
trusted derived image with the project-compatible Ruby and Bundler installed.
The runner records its Ruby version before cloning and checks the report against
that version. Install dependencies in the private trial HOME (for example,
`bundle config set --global path /trial/home/bundle`, then `bundle install`) and
use `bundle check` as the readiness command. For large Ruby matrices, use the
container-native private `/tmp/bundle` as Bundler's path to avoid bind-mount
filesystem overhead; it is removed with the container, while lockfiles and both
reports stay in the checkout until collection. Ruby's `TMPDIR` also uses the
container's private executable tmpfs; no host cache is mounted there.
For `--framework junit4`, `junit5`, `testng`, `spock` or `cucumber-java`, build `--target java`: it adds a
digest-pinned Temurin JDK 21 (select another with
`--build-arg JDK_IMAGE=eclipse-temurin:<version>-jdk@sha256:<digest>`) and a
checksum-verified Maven. The runner records the JDK's release version before
cloning and checks the report against it; local validation certifies only CI
rows of that JDK major. Maven downloads dependencies into the private trial
HOME during validation, so no setup command is required; `mvn -B -q
dependency:go-offline` works as a readiness command. Java's `TMPDIR` uses the
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
GOOS=linux GOARCH="$ARCH" go build -o /absolute/path/to/ddtest-linux .
```

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
The runner mounts storage at the short container path `/trial`, explicitly fetches the ref, records its resolved SHA, and creates a
fresh clone and private caches every time. Submodules are opt-in with
`--submodules`. Setup arguments use `shlex` without an implicit shell; setup must
leave tracked/untracked Git state clean. Optional `--node` selects an absolute
**image** executable for Codex, not a host binary. The recorded final ddtest
runtime must match it. Optional `--model` selects a model; otherwise the fresh
CLI default applies. `run.json` records the CLI version and requested model.

The tested prompt is exactly:

> Use ddtest in /trial/bin and onboard this repository to test-optimization.

`--framework <name>` adds only “for <name> only”. Let ddtest guide the agent.
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

## Grade independently

Read `run.json`, `activity.jsonl`, `changes.patch`, `status.txt`, the final answer,
and both retained ddtest reports. Public tool activity includes file, web and MCP
targets; reasoning is excluded. Text is redacted before bounded excerpts. Treat
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
  `COMPLETE LOCALLY` need review even when the numbers match.
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
