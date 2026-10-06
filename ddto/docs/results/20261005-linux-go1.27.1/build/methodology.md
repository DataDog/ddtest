# Compile-only comparison

The four variants compile the same source and module graph with
`go test -c -o <directory>/ -ldflags=-w ./...`. No test executable runs during
these measurements. Orchestrion loads only the SDK's testing aspects.

| Input | Recorded value |
| --- | --- |
| POC source | `9d4786fbd27f573bb68c5516bf23b336e8d7bdaa` |
| Go | `go version go1.27.1 linux/amd64` |
| SDK | `v2.12.0-dev.3.0.20261002145613-96aedb31048c` |
| Orchestrion | `v1.13.2-0.20260917114356-5c24783fcd76` |

Times are medians in seconds. POC percentages show the signed change against
total Orchestrion time first, then Native time, calculated from unrounded values.
Builds run serially with rotating variant order. Affinity, `GOMAXPROCS` and `-p`
match the CPU column; the manifest records CPU IDs and physical-core topology.

Every cold run uses an empty Go build cache and removed output. Downloads are
disabled during timing; the module cache and OS page cache stay warm. Tool traces
verify unchanged output reuse, forced links and reachable edits. Binary checks
verify the testing/Testify hooks and absence of DWARF.

The dataset contains 7,153 completed commands and 432 qualified binaries.
The Native link control has 160 repetitions. CPU and memory are measured in
exclusive cgroups, including daemons and nested builds. Wall time ends when the
top-level command exits; process-tree drain time is recorded separately.

[Every command](observations.csv), [ranges and uncertainty](statistics.json),
[inputs](manifest.json) and [binary qualification](final-binaries.json) are kept
with this report. All observations contribute according to their scenario;
no slow samples are discarded. These results apply to the recorded fixtures.
