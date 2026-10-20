# Ruby completion follow-up — 2026-10-19

Money and GlobalID now reach **COMPLETE LOCALLY** with both Claude Code 2.1.220
and Codex CLI 0.163.0. This covers the discovered local commands/environments,
native comparisons, feature controls, application coverage and static CI runtime
checks. Actual CI execution and Datadog backend processing remain untested.

## Changes that enable completion

- Resolve Ruby matrix `BUNDLE_GEMFILE` paths containing `github.workspace` and
  static matrix values into repository-local paths. Preserve each Gemfile as a
  separate configuration; reject escaping paths and unresolved expressions.
- Check numeric Ruby selectors against RubyGems requirements across their stable
  releases, including requirement boundaries, exclusions and pessimistic ranges.
  An ambiguous selector remains inconclusive. Require an exact tracer release
  matching local validation.
- Inspect the resolved tracer's runtime dependency tree. A locally installed
  Rails or development-tool version does not describe every CI application bundle.
- Recognize explicitly skipped Ruby instrumentation without removing test jobs.
  Preserve the original matrix and require at least one compatible instrumented
  consumer; excluding every entry cannot manufacture completion.
- Explain Ruby coverage/setup failures using the selected Gemfile and runtime,
  replacing the misleading Mocha/NYC remediation. Keep all existing evidence gates.

GlobalID's matrix resolves to five local Gemfile configurations with all 34 CI
consumers retained. Its existing matrix excludes Ruby 3.4 with Rails 6.1/7.0;
Ruby **3.3.11** supplies a runtime admitted across all five Gemfiles. No project
Ruby versions, tests, Gemfiles, tracked lockfile or Rakefile were changed to pass.

## Controlled retests

Each cell represents a fresh clone, the same pinned project and binary for both
agents, private dependency directories, default models and the skill's minimal
prompt. No follow-up coaching, valid Datadog credentials or Datadog MCP access was
supplied. Both agents ran automatic `ddtest onboard` detection and full `testdrive`.

| Project / runtime | Claude | Codex | Native execution per agent | Feature checks per agent |
| --- | --- | --- | --- | --- |
| Money / Ruby 3.4.9 | COMPLETE LOCALLY | COMPLETE LOCALLY | Existing `bin/rspec`: 502 passing executions in each mode | 6/6 passed |
| GlobalID / Ruby 3.3.11 | COMPLETE LOCALLY | COMPLETE LOCALLY | Existing `bundle exec rake test`: five Gemfiles, 175 executions per Gemfile in each mode | 30/30 passed |

GlobalID totals **875 executions per mode across configurations**, not 875 distinct
tests. Rails 6.1, 7.0, 7.1 and 8.0 each have 175 passing tests and exit zero.
Rails main has 174 passing tests and **one matching pre-existing assertion failure**
in both modes, with exit one. Compatibility accepts matching existing failures;
it does not claim that this application suite is green. Native outcomes and
telemetry agree in the final reports. Source-test identity counts are unavailable
where native file/name identities repeat.

Real application coverage was observed for all reporting results. Each Gemfile
has its own positive/negative controls and independent probe-body counts for
retries, EFD, skipping, quarantine, disabled tests and attempt-to-fix. Synthetic
probes do not appear in the retained suite reports.

Static CI scope preserves the original test steps and matrices. Money instruments
numeric Ruby 3.1–4.0 and explicitly excludes head/JRuby instrumentation. GlobalID
instruments numeric Ruby 3.0–4.0 and excludes Ruby 2.7, head and TruffleRuby
instrumentation. The 2.7 exclusion reflects this locally resolved tracer
dependency tree's Ruby >=3.0 requirement; it does **not** establish that other
dependency selections or tracer releases cannot support Ruby 2.7. Every excluded
entry still runs its original tests. These are static checks, not execution on
every Ruby engine, platform or CI dependency resolution.

## Earlier attempts and remaining limitations

- The original Ruby 3.4.9 GlobalID/Claude trial remains INCOMPLETE: four of five
  configurations validated; Rails 6.1 fails before native results. A separate
  focused Ruby 3.2.2 check does not upgrade that final all-configurations report.
