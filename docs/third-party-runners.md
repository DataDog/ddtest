# Integrating with Third Party Test Runners

Use DDTest's plan files when you want DDTest to choose which test files should
run, but another runner should execute them.

The two most useful files are:

| File | Use |
| --- | --- |
| `.testoptimization/runner/test-files.txt` | All runnable test files after Datadog Test Optimization skips are applied. |
| `.testoptimization/runner/tests-split/runner-N` | Files assigned to CI node or worker `N`. |

For the full plan file layout, see [Plan file layout](layout.md).

## Knapsack Pro

```bash
KNAPSACK_PRO_TEST_FILE_LIST_SOURCE_FILE=.testoptimization/runner/test-files.txt bundle exec rake knapsack_pro:queue:rspec
```

## Custom Runners

Read `.testoptimization/runner/test-files.txt` when your runner should handle
its own queueing or balancing.

Read `.testoptimization/runner/tests-split/runner-N` when your CI already fans
out jobs and each job should run only the files assigned to its CI node index.
