# CI Visibility extracted from dd-trace-go

- Repository: https://github.com/DataDog/dd-trace-go
- Default branch: `main`
- Base commit: `870449702d0a0cea26a6223eefe2f0a198069d79`
- Go module version: `v2.12.0-dev.3.0.20261008222249-870449702d0a`
- License: original Apache-2.0 text in [LICENSE](LICENSE).
- Files and hashes: [SOURCE.json](SOURCE.json).
- Ported test inventory and adaptations: [TESTS.json](TESTS.json).
- Fuzz/Examples: [PR #5442](https://github.com/DataDog/dd-trace-go/pull/5442),
  integrated in this base. `feature_ports` retains the original source reference.

The [CODEOWNERS package](../../../docs/codeownership.md) is maintained here
with the SDK's CI Visibility code. It is registered as local SDK additions
until an upstream Go revision includes it.

## Layout and adaptations

Upstream `internal/<path>` maps to `<path>` here; `ddtrace/ext/<path>` keeps its
original path. Only the top `internal` level is removed: Go's nested-internal
rule would prevent our native runtime from importing the extracted packages.
All remaining package and fixture paths are retained. The manifest stores the
exact original path for every upstream file, including generated sources.

This is a CI-only port. Calls to the general tracer target our native client;
metadata access is explicit; APM security/profiling/mock hooks, YAML/Fleet,
telemetry heartbeats, SCA/endpoint inventories and process enrichment are
excluded. So are APM span tags, the URL sanitizer, tracer log files, AppSec
stack capture and telemetry rate/gauge metrics, integrations and flush tickers.
Environment variables are read directly, without the SDK's generated
configuration registry. [ADAPTATIONS.md](ADAPTATIONS.md#removed-apm-code)
lists what each reduced package keeps and how to merge upstream changes into
it. Concurrent telemetry registries use the standard library. Native
platform calls use the adjacent `xsys` subset. Our runtime version is owned by
`internal/version`, independently of the SDK base. The MessagePack schema,
mini client and HTTP transport are owned outside this origin.

[ADAPTATIONS.md](ADAPTATIONS.md) records the performance changes retained on top
of this base, including shared CI tags, bound counters, inline metric points,
lazy stack tables, deferred delivery and goleak checkpoints. It explains their
ownership rules and the checks to run when updating the corresponding paths.

Static library capabilities are published before asynchronous settings loading.
This keeps fast manual hierarchy calls from losing capabilities while the
network request is still running; feature decisions still wait for settings.

Local tests/adapters are classified separately in the manifest. Original
assertions are retained when porting tests. Updating the base also updates the
SDK differential fixture to the same exact version.

CI behavior includes the retry environment override, ITR missing-line-coverage
field/cache contract, and serialized Go runtime coverage emission. Their
regression tests are part of the ported inventory.

Follow the [shared update procedure](../README.md#audit-and-update). A patch
against the recorded base exposes the complete adaptation diff, instead of
relying on an import-rewrite script hidden outside the repository.
