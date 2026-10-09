# Incorporated sources

This directory contains maintained source subsets, not Go module vendoring.
Mini consumers add only this module. Each origin has a README, exact upstream
commit, original
licenses, and a `SOURCE.json` manifest with upstream and local SHA-256 hashes.

| Origin | Local directory | Source base |
| --- | --- | --- |
| DataDog/dd-trace-go | `dd-trace-go/` | main at `870449702d0a0cea26a6223eefe2f0a198069d79` |
| tinylib/msgp | `msgp/` | v1.6.4 at `6f99c863451752e6aa9c7aacde4215a471a64242` |
| philhofer/fwd | `fwd/` | v1.2.0 at `20a13a1f6b7cb47a126dcb75152e21e1383bbaba` |
| golang/sys | `xsys/` | v0.47.0 at `9e7e939dcafac07e8ab4cffa6e5fc74908413f00` |

The POC's implementation stays in `internal/minitracer`, `internal/citransport`,
`internal/instrument`, and `internal/runner`. Local extensions that need access
to an upstream package's private state remain beside that package and are
marked `local` in its manifest. They must not be mistaken for upstream files.

## Audit and update

The [maintenance guide](../../docs/maintenance.md) explains the manifests, a
three-way SDK update, codec regeneration and platform validation. These commands
are the short reference; `rehash` updates local hashes and does not advance the
upstream revision.

From the repository root:

```sh
python3 scripts/upstream.py verify
python3 -m unittest discover -s scripts -p 'test_upstream.py'
python3 scripts/upstream.py diff --library dd-trace-go --source /path/to/new-sdk
python3 scripts/upstream.py patch --library dd-trace-go --source /path/to/recorded-sdk > /path/to/local-changes.diff
python3 scripts/upstream.py rehash --library dd-trace-go --source /path/to/recorded-sdk
```

`verify` is offline and rejects missing licenses, changed hashes, unsafe paths,
and source files missing from the manifest. `diff` compares selected upstream
files against the recorded base; review upstream additions separately. `patch`
requires the exact recorded original sources and shows our adaptations using
both source and destination paths. `rehash` records reviewed adaptation hashes only after the original source is
verified against the pinned base; it refuses missing files, unregistered source
additions, and modified license texts. Register new files and their ownership
explicitly. No command silently overwrites local code.

To synchronize, freeze the new upstream commit; obtain both the recorded and
new snapshots; review their diff; apply it with a three-way comparison to our
adapted sources; review newly added CI files/tests; then update the manifest's
commit, upstream hashes, local hashes, and local-extension classifications.
Retain the original licenses and notice files. Do not reintroduce removed APM
features or dependencies: the SDK manifest's `excluded_features` and its
[removed APM code](dd-trace-go/ADAPTATIONS.md#removed-apm-code) list them, and
the msgp README names its skipped directory. Generated files must be
regenerated using their checked-in directives. Run the provenance audit,
runtime/dependency checks, full SDK differential suite, coverage/race tests,
and platform checks. Update performance claims only after measuring the
changed implementation.

The manifests identify the source base used by this implementation. The
[benchmark inputs](../../docs/results/20261005-linux-go1.27.1/build/manifest.json)
identify the exact code and tool versions used for the latest measurements.
