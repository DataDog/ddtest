# Compile-only measurements: POC Mini

POC `126bf4fdd4c861d12453def57e9e47a05d003120`; `go version go1.27.1 linux/amd64`; SDK `v2.12.0-dev.3.0.20261002145613-96aedb31048c`; Orchestrion `v1.13.2-0.20260917114356-5c24783fcd76`.

Values are medians in seconds. Orchestrion's percentage compares time against Native. POC percentages compare Orchestrion first, then Native; all use unrounded medians.

All variants compile with `go test -c -o <directory>/ -ldflags=-w ./...`. Test binaries are never run. The selected variants use the same prepared source and module graph. Orchestrion loads only the pinned SDK's testing aspects.

Builds run serially in rotating order. Each cold run has an empty Go build cache and no existing output. Downloads are disabled during timing; modules and OS page cache stay warm. Affinity, `GOMAXPROCS` and `-p` match the selected CPU count; manifest.json records logical CPU IDs and physical core topology.

Unchanged output reuse, forced linking and reachable edits are checked with tool traces. Final binaries are checked for the expected testing/Testify hooks and absence of DWARF. CPU and memory include daemons and nested builds through exclusive cgroups. Wall time ends at the top-level command's exit; drain wait is recorded separately.

52 completed build commands, 6 qualified binaries. Native control repetitions: 0. A skipped control in a smoke run provides no stability evidence.

[Every observation](observations.csv), [ranges and uncertainty](statistics.json), [inputs](manifest.json) and [binary qualification](final-binaries.json) remain alongside this report. No slow samples are discarded. Fixture results are not a general application performance claim.

## Cold compilation

| Project | Flags | CPUs | POC Mini |
| --- | --- | ---: | ---: |
| Gin | `none` | 4 | 21.230 s |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | POC Mini |
| --- | --- | ---: | ---: |
| Gin | `none` | 4 | 0.197 s |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | POC Mini |
| --- | --- | ---: | ---: |
| Gin | `none` | 4 | 1.845 s |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | POC Mini |
| --- | --- | ---: | ---: |
| Gin | `none` | 4 | 1.505 s |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | POC Mini |
| --- | --- | ---: | ---: |
| Gin | `none` | 4 | 0.955 s |


