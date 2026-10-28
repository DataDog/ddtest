<!-- Composed by results/20261027-linux-go1.27.1/compose.py from scripts/build_benchmark.py's, scripts/refresh_benchmark_report.py's and scripts/benchmark_report.py's renderers. -->
# Build, runtime and memory comparisons

Native, Orchestrion and `ddtest instrument` were measured together from 2026-10-27, at
ddtest `282b22a15776c3dfb1073d2e0197c78445c00d6f`:
the build, the runtime and the local Agent control. Each build scenario ran 3
times instead of the recorded counts (cold, cached, forced link, body edit,
constant edit: 5/10/20/10/3 for Gin and Chi without flags, 3/10/10/5/3 for the
other cases), and each runtime group 3 times instead of
5, to shorten the collection; a
[confirmation run](results/20261027-linux-go1.27.1/confirm-race/README.md) repeated the Gin and Testify
race builds, Native and `ddtest instrument`, 248 builds,
with the recorded counts.
All variants use `go version go1.27.1 linux/amd64` and the recorded subject versions,
flags and CPU affinity. The host is an AMD Ryzen 9 5950X: four CPUs use four
physical cores; 32 use all hardware threads.

The build comparison contains 2,160 measured builds,
24 subject/flag combinations and 4/32 CPUs. All 324
final binaries passed traces and binary qualification, and the Native link
control converged before the matrix.

All time cells are medians in seconds. Orchestrion's percentage compares its
time against Native. `ddtest instrument`'s percentages compare Orchestrion first, then Native.
For example, `(-50%; +20%)` means half Orchestrion's time and 20% more than Native's. Memory uses aggregate cgroup MiB.
The records keep CPU time, ranges, repetitions, failures and median uncertainty.
No slow observations are removed. With three repetitions, a small difference
in wall time alone does not establish a stable gain.

| Report | Contents |
| --- | --- |
| [Build](results/20261027-linux-go1.27.1/build/README.md) | Every flag combination in cold, cached, forced-link and both edit scenarios |
| [Race confirmation](results/20261027-linux-go1.27.1/confirm-race/README.md) | Gin and Testify race builds with the recorded repetitions |
| [Runtime](results/20261027-linux-go1.27.1/runtime/README.md) | Native, Orchestrion, `ddtest instrument` with normal and deferred delivery, each run, CPU, memory and event validation |
| [Agent control](results/20261027-linux-go1.27.1/agent/README.md) | Gin through the local EVP and telemetry proxy |
| [Memory](results/20261027-linux-go1.27.1/memory.md) | All build and runtime cells and the local Agent control |
| [Data and provenance](results/20261027-linux-go1.27.1/README.md) | Dates, revisions, repetitions and how this page is composed |

## Compilation

Commands compile with `go test -c -o <directory>/ -ldflags=-w ./...`.
Cold runs start with an empty Go build cache and removed output; modules and
the OS page cache remain warm. The other scenarios reuse output, force a link,
edit a reachable test body or edit an unused constant. The unused-constant edit
is a diagnostic; it can leave compiled code reusable.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
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

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
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

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
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

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
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

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
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

Orchestrion's percentage compares memory against Native. `ddtest instrument`'s percentages
compare memory against Orchestrion first and Native second. Failed variants
have no comparative median; their individual peaks remain in the raw records.

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
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

