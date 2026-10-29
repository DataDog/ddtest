# Bazel plan

This plan replaces Orchestrion in
[DataDog/rules_test_optimization](https://github.com/DataDog/rules_test_optimization)
with `ddtest instrument`. It was written on 2026-10-29, after two
throwaway spikes on that date, and nothing in it is implemented yet. Each phase
gets its own pull request. A phase starts only after the plan is agreed and,
for later phases, after the phases it depends on are merged.

## Goal

- `ddtest instrument bazel <dir>` gives Bazel everything it needs to
  instrument Go tests. It comes from the same ddtest release that customers
  already install; there is no other artifact.
- rules_test_optimization uses upstream rules_go. It drops Orchestrion, the
  vendored rules_go trees and the pin files.
- Go tests in Bazel get what `ddtest instrument` gives outside Bazel: standard
  `testing`, Testify suites, goleak, [SDK span copies](sdk-span-mirror.md) and
  the CI guards that keep the application's dd-trace-go out of CI Visibility
  mode. Settings come from the rule's sync manifest, as they do today.

## rules_test_optimization today

Read at `eceed1a`, 2026-10-29:

- `dd_topt_go_test` transitions a hidden `go_test` to one of four vendored
  rules_go trees (`third_party/rgo`, 0.60.0 to 0.63.0, 54 MB). Patches in
  `third_party/rules_go_orchestrion` run Orchestrion in the stdlib, synthetic
  test main and link actions.
- Orchestrion is built from source. In the rule's fixture, a cold build took
  249 s, 153 s of them in the Orchestrion bootstrap
  (`docs/go_orchestrion_maintainer_state.md`).
- `orchestrion_mode = "test_optimization"`, the mode used in pilots, supports
  standard `testing` only, without Testify. The default `general` mode keeps
  Orchestrion's generic behavior. Both need pin files (`go.mod`,
  `orchestrion.tool.go`, `orchestrion.yml`).
- A repository rule syncs settings, known tests and test management into a
  manifest with `cache/http` JSON files. Tests get no network and no
  credentials, and they write payload files. A doctor validates the files and an
  uploader sends them with the API key. This data path stays.

## Decisions

The user settled these on 2026-10-29.

| Decision | Detail |
| --- | --- |
| No Orchestrion | No legacy path and no `general` mode; Go tests use `ddtest instrument` only. |
| One release | The ddtest CLI is the only artifact. There is no Bazel Central Registry module and no patch file in releases. |
| One version | The SDK edits, the compiler wrapper and Mini come from the same ddtest binary, so they never drift. |
| Inert hooks | A binary built with the instrumented SDK that does not link Mini links and behaves as if nothing were instrumented. |
| Full feature set | Testify, goleak, SDK span copies and the CI guards work in Bazel. There are no per-version patches and no Bazel-only variable to activate Mini. |
| Bazel CI | ddtest's CI runs a real Bazel build end to end. |
| Plan first | No implementation starts before this plan is agreed. |

## Design

### The instrumented SDK

Bazel has no `-toolexec`, but rules_go always runs the compiler from the SDK:

- **Packages.** `goTool("compile")` resolves
  `$SDK/pkg/tool/<goos_goarch>/compile` (`go/tools/builders/env.go`).
  `compileGo` passes `-p <importpath> -importcfg <file> -pack` and the sources
  after `--` (`go/tools/builders/compilepkg.go`).
- **Standard library.** The stdlib action runs
  `go install -toolexec <builder> filterbuildid` (`go/tools/builders/stdlib.go`),
  which reaches the same compiler.
- **Toolchain inputs.** The SDK's tool files are `pkg/tool/**`
  (`go/private/BUILD.sdk.bazel`).

The instrumented SDK is therefore the official SDK with ddtest as `compile`
and the real compiler next to it. ddtest decides from `-p` alone:

