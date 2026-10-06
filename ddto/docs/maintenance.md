# Maintenance and source updates

The files under `internal/thirdparty` are maintained source subsets. We own their
adaptations and tests. Each origin records an exact upstream commit so a later
update can compare the original code with both the new source and our port.

An SDK update must preserve CI behavior and the Mini runtime's dependency
boundary. Review upstream changes in removed APM code too when they affect a
shared helper or the CI wire schema.

## Source ownership and records

[`internal/thirdparty/README.md`](../internal/thirdparty/README.md) lists the
origins and revisions. Every origin has its original licenses and a
`SOURCE.json` manifest. Paths in that manifest are relative to the origin
directory and use forward slashes.

| Record | Meaning |
| --- | --- |
| `repository`, `commit`, `version` | Upstream repository, exact 40-character Git SHA and module version |
| `files[].path`, `sha256` | Local file and its exact byte hash |
| `source_path`, `source_sha256` | Original upstream path and bytes at the recorded base |
| `role: upstream` | A copied file that matches upstream |
| `role: adapted` | A derived file with local changes |
| `role: local` | POC-owned code, test or documentation beside the upstream package |
| `licenses` | License files that must remain present and recorded |
| `additional_sources` | Upstream inputs used for an extraction or schema outside a direct file copy |

The SDK's [test inventory](../internal/thirdparty/dd-trace-go/TESTS.json) records
ported assertions and adaptations. The platform
[extraction inventory](../internal/thirdparty/xsys/EXTRACTION.json) identifies
selected declarations and their origins. These records need to agree with
`SOURCE.json` after an update.

The SDK's [performance adaptation record](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md)
explains each hot-path change, its ownership/lifetime rules and the regression
checks required during synchronization. Review it alongside the generated patch;
hashes identify changed bytes but do not explain why those changes exist.

SDK `internal/<path>` maps to `internal/thirdparty/dd-trace-go/<path>`.
Its `ddtrace/ext/` subtree keeps that relative path. Removing only the top
upstream `internal/` avoids a second Go visibility boundary. Codec files retain
`msgp/msgp/` and the original `fwd/` root layout; platform code keeps
`xsys/windows/`, `windows/registry/` and `unix/`.

Code outside these origins has a separate owner. The native event schema in
[`events.go`](../internal/minitracer/events.go) is derived from the SDK's
`ddtrace/tracer/civisibility_tslv.go`, recorded under `additional_sources`.
It must be reviewed even when the copied CI files are unchanged.

## Audit commands

Run these from the repository root with Python 3:

```sh
python3 scripts/upstream.py verify
python3 -B -m unittest discover -s scripts -p 'test_upstream.py'
```

`verify` checks recorded local hashes, exact commit syntax, licenses, safe paths
and unregistered Go or assembly sources. It runs offline. It does not contact
upstream to prove the supplied commit's contents or discover new upstream files.

Given explicit upstream snapshots:

```sh
python3 scripts/upstream.py diff --library dd-trace-go --source "$NEW_SDK"
python3 scripts/upstream.py patch --library dd-trace-go --source "$OLD_SDK" > "$UPDATE_DIR/local-adaptations.diff"
```

`diff` reports changed or missing selected source inputs. A successful exit can
still report changed files; read the output. New upstream files outside the
selection are not listed. `patch` first checks that the supplied old snapshot
matches the recorded hashes, then writes the adaptation diff with original and
local paths. Binary fixtures remain covered by hashes.

After reviewing a local change against the same upstream base:

```sh
python3 scripts/upstream.py rehash --library dd-trace-go --source "$OLD_SDK"
python3 scripts/upstream.py verify
```

`rehash` changes local hashes only. It preserves the upstream revision and
original hashes, and refuses missing files, unregistered code and changed
license texts. It cannot advance the SDK base. Origin READMEs and local test
inventories are also hashed; editing them needs the same review and rehash.

## Updating the SDK base

Prepare an isolated POC worktree and an update directory outside it. The examples
below use a Unix shell, Git and tar. On Windows, use Git Bash or create equivalent
archives with your Git client.

1. Read the current SDK manifest and freeze the candidate SHA from the SDK's
   default branch. Record its module version and verify that the Git commit and
   module version identify the same revision. Resolve the default branch for
   every update rather than assuming the pinned base is still its tip. This
   command returns both the branch name and its current full SHA:

```sh
git ls-remote --symref https://github.com/DataDog/dd-trace-go.git HEAD
```

2. Obtain both snapshots from an SDK checkout containing those commits.
   Set `SDK_CHECKOUT` to that checkout and `UPDATE_DIR` to a new scratch
   directory. Read `OLD_SHA` from the manifest and set `NEW_SHA` to the
   candidate's full SHA.

