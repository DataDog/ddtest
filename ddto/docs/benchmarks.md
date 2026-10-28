<!-- Composed by results/20261027-linux-go1.27.1/compose.py from scripts/build_benchmark.py's and scripts/benchmark_report.py's renderers. -->
# Build, runtime and memory comparisons

Build: Native, Orchestrion and POC Mini were measured together from 2026-10-27,
at ddtest `282b22a15776c3dfb1073d2e0197c78445c00d6f`, where POC Mini runs as `ddtest instrument`.
Each scenario ran 3 times instead of the recorded counts (cold, cached, forced
link, body edit, constant edit: 5/10/20/10/3 for Gin and Chi without flags,
3/10/10/5/3 for the other cases), to shorten the collection; a
[confirmation run](results/20261027-linux-go1.27.1/confirm-race/README.md) repeated the Gin and Testify
race cases, Native and POC Mini, 248 builds,
with the recorded counts.
Runtime: retained from [20261016-linux-go1.27.1](results/20261016-linux-go1.27.1/README.md), where POC
Mini was measured on 2026-10-16 in ddto's former repository and Native and
Orchestrion on 2026-10-04; it was not rerun.
All variants use `go version go1.27.1 linux/amd64` and the recorded subject versions,
flags and CPU affinity. The host is an AMD Ryzen 9 5950X: four CPUs use four
physical cores; 32 use all hardware threads.

The build comparison contains 2,160 measured builds,
24 subject/flag combinations and 4/32 CPUs. All 324
final binaries passed traces and binary qualification, and the Native link
control converged before the matrix.

All time cells are medians in seconds. Orchestrion's percentage compares its
time against Native. Mini's percentages compare Orchestrion first, then Native.
For example, `(-50%; +20%)` means half Orchestrion's time and 20% more than Native's. Memory uses aggregate cgroup MiB.
The records keep CPU time, ranges, repetitions, failures and median uncertainty.
No slow observations are removed. With three repetitions, a small difference
in wall time alone does not establish a stable gain.

| Report | Contents |
| --- | --- |
| [Build](results/20261027-linux-go1.27.1/build/README.md) | Every flag combination in cold, cached, forced-link and both edit scenarios |
| [Race confirmation](results/20261027-linux-go1.27.1/confirm-race/README.md) | Gin and Testify race cases with the recorded repetitions |
| [Runtime](results/20261016-linux-go1.27.1/runtime/README.md) | Normal/deferred Mini, each run, CPU, memory and event validation (2026-10-16) |
| [Memory](results/20261027-linux-go1.27.1/memory.md) | All build cells; [runtime and the local Agent control](results/20261016-linux-go1.27.1/memory.md) (2026-10-16) |
| [Data and provenance](results/20261027-linux-go1.27.1/README.md) | Dates, revisions, repetitions and how this page is composed |

## Compilation

