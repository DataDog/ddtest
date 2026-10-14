# AGENTS.md

This file tells coding agents how to install `ddto`, use it to report a Go
project's tests to Datadog Test Optimization (CI Visibility), and choose its
settings for that project. The last section is for agents changing this
repository.

`ddto` is an experimental proof of concept. It wraps `go test`: it prepares
temporary build overlays, lets Go compile and run the tests, and exits with
Go's status. ddto leaves no files of its own in the project, but, as with the
SDK, the run can still change it. With `-mod=mod`, Go updates `go.mod` and
`go.sum` as it would without ddto. The Git work can fetch into `.git`: the
repository upload deepens a shallow clone, impacted-test detection fetches the
base branch, and a pull-request run in a shallow clone fetches its head
commit. `DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=false` and
`DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false` stop the first two.

## Install

`ddto` needs a Go 1.25 or newer toolchain on `PATH`. The project under test
keeps its own language version; Go 1.21 and newer are tested.

```sh
go version                    # go1.25 or newer
go install github.com/tonyredondo/dd-ci-testing-poc/cmd/ddto@main
ddto --help                   # prints the usage line
```

Go puts the binary in `$(go env GOBIN)`, or in `$(go env GOPATH)/bin` when
`GOBIN` is empty; add that directory to `PATH` if `ddto --help` is not found.
Do not add this module to the project's `go.mod`: each run supplies the runtime
through temporary module files.

## Instrument a project

1. Find how the project runs its tests: the CI workflow, a `Makefile` or a
   script that calls `go test`, and any `GOFLAGS`. Keep those flags and
   package patterns as they are.
2. Put `ddto` in front of the command. `go test <args>` becomes
   `ddto test <args>`, and `go -C dir test <args>` becomes
   `ddto go -C dir test <args>`. ddto runs any other command unchanged, so a
   script can prefix all of its `go` commands; only `go test` reports.
3. Leave the project's test caching as it is. The first ddto run builds
   different test binaries, so Go runs every package again. After that, a
   package whose result Go takes from its test cache runs no tests and sends
   nothing in that run, as with plain `go test`. A run whose results all come
   from the cache, without relinking a test binary, makes no requests; a change
   such as an edited comment relinks the binary, so the Git worker still sends
   its requests although no test runs. Add `-count=1` only if the project
   wants every test to report on every run.
