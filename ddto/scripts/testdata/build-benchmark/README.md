# Frozen benchmark inputs

These inputs define the module graphs and Testify fixtures for the build matrix.
They belong to the comparison, not the Mini runtime dependency graph.
The comparison graph intentionally includes both instrumentators and both
runtimes so Native, Orchestrion, SDK and Mini select identical dependency versions.

`../../build-benchmark.json` records the SDK version/commit, Orchestrion version,
Gin/Chi upstream versions and SHAs, expected binaries and edit signatures.
Gin/Chi source is obtained from the matching Go module during setup; only their
prepared `go.mod.template` and `go.sum` are stored here. The Testify fixtures are
POC-owned sources and use the same comparison dependency versions.

`@POC_ROOT@` is replaced with the current checkout's quoted absolute path.
The external fixture's `example.com/testkit` replacement stays relative to that
fixture. No other module version changes are made during setup. All four variants
share each prepared subject; temporary copies and overlays leave these files
unchanged.

See [the runner protocol](../../../docs/build-benchmarks.md) before updating
these inputs. Use a new artifact directory when a project, SDK pin, graph or
fixture changes, then validate the run before updating the comparison document.