Commands compile with `go test -c -o <directory>/ -ldflags=-w ./...`.
Cold runs start with an empty Go build cache and removed output; modules and
the OS page cache remain warm. The other scenarios reuse output, force a link,
edit a reachable test body or edit an unused constant. The unused-constant edit
is a diagnostic; it can leave compiled code reusable.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 21.355 s | 57.543 s (+169.5%) | 22.531 s (-60.8%; +5.5%) |
| Gin | `none` | 32 | 13.404 s | 28.275 s (+110.9%) | 15.274 s (-46.0%; +13.9%) |
| Chi | `none` | 4 | 8.592 s | 33.667 s (+291.8%) | 11.089 s (-67.1%; +29.1%) |
| Chi | `none` | 32 | 5.560 s | 19.359 s (+248.2%) | 6.935 s (-64.2%; +24.7%) |
| Testify Direct | `none` | 4 | 8.895 s | 31.245 s (+251.3%) | 11.212 s (-64.1%; +26.0%) |
| Testify Direct | `none` | 32 | 5.174 s | 21.245 s (+310.6%) | 7.290 s (-65.7%; +40.9%) |
| Testify External | `none` | 4 | 8.940 s | 33.706 s (+277.0%) | 11.503 s (-65.9%; +28.7%) |
| Testify External | `none` | 32 | 5.962 s | 23.816 s (+299.4%) | 7.061 s (-70.4%; +18.4%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.098 s | 0.374 s (+283.0%) | 0.184 s (-50.6%; +89.1%) |
| Gin | `none` | 32 | 0.102 s | 0.441 s (+330.5%) | 0.217 s (-50.8%; +111.9%) |
| Chi | `none` | 4 | 0.065 s | 0.352 s (+444.3%) | 0.128 s (-63.7%; +97.5%) |
| Chi | `none` | 32 | 0.066 s | 0.411 s (+524.7%) | 0.135 s (-67.2%; +105.0%) |
| Testify Direct | `none` | 4 | 0.063 s | 0.339 s (+439.0%) | 0.143 s (-57.9%; +127.2%) |
| Testify Direct | `none` | 32 | 0.062 s | 0.389 s (+529.2%) | 0.162 s (-58.3%; +162.4%) |
| Testify External | `none` | 4 | 0.061 s | 0.347 s (+469.3%) | 0.149 s (-57.1%; +144.2%) |
| Testify External | `none` | 32 | 0.062 s | 0.392 s (+533.5%) | 0.162 s (-58.7%; +161.6%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.337 s | 6.183 s (+362.4%) | 1.838 s (-70.3%; +37.4%) |
| Gin | `none` | 32 | 2.799 s | 6.523 s (+133.1%) | 1.706 s (-73.9%; -39.1%) |
| Chi | `none` | 4 | 0.282 s | 2.155 s (+663.5%) | 0.425 s (-80.3%; +50.4%) |
| Chi | `none` | 32 | 0.366 s | 1.889 s (+416.9%) | 0.438 s (-76.8%; +19.8%) |
| Testify Direct | `none` | 4 | 0.276 s | 1.820 s (+559.7%) | 0.423 s (-76.8%; +53.2%) |
| Testify Direct | `none` | 32 | 0.351 s | 2.105 s (+500.6%) | 0.599 s (-71.6%; +70.8%) |
| Testify External | `none` | 4 | 0.274 s | 1.861 s (+579.3%) | 0.426 s (-77.1%; +55.6%) |
| Testify External | `none` | 32 | 0.279 s | 1.922 s (+587.8%) | 0.459 s (-76.1%; +64.3%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.258 s | 3.023 s (+140.3%) | 1.520 s (-49.7%; +20.8%) |
| Gin | `none` | 32 | 1.199 s | 3.200 s (+166.8%) | 1.470 s (-54.1%; +22.6%) |
| Chi | `none` | 4 | 0.481 s | 1.924 s (+299.8%) | 0.588 s (-69.4%; +22.3%) |
| Chi | `none` | 32 | 0.453 s | 2.041 s (+350.8%) | 0.603 s (-70.5%; +33.2%) |
| Testify Direct | `none` | 4 | 0.312 s | 1.796 s (+476.0%) | 0.549 s (-69.4%; +76.2%) |
| Testify Direct | `none` | 32 | 0.320 s | 2.140 s (+567.9%) | 0.502 s (-76.6%; +56.6%) |
| Testify External | `none` | 4 | 0.324 s | 2.018 s (+523.3%) | 0.454 s (-77.5%; +40.3%) |
| Testify External | `none` | 32 | 0.303 s | 1.910 s (+531.3%) | 0.486 s (-74.5%; +60.8%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.841 s | 1.259 s (+49.6%) | 0.939 s (-25.4%; +11.6%) |
| Gin | `none` | 32 | 0.739 s | 1.207 s (+63.2%) | 0.837 s (-30.6%; +13.2%) |
| Chi | `none` | 4 | 0.307 s | 0.711 s (+131.5%) | 0.374 s (-47.5%; +21.6%) |
| Chi | `none` | 32 | 0.299 s | 0.774 s (+158.4%) | 0.404 s (-47.8%; +34.9%) |
| Testify Direct | `none` | 4 | 0.115 s | 0.609 s (+432.2%) | 0.202 s (-66.9%; +76.1%) |
| Testify Direct | `none` | 32 | 0.111 s | 0.905 s (+712.2%) | 0.215 s (-76.3%; +92.9%) |
| Testify External | `none` | 4 | 0.086 s | 0.608 s (+605.3%) | 0.177 s (-70.8%; +105.6%) |
| Testify External | `none` | 32 | 0.089 s | 0.654 s (+634.6%) | 0.192 s (-70.7%; +115.2%) |

## Cold compilation: memory

Each cell is the median of the per-run `memory.peak` values, in MiB
(1 MiB = 1,048,576 bytes). The cgroup includes all build or test processes,
detached daemons, nested builds and the measurement helper. Charged file-cache
pages and kernel memory are included. This is neither a Go heap measurement
nor the sum of independently observed process RSS peaks. Runtime receivers run
outside the measured cgroup. Warmups and build qualification commands are excluded.

Orchestrion's percentage compares memory against Native. Mini's percentages
compare memory against Orchestrion first and Native second. Failed variants
have no comparative median; their individual peaks remain in the raw records.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1643.1 MiB | 2370.4 MiB (+44.3%) | 1763.6 MiB (-25.6%; +7.3%) |
| Gin | `none` | 32 | 1787.8 MiB | 2847.4 MiB (+59.3%) | 1809.0 MiB (-36.5%; +1.2%) |
| Chi | `none` | 4 | 429.9 MiB | 1242.9 MiB (+189.1%) | 529.6 MiB (-57.4%; +23.2%) |
| Chi | `none` | 32 | 630.4 MiB | 1988.0 MiB (+215.3%) | 693.5 MiB (-65.1%; +10.0%) |
| Testify Direct | `none` | 4 | 425.1 MiB | 994.6 MiB (+134.0%) | 529.0 MiB (-46.8%; +24.4%) |
| Testify Direct | `none` | 32 | 669.6 MiB | 1258.7 MiB (+88.0%) | 703.4 MiB (-44.1%; +5.1%) |
| Testify External | `none` | 4 | 426.7 MiB | 988.6 MiB (+131.7%) | 529.2 MiB (-46.5%; +24.0%) |
| Testify External | `none` | 32 | 646.9 MiB | 1281.9 MiB (+98.2%) | 712.1 MiB (-44.5%; +10.1%) |

## Runtime of the original test binaries

Five measured runs follow one warmup. The clock includes process startup,
concurrent package tests, settings and final delivery to loopback HTTP.
Compilation, receiver startup and decoding are excluded. Telemetry is disabled
in this matrix and enabled in the separate local Agent control. Mini deferred
uses the same executable as normal Mini.

Test names, statuses and multiplicities match the retained Native inventory.
The recorded SDK supplies CI event expectations. New native Examples are
identified from the executable's function/source table and checked explicitly.
A failed or unavailable reference remains visible in the validation results.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.183472 s | 0.227665 s (+24.1%) | 0.221166 s (-2.9%; +20.5%) | 0.227677 s (+0.0%; +24.1%) |
| Gin | `none` | 32 | 0.192854 s | 0.235763 s (+22.2%) | 0.214014 s (-9.2%; +11.0%) | 0.223292 s (-5.3%; +15.8%) |
| Chi | `none` | 4 | 26.136864 s | 26.174048 s (+0.1%) | 26.160106 s (-0.1%; +0.1%) | 26.157522 s (-0.1%; +0.1%) |
| Chi | `none` | 32 | 26.141335 s | 26.179190 s (+0.1%) | 26.171993 s (-0.0%; +0.1%) | 26.166768 s (-0.0%; +0.1%) |
| Testify Direct | `none` | 4 | 0.002768 s | 0.014293 s (+416.4%) | 0.010578 s (-26.0%; +282.2%) | 0.010303 s (-27.9%; +272.3%) |
| Testify Direct | `none` | 32 | 0.003034 s | 0.016014 s (+427.8%) | 0.011907 s (-25.6%; +292.4%) | 0.011984 s (-25.2%; +295.0%) |
| Testify External | `none` | 4 | 0.002716 s | 0.014248 s (+424.6%) | 0.010423 s (-26.8%; +283.8%) | 0.010840 s (-23.9%; +299.1%) |
| Testify External | `none` | 32 | 0.002940 s | 0.016444 s (+459.4%) | 0.011769 s (-28.4%; +300.3%) | 0.012057 s (-26.7%; +310.1%) |

## Runtime: memory

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 36.6 MiB | 60.3 MiB (+64.5%) | 38.9 MiB (-35.4%; +6.3%) | 38.6 MiB (-36.0%; +5.3%) |
| Gin | `none` | 32 | 42.2 MiB | 70.7 MiB (+67.6%) | 53.1 MiB (-24.9%; +25.8%) | 51.1 MiB (-27.7%; +21.2%) |
| Chi | `none` | 4 | 45.6 MiB | 79.7 MiB (+74.5%) | 32.1 MiB (-59.7%; -29.6%) | 31.5 MiB (-60.5%; -31.0%) |
| Chi | `none` | 32 | 94.7 MiB | 128.0 MiB (+35.2%) | 57.5 MiB (-55.1%; -39.3%) | 67.8 MiB (-47.0%; -28.4%) |
| Testify Direct | `none` | 4 | 14.5 MiB | 26.6 MiB (+83.0%) | 13.4 MiB (-49.6%; -7.8%) | 13.0 MiB (-51.2%; -10.7%) |
| Testify Direct | `none` | 32 | 15.0 MiB | 29.4 MiB (+96.5%) | 15.4 MiB (-47.5%; +3.2%) | 14.6 MiB (-50.5%; -2.7%) |
| Testify External | `none` | 4 | 14.3 MiB | 27.1 MiB (+89.1%) | 13.2 MiB (-51.2%; -7.7%) | 13.0 MiB (-52.2%; -9.6%) |
| Testify External | `none` | 32 | 14.5 MiB | 32.3 MiB (+123.1%) | 15.1 MiB (-53.2%; +4.5%) | 15.1 MiB (-53.3%; +4.2%) |

## Read and repeat the measurements

[Collection and regeneration](build-benchmarks.md) explains how to run the
benchmarks. The [runtime report](results/20261016-linux-go1.27.1/runtime/README.md) lists
failed or unverified groups and preserves their observed durations. Those groups
have no comparative median in the main tables. Coverage in Go's default set
mode is aggregate only; per-test uploads use count or atomic mode.

These are Linux/amd64 measurements with a loopback receiver. Live intake and
other operating systems are covered separately by [validation](validation.md).
The archived runtime and parity measurements keep their original revisions and
dates; they were not repeated by this build collection.
