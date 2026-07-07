# DDTest 1.0 Upgrade Guide

DDTest 1.0 removes compatibility writes for legacy `.testoptimization` plan files.
Before upgrading to 1.0, update CI jobs and custom integrations to consume the
1.0 plan layout.

## Required Changes

Minimum supported library and runtime requirements:

- Ruby requires the `datadog-ci` gem 1.31.0 or higher.
- Python requires the `ddtrace` package 4.10.3 or higher and `pytest`.
- JavaScript requires the `dd-trace` package 5.111.0 or higher, Node.js, and
  Jest.

DDTest 1.0 writes plan files under `.testoptimization/runner/*`. If your
scripts, CI jobs, or custom test runners read DDTest plan files directly, update
these paths:

| Legacy path | New path |
| --- | --- |
| `.testoptimization/test-files.txt` | `.testoptimization/runner/test-files.txt` |
| `.testoptimization/parallel-runners.txt` | `.testoptimization/runner/parallel-runners.txt` |
| `.testoptimization/skippable-percentage.txt` | `.testoptimization/runner/skippable-percentage.txt` |
| `.testoptimization/tests-split/runner-*` | `.testoptimization/runner/tests-split/runner-*` |

## Validation Checklist

1. Verify the Datadog Test Optimization library version for your platform:
   `datadog-ci` 1.31.0 or higher for Ruby, or `ddtrace` 4.10.3 or higher for
   Python, or `dd-trace` 5.111.0 or higher for JavaScript.
2. Remove references to legacy root plan paths from CI templates and custom scripts.
3. Run `ddtest plan` in CI.
4. Run one CI shard with `DD_TEST_OPTIMIZATION_RUNNER_CI_NODE=0 ddtest run`.
5. Verify CI jobs read plan files only from `.testoptimization/runner/*`.