```sh
OLD_SDK="$UPDATE_DIR/old-sdk"
NEW_SDK="$UPDATE_DIR/new-sdk"
mkdir -p "$OLD_SDK" "$NEW_SDK"
git -C "$SDK_CHECKOUT" archive "$OLD_SHA" | tar -x -C "$OLD_SDK"
git -C "$SDK_CHECKOUT" archive "$NEW_SHA" | tar -x -C "$NEW_SDK"

python3 scripts/upstream.py diff --library dd-trace-go --source "$NEW_SDK"
python3 scripts/upstream.py patch --library dd-trace-go --source "$OLD_SDK" > "$UPDATE_DIR/local-adaptations.diff"
git -C "$SDK_CHECKOUT" diff --name-status "$OLD_SHA" "$NEW_SHA" -- internal/civisibility ddtrace/tracer/civisibility_tslv.go
```

3. Compare each affected file with three inputs: the old upstream source, the
   new upstream source and the current local adaptation. Retain import
   relocation, native-client bindings, CI-only exclusions and local ownership
   tests. Inspect added and deleted CI files separately, including helpers
   called by CI code and changes to the schema recorded in `additional_sources`.

```mermaid
flowchart TB
    Old["Recorded upstream<br/>base"] --> Upstream["Review old-to-new<br/>upstream changes"]
    New["New default-branch<br/>snapshot"] --> Upstream
    Old --> Adaptations["Review old-to-local<br/>adaptations"]
    Local["Current CI-only port"] --> Adaptations
    Upstream --> Merge["Merge selected<br/>changes into the<br/>local port"]
    Adaptations --> Merge
    Merge --> Records["Update source<br/>records, pins and<br/>test inventory"]
    Records --> Generate["Regenerate affected<br/>serializers"]
    Generate --> Audit["Audit hashes,<br/>licenses and<br/>dependency boundary"]
    Audit --> Tests["Differential,<br/>coverage, race and<br/>platform checks"]
```

4. Update the SDK `SOURCE.json` repository/version/commit fields, original
   hashes and source paths to the new base. Classify every new local or derived
   file explicitly and remove records only for deliberately removed files.
   Preserve original copyright headers, LICENSE and NOTICE; review any upstream
   license change. Refresh the ported-test inventory and its own manifest hash.
   If a wire-schema input changes, update its source hash and our native schema
   together.

5. Advance `SDKVersion` and `SDKCommit` in
   [`internal/version/version.go`](../internal/version/version.go).
   Update `testdata/fixture/go.mod` and its checksums to that exact module
   version, and review transitive module changes there. The CLI, differential
   tests and fixture must agree. The fixture's full SDK dependencies belong to
   comparison tests; they must not become Mini runtime imports.

6. Regenerate affected code, then use `rehash` against `NEW_SDK` after the
   original-hash records have been updated. Refresh per-origin READMEs,
   [NOTICE](../NOTICE), the root source overview and any configuration or
   compatibility descriptions changed by the update. Benchmark reports keep
   their measured revision until a new run replaces them; use the
   [benchmark guide](build-benchmarks.md) to update the latest dataset.

7. Run the applicable checks below and inspect the complete diff. An update
   description should identify the old and new SHAs, CI behavior changes,
   adaptations preserved, dependency result and platform proof.

The update is a reviewed merge. There is no command in this repository that
replaces an origin wholesale with the latest upstream source.

## Codecs and generated files

The MessagePack generator pins live in
[`scripts/internal/messagepack/source.go`](../scripts/internal/messagepack/source.go).
The copier reads those exact versions from the Go module cache; its output
includes per-origin `COPY.json` files. Those copy inventories do not replace the
canonical `SOURCE.json` record.

For a deliberate codec recopy at the checked-in pins:

```sh
go mod download github.com/tinylib/msgp@v1.6.4 github.com/philhofer/fwd@v1.2.0
go run ./scripts/vendor-msgpack
go generate ./internal/thirdparty/msgp/msgp ./internal/minitracer ./internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage
```

The copier overwrites selected codec sources, so run it in an isolated worktree
and review the diff before keeping its output. A codec version upgrade also
changes the generator pins, exact Git SHAs, manifests and origin READMEs.
Generated `*_msgp.go` files come from their source schema and directives; edit
those inputs and regenerate.

Generation runs the pinned tool in an isolated module. Tool dependencies are
allowed there; the resulting Mini runtime must still import only this module
and the standard library. Check `go.mod` and `go.sum` for unintended changes.

## Platform updates and Go upgrades

The `xsys` origin is a selected adaptation, so review the declarations in
`EXTRACTION.json` alongside `SOURCE.json` and `additional_sources`. Changes
to Windows Job Objects, suspended-thread handling, timer layouts or registry
decoding need native regression tests and ABI checks for the affected
architectures. Unix metadata changes need fixed-buffer, partial-read and
formatting checks. Solaris's runtime trampoline needs special review when Go
changes.

