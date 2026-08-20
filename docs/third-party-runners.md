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

## Pytest

When another runner consumes DDTest's file list for pytest, make sure the
`ddtrace` pytest plugin is enabled the same way `ddtest run` enables it:

```bash
export PYTEST_ADDOPTS="${PYTEST_ADDOPTS:+$PYTEST_ADDOPTS }--ddtrace"
if [ -s .testoptimization/runner/test-files.txt ]; then
  xargs python -m pytest < .testoptimization/runner/test-files.txt
fi
```

## Jest

When another runner consumes DDTest's file list for Jest, make sure the
`dd-trace` Test Optimization init is loaded the same way `ddtest run` loads it:

```bash
case "${NODE_OPTIONS:-}" in
  *dd-trace/ci/init*) ;;
  *) export NODE_OPTIONS="-r dd-trace/ci/init${NODE_OPTIONS:+ $NODE_OPTIONS}" ;;
esac
if [ -s .testoptimization/runner/test-files.txt ]; then
  xargs ./node_modules/.bin/jest --runTestsByPath < .testoptimization/runner/test-files.txt
fi
```

## Vitest

When another runner consumes DDTest's file list for Vitest, load both dd-trace
initialization entry points:

```bash
export NODE_OPTIONS="--import dd-trace/register.js -r dd-trace/ci/init${NODE_OPTIONS:+ $NODE_OPTIONS}"
if [ -s .testoptimization/runner/test-files.txt ]; then
  xargs ./node_modules/.bin/vitest run < .testoptimization/runner/test-files.txt
fi
```

## Mocha

When another runner consumes DDTest's Mocha file list, load Test Optimization
initialization before invoking Mocha:

```bash
export NODE_OPTIONS="-r dd-trace/ci/init${NODE_OPTIONS:+ $NODE_OPTIONS}"
if [ -s .testoptimization/runner/test-files.txt ]; then
  xargs ./node_modules/.bin/mocha < .testoptimization/runner/test-files.txt
fi
```

## Cypress

Keep the project's existing `dd-trace` Cypress plugin and support-file setup.
Cypress accepts its selected specs as one comma-separated `--spec` value:

```bash
if [ -s .testoptimization/runner/test-files.txt ]; then
  specs=$(paste -sd, .testoptimization/runner/test-files.txt)
  ./node_modules/.bin/cypress run --spec "$specs"
fi
```

## Playwright

Playwright treats positional file arguments as regular expressions matched
against absolute paths. Escape the paths before passing a DDTest file list, or
use a small script that invokes Playwright with one exact expression per file.
Keep `-r dd-trace/ci/init` in `NODE_OPTIONS` so Test Optimization remains
enabled. Do not add Playwright's own `--shard`; DDTest has already partitioned
the files.

## Cucumber

Load Test Optimization initialization and pass DDTest's feature list to
`cucumber-js`:

```bash
export NODE_OPTIONS="-r dd-trace/ci/init${NODE_OPTIONS:+ $NODE_OPTIONS}"
if [ -s .testoptimization/runner/test-files.txt ]; then
  xargs ./node_modules/.bin/cucumber-js < .testoptimization/runner/test-files.txt
fi
```

## Custom Runners

Read `.testoptimization/runner/test-files.txt` when your runner should handle
its own queueing or balancing.

Read `.testoptimization/runner/tests-split/runner-N` when your CI already fans
out jobs and each job should run only the files assigned to its CI node index.
