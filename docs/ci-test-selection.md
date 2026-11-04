# Faster PR and contributor checks

The `CI` workflow actively selects checks for pull requests and pushes to `main`.
It runs native Go tests once, without global coverage or SDK weaving. Affected
frameworks get a pinned Linux smoke, rather than the complete historical-version
and scenario matrices. This is the PR acceptance policy, with no shadow phase.

`Full compatibility` retains the complete framework matrices, both instrumented
root Go test paths, coverage, and the Go instrumentation OS/toolchain/SDK matrix.
Every release waits for it on the commit being released. Maintainers can also
run it from Actions → Full compatibility → Run workflow on any branch. The
existing nightly Go instrumentation matrix continues to catch upstream changes.
The full framework matrix has no recurring schedule; it runs for releases and
manual dispatches.

## Local feedback

From the repository root, with Python 3 and the Go version in `go.mod`:

```sh
make test-fast                       # compare with the merge base of origin/main
make test-fast TEST_BASE=<commit>    # choose another base
```

This includes staged, unstaged, deleted, renamed and non-ignored untracked files.
A production Go edit selects its package and every transitive importer, including
imports made only by tests. A test-only edit selects its own package. Non-Go
inputs (including embedded assets), removed packages and unknown history select
all root packages. Go's own build and test caches remain enabled. `make test`
still runs the whole root module. The instrumentation in `ddto/` is a separate
module; its local checks are described in [its guide](../ddto/AGENTS.md#changing-this-repository).

Inspect the same integration selection that CI uses:

```sh
python3 scripts/ci_test_plan.py plan --base origin/main --output /tmp/pr-plan.json
cat /tmp/pr-plan.json
```

To execute a selected framework smoke, install its runtime and dependencies
listed in `scripts/ci_test_plan.json`, then run, for example:

```sh
python3 scripts/ci_smoke.py --id cucumber
```

The smoke command installs its npm fixture in a temporary directory and removes
it afterwards. Ruby smokes install the listed gems in the active Ruby environment.
Browser checks need the CI browser/Xvfb environment; Playwright uses the pinned
official image in `ci.yml`. These checks use controlled local fixtures without
Datadog credentials.

## Selection and acceptance

[`scripts/ci_test_plan.json`](../scripts/ci_test_plan.json) owns framework paths,
embedded instructions/scripts, JavaScript dependencies and smoke recipes.
Python-only changes select Python; JavaScript platform changes also select all
JavaScript frameworks. Shared or unclassified production inputs select every
PR smoke. Instrumentation module changes select its units and the root/release
smoke without unrelated framework integrations. Unit-test-only edits omit
external smokes; integration-test edits still select their framework.
The native Go job installs Python/pytest/ddtrace, Ruby/Bundler and Node/Yarn,
and sets `DDTEST_REQUIRE_PROJECT_RUNTIMES=1` so focused runtime regressions in
selected packages fail on missing prerequisites instead of silently skipping.
Documentation-only changes omit runtime jobs; evaluator-only changes run the
evaluator checks. The small Python suite tests CI and installer scripts on every
PR. Missing base history selects all PR checks, never an empty plan.

Most framework smokes exercise a passing customer fixture and an existing
failure. Python, Ruby, Java and Vitest use their existing validation journeys;
Java covers both Maven and Gradle. PRs use one pinned framework/tracer/runtime
combination per journey. Older versions, `latest`, remaining scenarios, SDK parity
and platform differences are checked by Full compatibility.

The `pr-test-plan` artifact and job summary show the selection. Every smoke
requires explicit named Go test cases to report `pass`: a filtered, missing or
skipped case fails. The required `test` check rejects failed, cancelled or
unexpectedly skipped selected jobs. The existing required `lint` check remains.
New production paths default to all smokes; add narrower ownership only when its
framework boundary is known and tested.

## Deduplicating resolved Cucumber configurations

The full suite resolves `latest` once and creates a dependency lockfile per exact
Cucumber version before expanding its jobs. Requested labels share a job only
when the resolved version, complete dependency graph, exact Node/npm versions,
test filter and shard all match. For example, when `latest` resolves to the
pinned `13.3.0`, its four duplicate jobs collapse, reducing 13 requested lanes
to 9. A new latest release creates separate jobs again. The `8.11.1` floor smoke
remains distinct from full scenario lanes.

Workers download the planned fixture, verify npm's version and use `npm ci`.
Registry or lockfile failures fail planning. To inspect a live resolution with
Node 24, npm and registry access, use a fresh directory:

```sh
python3 scripts/ci_cucumber_matrix.py --output /tmp/cucumber-ci-fixtures \
  --summary /tmp/cucumber-ci-matrix.md
```

Inspect `matrix.json`, the summary and each version's `package-lock.json`. The
summary lists all requested aliases represented by each remaining job.
