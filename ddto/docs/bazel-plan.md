# Bazel plan

This plan replaces Orchestrion in
[DataDog/rules_test_optimization](https://github.com/DataDog/rules_test_optimization)
with `ddtest instrument`. It was written on 2026-10-29, after two
throwaway spikes on that date, and updated the same day with the user's
review. Nothing in it is implemented yet. Each phase gets its own pull
request. A phase starts only after the plan is agreed and the phases it
depends on are merged.

## Goal

- `ddtest instrument bazel <dir>` generates what Bazel needs from ddtest to
  instrument Go tests: the instrumented SDKs and Mini. It comes from the same
  ddtest release that customers already install; there is no other
  artifact. rules_test_optimization owns the Starlark that uses them.
- rules_test_optimization uses upstream rules_go. It drops Orchestrion, the
  vendored rules_go trees and the pin files.
- Go tests in Bazel get what `ddtest instrument` gives outside Bazel:
  - standard `testing`;
  - Testify suites and goleak;
  - [SDK span copies](sdk-span-mirror.md);
  - the CI guards that keep the application's dd-trace-go out of CI
    Visibility mode;
  - automatic retries, early flake detection and test management.
- **Settings.** The last three follow the backend settings that the rule's
  sync writes into the manifest. In manifest mode Mini reads
  `cache/http/settings.json`, `known_tests.json` and `test_management.json`
  (`internal/thirdparty/dd-trace-go/civisibility/utils/net`). The spikes did
  not exercise retries, so the Bazel suites cover them.

## rules_test_optimization today

Read at `eceed1a`, 2026-10-29:

- **The Orchestrion patches.** The consumer's rules_go carries the patches in
  `third_party/rules_go_orchestrion`. They come either from one of the four
  vendored rules_go trees (`third_party/rgo`, 0.60.0 to 0.63.0, 54 MB) or from
  the consumer's own patched rules_go. They run Orchestrion in the stdlib,
  synthetic test main and link actions.
- **The transition.** `dd_topt_go_test` turns those patches on for a hidden
  `go_test`, through `@rules_go//go/private/orchestrion:enabled` and `:mode`
  (`modules/go/topt_go_orchestrion.bzl`).
- Orchestrion is built from source. In the rule's fixture, a cold build took
  249 s, 153 s of them in the Orchestrion bootstrap
  (`docs/go_orchestrion_maintainer_state.md`).
- `orchestrion_mode = "test_optimization"`, the mode used in pilots, supports
  standard `testing` only, without Testify. The default `general` mode keeps
  Orchestrion's generic behavior. Both need pin files: `go.mod` and `go.sum`
  in `test_optimization` mode, plus `orchestrion.tool.go` in `general` mode.
- A repository rule syncs settings, known tests and test management into a
  manifest with `cache/http` JSON files. Tests get no network and no
  credentials, and they write payload files. A doctor validates the files and
  an uploader sends them with the API key.
- **This data path stays and must behave the same.** The doctor and the
  uploader must accept the new files as they accept today's, with the same
  enrichment.
- **Consumer validation.** It also runs in the sibling repository
  `rules_test_optimization_tests` (`AGENTS.md`).
- **Windows.** The rule's CI has a Windows Go lane,
  `windows-go-bootstrap-shard`, over WORKSPACE and Bzlmod. It requires test
  payload files (`.github/workflows/ci.yml`,
  `tools/tests/integration/run_workspace_go_integration.sh`).

## Decisions

The user settled these on 2026-10-29.

| Decision | Detail |
| --- | --- |
| No Orchestrion | No legacy path and no `general` mode; Go tests use `ddtest instrument` only. |
| One release | The ddtest CLI is the only artifact. There is no Bazel Central Registry module and no patch file in releases. |
| One binary | The ddtest executable is the compiler wrapper. There is no second, smaller wrapper. |
| One version | The SDK edits, the compiler wrapper and Mini come from the same ddtest binary, so they never drift. |
| Inert hooks | A binary built with the instrumented SDK that does not link Mini links and behaves as if nothing were instrumented. The same hooks serve `ddtest instrument` outside Bazel. |
| Full feature set | Testify, goleak, SDK span copies and the CI guards work in Bazel. There are no per-version patches and no Bazel-only variable to activate Mini. |
| Code checks, not versions | Library gates check the code that each edit needs, in and outside Bazel. Testify v1.3.0 is instrumented when its code fits. |
| No API key in tests | Tests run in agent mode, with no network and no key; the uploader keeps the key. |
| Where Bazel is tested | ddtest's CI runs a basic Bazel test. The full Bazel suites live in rules_test_optimization and rules_test_optimization_tests. |
| The Starlark | rules_test_optimization owns it: the macro, the test rule, the transition, the registration file template and the toolchain declarations. ddtest generates only the SDKs and Mini, behind a contract version that the extension checks when it fetches them. `BUILD` files load the rule in every build, so ordinary builds never need ddtest. Settled on 2026-10-30; [Validating the Starlark](#validating-the-starlark) lists the checks. |
| Platforms | Every platform with a ddtest release asset: Linux, macOS and Windows on amd64 and arm64, as exec and target platforms, plus remote execution. Every rules_go minor version from 0.60 to the latest, at its latest patch. |
| WORKSPACE and Bzlmod | Both stay supported. |
| One Mini per test | The test binary links only the Mini of the ddtest release, as outside Bazel. [Mini from `go_deps`](#mini-from-go_deps) covers tests that use Mini's Go API. |
| Dogfooding | dd-source. |
| Releases | Neither ddtest nor the rule is released before end-to-end tests pass in the rule's repository, in its test repository and in internal dogfooding. |
| A major version of the rule | `orchestrion_mode` and the pin file attributes are removed. |
| Same Bazel behavior as today | `stage_sources` stays opt-in and documented. Support warnings appear only when an action runs, as with Orchestrion, and are documented. |
| Coverage | Not in the first version. Reporting coverage with Mini in Bazel, not test skipping, is investigated afterwards. |
| Plan first | No implementation starts before this plan is agreed. |

## Design

### The instrumented SDK

Bazel has no `-toolexec`, but rules_go always runs the compiler from the SDK:

- **Packages.** `goTool("compile")` resolves
  `$SDK/pkg/tool/<goos_goarch>/compile` (`go/tools/builders/env.go`).
  `compileGo` passes `-p <importmap> -importcfg <file> -pack` and the sources
  after `--` (`go/tools/builders/compilepkg.go`). The `importmap` is the
  import path unless the library sets another one.
- **Standard library.** The stdlib action runs
  `go install -toolexec <builder> filterbuildid` (`go/tools/builders/stdlib.go`),
  which reaches the same compiler.
- **Toolchain inputs.** The SDK's tool files are `pkg/tool/**`
  (`go/private/BUILD.sdk.bazel`).

The instrumented SDK is therefore the official SDK with the ddtest executable
as `compile` and the real compiler next to it. ddtest decides from `-p`.
Gazelle gives vendored packages an `importmap` such as
`<prefix>/vendor/<import path>`, or `vendor/<import path>` when the prefix is
empty, so the wrapper uses the part after the last `vendor` path element. A
library with any other `importmap` is compiled unmodified.

| Package | Edit |
| --- | --- |
| `testing`, `os/signal` | Test hooks, exit, timeout and signal hooks, as in the `-overlay` of `ddtest instrument` |
| `github.com/stretchr/testify/suite` | [Suite registration](testify.md) |
| `go.uber.org/goleak` | [Leak check entry](delivery.md#automatic-goleak-integration) |
| dd-trace-go v2 `internal/config`, `internal/civisibility/envconfig`, `ddtrace/tracer` and the package that writes payload files | CI guards, the telemetry guard and span copies |
| Mini's packages, with `-race` | `norace` annotations |
| Anything else | Exec of the real compiler, with no file I/O |

The edits reuse the transforms and validators of the `-toolexec` path; the
SDK guards already work from the arguments alone (`prepareSDKCICompile` in
`internal/runner/sdk_ci.go`). Library edits only add `//go:linkname`
declarations and standard-library imports. When an added standard import is
missing from the package's import configuration, the wrapper adds it next to
the existing standard entries; any other added import is an error.

A compile action does not know module versions, so every gate checks the APIs
that its edit uses, in the package's own files (see open problem 3). Otherwise
support follows `ddtest instrument`: the same window in
`scripts/supported_versions.json`, the same nightly check, and the same
warning when a library is not supported.

### Linking Mini

`ddtest instrument bazel <dir>` writes Mini's packages, from the runtime that
the binary embeds, with `BUILD.bazel` files that ddtest generates itself;
Gazelle does not run at fetch time. The test rule adds Mini's `testopt`
library and the registration file to the `go_test`.

Mini is then an ordinary dependency of the test. It compiles in the
instrumented configuration and links into the test binary, where it sets the
hooks that `testing` and the libraries declare. The binary that compiled the
hooks also wrote Mini's sources, so both sides of every `//go:linkname`
come from the same release.

### Mini from `go_deps`

A test binary links one Mini, the one from the ddtest release. Outside Bazel
the CLI's Mini also replaces the version that a module requires
([runtime provisioning](mini-runtime.md#runtime-provisioning)). A workspace
whose tests use Mini's Go API
([explicit clients](mini-runtime.md#explicit-clients-and-manual-instrumentation))
also gets Mini from `go_deps`, with the same import paths. Such a test links two
copies of Mini's packages, and rules_go 0.64 reports no error: its
`package conflict error` in `go/tools/builders/importcfg.go` does not fire, and
the order of its archives decides which copy of each package the binary gets.
Phase 2's basic Bazel test found this.

- **Bzlmod.** The rule's extension exports Mini under a stable repository
  name. Such a workspace makes that repository visible to its root module and
  overrides `go_deps`' copy with it, in its root `MODULE.bazel`:

  ```starlark
  use_repo(<the rule's Go extension>, "<Mini repository>")
  override_repo(go_deps, com_github_datadog_ddtest_ddto = "<Mini repository>")
  ```

  `override_repo` only applies in the root module, and its value must be a
  repository visible there. The generated `BUILD.bazel` files use the target
  names that Gazelle gives `go_deps` repositories, a target named after the
  package plus a `go_default_library` alias, so the API's imports resolve to
  the generated Mini unchanged.
- **WORKSPACE.** The rule's repository macro declares the same repository
  name. Phase 3 settles which declaration wins and documents the order.
- **The check.** rules_test_optimization's rule fails at analysis when a
  package of `github.com/DataDog/ddtest/ddto` reaches an instrumented test
  from a repository other than the Mini repository, and its message shows
  the lines above.
- **Plain builds.** After the override, builds with the default SDK that use
  the API also link the generated Mini, so Mini must link without the
  instrumented `testing`.

### Registration

rules_test_optimization's module extension creates three kinds of
repository, so that a build fetches only what it uses:

- **Registration.** One `toolchain()` per supported pair of exec and target
  platforms, as rules_go declares its own (`go/private/go_toolchain.bzl`):
  - each carries both platforms' constraints and is gated on
    `@rules_go//go/toolchain:sdk_name == "ddto"`, so none matches the default
    empty value;
  - the extension writes them itself, with no download. Bazel loads every
    registered toolchain declaration, and these load nothing else;
  - each points at the implementation for its target platform in the exec
    platform's SDK repository, which Bazel fetches only when it selects that
    toolchain.
- **SDK, one per exec platform.** It downloads, from one ddtest release, the
  host's ddtest executable and that platform's one (`ddtest-<os>-<arch>`,
  checked against `SHA256SUMS`). It takes the workspace's SDK for that
  platform or the official one. It then runs the host's
  `ddtest instrument bazel`, which makes the platform's executable the SDK's
  `compile`, and wraps the result with rules_go's
  `go_wrap_sdk(register_toolchains = False)`.
- **Mini.** Mini's packages, written by the host's ddtest. Bazel fetches it
  only for targets that depend on Mini.

Target platforms reuse those SDKs by cross-compiling, as rules_go does with
its own: an SDK repository holds one implementation per target platform. Only
exec platforms need an instrumented SDK.

rules_test_optimization's `MODULE.bazel` registers those toolchains, so the
customer registers no extra SDK. rules_go's own toolchain
declarations for a wrapped SDK also match the empty `sdk_name`
(`match_all_sdks` in `go/private/go_toolchain.bzl`), so they would make the
instrumented SDK the default for every target; that is why registration goes
through toolchains of our own.

The new major version keeps WORKSPACE mode: a repository macro does the same
and registers the toolchains.

**The base SDK.** The instrumented SDK must match the SDK the workspace
already uses: its version, download mirrors, checksums, patches and
`GOEXPERIMENT` values (`go_wrap_sdk` accepts `experiments`).

- **Named SDKs.** rules_go keeps one SDK repository per exec platform.
  A workspace that registers its SDKs with `go_sdk.download(name = ...)`
  can `use_repo` them. It then gives the extension a map from each exec
  platform to the `ROOT` label of that platform's SDK. The generator copies
  the matching SDK, and the extension declares the same experiments.
- **Default.** Without one, the extension downloads the official SDK. Its
  version follows rules_go's `go_sdk.from_file`: the root module's `go.mod`
  `toolchain` directive, then its `go` directive (`go/private/go_mod.bzl`).
- **Laziness.** Builds without the instrumented configuration fetch none of
  these repositories and change no ordinary action. There is one exception:
  in a workspace that uses Mini's Go API with the override above, targets
  that depend on Mini fetch the Mini repository, which needs only the
  host's ddtest. The Starlark that `BUILD` files load comes from
  rules_test_optimization and needs no ddtest. The full suites check all of
  this.

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

rules_test_optimization owns the Go test rule. It:

- Transitions `sdk_name` to `"ddto"`.
- Adds Mini's `testopt` library and the registration file
  `zz_dd_ci_visibility_test.go` with the content `ddtest instrument` writes.
  The file uses the external test package when a source declares it, and the
  internal one otherwise.
- Sets `DD_CIVISIBILITY_ENABLED=true`,
  `DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true` and
  `DD_TEST_OPTIMIZATION_MANIFEST_FILE` as an `rlocationpath`.
- Keeps the user's `data` outside the transition, so binaries the test runs
  build with the default SDK.

`dd_topt_go_test` wraps it and keeps the rest of its current work: sync,
`importpath` inference, `stage_sources` and target metadata.

### `ddtest instrument bazel <dir>`

The command writes one of these into `<dir>`, which must be empty or
missing, with a `DDTEST_INSTRUMENT` file. The SDK's own `VERSION` file is
Go's, which rules_go reads.

- `--goroot=<SDK> --compiler=<executable> [--panic-hook]`: an instrumented
  SDK, from an SDK and the ddtest executable for its platform;
- `--mini [--rules-go=<name>]`: Mini's packages with their `BUILD.bazel`
  files, which load rules_go as `@<name>` (`io_bazel_rules_go` by default).

Today `ddtest instrument bazel test //...` runs `bazel test //...` unchanged.
The subcommand therefore accepts only its directory and flags, and it fails
with a clear message when it receives a Bazel command such as `test`,
`build`, `run` or `coverage`.

### Validating the Starlark

The Starlark lives in rules_test_optimization, so its CI validates it.
`ddtest instrument bazel` writes only data: the SDKs, Mini's packages with
their `BUILD.bazel` files, and a `DDTEST_INSTRUMENT` file with the release
version, the contract version and the `sdk_name` value.

- **In ddtest:**
  - a Go test compares the command's output with golden files;
  - buildifier checks the generated `BUILD.bazel` files;
  - the basic Bazel test uses a minimal test-only rule, in Bzlmod and
    WORKSPACE modes.
- **In rules_test_optimization:**
  - buildifier on its Starlark;
  - analysis tests, with bazel-skylib's `analysistest`:
    - the transition sets `sdk_name`;
    - the test gets Mini and the registration file;
    - the environment is set;
    - `data` stays outside the transition;
    - the toolchains never match an empty `sdk_name`, and loading them
      fetches no SDK repository.
- **Cross validation.** rules_test_optimization and
  rules_test_optimization_tests run their full suites on the pinned ddtest
  and on the newest release candidate, over every supported Bazel and
  rules_go version, mode and platform.
- **Contract version.** The extension checks the generated contract version
  at fetch time, so a ddtest outside the supported range fails with a clear
  message.

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
4% more. The spike's wrapper was an 11 MB binary that links all of ddto's
runner; the ddtest executable is larger, and phase 4 measures it. Building
the plain configuration and then the instrumented one reran all 315 compile
actions, the stdlib and the builder: the two configurations share no cache
entries.

## Open problems

1. **The exec configuration.** `sdk_name` reaches tools built in the exec
   configuration. A tool or a transitioned `data` binary that imports
   `testing` without Mini failed to link.
   - **Inert hooks fix this.** `testing` and the libraries define the hook
     variables, Mini sets them at initialization, and unset hooks keep the
     original behavior. `ParallelStopHook` in `internal/instrument/hooks.go`
     already uses a variable instead of a bodyless function.
   - **The CI guards too.** The guards in `internal/instrument/sdk_ci.go` turn
     CI Visibility off unconditionally. They must also apply only when Mini is
     linked, which they have to learn before dd-trace-go reads its
     configuration.
   - **Outside Bazel.** The same hooks replace the bodyless functions that
     `ddtest instrument` uses there.
2. **Telemetry file names.** In payload-file mode, dd-trace-go v2.11.2 also
   writes its own telemetry to `payloads/telemetry`, with the same file name
   pattern as Mini. In spike 2 both wrote
   `telemetry-00000000000000000001-25.json`, and one replaced the other. A
   telemetry guard must stop the application's dd-trace-go from writing
   payload files when Mini is linked, in and outside Bazel.
3. **Code checks instead of versions.** A compile action does not know module
   versions, and a version table from `go.mod` would be an input of every
   compile action, so any change to `go.mod` would rebuild everything. Each
   gate therefore checks the code, in and outside Bazel:
   - **goleak:** v1.2.1 has the same `Find` as v1.3.0 but no
     `IgnoreAnyFunction`, which the edit calls. Without a check, the build
     fails. The gate checks that the package declares `IgnoreAnyFunction`, as
     spike 2 did.
   - **Testify:** ddto requires v1.4.0 or later today, but v1.3.0 runs each
     method with `r.Run(test.Name, test.F)` as v1.4.0 does; only the placement
     of `SetupSuite` differs. The gate checks the code that registration
     needs, so v1.3.0 is instrumented. [Testify support](testify.md) changes
     with it.
   - **The Go toolchain:** the instrumented SDK has a known version, and the
     exit hook keeps its own shape check.
4. **The test's end line.** Mini cannot open a relative `test.source.file`
   from the test's working directory, so `test.source.end` is missing, as it
   is with Orchestrion. Mini resolves the path under the runfiles, where the
   opt-in `stage_sources` puts the files.
5. **Platforms.** Only linux_amd64 ran. The other five platforms, remote
   execution, and a host that differs from the exec platform are untested.
6. **API key wording.** [Delivery and offline output](mini-runtime.md#delivery-and-offline-output)
   says the settings-cache reader needs a nonempty API key. Both spikes read
   the cache in agent mode without one; the requirement applies to agentless
   mode, and the page needs to say so.

7. **Other transitions.** A workspace can already transition Go SDKs, for
   example with `go_cross_binary(sdk_version = ...)`, transition platforms,
   or patch rules_go to restore the original SDK across non-Go edges.
   Keeping the test's own `data` outside the transition is not enough.
   Nested data, exec tools and `nogo` analyzers must keep the SDK they would
   have had, or at least still build and run. The full suites test those
   combinations.
8. **Patched rules_go.** A workspace may carry its own rules_go patches for
   linking, build metadata or reproducibility. The rule needs no fork of its
   own, but the internals it relies on (the compiler path, the stdlib
   `-toolexec` and the SDK `BUILD` template) must survive those patches.
9. **The metadata contract.** Today the rule's metadata producer resolves an
   Orchestrion tool label and emits `bazel.go.orchestrion.enabled` and
   `bazel.go.orchestrion.mode` (`modules/go/topt_go_infer.bzl`), and
   consumer validators check those values. The major version defines
   replacement metadata for the ddtest instrumentation. It keeps
   `bazel_target_metadata.json`, the public target identity, the service
   and repository identity, the import path and the payload-selection
   metadata.
10. **dd-trace-go v1.** dd-trace-go v1.74 is built on v2: its
    `ddtrace/tracer` imports the v2 tracer. v2's guards therefore cover it
    when the selected v2 is in the window. Older v1 releases are not
    covered.

## Phases

| Phase | Repository | Work | Done when |
| --- | --- | --- | --- |
| 1 | ddtest | Prerequisites in ddto, without Bazel | Existing CI and the full compatibility window pass |
| 2 | ddtest | Compiler mode, `ddtest instrument bazel`, a basic Bazel test and a release candidate | The basic Bazel test passes |
| 3 | rules_test_optimization and its test repository | The major version on the release candidate | Both repositories' full Bazel suites pass |
| 4 | dd-source | Internal dogfooding and measurements | It reports through the new path |
| 5 | ddtest, then the rule | Releases | Built from the release candidate's commit |

### Phase 1: prerequisites in ddto

Each change also applies to `ddtest instrument` outside Bazel. Each one is
tested in ddtest without Bazel, with unit tests checked by mutation.

- **Inert hooks** for `testing` and for the Testify, goleak and span copy
  hooks.
- **CI guards that apply only when Mini is linked.** A fixture without Mini
  that enables CI Visibility in dd-trace-go checks that it still works.
- **The telemetry guard** for dd-trace-go's payload files, added to the
  validator and the compatibility window.
- **Code checks instead of version gates** for Testify and goleak, with
  fixtures for goleak v1.2.1 and Testify v1.3.0.
- **`//line` directives** with stable names on every generated and hook file,
  including the SDK span copy, Testify and goleak hooks.
- **Relative source paths:** Mini resolves them under the runfiles.
- **The API key wording** in `mini-runtime.md`.

**Status.** Implemented in this pull request, after the plan:

- **Inert hooks:** Mini defines statically initialized variables with its
  functions as values (`internal/instrument/mini_hooks.go`). Preparation
  checks that a runtime that a client selects declares them with their exact
  types.
- **Guards:** the CI guards and the telemetry guard, on
  `internal/bazel.CurrentMode`, read `minitracer.sdkGuardMiniLinked`.
- **Not converted:** per-test coverage, context file coverage and
  `--panic-hook` keep their calls into Mini, because Bazel's first version
  compiles none of them.

### Phase 2: compiler mode and `ddtest instrument bazel`

- **A compiler mode of the ddtest executable that needs no plan.** It
  decides from `-p` and the arguments. It keeps `argv[0]` as `compile` for
  the real compiler, because cmd/go parses `compile -V=full`. It accepts
  sources after `--` or at the end.
- **Import configuration additions** for standard packages.
- **Vendored packages** identified after the last `vendor` element of `-p`,
  with fixtures for a Gazelle prefix and an empty one.
- **Mini from `go_deps`** as described above. Fixtures cover a test that uses
  the API, with the complete recipe in a root module that did not import the
  Mini repository before, and a plain build that uses it. The case without
  the override belongs to phase 3, with the rule's check.
- **`ddtest instrument bazel <dir>`** as described above, rejecting Bazel
  commands.
- **ddtest's checks** in [Validating the Starlark](#validating-the-starlark):
  golden files, buildifier on the generated files and a minimal test-only
  rule.
- **A basic Bazel test in ddtest's CI,** on Linux and macOS, with Go 1.25 to
  1.27, in Bzlmod and WORKSPACE modes. The spikes covered only Go 1.25 and
  1.26. Its fixture covers:
  - standard tests, Testify, goleak, dd-trace-go v2 and v1.74;
  - a plain binary that must stay unmodified;
  - an exec tool that imports `testing`;
  - an event comparison with the same packages run through
    `ddtest instrument` outside Bazel.
- **A user guide,** `docs/bazel.md`, and the README's Bazel row, including
  `stage_sources` and the warnings that cached actions do not repeat.
- **A ddtest release candidate** with the subcommand, published by the
  existing prerelease workflow.

**Status.** Implemented in this pull request, except the release candidate:

- **Compiler mode:** `internal/runner/bazel_compile.go`. An edited file keeps
  its original's absolute name through a `//line` directive, so test binaries
  name no temporary file. A missing or unreadable SDK configuration stops the
  runtime's compile instead of dropping the panic hook.
- **Generator:** `internal/bazelgen`. Mini's repository carries the files and
  dependencies of every supported platform, Go release from Mini's `go`
  directive to the one that built ddtest, and the race detector, because
  rules_go applies build constraints itself. The SDK copy follows symbolic
  links, as rules_go's local and wrapped SDKs use them.
- **Panic hook:** `--panic-hook` on the SDK. Its fatal-error copy acts only
  when Mini set the hook.
- **Basic Bazel test:** `scripts/bazel_basic.py` with the test-only rule and
  workspace in `testdata/bazel`, in the `bazel` job of the Go instrumentation
  workflow. Its fixture uses dd-trace-go v2.10.1, the oldest release in the
  window, because v2.11 requires Go 1.26, and rules_go 0.64.0, because
  0.65.0's `go.mod` raises `golang.org/x/mod` and `golang.org/x/sys` in
  `go_deps` to releases that need Go 1.26. Linux runs Go 1.25 to 1.27 in both
  modes, and macOS runs Go 1.27 in both modes. WORKSPACE mode writes
  `deps.bzl` with `gazelle update-repos` at test time, instead of a generated
  file in the repository. The `go_deps` fixture runs only in Bzlmod, since
  `override_repo` is Bzlmod's; phase 3 settles WORKSPACE. After the
  override, the API's labels are Mini's, so the test-only macro adds Mini
  only when the test's `deps` do not name it; rules_test_optimization's
  macro must do the same, because Bazel rejects a duplicated label. Without
  the override, the test linked two copies of Mini's packages with no error,
  so the check moved to the rule (see [Mini from `go_deps`](#mini-from-go_deps)).
- **Guide:** [Bazel](bazel.md).

### Phase 3: rules_test_optimization

The new major version, built on the release candidate:

- **The module extension and toolchains** described above, for every
  platform.
- **WORKSPACE support:** the same repository rule and toolchain registration in
  `tools/go/workspace_repositories.bzl`, with a WORKSPACE test, before the
  vendored trees that those helpers fetch are removed.
- **The Starlark:** the test rule, the transition, the registration file
  template, the toolchain declarations and `dd_topt_go_test` on top of them,
  with buildifier and the analysis tests in
  [Validating the Starlark](#validating-the-starlark).
- **Removals:** `third_party/rgo`, `third_party/rules_go_orchestrion`, the
  Orchestrion modules, the pin files and `orchestrion_mode`.
- **Documentation,** onboarding guides and agent skills.
- **The full Bazel suites** in rules_test_optimization and
  rules_test_optimization_tests. They cover:
  - retries, early flake detection and test management from the manifest;
  - sharding and `-race`;
  - reproducibility;
  - vendored packages and Mini from `go_deps`, including the analysis error
    without the override;
  - every supported Bazel and rules_go version, in Bzlmod and WORKSPACE
    modes;
  - every platform, remote execution, and a host that differs from the exec
    platform;
  - named SDKs with their own mirrors, patches and experiments, on a host
    that differs from the exec platform;
  - other SDK and platform transitions, nested data, exec tools and `nogo`;
  - builds without the instrumented configuration: nothing fetched, no
    metadata requests and unchanged action inputs, and a plain build that
    uses Mini's API fetching only the Mini repository;
  - the replacement metadata;
  - the doctor and the uploader dry run with `--validate-enrichment`. Checks
    read their reports, because a runner can return the tests' exit status
    even when post-processing fails;
  - a comparison of the payloads with today's Orchestrion path on the same
    targets, before that path is removed.

### Phase 4: internal dogfooding and measurements

Run dd-source on the release candidate and the new major version.

- **Its own migration.** dd-source migrates in step with the major version.
  - Its central test wrapper drops the removed attributes and the
    Orchestrion-only dependencies.
  - Its validator moves to the replacement metadata.
  - It keeps its public test labels, eligibility rules, tags and named
    configuration.
- **Measurements and acceptance.** Compare with today's Orchestrion path:
  - cold and warm builds;
  - a plain build followed by an instrumented one, including the stdlib and
    analyzer work, and with remote execution;
  - remote cache volume;
  - the ddtest executable's share of compile time;
  - payload enrichment.

  The split cache is accepted on these numbers, not before.

### Phase 5: releases

A ddtest release with the subcommand, then the rule's major version, pinned
to that release.

- **Same commit.** The release workflow runs only from `main`
  (`.github/workflows/release.yml`). It is dispatched while `main` is still at
  the commit of the release candidate that passed phases 3 and 4. Any later
  change to `main` needs a new candidate and phases 3 and 4 again.
- **Release gate.** It runs the basic Bazel test with the full compatibility
  window.

## Open questions

None. The dd-source rollout thread reviewed the plan on 2026-10-29: open
problems 7 to 10, the base SDK, the laziness checks and phase 4's consumer
migration and acceptance criteria come from that review. The user settled
the Starlark's owner on 2026-10-30.

## Out of scope

Bazel's offline mode does not do intelligent test skipping or Git uploads
today, and this plan keeps it that way. Coverage is not in the first
version. Reporting coverage with Mini in Bazel, not test skipping, is
investigated afterwards; it needs a check of rules_go's coverage
instrumentation.