- The original GlobalID/Codex trial independently built a private Ruby 3.3.11
  and reached 5/5. The evaluator correctly flagged a mismatch with its requested
  Ruby 3.4.9. That protocol failure is retained separately from the two fresh
  trials pinned to 3.3.11 from the start.
- An earlier controlled Codex run missed telemetry for the failing isolated
  Rails-main test and correctly stayed INCOMPLETE. The required full rerun after
  CI instrumentation-scope edits matched all events. Earlier Claude runs also
  observed Rails-main telemetry/initialization intermittency. **This reliability
  issue is not claimed fixed**; retain missing-event results and investigate if
  it recurs. Do not drop the isolated test or relax telemetry matching to pass.
- Money/Codex's final answer omitted configuration ratios and unavailable source
  identities; its factual-reporting grade remains review despite the correct
  technical verdict. Both controlled GlobalID final answers retain the complete
  verdict/count block. Claude's broader assertion that no pinned release can
  cover Ruby 2.7 and 4.0 was not established by these tests.
- The original GlobalID/Claude trial used predictable `/tmp` scratch filenames
  and wildcard cleanup without proving prior ownership. This remains an isolation
  limitation. The controlled 3.3 trial used no such scratch; its prepared ignored
  Gemfile locks were captured before evaluator teardown. Codex removed its
  generated locks. Evaluator cleanup does not erase agent omissions.
- HTML contents, probe exclusion and relative links were checked. Browser
  rendering was not exercised because the browser inventory was empty.

## Reproduce and inspect

Pinned projects:

| Repository | Commit |
| --- | --- |
| [Money](https://github.com/RubyMoney/money) | `1d60efeaeea009cfc69374421561b23b851bd36e` |
| [GlobalID](https://github.com/rails/globalid) | `3155f04179c59bc79a6c60b01a40ee296905f069` |

Money used clean signed ddtest `f4c1b233661022940e4001f4e277920bc2bd050e`, binary
SHA-256 `caf9c8dc6c9cac7ab04d41de63605a36037222955e80d6246d2a084ec805bc41`.
The controlled GlobalID follow-up used clean signed
`93f9ddfcc15938b3f52f414cd0fa97702bb42df8`, SHA-256
`fbbb85ea1da1abffd2becd8cc4e13fbad56388577e36e9ea6a238ab2cf99a870`.
The latter commit only corrects Ruby failure diagnostics and adds their regression
test relative to the Money binary.

Run the evaluator once for each project/agent, using Ruby 3.4.9 for Money and
3.3.11 for GlobalID:

```sh
python3 skills/ddtest-onboarding-eval/scripts/run_eval.py \
  --repo <repository-url> --ref <pinned-commit> \
  --ddtest /absolute/path/to/ddtest --agent <codex-or-claude> \
  --ruby /absolute/path/to/the-selected-ruby \
  --setup-command 'bundle install --jobs 4 --retry 2' \
  --output /absolute/path/outside-target/<new-directory> \
  <process-local-MCP-isolation-flags>
```

Use `--disable-mcp-server <Datadog-server-name>` for Codex and `--strict-mcp` for
Claude; preserve normal permission checks and inherited CLI instructions. The
private Ruby build needed the process-local `ac_cv_func_dup3=no
ac_cv_func_pipe2=no` workaround used by
[current ruby-build](https://github.com/rbenv/ruby-build/blob/master/bin/ruby-build)
for the [macOS SDK build issue](https://bugs.ruby-lang.org/issues/22311).
Rails main remains a moving Git dependency; retain resolved lockfiles when present.

Evidence is retained locally at `/tmp/ddtest-ruby-completion-flq4cqlo`: experiment
manifests, six trial directories containing public activity, diffs/status,
assessments and JSON/HTML pairs, plus earlier failed snapshots. These are local
artifacts, not hosted links. All six exact owned trial roots, private Ruby build
sources and the evaluator's installed runtime were removed after collection;
report pairs and preparation logs remain outside them.

`make test`, `make lint` and the real RSpec/Minitest integration tests passed.
Integration coverage includes numeric Ruby selectors, explicit runtime exclusions,
rejection of an all-excluded CI scope, matching existing failures and all six
feature controls. The original three-project evaluation remains recorded in
`ruby-2026-10-19.md`; these follow-ups add Money and GlobalID.
