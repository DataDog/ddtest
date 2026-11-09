# Multi-suite planning QA

Prerequisites: Go from the repository's `go.mod`, Python 3, Ruby 3.4+, Bundler,
and access to RubyGems (or locally installed RSpec 3.13 and datadog-ci 1.39+).
The fixture backend binds only to `127.0.0.1:8765`; this port must be free.
Run the commands from this PR's repository root, in a disposable shell.

```sh
qa_root=$(mktemp -d)
go build -o "$qa_root/ddtest" .
cp -R internal/planner/testdata/multi_suite "$qa_root/project"
cd "$qa_root/project"
git init -q
git -c core.hooksPath=/dev/null -c commit.gpgsign=false \
  -c user.name='DDTest QA' -c user.email='qa@example.com' \
  commit --allow-empty -qm 'Initialize QA fixture'
bundle install
python3 backend.py > backend.log 2>&1 &
qa_backend_pid=$!

export DD_API_KEY=qa
export DD_CIVISIBILITY_AGENTLESS_ENABLED=true
export DD_CIVISIBILITY_AGENTLESS_URL=http://127.0.0.1:8765
export DD_INSTRUMENTATION_TELEMETRY_ENABLED=false
export DD_GIT_REPOSITORY_URL=https://example.com/multi-suite.git
export DD_GIT_COMMIT_SHA=1111111111111111111111111111111111111111
export DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=false
export DD_CIVISIBILITY_GIT_METADATA_UPLOAD_ENABLED=false
```

For each scenario below, replace `scenario.json` with the indicated JSON,
remove only this disposable project's `.testoptimization` directory to avoid
reusing discovery or tracer caches, and run:

```sh
rm -rf .testoptimization
"$qa_root/ddtest" plan --framework rspec --test-skipping-mode suite \
  --min-parallelism 1 --max-parallelism 1
cat .testoptimization/runner/test-files.txt
python3 - <<'PY'
import json
from pathlib import Path
cache = json.loads(Path('.testoptimization/runner/cache/test_suite_durations.json').read_text())
for key in ['completeSuiteInventory', 'testFileWeights', 'testFileDurationSources']:
    print(key, cache.get(key))
PY
```

| Scenario | `scenario.json` | Discovery switch | Expected result |
| --- | --- | --- | --- |
| Runnable sibling | `{"skippable":["A"],"duration_b":"30000000000"}` | `unset DDTEST_QA_FAIL_DISCOVERY` | Full discovery; file retained, weight `30000`, source `known`. |
| Failed discovery | Same as above | `export DDTEST_QA_FAIL_DISCOVERY=true` | Fast fallback; file retained, weight `40000`, source `known`; no complete inventory. |
| Missing sibling timing | `{"skippable":["A"],"duration_b":null}` | Unset failure switch | Full discovery; file retained, weight `1000`, source `default`. |
| Incomplete historical inventory | Same as above | Enable failure switch | Fast fallback retains file, weight `10000`, source `known`; missing historical siblings must not permit omission. |
| Nonpositive timing | `{"skippable":["A"],"duration_b":"0"}` | Enable failure switch | Fast fallback retains both historical suites; weight `11000`, source `mixed`. Repeat with `"-1"`. |
| All suites skippable | `{"skippable":["A","B"],"duration_b":"30000000000"}` | Unset failure switch | Full discovery; file omitted from file list and weights. |
| All historical suites skippable | Same as above | Enable failure switch | Fast fallback retains file at `40000`; historical rows do not prove inventory completeness. |
| Mixed coverage | `{"skippable":[],"duration_b":null}` | Unset failure switch; add `--force-full-test-discovery` to the plan command | Weight `11000`, source `mixed`, report says `Mixed backend/default durations: 1 file`. |

For every retained file, inspect
`.testoptimization/runner/tests-split/runner-0` and confirm it includes
`spec/multiple_spec.rb`. In the runnable-sibling scenario, also run:

```sh
"$qa_root/ddtest" run --framework rspec --test-skipping-mode suite --ci-node 0
```

Expect successful execution with `RAN_B` in the output. Repeat with failed
discovery to confirm that the conservatively retained file still runs.

For the mixed-coverage scenario, rename `RSpec.describe "A"` to `"Z"` in the
fixture and replace `A at ./spec/multiple_spec.rb` with
`Z at ./spec/multiple_spec.rb` in `backend.py`. Restart the backend and replan
with a clean cache. The weight and mixed classification must remain unchanged.

With the failure switch enabled, repeat planning with `--strict-discovery`.
Expect a nonzero exit and a full-discovery error rather than a successful plan.

Stop the backend and remove the disposable project when finished:

```sh
kill "$qa_backend_pid"
cd /
rm -rf "$qa_root"
```

Exit the disposable shell to discard its QA environment variables.
