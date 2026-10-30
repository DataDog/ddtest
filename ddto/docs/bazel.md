# Bazel

Bazel has no `-toolexec`, so `ddtest instrument` reaches Go tests in Bazel
through the Go SDK instead. The SDK that rules_go compiles with gets the
ddtest executable as its compiler, and Mini's packages become a Bazel
repository that each test links. One ddtest release provides both: the
executable writes them with `ddtest instrument bazel`.

rules_test_optimization owns the Starlark: the test rule, the transition to
the instrumented SDK, the toolchains and the repositories that run
`ddtest instrument bazel` while Bazel fetches. Its major version that uses
this command is in development. This page covers what ddtest provides to it.

## `ddtest instrument bazel`

```sh
ddtest instrument bazel --goroot=<SDK> --compiler=<executable> [--panic-hook] <dir>
ddtest instrument bazel --mini [--rules-go=<name>] <dir>
```

- **The instrumented SDK.** `--goroot` names the Go SDK to instrument, such
  as the one the workspace already uses, and `--compiler` the ddtest
  executable for that SDK's platform. The command copies the SDK, with hard
  links where it can, and follows its symbolic links: rules_go's local and
  wrapped SDKs link their directories to another one. The real compiler
  becomes `pkg/tool/<goos_goarch>/compile.real`, and the ddtest executable
  `compile`. `--panic-hook` edits the runtime as
  [`--panic-hook`](../README.md) does outside Bazel.
- **Mini's repository.** `--mini` writes the Mini packages that tests import,
  from the sources that the release carries, with a `BUILD.bazel` file per
  package. The files load rules_go as `@<name>`, `io_bazel_rules_go` by
  default. Targets have Gazelle's names, the package's directory and a
  `go_default_library` alias, and their `srcs` and `deps` cover every
  supported platform and Go release, the race detector and the `purego` tag,
  because rules_go applies the build constraints itself.

Both write `<dir>`, which must be empty or missing, and a `DDTEST_INSTRUMENT`
file at its root, with one `key=value` per line:

- `version`: the runtime's SemVer version. A release's tag, such as `v1.14.0`,
  stays as it is; any other name gets `v0.0.0-` before it, so the pre-release
  `pre-release-fbbc65e`, which `ddtest --version` prints, writes
  `v0.0.0-pre-release-fbbc65e`.
- `contract`: the contract version that the rule checks, `1`.
- `sdk_name`: the value of `@io_bazel_rules_go//go/toolchain:sdk_name` that
  selects the SDK's toolchains, `ddto`.

The SDK also has `pkg/tool/<goos_goarch>/ddtest-instrument.json`, with the
same `version` and `contract` and the `panicHook` setting. It keeps Go's own
`VERSION` file, which rules_go reads.

Contract 1 covers:

- the SDK's layout: `compile`, `compile.real` and `ddtest-instrument.json` in
  `pkg/tool/<goos_goarch>`, and the original SDK's other files where it has
  them;
- the Mini repository's layout: a package per directory of Mini's import path,
  `github.com/DataDog/ddtest/ddto`, its target names, and the `testopt` target
  that the rule adds to each test;
- the registration that the rule adds to each test, the file that
  `ddtest instrument` writes as `zz_dd_ci_visibility_test.go`. It goes in the
  test's external package when one of its sources declares it, or when Mini
  imports the package under test, where the package's own name would make an
  import cycle; otherwise in the package's own name:

  ```go
  package <the test package>
  import __dd_ci_runtime "github.com/DataDog/ddtest/ddto/testopt"
  func init() { __dd_ci_runtime.RegisterTestPackage() }
  ```

A change to any of them takes a new contract version.

The command runs no Bazel command. `ddtest instrument bazel test //...`, which
used to run Bazel unchanged, now fails with a message that says so.

## The compiler

rules_go runs the SDK's `compile` for every package, and for the standard
library through `go install -toolexec`. The ddtest executable decides from
the arguments and, for `runtime`, from `ddtest-instrument.json` next to it. A
missing or unreadable configuration, or one with another contract, stops that
compilation with an error:

