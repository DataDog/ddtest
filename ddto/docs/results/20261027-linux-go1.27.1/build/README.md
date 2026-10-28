# Compile-only measurements: Native, Orchestrion, `ddtest instrument`

POC `282b22a15776c3dfb1073d2e0197c78445c00d6f`; `go version go1.27.1 linux/amd64`; SDK `v2.12.0-dev.3.0.20261008222249-870449702d0a`; Orchestrion `v1.13.2-0.20260917114356-5c24783fcd76`.

Values are medians in seconds. Orchestrion's percentage compares time against Native. POC percentages compare Orchestrion first, then Native; all use unrounded medians.

All variants compile with `go test -c -o <directory>/ -ldflags=-w ./...`. Test binaries are never run. The selected variants use the same prepared source and module graph. Orchestrion loads only the pinned SDK's testing aspects.

Builds run serially in rotating order. Each cold run has an empty Go build cache and no existing output. Downloads are disabled during timing; modules and OS page cache stay warm. Affinity, `GOMAXPROCS` and `-p` match the selected CPU count; manifest.json records logical CPU IDs and physical core topology.

Unchanged output reuse, forced linking and reachable edits are checked with tool traces. Final binaries are checked for the expected testing/Testify hooks and absence of DWARF. CPU and memory include daemons and nested builds through exclusive cgroups. Wall time ends at the top-level command's exit; drain wait is recorded separately.

2817 completed build commands, 324 qualified binaries. Native control repetitions: 80. A skipped control in a smoke run provides no stability evidence.

