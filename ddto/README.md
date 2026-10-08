# ddtest

`ddtest` instruments Go tests and reports them to Datadog Test Optimization
(CI Visibility). Run it in place of `go test`; it prepares temporary build
overlays, then lets Go compile and execute the tests.

The default Mini runtime contains the CI policies from `dd-trace-go` and a
small event client. It adds no external runtime module dependencies. Your
application can still use the full SDK for APM. This repository is an
experimental POC; compatibility is checked against pinned Go, SDK and library
versions.

## Install

Shell examples use POSIX syntax. On Windows, use Git Bash or set environment
variables with PowerShell; locally built executables can use a `.exe` suffix.

Use an installed Go 1.26 or 1.27 toolchain:

```sh
go install github.com/tonyredondo/dd-ci-testing-poc/cmd/ddtest@main
```

Go places `ddtest` in `GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is unset.
Add that directory to `PATH`. To build from a local checkout:

```sh
git clone https://github.com/tonyredondo/dd-ci-testing-poc.git
cd dd-ci-testing-poc
go build -o bin/ddtest ./cmd/ddtest
```

Run the resulting binary from the module you want to test. The toolchain must
live outside `GOMODCACHE`, because Go rejects overlays inside that directory.
See [runtime provisioning](docs/mini-runtime.md#runtime-provisioning) for vendor
mode, workspaces and local replacements.

## Report your tests

With a Datadog Agent that accepts CI Visibility requests, run from your project:

```sh
DD_SERVICE=my-tests DD_ENV=ci ddtest test -count=1 ./...
```

Set `DD_TRACE_AGENT_URL` to select an Agent address. Without an override, Mini
uses the default Agent Unix socket when present, then `http://localhost:8126`.
For Agentless delivery, supply `DD_API_KEY` through your usual secret
configuration and select your Datadog site:

```sh
DD_CIVISIBILITY_AGENTLESS_ENABLED=true DD_SITE=datadoghq.com \
  ddtest test -count=1 ./...
```

