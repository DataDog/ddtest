# DDTest Plan File Layout

`ddtest plan` writes a `.testoptimization/` directory in the current working
directory. Copy this directory from the planning job to every CI job that runs
`ddtest run` or consumes DDTest's plan file lists.

Most integrations should treat `.testoptimization/` as a generated artifact. The
stable files for external consumers are the manifest, the plan file lists under
`.testoptimization/runner/`, the GitHub Actions matrix file, and the HTTP cache.
Files under `runner/cache/` and `tests-discovery/` are documented for
troubleshooting, but they are DDTest implementation details.

## Directory Tree

```text
.testoptimization/
  manifest.txt
  runner/
    test-files.txt
    parallel-runners.txt
    skippable-percentage.txt
    tests-split/
      runner-0
      runner-1
      ...
    cache/
      test_suite_durations.json
  github/
    config
  cache/
    http/
      settings.json
      known_tests.json
      skippable_tests.json
      test_management.json
  tests-discovery/
    tests.json
```

Some files are conditional. For example, `github/config` is only written when
DDTest detects GitHub Actions, and individual `cache/http/*.json` files are only
written when the corresponding Datadog Test Optimization data is available.

## Manifest

### `.testoptimization/manifest.txt`

Plain text file containing the plan layout version.

```text
1
```

DDTest sets `TEST_OPTIMIZATION_MANIFEST_FILE` for worker processes to point at
this file unless that environment variable is already set.

## Plan Files

### `.testoptimization/runner/test-files.txt`

Newline-delimited list of runnable test files. Paths are relative to the
directory where `ddtest plan` ran. The file has a trailing newline when it is not
empty.

```text
spec/models/user_spec.rb
spec/services/checkout_spec.rb
```

This is the main file to feed into another runner when you do not want DDTest to
execute tests itself.

### `.testoptimization/runner/parallel-runners.txt`

Plain text integer with the selected parallelism count. In CI-node mode, this is
the CI node count. In a single-node run, this is the worker count. There is no
percent sign or extra JSON wrapper.

```text
8
```

### `.testoptimization/runner/skippable-percentage.txt`

Plain text decimal percentage of test time skipped by Test Impact Analysis,
formatted with two decimal places and no `%` suffix.

```text
42.75
```

### `.testoptimization/runner/tests-split/runner-N`

Newline-delimited list of runnable test files assigned to index `N`, where `N`
is zero-indexed. DDTest writes one file for each planned CI node or worker from
`runner-0` through `runner-(parallelism - 1)`.

```text
spec/models/user_spec.rb
spec/services/checkout_spec.rb
```

Use these files when your CI already fans out jobs and each CI node should run
only its assigned files. `ddtest run --ci-node N` reads `runner-N`.

## GitHub Actions Matrix

### `.testoptimization/github/config`

GitHub Actions output file. It contains a single `matrix=` assignment whose
value is compact JSON.

```text
matrix={"include":[{"ci_node_index":0,"ci_node_total":2},{"ci_node_index":1,"ci_node_total":2}]}
```

When `ddtest plan` runs in GitHub Actions, DDTest also appends the same
`matrix=...` assignment to `$GITHUB_OUTPUT`. Give the plan step an `id`, then
use the matching `steps.<id>.outputs.matrix` value in the job output:

```yaml
- id: dd_plan
  run: bin/ddtest plan
```

The resulting matrix entries expose:

| Field | Description |
| --- | --- |
| `ci_node_index` | Zero-indexed CI node number to pass to `ddtest run --ci-node`. |
| `ci_node_total` | Total number of CI nodes DDTest selected. |

## Datadog HTTP Cache

### `.testoptimization/cache/http/*.json`

These files contain Datadog Test Optimization JSON cache data used by Datadog
libraries during test execution.

| File | Contents |
| --- | --- |
| `settings.json` | Repository Test Optimization settings. |
| `known_tests.json` | Known tests data. |
| `skippable_tests.json` | Skippable tests data for the runtime tags used by the plan. |
| `test_management.json` | Flaky test management data. |

These cache files are only for Datadog libraries.

## DDTest Private Cache

### `.testoptimization/runner/cache/test_suite_durations.json`

DDTest-private JSON cache used to carry planning data from `ddtest plan` to
`ddtest run`. It is useful for debugging, but it is not intended as a stable
integration contract.

Top-level shape:

```json
{
  "testSuiteDurations": {
    "module-name": {
      "suite-name": {
        "source_file": "spec/models/user_spec.rb",
        "duration": {
          "p50": "1200",
          "p90": "1800"
        }
      }
    }
  },
  "suiteAggregates": {
    "[\"module-name\",\"suite-name\"]": {
      "module": "module-name",
      "suite": "suite-name",
      "sourceFile": "spec/models/user_spec.rb",
      "totalDuration": 1200,
      "estimatedDuration": 1200,
      "durationSource": "known",
      "numTests": 3,
      "numTestsSkipped": 1
    }
  },
  "suitesBySourceFile": {
    "spec/models/user_spec.rb": [
      {
        "module": "module-name",
        "suite": "suite-name"
      }
    ]
  },
  "testFileWeights": {
    "spec/models/user_spec.rb": 1200
  },
  "testFileDurationSources": {
    "spec/models/user_spec.rb": "known"
  },
  "runInfo": {
    "service": "my-service",
    "repository": "https://github.com/example/repo.git",
    "commit": "abc123",
    "branch": "main",
    "platform": "ruby",
    "framework": "rspec",
    "osTags": {
      "os.platform": "linux"
    },
    "runtimeTags": {
      "runtime.name": "ruby",
      "runtime.version": "3.3.0"
    }
  }
}
```

`testFileWeights` values are integer millisecond weights used for parallelism
selection. `totalDuration` and `estimatedDuration` preserve DDTest's internal
duration estimate for the suite aggregate.

`durationSource` is `known` when Datadog duration data was available and
`default` when DDTest fell back to its local estimate.

## Test Discovery File

### `.testoptimization/tests-discovery/tests.json`

JSON stream produced by framework discovery. Each line is one JSON object.

```json
{"name":"validates email","suite":"User","module":"rspec","parameters":"{}","suiteSourceFile":"spec/models/user_spec.rb"}
{"name":"creates checkout","suite":"Checkout","module":"rspec","parameters":"{}","suiteSourceFile":"spec/services/checkout_spec.rb"}
```

Fields:

| Field | Description |
| --- | --- |
| `name` | Test name reported by the framework. |
| `suite` | Test suite name reported by the framework. |
| `module` | Framework module name, such as `rspec`, `minitest`, `pytest`, or `jest`. |
| `parameters` | Serialized test parameters. |
| `suiteSourceFile` | Source file containing the suite. |

This file is an intermediate discovery output. Prefer
`.testoptimization/runner/test-files.txt` or `tests-split/runner-N` for custom
execution.
