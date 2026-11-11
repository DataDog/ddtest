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
  --force-full-test-discovery --min-parallelism 1 --max-parallelism 1
cat .testoptimization/runner/test-files.txt
python3 - <<'PY'
import json
from pathlib import Path
cache = json.loads(Path('.testoptimization/runner/cache/test_suite_durations.json').read_text())
for key in ['testFileWeights', 'testFileDurationSources']:
    print(key, cache.get(key))
PY
```

Keep `DDTEST_QA_FAIL_DISCOVERY` unset for these full-discovery scenarios.

| Scenario | `scenario.json` | Expected result |
| --- | --- | --- |
| Mixed coverage | `{"skippable":[],"duration_b":null}` | Weight `11000`, source `mixed`; report says `Mixed backend/default durations: 1 file` and does not count it as default-only. |
| Both timings available | `{"skippable":[],"duration_b":"30000000000"}` | Weight `40000`, source `known`; no mixed-duration file. |
| Runnable sibling | `{"skippable":["A"],"duration_b":"30000000000"}` | File retained, weight `30000`, source `known`. |
| Missing sibling timing | `{"skippable":["A"],"duration_b":null}` | File retained, weight `1000`, source `default`; the skipped suite's backend timing does not make it mixed. Repeat with `"0"`, `"-1"`, and `"invalid"`. |
| All suites skippable | `{"skippable":["A","B"],"duration_b":"30000000000"}` | File omitted from file list and weights. |

For every retained file, inspect
`.testoptimization/runner/tests-split/runner-0` and confirm it includes
`spec/multiple_spec.rb`. In the runnable-sibling scenario, also run:

```sh
"$qa_root/ddtest" run --framework rspec --test-skipping-mode suite --ci-node 0
```

Expect successful execution with `RAN_B` in the output.

For the mixed-coverage scenario, rename `RSpec.describe "A"` to `"Z"` in the
fixture and replace `A at ./spec/multiple_spec.rb` with
`Z at ./spec/multiple_spec.rb` in `backend.py`. Restart the backend and replan
with a clean cache. The weight and mixed classification must remain unchanged.

To check that ordinary suite mode still uses fast discovery, restore the original
suite names, use `{"skippable":[],"duration_b":"30000000000"}`, clear the cache,
set `DDTEST_QA_FAIL_DISCOVERY=true`, and run the plan command **without**
`--force-full-test-discovery`, adding `--strict-discovery`. Planning should succeed
with fast discovery and retain the file, because it never loads the Ruby fixture.
Run again with `--force-full-test-discovery --strict-discovery`: the deliberate
collection failure must produce a nonzero exit. Unset the failure switch afterward.

For a real fast-discovery regression, run from the repository root:

```sh
python3 internal/planner/testdata/multi_suite/reproduce_fast.py "$qa_root/ddtest"
```

This creates a separate temporary Ruby project and loopback backend. Suite B
intentionally fails. Native RSpec must fail, and `ddtest run` must also expose
`RUNNABLE_B_FAILED` when only A is skippable, with either positive or zero timing
for B. With neither suite skipped, the 10s and 30s timings must sum to 40000ms.
Planning uses strict fast discovery; loading the test file during discovery
would deliberately fail. Before the fix, zero timing hides B's failure and the
positive timings are not summed; the reproducer exits nonzero. After the fix,
all checks pass. Logs remain in the printed temporary directory; remove that
directory after inspection. The backend stops automatically.

Stop the backend and remove the disposable project when finished:

```sh
kill "$qa_backend_pid"
cd /
rm -rf "$qa_root"
```

Exit the disposable shell to discard its QA environment variables.
