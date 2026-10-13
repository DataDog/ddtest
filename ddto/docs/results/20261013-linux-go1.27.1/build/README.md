# Compile-only comparisons

Mini: `137a9126dfdf002d50f750c3d63f87e23617c8cc`, collected 2026-10-13. Native/Orchestrion: retained from 2026-10-04 at `9d4786fbd27f573bb68c5516bf23b336e8d7bdaa`.

All variants use `go version go1.27.1 linux/amd64`, the recorded subject versions, flags, CPU affinity and repetition counts. Each observation includes wall time, aggregate cgroup CPU and memory, command and exit status. Cold builds start with an empty Go build cache; module downloads and the OS page cache remain warm. The other scenarios reuse unchanged output, force a fresh link, edit a reachable test body or edit an unused constant.

Mini traces and symbols are checked again. Native and Orchestrion retain their original qualification. Those variants were not rerun. Dates differ, so small timing differences need the recorded ranges and uncertainty. Orchestrion's percentage compares time against Native. Mini's percentages compare Orchestrion first, then Native. All time cells are seconds.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 17.896 s | 51.532 s (+188.0%) | 20.432 s (-60.4%; +14.2%) |
| Gin | `none` | 32 | 10.844 s | 22.679 s (+109.1%) | 12.430 s (-45.2%; +14.6%) |
| Chi | `none` | 4 | 7.764 s | 30.543 s (+293.4%) | 10.342 s (-66.1%; +33.2%) |
| Chi | `none` | 32 | 4.576 s | 16.765 s (+266.3%) | 6.587 s (-60.7%; +43.9%) |
| Gin | `-race` | 4 | 27.873 s | 56.274 s (+101.9%) | 30.086 s (-46.5%; +7.9%) |
| Gin | `-race` | 32 | 23.244 s | 34.631 s (+49.0%) | 24.461 s (-29.4%; +5.2%) |
| Gin | `-cover` | 4 | 18.781 s | 66.330 s (+253.2%) | 21.476 s (-67.6%; +14.3%) |
| Gin | `-cover` | 32 | 10.933 s | 29.664 s (+171.3%) | 12.487 s (-57.9%; +14.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 19.328 s | 56.685 s (+193.3%) | 21.730 s (-61.7%; +12.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 12.161 s | 27.508 s (+126.2%) | 12.009 s (-56.3%; -1.3%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 29.271 s | 68.432 s (+133.8%) | 30.380 s (-55.6%; +3.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 24.174 s | 39.394 s (+63.0%) | 24.872 s (-36.9%; +2.9%) |
| Chi | `-race` | 4 | 11.393 s | 37.963 s (+233.2%) | 14.657 s (-61.4%; +28.6%) |
| Chi | `-race` | 32 | 9.318 s | 27.565 s (+195.8%) | 12.608 s (-54.3%; +35.3%) |
| Chi | `-cover` | 4 | 7.934 s | 38.145 s (+380.8%) | 10.755 s (-71.8%; +35.6%) |
| Chi | `-cover` | 32 | 4.815 s | 19.639 s (+307.9%) | 6.568 s (-66.6%; +36.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 8.179 s | 33.966 s (+315.3%) | 10.973 s (-67.7%; +34.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 4.946 s | 17.755 s (+259.0%) | 6.673 s (-62.4%; +34.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 11.745 s | 41.717 s (+255.2%) | 15.407 s (-63.1%; +31.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.538 s | 33.637 s (+252.7%) | 12.845 s (-61.8%; +34.7%) |
| Testify Direct | `none` | 4 | 8.078 s | 33.039 s (+309.0%) | 11.350 s (-65.6%; +40.5%) |
| Testify Direct | `none` | 32 | 4.867 s | 20.067 s (+312.3%) | 6.834 s (-65.9%; +40.4%) |
| Testify Direct | `-race` | 4 | 12.691 s | 46.494 s (+266.4%) | 15.831 s (-66.0%; +24.7%) |
| Testify Direct | `-race` | 32 | 10.094 s | 33.293 s (+229.8%) | 12.611 s (-62.1%; +24.9%) |
| Testify Direct | `-cover` | 4 | 8.650 s | 39.414 s (+355.7%) | 11.487 s (-70.9%; +32.8%) |
| Testify Direct | `-cover` | 32 | 5.337 s | 21.922 s (+310.8%) | 6.746 s (-69.2%; +26.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 9.083 s | 34.215 s (+276.7%) | 11.493 s (-66.4%; +26.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 5.248 s | 28.446 s (+442.0%) | 6.799 s (-76.1%; +29.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 13.186 s | 53.793 s (+307.9%) | 16.135 s (-70.0%; +22.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 10.073 s | 29.693 s (+194.8%) | 12.703 s (-57.2%; +26.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.603 s | 30.824 s (+258.3%) | 11.171 s (-63.8%; +29.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.913 s | 20.015 s (+307.4%) | 6.735 s (-66.3%; +37.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.509 s | 39.152 s (+213.0%) | 16.352 s (-58.2%; +30.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.825 s | 29.736 s (+202.6%) | 12.940 s (-56.5%; +31.7%) |
| Testify External | `none` | 4 | 8.381 s | 30.092 s (+259.0%) | 11.337 s (-62.3%; +35.3%) |
| Testify External | `none` | 32 | 4.810 s | 18.167 s (+277.7%) | 6.788 s (-62.6%; +41.1%) |
| Testify External | `-race` | 4 | 12.450 s | 37.839 s (+203.9%) | 15.514 s (-59.0%; +24.6%) |
| Testify External | `-race` | 32 | 9.726 s | 27.976 s (+187.6%) | 12.576 s (-55.0%; +29.3%) |
| Testify External | `-cover` | 4 | 8.407 s | 34.683 s (+312.6%) | 11.241 s (-67.6%; +33.7%) |
| Testify External | `-cover` | 32 | 4.861 s | 21.347 s (+339.2%) | 6.837 s (-68.0%; +40.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 8.507 s | 31.696 s (+272.6%) | 11.439 s (-63.9%; +34.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 5.078 s | 18.402 s (+262.4%) | 6.743 s (-63.4%; +32.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 12.936 s | 40.899 s (+216.2%) | 15.394 s (-62.4%; +19.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.861 s | 27.088 s (+174.7%) | 12.622 s (-53.4%; +28.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.465 s | 29.977 s (+254.1%) | 11.511 s (-61.6%; +36.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.869 s | 17.912 s (+267.8%) | 6.801 s (-62.0%; +39.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.725 s | 38.705 s (+204.2%) | 16.148 s (-58.3%; +26.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.847 s | 28.139 s (+185.8%) | 12.737 s (-54.7%; +29.3%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.098 s | 0.366 s (+273.5%) | 0.176 s (-51.8%; +79.9%) |
| Gin | `none` | 32 | 0.097 s | 0.410 s (+322.2%) | 0.194 s (-52.8%; +99.5%) |
| Chi | `none` | 4 | 0.063 s | 0.329 s (+421.5%) | 0.108 s (-67.2%; +71.3%) |
| Chi | `none` | 32 | 0.067 s | 0.385 s (+476.1%) | 0.119 s (-69.0%; +78.8%) |
| Gin | `-race` | 4 | 0.098 s | 0.370 s (+279.1%) | 0.190 s (-48.8%; +94.3%) |
| Gin | `-race` | 32 | 0.098 s | 0.413 s (+323.5%) | 0.206 s (-50.2%; +111.1%) |
| Gin | `-cover` | 4 | 0.127 s | 0.514 s (+305.2%) | 0.206 s (-59.9%; +62.4%) |
| Gin | `-cover` | 32 | 0.125 s | 0.570 s (+358.0%) | 0.221 s (-61.3%; +77.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.130 s | 0.432 s (+231.9%) | 0.210 s (-51.4%; +61.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.464 s (+264.8%) | 0.225 s (-51.4%; +77.3%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.144 s | 0.466 s (+224.4%) | 0.224 s (-52.1%; +55.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.471 s (+270.6%) | 0.235 s (-50.1%; +85.1%) |
| Chi | `-race` | 4 | 0.062 s | 0.327 s (+428.8%) | 0.119 s (-63.6%; +92.6%) |
| Chi | `-race` | 32 | 0.067 s | 0.387 s (+476.2%) | 0.132 s (-65.9%; +96.4%) |
| Chi | `-cover` | 4 | 0.066 s | 0.419 s (+536.9%) | 0.113 s (-73.0%; +71.8%) |
| Chi | `-cover` | 32 | 0.070 s | 0.494 s (+605.6%) | 0.126 s (-74.5%; +79.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.371 s (+462.5%) | 0.115 s (-68.9%; +75.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.071 s | 0.430 s (+502.4%) | 0.124 s (-71.3%; +73.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.067 s | 0.370 s (+452.3%) | 0.131 s (-64.6%; +95.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.072 s | 0.432 s (+503.9%) | 0.134 s (-68.9%; +87.7%) |
| Testify Direct | `none` | 4 | 0.059 s | 0.314 s (+436.9%) | 0.133 s (-57.7%; +127.0%) |
| Testify Direct | `none` | 32 | 0.061 s | 0.358 s (+488.3%) | 0.145 s (-59.4%; +138.6%) |
| Testify Direct | `-race` | 4 | 0.056 s | 0.314 s (+456.1%) | 0.141 s (-55.0%; +150.1%) |
| Testify Direct | `-race` | 32 | 0.063 s | 0.388 s (+520.6%) | 0.157 s (-59.6%; +150.6%) |
| Testify Direct | `-cover` | 4 | 0.060 s | 0.385 s (+538.2%) | 0.138 s (-64.3%; +128.0%) |
| Testify Direct | `-cover` | 32 | 0.068 s | 0.472 s (+597.8%) | 0.149 s (-68.4%; +120.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.365 s (+451.6%) | 0.148 s (-59.5%; +123.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.067 s | 0.415 s (+516.9%) | 0.149 s (-64.2%; +120.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.065 s | 0.365 s (+459.7%) | 0.152 s (-58.3%; +133.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.064 s | 0.386 s (+507.2%) | 0.164 s (-57.6%; +157.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.322 s (+401.0%) | 0.142 s (-55.9%; +121.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.366 s (+469.1%) | 0.149 s (-59.4%; +130.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.319 s (+407.9%) | 0.150 s (-52.9%; +139.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.066 s | 0.366 s (+458.5%) | 0.161 s (-55.9%; +146.1%) |
| Testify External | `none` | 4 | 0.059 s | 0.314 s (+434.2%) | 0.144 s (-54.2%; +144.6%) |
| Testify External | `none` | 32 | 0.059 s | 0.358 s (+502.3%) | 0.143 s (-60.1%; +140.4%) |
| Testify External | `-race` | 4 | 0.058 s | 0.312 s (+438.9%) | 0.142 s (-54.6%; +144.8%) |
| Testify External | `-race` | 32 | 0.062 s | 0.354 s (+474.6%) | 0.158 s (-55.3%; +157.0%) |
| Testify External | `-cover` | 4 | 0.062 s | 0.376 s (+507.5%) | 0.138 s (-63.3%; +122.9%) |
| Testify External | `-cover` | 32 | 0.063 s | 0.435 s (+589.3%) | 0.149 s (-65.8%; +135.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.062 s | 0.335 s (+437.0%) | 0.138 s (-58.8%; +121.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.063 s | 0.394 s (+525.3%) | 0.147 s (-62.7%; +132.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.060 s | 0.341 s (+465.5%) | 0.148 s (-56.6%; +145.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.066 s | 0.395 s (+502.8%) | 0.160 s (-59.5%; +144.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.323 s (+404.0%) | 0.153 s (-52.8%; +138.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.364 s (+468.6%) | 0.148 s (-59.5%; +130.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.320 s (+406.8%) | 0.152 s (-52.5%; +140.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.368 s (+476.1%) | 0.162 s (-56.1%; +152.9%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.158 s | 5.302 s (+357.8%) | 2.075 s (-60.9%; +79.1%) |
| Gin | `none` | 32 | 1.213 s | 5.372 s (+342.7%) | 1.959 s (-63.5%; +61.4%) |
| Chi | `none` | 4 | 0.266 s | 1.810 s (+580.4%) | 0.387 s (-78.6%; +45.3%) |
| Chi | `none` | 32 | 0.272 s | 1.759 s (+547.0%) | 0.449 s (-74.5%; +65.3%) |
| Gin | `-race` | 4 | 1.987 s | 7.649 s (+284.9%) | 2.581 s (-66.3%; +29.9%) |
| Gin | `-race` | 32 | 1.949 s | 8.048 s (+313.0%) | 2.443 s (-69.6%; +25.4%) |
| Gin | `-cover` | 4 | 1.086 s | 5.733 s (+427.9%) | 1.923 s (-66.4%; +77.1%) |
| Gin | `-cover` | 32 | 1.143 s | 6.131 s (+436.2%) | 1.719 s (-72.0%; +50.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.245 s | 5.492 s (+341.0%) | 1.821 s (-66.8%; +46.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.281 s | 5.750 s (+349.0%) | 1.646 s (-71.4%; +28.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.012 s | 8.356 s (+315.3%) | 2.857 s (-65.8%; +42.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.258 s | 8.331 s (+268.9%) | 3.017 s (-63.8%; +33.6%) |
| Chi | `-race` | 4 | 0.361 s | 2.053 s (+468.9%) | 0.517 s (-74.8%; +43.2%) |
| Chi | `-race` | 32 | 0.365 s | 2.021 s (+453.6%) | 0.688 s (-66.0%; +88.3%) |
| Chi | `-cover` | 4 | 0.290 s | 2.012 s (+593.0%) | 0.408 s (-79.7%; +40.4%) |
| Chi | `-cover` | 32 | 0.285 s | 2.097 s (+636.1%) | 0.441 s (-79.0%; +54.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.287 s | 2.012 s (+600.1%) | 0.418 s (-79.2%; +45.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.977 s (+580.2%) | 0.419 s (-78.8%; +44.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.384 s | 2.132 s (+454.8%) | 0.720 s (-66.2%; +87.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.424 s | 2.296 s (+441.7%) | 0.739 s (-67.8%; +74.3%) |
| Testify Direct | `none` | 4 | 0.246 s | 1.622 s (+560.1%) | 0.409 s (-74.8%; +66.2%) |
| Testify Direct | `none` | 32 | 0.270 s | 1.795 s (+563.7%) | 0.444 s (-75.3%; +64.2%) |
| Testify Direct | `-race` | 4 | 0.335 s | 1.839 s (+449.3%) | 0.538 s (-70.7%; +60.9%) |
| Testify Direct | `-race` | 32 | 0.381 s | 2.030 s (+432.5%) | 0.581 s (-71.4%; +52.4%) |
| Testify Direct | `-cover` | 4 | 0.271 s | 1.895 s (+599.4%) | 0.428 s (-77.4%; +58.0%) |
| Testify Direct | `-cover` | 32 | 0.291 s | 1.989 s (+582.3%) | 0.451 s (-77.3%; +54.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.304 s | 1.903 s (+526.8%) | 0.435 s (-77.2%; +43.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.965 s (+576.0%) | 0.457 s (-76.7%; +57.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.367 s | 1.929 s (+425.0%) | 0.571 s (-70.4%; +55.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.358 s | 2.024 s (+465.9%) | 0.587 s (-71.0%; +64.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.259 s | 1.753 s (+577.6%) | 0.414 s (-76.4%; +60.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.264 s | 1.968 s (+644.8%) | 0.451 s (-77.1%; +70.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.789 s (+426.6%) | 0.582 s (-67.5%; +71.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.359 s | 2.022 s (+463.0%) | 0.609 s (-69.9%; +69.6%) |
| Testify External | `none` | 4 | 0.239 s | 1.716 s (+618.1%) | 0.419 s (-75.6%; +75.6%) |
| Testify External | `none` | 32 | 0.256 s | 1.867 s (+629.4%) | 0.427 s (-77.1%; +66.8%) |
| Testify External | `-race` | 4 | 0.328 s | 1.821 s (+456.1%) | 0.523 s (-71.3%; +59.8%) |
| Testify External | `-race` | 32 | 0.341 s | 1.952 s (+472.8%) | 0.579 s (-70.4%; +69.8%) |
| Testify External | `-cover` | 4 | 0.248 s | 1.696 s (+582.5%) | 0.419 s (-75.3%; +68.5%) |
| Testify External | `-cover` | 32 | 0.262 s | 1.960 s (+646.8%) | 0.449 s (-77.1%; +71.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.257 s | 1.700 s (+562.2%) | 0.426 s (-74.9%; +66.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.270 s | 1.956 s (+625.8%) | 0.445 s (-77.2%; +65.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.345 s | 1.920 s (+457.1%) | 0.543 s (-71.7%; +57.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.352 s | 2.055 s (+484.5%) | 0.609 s (-70.4%; +73.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.258 s | 1.762 s (+583.7%) | 0.445 s (-74.7%; +72.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.270 s | 1.895 s (+602.2%) | 0.439 s (-76.8%; +62.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.840 s (+441.7%) | 0.576 s (-68.7%; +69.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.358 s | 2.009 s (+461.4%) | 0.594 s (-70.4%; +65.9%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.280 s | 2.810 s (+119.6%) | 1.418 s (-49.5%; +10.8%) |
| Gin | `none` | 32 | 1.160 s | 2.871 s (+147.6%) | 1.380 s (-51.9%; +19.0%) |
| Chi | `none` | 4 | 0.413 s | 1.794 s (+334.9%) | 0.525 s (-70.8%; +27.1%) |
| Chi | `none` | 32 | 0.409 s | 1.901 s (+364.2%) | 0.587 s (-69.1%; +43.4%) |
| Gin | `-race` | 4 | 2.120 s | 3.873 s (+82.7%) | 2.472 s (-36.2%; +16.6%) |
| Gin | `-race` | 32 | 2.182 s | 4.379 s (+100.7%) | 2.630 s (-39.9%; +20.5%) |
| Gin | `-cover` | 4 | 1.440 s | 3.115 s (+116.4%) | 1.662 s (-46.6%; +15.5%) |
| Gin | `-cover` | 32 | 1.338 s | 3.272 s (+144.6%) | 1.526 s (-53.4%; +14.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.533 s | 3.126 s (+103.9%) | 1.728 s (-44.7%; +12.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.351 s | 3.375 s (+149.8%) | 1.524 s (-54.9%; +12.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.451 s | 4.147 s (+69.2%) | 2.743 s (-33.8%; +11.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.473 s | 4.708 s (+90.4%) | 2.861 s (-39.2%; +15.7%) |
| Chi | `-race` | 4 | 0.723 s | 2.295 s (+217.5%) | 0.847 s (-63.1%; +17.1%) |
| Chi | `-race` | 32 | 0.750 s | 2.399 s (+219.9%) | 0.896 s (-62.6%; +19.5%) |
| Chi | `-cover` | 4 | 0.475 s | 2.002 s (+321.6%) | 0.600 s (-70.0%; +26.3%) |
| Chi | `-cover` | 32 | 0.465 s | 1.910 s (+310.7%) | 0.582 s (-69.5%; +25.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.479 s | 1.961 s (+309.1%) | 0.626 s (-68.1%; +30.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.487 s | 1.974 s (+305.1%) | 0.632 s (-68.0%; +29.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.818 s | 2.226 s (+172.2%) | 0.990 s (-55.5%; +21.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.851 s | 2.501 s (+194.0%) | 1.035 s (-58.6%; +21.7%) |
| Testify Direct | `none` | 4 | 0.285 s | 2.451 s (+760.6%) | 0.467 s (-81.0%; +63.8%) |
| Testify Direct | `none` | 32 | 0.287 s | 1.882 s (+554.8%) | 0.480 s (-74.5%; +66.9%) |
| Testify Direct | `-race` | 4 | 0.447 s | 2.297 s (+413.5%) | 0.625 s (-72.8%; +39.7%) |
| Testify Direct | `-race` | 32 | 0.468 s | 2.876 s (+515.0%) | 0.700 s (-75.7%; +49.7%) |
| Testify Direct | `-cover` | 4 | 0.332 s | 2.391 s (+621.1%) | 0.482 s (-79.9%; +45.2%) |
| Testify Direct | `-cover` | 32 | 0.336 s | 2.099 s (+524.0%) | 0.485 s (-76.9%; +44.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 2.076 s (+536.3%) | 0.495 s (-76.2%; +51.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.339 s | 2.229 s (+558.4%) | 0.509 s (-77.2%; +50.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.462 s | 2.537 s (+449.4%) | 0.664 s (-73.8%; +43.9%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.437 s | 2.183 s (+399.5%) | 0.694 s (-68.2%; +58.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.286 s | 1.915 s (+568.8%) | 0.453 s (-76.3%; +58.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.303 s | 2.036 s (+571.3%) | 0.493 s (-75.8%; +62.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.423 s | 2.010 s (+374.6%) | 0.644 s (-68.0%; +52.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.449 s | 2.246 s (+400.1%) | 0.707 s (-68.5%; +57.3%) |
| Testify External | `none` | 4 | 0.253 s | 1.742 s (+589.7%) | 0.446 s (-74.4%; +76.7%) |
| Testify External | `none` | 32 | 0.264 s | 1.933 s (+633.3%) | 0.444 s (-77.0%; +68.5%) |
| Testify External | `-race` | 4 | 0.340 s | 1.838 s (+440.7%) | 0.545 s (-70.3%; +60.4%) |
| Testify External | `-race` | 32 | 0.360 s | 2.089 s (+480.9%) | 0.592 s (-71.7%; +64.6%) |
| Testify External | `-cover` | 4 | 0.267 s | 1.868 s (+598.4%) | 0.438 s (-76.5%; +63.9%) |
| Testify External | `-cover` | 32 | 0.275 s | 1.972 s (+615.9%) | 0.470 s (-76.2%; +70.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.280 s | 1.884 s (+572.7%) | 0.447 s (-76.3%; +59.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.286 s | 2.043 s (+614.8%) | 0.473 s (-76.9%; +65.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.354 s | 1.992 s (+463.4%) | 0.571 s (-71.3%; +61.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.383 s | 2.090 s (+445.4%) | 0.623 s (-70.2%; +62.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.274 s | 1.811 s (+560.5%) | 0.471 s (-74.0%; +71.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.285 s | 1.993 s (+598.4%) | 0.488 s (-75.5%; +71.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.354 s | 1.928 s (+444.1%) | 0.596 s (-69.1%; +68.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.389 s | 2.126 s (+447.2%) | 0.616 s (-71.0%; +58.6%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.760 s | 1.161 s (+52.8%) | 0.860 s (-25.9%; +13.2%) |
| Gin | `none` | 32 | 0.651 s | 1.110 s (+70.4%) | 0.790 s (-28.9%; +21.3%) |
| Chi | `none` | 4 | 0.266 s | 0.657 s (+147.5%) | 0.325 s (-50.6%; +22.2%) |
| Chi | `none` | 32 | 0.250 s | 0.682 s (+173.1%) | 0.352 s (-48.4%; +40.9%) |
| Gin | `-race` | 4 | 1.368 s | 1.769 s (+29.2%) | 1.550 s (-12.4%; +13.3%) |
| Gin | `-race` | 32 | 1.411 s | 1.821 s (+29.1%) | 1.642 s (-9.8%; +16.4%) |
| Gin | `-cover` | 4 | 0.944 s | 1.579 s (+67.2%) | 1.032 s (-34.7%; +9.2%) |
| Gin | `-cover` | 32 | 0.823 s | 1.501 s (+82.4%) | 0.948 s (-36.8%; +15.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.982 s | 1.581 s (+61.1%) | 1.084 s (-31.4%; +10.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.868 s | 1.467 s (+68.9%) | 1.048 s (-28.5%; +20.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.665 s | 2.253 s (+35.3%) | 1.785 s (-20.8%; +7.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.667 s | 2.338 s (+40.2%) | 1.919 s (-17.9%; +15.1%) |
| Chi | `-race` | 4 | 0.491 s | 0.872 s (+77.6%) | 0.555 s (-36.4%; +12.9%) |
| Chi | `-race` | 32 | 0.520 s | 0.939 s (+80.5%) | 0.606 s (-35.5%; +16.4%) |
| Chi | `-cover` | 4 | 0.315 s | 0.795 s (+152.1%) | 0.387 s (-51.3%; +22.9%) |
| Chi | `-cover` | 32 | 0.299 s | 0.799 s (+167.1%) | 0.377 s (-52.8%; +26.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 0.774 s (+137.3%) | 0.419 s (-45.8%; +28.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.331 s | 0.796 s (+140.5%) | 0.414 s (-48.0%; +25.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.572 s | 0.996 s (+74.2%) | 0.676 s (-32.1%; +18.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.595 s | 2.099 s (+252.5%) | 0.727 s (-65.4%; +22.1%) |
| Testify Direct | `none` | 4 | 0.102 s | 0.656 s (+542.8%) | 0.182 s (-72.2%; +78.6%) |
| Testify Direct | `none` | 32 | 0.099 s | 1.002 s (+914.9%) | 0.207 s (-79.3%; +109.8%) |
| Testify Direct | `-race` | 4 | 0.154 s | 0.669 s (+334.7%) | 0.233 s (-65.1%; +51.6%) |
| Testify Direct | `-race` | 32 | 0.167 s | 0.736 s (+340.0%) | 0.264 s (-64.1%; +58.0%) |
| Testify Direct | `-cover` | 4 | 0.121 s | 0.698 s (+477.6%) | 0.198 s (-71.6%; +64.0%) |
| Testify Direct | `-cover` | 32 | 0.119 s | 0.775 s (+554.0%) | 0.207 s (-73.3%; +74.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.124 s | 1.310 s (+958.5%) | 0.218 s (-83.3%; +76.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.120 s | 0.728 s (+507.7%) | 0.219 s (-69.9%; +83.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.164 s | 0.776 s (+373.3%) | 0.258 s (-66.8%; +57.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.161 s | 0.725 s (+348.9%) | 0.278 s (-61.7%; +72.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.107 s | 0.589 s (+450.0%) | 0.187 s (-68.2%; +74.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.105 s | 0.650 s (+518.9%) | 0.200 s (-69.3%; +90.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.150 s | 0.619 s (+312.1%) | 0.249 s (-59.8%; +65.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.158 s | 0.706 s (+347.7%) | 0.271 s (-61.6%; +71.7%) |
| Testify External | `none` | 4 | 0.078 s | 0.546 s (+598.3%) | 0.174 s (-68.1%; +122.4%) |
| Testify External | `none` | 32 | 0.080 s | 0.614 s (+667.6%) | 0.173 s (-71.9%; +116.0%) |
| Testify External | `-race` | 4 | 0.078 s | 0.549 s (+607.0%) | 0.167 s (-69.6%; +115.2%) |
| Testify External | `-race` | 32 | 0.081 s | 0.611 s (+654.1%) | 0.180 s (-70.4%; +122.9%) |
| Testify External | `-cover` | 4 | 0.081 s | 0.624 s (+667.1%) | 0.166 s (-73.5%; +103.4%) |
| Testify External | `-cover` | 32 | 0.086 s | 1.060 s (+1136.5%) | 0.194 s (-81.7%; +125.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.086 s | 0.573 s (+568.0%) | 0.174 s (-69.6%; +102.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.088 s | 0.653 s (+644.6%) | 0.186 s (-71.5%; +111.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.085 s | 0.592 s (+596.4%) | 0.180 s (-69.6%; +112.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.089 s | 0.651 s (+631.3%) | 0.209 s (-67.9%; +134.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.082 s | 0.554 s (+572.3%) | 0.185 s (-66.7%; +124.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.084 s | 0.630 s (+650.7%) | 0.182 s (-71.2%; +116.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.084 s | 0.772 s (+821.5%) | 0.176 s (-77.2%; +110.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.086 s | 0.623 s (+622.8%) | 0.194 s (-68.9%; +125.1%) |

The original convergence control is retained evidence for its collection date. It is not a new host-stability check.
