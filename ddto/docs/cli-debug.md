# CLI build diagnostics

Set `DD_TRACE_DEBUG=true` to see what `ddto` does before the test runtime
starts. The CLI writes diagnostics to `stderr` with the prefix
`TestOptimization.build v0.0.0-dev`. Mini runtime logs use
`TestOptimization.run  v0.0.0-dev`. Both prefixes use the version in
`internal/version`; the SDK backend keeps its own logger and SDK version.

```sh
DD_TRACE_DEBUG=true ddto test -count=1 ./...
```

For a build-only investigation, compile the test binary without running it:

```sh
DD_TRACE_DEBUG=true ddto test -c -o ./tests.bin .
```

`DD_TRACE_DEBUG` accepts Go's `strconv.ParseBool` values, including `true`, `1`
and `TRUE`. An absent, empty, false or invalid value disables CLI debug logs.
Debugging needs no extra flag and writes no log file. Without it, the CLI keeps
its usual warnings, errors and live `go get` progress.

## Read the output

Each debug line starts with local date and time (`YYYY/MM/DD HH:MM:SS`),
followed by the component, version and log level, matching Mini's runtime logger.
The message starts with `+` elapsed time since the CLI began. A phase has a
`started` line followed by `finished duration=... status=ok|error`. The start
line appears before work begins, so a stalled command still shows where it is
waiting. Durations include subprocess waiting and I/O; they do not measure CPU.

The log records:

- The command form that ran go test (`command="go -C test"`, for example).
- The selected runtime, its resolved module version and replacement status, and
  the effective working directory, including `-C`.
- The effective toolchain version and package resolution, with the number of
  requested patterns and resolved packages.
- For a toolchain in `GOMODCACHE`, the `goroot mirror` phase: whether its
  mirror was created or reused, its source and location, and for a new one
  whether files were linked or copied, how many, and how long it took. Without
  a usable mirror, `instrumentation bypass goroot_mirror=unavailable` follows
  the warning, and native `go test` runs.
- When ddto must wrap a `-exec` program or the cross-compilation helper and
  cannot reproduce how `go test` finds or starts it, `instrumentation bypass
  exec_program=unresolved`, `exec_program=unquotable` or `exec_helper=relative`
  follows the warning, and native `go test` runs.
- The `work` package pattern, also in `-coverpkg` or a per-package flag such
  as `-gcflags=work=…`, with a workspace that ddto provides: Mini is one of its
  main modules, so `instrumentation bypass package_pattern=work` follows the
  warning, and native `go test` runs.
- Standard library tests, as `go test fmt`, `std` or `all` select them:
  `instrumentation bypass stdlib_tests` follows the warning, and native
  `go test` runs.
- The tests of a package in the module cache, as `go test` of a downloaded
  dependency's package selects them: go rejects overlay entries there, so
  `instrumentation bypass module_cache_tests` follows the warning, and native
  `go test` runs.
- Outside a Go module, where ddto cannot provide its runtime:
  `instrumentation bypass no_module` follows the warning, and native `go test`
  runs, which reports what it cannot run there.
- A workspace whose runtime ddto cannot resolve or provide, as offline with a
  vendored one: `instrumentation bypass workspace_runtime_unavailable` follows
  the warning, and native `go test` runs, which can still build it.
- An `-overlay` that deletes the module's `go.mod`, after which go finds
  another module root or none: `instrumentation bypass overlay_deletes_go_mod`
  follows the warning, and native `go test` runs.
- A module whose vendor directory go selects but that does not vendor the
  runtime, including a vendor directory without `modules.txt` when `go.mod`
  requires or replaces modules, which go rejects then, or when it holds Go
  files, which a module before Go 1.23 can import:
  `instrumentation bypass vendor_without_runtime` follows the warning, which
  says how to vendor it, and native `go test` runs.
- When go rejects the command or its environment while ddto prepares it, as
  with `-p=0`, an unknown flag in `GOFLAGS`, a missing `-C` directory or
  `-modfile`, an `-overlay` file that go cannot read or parse, or a `go.sum`
  without a checksum that the build needs,
  `instrumentation bypass go_rejected="<reason>"`, and native
  `go test` runs and reports the error with its own exit status. ddto adds a
  warning only if that `go test` passes, which means the failure was ddto's.
