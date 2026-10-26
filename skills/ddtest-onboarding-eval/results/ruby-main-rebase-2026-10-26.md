# Ruby rebase evaluation — 2026-10-26

This evaluates #193 after moving its Ruby-only commits from the closed #152
branch onto main `c2782fd0af360ba2fb4baa24b907276c37e248b1`.
Actual GitHub CI and Datadog backend processing were not exercised. Local runs
use ddtest's mock intake, without Datadog API keys.

## Rebase fixes and automated validation

The Ruby adapter now uses main's framework capability registry, explicit run
roles, configuration isolation/recovery and plain-run reporting. Ruby structured
coordination remains unsupported. A successful plain validation says `PASSED`,
not `COMPLETE LOCALLY`.

Native checks exposed and corrected Ruby tracer aggregation without JavaScript
selection metadata, whitespace left in resolved RUBYOPT, strict coverage paths
through symlinked temporary roots, and fallback Bundler environment propagation.
The evaluator retains main's container boundary and now records/checks Ruby
runtime evidence. Its private Docker environment forwarding supports multiline
managed Claude model headers without placing their values in argv or an env
file line. Invalid environment names/NUL values remain rejected.

- Native RSpec/Minitest CLI integration: passed, including all six features,
  anonymous/parameterized identities, equal pre-existing failures, scoped CI
  exclusions, tracer/runtime mismatch, check-only historical evidence, missing
  lockfile preservation and cleanup.
- Ruby/configuration regression tests: passed.
- `make lint`: passed, 0 issues (task-local golangci-lint 2.14.0).
- Evaluator suite: 93 tests total, 84 passed and 9 optional offline-image Docker tests skipped.
  A separate real-container multiline environment canary passed without credentials.
- `make test`: fails only `TestMochaNestedGuardDisablesTracingOnlyForNestedCLIs`.
  The same failure was reproduced on unchanged main at `c2782fd`:
  the owner worker reports `true true mocha mocha` rather than `true true - -`.
  This existing Mocha failure was not changed in the Ruby PR.

## Controlled experiment

The final comparison uses clean source
`aca8bd469ee84da4af71e2c1e1b2cb5aef3606df`, Linux arm64 binary SHA-256
`e6d7ac586ad9433a77ef6c6d4863ee130655a4bb4184d5314cddd7c0fe06162a`,
and immutable image
`sha256:004d012b985de5b49b052c7fbcc843ccdaae0c9101bc3d0c73cdf0c5dc38b52f`.
Ruby 3.3.11, Bundler 2.5.22, Node 24.19.0, Codex 0.163.0 and Claude 2.1.220
are the same for both agents. CLI default models were used without a model
selection flag. Existing CLI model logins were explicitly authorized by the
user and selected without host profiles, hooks, MCP servers or other credentials.

Each trial used a fresh pinned clone with private dependencies/caches in a
container with a read-only root, dropped capabilities, no host Docker socket,
4 CPU / 6 GiB limits and a 2 GiB executable temporary filesystem. The dedicated
Colima VM has four native arm64 vCPUs, no Datadog Agent/database service or
instance credentials. Pairs share that VM; timings are not a performance comparison.
Egress is unrestricted as documented by the evaluator skill.

The only task prompt was:

> Use ddtest in /trial/bin and onboard this repository to test-optimization for FRAMEWORK only.

No follow-up coaching or grading rubric was supplied. Public activity, diffs,
final answers, report pairs and per-trial assessments were reviewed independently.

| Project | Pin | Framework / original suite |
| --- | --- | --- |
| RubyMoney/money | `1d60efeaeea009cfc69374421561b23b851bd36e` | RSpec / `bin/rspec` |
| rails/globalid | `3155f04179c59bc79a6c60b01a40ee296905f069` | Minitest / `bundle exec rake test` |
| ruby-i18n/i18n | `547917dd8d41fab781a81880f22687fc4eac5d85` | Minitest / `bundle exec rake` |

## Final trial outcomes

A completed CLI session is not a passed onboarding. All rows below use the final
fixed binary. Counts are native test executions per mode, not unique tests across
Gemfiles. Receiving coverage for reported events does not excuse missing native
results.

| Project / agent | Native comparison | Features | Product result / scope |
| --- | --- | --- | --- |
| Money / Codex | 502 passed in each mode; source coverage observed | 6/6 | `PASSED`, 1/1 configuration |
| Money / Claude | 502 passed in each mode; source coverage observed | 6/6 | `PASSED`, 1/1 configuration |
| GlobalID / Codex | Root bundle: 175 native passes, but only 172 matching reporting results | 6/6 for root only | `INCOMPLETE`; five CI Gemfiles not exercised |
| GlobalID / Claude | Five pairs: 875 executions per mode; only 171–172/175 results reported per Gemfile | 30/30 | `INCOMPLETE`, 0/5 validated despite all five executing |
| i18n / Codex | 1,607 passed in each mode; source coverage observed | 6/6 | `INCOMPLETE`; original default Rake CI scope unresolved |
| i18n / Claude | Root: 1,607; five Rails Gemfiles: 1,888 each; 11,047 passed per mode | 36/36 | `PASSED`, 6/6 configurations; CI edits need review |

Money's original matrix and `bin/rspec` stayed intact. Both agents pinned
`datadog-ci` 1.42.0 and explicitly excluded head/JRuby instrumentation while
preserving their tests. Numeric Ruby selections were statically compatible.

