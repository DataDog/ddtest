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

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 17.896 s | 51.532 s | 27.986 s (-45.7%; +56.4%) | 19.590 s (-62.0%; +9.5%) |
| Gin | `none` | 32 | 10.844 s | 22.679 s | 14.805 s (-34.7%; +36.5%) | 11.836 s (-47.8%; +9.1%) |
| Chi | `none` | 4 | 7.764 s | 30.543 s | 18.221 s (-40.3%; +134.7%) | 9.510 s (-68.9%; +22.5%) |
| Chi | `none` | 32 | 4.576 s | 16.765 s | 9.929 s (-40.8%; +117.0%) | 5.961 s (-64.4%; +30.3%) |
| Gin | `-race` | 4 | 27.873 s | 56.274 s | 37.758 s (-32.9%; +35.5%) | 28.424 s (-49.5%; +2.0%) |
| Gin | `-race` | 32 | 23.244 s | 34.631 s | 26.873 s (-22.4%; +15.6%) | 23.433 s (-32.3%; +0.8%) |
| Gin | `-cover` | 4 | 18.781 s | 66.330 s | 27.765 s (-58.1%; +47.8%) | 19.819 s (-70.1%; +5.5%) |
| Gin | `-cover` | 32 | 10.933 s | 29.664 s | 15.498 s (-47.8%; +41.8%) | 11.718 s (-60.5%; +7.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 19.328 s | 56.685 s | 28.551 s (-49.6%; +47.7%) | 19.847 s (-65.0%; +2.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 12.161 s | 27.508 s | 16.342 s (-40.6%; +34.4%) | 12.248 s (-55.5%; +0.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 29.271 s | 68.432 s | 37.648 s (-45.0%; +28.6%) | 29.979 s (-56.2%; +2.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 24.174 s | 39.394 s | 29.313 s (-25.6%; +21.3%) | 25.687 s (-34.8%; +6.3%) |
| Chi | `-race` | 4 | 11.393 s | 37.963 s | 24.185 s (-36.3%; +112.3%) | 14.343 s (-62.2%; +25.9%) |
| Chi | `-race` | 32 | 9.318 s | 27.565 s | 17.413 s (-36.8%; +86.9%) | 12.263 s (-55.5%; +31.6%) |
| Chi | `-cover` | 4 | 7.934 s | 38.145 s | 18.185 s (-52.3%; +129.2%) | 9.737 s (-74.5%; +22.7%) |
| Chi | `-cover` | 32 | 4.815 s | 19.639 s | 10.173 s (-48.2%; +111.3%) | 5.886 s (-70.0%; +22.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 8.179 s | 33.966 s | 18.313 s (-46.1%; +123.9%) | 9.720 s (-71.4%; +18.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 4.946 s | 17.755 s | 9.539 s (-46.3%; +92.9%) | 6.200 s (-65.1%; +25.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 11.745 s | 41.717 s | 23.718 s (-43.1%; +101.9%) | 14.485 s (-65.3%; +23.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.538 s | 33.637 s | 18.772 s (-44.2%; +96.8%) | 13.163 s (-60.9%; +38.0%) |
| Testify Direct | `none` | 4 | 8.078 s | 33.039 s | 18.006 s (-45.5%; +122.9%) | 9.998 s (-69.7%; +23.8%) |
| Testify Direct | `none` | 32 | 4.867 s | 20.067 s | 9.921 s (-50.6%; +103.8%) | 6.431 s (-68.0%; +32.1%) |
| Testify Direct | `-race` | 4 | 12.691 s | 46.494 s | 24.671 s (-46.9%; +94.4%) | 15.520 s (-66.6%; +22.3%) |
| Testify Direct | `-race` | 32 | 10.094 s | 33.293 s | 18.526 s (-44.4%; +83.5%) | 13.703 s (-58.8%; +35.7%) |
| Testify Direct | `-cover` | 4 | 8.650 s | 39.414 s | 20.011 s (-49.2%; +131.3%) | 10.894 s (-72.4%; +25.9%) |
| Testify Direct | `-cover` | 32 | 5.337 s | 21.922 s | 12.525 s (-42.9%; +134.7%) | 6.979 s (-68.2%; +30.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 9.083 s | 34.215 s | 19.742 s (-42.3%; +117.3%) | 11.432 s (-66.6%; +25.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 5.248 s | 28.446 s | 13.568 s (-52.3%; +158.5%) | 6.624 s (-76.7%; +26.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 13.186 s | 53.793 s | 25.116 s (-53.3%; +90.5%) | 17.973 s (-66.6%; +36.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 10.073 s | 29.693 s | 22.056 s (-25.7%; +119.0%) | 13.780 s (-53.6%; +36.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.603 s | 30.824 s | 19.026 s (-38.3%; +121.2%) | 10.690 s (-65.3%; +24.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.913 s | 20.015 s | 10.170 s (-49.2%; +107.0%) | 6.222 s (-68.9%; +26.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.509 s | 39.152 s | 23.984 s (-38.7%; +91.7%) | 15.666 s (-60.0%; +25.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.825 s | 29.736 s | 17.250 s (-42.0%; +75.6%) | 12.382 s (-58.4%; +26.0%) |
| Testify External | `none` | 4 | 8.381 s | 30.092 s | 18.676 s (-37.9%; +122.8%) | 10.283 s (-65.8%; +22.7%) |
| Testify External | `none` | 32 | 4.810 s | 18.167 s | 11.453 s (-37.0%; +138.1%) | 6.241 s (-65.6%; +29.8%) |
| Testify External | `-race` | 4 | 12.450 s | 37.839 s | 23.904 s (-36.8%; +92.0%) | 15.307 s (-59.5%; +23.0%) |
| Testify External | `-race` | 32 | 9.726 s | 27.976 s | 17.143 s (-38.7%; +76.3%) | 12.289 s (-56.1%; +26.4%) |
| Testify External | `-cover` | 4 | 8.407 s | 34.683 s | 18.653 s (-46.2%; +121.9%) | 10.257 s (-70.4%; +22.0%) |
| Testify External | `-cover` | 32 | 4.861 s | 21.347 s | 11.746 s (-45.0%; +141.7%) | 6.214 s (-70.9%; +27.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 8.507 s | 31.696 s | 18.758 s (-40.8%; +120.5%) | 10.465 s (-67.0%; +23.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 5.078 s | 18.402 s | 9.973 s (-45.8%; +96.4%) | 6.625 s (-64.0%; +30.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 12.936 s | 40.899 s | 24.910 s (-39.1%; +92.6%) | 16.005 s (-60.9%; +23.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.861 s | 27.088 s | 17.312 s (-36.1%; +75.6%) | 12.856 s (-52.5%; +30.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.465 s | 29.977 s | 18.843 s (-37.1%; +122.6%) | 10.505 s (-65.0%; +24.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.869 s | 17.912 s | 9.837 s (-45.1%; +102.0%) | 6.392 s (-64.3%; +31.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.725 s | 38.705 s | 24.240 s (-37.4%; +90.5%) | 15.696 s (-59.4%; +23.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.847 s | 28.139 s | 17.414 s (-38.1%; +76.8%) | 12.543 s (-55.4%; +27.4%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.098 s | 0.366 s | 0.230 s (-37.3%; +134.3%) | 0.171 s (-53.2%; +74.6%) |
| Gin | `none` | 32 | 0.097 s | 0.410 s | 0.241 s (-41.1%; +148.5%) | 0.189 s (-53.9%; +94.7%) |
| Chi | `none` | 4 | 0.063 s | 0.329 s | 0.187 s (-43.0%; +197.4%) | 0.123 s (-62.5%; +95.3%) |
| Chi | `none` | 32 | 0.067 s | 0.385 s | 0.197 s (-48.8%; +194.8%) | 0.138 s (-64.1%; +106.8%) |
| Gin | `-race` | 4 | 0.098 s | 0.370 s | 0.232 s (-37.3%; +137.7%) | 0.172 s (-53.5%; +76.2%) |
| Gin | `-race` | 32 | 0.098 s | 0.413 s | 0.241 s (-41.7%; +147.1%) | 0.188 s (-54.4%; +93.0%) |
| Gin | `-cover` | 4 | 0.127 s | 0.514 s | 0.262 s (-49.0%; +106.6%) | 0.202 s (-60.7%; +59.4%) |
| Gin | `-cover` | 32 | 0.125 s | 0.570 s | 0.262 s (-54.0%; +110.5%) | 0.212 s (-62.8%; +70.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.130 s | 0.432 s | 0.271 s (-37.3%; +108.1%) | 0.206 s (-52.3%; +58.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.464 s | 0.273 s (-41.2%; +114.4%) | 0.214 s (-53.8%; +68.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.144 s | 0.466 s | 0.275 s (-41.1%; +91.1%) | 0.219 s (-53.0%; +52.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.471 s | 0.272 s (-42.2%; +114.3%) | 0.213 s (-54.8%; +67.6%) |
| Chi | `-race` | 4 | 0.062 s | 0.327 s | 0.188 s (-42.4%; +204.8%) | 0.122 s (-62.7%; +97.2%) |
| Chi | `-race` | 32 | 0.067 s | 0.387 s | 0.199 s (-48.6%; +195.9%) | 0.141 s (-63.7%; +109.1%) |
| Chi | `-cover` | 4 | 0.066 s | 0.419 s | 0.191 s (-54.4%; +190.7%) | 0.126 s (-70.1%; +90.7%) |
| Chi | `-cover` | 32 | 0.070 s | 0.494 s | 0.197 s (-60.2%; +181.1%) | 0.140 s (-71.7%; +100.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.371 s | 0.198 s (-46.5%; +201.2%) | 0.126 s (-65.9%; +91.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.071 s | 0.430 s | 0.201 s (-53.2%; +181.7%) | 0.139 s (-67.7%; +94.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.067 s | 0.370 s | 0.198 s (-46.4%; +195.8%) | 0.128 s (-65.4%; +90.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.072 s | 0.432 s | 0.206 s (-52.3%; +187.9%) | 0.141 s (-67.5%; +96.5%) |
| Testify Direct | `none` | 4 | 0.059 s | 0.314 s | 0.179 s (-43.0%; +206.0%) | 0.119 s (-62.1%; +103.5%) |
| Testify Direct | `none` | 32 | 0.061 s | 0.358 s | 0.188 s (-47.4%; +209.6%) | 0.132 s (-63.1%; +117.3%) |
| Testify Direct | `-race` | 4 | 0.056 s | 0.314 s | 0.183 s (-41.7%; +224.5%) | 0.118 s (-62.3%; +109.7%) |
| Testify Direct | `-race` | 32 | 0.063 s | 0.388 s | 0.202 s (-48.1%; +221.9%) | 0.141 s (-63.6%; +125.7%) |
| Testify Direct | `-cover` | 4 | 0.060 s | 0.385 s | 0.184 s (-52.1%; +205.5%) | 0.121 s (-68.7%; +100.0%) |
| Testify Direct | `-cover` | 32 | 0.068 s | 0.472 s | 0.206 s (-56.3%; +205.2%) | 0.144 s (-69.4%; +113.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.365 s | 0.206 s (-43.6%; +211.2%) | 0.134 s (-63.3%; +102.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.067 s | 0.415 s | 0.212 s (-49.0%; +214.3%) | 0.144 s (-65.4%; +113.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.065 s | 0.365 s | 0.205 s (-43.9%; +214.2%) | 0.131 s (-64.1%; +101.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.064 s | 0.386 s | 0.198 s (-48.8%; +210.9%) | 0.137 s (-64.4%; +116.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.322 s | 0.198 s (-38.3%; +209.1%) | 0.128 s (-60.1%; +99.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.366 s | 0.203 s (-44.6%; +215.1%) | 0.138 s (-62.4%; +113.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.319 s | 0.199 s (-37.7%; +216.2%) | 0.126 s (-60.6%; +100.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.066 s | 0.366 s | 0.203 s (-44.6%; +209.5%) | 0.138 s (-62.4%; +110.0%) |
| Testify External | `none` | 4 | 0.059 s | 0.314 s | 0.182 s (-42.2%; +208.9%) | 0.122 s (-61.2%; +107.1%) |
| Testify External | `none` | 32 | 0.059 s | 0.358 s | 0.188 s (-47.3%; +217.2%) | 0.132 s (-63.2%; +121.7%) |
| Testify External | `-race` | 4 | 0.058 s | 0.312 s | 0.181 s (-42.0%; +212.6%) | 0.118 s (-62.1%; +104.2%) |
| Testify External | `-race` | 32 | 0.062 s | 0.354 s | 0.192 s (-45.9%; +211.1%) | 0.134 s (-62.3%; +116.7%) |
| Testify External | `-cover` | 4 | 0.062 s | 0.376 s | 0.184 s (-51.2%; +196.7%) | 0.122 s (-67.7%; +96.2%) |
| Testify External | `-cover` | 32 | 0.063 s | 0.435 s | 0.188 s (-56.7%; +198.3%) | 0.133 s (-69.5%; +110.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.062 s | 0.335 s | 0.191 s (-42.9%; +206.9%) | 0.125 s (-62.7%; +100.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.063 s | 0.394 s | 0.196 s (-50.4%; +210.4%) | 0.135 s (-65.8%; +113.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.060 s | 0.341 s | 0.192 s (-43.7%; +218.6%) | 0.124 s (-63.6%; +105.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.066 s | 0.395 s | 0.199 s (-49.8%; +202.9%) | 0.137 s (-65.3%; +109.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.323 s | 0.198 s (-38.8%; +208.4%) | 0.129 s (-60.2%; +100.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.364 s | 0.201 s (-44.8%; +214.0%) | 0.138 s (-62.2%; +115.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.320 s | 0.199 s (-37.9%; +214.6%) | 0.125 s (-60.9%; +98.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.368 s | 0.204 s (-44.7%; +218.7%) | 0.138 s (-62.5%; +116.2%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.158 s | 5.302 s | 4.323 s (-18.5%; +273.3%) | 1.745 s (-67.1%; +50.7%) |
| Gin | `none` | 32 | 1.213 s | 5.372 s | 4.368 s (-18.7%; +260.0%) | 1.902 s (-64.6%; +56.7%) |
| Chi | `none` | 4 | 0.266 s | 1.810 s | 1.026 s (-43.3%; +285.8%) | 0.386 s (-78.6%; +45.3%) |
| Chi | `none` | 32 | 0.272 s | 1.759 s | 1.018 s (-42.1%; +274.4%) | 0.404 s (-77.0%; +48.5%) |
| Gin | `-race` | 4 | 1.987 s | 7.649 s | 6.565 s (-14.2%; +230.3%) | 2.494 s (-67.4%; +25.5%) |
| Gin | `-race` | 32 | 1.949 s | 8.048 s | 6.521 s (-19.0%; +234.6%) | 2.426 s (-69.9%; +24.5%) |
| Gin | `-cover` | 4 | 1.086 s | 5.733 s | 4.376 s (-23.7%; +303.0%) | 1.867 s (-67.4%; +71.9%) |
| Gin | `-cover` | 32 | 1.143 s | 6.131 s | 4.566 s (-25.5%; +299.3%) | 1.778 s (-71.0%; +55.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.245 s | 5.492 s | 4.267 s (-22.3%; +242.6%) | 2.097 s (-61.8%; +68.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.281 s | 5.750 s | 4.627 s (-19.5%; +261.3%) | 1.515 s (-73.7%; +18.3%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.012 s | 8.356 s | 6.328 s (-24.3%; +214.5%) | 2.597 s (-68.9%; +29.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.258 s | 8.331 s | 7.902 s (-5.2%; +249.9%) | 2.228 s (-73.3%; -1.4%) |
| Chi | `-race` | 4 | 0.361 s | 2.053 s | 1.250 s (-39.1%; +246.5%) | 0.573 s (-72.1%; +58.9%) |
| Chi | `-race` | 32 | 0.365 s | 2.021 s | 1.243 s (-38.5%; +240.4%) | 0.660 s (-67.3%; +80.8%) |
| Chi | `-cover` | 4 | 0.290 s | 2.012 s | 1.003 s (-50.2%; +245.4%) | 0.401 s (-80.1%; +38.0%) |
| Chi | `-cover` | 32 | 0.285 s | 2.097 s | 1.054 s (-49.8%; +269.8%) | 0.420 s (-80.0%; +47.5%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.287 s | 2.012 s | 1.093 s (-45.7%; +280.1%) | 0.405 s (-79.8%; +41.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.977 s | 1.082 s (-45.3%; +272.3%) | 0.421 s (-78.7%; +44.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.384 s | 2.132 s | 1.360 s (-36.2%; +253.7%) | 0.710 s (-66.7%; +84.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.424 s | 2.296 s | 1.602 s (-30.2%; +278.1%) | 0.789 s (-65.7%; +86.1%) |
| Testify Direct | `none` | 4 | 0.246 s | 1.622 s | 0.861 s (-46.9%; +250.5%) | 0.374 s (-77.0%; +52.0%) |
| Testify Direct | `none` | 32 | 0.270 s | 1.795 s | 0.883 s (-50.8%; +226.6%) | 0.394 s (-78.1%; +45.5%) |
| Testify Direct | `-race` | 4 | 0.335 s | 1.839 s | 1.100 s (-40.2%; +228.6%) | 0.512 s (-72.2%; +53.0%) |
| Testify Direct | `-race` | 32 | 0.381 s | 2.030 s | 1.158 s (-43.0%; +203.7%) | 0.615 s (-69.7%; +61.4%) |
| Testify Direct | `-cover` | 4 | 0.271 s | 1.895 s | 0.955 s (-49.6%; +252.7%) | 0.408 s (-78.5%; +50.5%) |
| Testify Direct | `-cover` | 32 | 0.291 s | 1.989 s | 0.964 s (-51.5%; +230.7%) | 0.445 s (-77.6%; +52.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.304 s | 1.903 s | 0.952 s (-50.0%; +213.5%) | 0.420 s (-77.9%; +38.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.965 s | 1.017 s (-48.2%; +249.9%) | 0.445 s (-77.4%; +53.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.367 s | 1.929 s | 1.152 s (-40.3%; +213.4%) | 0.578 s (-70.1%; +57.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.358 s | 2.024 s | 1.100 s (-45.7%; +207.4%) | 0.571 s (-71.8%; +59.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.259 s | 1.753 s | 1.023 s (-41.6%; +295.7%) | 0.385 s (-78.0%; +48.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.264 s | 1.968 s | 1.038 s (-47.2%; +292.9%) | 0.410 s (-79.2%; +55.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.789 s | 1.034 s (-42.2%; +204.4%) | 0.574 s (-67.9%; +68.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.359 s | 2.022 s | 1.102 s (-45.5%; +207.0%) | 0.559 s (-72.3%; +55.7%) |
| Testify External | `none` | 4 | 0.239 s | 1.716 s | 0.968 s (-43.6%; +305.2%) | 0.374 s (-78.2%; +56.7%) |
| Testify External | `none` | 32 | 0.256 s | 1.867 s | 0.995 s (-46.7%; +288.7%) | 0.393 s (-79.0%; +53.4%) |
| Testify External | `-race` | 4 | 0.328 s | 1.821 s | 1.019 s (-44.1%; +211.1%) | 0.519 s (-71.5%; +58.3%) |
| Testify External | `-race` | 32 | 0.341 s | 1.952 s | 1.069 s (-45.2%; +213.8%) | 0.542 s (-72.2%; +59.1%) |
| Testify External | `-cover` | 4 | 0.248 s | 1.696 s | 0.969 s (-42.9%; +289.9%) | 0.375 s (-77.9%; +51.0%) |
| Testify External | `-cover` | 32 | 0.262 s | 1.960 s | 1.007 s (-48.6%; +283.9%) | 0.404 s (-79.4%; +53.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.257 s | 1.700 s | 0.977 s (-42.5%; +280.6%) | 0.382 s (-77.5%; +48.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.270 s | 1.956 s | 1.045 s (-46.6%; +287.9%) | 0.411 s (-79.0%; +52.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.345 s | 1.920 s | 1.098 s (-42.8%; +218.6%) | 0.532 s (-72.3%; +54.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.352 s | 2.055 s | 1.118 s (-45.6%; +218.0%) | 0.556 s (-72.9%; +58.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.258 s | 1.762 s | 0.996 s (-43.5%; +286.6%) | 0.383 s (-78.3%; +48.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.270 s | 1.895 s | 1.029 s (-45.7%; +281.2%) | 0.411 s (-78.3%; +52.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.840 s | 1.058 s (-42.5%; +211.4%) | 0.544 s (-70.4%; +60.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.358 s | 2.009 s | 1.108 s (-44.8%; +209.7%) | 0.551 s (-72.6%; +54.0%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.280 s | 2.810 s | 2.219 s (-21.0%; +73.4%) | 1.400 s (-50.2%; +9.4%) |
| Gin | `none` | 32 | 1.160 s | 2.871 s | 2.054 s (-28.5%; +77.1%) | 1.241 s (-56.8%; +7.0%) |
| Chi | `none` | 4 | 0.413 s | 1.794 s | 0.979 s (-45.4%; +137.4%) | 0.523 s (-70.8%; +26.9%) |
| Chi | `none` | 32 | 0.409 s | 1.901 s | 1.072 s (-43.6%; +161.9%) | 0.544 s (-71.4%; +32.8%) |
| Gin | `-race` | 4 | 2.120 s | 3.873 s | 3.153 s (-18.6%; +48.7%) | 2.302 s (-40.6%; +8.6%) |
| Gin | `-race` | 32 | 2.182 s | 4.379 s | 3.140 s (-28.3%; +43.9%) | 2.383 s (-45.6%; +9.2%) |
| Gin | `-cover` | 4 | 1.440 s | 3.115 s | 2.358 s (-24.3%; +63.8%) | 1.602 s (-48.6%; +11.3%) |
| Gin | `-cover` | 32 | 1.338 s | 3.272 s | 2.172 s (-33.6%; +62.4%) | 1.459 s (-55.4%; +9.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.533 s | 3.126 s | 2.338 s (-25.2%; +52.5%) | 1.654 s (-47.1%; +7.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.351 s | 3.375 s | 2.443 s (-27.6%; +80.8%) | 1.510 s (-55.2%; +11.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.451 s | 4.147 s | 3.284 s (-20.8%; +34.0%) | 2.594 s (-37.4%; +5.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.473 s | 4.708 s | 3.418 s (-27.4%; +38.2%) | 2.724 s (-42.1%; +10.1%) |
| Chi | `-race` | 4 | 0.723 s | 2.295 s | 1.371 s (-40.3%; +89.6%) | 0.853 s (-62.8%; +18.0%) |
| Chi | `-race` | 32 | 0.750 s | 2.399 s | 1.401 s (-41.6%; +86.9%) | 0.895 s (-62.7%; +19.4%) |
| Chi | `-cover` | 4 | 0.475 s | 2.002 s | 1.021 s (-49.0%; +115.0%) | 0.570 s (-71.5%; +20.1%) |
| Chi | `-cover` | 32 | 0.465 s | 1.910 s | 1.081 s (-43.4%; +132.5%) | 0.582 s (-69.5%; +25.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.479 s | 1.961 s | 1.102 s (-43.8%; +130.0%) | 0.583 s (-70.3%; +21.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.487 s | 1.974 s | 1.184 s (-40.0%; +143.0%) | 0.601 s (-69.5%; +23.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.818 s | 2.226 s | 1.743 s (-21.7%; +113.2%) | 0.949 s (-57.4%; +16.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.851 s | 2.501 s | 1.621 s (-35.2%; +90.6%) | 0.993 s (-60.3%; +16.7%) |
| Testify Direct | `none` | 4 | 0.285 s | 2.451 s | 1.059 s (-56.8%; +271.9%) | 0.413 s (-83.2%; +44.8%) |
| Testify Direct | `none` | 32 | 0.287 s | 1.882 s | 1.045 s (-44.5%; +263.7%) | 0.436 s (-76.8%; +51.7%) |
| Testify Direct | `-race` | 4 | 0.447 s | 2.297 s | 1.531 s (-33.3%; +242.3%) | 0.651 s (-71.7%; +45.5%) |
| Testify Direct | `-race` | 32 | 0.468 s | 2.876 s | 1.390 s (-51.7%; +197.2%) | 0.660 s (-77.1%; +41.1%) |
| Testify Direct | `-cover` | 4 | 0.332 s | 2.391 s | 1.153 s (-51.8%; +247.8%) | 0.459 s (-80.8%; +38.4%) |
| Testify Direct | `-cover` | 32 | 0.336 s | 2.099 s | 1.113 s (-47.0%; +230.8%) | 0.493 s (-76.5%; +46.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 2.076 s | 1.216 s (-41.4%; +272.6%) | 0.477 s (-77.0%; +46.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.339 s | 2.229 s | 1.283 s (-42.4%; +279.1%) | 0.499 s (-77.6%; +47.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.462 s | 2.537 s | 1.675 s (-34.0%; +262.7%) | 0.652 s (-74.3%; +41.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.437 s | 2.183 s | 1.223 s (-44.0%; +179.7%) | 0.641 s (-70.6%; +46.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.286 s | 1.915 s | 1.036 s (-45.9%; +261.7%) | 0.420 s (-78.1%; +46.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.303 s | 2.036 s | 1.186 s (-41.8%; +291.0%) | 0.440 s (-78.4%; +45.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.423 s | 2.010 s | 1.113 s (-44.6%; +162.8%) | 0.599 s (-70.2%; +41.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.449 s | 2.246 s | 1.350 s (-39.9%; +200.6%) | 0.647 s (-71.2%; +44.0%) |
| Testify External | `none` | 4 | 0.253 s | 1.742 s | 0.969 s (-44.4%; +283.7%) | 0.373 s (-78.6%; +47.8%) |
| Testify External | `none` | 32 | 0.264 s | 1.933 s | 0.999 s (-48.3%; +278.9%) | 0.406 s (-79.0%; +53.8%) |
| Testify External | `-race` | 4 | 0.340 s | 1.838 s | 1.050 s (-42.9%; +208.9%) | 0.499 s (-72.8%; +46.8%) |
| Testify External | `-race` | 32 | 0.360 s | 2.089 s | 1.106 s (-47.0%; +207.7%) | 0.534 s (-74.4%; +48.5%) |
| Testify External | `-cover` | 4 | 0.267 s | 1.868 s | 0.967 s (-48.2%; +261.6%) | 0.393 s (-78.9%; +47.0%) |
| Testify External | `-cover` | 32 | 0.275 s | 1.972 s | 1.009 s (-48.8%; +266.4%) | 0.424 s (-78.5%; +53.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.280 s | 1.884 s | 0.940 s (-50.1%; +235.7%) | 0.424 s (-77.5%; +51.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.286 s | 2.043 s | 1.055 s (-48.4%; +269.1%) | 0.422 s (-79.4%; +47.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.354 s | 1.992 s | 1.128 s (-43.3%; +219.2%) | 0.548 s (-72.5%; +55.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.383 s | 2.090 s | 1.161 s (-44.5%; +202.9%) | 0.591 s (-71.7%; +54.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.274 s | 1.811 s | 0.996 s (-45.0%; +263.0%) | 0.404 s (-77.7%; +47.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.285 s | 1.993 s | 1.057 s (-47.0%; +270.3%) | 0.431 s (-78.4%; +50.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.354 s | 1.928 s | 1.104 s (-42.7%; +211.5%) | 0.681 s (-64.7%; +92.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.389 s | 2.126 s | 1.172 s (-44.9%; +201.7%) | 0.600 s (-71.8%; +54.5%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.760 s | 1.161 s | 0.875 s (-24.7%; +15.1%) | 0.822 s (-29.2%; +8.2%) |
| Gin | `none` | 32 | 0.651 s | 1.110 s | 0.804 s (-27.6%; +23.5%) | 0.737 s (-33.6%; +13.1%) |
| Chi | `none` | 4 | 0.266 s | 0.657 s | 0.376 s (-42.8%; +41.5%) | 0.327 s (-50.2%; +23.2%) |
| Chi | `none` | 32 | 0.250 s | 0.682 s | 0.402 s (-40.9%; +61.3%) | 0.328 s (-51.9%; +31.4%) |
| Gin | `-race` | 4 | 1.368 s | 1.769 s | 1.485 s (-16.0%; +8.5%) | 1.443 s (-18.4%; +5.4%) |
| Gin | `-race` | 32 | 1.411 s | 1.821 s | 1.524 s (-16.3%; +8.0%) | 1.467 s (-19.5%; +3.9%) |
| Gin | `-cover` | 4 | 0.944 s | 1.579 s | 1.052 s (-33.4%; +11.4%) | 1.012 s (-35.9%; +7.2%) |
| Gin | `-cover` | 32 | 0.823 s | 1.501 s | 0.946 s (-37.0%; +14.9%) | 0.925 s (-38.3%; +12.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.982 s | 1.581 s | 1.096 s (-30.7%; +11.6%) | 1.056 s (-33.2%; +7.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.868 s | 1.467 s | 1.019 s (-30.6%; +17.3%) | 0.951 s (-35.1%; +9.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.665 s | 2.253 s | 1.791 s (-20.5%; +7.6%) | 1.739 s (-22.8%; +4.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.667 s | 2.338 s | 1.854 s (-20.7%; +11.2%) | 1.746 s (-25.3%; +4.8%) |
| Chi | `-race` | 4 | 0.491 s | 0.872 s | 0.599 s (-31.3%; +22.0%) | 0.546 s (-37.4%; +11.2%) |
| Chi | `-race` | 32 | 0.520 s | 0.939 s | 0.646 s (-31.2%; +24.1%) | 0.583 s (-37.9%; +12.0%) |
| Chi | `-cover` | 4 | 0.315 s | 0.795 s | 0.429 s (-46.0%; +36.3%) | 0.397 s (-50.0%; +26.0%) |
| Chi | `-cover` | 32 | 0.299 s | 0.799 s | 0.447 s (-44.1%; +49.3%) | 0.374 s (-53.2%; +25.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 0.774 s | 0.444 s (-42.6%; +36.2%) | 0.381 s (-50.8%; +16.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.331 s | 0.796 s | 0.470 s (-40.9%; +42.1%) | 0.401 s (-49.6%; +21.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.572 s | 0.996 s | 0.704 s (-29.3%; +23.1%) | 0.629 s (-36.8%; +10.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.595 s | 2.099 s | 0.726 s (-65.4%; +21.9%) | 0.664 s (-68.4%; +11.5%) |
| Testify Direct | `none` | 4 | 0.102 s | 0.656 s | 0.237 s (-64.0%; +131.7%) | 0.171 s (-73.9%; +67.6%) |
| Testify Direct | `none` | 32 | 0.099 s | 1.002 s | 0.223 s (-77.8%; +125.5%) | 0.174 s (-82.6%; +76.3%) |
| Testify Direct | `-race` | 4 | 0.154 s | 0.669 s | 0.270 s (-59.6%; +75.6%) | 0.232 s (-65.3%; +51.0%) |
| Testify Direct | `-race` | 32 | 0.167 s | 0.736 s | 0.300 s (-59.2%; +79.4%) | 0.246 s (-66.5%; +47.4%) |
| Testify Direct | `-cover` | 4 | 0.121 s | 0.698 s | 0.245 s (-65.0%; +102.4%) | 0.190 s (-72.8%; +57.1%) |
| Testify Direct | `-cover` | 32 | 0.119 s | 0.775 s | 0.256 s (-66.9%; +116.2%) | 0.199 s (-74.3%; +67.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.124 s | 1.310 s | 0.257 s (-80.4%; +107.5%) | 0.190 s (-85.5%; +53.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.120 s | 0.728 s | 0.265 s (-63.6%; +121.5%) | 0.204 s (-72.0%; +69.9%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.164 s | 0.776 s | 0.299 s (-61.5%; +82.2%) | 0.234 s (-69.9%; +42.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.161 s | 0.725 s | 0.299 s (-58.8%; +85.0%) | 0.238 s (-67.1%; +47.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.107 s | 0.589 s | 0.232 s (-60.7%; +116.4%) | 0.172 s (-70.7%; +60.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.105 s | 0.650 s | 0.240 s (-63.1%; +128.3%) | 0.184 s (-71.7%; +75.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.150 s | 0.619 s | 0.274 s (-55.8%; +82.3%) | 0.214 s (-65.3%; +42.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.158 s | 0.706 s | 0.297 s (-57.9%; +88.3%) | 0.236 s (-66.5%; +49.9%) |
| Testify External | `none` | 4 | 0.078 s | 0.546 s | 0.192 s (-64.8%; +146.1%) | 0.137 s (-74.8%; +75.7%) |
| Testify External | `none` | 32 | 0.080 s | 0.614 s | 0.202 s (-67.1%; +152.3%) | 0.151 s (-75.4%; +89.1%) |
| Testify External | `-race` | 4 | 0.078 s | 0.549 s | 0.194 s (-64.7%; +149.2%) | 0.141 s (-74.3%; +81.5%) |
| Testify External | `-race` | 32 | 0.081 s | 0.611 s | 0.204 s (-66.6%; +151.7%) | 0.154 s (-74.8%; +90.1%) |
| Testify External | `-cover` | 4 | 0.081 s | 0.624 s | 0.198 s (-68.3%; +143.1%) | 0.145 s (-76.8%; +78.3%) |
| Testify External | `-cover` | 32 | 0.086 s | 1.060 s | 0.217 s (-79.6%; +152.7%) | 0.159 s (-85.0%; +84.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.086 s | 0.573 s | 0.203 s (-64.5%; +137.0%) | 0.150 s (-73.8%; +75.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.088 s | 0.653 s | 0.221 s (-66.2%; +151.6%) | 0.161 s (-75.3%; +83.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.085 s | 0.592 s | 0.212 s (-64.1%; +149.7%) | 0.150 s (-74.7%; +76.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.089 s | 0.651 s | 0.227 s (-65.1%; +155.2%) | 0.164 s (-74.9%; +83.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.082 s | 0.554 s | 0.209 s (-62.4%; +153.1%) | 0.150 s (-72.9%; +82.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.084 s | 0.630 s | 0.230 s (-63.4%; +174.6%) | 0.156 s (-75.2%; +86.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.084 s | 0.772 s | 0.213 s (-72.4%; +154.1%) | 0.147 s (-81.0%; +75.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.086 s | 0.623 s | 0.219 s (-64.9%; +154.1%) | 0.160 s (-74.3%; +85.8%) |