For a Go upgrade, inspect the transformer and private hook signatures, testing
reflection offsets, retry-process handling and the runtime coverage emitter.
Add the new toolchain to the compatibility matrix only after exercising it.
Keep the oldest tested toolchain until a compatibility change is explicitly
accepted.

## Verification before publication

Use focused checks while developing. For an SDK, codec, platform or ownership
change, the relevant full validation is:

```sh
python3 scripts/upstream.py verify
python3 -B -m unittest discover -s scripts -p 'test_upstream.py'
go vet ./...
go mod verify
(cd testdata/fixture && go mod verify)
go list -deps -f '{{if and (not .Standard) .Module}}{{.Module.Path}}{{end}}' ./testopt ./cmd/ddtest | sort -u
```

The final command should list only `github.com/tonyredondo/dd-ci-testing-poc`.
Test-only dependencies in the root module are expected. Their presence in
`go.mod` alone does not establish a runtime dependency, but Go's minimal version
selection applies them to consumers' module graphs. Keep
`github.com/stretchr/testify` at v1.7.5: the oldest release that builds the
ported tests and requires a `yaml.v3` without CVE-2022-28948. A consumer with
Testify v1.7.5 or newer keeps its version; one without Testify gains no
requirement, only `go.sum` checksums for Testify's `go.mod`.
`TestMiniConsumerAddsOnlyOwnModule` fails if the requirement is raised. Adapt a
ported assertion that needs a newer Testify instead of raising the requirement:
before v1.8.2, for example, `Nil` rejects a nil `unsafe.Pointer`, so those
checks use `Zero`.

Install the frozen Orchestrion reference used by the
[workflow](../.github/workflows/compatibility.yml), then export
`ORCHESTRION_BIN` to that binary's absolute path. On Windows the binary has an
`.exe` suffix. Without this variable, the Orchestrion comparison is skipped.

```sh
go test -count=1 -timeout=20m ./...
go test -race -count=1 -timeout=20m ./...
go test -race -covermode=atomic \
  -coverpkg=./internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage \
  -run TestRuntimeCoverage -count=1 \
  ./internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage
```

The full compatibility suite exercises actual binaries, payloads, retry
processes and failure paths. Its comparison preserves CI attributes and
hierarchy semantics while allowing the documented APM-only differences.
Do not expand normalization to hide a changed CI value.

The CI matrix executes Go 1.26/1.27 on Linux and Go 1.27 on macOS/Windows.
Linux also runs `-race`. Inspect the current head and PR merge checks after
publication and verify that their checked-out inputs match the proposed change.
Cross-compiling another architecture establishes build compatibility only.
Actual intake acceptance and a real Bazel compiler invocation are separate
verification tasks.

For a docs-only change, check local links, code paths, command examples and
Mermaid rendering. Diagrams are editable `mermaid` fences rendered by
[GitHub](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/creating-diagrams).
Update an invariant's description when its implementation changes.

## Updating Testify instrumentation

The Testify source belongs to the client; it is not an incorporated source subset.
The [Testify design and validation notes](testify.md) describe the version floor,
ABI guard, selective tool dispatch, cache fingerprint and coverage bridge. Review
the SDK's Testify advice and registration
hook together with a Testify version update. Add the version to
`TestTestifySupportedVersionsAndNativeSemantics` and run the full Orchestrion
comparison, including covered library/helpers, external callers, user overlays,
GOFLAGS and both retry modes. Keep the SDK/Orchestrion reference independent of
both POC backends. No Testify source is vendored into this repository.

`testifyContractVersion` in [`runner/testify.go`](../internal/runner/testify.go)
versions compiler-side edits. Bump it when the transformation changes without
changing the prepared source or hook; otherwise Go can reuse an older
instrumented object. `coverContractVersion` in
[`runner/cover.go`](../internal/runner/cover.go) versions the coverage bridge.
Prepared content fingerprints omit temporary paths. Preserve native compiler
and linker `-V=full` responses and check unchanged builds avoid both tools.

Run [`TestTestifyVersionGuardWithWarmVendoredSources`](../integration/vendor_cache_test.go)
when changing detection or version validation. Its source bytes stay constant
while module/vendor metadata changes, so compile-time-only guards cannot pass
it. Also run [`TestTestifyContractInvalidatesOnlyTestingDependents`](../integration/selective_tools_test.go)
when changing the marker or tool identity: Testify must rebuild while unrelated
standard-library packages remain cached. These assertions use
`compilerTraceLines` to recognize native `go test -x` commands, including quoted
Windows executables and Unix paths with spaces. Keep that parser control when
changing cache tests; a missed command can invalidate their proof. The
[Testify contract](testify.md) explains why selected-version validation must
run before compilation even when Go can reuse a cached archive.

After a platform change, check Unix `exec` replacement and Windows child exit
status separately. A cross-compiled CLI is build proof; the workflow must run
the integration fixtures natively on that platform. Update the architecture,
Testify contract, validation inventory and performance guide together.