4. Configure delivery, as described in the next section.
5. Check one package before rolling out, as described in
   [Verify that tests report](#verify-that-tests-report).

```sh
DD_SERVICE=my-service DD_ENV=ci ddto test -race ./...
```

## Configure delivery

With a Datadog Agent, ddto needs no setting: it uses `DD_TRACE_AGENT_URL`,
then `DD_AGENT_HOST` and `DD_TRACE_AGENT_PORT`, then the Agent's Unix socket,
then `http://localhost:8126`. Without an Agent, send directly to Datadog:

```sh
DD_CIVISIBILITY_AGENTLESS_ENABLED=true DD_SITE=datadoghq.com \
  ddto test ./...
```

Agentless delivery also needs `DD_API_KEY`. Take it from the CI secret store
and never write it into a file. Set `DD_SERVICE`, which otherwise defaults to
the repository name, and `DD_ENV`. Datadog's settings need the repository URL
and commit SHA: a Git checkout supplies them, and elsewhere set
`DD_GIT_REPOSITORY_URL` and `DD_GIT_COMMIT_SHA`.

The service's Test Optimization settings in Datadog decide which features run:
automatic test retries, early flake detection (EFD), Test Impact Analysis,
test management and impacted tests. The variables in
[Turn features off](#turn-features-off) can switch them off for one run.
`DD_CIVISIBILITY_FLAKY_RETRY_ENABLED` and
`DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED` can also switch their feature
on, although EFD still needs known tests enabled in Datadog.

## Verify that tests report

Delivery problems never change the exit code, and a test process's logs only
appear with `-v` or when a test fails. Run one package with debug logs, and
with `-count=1` so that it runs even when Go has its result cached:

```sh
DD_TRACE_DEBUG=true ddto test -count=1 -v ./path/to/pkg 2>&1 |
  grep -E 'runtime=mini|plan ready|WARN|ERROR|send finished'
```

- `runtime=mini` and `plan ready` mean ddto instrumented the build.
- `test-cycle: send finished ... status=ok` means the events reached the Agent
  or the intake.
- `TestOptimization.run ... ERROR` lines report failed requests, such as
  `connection refused` when no Agent listens.
- `TestOptimization.build ... WARN: ... running go test without CI Visibility`
  means ddto ran plain `go test` and reported nothing; the line gives the
  reason.

[CLI build diagnostics](docs/cli-debug.md) explains every line, and
`--keep-ci-state` keeps the state that ddto shared with the test processes.

## Choose settings for the target

Start with the defaults, and change one setting at a time for a symptom you
saw.

### Retries and early flake detection

Automatic test retries rerun failing tests, and EFD reruns new tests several
times. By default every attempt runs again in the same process. A new test
that changes process-wide state then passes its first attempt and fails the
EFD retries, so Datadog reports it as flaky, and a test that failed once can
keep failing its automatic retries for the same reason. Typical errors name an
HTTP pattern already registered on `http.DefaultServeMux`, a flag redefined,
or a duplicate Prometheus collector; package variables, `sync.Once` state and
ports or files left open cause the same problem.

For those projects, run retries in separate processes:

```sh
DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process ddto test ./...
```

The first attempt still runs in the test binary's process. Each retry then
reruns the whole top-level test function, subtests included, in a new child
process of the same binary, which runs the project's `TestMain` again. With a
test that registers an HTTP handler, EFD reported one pass and three failed
retries in process, and four passes in process mode. Keep in mind:

- Every retry starts from a fresh process, as a first run does. A test that
  passes only because an earlier attempt left state behind fails every retry
  in this mode.
- A `TestMain` that starts containers or databases pays that setup on every
  retry.
- `DD_CIVISIBILITY_RETRY_PROCESS_TIMEOUT=2m` bounds each child, which
  otherwise has only the test binary's own deadline.
- `DD_CIVISIBILITY_RETRY_PROCESS_MAX_CONCURRENCY` (default 1) runs that many
  children at once; raise it only for tests that do not share ports or files.

When process mode costs too much, keep the default and limit the retries
instead: `DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES=1` caps EFD,
`DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false` and
`DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false` switch the features off, and
`DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED=false` stops policies that target
individual subtests. Do not change the tests' code to suit the retries unless
the project's owners ask for it.

### Background delivery

Mini sends events from background goroutines while tests run. Tests that count
goroutines, check for open connections, or measure tight timings can notice
them. Set `DD_CIVISIBILITY_DEFERRED_DELIVERY=true` to send only between test
groups; parallel groups then hold their events in memory until they finish.
goleak needs no setting, because ddto pauses CI delivery and filters its own
goroutines for every goleak check.

### Project traits

| Project | What to do |
| --- | --- |
| Testify suites, v1.4.0 or newer v1 | Nothing. An unsupported version warns and its methods report as ordinary subtests. |
| goleak, v1.3.0 or newer v1 | Nothing. |
| dd-trace-go for APM in tests | Nothing: Mini stays the only CI reporter and APM keeps its own delivery. Spans that the SDK tracer starts from `t.Context()` also appear under the test. |
| Orchestrion builds | `ddto orchestrion go test ...`, `ddto go tool orchestrion go test ...`, or keep `-toolexec="orchestrion toolexec"` after `ddto test`. See [Orchestrion](docs/orchestrion.md) for toolchain requirements. |
| A `toolchain` line that makes Go download a newer toolchain | Nothing. The first run builds a mirror of that toolchain and relinks, so it is slower. |
| Vendoring or `go.work` | Nothing. |
| `go test -exec` or a cross-compilation helper | Nothing, unless ddto warns that it cannot resolve the program. |
| Test binaries built with `-c` and run later | Where they run, set `DD_CIVISIBILITY_ENABLED=parent` and the delivery variables, and run each binary from its package directory. |
| Test Impact Analysis enabled in Datadog | Build with coverage in count or atomic mode, such as `-cover -covermode=atomic`, so each test reports the code it ran. Set mode reports only aggregate coverage. |
| A monorepo that wants one service per CODEOWNERS team | `DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS=true`, with `DD_SERVICE` unset. See [Services from CODEOWNERS](docs/codeowners-service.md). |
| A large repository whose Git upload is slow | `--git-upload-timeout=2m` after `ddto test`. |
| Fuzzing with `-fuzz` | Nothing: fuzz roots and seeds report, generated inputs do not. |
| `go test file_test.go` or standard-library packages | Not instrumented: name packages instead of files. |
| Bazel | The payload-file and manifest modes have not been tested with a real Bazel build; see [Mini runtime](docs/mini-runtime.md#delivery-and-offline-output). |

### Turn features off

Each of these defaults to `true`, and `false` switches its feature off for the
run:

| Variable | Feature |
| --- | --- |
| `DD_TEST_MANAGEMENT_ENABLED` | Disabled, quarantined and attempt-to-fix tests from Datadog. |
| `DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED` | Impacted-test detection. |
| `DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED` | Management and retry policies for individual subtests. |
| `DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED` | Aggregate coverage report uploads. |
| `DD_CIVISIBILITY_GIT_UPLOAD_ENABLED` | Repository metadata uploads; Git tags stay. |

`DD_CIVISIBILITY_ENABLED=false` switches off all reporting: the build is still
instrumented, but the tests run as with `go test` and send nothing. When the
variable is absent, ddto sets it to `parent`. A test binary then reports, and
once Mini has started it sets the variable to `false` for the processes it
starts. A helper started before that, from `init` or from `TestMain` before
`m.Run`, inherits `parent`; if it is itself an instrumented test binary, start
it with `DD_CIVISIBILITY_ENABLED=false`, or it reports a session of its own.
The [README](README.md#environment-variables) lists every variable and
default.

## When something breaks

1. Run the same command with plain `go test`. If it also fails, the problem
   belongs to the project.
2. If only the ddto build fails, keep the output of a run with
   `DD_TRACE_DEBUG=true` and [report it](#report-a-ddto-bug). To unblock CI,
   run `go test` without the `ddto` prefix; `DD_CIVISIBILITY_ENABLED=false`
   does not skip the instrumented build.
3. If tests fail only under ddto, check the retry and delivery settings above,
   then compare with `--runtime=sdk`, which uses the unmodified dd-trace-go
   SDK and needs the version pinned in
   [`internal/version`](internal/version/version.go).

Never edit the project's sources, `go.mod` or `go.sum` to work around ddto,
and never set `DD_CIVISIBILITY_INTERNAL_*` or `DDTO_*` variables: ddto manages
them.

## Report a ddto bug

When a problem belongs to ddto, tell the user and offer to prepare an issue in
[this repository](https://github.com/tonyredondo/dd-ci-testing-poc/issues).
That covers a build that fails only under ddto, a panic or an `ERROR` line
from `TestOptimization.build` or `TestOptimization.run`, a run that hangs,
missing or wrong events, and a `WARN` fallback to plain `go test` that the
project does not explain. Collect:

1. ddto's version from
   `go version -m "$(command -v ddto)" | grep -E '^\s+(mod|build\s+vcs\.)'`,
   which prints its module version and, for a binary built from a checkout,
   the commit and whether it had local changes, plus `go version` and
   `go env GOOS GOARCH GOTOOLCHAIN GOFLAGS`.
2. The ddto command that fails, the plain `go test` command that works, and
   both exit codes.
3. The `DD_*` variables set for the run, with secrets written as `<set>`.
4. What the project uses: the `go` line of its `go.mod`, the modules its
   tests build from
   `go list -deps -test -f '{{with .Module}}{{.Path}} {{.Version}}{{with .Replace}} => {{.Path}} {{.Version}}{{end}}{{end}}' ./... | sort -u | grep -E 'testify|goleak|dd-trace-go'`,
   which shows replacements after `=>` and also works with vendoring (write
   local replacement paths as placeholders), the Orchestrion version if it uses
   Orchestrion, and whether it vendors, uses `go.work` or runs tests through
   `-exec`.
5. The error from a run with `DD_TRACE_DEBUG=true` and the 20 to 40 lines
   before it, instead of the whole log.
6. A minimal reproduction, such as a small module or a single test, when one
   is easy to write.

This repository is public. Before showing the issue to anyone, replace API
keys, tokens and other credentials, internal hostnames and URLs, private
repository and service names, and personal paths and emails with
placeholders. Debug logs and the state that `--keep-ci-state` keeps include
the repository URL, commit messages and author emails.

Write the issue to a file outside the project, such as in a temporary
directory, with this layout:

````markdown
### What happened

<what failed, in a few sentences>

### Commands

- ddto: `<command>`, exit <code>
- go test: `<command>`, exit <code>

### Environment

- ddto: <module version and commit>
- Go: <go version>, <GOOS>/<GOARCH>
- Settings: <DD_* variables, secrets as <set>>
- Project: go <version>; <Testify, goleak, dd-trace-go, Orchestrion versions>; <vendor, go.work, -exec>

### Output

```text
<the trimmed DD_TRACE_DEBUG output>
```

### Reproduction

<steps or a minimal module>
````

Show it to the user, then print a link that opens GitHub's new-issue form
with the title and body filled in:

```sh
python3 - /tmp/ddto-issue.md "ddto test: <one-line summary>" <<'EOF'
import sys, urllib.parse
query = urllib.parse.urlencode(
    {"title": sys.argv[2], "body": open(sys.argv[1]).read()},
    quote_via=urllib.parse.quote,
)
print("https://github.com/tonyredondo/dd-ci-testing-poc/issues/new?" + query)
EOF
```

GitHub rejects very long links, so keep the link under about 8,000
characters: trim the output in the body, and ask the user to attach the full
log to the issue instead. The user reviews and submits the issue. Create it
yourself, for example with
`gh issue create --repo tonyredondo/dd-ci-testing-poc --title "<summary>" --body-file /tmp/ddto-issue.md`,
only when the user asks you to.

## Use ddto from another project

To make agents in another repository use ddto, add this to that repository's
`AGENTS.md` or `CLAUDE.md`:

```markdown
## Datadog Test Optimization

Run Go tests through ddto so they report to Datadog Test Optimization.
Install it with `go install github.com/tonyredondo/dd-ci-testing-poc/cmd/ddto@main`
and run `ddto test` with the usual `go test` flags and packages.
Settings for this project: <for example DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process>.
Guide: https://github.com/tonyredondo/dd-ci-testing-poc/blob/main/AGENTS.md
```

## Changing this repository

[docs/README.md](docs/README.md) indexes the design, validation and
maintenance guides. Before proposing a change:

- Run `go vet ./...` and `go test ./...`. The full SDK comparison also needs
  the pinned Orchestrion in `ORCHESTRION_BIN`; see
  [feature parity](docs/ci-parity.md#run-and-maintain-the-gate).
- Changes under `internal/thirdparty` update
  [`ADAPTATIONS.md`](internal/thirdparty/dd-trace-go/ADAPTATIONS.md) and the
  origin's `SOURCE.json` hashes, and `python3 scripts/upstream.py verify` must
  pass; see [source maintenance](docs/maintenance.md).
- Mini, the CLI and the ported tests import only the standard library and this
  module; `python3 scripts/dependency_boundary.py` checks it.
- `docs/benchmarks.md` and `docs/results/**` are generated by the benchmark
  scripts; never edit them by hand. See
  [the benchmark guide](docs/build-benchmarks.md).
- CI skips changes limited to `docs/` and `README.md`; other changes run the
  full compatibility matrix.
