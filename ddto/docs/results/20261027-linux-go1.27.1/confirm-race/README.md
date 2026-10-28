# Compile-only measurements: Native, POC Mini

POC `282b22a15776c3dfb1073d2e0197c78445c00d6f`; `go version go1.27.1 linux/amd64`; SDK `v2.12.0-dev.3.0.20261008222249-870449702d0a`; Orchestrion `v1.13.2-0.20260917114356-5c24783fcd76`.

Values are medians in seconds. Orchestrion's percentage compares time against Native. POC percentages compare Orchestrion first, then Native; all use unrounded medians.

All variants compile with `go test -c -o <directory>/ -ldflags=-w ./...`. Test binaries are never run. The selected variants use the same prepared source and module graph. Orchestrion loads only the pinned SDK's testing aspects.

Builds run serially in rotating order. Each cold run has an empty Go build cache and no existing output. Downloads are disabled during timing; modules and OS page cache stay warm. Affinity, `GOMAXPROCS` and `-p` match the selected CPU count; manifest.json records logical CPU IDs and physical core topology.

Unchanged output reuse, forced linking and reachable edits are checked with tool traces. Final binaries are checked for the expected testing/Testify hooks and absence of DWARF. CPU and memory include daemons and nested builds through exclusive cgroups. Wall time ends at the top-level command's exit; drain wait is recorded separately.

280 completed build commands, 28 qualified binaries. Native control repetitions: 0. A skipped control in a smoke run provides no stability evidence.

[Every observation](observations.csv), [ranges and uncertainty](statistics.json), [inputs](manifest.json) and [binary qualification](final-binaries.json) remain alongside this report. No slow samples are discarded. Fixture results are not a general application performance claim.

## Cold compilation

| Project | Flags | CPUs | Native | POC Mini |
| --- | --- | ---: | ---: | ---: |
| Gin | `-race` | 4 | 28.686 s | 29.349 s |
| Gin | `-race` | 32 | 24.298 s | 24.470 s |
| Testify External | `-race` | 4 | 12.920 s | 16.141 s |
| Testify External | `-race` | 32 | 10.118 s | 12.878 s |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | POC Mini |
| --- | --- | ---: | ---: | ---: |
| Gin | `-race` | 4 | 0.102 s | 0.211 s |
| Gin | `-race` | 32 | 0.105 s | 0.229 s |
| Testify External | `-race` | 4 | 0.060 s | 0.157 s |
| Testify External | `-race` | 32 | 0.062 s | 0.171 s |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | POC Mini |
| --- | --- | ---: | ---: | ---: |
| Gin | `-race` | 4 | 1.909 s | 2.941 s |
| Gin | `-race` | 32 | 1.950 s | 2.632 s |
| Testify External | `-race` | 4 | 0.350 s | 0.573 s |
| Testify External | `-race` | 32 | 0.366 s | 0.598 s |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | POC Mini |
| --- | --- | ---: | ---: | ---: |
| Gin | `-race` | 4 | 2.248 s | 2.507 s |
| Gin | `-race` | 32 | 2.261 s | 2.564 s |
| Testify External | `-race` | 4 | 0.363 s | 0.577 s |
| Testify External | `-race` | 32 | 0.378 s | 0.625 s |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | POC Mini |
| --- | --- | ---: | ---: | ---: |
| Gin | `-race` | 4 | 1.487 s | 1.585 s |
| Gin | `-race` | 32 | 1.504 s | 1.612 s |
| Testify External | `-race` | 4 | 0.084 s | 0.187 s |
| Testify External | `-race` | 32 | 0.088 s | 0.201 s |


