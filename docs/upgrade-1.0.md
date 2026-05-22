# DDTest 1.0 Upgrade Guide

DDTest 1.0 removes compatibility writes for legacy `.testoptimization` plan files.
Before upgrading to 1.0, update CI jobs and custom integrations to consume the
1.0 plan layout. Ruby requires the `datadog-ci` gem 1.31.0 or higher.

## Required Changes

Ruby requires the `datadog-ci` gem 1.31.0 or higher.

Update any scripts, CI jobs, or custom test runners that read DDTest plan files:

| Legacy path | New path |
| --- | --- |
| `.testoptimization/test-files.txt` | `.testoptimization/runner/test-files.txt` |
| `.testoptimization/parallel-runners.txt` | `.testoptimization/runner/parallel-runners.txt` |
| `.testoptimization/skippable-percentage.txt` | `.testoptimization/runner/skippable-percentage.txt` |
| `.testoptimization/tests-split/runner-*` | `.testoptimization/runner/tests-split/runner-*` |

Use `.testoptimization/manifest.txt` as the plan layout marker. DDTest sets
`TEST_OPTIMIZATION_MANIFEST_FILE` for worker processes to the absolute manifest
path unless that environment variable is already provided.

## Removed Compatibility Paths

DDTest 1.0 writes plan files only under `.testoptimization/runner/*` and no
longer writes these legacy plan files:

- `.testoptimization/test-files.txt`
- `.testoptimization/parallel-runners.txt`
- `.testoptimization/skippable-percentage.txt`
- `.testoptimization/tests-split/runner-*`

## Cache Layout

Datadog Test Optimization JSON cache data is available under `.testoptimization/cache/http/*`.
DDTest 1.0 no longer writes processed cache files directly under `.testoptimization/cache/*`.

DDTest-private cache files, such as `test_suite_durations.json`, live under
`.testoptimization/runner/cache/*`.

## Validation Checklist

1. Run `ddtest plan` in CI and verify `.testoptimization/manifest.txt` exists.
2. Verify CI jobs read plan files only from `.testoptimization/runner/*`.
3. Verify the `datadog-ci` gem version is 1.31.0 or higher.
4. Run one CI shard with `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE=0 ddtest run`.
5. Remove any references to legacy root plan paths from CI templates and custom scripts.
