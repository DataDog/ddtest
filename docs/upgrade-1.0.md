# DDTest 1.0 Upgrade Guide

DDTest 1.0 removes compatibility writes for legacy `.testoptimization` runner files.
Before upgrading to 1.0, update CI jobs and custom integrations to consume the
runner layout and upgrade the Datadog Ruby library to 1.31 or later.

## Required Changes

Upgrade the Datadog Ruby library to `1.31+`.

Update any scripts, CI jobs, or custom test runners that read DDTest runner files:

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

DDTest 1.0 writes runner files only under `.testoptimization/runner/*` and no
longer writes these legacy runner files:

- `.testoptimization/test-files.txt`
- `.testoptimization/parallel-runners.txt`
- `.testoptimization/skippable-percentage.txt`
- `.testoptimization/tests-split/runner-*`

## Cache Layout

Backend-shaped Datadog responses are available under `.testoptimization/cache/http/*`.
DDTest 1.0 no longer writes processed cache files directly under `.testoptimization/cache/*`.

DDTest-private runner cache files, such as `test_suite_durations.json`, live under
`.testoptimization/runner/cache/*`.

## Validation Checklist

1. Run `ddtest plan` in CI and verify `.testoptimization/manifest.txt` exists.
2. Verify CI jobs read runner files only from `.testoptimization/runner/*`.
3. Verify the Ruby library version is `1.31+`.
4. Run one CI shard with `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE=0 ddtest run`.
5. Remove any references to legacy root runner paths from CI templates and custom scripts.