Three measured runs follow one warmup, for every variant in rotating
order. The clock includes process startup, concurrent package tests, settings
and final delivery to loopback HTTP. Compilation, receiver startup and decoding
are excluded. Telemetry is disabled in this matrix and enabled in the separate
local Agent control. Deferred delivery uses the same executable as normal delivery. The
runtime comes from two collections, each case from one of them; the
[runtime report](results/20261027-linux-go1.27.1/runtime/README.md#collections) explains why.

Test names, statuses and multiplicities match the archived Native inventory.
The archived SDK supplies CI event expectations for Orchestrion and `ddtest instrument`. New
native Examples are identified from the executable's function/source table and
checked explicitly. A failed or unavailable reference remains visible in the
validation results.

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` | `ddtest instrument`, deferred delivery |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.189747 s | 0.244531 s (+28.9%) | 0.220352 s (-9.9%; +16.1%) | 0.224050 s (-8.4%; +18.1%) |
| Gin | `none` | 32 | 0.197496 s | 0.238528 s (+20.8%) | 0.218361 s (-8.5%; +10.6%) | 0.217293 s (-8.9%; +10.0%) |
| Chi | `none` | 4 | 26.141200 s | 26.180264 s (+0.1%) | 26.155881 s (-0.1%; +0.1%) | 26.156406 s (-0.1%; +0.1%) |
| Chi | `none` | 32 | 26.141928 s | 26.158359 s (+0.1%) | 26.154071 s (-0.0%; +0.0%) | 26.161188 s (+0.0%; +0.1%) |
| Testify Direct | `none` | 4 | 0.002854 s | 0.014925 s (+422.9%) | 0.010428 s (-30.1%; +265.4%) | 0.010546 s (-29.3%; +269.5%) |
| Testify Direct | `none` | 32 | 0.003185 s | 0.016874 s (+429.7%) | 0.011855 s (-29.7%; +272.2%) | 0.011521 s (-31.7%; +261.7%) |
| Testify External | `none` | 4 | 0.002879 s | 0.014796 s (+413.9%) | 0.010448 s (-29.4%; +262.9%) | 0.010295 s (-30.4%; +257.6%) |
| Testify External | `none` | 32 | 0.003148 s | 0.016865 s (+435.8%) | 0.011802 s (-30.0%; +274.9%) | 0.011919 s (-29.3%; +278.6%) |

## Runtime: memory

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` | `ddtest instrument`, deferred delivery |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 35.1 MiB | 55.4 MiB (+57.9%) | 38.5 MiB (-30.5%; +9.7%) | 42.0 MiB (-24.1%; +19.8%) |
| Gin | `none` | 32 | 42.7 MiB | 81.3 MiB (+90.2%) | 52.8 MiB (-35.0%; +23.6%) | 53.7 MiB (-34.0%; +25.6%) |
| Chi | `none` | 4 | 34.2 MiB | 57.1 MiB (+67.0%) | 33.5 MiB (-41.3%; -1.9%) | 45.0 MiB (-21.2%; +31.6%) |
| Chi | `none` | 32 | 106.1 MiB | 133.8 MiB (+26.1%) | 91.5 MiB (-31.6%; -13.8%) | 94.1 MiB (-29.7%; -11.3%) |
| Testify Direct | `none` | 4 | 11.1 MiB | 17.4 MiB (+57.2%) | 13.7 MiB (-21.4%; +23.6%) | 13.2 MiB (-24.6%; +18.5%) |
| Testify Direct | `none` | 32 | 12.0 MiB | 20.6 MiB (+71.8%) | 14.3 MiB (-30.8%; +18.8%) | 14.6 MiB (-29.4%; +21.2%) |
| Testify External | `none` | 4 | 11.0 MiB | 17.5 MiB (+58.3%) | 13.3 MiB (-23.8%; +20.6%) | 13.3 MiB (-23.7%; +20.9%) |
| Testify External | `none` | 32 | 11.5 MiB | 20.3 MiB (+76.0%) | 15.3 MiB (-24.7%; +32.6%) | 14.6 MiB (-28.0%; +26.7%) |

## Read and repeat the measurements

[Collection and regeneration](build-benchmarks.md) explains how to run the
benchmarks. The [runtime report](results/20261027-linux-go1.27.1/runtime/README.md) lists
failed or unverified groups and preserves their observed durations. Those groups
have no comparative median in the main tables. Coverage in Go's default set
mode is aggregate only; per-test uploads use count or atomic mode.

These are Linux/amd64 measurements with a loopback receiver. Live intake and
other operating systems are covered separately by [validation](validation.md).
