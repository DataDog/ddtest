# Issue #168 reproducer and impact assessment

[Issue #168](https://github.com/DataDog/ddtest/issues/168) is reproducible on
ddtest source commit `de191d143` with Node 24.14.0, Vitest 5.0.1, and dd-trace
5.111.0 on macOS arm64. The investigation was performed on 2026-10-07.
This validates the current source; the issue's released v1.11.0 binary and hosted
date-fns run were not independently rerun.

## Run the reproducer

Prerequisites: the Go version in `go.mod`, Node 24.14.0 on PATH, npm, Git, and
access to the npm registry. No Datadog credentials are needed.

From the repository root:

```sh
go build -o /tmp/ddtest-issue-168 main.go
node docs/examples/issue-168/reproduce.mjs /tmp/ddtest-issue-168
```

The script creates an isolated temporary Git repository, installs pinned direct
dependencies, and generates two one-test files:

```text
src/endOfYear/test.ts
src/eachWeekendOfYear/test.ts
```

Its Vitest configuration is:

```js
export default { test: { dir: 'src', include: ['**/test.ts'] } }
```

The script disables Datadog CI Visibility and tracing for these subprocesses.
Every test appends its file name, process ID, and ddtest worker session to a
JSONL file **inside the test body**. Counts therefore reflect actual test
execution, independently of ddtest's summary. Each scenario starts with a fresh
plan and separate local resources.

The script now exits zero when all eight scenarios validate the fix: ddtest
runs each assigned file exactly once, honors selection/exclusion, and passes
the create-once test. Direct Vitest still demonstrates substring filtering.
To reproduce the original behavior with an unfixed binary, append `--expect-bug`:

```sh
node docs/examples/issue-168/reproduce.mjs /absolute/path/to/unfixed-ddtest --expect-bug
```

It prints the temporary directory containing the generated
fixture, npm lockfile, `versions.txt`, `results.json`, per-scenario logs,
execution records, and saved worker assignments. Delete that directory when
finished; it is retained for inspection.

## Observed results before the fix

Each file contains exactly one test. Expected counts below follow the user's
selection; actual counts come from the execution records.

| Scenario | Expected executions | Actual executions | Exit code |
| --- | ---: | ---: | ---: |
| Ordinary Vitest, all files | 2 | 2 | 0 |
| Vitest, relative `endOfYear` file filter | 1 | 2 | 0 |
| Vitest, canonical absolute `endOfYear` file filter | 1 | 2 | 0 |
| ddtest, two workers | 2 | 3 | 0 |
| ddtest, explicit selection of `endOfYear` only | 1 | 2 | 0 |
| ddtest, exclude `eachWeekendOfYear` | 1 | 2 | 0 |
| Ordinary Vitest, create-once resources | 2 | 2 | 0 |
| ddtest, two workers and create-once resources | 2 | 3 | 1 |

The parallel run saved this correct, disjoint plan:

```text
runner-0: src/eachWeekendOfYear/test.ts
runner-1: src/endOfYear/test.ts
```

Execution records show `eachWeekendOfYear` running in both worker sessions.
Vitest reports one passed file in runner 0 and two passed files in runner 1.
ddtest still reports `Test files run: 2` and `Result: passed`.
The explicit-selection and exclusion scenarios each plan only `endOfYear`, run
both files, and report `Test files run: 1`.

## Cause

`Vitest.RunTests` in `internal/framework/vitest.go` uses
`withFrameworkFiles` in `internal/framework/command_args.go` to append the
assigned file paths as positional arguments. Vitest documents that these are
[substring filename filters](https://vitest.dev/guide/filtering#filtering-by-file-name).

In the installed Vitest 5.0.1 implementation, `filterFiles` also compares paths
relative to the configured test directory and lowercases them:

```text
filter:    endofyear/test.ts
candidate: eachweekendofyear/test.ts
```

The candidate contains the filter. Selecting the shorter file consequently
selects the longer file as well. Canonical absolute paths still reproduce this
because the implementation retains the relative substring fallback.

The planner assigns the files correctly; execution expands its assignments.
The report counts the selected batch lengths in
`internal/runner/parallel_executor.go` and `sequential_executor.go`, rather than
measuring the files actually executed by Vitest.

## Impact and simple failure example

This is an execution correctness problem. In the two-file fixture it adds 50%
more test executions and understates the actual work. Across more workers,
one file can execute in every worker whose filter matches it. Multiple matching
filters in one Vitest invocation do not themselves imply multiple executions
there; the observed duplication is between worker invocations.

The same expansion can violate an explicit selection or ddtest exclusion even
with just one worker. Pure tests can remain green while wasting CPU and API
calls. Tests with shared resources can produce duplicate side effects, contention,
or failures; intended shard isolation no longer holds.

A simple example is a test in `eachWeekendOfYear/test.ts` that creates a database
row with a unique key. Both workers execute it, so the second insertion fails.
The reproducer models that without a database, using an exclusive file creation:

```js
writeFileSync(resourcePath, 'created', { flag: 'wx' })
```

Each test has its own resource path, so ordinary Vitest passes. Under ddtest the
duplicated test attempts to create the same resource twice, causing `EEXIST`
and exit code 1. This failure is deterministic regardless of which worker wins
the first creation; it needs no timing-based race or flaky application code.

An unrestricted single-worker run of both files executes each once in this
fixture. The issue requires overlapping filters that cross an assignment or
selection boundary. No omissions were observed in these scenarios.
Authenticated tracer/TIA enforcement was not tested, so its effect on the
extra test executions remains unverified.

## Fix

The Vitest adapter now installs a Node preload that restricts Vitest's file
filter to exact membership in the worker's assigned files. Both assigned paths
and candidates are resolved to canonical absolute paths without lowercasing.
The original CLI still owns the real run, including its configuration, command
wrappers, reporters, coverage, tracer hooks, and failure exit codes.

Vitest does not export its project class on every supported version. The preload
obtains the shared project prototype from an empty context rooted in the
adapter's temporary directory, with config loading disabled, then closes that
context before the real CLI starts. It does not load the user's configuration
or workspace for that preliminary context. The exact filter applies to every
project in the subsequent real run. An unsupported API fails explicitly rather
than falling back to fuzzy selection. Empty batches do not invoke Vitest.

The assigned paths are passed through a temporary JSON file to avoid putting a
large allowlist into the environment. The adapter and list are removed after
the worker finishes, including on test failure.

The integration regression in `internal/compatibility/vitest_test.go` runs in
the existing Vitest CI matrix. It checks overlapping names under `test.dir`,
relative and absolute assignments, direct and package-manager commands,
end-of-options separators, configured setup files, worker environment, and
failure exit status. The executable repro additionally validates parallel
execution, explicit selection, exclusion, and create-once side effects.