GlobalID retains a real reporting gap in Rails-isolated railtie tests. Four
Gemfiles have 175 passes; Rails main has 174 passes and one pre-existing signed-ID
assertion failure in both modes. Equal native results do not establish telemetry
parity. The tracer's asynchronous writer and Rails fork/exit! isolation are a
plausible explanation, not a verified fix in this PR. No test was deleted or
changed to bypass the gate. Ruby 2.7 instrumentation was excluded after the local
precompiled ffi dependency required Ruby 3.0+: this does not prove every possible
Ruby 2.7 dependency solution is incompatible, so the exclusion needs CI review.
One intermediate Claude attempt hit host/filesystem FD exhaustion during Bundler
installation. It independently reran within the same trial; the final run still
correctly remains incomplete for missing telemetry. First/final full reports and
public activity retain these failures.

i18n exposes the conservative default/custom Rake rule, unchanged from the
pre-rebase branch. Codex kept `bundle exec rake` and reported incomplete CI scope.
Claude changed it to `bundle exec rake test`; independent Rakefile inspection
confirms `task :default => [:test]`, so it runs the same target. Claude also quoted
Ruby matrix values and corresponding include/exclude entries to avoid static
mixed-type comparisons, prepared all six bundles and passed all six local
configurations. These CI changes require review and must not be described as
preserving the literal entry point. No tests, Rakefile, Gemfiles or gemspecs were
changed. Fresh dependency resolutions, including Rails Git dependencies, are not
a dependency-lock reproducibility benchmark.

## Agent reporting and cleanup

All six final answers omitted the verbatim canonical `summary.final_response`.
Independent review accepts factual paraphrases with explicit limits, but found:

- GlobalID/Claude says all 175 tests passed across the matrix, contradicting the
  Rails-main failure. Its incomplete verdict is accurate; that count claim is not.
- i18n/Claude says onboarding is complete despite a plain `PASSED` result.
  The automated evaluator correctly records `agent_failed` / `agent_reporting`;
  no retry was made. This does not erase the successful 6/6 native validation.
- Several answers say “onboarded” or suggest inability to execute real CI causes
  local incompleteness. Actual CI/backend execution is a separate local-only
  limitation, not a waiver for a failed native, scope or telemetry check.

Claude used shell backgrounding and waits for i18n despite the intended synchronous
Bash setup. All runs finished before CLI exit; no known unfinished background task
remained and container teardown stopped descendants. This is retained as an
execution-discipline review item. The six-configuration HTML report is about 78 MB.

All ddtest session-cleanup verdicts passed. Agent exit diffs contain CI edits and,
for Money/Claude and i18n/Claude, report ignore rules; other trials left only the
two expected report files untracked and need ignore review before committing in a
customer repository. Relevant agent dependency-analysis scratch was removed.
Every report pair and assessment was retained outside the clone, then all nine
owned trials (six final comparisons and three earlier attempts) were removed
through ownership-checked teardown. No other checkout or shared cache was removed.

Local evidence: `/tmp/ddtest-ruby-main-20261026.wg8aUh`, including `experiment*.json`,
per-trial `run.json`, public activity, diffs, final answers, report pairs and
`assessment.md`. Evidence is local, not a published report archive. Current source
and binary identities are above; earlier source hashes remain historical.

## Preserved earlier attempts

The initial clean rebased binary at `dfc8245` exposed the fallback bundle bug in
Money/Codex: the temporary tracer installed successfully, but native execution
could not find its gems. The uncoached agent installed the same release into its
trial-private existing bundle and then passed 502 native tests and all six
features. This was not counted as a clean first-attempt success. The product
fix is covered by a regression check and the fresh fixed-binary Money trials.

The first Claude attempt stopped before model contact because Docker env-file
handling rejected its managed login's multiline headers. The user authorized a
fresh attempt after the evaluator fix. One fixed-binary Codex attempt likewise
stopped before cloning/model contact because another evaluation held the shared
login lock; the user authorized fresh trials after its release. Both stopped
results remain in the evidence, with no product or authentication success claimed.

## Reproduction

Use the container-only [evaluator skill](../SKILL.md). Prepare a clean trusted
image containing Ruby/Bundler and the skill's pinned agent CLI versions; never
bake credentials into it. The evaluated derived image used:

```dockerfile
FROM ruby@sha256:0e196c23856f88f5e6e6a9a76cc983e5affdcf34558d9409d33294e2a9058be5 AS ruby_runtime
FROM node:24.19.0-bookworm@sha256:4196d66a565c6f195728d9952f161f4adfe2ad753052a08b7ec7f1c5a6bda42b
RUN npm install --global pnpm@12.14.0 @openai/codex@0.163.0 @anthropic-ai/claude-code@2.1.220 && npm cache clean --force
COPY --from=ruby_runtime /usr/local/ /usr/local/
```

Build the branch for the image's Linux architecture. For each project and each
agent use a new evidence directory, the same binary/image and the exact pin:

```sh
python3 skills/ddtest-onboarding-eval/scripts/run_eval.py \
  --image <immutable-image-id> --repo <repository-url> --ref <pin> \
  --ddtest /absolute/path/to/ddtest-linux --agent <codex-or-claude> \
  --framework <rspec-or-minitest> --existing-login \
  --setup-command 'bundle config set --global path /trial/home/bundle' \
  --setup-command 'bundle install --jobs 2 --retry 2' \
  --readiness-command 'bundle check' \
  --output /absolute/path/to/new-project-agent-evidence
```

`--existing-login` requires explicit account-owner authorization. Preserve the
report pair and relevant configuration changes outside the clone before calling
`run_eval.py --cleanup <evidence-directory>`. The runner verifies ownership;
do not manually delete other checkouts or scan shared dependency caches.
