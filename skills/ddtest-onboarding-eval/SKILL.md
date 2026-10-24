---
name: ddtest-onboarding-eval
description: Compare uncoached Codex or Claude onboarding trials in fresh repositories, independently grade their ddtest evidence, and clean up owned trials. Maintainer tooling; not customer onboarding instructions.
---

# ddtest onboarding evaluation

**Run untrusted repositories and auto-approved agents only with the container
runner below. Use a clean trusted image, no cloud/GitHub/npm credentials or MCP
servers, and an explicitly selected throwaway agent login. A CLI permission mode
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
commit, binary and image. Record ddtest's source commit and dirty state outside
the target checkout.

Build the trusted image (add project-specific OS/browser dependencies to a
reviewed derived image if needed). Never bake personal configuration or secrets
into an image. The supplied image pins Node, pnpm, Codex and Claude; unexpected
CLI versions fail before cloning. The binary must also start inside the image;
its version and hash are recorded before a model session begins.

```sh
docker build --target agents -t ddtest-eval \
  -f skills/ddtest-onboarding-eval/scripts/Dockerfile \
  skills/ddtest-onboarding-eval/scripts
IMAGE=$(docker image inspect --format '{{.Id}}' ddtest-eval)
ARCH=$(docker image inspect --format '{{.Architecture}}' "$IMAGE")
GOOS=linux GOARCH="$ARCH" go build -o /absolute/path/to/ddtest-linux .
```

Use a login JSON from a **throwaway account**. `--login-json` copies only Codex's
`tokens` or Claude's `claudeAiOauth` into a private home, after setup completes.
It never copies settings, hooks, skills, histories or MCP configuration. No API
key, SSH agent, parent Claude session or credential environment is forwarded.
That selected login is accessible to the tested agent; it must not grant access
to any real customer or developer account. It is written after setup without
following links out of the trial, and deleted from the retained trial at
teardown. Forwarded environment values reach the container through a private
`--env-file`, never the `docker exec` command line; never pass a secret as a
plain command argument (`--setup-command`, `--repo`), where `ps` can read it. The image must contain no system-wide
agent configuration. Container trials run as the host UID with dropped
capabilities, a read-only root and only one writable bind mount: their own trial.

## Run

```sh
python3 skills/ddtest-onboarding-eval/scripts/run_eval.py \
  --image "$IMAGE" \
  --repo https://github.com/moment/luxon.git --ref <chosen-commit-or-ref> \
  --ddtest /absolute/path/to/ddtest-linux --agent codex --framework jest \
  --login-json /absolute/path/to/throwaway-codex-auth.json \
  --setup-command 'npm ci --no-audit --no-fund' \
  --output /absolute/path/to/new-luxon-codex-evidence
```

Repeat with `--agent claude`, its selected login and a new output directory.
The runner explicitly fetches the ref, records its resolved SHA, and creates a
fresh clone and private caches every time. Submodules are opt-in with
`--submodules`. Setup arguments use `shlex` without an implicit shell; setup must
leave tracked/untracked Git state clean. Optional `--node` selects an absolute
**image** executable for Codex, not a host binary. The recorded final ddtest
runtime must match it. Optional `--model` selects a model; otherwise the fresh
CLI default applies. `run.json` records the CLI version and requested model.

The tested prompt is exactly:

> Use ddtest in /temporary/trial/bin and onboard this repository to test-optimization.

`--framework <name>` adds only “for <name> only”. Let ddtest guide the agent.
Codex uses automatic approval and an empty MCP table in a private `CODEX_HOME`.
Claude uses automatic permission mode, no setting sources and strict empty MCP
configuration. Its Bash tasks remain synchronous with framework-scoped timeouts.
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
- **Reporting:** require `summary.final_response` unchanged in the final answer;
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