- With coverage, whether testing gets the in-memory per-test coverage hook:
  `per-test coverage in_memory_hook=true`, or `false` with the reason. A test
  process's own log says whether it uses the hook: `per-test coverage in
  memory: true`.
- An early `resolve runtime module` probe when Mini is absent from the effective
  module text. It uses a temporary module and checksum file. An unknown module
  is an expected result; Go still resolves every package after provisioning.
- Runtime provisioning and each `go env`, `go mod` or `go get` invocation.
  Mini reports whether it uses a client replacement, local CLI sources, or a
  published version. Local CLI sources can be a checkout, the sources that a
  release executable carries, or its exact cached module. Older client
  languages use a temporary workspace, without changing their module's Go
  directive.
- For a release executable, the state of the runtime sources it carries:
  `bundled runtime state=created` with the extraction's duration, `reused`, or
  `unavailable` with the error. Without a usable user cache, `state=temporary`
  follows: the run's temporary directory holds them. Otherwise ddto looks for
  its version in the module cache. `state=stale` comes first when ddto
  extracts them again.
- The `testing` transformation, its source and rewritten-file counts, and the
  Fuzz and parallel-stop hooks selected for that runtime.
- With Mini, the hooks for [forced exits](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md#forced-exits):
  `signal hook installed=true` when os/signal lets Mini receive only the
  signals that would end the process, and `exit hook installed=true` when
  testing closes Mini's CI Visibility in `os.Exit`; `false` comes with the
  reason. A test process without the signal hook logs
  `civisibility: no signal handler: os/signal was built without ddto's overlay`
  and handles no signal, as under go test. A test process that `os.Exit`
  ends during its tests logs
  `civisibility: os.Exit before CI Visibility closed; closing it`, and
  `ERROR: civisibility: closing CI Visibility in os.Exit did not finish within 1m0s; exiting without it`
  when that shutdown cannot finish.
