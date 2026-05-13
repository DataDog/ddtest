# DDTest 1.0 Upgrade Guide

DDTest 1.0 will remove compatibility writes for legacy `.testoptimization` runner files.
Before upgrading to 1.0, update CI jobs and custom integrations to consume the new
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
`DD_TEST_OPTIMIZATION_MANIFEST_FILE` for worker processes to the absolute manifest
path unless that environment variable is already provided.

## Current Compatibility Window

Before 1.0, DDTest writes runner files to both the legacy paths and the new
`.testoptimization/runner/*` paths. This is write-only compatibility: DDTest itself
reads from the new runner paths.

In 1.0, DDTest will stop writing these legacy runner files:

- `.testoptimization/test-files.txt`
- `.testoptimization/parallel-runners.txt`
- `.testoptimization/skippable-percentage.txt`
- `.testoptimization/tests-split/runner-*`

## Cache Layout

Backend-shaped Datadog responses are available under `.testoptimization/cache/http/*`.
The processed cache files under `.testoptimization/cache/*` are kept for Ruby library
compatibility before 1.0.

DDTest-private runner cache files, such as `test_suite_durations.json`, live under
`.testoptimization/runner/cache/*` and do not cross the Ruby library compatibility
boundary.

## Validation Checklist

1. Run `ddtest plan` in CI and verify `.testoptimization/manifest.txt` exists.
2. Verify CI jobs read runner files only from `.testoptimization/runner/*`.
3. Verify the Ruby library version is `1.31+`.
4. Run one CI shard with `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE=0 ddtest run`.
5. Remove any references to legacy root runner paths from CI templates and custom scripts.
