# Additional Ruby CLI E2E checks — 2026-10-19

Tested two new projects with both Claude Code 2.1.220 and Codex CLI 0.163.0.
Each trial used a fresh clone and the minimal onboarding prompt from `../SKILL.md`,
without coaching or model overrides. The binary was built from clean, signed
commit `975090a4f6f5f897baba09c5b74f90302b43f1d2`; SHA-256:
`b569cada15dd2b8c1dcdc2ae19d97e87708119acb5e59ba1666ce733be86f2b4`.
All four trials used Ruby 3.4.9 and datadog-ci 1.42.0.

| Project / pinned commit | Framework | `ddtest onboard` | Paired execution, per agent | Features, per agent |
| --- | --- | --- | --- | --- |
| [Money](https://github.com/RubyMoney/money/tree/1d60efeaeea009cfc69374421561b23b851bd36e) | RSpec 3.13.6 | Correct automatic detection and Ruby setup guidance | 502 examples passed in both modes | 6/6 passed |
| [GlobalID](https://github.com/rails/globalid/tree/3155f04179c59bc79a6c60b01a40ee296905f069) | Minitest 5.27.0 / Rails 8.0.3 | Correct automatic detection; dynamic CI setup remains unresolved | 175 tests passed in both modes | 6/6 passed |

`onboard` ran without a framework override in every trial. `testdrive` compared
the existing complete local suite without instrumentation and with reporting-only
instrumentation. Native results, failure details, exit codes and telemetry
matched. Application source coverage was observed. Passing/failing controls and
independent body counters verified retries, early flake detection, TIA skipping,
quarantine, disabled tests and attempt-to-fix. No application tests, Gemfiles,
tracked lockfiles, Rakefiles or matrix entries were changed to obtain the results.

## What works and what remains incomplete

Money was validated through its existing `bin/rspec` entry point with:

```sh
ddtest testdrive --framework rspec --all --tracer-version 1.42.0 --yes
```

Both reports have `executed_validation: PASSED` and `local_validation: PASSED`.
The repository verdict remains `INCOMPLETE`: floating Ruby selectors and other
Ruby/JRuby runtimes were not certified by the static CI check or executed locally.

GlobalID's `--all` run could not resolve
`BUNDLE_GEMFILE: ${{ github.workspace }}/gemfiles/rails_${{ matrix.rails }}.gemfile`.
All 34 discovered CI configurations therefore remain **not exercised**. Both
agents preserved that matrix and validated the installed root Gemfile instead:

```sh
ddtest testdrive --framework minitest --command 'bundle exec rake test' --tracer-version 1.42.0 --yes
```

This executed all 175 local tests and the six controls successfully. Its
`executed_validation` is `PASSED`, while **selected, local and repository validation
remain `INCOMPLETE`** because CI/setup blockers are unresolved. A passing focused
execution does not validate the Ruby/Rails matrix. Codex compressed this into
"Local checks passed" with an explicit overall-incomplete caveat; the JSON's
narrower verdicts above remain authoritative. Claude retained the full verdict.

Thus both commands work for the tested local suites, with explicit scope limits.
GlobalID is not fully automatic CI onboarding. No ddtest implementation change
was required by these tests. Actual CI installation/execution and Datadog backend
processing were not exercised; no valid Datadog credentials were supplied.

## Reproduce and inspect

Use the pinned commits above and the evaluator runner once per repository/agent:

```sh
python3 skills/ddtest-onboarding-eval/scripts/run_eval.py \
  --repo <repository-url> --ref <pinned-commit> \
  --ddtest /absolute/path/to/ddtest --agent <codex-or-claude> \
  --ruby /absolute/path/to/ruby-3.4.9 \
  --setup-command 'bundle install --jobs 4 --retry 2' \
  --output /absolute/path/outside-target/<new-directory> \
  <process-local-MCP-isolation-flags>
```

Use `--disable-mcp-server <configured-Datadog-server-name>` for Codex and
`--strict-mcp` for Claude. Normal permission checks and global CLI instructions
remain active. Each trial owns its Bundler/gem installation and cache directories.
Codex retried sandbox network failures during temporary tracer installation;
those failures and subsequent executions remain in the public activity logs.

Evidence is retained locally at `/tmp/ddtest-ruby-extra-mmI0G3`, outside target
clones: experiment metadata, per-trial `run.json`, public `activity.jsonl`, tracked
diff, ignored-file list, final answer, assessment and JSON/HTML report pair. These
paths are not hosted artifacts. Reports contain real suites without probe results
or broken local artifact links; browser rendering was not exercised because no
enabled browser was available.

All four ddtest sessions and declared unique agent scratch directories were gone
at collection. The evaluator removed each owned trial root after retaining the
evidence. GlobalID's Claude trial also used three predictable `/tmp/*.txt` scratch
paths and removed them; their ownership before use was not recorded, which is an
isolation limitation. `make test` and `make lint` passed after adding this record.

Rubyzip was considered first, but automatic approval review rejected enabling its
ZIP64 test, which writes an archive larger than 4 GB, on a disk with 16 GB free.
Its accepted setup was stopped before any agent or test execution. It is not
counted as a tested project; GlobalID replaced it. No approval bypass was used.