| Package | Edit |
| --- | --- |
| `testing`, `os/signal` | Test hooks, exit, timeout and signal hooks, as in the `-overlay` of `ddtest instrument` |
| `github.com/stretchr/testify/suite` | [Suite registration](testify.md) |
| `go.uber.org/goleak` | [Leak check entry](delivery.md#automatic-goleak-integration) |
| dd-trace-go v2 `internal/config`, `internal/civisibility/envconfig`, `ddtrace/tracer` | CI guards and span copies |
| Mini's packages, with `-race` | `norace` annotations |
| Anything else | Exec of the real compiler, with no file I/O |

The edits reuse the transforms and validators of the `-toolexec` path; the
SDK guards already work from the arguments alone (`prepareSDKCICompile` in
`internal/runner/sdk_ci.go`). Library edits only add `//go:linkname`
declarations and standard-library imports. When an added standard import is
missing from the package's import configuration, the wrapper adds it next to
the existing standard entries; any other added import is an error. Support
follows `ddtest instrument`: the same window in
`scripts/supported_versions.json`, the same nightly check, and the same
warning when a library is not supported.

### Registration

rules_test_optimization's module extension:

1. Downloads the official Go SDK, checked against the go.dev checksum, and
   the ddtest release asset (`ddtest-<os>-<arch>`), checked against
   `SHA256SUMS`.
2. Runs `ddtest instrument bazel <dir>`.
3. Wraps the result with rules_go's `go_wrap_sdk(register_toolchains = False)`.
4. Declares its own toolchains, gated on
   `@rules_go//go/toolchain:sdk_name == "ddto"`. They never match the default
   empty value.

rules_test_optimization's `MODULE.bazel` registers those toolchains. The
customer's `MODULE.bazel` has no SDK lines. rules_go's own toolchain
declarations for a wrapped SDK also match the empty `sdk_name`
(`match_all_sdks` in `go/private/go_toolchain.bzl`), so they would make the
instrumented SDK the default for every target; that is why registration goes
through toolchains of our own.

| rules_go API | Status |
| --- | --- |
| `go_wrap_sdk` in `go/deps.bzl` | Public |
| `@rules_go//go:toolchain` toolchain type | Public |
| `//go/toolchain:sdk_name` flag | Documented, used by rules_go internally |
| `go_<platform>-impl` target names | Private |
| SDK `BUILD` template loading `@io_bazel_rules_go//go/private/...` | Private; requires `repo_name = "io_bazel_rules_go"` |
| The builder's compiler path and stdlib `-toolexec` | Internal behavior |

All of them exist in rules_go 0.60.0, 0.61.1, 0.62.0, 0.63.0 and 0.65.0.

### The test rule

The Go test rule:

- Transitions `sdk_name` to `"ddto"`.
- Adds Mini's `testopt` package and the registration file
  `zz_dd_ci_visibility_test.go` with the content `ddtest instrument` writes.
  The file uses the external test package when a source declares it, and the
  internal one otherwise.
- Sets `DD_CIVISIBILITY_ENABLED=true`,
  `DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true` and
  `DD_TEST_OPTIMIZATION_MANIFEST_FILE` as an `rlocationpath`. It runs in agent
  mode without an API key; the uploader keeps the key.
- Keeps the user's `data` outside the transition, so binaries the test runs
  build with the default SDK.

`dd_topt_go_test` wraps it and keeps the rest of its current work: sync,
`importpath` inference, `stage_sources` and target metadata.

### `ddtest instrument bazel <dir>`

The command writes into `<dir>`:

- The instrumented SDK, from the official SDK given with `--goroot`.
- Mini's packages with `BUILD.bazel` files, from the runtime that the binary
  embeds.
- The toolchain declarations and, if ddtest owns it, the test rule.
- A `VERSION` file.

Today `ddtest instrument bazel test //...` runs `bazel test //...` unchanged.
The subcommand therefore accepts only its directory and flags, and it fails
with a clear message when it receives a Bazel command such as `test`,
`build`, `run` or `coverage`.

## Spike results

Both spikes ran on Linux amd64 with Bazel 8.8.2 and rules_go 0.65.0 from the
Bazel Central Registry, with no fork. Their code stayed outside the
repositories.

- **Spike 1:** `testing` patched statically in a Go 1.25.14 SDK.
- **Spike 2:** the compiler wrapper in a Go 1.26.7 SDK, with Testify v1.10.0,
  goleak v1.3.0 and dd-trace-go v2.11.2. dd-trace-go v2.11.2 requires Go 1.26.

| Check | Result |
| --- | --- |
| Standard tests: pass, fail, skip, subtests, parallel tests, `TestMain` | Expected events and statuses |
| Testify suite | Methods reported as tests of `suite_test.go/ExampleSuite` |
| goleak | The real leak is reported; Mini's goroutines are not |
| SDK span copies | Spans created from `t.Context()` join the test's trace with `_dd.origin=ciapp-test`. Spans without a test context are not copied, as outside Bazel |
| CI guards | The application's tracer stays in APM mode. Without the guards, dd-trace-go v2.11.2 writes its own tests file |
| Parity with `ddtest instrument` outside Bazel | Same events, names, suites, statuses, Testify metadata and span parentage |
| Payload contract | rules_test_optimization's schema check and the doctor's output check pass |
| Network | None: the sandbox had no network, and `strace` saw no network calls |
| `shard_count = 2` | Each shard writes its own session with the same name |
| `--@rules_go//go/config:race` | Works, with `norace` on Mini |
| Plain `go_binary` with dd-trace-go and Testify | Built with the default SDK, with no ddto symbols |
| Reproducibility | Three clean builds produce the same test binary, with no absolute paths |
| Registration | From a module other than the root; the root `MODULE.bazel` has no SDK lines |

The parity differences come from Bazel itself:

- Source paths are relative, because of `-trimpath`.
- The end line of a test is missing (see the open problems below).
- The binary name appears in the session name, `test.command` and the span
  copies' process tags.

### Measurements

These come from spike 2's dd-trace-go test: 315 compile actions, built cold
with no disk cache, three runs per configuration.

| Measure | Instrumented | Plain |
| --- | --- | --- |
| Wall time | 27.5–28.2 s | 26.8–27.1 s |
| Sum of compile actions | 40.5–43.5 s | 38.5–41.2 s |
| Stdlib action, median | 5.79 s | 5.54 s |
| `compile -V=full` | 4.70 ms | 2.52 ms |

The wrapper costs about 8 ms per compile action, and the whole build about
4% more. The wrapper was an 11 MB binary that links all of ddto's runner.
Building the plain configuration and then the instrumented one reran all 315
compile actions, the stdlib and the builder: the two configurations share no
cache entries.

## Open problems

1. **The exec configuration.** `sdk_name` reaches tools built in the exec
   configuration. A tool or a transitioned `data` binary that imports
   `testing` without Mini failed to link. Inert hooks fix this: `testing` and
   the libraries define the hook variables, Mini sets them at initialization,
   and unset hooks keep the original behavior. `ParallelStopHook` in
   `internal/instrument/hooks.go` already uses a variable instead of a
   bodyless function.
2. **Telemetry file names.** In payload-file mode, dd-trace-go v2.11.2 also
   writes its own telemetry to `payloads/telemetry`, with the same file name
   pattern as Mini. In spike 2 both wrote
   `telemetry-00000000000000000001-25.json`, and one replaced the other. A
   new CI guard must stop the application's dd-trace-go from writing payload
   files when Mini is linked.
3. **The Testify version.** ddto requires Testify v1.4.0 or later. A compile
   action does not know module versions, and the code cannot tell v1.3.0 from
   v1.4.0: both run each method with `r.Run(test.Name, test.F)`, and only the
   placement of `SetupSuite` differs. A version table from `go.mod` would be
   an input of every compile action, so any change to `go.mod` would rebuild
   everything.
4. **The test's end line.** Mini cannot open a relative `test.source.file`
   from the test's working directory, so `test.source.end` is missing. Mini
   should resolve it under the runfiles; `stage_sources` puts the files
   there.
5. **Warnings and the action cache.** Bazel shows an action's output only when
   the action runs, so a support warning disappears after a cache hit.
6. **Platforms.** Only linux_amd64 ran. Other target and exec platforms,
   remote execution and Windows are untested.
7. **API key wording.** [Delivery and offline output](mini-runtime.md#delivery-and-offline-output)
   says the settings-cache reader needs a nonempty API key. Both spikes read
   the cache in agent mode without one; the requirement applies to agentless
   mode, and the page needs to say so.

## Phases

| Phase | Repository | Work | Done when |
| --- | --- | --- | --- |
| 1 | ddtest | Prerequisites in ddto | Existing CI and the full compatibility window pass |
| 2 | ddtest | Compiler mode and `ddtest instrument bazel` | The Bazel job passes and matches `ddtest instrument` outside Bazel |
| 3 | ddtest | Release | The release gate runs the Bazel job |
| 4 | rules_test_optimization | Extension, toolchains, test rule, removal of Orchestrion | Its CI, doctor and uploader dry run pass |
| 5 | Both | Pilot and measurements | A real repository reports through the new path |

### Phase 1: prerequisites in ddto

- Inert hooks for `testing` and for the Testify, goleak and span copy hooks.
- The CI guard for dd-trace-go's payload files, added to the validator and
  the compatibility window.
- `//line` directives with stable names on every generated and hook file,
  including the SDK span copy, Testify and goleak hooks.
- Mini resolves relative source paths under the runfiles.
- The API key wording in `mini-runtime.md`.

Each change works outside Bazel too and gets unit tests checked by mutation.

### Phase 2: compiler mode and `ddtest instrument bazel`

- A compiler mode that needs no plan: it decides from `-p` and the arguments,
  keeps `argv[0]` as `compile` for the real compiler (cmd/go parses
  `compile -V=full`), and accepts sources after `--` or at the end.
- Import configuration additions for standard packages.
- `ddtest instrument bazel <dir>` as described above, rejecting Bazel
  commands.
- A Bazel job in ddtest's CI, on Linux and macOS, over rules_go 0.60.0 and
  the latest release and Go 1.25 to 1.27. Its fixture covers:
  - standard tests, Testify, goleak and dd-trace-go;
  - sharding and `-race`;
  - a plain binary that must stay unmodified;
  - an exec tool that imports `testing`;
  - reproducibility;
  - an event comparison with the same packages run outside Bazel.
- A user guide, `docs/bazel.md`, and the README's Bazel row.

### Phase 3: release

A ddtest release with the subcommand. The release gate runs the Bazel job
with the full compatibility window.

### Phase 4: rules_test_optimization

- The module extension and toolchains described above, for the platforms
  chosen for the first release.
- `dd_topt_go_test` on top of the new test rule.
- Removal of `third_party/rgo`, `third_party/rules_go_orchestrion`, the
  Orchestrion modules, the pin files and `orchestrion_mode`.
- Documentation, onboarding guides and agent skills.
- Its CI, the doctor and the uploader dry run with `--validate-enrichment`.

### Phase 5: pilot and measurements

Run the pilot repository that rules_test_optimization's plan names, and
compare with today's Orchestrion path:

- cold and warm build time;
- the cost of the split cache;
- the wrapper's share of compile time;
- payload enrichment.

The results decide whether the wrapper needs the work in open question 2.

## Open questions

1. **Who owns the test rule.** If ddtest generates the Starlark, the rule
   always matches Mini and the registration file of the same release. If
   rules_test_optimization owns it, the rule is easier to read and review.
   Recommendation: ddtest generates it, and `dd_topt_go_test` wraps it.
2. **The wrapper binary.** The ddtest executable can act as `compile`, or the
   extension can build a smaller wrapper from the embedded sources with the
   downloaded SDK. Recommendation: start with the executable, and measure its
   startup in phase 5.
3. **The Testify gate.** Decide by code shape in Bazel only, or everywhere,
   which would accept v1.3.0.
4. **The old attributes.** Remove `orchestrion_mode` and the pin file
   attributes, or accept and ignore them with a warning for one release so
   existing `BUILD` files keep loading.
5. **The Go version.** Read it from the root module's `go.mod` (`toolchain`,
   then `go`), or take it from an extension attribute.
6. **Platforms and rules_go versions** for the first release.
7. **Warnings after cache hits.** Document them, or have Mini report at run
   time which libraries the build left uninstrumented.

## Out of scope

Bazel's offline mode does not do intelligent test skipping or Git uploads
today, and this plan keeps it that way. Per-test coverage in Bazel needs a
separate check of rules_go's coverage instrumentation.