- The hooks for [crashes](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md#crashes):
  `timeout hook installed=true` when testing's `-timeout` alarm closes Mini's
  CI Visibility before it panics, and `panic hook installed=true` when
  `--panic-hook` edits the runtime to do so before an unrecovered panic;
  `false` comes with the reason, such as `--panic-hook not set`. Both need the
  exit hook.
- `file coverage installed=true packages=<n> marked_files=<n> always_files=<n> context=<bool> context_reason=<reason>`
  when `--experimental-file-coverage` marks the repository's packages:
  `always_files` counts the entries that every test reports, and `context`
  whether `=context` edited the toolchain, which keeps `t.Parallel()`.
  `context_reason` says why `=context` fell back to serial marks. `installed=false` comes with the reason, such as
  `-race`, and a warning. Test processes then log
  `civisibility.cov: per-test file coverage: true [banks:<n> files:<n>]`, or,
  with context banks, `per-test context file coverage: true [banks:<n> words:<n>]`
  and after each test
  `context coverage [epoch:<n> files:<n> contexts:<n> merges:<n> walks:<n> incomplete:<n> overflows:<n> io:<n> leaky:<n>]`:
  the contexts that Mini keeps, receives that added sinks, stack walks and
  those that could not tell every file, overflowed contexts, socket or pipe
  reads, and singleflight calls that leaked to windows. See
  [Experimental file coverage](mini-runtime.md#experimental-file-coverage).
- `test session owned=true` when ddto owns the command's
  [test session](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md#ddtos-test-session),
  followed by `test session id=<id> dir=<dir>`; `false`
  comes with the reason, such as `--session-per-binary`, and the test
  processes then report their own. After go test, with `DD_TRACE_DEBUG=true`,
  ddto logs each process it found
  (`civisibility: test session process <file> closed=<bool> exit_code=<n|none> crash_output_bytes=<n> modules=<n> suites=<n>`),
  where the session's CI tags come from
  (`civisibility: test session CI tags directory=<dir> git_worker_record=<bool> git_timeout=<d>`)
  and what it sends
  (`civisibility: sending ddto's test session id=<id> exit_code=<n> duration=<d> tags=<n> coverage=<pct> crashed_processes=<n> empty=<bool>`),
  where `empty=true` means that every process that joined ran nothing, so a
  successful session is reported as skipped
  ([empty test sessions](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md#empty-test-sessions)).
  Then
  `test session sent=<bool> processes=<n> closed=<n> crashed=<n> recreated=<n> exit_code=<n>`
  counts the test processes that joined it, those that closed CI Visibility,
  those that left crash output, and the modules, suites and tests recreated
  for the processes that did not close.
- Optional library discovery, including how many imports still need a query
  and how many standard-library imports it left out.
  Testify and goleak each report detection, instrumentation and warnings.
- User-overlay loading, final-overlay writing, test-package counts and whether
  a temporary modfile or coverage bridge is needed. `generated_backing_files`
  counts the distinct generated hook/import files; rewritten standard-library
  files are counted separately.
- The selective tool decision for Testify, goleak, coverage, Orchestrion, the
  SDK's CI guard and the Git worker's link signal, plus whether the client
  supplied its own `toolexec`.
- With a selective tool, the state of its wrapper executable:
  `tool wrapper state=created` with `source=build`, or `source=bundle` when a
  release executable wrote the wrapper it carries, and the duration; `reused`;
  or `unavailable` with the error, in which case ddto itself is the wrapper.
  `tool wrapper bundled=unusable` means the carried wrapper did not run, and
  ddto built one.
  `state=stale` comes first when ddto must rebuild it and waits for runs that
  still use the old one; a signal ends that wait. See
  [the bypass](testify.md#the-bypass-and-cache-contract).
- When the Git worker requests for the test processes, and why: see
  [Requests ahead](#requests-ahead).
- Native `go test` duration and exit code, followed by the whole CLI duration
  and exit code. Interrupt forwarding and context cancellation are recorded.

For example, these abbreviated lines explain a local Mini build. The times
illustrate the format; they are not benchmark results:

```text
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +5µs command="test"
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +120µs runtime=mini
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +200µs prepare started
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +400µs resolve packages started
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +24ms resolve packages finished duration=23.6ms status=ok
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +24.1ms provide runtime started
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +31ms mini source=cli-local
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +36ms provide runtime finished duration=11.9ms status=ok
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +58ms plan ready testing_files=3 test_packages=1 overlay_entries=5 generated_backing_files=2 temporary_modfile=true cover_bridge=false
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +59ms tool selection testify=false goleak=false cover=false user_toolexec=false orchestrion=false sdk_ci_gate=false link_signal=false
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +60ms go test started
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +410ms go test finished duration=350ms status=ok
2026/10/07 10:00:00 TestOptimization.build v0.0.0-dev DEBUG: +410.1ms go test exit_code=0
```

Preparation contains the smaller preparation phases. Whole-CLI time contains
preparation, `go test` and cleanup. Do not add these overlapping durations.
A normal `go test` duration includes building and running tests. With `-c`, it
covers building only. To inspect Go's individual compiler and linker commands,
combine debug with native `-x`; the CLI does not profile those tools itself.

## Runtime timing

Mini records these additional durations under the same debug setting:

| Log field | What the duration includes |
| --- | --- |
| `runtime bootstrap finished` | CI tags, tracer setup and other synchronous bootstrap work |
| `settings initialization finished` | Settings setup and its join with initial telemetry, including error paths |
| `ciVisibilityHttpClient: request finished` | One attempt, serialization, response consumption and the backoff before the next attempt; a final attempt has no backoff and reports `retry=false` |
| `telemetry: request finished` | One endpoint attempt through response EOF and close |
| `test-cycle: request finished` | One HTTP attempt through bounded response consumption and close, before retry backoff |
| `test-cycle: send finished` | One payload delivery, including admission pauses, compression, HTTP attempts, backoff and request-body/connection cleanup; in Bazel file mode, conversion and file writing |
| `ci mini tracer: flush finished` | The process-wide client's flush, including waiting for other sends and serializing/delivering queued events |
| `ci mini tracer: close finished` | The process-wide client's terminal close and final delivery |
| `ci mini tracer: final drain` | The batches left for `Close`, the deliveries still in flight and how many senders send them; with all eight slots in flight, `Close` first waits for one to free |
| `ci mini tracer: batch encoded` | A test-cycle payload's events, the size its batch accounted for and its encoded size, before the send |
| `civisibility: session close finished` | Waiting for the session lock, closing modules, finishing the session event and flushing the tracer |
| `civisibility: shutdown barriers finished` | Deferred-delivery drain and all pre-close barriers |
| `civisibility: close actions finished` | Registered close actions, in their existing order |
| `civisibility: logger stop finished` | CI log writer shutdown |
| `civisibility: telemetry stop finished` | Telemetry shutdown, its final sends and idle CI connection cleanup |
| `civisibility: shutdown finished` | The complete CI shutdown call, including state publication and the signal-handler join |

The bootstrap can launch feature initialization asynchronously. Its duration
therefore does not mean the runtime is ready to enter a test. Settings and
`app-started` overlap; do not sum their durations. HTTP summaries identify the
endpoint path or telemetry request type, status and retry information without
adding headers, query strings or bodies.

`civisibility: start-up connections closed before the first test` marks the
end of startup: the startup requests have returned, and Mini has closed the
CI clients' idle connections and canceled the dials that no request waits for.
The [adaptation record](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md#telemetry-startup-and-http-completion)
lists the cases where such a dial can still finish after the first test
starts.

A session closes before `civisibility: exiting`, so its flush can explain a gap
between `PASS` and that line. Shutdown then closes the tracer once; its `close`
summary includes the final delivery of events queued after the session closed.

Test-cycle summaries report a sanitized host/path, one-based attempt number,
HTTP status (`0` without a response), network-error flag, planned retry,
request body bytes and gzip use. The payload summary reports uncompressed bytes
and the actual attempt count. `retry=true` means another attempt is permitted,
although cancellation can still prevent it. `mode=files` identifies Bazel
output without an HTTP request.

For example, these illustrative runtime lines show a completed delivery:

```text
2026/10/07 10:00:00 TestOptimization.run  v0.0.0-dev DEBUG: test-cycle: request finished host=localhost:8126 path=/evp_proxy/v2/api/v2/citestcycle attempt=1 duration=15ms status_code=202 network_error=false retry=false body_bytes=2048 gzip=false
2026/10/07 10:00:00 TestOptimization.run  v0.0.0-dev DEBUG: test-cycle: send finished duration=16ms mode=agent payload_bytes=2048 attempts=1 status=ok
2026/10/07 10:00:00 TestOptimization.run  v0.0.0-dev DEBUG: civisibility: session close finished duration=17ms exit_code=0 already_closed=false skip_if_empty=true empty=false
```

Session close, flush, send and request timings overlap. Complete shutdown contains
the smaller shutdown phases. Do not add them together. `owner=false` on a shutdown
summary means that call did not run the teardown: it either waited for its owner
or found CI already inactive. `already_closed=true` identifies a repeated session
close. Session `exit_code` is the test command's result; delivery errors are
reported separately and do not change it. `status=ok` on a send or flush
describes that operation; past the HTTP response, it says nothing about whether
the backend accepted the events.

Mini writes to the test process's `stderr`; `go test` can include that stream
in package output or JSON `Output` events. Go groups output by package, even
when binaries run concurrently. Keep each package's runtime logs together when
reading a multi-package command, and do not sum their durations as the CLI's
elapsed time.

The telemetry debug duration includes response consumption. The existing
`telemetry_api.ms` metric keeps its measurement at response headers. Test-cycle
`endpoint_payload.requests_ms` also keeps its measurement at headers. Logging
must not redefine either metric or change request accounting.

These logs can produce a phase table for one invocation. Repeated invocations
are needed for medians and ranges. They do not measure the exact first test-body
entry, individual compiler/linker durations, CPU time or memory. A normal
`go test` duration includes both building and executing tests; use `-c` for a
build-only observation.

## Why a test process ended

With `DD_TRACE_DEBUG=true`, a test process's log says how it ended, so that a
failure can be explained without the project's code. go test shows the log
with `-v` or when the package fails.

| Line | Meaning |
| --- | --- |
| `civisibility: test started name=<name> suite=<suite>` | A test or subtest started. |
| `gotesting: test finished name=<name> status=<pass/fail/skip> duration=<d>` | A test ended; `panic="<value>"` follows when it panicked. The last start without a finish names the test that ran when the process ended. |
| `instrumentTestingM: finished with exit code: <n>` | testing.M.Run returned; the process exits normally with that status. |
| `instrumentTestingM: a panic leaves testing.M.Run and will end the process; closing CI Visibility first` | A panic escaped the tests; it ends the process after CI Visibility closes. |
| `civisibility: signal handler registered for [<signals>], ...; ignored by the process: [<signals>]` | Which signals Mini watches, and those it leaves alone because the process ignores them. `civisibility: no signal handler: ...` says why it watches none. |
| `civisibility: signal "<name>" arrived while a channel of the test is registered for it; the signal does not end the process, and CI Visibility keeps running` | The test registered for the signal itself, as a graceful-shutdown test does, so the signal does not end the process. Whether the test's channel took it depends on its buffer. |
| `civisibility: received signal "<name>", which no channel of the test handles and which ends the process: closing CI Visibility` | A signal that ends the process, such as a CI job's cancellation. |
| `civisibility: CI Visibility closed; ending the process with signal "<name>", as without CI Visibility` | Next, go test reports `signal: <name>`. On Windows the process exits with status 1 instead. |
| `civisibility: os.Exit before CI Visibility closed; closing it. os.Exit was called from:` | Something called `os.Exit`, or `log.Fatal`, during the tests. The stack that follows shows the status, as in `os.Exit(0x3)`, and the frame below it names the caller. |
| `civisibility: closed CI Visibility for os.Exit duration=<d>` | CI Visibility's shutdown finished before the process exited. The delivery lines and errors before it say whether the events reached the intake. |
| `ERROR: civisibility: closing CI Visibility in os.Exit did not finish within 1m0s; exiting without it` | The shutdown blocked, and the process exited without it. |
| `test-cycle: delivery uses <transport> endpoint=<url> agentless=<bool>` | Which HTTP transport sends the events: Mini's own, a clone of the client's, or a client's custom RoundTripper. |
| `civisibility: the process crashes (<timeout/panic>); failing the running tests and closing CI Visibility first: <message>` | testing's `-timeout` alarm, or an unrecovered panic with `--panic-hook`, is about to end the process. The stack follows for a panic. |
| `civisibility: failed <n> running tests before the crash` and `civisibility: closed CI Visibility before the crash duration=<d>` | The tests that the crash interrupted were reported as failed, and the events were sent; the crash then ends the process as without Mini. |
| `civisibility: os.Exit while the process crashes; the crash ends the process` | The tests finished while Mini sent before a crash; the crash keeps its exit status. |
| `civisibility: joined ddto's test session id=<id> record=<file> crash_output=<file>` and `civisibility: reporting in ddto's test session id=<id>` | The process reports in the session of ddto's command, and where its record and crash output are; `WARN: civisibility: ... reports its own session` says why it could not. |
| `civisibility: test session record lines=<n> failed=<n> first_error=<error>` | Once CI Visibility closes: how many module and suite starts and ends, and session closes, the process recorded for ddto, which recreates the containers left open if the process crashes, and whether a write failed. |
| `civisibility: failed the test name=<name> suite=<suite> for the crash (<timeout/panic>) final_status=<status>` | A test that the crash interrupted, reported as failed. |
| `civisibility: recorded the close of this process in ddto's test session exit_code=<n> tags=<n> skip_if_empty=<bool> empty=<bool>` | The process closed CI Visibility and left its session tags for ddto. `empty=true` means it ran no test and passed; see [Decisions in a test process](#decisions-in-a-test-process). |

A process that ends without any of these lines was ended by something that
runs no Go code first: a fatal runtime error, an unrecovered panic in another
goroutine without `--panic-hook`, the race detector with
`GORACE=halt_on_error=1`, or a signal that cannot be caught, such as SIGKILL.
The test lines still show which test was running. In ddto's test session, ddto
then logs, with `DD_TRACE_DEBUG=true`,
`civisibility: test process process-<id> ended without closing CI Visibility (<reason>); recreated <n> events`
and, when the crash output names the test,
`civisibility: recreated the crashed test name=<name> suite=<suite> module=<module>`.

## Decisions in a test process

With `DD_TRACE_DEBUG=true`, a test process also logs the choices that change
what it reports. Without it, each of these lines costs one check of the log
level and allocates nothing:

| Line | Meaning |
| --- | --- |
| `civisibility: dynamic automatic test retries enabled [retryCounts: [<5 counts>]]` | `DD_CIVISIBILITY_DYNAMIC_ATR_ENABLED` is set: a failing test gets the retries of its first execution's duration bucket, up to 5 s, 10 s, 30 s, 5 min and slower. A `WARN` before it says that `DD_CIVISIBILITY_DYNAMIC_ATR_BUCKETS` was invalid and ignored |
| `civisibility: dynamic automatic test retries test="<name>" first_duration=<d> bucket=<0-4> retry_count=<n>` | The retries that a test gets, if its first execution fails, from that execution's duration |
| `civisibility: EFD has no retries for test="<name>" first_duration=<d>, using automatic test retries` | A test that Early Flake Detection would retry took 5 min or more, which it does not retry, so a failure gets automatic test retries instead |
| `civisibility: EFD retries suppressed for failing test="<name>", using automatic test retries` | Early Flake Detection stopped retrying in a faulty session, so a test that still fails gets automatic test retries |
| `civisibility: session close finished … skip_if_empty=<bool> empty=<bool>` | `empty=true`: the session ran no test and passed, so it is reported as skipped with `test.session.empty_reason=zero_tests`. A test process in ddto's test session says the same on its `recorded the close` line, and ddto reports its session as skipped only when every test process was empty, with `empty=true` on its `sending ddto's test session` line. In a test process, `skip_if_empty=false` after a successful run means it could not tell whether it ran nothing, and an earlier `a session that runs nothing` line says why |
| `civisibility: a session that runs nothing will not be reported as skipped: wrapped_tests=… wrapped_fuzz=… wrapped_examples=… wrapped_benchmarks=…` | Some workloads, such as benchmarks that `TestMain` selects, run without the module that would show they ran, so an empty session stays passed |
| `civisibility.cov: testing will write its coverage profile to <file>` | Without `-coverprofile`, testing writes its profile to a temporary file for the session's coverage, so it prints its coverage line once. `no temporary coverage profile: <error>` says why it could not, and `session coverage will read the -coverprofile profile` that the command set one |
| `civisibility.cov: session coverage from the profile that testing wrote: <file>` or `… from testing's tear-down: <file>` | Where the session's coverage percentage, its ITR backfill and the LCOV upload come from. `error getting coverage file: testing wrote no coverage profile` means no coverage was reported, as after `-list` |
| `civisibility: CODEOWNERS loaded file="<file>" dialect=<n> diagnostics=<n>` | The ownership rules for `test.codeowners`; `diagnostics` counts the invalid entries |
| `civisibility: no CODEOWNERS file in the CI workspace "<dir>", tests have no owners` or `… in "<dir>" or "<dir>", where the CI workspace "<dir>" is not a directory, …` | Where CODEOWNERS was searched: the CI workspace, or, when it is not a directory where the test runs, the current and test binary's directories |
| `civisibility: CODEOWNERS discovery failed, it will be retried: <error>` | A directory or file could not be read; the next lookup, for another test or suite, tries again |

ddto's Git worker, whose log `--keep-ci-state` keeps in `worker.log`, logs how
it packs the commits that the backend asks for:

| Line | Meaning |
| --- | --- |
| `civisibility: sending pack file with missing commits. files: [<files>]` | The pack files to upload, which the worker removes afterwards |
| `civisibility: <dir> is on another filesystem than the git objects, pack files go to <objects dir>` | git would fail to move its packs across filesystems, so the worker writes them next to the repository's objects |
| `civisibility: git pack-objects failed in <dir>: <error>` | git could not pack the commits in that directory, and the worker tries the next; `WARN: civisibility: error creating pack files …` means every one failed, and the commits were not uploaded |

## Requests ahead

ddto's Git worker requests the settings, known tests, skippable tests and test
management data for the test processes, which then find the responses in the
invocation's read cache. With `DD_TRACE_DEBUG=true`, each side logs its part.
Add `--keep-ci-state` to keep the worker's log, `worker.log`.

`ddto`, on `stderr`:

| Line | Meaning |
| --- | --- |
| `test runtime version=… source=go_env\|go_tool_compile` | The Go version that the worker requests with, as the test binaries report it; `go_tool_compile` when `GOEXPERIMENT` adds experiments |
| `git worker test description packages=… goarch=… runs_every_test=… go_version=…` | What ddto handed the worker in `tests.json` |
| `git worker requests_ahead=while_building reason=…` | go test takes no result from its cache, so the worker requests while it builds. The reason is the first one: `local directory mode`, `test binary flag -count`, `… in GOFLAGS`, an unknown flag, or test binary arguments after the packages, `-args` or `--`. It never shows a value |
| `git worker requests_ahead=at_link` | Results may come from Go's cache: the worker requests when go test links a test binary, which ddto's tool marks as `linking` |
| `git worker requests_ahead=at_first_test reason="-o …"` | `-o` or `-a` link even cached results, so the worker waits for a test process |
| `git worker read cache removed left_directories_removed=…` | The invocation's cache entries went with it; the count is of directories that earlier, killed invocations left |

The worker, in `worker.log`:

| Line | Meaning |
| --- | --- |
| `civisibility: CLI test description [packages:… goarch:… runs_every_test:…]` | The description it read |
| `civisibility: CLI requests ahead started [trigger:… go_version:… clients:… after:…]` | It started requesting: `runs_every_test` while go test builds, `linking` when go test linked a test binary, `used` when a test process started first. `clients` counts the services it requests for; `after` is the time since it started waiting |
| `civisibility: CLI requests ahead finished [client:… known_tests:… skippable:… test_management:… duration:…]` | One client's requests, with the features that the settings enabled |
| `civisibility: CLI request ahead settings failed`, `… known tests failed`, … | A request failed; test processes request it again themselves. The error hides a URL's user and password |
| `civisibility: CLI requests ahead stop: the settings require Git` | The settings change after the upload, so test processes request after it |
| `civisibility: CLI requests ahead not needed: …` | go test exited before any test process used the state, as when every result came from its cache; `started` tells whether requests had begun and were cancelled |
| `civisibility: CLI requests ahead stopped: ddto stopped the worker [reason:…]` | ddto stopped the worker first, for a `signal` or `timeout` |

Test processes, in their output, and the worker:

| Line | Meaning |
| --- | --- |
| `civisibility.read_cache: invocation scope [directory:cli/… ttl:1h0m0s]` | The process shares ddto's invocation's cache |
| `civisibility.read_cache: hit [endpoint:… key:… scope:cli]` | The response was there, usually from the worker |
| `civisibility.read_cache: hit after wait [… waited:…]` | Another process was requesting it; this one waited for its response, for up to 2 s, or up to two minutes while that process runs |
| `civisibility.read_cache: owner gone, taking over [… waited:…]` | The process that held the key's lock ended without releasing it; this one took the lock, requested and stored the response |
| `civisibility.read_cache: waiter fallback [… waited:…]` | The owner released its lock without a response, or the wait ran out; this process requested for itself, without storing the response |
| `civisibility.read_cache: miss [… duration:… cacheable:… error:…]` | This process requested it itself. `cacheable` says whether the response may be stored; a store that fails logs its own line, such as `temp write failed` or `rename failed`. In a test process with `scope:cli`, the worker's request differed, had not started, failed, or was not stored |
| `civisibility.coverage: module from ddto's test description` or `… from go list [… duration:…]` | Where per-test coverage found the test package's module |

A test process prints its output only with `-v` or when a test fails.

For example, a `-count=1` run:

```text
… TestOptimization.build v0.0.0-dev DEBUG: +58ms git worker test description packages=2 goarch=amd64 runs_every_test=true go_version=go1.27.0
… TestOptimization.build v0.0.0-dev DEBUG: +58ms git worker requests_ahead=while_building reason="test binary flag -count"
… TestOptimization.run  v0.0.0-dev DEBUG: civisibility: CLI requests ahead started [trigger:runs_every_test go_version:go1.27.0 clients:1 after:12µs]
… TestOptimization.run  v0.0.0-dev DEBUG: civisibility: CLI requests ahead finished [client:0 known_tests:true skippable:true test_management:true duration:4.1ms]
… TestOptimization.run  v0.0.0-dev DEBUG: civisibility.read_cache: hit [endpoint:settings key:… scope:cli]
```

## Commands other than go test

ddto runs any other command unchanged. With `DD_TRACE_DEBUG=true`, it logs on
`stderr` which program it started and how. The lines name the command and
count its arguments; they never show them. A test form logs its name,
`command="go -C test"` for example, before ddto parses its own options.

On Unix, ddto replaces itself with the program, searching `PATH` as the
platform's `execvp` does. Its lines come before the program's output. They
go to a duplicate of `stderr`, so a reader that already closed it does not
end ddto before the program starts:

```text
… TestOptimization.build v0.0.0-dev DEBUG: +40µs pass-through command="go" args=2
… TestOptimization.build v0.0.0-dev DEBUG: +52µs pass-through search path_entries=4 default_path=false
… TestOptimization.build v0.0.0-dev DEBUG: +55µs pass-through exec path="/home/user/bin/go"
… TestOptimization.build v0.0.0-dev DEBUG: +61µs pass-through exec path="/home/user/bin/go" failed error="no such file or directory"
… TestOptimization.build v0.0.0-dev DEBUG: +63µs pass-through exec path="/usr/local/go/bin/go"
```

For `ddto gotestsum` and `--go-shim`, ddto puts its own `go` first on the
command's `PATH`, and logs why and how:

| Line | Meaning |
| --- | --- |
| `go_shim reason=flag\|gotestsum\|launcher\|go_run_program` | Why the command gets ddto's `go`: `--go-shim`; gotestsum, run directly, with `go tool` or with `go run`; a `go run` or `go tool` that arrived through ddto's `go`, as from a script under `--go-shim`; or the program of a `go run`, which ddto starts again as `go run`'s `-exec` program |
| `go_shim first_on_path=<dir>` | The directory of ddto's `go` that the command gets first on `PATH`. Without it after a `reason` line, a `WARN` says why ddto ran the command without its `go` |
| `go_shim go=<path>` | For a go command, the toolchain's go that it runs |
| `go_shim go_command=run_exec\|module_tool\|toolchain_tool\|tool_not_built\|native` | How that go command runs: `go run` with ddto as its `-exec` program; a tool of the module, which ddto runs itself as `go tool` would; a tool of the toolchain, or one that `go tool -n` could not build, which `go tool` runs and reports; or unchanged |
| `go_shim module_tool="<tool>" path=<path> args=<n> goroot=kept\|set\|removed` | The module tool that ddto runs, its binary and its argument count, and whether ddto kept `GOROOT` or, after a toolchain switch, set or removed it as `go` does |
| `go_shim module_tool forwarded signals=["<name>" …]` | The signals that ddto received while the tool ran and forwarded to it, as `go tool` does, logged once the tool ends |
| `go_shim module_tool exited code=<n> duration=<d>` | ddto's exit status: the tool's, or 1 when the tool did not start or exit cleanly, as `go tool` does. A `go_shim module_tool ended error="<error>"` line before it says why (`signal: killed`, for example) |
| `go_shim as_go=true` | This ddto runs as that `go`, so its go test reports with the command's arguments and no ddto options |
| `go_shim left=<dir>` | Run as that `go`, or under the command, ddto left the directory off `PATH` again, so the tests and the go commands it runs see the caller's `PATH` |
| `nested_go depth=<n>` | This ddto go command runs inside `<n>-1` other ddto go commands, as gotestsum's `go test` runs inside `ddto go tool gotestsum`. ddto stops at 9 with an error that names the go on `PATH` |

Each `exec path` line is a candidate, and a `failed` line follows each
candidate that did not start. The last `exec path` line without one is the
program that runs. `shell=/bin/sh reason="exec format error"` marks a file that
the kernel could not start, such as a script without `#!`, which `/bin/sh`
then runs. `skip path_entry=… bytes=…` marks a `PATH` entry too long for the
libc, which it does not search.

On Windows, ddto starts the program in a job and waits for it:

```text
… TestOptimization.build v0.0.0-dev DEBUG: +40µs pass-through command="build.cmd" args=1
… TestOptimization.build v0.0.0-dev DEBUG: +45µs pass-through caller_command_line=true
… TestOptimization.build v0.0.0-dev DEBUG: +2.1ms pass-through started path="C:\\tools\\build.cmd" job=true breakaway=true
… TestOptimization.build v0.0.0-dev DEBUG: +1.2s pass-through exited code=0 duration=1.2s
```

`caller_command_line=true` means the program received ddto's command line
after `ddto` as written. `job=false` means no job was available, so the program
would outlive a terminated ddto; a preceding `job unavailable step=create|limits|assign`
line says which call failed. The program runs once it is resumed, so its
first output can come before `started`.

## Keeping the CI state

With the Mini runtime and CI Visibility enabled, `ddto test` starts a Git
worker and shares state with the test processes in a temporary directory,
`ddto-git-*` in the system's temporary directory. `ddto` removes it when the
command ends. To keep it for debugging:

```sh
ddto test --keep-ci-state ./...                 # prints: INFO: kept the CI state in /tmp/ddto-git-…
ddto test --keep-ci-state=ci-state.zip ./...    # prints: INFO: archived the CI state in /…/ci-state.zip
```

| File | Contents |
| --- | --- |
| `git.json` | The checkout's Git metadata that the worker read once, and the upload wait that test processes use |
| `telemetry.json` | The invocation's telemetry application: runtime ID, application and host |
| `seq/` | One file per telemetry request number taken, by the worker or a test process |
| `linking` | Present when `go test` linked a test binary in a run whose results could come from its cache; `ddto`'s tool writes it |
| `used` | Present when a test process used the state |
| `upload.json` | The repository upload's result, or why it failed or stopped |
| `impacted.json` | The impacted-tests base commit and modified files, or why they were not computed |
| `tests.json` | `ddto`'s description of the test processes: their architecture, whether `go test` runs every test package without its result cache, and each test package's directory and module |
| `telemetry-failure-*` | Telemetry requests that a test process could not deliver |
| `worker.log` | The worker's own log; add `DD_TRACE_DEBUG=true` for its debug lines |
| `ddto.json` | `ddto`'s view: whether it handed the state over and why, the upload timeout, how the worker ended (`exited`, `stopped` or `killed`), the stop reason and how long `ddto` waited after `go test` |

A command that starts no worker, such as the SDK runtime, `-c` or a disabled
CI Visibility, prints that it has no CI state. The state holds repository
metadata such as commit messages, author emails and the repository URL;
review it before sharing.

## Output, cache and ownership

The CLI's debug lines stay out of `stdout`, including `go test -json`. The CLI
does not change test arguments, generated sources, fingerprints, compiler identities or
exit codes when logging is enabled. Go still decides cache reuse; environment
variables read by a test can legitimately affect Go's test-result cache.

The CLI's debug summaries do not dump arguments, `GOFLAGS`, credentials or
subprocess output. Existing native output, download progress and errors pass
through as before. The working directory is included to diagnose path issues.
Logging failures do not replace the Go command's result.

The tool wrapper and `tool-overlay` do not initialize logging or read
`DD_TRACE_DEBUG`, so unrelated tools and compiler/linker version probes keep
their native output and fast dispatch. The parent logs which integrations
required the tool wrapper; there is no per-tool debug stream, and debugging
never adds a `-toolexec`.

The logger is invocation-local in `internal/runner/debug.go`. A context carries
it through preparation and Go helpers; package APIs such as `PrepareRuntime`
stay silent even when the environment enables debug. The debug writer also
serializes Go's `stderr` with cancellation messages, so callers can safely use
a buffer for that stream. Runtime durations use the incorporated SDK logger;
its adaptations and update checks are recorded in
[`ADAPTATIONS.md`](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md).

The CLI tests compare debug-disabled and debug-enabled binaries byte for byte,
check JSON output and Go cache reuse, and cover build errors, downloads,
provisioning, signals and tool bypasses. These tests run in the compatibility
workflow on Linux, macOS and Windows, including Linux `-race` jobs.

When a combined build selects `go tool orchestrion`, the
`resolve Orchestrion executable` phase measures Go's tool lookup and any cold
tool compilation. Its output is a Go-owned cached executable, reused by all
compiler calls in that invocation. Application compilation and weaving remain
inside the `go test` duration.