Git metadata normally comes from the checkout and CI environment. Repository
URL and commit SHA are required to fetch Test Optimization settings. Outside a
Git checkout, supply `DD_GIT_REPOSITORY_URL` and `DD_GIT_COMMIT_SHA`.
[Runtime configuration](docs/mini-runtime.md#configuration) lists the main
settings and their defaults.

The CLI sets `DD_CIVISIBILITY_ENABLED=parent` when it is absent and preserves
explicit values. Use `-count=1` for fresh events: Go can otherwise reuse cached
test results. Delivery errors are logged; the command retains Go's exit code.
The runtime is provided through temporary module files when needed, leaving
your sources unchanged. Explicit `-mod=mod` still permits Go's own module edits.

## Common commands

```sh
# Select tests and keep Go's normal output.
ddtest test -count=1 -run '^TestRequest$' -v ./...

# Race detection and atomic coverage of client packages.
ddtest test -count=1 -race -covermode=atomic -coverpkg=./... ./...

# JSON output; CLI diagnostics stay on stderr.
ddtest test -count=1 -json ./...

# Compile test binaries without running tests or sending runtime events.
mkdir -p test-binaries
ddtest test -c -o "$PWD/test-binaries/" ./...

# Execute one compiled binary from the package's expected working directory.
DD_CIVISIBILITY_ENABLED=parent ./test-binaries/example.test \
  -test.count=1 -test.timeout=10m -test.v

# Compare with the pinned, unmodified full SDK runtime.
ddtest test --runtime=sdk -count=1 ./...
```

Mini is selected when `--runtime` is omitted. Both `--runtime=mini` and
`--runtime mini` work among Go flags and package names. Put the option before
custom test flags, `-args` or `--`, which begin the test binary's arguments.
The SDK backend requires the exact version pinned in
[`internal/version`](internal/version/version.go).

## What is supported

| Area | Support and limits |
| --- | --- |
| Native testing | `TestMain`, subtests, parallel tests, cleanup, failure/skip methods, benchmarks and Go's test flags |
| Fuzz and Examples | Fuzz roots, executed seeds and executable examples; active fuzz mutations are not individual CI tests. [Details](docs/fuzz-examples.md) |
| Test Optimization | Automatic retries, early flake detection, intelligent test skipping, impacted tests and test management, according to backend settings. [Feature matrix](docs/ci-parity.md) |
| Coverage | Aggregate coverage and per-test count/atomic coverage, including parallel tests, cleanup and retries. [Validation](docs/validation.md#mini-runtime-contracts) |
| Testify suites | v1.4.0 and newer v1 releases, including external callers and compatible forks. Unsupported entries warn and run as ordinary subtests. [Details](docs/testify.md) |
| Goleak | Automatic Mini integration for v1.3.0 and newer v1 releases, including compatible forks; real test leaks remain visible. [Details](docs/delivery.md#automatic-goleak-integration) |
| Orchestrion and APM | Combined builds and independent SDK span copies under context-associated Mini tests. [Build commands](docs/orchestrion.md), [span association](docs/sdk-span-mirror.md) |
| Bazel | Offline manifest and payload-file contracts; a real Bazel toolchain invocation is not part of the validation. [Details](docs/mini-runtime.md#delivery-and-offline-output) |
| Platforms | Linux with Go 1.26/1.27, macOS and Windows with Go 1.27; Linux also runs the full suite with `-race`. [CI matrix](docs/validation.md#compatibility-workflow) |

A client's `go.mod` can declare an older Go version; the installed toolchain
must meet Mini's Go 1.26 minimum. Future Go and private SDK layouts need their
own compatibility runs. Standard-library test targets are unsupported. Explicit
`.go` file mode runs native Go without instrumentation and prints a warning.
The [validation contract](docs/validation.md) distinguishes loopback protocol
checks from live intake, UI and downstream-project validation.

## Environment variables

Set these variables before invoking `ddtest` or a compiled test binary. The
tables describe the default Mini runtime; `--runtime=sdk` uses the pinned SDK's
configuration. Boolean settings accept `true` and `false`. CI-provider variables
are detected automatically. Internal retry and compiler-wrapper variables are
managed by the tool.

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
| `DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED` | Backend setting | Override early flake detection; the backend must still enable known-test data. |
| `DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES` | `-1` | Nonnegative values cap EFD retries without increasing backend counts; negative values retain those counts. |
| `DD_CIVISIBILITY_RETRY_EXECUTION_MODE` | `in_process` | `in_process` reuses the binary's process; `process` launches isolated retry children. |
| `DD_CIVISIBILITY_RETRY_PROCESS_TIMEOUT` | No additional limit | Positive Go duration, such as `30s`, that bounds a retry child. Native test deadlines still apply. |
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
| `DD_TRACE_DEBUG` | `false` | Build and runtime debug logs, including phase and request timings. |
| `DD_CIVISIBILITY_LOGS_ENABLED` | `false` | Send CI diagnostic logs. |
| `DD_LOGGING_RATE` | `60` | Error-log aggregation interval in seconds; `0` reports errors immediately. |
| `DD_INSTRUMENTATION_TELEMETRY_ENABLED` | `true` | Enable CI instrumentation telemetry. |
| `DD_TELEMETRY_METRICS_ENABLED` | `true` | Collect telemetry metrics. |
| `DD_TELEMETRY_LOG_COLLECTION_ENABLED` | `true` | Collect telemetry diagnostic logs, separately from CI logs. |
| `DD_TELEMETRY_DEBUG` | `false` | Mark telemetry payloads for debug handling. |

Goleak integration is automatic when a supported library is reachable, in both
delivery modes. It needs no environment switch.

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
DD_TRACE_DEBUG=true ddtest test -count=1 -v ./...
```

Build logs use `TestOptimization.build`; Mini logs use `TestOptimization.run`.
Both include timestamps. [Reading the timings](docs/cli-debug.md) explains
preparation, subprocesses, requests and shutdown, including overlapping phases.

The [documentation index](docs/README.md) links user guides, architecture
diagrams, compatibility tests and source-update procedures.

<!-- build-benchmark-summary:start -->
## Benchmarks

[Build, runtime and memory tables](docs/benchmarks.md) compare Native,
Orchestrion, POC SDK and Mini at 4/32 CPUs, with coverage and race cases.
The [recorded dataset](docs/results/20261005-linux-go1.27.1/build/README.md) contains
6,224 comparative observations, measured at
commit `9d4786fbd27f573bb68c5516bf23b336e8d7bdaa`. Use a new run to measure another revision.

[Run benchmarks or regenerate the tables](docs/build-benchmarks.md).
<!-- build-benchmark-summary:end -->
