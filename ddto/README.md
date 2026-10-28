# ddtest instrument: Go test instrumentation

`ddtest instrument` instruments Go tests and reports them to Datadog Test
Optimization (CI Visibility). Put it in front of `go test`: it prepares
temporary build overlays, then lets Go compile and execute the tests.

This directory holds its Go module, `github.com/DataDog/ddtest/ddto`; `ddto` is
the internal name of the module and its packages. The default Mini runtime
contains the CI policies from `dd-trace-go` and a small event client. It adds
no external runtime module dependencies. Your application can still use the
full SDK for APM. The instrumentation is experimental; compatibility is
checked against pinned Go, SDK and library versions.

## Install

Shell examples use POSIX syntax. On Windows, use Git Bash or set environment
variables with PowerShell; locally built executables can use a `.exe` suffix.

Tests need an installed Go 1.25 or newer toolchain. Install ddtest from its
[latest release](https://github.com/DataDog/ddtest/releases/latest):

```sh
curl -fsSL https://github.com/DataDog/ddtest/releases/latest/download/install.sh | bash
```

In PowerShell, on Windows or elsewhere:

```powershell
irm https://github.com/DataDog/ddtest/releases/latest/download/install.ps1 | iex
```

The scripts download the executable for your platform into `~/.local/bin`
and check it against the release's `SHA256SUMS`. `--version v1.2.3` picks a
release and `--dir` another directory; in PowerShell, set
`$env:DDTEST_INSTALL_VERSION` and `$env:DDTEST_INSTALL_DIR` first, or run
`& ([scriptblock]::Create((irm <url>/install.ps1))) -Version v1.2.3 -Dir C:\tools`.
`DDTEST_INSTALL_DOWNLOAD_URL` names a mirror of the release's files. The
scripts keep an installed executable of the same version, so a persistent
runner downloads nothing for a requested version and only `SHA256SUMS` for the
latest. Without an executable for the platform, or when its checksum differs,
they stop. In GitHub Actions they add the directory to `PATH` for later steps;
`install.ps1` also adds it to the current session.

`ddtest instrument --version` prints `ddtest instrument <version>`: the
instrumentation is released with ddtest and reports ddtest's version, in its
logs, telemetry and test events too.

Releases have `ddtest-<os>-<arch>` for Linux, macOS (`darwin`) and Windows on
`amd64` and `arm64`, with `.exe` on Windows. Each executable carries the Mini
runtime's sources and its tool wrapper, so a CI job builds neither ddtest nor
the wrapper and downloads no runtime module; it compiles only what goes into
the test binaries.

To build from a checkout of ddtest instead:

```sh
git clone https://github.com/DataDog/ddtest.git
cd ddtest
make install   # ddtest, in GOBIN or the first GOPATH entry's bin
```

Coding agents can follow [AGENTS.md](AGENTS.md), which covers installation,
verification and the settings to choose for a project. AGENTS.md and the
[documents](docs/README.md) of this directory still write `ddto <arguments>`:
in ddtest, that is `ddtest instrument <arguments>`, with the same behavior.

Run `ddtest instrument` from the module you want to test. A toolchain that
`GOTOOLCHAIN` downloads into `GOMODCACHE` works too: Go rejects overlays there,
so the CLI runs that toolchain from a mirror in its user cache, under
`ddtest/instrument/goroots`.
[Runtime provisioning](docs/mini-runtime.md#runtime-provisioning) covers the
mirror, vendor mode, workspaces and local replacements.

## Report your tests

With a Datadog Agent that accepts CI Visibility requests, run from your project:

```sh
DD_SERVICE=my-tests DD_ENV=ci ddtest instrument go test ./...
```

`ddtest instrument test` is the short form of `ddtest instrument go test`, also
with go's `-C dir` before `test`. Any other command runs unchanged, with its
arguments, standard streams, environment and exit status:
`ddtest instrument go build ./...` runs `go build ./...`. As with `env`, a
command that is not found exits with 127, and one that cannot run with 126. A
CI script can put `ddtest instrument` in front of its go commands; only
`go test` reports to CI Visibility. gotestsum, and any command after
`--go-shim`, report the `go test` that they run; see [gotestsum](#gotestsum)
and [Other test wrappers](#other-test-wrappers). The one addition is for go:
its environment gets `DDTEST_INSTRUMENT_NESTED_GO`, which counts the
`ddtest instrument` commands that run go around it. A `go` on `PATH` that
starts the CLI again, such as a script that runs `ddtest instrument go`, would
otherwise make it start itself without end; it stops at the ninth with an
error that names that `go`. With `DD_TRACE_DEBUG=true`, the CLI logs on
`stderr` which program it started and how; see
[CLI build diagnostics](docs/cli-debug.md#commands-other-than-go-test).

ddtest is a Go program, and a command starts with the process state that Go's
runtime leaves. On Unix, some signals that the caller ignored or blocked,
such as SIGTERM, reach the command with their default action, and a standard
stream that the caller closed is open on `/dev/null`. On Windows, the
command receives ddtest's command line after `ddtest instrument` as written,
and ddtest waits for it in a job that ends it if ddtest is terminated.

Set `DD_TRACE_AGENT_URL` to select an Agent address. Without an override, Mini
uses the default Agent Unix socket when present, then `http://localhost:8126`.
For Agentless delivery, supply `DD_API_KEY` through your usual secret
configuration and select your Datadog site:

```sh
DD_CIVISIBILITY_AGENTLESS_ENABLED=true DD_SITE=datadoghq.com \
  ddtest instrument go test ./...
```

Git metadata normally comes from the checkout and CI environment. Repository
URL and commit SHA are required to fetch Test Optimization settings. Outside a
Git checkout, supply `DD_GIT_REPOSITORY_URL` and `DD_GIT_COMMIT_SHA`.
[Runtime configuration](docs/mini-runtime.md#configuration) lists the main
settings and their defaults.

For `go test`, the CLI sets `DD_CIVISIBILITY_ENABLED=parent` when it is absent
and preserves explicit values. Go's test cache works as with `go test`: the
first run builds instrumented binaries and runs every package, and later runs
report the packages that Go runs again. Add `-count=1` when every test must
report on every run. Delivery errors are logged, and the command keeps Go's
exit code. When go rejects the command, as with an invalid flag, go test
reports the error itself, with its own exit status.
The runtime is provided through temporary module files when needed, leaving
your sources unchanged. Explicit `-mod=mod` still permits Go's own module edits.

## Common commands

```sh
# Select tests and keep Go's normal output.
ddtest instrument go test -count=1 -run '^TestRequest$' -v ./...

# Race detection and atomic coverage of client packages.
ddtest instrument go test -count=1 -race -covermode=atomic -coverpkg=./... ./...

# JSON output; CLI diagnostics stay on stderr.
ddtest instrument go test -count=1 -json ./...

# Compile test binaries without running tests or sending runtime events.
mkdir -p test-binaries
ddtest instrument go test -c -o "$PWD/test-binaries/" ./...

# Execute one compiled binary from the package's expected working directory.
DD_CIVISIBILITY_ENABLED=parent ./test-binaries/example.test \
  -test.count=1 -test.timeout=10m -test.v

# Compare with the pinned, unmodified full SDK runtime.
ddtest instrument go test --runtime=sdk -count=1 ./...
```

Mini is selected when `--runtime` is omitted. Both `--runtime=mini` and
`--runtime mini` work among Go flags and package names. Put the option before
custom test flags, `-args` or `--`, which begin the test binary's arguments.

More options follow the same rule:

- `--git-upload-timeout=2m` (default one minute) bounds every wait for the
  repository upload of the CLI's Git worker. Test processes wait at most that
  long for its result, and the CLI waits at most that long after `go test`
  exits, then stops the upload cleanly.
- `--keep-ci-state` keeps the invocation's CI state and prints where;
  `--keep-ci-state=state.zip` archives it. See
  [Keeping the CI state](docs/cli-debug.md#keeping-the-ci-state). It also
  keeps the test session's directory.
- `--panic-hook` edits Go's runtime so that Mini sends its events before an
  unrecovered panic in any goroutine ends a test process, and so that a fatal
  error's message, such as `concurrent map writes`, reaches the session. The
  first build with it, per Go version and build variant, compiles every
  package again; other projects then reuse the standard library that it
  compiled. See [Crashes](internal/thirdparty/dd-trace-go/ADAPTATIONS.md#crashes).
- `--session-per-binary` keeps one test session per test binary instead of
  one for the command.
- `--experimental-file-coverage` takes per-test coverage for Test Impact
  Analysis from one mark per source file instead of Go's coverage counters. It
  needs no `-cover`, and it reports every package of the repository that a
  test runs. Rows name files without line bitmaps. Without Mini, with Go's
  coverage flags or with `-race`, the CLI warns and keeps Go's counters.
  Top-level tests then run one at a time while coverage is collected.
  `--experimental-file-coverage=context` keeps `t.Parallel()`: goroutines,
  channels and timers carry the tests that they work for, so each test still
  reports the files that ran for it. Without the flag,
  `DD_CIVISIBILITY_EXPERIMENTAL_FILE_COVERAGE=true` or `=context` does the
  same; the flag decides when both are set.
  See [Experimental file coverage](docs/mini-runtime.md#experimental-file-coverage).

`ddtest instrument` reports one test session per command: every package's
tests report in it, and it lasts the command and has `go test`'s exit status.
A test binary run without `ddtest instrument` reports its own session, and so
does each test binary when a tested package lies in another Git checkout than
`go test`'s directory, whose Git metadata and service the session takes. When
a test process crashes, the session says how; testing's `-timeout` sends the
tests' events first, and so does an unrecovered panic with `--panic-hook`. For
other crashes, the CLI reports, failed, the module and suites that the process
left open and the test that the crash names.
[Details](internal/thirdparty/dd-trace-go/ADAPTATIONS.md#ddtos-test-session)

`--runtime=sdk` requires the exact SDK version pinned in
[`internal/version`](internal/version/version.go).

## gotestsum

Put `ddtest instrument` in front of the gotestsum command that you already run:

```sh
ddtest instrument gotestsum --format testname --junitfile junit.xml -- -race ./...
ddtest instrument go tool gotestsum -- ./...                      # a tool of the module
ddtest instrument go tool -modfile=tools.mod gotestsum -- ./...   # or of a tools modfile
ddtest instrument go run gotest.tools/gotestsum@v1.13.0 -- ./...
```

gotestsum keeps its arguments, output and exit status, with `--packages`,
`--rerun-fails`, `--watch`, `--junitfile` and `--raw-command`, and the
`go test` that it runs reports to CI Visibility. The CLI puts a `go` of its
own, in its user cache under `ddtest/instrument/go-shim`, first on gotestsum's
`PATH`. When gotestsum runs `go test -json …`, that `go` runs
`ddtest instrument go test -json …`, and each of them leaves it off `PATH`, so
the tests see gotestsum's `PATH`. `go run` and `go tool` put `$GOROOT/bin`
first on their program's `PATH`, so the CLI runs gotestsum through its own
`-exec` program or directly.

Each `go test` that gotestsum runs reports its own session, such as each
`--rerun-fails` run. The CLI's options, such as `--runtime`, do not apply
there, so a test flag named `--runtime` stays the test's. If the CLI cannot
set up its `go`, it warns and runs gotestsum without Test Optimization.

## Other test wrappers

`--go-shim` does the same for any command that runs `go test`, or starts one
that does:

```sh
ddtest instrument --go-shim make test
ddtest instrument --go-shim task test
ddtest instrument --go-shim ./scripts/test.sh
ddtest instrument --go-shim go run ./tools/testrunner
```

Each `go test` that the command runs reports its own session, and other go
commands run unchanged. Only a `go` found on `PATH` reports: a command that
runs `$GOROOT/bin/go` by its path or sets its own `PATH` runs `go test`
without Test Optimization, as do commands that `go generate` starts, which find
`$GOROOT/bin` first. A `go run` or `go tool` that the command starts keeps
the CLI's `go` for its program, unless `go run` has its own `-exec` or builds
for another platform; then the CLI warns and runs it without its `go`.
`ddtest instrument --go-shim go test …` is `ddtest instrument go test …`.

## What is supported

| Area | Support and limits |
| --- | --- |
| Native testing | `TestMain`, subtests, parallel tests, cleanup, failure/skip methods, benchmarks and Go's test flags |
| Fuzz and Examples | Fuzz roots, executed seeds and executable examples; active fuzz mutations are not individual CI tests. [Details](docs/fuzz-examples.md) |
| Test Optimization | Automatic retries, early flake detection, intelligent test skipping, impacted tests and test management, according to backend settings. [Feature matrix](docs/ci-parity.md) |
| Coverage | Aggregate coverage and per-test count/atomic coverage, including parallel tests, cleanup and retries. [Validation](docs/validation.md#mini-runtime-contracts) |
| Testify suites | v1.4.0 and newer v1 releases, including external callers and compatible forks. Unsupported entries warn and run as ordinary subtests. [Details](docs/testify.md) |
| Goleak | Automatic Mini integration for v1.3.0 and newer v1 releases, including compatible forks; real test leaks remain visible. [Details](docs/delivery.md#automatic-goleak-integration) |
| Orchestrion and APM | Combined builds; SDK spans under context-associated Mini tests report to Test Optimization, not APM. [Build commands](docs/orchestrion.md), [span association](docs/sdk-span-mirror.md) |
| Bazel | Offline manifest and payload-file contracts; a real Bazel toolchain invocation is not part of the validation. [Details](docs/mini-runtime.md#delivery-and-offline-output) |
| Platforms | Linux with Go 1.25/1.26/1.27 and tip, macOS and Windows with Go 1.27; Linux also runs Go 1.25 and 1.27 with `-race`. [CI matrix](docs/validation.md#go-instrumentation-workflow) |

With `ddtest instrument`, a client's `go.mod` can declare Go 1.21 or newer; the
installed toolchain must meet Mini's Go 1.25 minimum. Older toolchains are
rejected before module provisioning. Instrumentation preserves the client's
language version, including its loop-variable behavior. A direct `testopt`
dependency requires the consuming module to declare Go 1.25 or newer. Future Go
releases and private SDK layouts need their own compatibility runs.

Standard-library test targets are unsupported, and explicit `.go` file mode
runs native Go without instrumentation and prints a warning. The
[validation contract](docs/validation.md) distinguishes loopback protocol
checks from live intake, UI and downstream-project validation.

## Environment variables

Set these variables before invoking `ddtest instrument` or a compiled test
binary. The tables describe the default Mini runtime; `--runtime=sdk` uses the
pinned SDK's configuration. Boolean settings accept `true` and `false`.
CI-provider variables are detected automatically. The CLI hands its own
processes variables that start with `DDTEST_INSTRUMENT_`, such as the Git
state, the test session and the saved Go settings; do not set them.
### Reporting and delivery

| Variable | Default | Meaning |
| --- | --- | --- |
| `DD_CIVISIBILITY_ENABLED` | CLI: `parent` when absent | `parent` activates the test process and disables reporting in ordinary child processes; `true` enables it and `false` disables it. Explicit values are preserved. Standalone binaries need activation explicitly. |
| `DD_CIVISIBILITY_AGENTLESS_ENABLED` | `false` | Send directly to Datadog instead of through an Agent. |
| `DD_API_KEY` | Unset | Required for Agentless HTTP delivery. |
| `DD_SITE` | `datadoghq.com` | Datadog site used for Agentless endpoints. |
| `DD_CIVISIBILITY_AGENTLESS_URL` | Derived from `DD_SITE` | Custom CI endpoint base URL; endpoint paths are appended. |
| `DD_TRACE_AGENT_URL` | Automatic | Agent URL: `http://`, `https://` or `unix://`. Overrides host and port settings. |
| `DD_AGENT_HOST` / `DD_TRACE_AGENT_PORT` | `localhost` / `8126` | Explicit host or port selects HTTP. Otherwise try `/var/run/datadog/apm.socket`, then HTTP on localhost. |
| `DD_SERVICE` | Repository name, or `go.test` fallback | Test service; a nonempty value overrides CODEOWNERS naming. |
| `DD_ENV` / `DD_VERSION` | Unset | Environment and tested service version. |
| `DD_TAGS` | Unset | Custom `key:value` tags, separated by commas or spaces. `test.configuration.*` tags also participate in feature requests. |
| `DD_TEST_SESSION_NAME` | CI job name plus command, or command alone | Explicit session name; an explicitly empty value is retained. |

### Test policies and coverage

Backend settings select the policies. Retry/EFD overrides apply after settings
load; the other feature switches below can disable a backend-enabled feature.
Coverage still requires Go coverage flags.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DD_CIVISIBILITY_FLAKY_RETRY_ENABLED` | Backend setting | Override automatic test retries. |
| `DD_CIVISIBILITY_FLAKY_RETRY_COUNT` | `5` | Maximum retries per failing test when automatic retries are enabled. |
| `DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT` | `1000` | Automatic retry budget for one test-binary session. |
| `DD_CIVISIBILITY_DYNAMIC_ATR_ENABLED` | `false` | Take each test's automatic retry count from its first execution's duration: up to 5s, 10s, 30s, 5m, and slower. `DD_CIVISIBILITY_FLAKY_RETRY_COUNT` is then ignored. |
| `DD_CIVISIBILITY_DYNAMIC_ATR_BUCKETS` | Backend EFD counts | Five comma-separated retry counts from 1 to 20, one per duration bucket, for example `3,2,1,1,1`. Without it, the first four counts follow the backend's EFD slow test retries; the defaults are 10, 5, 3, 2 and 1. |
| `DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED` | Backend setting | Override early flake detection; the backend must still enable known-test data. |
| `DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES` | `-1` | Nonnegative values cap EFD retries without increasing backend counts; negative values retain those counts. |
| `DD_CIVISIBILITY_RETRY_EXECUTION_MODE` | `in_process` | `in_process` reuses the binary's process; `process` launches isolated retry children. Those run after testing has reported the package, so without `-v` a test that still fails after them shows only the retry summary, not its output; its CI events keep the error. |
| `DD_CIVISIBILITY_RETRY_PROCESS_TIMEOUT` | `10m` | Positive Go duration, such as `30s`, that bounds a retry child. A shorter `-timeout` and the test binary's remaining deadline shorten it. |
| `DD_CIVISIBILITY_RETRY_PROCESS_MAX_CONCURRENCY` | `1` | Positive maximum number of concurrent process-retry children. |
| `DD_TEST_MANAGEMENT_ENABLED` | `true` | Allow backend test-management policies; `false` disables them. |
| `DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES` | Backend setting (`-1`) | Override attempt-to-fix retry count; `-1` keeps the backend value. |
| `DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED` | `true` | Allow subtest-specific management and retry policies. |
| `DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED` | `true` | Allow backend-enabled impacted-test detection. |
| `DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED` | `true` | Allow backend-enabled aggregate coverage-report uploads. |
| `DD_CODE_COVERAGE_FLAGS` | Unset | Comma-separated report labels, up to 32. |

See the [feature matrix](docs/ci-parity.md) for combinations and limitations.

### Mini behavior and diagnostics

| Variable | Default | Meaning |
| --- | --- | --- |
| `DD_CIVISIBILITY_DEFERRED_DELIVERY` | `false` | Send queued CI data between completed test groups. The next group waits for delivery; parallel groups can buffer more data. [Delivery and goleak](docs/delivery.md) |
| `DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS` | `false` | Derive package services when `DD_SERVICE` has no value. [CODEOWNERS configuration](docs/codeowners-service.md) |
| `DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS_FORMAT` | `service-$(owner)` | Service format; quote it with single quotes so the shell passes `$(owner)` literally. |
| `DD_CIVISIBILITY_APM_SPANS_ENABLED` | `false` | Also send `dd-trace-go` spans created under Mini tests to APM, with their own traces. By default they join the test's trace and only their copies are sent, to Test Optimization. [SDK spans](docs/sdk-span-mirror.md) |
| `DD_TRACE_DEBUG` | `false` | Build and runtime debug logs, including phase and request timings. |
| `DD_CIVISIBILITY_LOGS_ENABLED` | `false` | Send CI diagnostic logs. |
| `DD_LOGGING_RATE` | `60` | Error-log aggregation interval in seconds; `0` reports errors immediately. |
| `DD_INSTRUMENTATION_TELEMETRY_ENABLED` | `true` | Enable CI instrumentation telemetry. |
| `DD_TELEMETRY_METRICS_ENABLED` | `true` | Collect telemetry metrics. |
| `DD_TELEMETRY_LOG_COLLECTION_ENABLED` | `true` | Collect telemetry diagnostic logs, separately from CI logs. |
| `DD_TELEMETRY_DEBUG` | `false` | Mark telemetry payloads for debug handling. |

Goleak integration is automatic when a supported library is reachable, in both
delivery modes, and needs no environment switch.

### Git identity and offline inputs

Nonempty Git overrides take precedence over detected values. Unset fields use
the CI environment or checkout.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DD_GIT_REPOSITORY_URL` / `DD_GIT_COMMIT_SHA` | Detected | Repository URL and commit SHA; both are required for settings requests. |
| `DD_GIT_BRANCH` / `DD_GIT_TAG` / `DD_GIT_COMMIT_MESSAGE` | Detected | Branch, tag and commit message. |
| `DD_GIT_COMMIT_AUTHOR_NAME` / `DD_GIT_COMMIT_AUTHOR_EMAIL` / `DD_GIT_COMMIT_AUTHOR_DATE` | Detected | Commit author metadata. |
| `DD_GIT_COMMIT_COMMITTER_NAME` / `DD_GIT_COMMIT_COMMITTER_EMAIL` / `DD_GIT_COMMIT_COMMITTER_DATE` | Detected | Committer metadata. |
| `DD_GIT_PULL_REQUEST_BASE_BRANCH` / `DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA` | Detected | Pull-request base branch and SHA for impacted-test discovery. |
| `DD_CIVISIBILITY_GIT_UPLOAD_ENABLED` | `true` | Allow repository metadata uploads; `false` disables uploads while retaining Git tags. |
| `DD_TEST_OPTIMIZATION_ENV_DATA_FILE` | `<binary>.env.json` beside the executable | Environmental-data JSON file. |
| `DD_TEST_OPTIMIZATION_MANIFEST_FILE` | Unset | Offline manifest used to resolve cached feature responses. |
| `DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES` | `false` | Write test, coverage and telemetry payloads to files. |
| `TEST_UNDECLARED_OUTPUTS_DIR` | Unset | Required output directory for payload-file mode; payloads go beneath `payloads/`. |

[Runtime configuration](docs/mini-runtime.md) describes provisioning and the
[offline file contract](docs/mini-runtime.md#delivery-and-offline-output),
including manifest credentials and validation limits.

## Debug and maintain

```sh
DD_TRACE_DEBUG=true ddtest instrument go test -count=1 -v ./...
```

Build logs start with `TestOptimization.build <version>` and Mini logs with
`TestOptimization.run  <version>`, with ddtest's version. Both include
timestamps. [Reading the timings](docs/cli-debug.md) explains preparation,
subprocesses, requests and shutdown, including overlapping phases.

The [documentation index](docs/README.md) links user guides, architecture
diagrams, compatibility tests and source-update procedures.

## Changes from the standalone ddto

The instrumentation was a separate CLI, `ddto`, before it moved into ddtest.
Its behavior is the same; what it is called, and what it leaves on your
machine, changed:

| Before, with ddto | Now, with ddtest instrument |
| --- | --- |
| `ddto test ./...`, `ddto gotestsum …`, `ddto --go-shim …` | `ddtest instrument go test ./...`, `ddtest instrument gotestsum …`, `ddtest instrument --go-shim …` |
| Standalone ddto releases, `ddto-<os>-<arch>`, ddto's own version | ddtest's releases, `ddtest-<os>-<arch>`, ddtest's version |
| Install settings `DDTO_VERSION`, `DDTO_INSTALL_DIR`, `DDTO_DOWNLOAD_URL`; a fallback to `go install` | `DDTEST_INSTALL_VERSION`, `DDTEST_INSTALL_DIR`, `DDTEST_INSTALL_DOWNLOAD_URL`; no fallback |
| Variables that the CLI hands its processes: `DDTO_NESTED_GO`, `DDTO_GIT_STATE`, `DDTO_TEST_SESSION`, `DDTO_GITHUB_JOB_ID`, `DDTO_USER_TOOLEXEC`, `DDTO_ORCHESTRION_BYPASS`, `DDTO_ORIGINAL_*` | `DDTEST_INSTRUMENT_NESTED_GO`, `DDTEST_INSTRUMENT_GIT_STATE`, `DDTEST_INSTRUMENT_TEST_SESSION`, `DDTEST_INSTRUMENT_GITHUB_JOB_ID`, `DDTEST_INSTRUMENT_USER_TOOLEXEC`, `DDTEST_INSTRUMENT_ORCHESTRION_BYPASS`, `DDTEST_INSTRUMENT_ORIGINAL_*` |
| User cache `<UserCacheDir>/ddto/` | `<UserCacheDir>/ddtest/instrument/`, with the same `runtimes`, `toolwraps`, `goroots`, `vendor-workspaces` and `go-shim` |
| Tool wrapper `ddto-toolwrap` | `ddtest-toolexec` |
| Temporary directories and files `ddto-*` and `_ddto-*` | `ddtest-instrument-*` and `_ddtest-instrument-*` |
| CI state file `ddto.json`, GOROOT mirror record `.ddto-goroot-mirror.json` | `ci-state.json`, `.goroot-mirror.json` |
| Messages, warnings and logs that named ddto | They say the CLI; the debug phase of the whole command is `cli` |
| Module `github.com/DataDog/ddto` | `github.com/DataDog/ddtest/ddto`, in this directory |

A previous ddto's cache under `<UserCacheDir>/ddto/` is no longer used; remove
it to reclaim its space. The variables that the CLI hands its processes are
internal: nothing needs to set them.

<!-- build-benchmark-summary:start -->
## Benchmarks

[Build, runtime and memory tables](docs/benchmarks.md) compare Native,
Orchestrion and POC Mini at 4/32 CPUs, with coverage and race cases.
The [recorded build data](docs/results/20261016-linux-go1.27.1/build/README.md) contains
4,668 comparative observations.
Mini was measured on 2026-10-16 at `bfcf898db959aea62e14ef04aedd56d0e4ac226e`.
Native and Orchestrion retain their 2026-10-04 observations.

[Run benchmarks or regenerate the tables](docs/build-benchmarks.md).
<!-- build-benchmark-summary:end -->