| Package | Edit |
| --- | --- |
| `testing`, `os/signal` | Test, exit, timeout and signal hooks, as in `ddtest instrument`'s overlay |
| `github.com/stretchr/testify/suite` | [Suite registration](testify.md) |
| `go.uber.org/goleak` | [Leak check entry](delivery.md#automatic-goleak-integration) |
| dd-trace-go v2's configuration, Bazel and tracer packages | CI guards and [span copies](sdk-span-mirror.md) |
| `runtime`, with `--panic-hook` | The panic hook |
| Mini's packages, with `-race` | `norace` annotations |
| Anything else | The real compiler, through exec, with no file I/O |

A vendored package keeps its edits: Gazelle's `importmap` names it
`<prefix>/vendor/<import path>`, and the compiler reads the part after the
last `vendor` element.

Edited files keep their names: each edited copy starts with a `//line`
directive with the original's absolute name, which `-trimpath` rewrites as
it rewrites the original. Test binaries stay reproducible and name no
temporary file.

**Hooks without Mini.** The edits only declare hooks that Mini sets when a
binary links it; a hook that nobody set keeps Go's own behavior. Bazel builds
tools in the exec configuration with the test's SDK, and the test's data may
build with it too. Such a binary, even one that imports `testing`, runs as it
would with the official SDK. The CI guards act only when Mini is linked, so
an application's own dd-trace-go keeps its configuration there.

**Unsupported libraries.** A compile action does not know module versions, so
each edit checks the code that it needs. A Testify or goleak release that
lacks it compiles unchanged with a warning, as outside Bazel; so does
dd-trace-go's tracer without the hooks of the span copies. A dd-trace-go v2
whose configuration code the CI guards do not find stops the compile with an
error. Bazel prints a warning only when it runs the compile action: a cached
action does not print it again, and `bazel clean` or a changed input brings
it back.

## Tests

The rule adds Mini's `testopt` library and the registration file that
`ddtest instrument` writes, and sets `DD_CIVISIBILITY_ENABLED=true`,
`DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true` and
`DD_TEST_OPTIMIZATION_MANIFEST_FILE`. Mini then reads the settings from the
manifest's cache and writes its payload files under
`TEST_UNDECLARED_OUTPUTS_DIR`, with no network
([delivery and offline output](mini-runtime.md#delivery-and-offline-output)).

`test.source.file` is relative to the workspace, because of `-trimpath`.
Mini resolves it under the test's runfiles to find a test's end line; the
rule's opt-in `stage_sources` puts the sources there. Without them,
`test.source.end` is missing.

Coverage is not reported in this first version.

With Bzlmod, rules_go 0.65.0 raises `golang.org/x/mod` and `golang.org/x/sys`
in `go_deps` to releases that need Go 1.26, so a workspace whose Go SDK is
1.25 cannot fetch them once a target depends on them, as dd-trace-go does.
Such a workspace stays on rules_go 0.64 or older.

## Mini's Go API from `go_deps`

A workspace whose tests use Mini's Go API
([explicit clients](mini-runtime.md#explicit-clients-and-manual-instrumentation))
also gets `github.com/DataDog/ddtest/ddto` from `go_deps`. A test would then
link two copies of Mini's packages. rules_go 0.64 reports no error for that and
picks one copy of each package, so the rule that uses ddtest must stop such a
test at analysis.

In Bzlmod, the root `MODULE.bazel` makes the rule's Mini repository visible
and overrides `go_deps`' copy with it:

```starlark
use_repo(<the rule's extension>, "<Mini repository>")
override_repo(go_deps, com_github_datadog_ddtest_ddto = "<Mini repository>")
```

`override_repo` applies only in the root module. A test that uses the API then
already depends on Mini's `testopt`, so the rule adds it only when the test's
`deps` do not name it: Bazel rejects a duplicated label. Builds with the
default SDK that use the API link the generated Mini too, which works without
the instrumented `testing`. rules_test_optimization documents its extension's
names and the WORKSPACE equivalent.

## Validation

ddtest's CI runs a basic Bazel test, `scripts/bazel_basic.py`, with a minimal
test-only rule in `testdata/bazel`: Bazel 8.8.2, rules_go 0.64.0 and Gazelle
0.54.0, on Linux with Go 1.25, 1.26 and 1.27 and on macOS with Go 1.27, in
Bzlmod and WORKSPACE modes. It checks:

- the statuses and suites of standard tests, Testify suites and goleak, with
  the targets whose tests all pass exiting successfully;
- the span copies of dd-trace-go v2 and v1.74 with their parents, and one
  session per test binary;
- a tool that imports `testing`, built with the instrumented SDK as an exec
  tool and as data;
- a `go_binary` that keeps the default SDK and has none of ddtest's symbols;
- the race detector's configuration;
- that no test binary names the compiler's temporary files;
- the same test and span events as `ddtest instrument go test` outside Bazel;
- in Bzlmod, Mini's Go API from `go_deps` with the override.

`internal/bazelgen` compares Mini's repository with `go list` on every
platform and with a golden file, and `TestBazelCompilerInstrumentsTheSDK`
checks the SDK through `go install std`, as rules_go's standard library
action does. rules_test_optimization and its test repository run the full
Bazel suites on every supported Bazel and rules_go version.