[Every observation](observations.csv), [ranges and uncertainty](statistics.json), [inputs](manifest.json) and [binary qualification](final-binaries.json) remain alongside this report. No slow samples are discarded. Fixture results are not a general application performance claim.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 21.355 s | 57.543 s (+169.5%) | 22.531 s (-60.8%; +5.5%) |
| Gin | `none` | 32 | 13.404 s | 28.275 s (+110.9%) | 15.274 s (-46.0%; +13.9%) |
| Chi | `none` | 4 | 8.592 s | 33.667 s (+291.8%) | 11.089 s (-67.1%; +29.1%) |
| Chi | `none` | 32 | 5.560 s | 19.359 s (+248.2%) | 6.935 s (-64.2%; +24.7%) |
| Gin | `-race` | 4 | 28.602 s | 58.021 s (+102.9%) | 29.737 s (-48.7%; +4.0%) |
| Gin | `-race` | 32 | 23.887 s | 37.110 s (+55.4%) | 24.393 s (-34.3%; +2.1%) |
| Gin | `-cover` | 4 | 20.115 s | 71.361 s (+254.8%) | 21.690 s (-69.6%; +7.8%) |
| Gin | `-cover` | 32 | 12.265 s | 32.135 s (+162.0%) | 12.662 s (-60.6%; +3.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 20.459 s | 66.986 s (+227.4%) | 23.658 s (-64.7%; +15.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 13.376 s | 32.159 s (+140.4%) | 12.313 s (-61.7%; -8.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 30.283 s | 69.112 s (+128.2%) | 31.223 s (-54.8%; +3.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 24.664 s | 38.960 s (+58.0%) | 25.269 s (-35.1%; +2.5%) |
| Chi | `-race` | 4 | 12.078 s | 40.168 s (+232.6%) | 15.522 s (-61.4%; +28.5%) |
| Chi | `-race` | 32 | 9.751 s | 28.746 s (+194.8%) | 13.154 s (-54.2%; +34.9%) |
| Chi | `-cover` | 4 | 8.800 s | 40.602 s (+361.4%) | 11.284 s (-72.2%; +28.2%) |
| Chi | `-cover` | 32 | 5.276 s | 21.799 s (+313.2%) | 7.043 s (-67.7%; +33.5%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 8.808 s | 34.974 s (+297.1%) | 11.604 s (-66.8%; +31.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 5.424 s | 25.040 s (+361.7%) | 7.079 s (-71.7%; +30.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 12.415 s | 45.274 s (+264.7%) | 15.879 s (-64.9%; +27.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 10.090 s | 30.678 s (+204.1%) | 13.408 s (-56.3%; +32.9%) |
| Testify Direct | `none` | 4 | 8.895 s | 31.245 s (+251.3%) | 11.212 s (-64.1%; +26.0%) |
| Testify Direct | `none` | 32 | 5.174 s | 21.245 s (+310.6%) | 7.290 s (-65.7%; +40.9%) |
| Testify Direct | `-race` | 4 | 13.146 s | 42.255 s (+221.4%) | 15.796 s (-62.6%; +20.2%) |
| Testify Direct | `-race` | 32 | 10.069 s | 31.659 s (+214.4%) | 13.032 s (-58.8%; +29.4%) |
| Testify Direct | `-cover` | 4 | 9.051 s | 38.125 s (+321.2%) | 11.280 s (-70.4%; +24.6%) |
| Testify Direct | `-cover` | 32 | 5.243 s | 23.469 s (+347.6%) | 7.880 s (-66.4%; +50.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 9.055 s | 34.842 s (+284.8%) | 11.355 s (-67.4%; +25.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 5.534 s | 21.131 s (+281.8%) | 7.328 s (-65.3%; +32.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 13.226 s | 43.645 s (+230.0%) | 16.155 s (-63.0%; +22.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 12.513 s | 35.718 s (+185.4%) | 14.045 s (-60.7%; +12.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 9.143 s | 38.089 s (+316.6%) | 11.496 s (-69.8%; +25.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 6.030 s | 23.741 s (+293.7%) | 7.071 s (-70.2%; +17.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 13.287 s | 43.772 s (+229.4%) | 15.834 s (-63.8%; +19.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 10.489 s | 31.485 s (+200.2%) | 13.523 s (-57.0%; +28.9%) |
| Testify External | `none` | 4 | 8.940 s | 33.706 s (+277.0%) | 11.503 s (-65.9%; +28.7%) |
| Testify External | `none` | 32 | 5.962 s | 23.816 s (+299.4%) | 7.061 s (-70.4%; +18.4%) |
| Testify External | `-race` | 4 | 13.094 s | 43.068 s (+228.9%) | 16.150 s (-62.5%; +23.3%) |
| Testify External | `-race` | 32 | 10.076 s | 28.796 s (+185.8%) | 13.148 s (-54.3%; +30.5%) |
| Testify External | `-cover` | 4 | 9.112 s | 39.397 s (+332.4%) | 11.594 s (-70.6%; +27.2%) |
| Testify External | `-cover` | 32 | 5.497 s | 22.443 s (+308.3%) | 7.082 s (-68.4%; +28.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 9.093 s | 33.825 s (+272.0%) | 11.647 s (-65.6%; +28.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 5.587 s | 23.843 s (+326.7%) | 7.035 s (-70.5%; +25.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 13.281 s | 44.374 s (+234.1%) | 16.957 s (-61.8%; +27.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 10.707 s | 33.961 s (+217.2%) | 13.016 s (-61.7%; +21.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 9.113 s | 38.050 s (+317.5%) | 11.676 s (-69.3%; +28.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 5.726 s | 25.056 s (+337.6%) | 7.066 s (-71.8%; +23.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 13.310 s | 42.887 s (+222.2%) | 16.444 s (-61.7%; +23.6%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 10.210 s | 34.746 s (+240.3%) | 13.308 s (-61.7%; +30.3%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.098 s | 0.374 s (+283.0%) | 0.184 s (-50.6%; +89.1%) |
| Gin | `none` | 32 | 0.102 s | 0.441 s (+330.5%) | 0.217 s (-50.8%; +111.9%) |
| Chi | `none` | 4 | 0.065 s | 0.352 s (+444.3%) | 0.128 s (-63.7%; +97.5%) |
| Chi | `none` | 32 | 0.066 s | 0.411 s (+524.7%) | 0.135 s (-67.2%; +105.0%) |
| Gin | `-race` | 4 | 0.097 s | 0.394 s (+306.1%) | 0.198 s (-49.8%; +103.8%) |
| Gin | `-race` | 32 | 0.102 s | 0.450 s (+339.1%) | 0.228 s (-49.2%; +122.8%) |
| Gin | `-cover` | 4 | 0.130 s | 0.548 s (+320.6%) | 0.227 s (-58.5%; +74.4%) |
| Gin | `-cover` | 32 | 0.130 s | 0.615 s (+374.5%) | 0.247 s (-59.8%; +90.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.137 s | 0.455 s (+233.1%) | 0.240 s (-47.3%; +75.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.132 s | 0.505 s (+282.7%) | 0.256 s (-49.4%; +93.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.131 s | 0.462 s (+253.9%) | 0.242 s (-47.6%; +85.3%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.132 s | 0.504 s (+282.0%) | 0.261 s (-48.3%; +97.5%) |
| Chi | `-race` | 4 | 0.064 s | 0.355 s (+455.1%) | 0.131 s (-63.1%; +105.0%) |
| Chi | `-race` | 32 | 0.072 s | 0.412 s (+470.2%) | 0.150 s (-63.7%; +107.1%) |
| Chi | `-cover` | 4 | 0.069 s | 0.450 s (+551.6%) | 0.130 s (-71.0%; +88.9%) |
| Chi | `-cover` | 32 | 0.075 s | 0.541 s (+620.8%) | 0.145 s (-73.2%; +93.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.070 s | 0.402 s (+471.5%) | 0.133 s (-67.0%; +88.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.075 s | 0.466 s (+519.7%) | 0.141 s (-69.7%; +87.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.067 s | 0.398 s (+498.0%) | 0.141 s (-64.5%; +112.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.075 s | 0.466 s (+523.5%) | 0.157 s (-66.2%; +110.7%) |
| Testify Direct | `none` | 4 | 0.063 s | 0.339 s (+439.0%) | 0.143 s (-57.9%; +127.2%) |
| Testify Direct | `none` | 32 | 0.062 s | 0.389 s (+529.2%) | 0.162 s (-58.3%; +162.4%) |
| Testify Direct | `-race` | 4 | 0.059 s | 0.337 s (+468.1%) | 0.155 s (-54.0%; +161.1%) |
| Testify Direct | `-race` | 32 | 0.062 s | 0.391 s (+528.7%) | 0.174 s (-55.5%; +179.8%) |
| Testify Direct | `-cover` | 4 | 0.064 s | 0.408 s (+538.8%) | 0.149 s (-63.5%; +133.3%) |
| Testify Direct | `-cover` | 32 | 0.068 s | 0.465 s (+580.0%) | 0.167 s (-64.2%; +143.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.370 s (+456.6%) | 0.153 s (-58.7%; +129.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.066 s | 0.409 s (+518.9%) | 0.169 s (-58.6%; +156.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.067 s | 0.369 s (+452.2%) | 0.166 s (-55.0%; +148.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.067 s | 0.415 s (+523.0%) | 0.182 s (-56.1%; +173.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.068 s | 0.354 s (+418.4%) | 0.154 s (-56.4%; +125.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.068 s | 0.393 s (+475.3%) | 0.162 s (-58.8%; +137.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.067 s | 0.350 s (+420.5%) | 0.167 s (-52.2%; +148.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.066 s | 0.398 s (+501.2%) | 0.179 s (-55.0%; +170.3%) |
| Testify External | `none` | 4 | 0.061 s | 0.347 s (+469.3%) | 0.149 s (-57.1%; +144.2%) |
| Testify External | `none` | 32 | 0.062 s | 0.392 s (+533.5%) | 0.162 s (-58.7%; +161.6%) |
| Testify External | `-race` | 4 | 0.063 s | 0.345 s (+447.6%) | 0.163 s (-52.9%; +157.8%) |
| Testify External | `-race` | 32 | 0.063 s | 0.390 s (+515.2%) | 0.172 s (-55.9%; +171.3%) |
| Testify External | `-cover` | 4 | 0.064 s | 0.413 s (+541.2%) | 0.152 s (-63.1%; +136.9%) |
| Testify External | `-cover` | 32 | 0.065 s | 0.469 s (+617.6%) | 0.171 s (-63.5%; +161.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.065 s | 0.368 s (+463.1%) | 0.156 s (-57.6%; +139.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.066 s | 0.413 s (+526.1%) | 0.167 s (-59.7%; +152.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.064 s | 0.363 s (+463.8%) | 0.168 s (-53.6%; +161.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.069 s | 0.421 s (+511.7%) | 0.177 s (-58.0%; +156.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.068 s | 0.350 s (+415.7%) | 0.159 s (-54.5%; +134.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.068 s | 0.397 s (+487.4%) | 0.171 s (-56.9%; +153.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.067 s | 0.351 s (+426.4%) | 0.169 s (-51.8%; +153.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.070 s | 0.400 s (+468.1%) | 0.181 s (-54.9%; +156.4%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.337 s | 6.183 s (+362.4%) | 1.838 s (-70.3%; +37.4%) |
| Gin | `none` | 32 | 2.799 s | 6.523 s (+133.1%) | 1.706 s (-73.9%; -39.1%) |
| Chi | `none` | 4 | 0.282 s | 2.155 s (+663.5%) | 0.425 s (-80.3%; +50.4%) |
| Chi | `none` | 32 | 0.366 s | 1.889 s (+416.9%) | 0.438 s (-76.8%; +19.8%) |
| Gin | `-race` | 4 | 1.614 s | 8.494 s (+426.2%) | 4.284 s (-49.6%; +165.4%) |
| Gin | `-race` | 32 | 1.525 s | 8.523 s (+459.0%) | 3.596 s (-57.8%; +135.8%) |
| Gin | `-cover` | 4 | 1.361 s | 7.373 s (+441.7%) | 1.310 s (-82.2%; -3.8%) |
| Gin | `-cover` | 32 | 1.013 s | 6.950 s (+585.8%) | 1.905 s (-72.6%; +88.0%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.655 s | 8.250 s (+398.5%) | 1.946 s (-76.4%; +17.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.190 s | 6.366 s (+435.2%) | 1.487 s (-76.6%; +25.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.467 s | 8.417 s (+241.1%) | 2.728 s (-67.6%; +10.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.890 s | 8.827 s (+367.0%) | 3.357 s (-62.0%; +77.6%) |
| Chi | `-race` | 4 | 0.388 s | 2.181 s (+462.1%) | 0.710 s (-67.4%; +83.1%) |
| Chi | `-race` | 32 | 0.394 s | 2.127 s (+439.4%) | 0.756 s (-64.5%; +91.6%) |
| Chi | `-cover` | 4 | 0.317 s | 2.215 s (+599.3%) | 0.437 s (-80.3%; +37.9%) |
| Chi | `-cover` | 32 | 0.307 s | 2.117 s (+589.6%) | 0.460 s (-78.3%; +49.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.308 s | 2.240 s (+628.2%) | 0.446 s (-80.1%; +45.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.315 s | 2.065 s (+554.4%) | 0.449 s (-78.3%; +42.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.410 s | 2.462 s (+500.0%) | 0.762 s (-69.0%; +85.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.423 s | 2.263 s (+435.6%) | 0.740 s (-67.3%; +75.1%) |
| Testify Direct | `none` | 4 | 0.276 s | 1.820 s (+559.7%) | 0.423 s (-76.8%; +53.2%) |
| Testify Direct | `none` | 32 | 0.351 s | 2.105 s (+500.6%) | 0.599 s (-71.6%; +70.8%) |
| Testify Direct | `-race` | 4 | 0.442 s | 2.319 s (+424.5%) | 0.574 s (-75.2%; +29.9%) |
| Testify Direct | `-race` | 32 | 0.381 s | 1.963 s (+415.0%) | 0.617 s (-68.6%; +61.8%) |
| Testify Direct | `-cover` | 4 | 0.277 s | 1.968 s (+611.5%) | 0.457 s (-76.8%; +65.1%) |
| Testify Direct | `-cover` | 32 | 0.291 s | 2.134 s (+633.7%) | 0.473 s (-77.8%; +62.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.279 s | 1.780 s (+539.0%) | 0.440 s (-75.3%; +57.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.346 s | 2.344 s (+577.7%) | 0.643 s (-72.6%; +86.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.386 s | 2.612 s (+575.8%) | 0.635 s (-75.7%; +64.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.377 s | 2.000 s (+429.9%) | 0.625 s (-68.8%; +65.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.335 s | 1.782 s (+432.1%) | 0.588 s (-67.0%; +75.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.331 s | 2.101 s (+535.7%) | 0.545 s (-74.0%; +65.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.385 s | 1.952 s (+406.7%) | 0.595 s (-69.5%; +54.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.586 s | 2.069 s (+253.0%) | 0.625 s (-69.8%; +6.6%) |
| Testify External | `none` | 4 | 0.274 s | 1.861 s (+579.3%) | 0.426 s (-77.1%; +55.6%) |
| Testify External | `none` | 32 | 0.279 s | 1.922 s (+587.8%) | 0.459 s (-76.1%; +64.3%) |
| Testify External | `-race` | 4 | 0.362 s | 2.191 s (+505.5%) | 0.874 s (-60.1%; +141.7%) |
| Testify External | `-race` | 32 | 0.367 s | 2.012 s (+447.5%) | 0.617 s (-69.3%; +68.0%) |
| Testify External | `-cover` | 4 | 0.285 s | 1.974 s (+592.1%) | 0.442 s (-77.6%; +54.9%) |
| Testify External | `-cover` | 32 | 0.302 s | 2.033 s (+573.6%) | 0.466 s (-77.1%; +54.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.288 s | 1.898 s (+558.7%) | 0.459 s (-75.8%; +59.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.281 s | 2.138 s (+662.1%) | 0.470 s (-78.0%; +67.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.383 s | 2.038 s (+432.5%) | 0.600 s (-70.6%; +56.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.438 s | 2.102 s (+380.0%) | 0.649 s (-69.1%; +48.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.287 s | 2.172 s (+657.5%) | 0.455 s (-79.1%; +58.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.308 s | 1.932 s (+527.7%) | 0.476 s (-75.4%; +54.6%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.384 s | 2.045 s (+432.4%) | 0.633 s (-69.0%; +64.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.396 s | 2.100 s (+429.8%) | 0.636 s (-69.7%; +60.5%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.258 s | 3.023 s (+140.3%) | 1.520 s (-49.7%; +20.8%) |
| Gin | `none` | 32 | 1.199 s | 3.200 s (+166.8%) | 1.470 s (-54.1%; +22.6%) |
| Chi | `none` | 4 | 0.481 s | 1.924 s (+299.8%) | 0.588 s (-69.4%; +22.3%) |
| Chi | `none` | 32 | 0.453 s | 2.041 s (+350.8%) | 0.603 s (-70.5%; +33.2%) |
| Gin | `-race` | 4 | 2.242 s | 3.977 s (+77.4%) | 2.619 s (-34.2%; +16.8%) |
| Gin | `-race` | 32 | 2.383 s | 4.025 s (+68.9%) | 2.634 s (-34.6%; +10.5%) |
| Gin | `-cover` | 4 | 1.603 s | 3.333 s (+108.0%) | 1.743 s (-47.7%; +8.8%) |
| Gin | `-cover` | 32 | 1.377 s | 3.356 s (+143.7%) | 1.538 s (-54.2%; +11.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.511 s | 3.573 s (+136.5%) | 1.790 s (-49.9%; +18.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 2.333 s | 3.477 s (+49.0%) | 1.631 s (-53.1%; -30.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.455 s | 4.167 s (+69.7%) | 3.009 s (-27.8%; +22.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.597 s | 4.580 s (+76.4%) | 2.984 s (-34.8%; +14.9%) |
| Chi | `-race` | 4 | 0.776 s | 2.339 s (+201.3%) | 0.927 s (-60.4%; +19.4%) |
| Chi | `-race` | 32 | 0.794 s | 2.451 s (+208.7%) | 0.975 s (-60.2%; +22.8%) |
| Chi | `-cover` | 4 | 0.522 s | 2.053 s (+293.1%) | 0.653 s (-68.2%; +25.0%) |
| Chi | `-cover` | 32 | 0.526 s | 2.059 s (+291.1%) | 0.651 s (-68.4%; +23.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.548 s | 2.122 s (+287.0%) | 0.669 s (-68.5%; +22.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.530 s | 2.160 s (+307.1%) | 0.653 s (-69.8%; +23.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.862 s | 2.355 s (+173.0%) | 1.015 s (-56.9%; +17.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.890 s | 2.485 s (+179.1%) | 1.075 s (-56.7%; +20.7%) |
| Testify Direct | `none` | 4 | 0.312 s | 1.796 s (+476.0%) | 0.549 s (-69.4%; +76.2%) |
| Testify Direct | `none` | 32 | 0.320 s | 2.140 s (+567.9%) | 0.502 s (-76.6%; +56.6%) |
| Testify Direct | `-race` | 4 | 0.457 s | 2.750 s (+501.4%) | 0.688 s (-75.0%; +50.5%) |
| Testify Direct | `-race` | 32 | 0.478 s | 2.516 s (+426.1%) | 0.749 s (-70.2%; +56.6%) |
| Testify Direct | `-cover` | 4 | 0.328 s | 1.922 s (+485.6%) | 0.503 s (-73.8%; +53.3%) |
| Testify Direct | `-cover` | 32 | 0.394 s | 2.361 s (+499.3%) | 0.540 s (-77.1%; +37.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.353 s | 1.868 s (+429.3%) | 0.514 s (-72.5%; +45.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.335 s | 2.094 s (+525.0%) | 0.570 s (-72.8%; +70.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.475 s | 2.731 s (+474.8%) | 0.687 s (-74.8%; +44.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.485 s | 2.182 s (+349.6%) | 0.718 s (-67.1%; +48.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.329 s | 1.936 s (+487.9%) | 0.509 s (-73.7%; +54.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.386 s | 2.190 s (+466.7%) | 0.517 s (-76.4%; +33.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.472 s | 2.134 s (+351.8%) | 0.724 s (-66.1%; +53.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.502 s | 2.465 s (+391.2%) | 0.722 s (-70.7%; +43.8%) |
| Testify External | `none` | 4 | 0.324 s | 2.018 s (+523.3%) | 0.454 s (-77.5%; +40.3%) |
| Testify External | `none` | 32 | 0.303 s | 1.910 s (+531.3%) | 0.486 s (-74.5%; +60.8%) |
| Testify External | `-race` | 4 | 0.383 s | 1.893 s (+394.7%) | 0.605 s (-68.1%; +58.0%) |
| Testify External | `-race` | 32 | 0.384 s | 2.250 s (+485.5%) | 0.627 s (-72.1%; +63.2%) |
| Testify External | `-cover` | 4 | 0.306 s | 1.991 s (+550.5%) | 0.455 s (-77.1%; +48.7%) |
| Testify External | `-cover` | 32 | 0.309 s | 2.077 s (+571.4%) | 0.500 s (-75.9%; +61.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.299 s | 1.858 s (+522.4%) | 0.468 s (-74.8%; +56.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.311 s | 2.121 s (+582.1%) | 0.501 s (-76.4%; +61.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.408 s | 2.076 s (+409.3%) | 0.657 s (-68.4%; +61.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.396 s | 2.113 s (+432.8%) | 0.659 s (-68.8%; +66.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.307 s | 1.933 s (+529.5%) | 0.474 s (-75.5%; +54.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.335 s | 1.960 s (+485.0%) | 0.519 s (-73.5%; +54.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.388 s | 2.281 s (+487.4%) | 0.659 s (-71.1%; +69.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.401 s | 2.140 s (+434.2%) | 0.650 s (-69.6%; +62.3%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | `ddtest instrument` |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.841 s | 1.259 s (+49.6%) | 0.939 s (-25.4%; +11.6%) |
| Gin | `none` | 32 | 0.739 s | 1.207 s (+63.2%) | 0.837 s (-30.6%; +13.2%) |
| Chi | `none` | 4 | 0.307 s | 0.711 s (+131.5%) | 0.374 s (-47.5%; +21.6%) |
| Chi | `none` | 32 | 0.299 s | 0.774 s (+158.4%) | 0.404 s (-47.8%; +34.9%) |
| Gin | `-race` | 4 | 1.460 s | 1.876 s (+28.5%) | 1.591 s (-15.2%; +9.0%) |
| Gin | `-race` | 32 | 1.524 s | 2.009 s (+31.8%) | 1.654 s (-17.7%; +8.5%) |
| Gin | `-cover` | 4 | 1.033 s | 1.735 s (+67.9%) | 1.146 s (-33.9%; +10.9%) |
| Gin | `-cover` | 32 | 0.913 s | 1.624 s (+78.0%) | 1.022 s (-37.1%; +11.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.090 s | 1.837 s (+68.5%) | 1.190 s (-35.2%; +9.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.981 s | 1.648 s (+68.0%) | 1.101 s (-33.2%; +12.3%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.812 s | 2.423 s (+33.7%) | 1.906 s (-21.4%; +5.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.831 s | 2.499 s (+36.5%) | 1.946 s (-22.1%; +6.3%) |
| Chi | `-race` | 4 | 0.530 s | 0.940 s (+77.5%) | 0.596 s (-36.6%; +12.6%) |
| Chi | `-race` | 32 | 0.560 s | 1.003 s (+79.0%) | 0.649 s (-35.3%; +15.9%) |
| Chi | `-cover` | 4 | 0.359 s | 0.826 s (+130.2%) | 0.422 s (-48.9%; +17.7%) |
| Chi | `-cover` | 32 | 0.364 s | 0.884 s (+143.1%) | 0.439 s (-50.4%; +20.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.376 s | 0.829 s (+120.5%) | 0.438 s (-47.1%; +16.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.377 s | 1.694 s (+349.5%) | 0.442 s (-73.9%; +17.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.611 s | 1.045 s (+70.9%) | 0.689 s (-34.1%; +12.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.663 s | 1.142 s (+72.3%) | 0.748 s (-34.5%; +12.9%) |
| Testify Direct | `none` | 4 | 0.115 s | 0.609 s (+432.2%) | 0.202 s (-66.9%; +76.1%) |
| Testify Direct | `none` | 32 | 0.111 s | 0.905 s (+712.2%) | 0.215 s (-76.3%; +92.9%) |
| Testify Direct | `-race` | 4 | 0.159 s | 0.657 s (+313.4%) | 0.257 s (-60.8%; +61.9%) |
| Testify Direct | `-race` | 32 | 0.169 s | 0.729 s (+332.0%) | 0.285 s (-60.9%; +68.9%) |
| Testify Direct | `-cover` | 4 | 0.124 s | 0.738 s (+494.2%) | 0.213 s (-71.2%; +71.3%) |
| Testify Direct | `-cover` | 32 | 0.118 s | 0.896 s (+661.1%) | 0.229 s (-74.5%; +94.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.122 s | 0.818 s (+569.7%) | 0.216 s (-73.6%; +76.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.120 s | 0.701 s (+485.8%) | 0.230 s (-67.2%; +92.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.165 s | 0.823 s (+399.7%) | 0.275 s (-66.6%; +67.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.172 s | 0.853 s (+396.3%) | 0.296 s (-65.3%; +72.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.122 s | 0.632 s (+418.3%) | 0.216 s (-65.9%; +77.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.114 s | 0.687 s (+501.1%) | 0.223 s (-67.5%; +95.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.165 s | 0.696 s (+323.0%) | 0.269 s (-61.3%; +63.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.173 s | 0.741 s (+327.2%) | 0.289 s (-61.0%; +66.5%) |
| Testify External | `none` | 4 | 0.086 s | 0.608 s (+605.3%) | 0.177 s (-70.8%; +105.6%) |
| Testify External | `none` | 32 | 0.089 s | 0.654 s (+634.6%) | 0.192 s (-70.7%; +115.2%) |
| Testify External | `-race` | 4 | 0.088 s | 0.589 s (+568.1%) | 0.186 s (-68.4%; +111.0%) |
| Testify External | `-race` | 32 | 0.090 s | 0.704 s (+685.9%) | 0.207 s (-70.6%; +130.7%) |
| Testify External | `-cover` | 4 | 0.093 s | 0.666 s (+613.6%) | 0.186 s (-72.1%; +98.9%) |
| Testify External | `-cover` | 32 | 0.098 s | 0.745 s (+657.9%) | 0.205 s (-72.5%; +108.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.093 s | 0.634 s (+580.7%) | 0.191 s (-69.9%; +105.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.093 s | 0.691 s (+643.1%) | 0.202 s (-70.7%; +117.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.096 s | 0.616 s (+544.4%) | 0.198 s (-67.8%; +107.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.094 s | 0.683 s (+623.7%) | 0.218 s (-68.0%; +131.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.091 s | 0.607 s (+566.9%) | 0.185 s (-69.6%; +102.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.095 s | 0.664 s (+595.9%) | 0.197 s (-70.4%; +106.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.092 s | 0.656 s (+612.3%) | 0.197 s (-69.9%; +114.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.095 s | 0.661 s (+595.5%) | 0.215 s (-67.6%; +125.6%) |


