# Latest local benchmark data

Measured on Linux/amd64, Go 1.27.1, at POC commit
`9d4786fbd27f573bb68c5516bf23b336e8d7bdaa` on 2026-10-04/05.
SDK: `96aedb31048c07e29e7a20a4333dc3b8d289c52d`.
The [comparison document](../../benchmarks.md) explains the results and limits;
the [runner guide](../../build-benchmarks.md) gives collection and offline
regeneration commands. Updating the documentation does not refresh measurements.

| Directory or file | Records |
| --- | --- |
| [Build](build/README.md) | 6,224 comparative observations; 7,153 commands including controls and qualification; all five scenarios and 48 CPU/configuration cells |
| [Runtime](runtime/README.md) | 1,440 groups: 1,200 measured and 240 warmups; four Gin race cells contain real failures |
| [Memory](memory.md) | All build/runtime combinations and the Agent control, in aggregate cgroup MiB |
| [Parity](parity/README.md) | 115 cases × six rounds × two CPU counts, plus supplemental fixtures; durations and matching event counts |
| [Agent](agent/README.md) | Gin with local EVP and CI telemetry: 24 measured groups after eight warmups |
| [First Native control](control-first/convergence.json) | The failed 80-run convergence check; the successful 160-run control is in `build/convergence.json` |
| [Collection verification](verification.json) | Frozen report hashes, completed counts and final time/disk accounting |
| [Archive hashes](archive.json) | Source paths and capture hashes, plus hashes of the checked-in files and decompressed records |

`runtime/observations.csv` retains every duration, CPU time, `peak_bytes`, exit
status and original/validated failure classification. The compressed
`runtime/original-observations.jsonl.gz` and `runtime/validated-observations.json.gz`
also preserve test inventories, event delivery and coverage details. Use validated
classifications for comparison eligibility; raw classifications remain available
for audit. Passing coverage runs without a successful SDK oracle are unverified,
and failed race samples remain in both records.

The twelve `parity/cpu{4,32}/round{0..5}/` directories retain all feature/count
reports and harness clocks. No separate per-case SDK/Mini memory peaks were
collected for parity; project runtime peaks are in the runtime records.

`provenance/` contains the temporary collection/validation helper sources as
they ran, with their hashes. They record absolute local paths and are evidence,
not a portable runner. Use the maintained scripts in `scripts/` to collect new
builds, run parity or regenerate these tables.

The original artifact root was `/var/tmp/dd-ci-benchmark-20261004.s81k5i3w`.
Absolute paths in records identify that collection and are not required for
table regeneration. Large binaries, caches, full test logs and HTTP bodies stay
outside the repository. The collection completed within its agreed 10 h 30 min
active-time and 32 GiB limits. SDK provenance, adaptations and compatibility
fixtures have separate maintenance records.

## Data and report text

Raw CSV, JSON and compressed records are measurement inputs. Their capture
hashes stay fixed. Markdown explains and presents those inputs; editing its
prose does not change a measurement. `archive.json` records both the source
capture hash and the checked-in hash for each file. Its `presentation_only`
flag identifies Markdown whose explanatory text is maintained here.

The [regeneration commands](../../build-benchmarks.md#regenerate-the-recorded-tables)
check the archive hashes and rebuild the build, overview and memory tables.
