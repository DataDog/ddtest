# Compile-only comparisons

Mini: `20caa458420f567c65a15ca5124696a09714556a`, collected 2026-10-09. Native/Orchestrion: retained from 2026-10-04 at `9d4786fbd27f573bb68c5516bf23b336e8d7bdaa`.

All variants use `go version go1.27.1 linux/amd64`, the recorded subject versions, flags, CPU affinity and repetition counts. Each observation includes wall time, aggregate cgroup CPU and memory, command and exit status. Cold builds start with an empty Go build cache; module downloads and the OS page cache remain warm. The other scenarios reuse unchanged output, force a fresh link, edit a reachable test body or edit an unused constant.

Mini traces and symbols are checked again. Native and Orchestrion retain their original qualification. Those variants were not rerun. Dates differ, so small timing differences need the recorded ranges and uncertainty. Orchestrion's percentage compares time against Native. Mini's percentages compare Orchestrion first, then Native. All time cells are seconds.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 17.896 s | 51.532 s (+188.0%) | 19.055 s (-63.0%; +6.5%) |
| Gin | `none` | 32 | 10.844 s | 22.679 s (+109.1%) | 16.363 s (-27.9%; +50.9%) |
| Chi | `none` | 4 | 7.764 s | 30.543 s (+293.4%) | 9.604 s (-68.6%; +23.7%) |
| Chi | `none` | 32 | 4.576 s | 16.765 s (+266.3%) | 6.047 s (-63.9%; +32.1%) |
| Gin | `-race` | 4 | 27.873 s | 56.274 s (+101.9%) | 28.587 s (-49.2%; +2.6%) |
| Gin | `-race` | 32 | 23.244 s | 34.631 s (+49.0%) | 23.714 s (-31.5%; +2.0%) |
| Gin | `-cover` | 4 | 18.781 s | 66.330 s (+253.2%) | 25.409 s (-61.7%; +35.3%) |
| Gin | `-cover` | 32 | 10.933 s | 29.664 s (+171.3%) | 17.899 s (-39.7%; +63.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 19.328 s | 56.685 s (+193.3%) | 21.024 s (-62.9%; +8.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 12.161 s | 27.508 s (+126.2%) | 15.180 s (-44.8%; +24.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 29.271 s | 68.432 s (+133.8%) | 32.356 s (-52.7%; +10.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 24.174 s | 39.394 s (+63.0%) | 30.395 s (-22.8%; +25.7%) |
| Chi | `-race` | 4 | 11.393 s | 37.963 s (+233.2%) | 15.735 s (-58.6%; +38.1%) |
| Chi | `-race` | 32 | 9.318 s | 27.565 s (+195.8%) | 12.743 s (-53.8%; +36.8%) |
| Chi | `-cover` | 4 | 7.934 s | 38.145 s (+380.8%) | 9.844 s (-74.2%; +24.1%) |
| Chi | `-cover` | 32 | 4.815 s | 19.639 s (+307.9%) | 5.950 s (-69.7%; +23.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 8.179 s | 33.966 s (+315.3%) | 10.483 s (-69.1%; +28.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 4.946 s | 17.755 s (+259.0%) | 6.057 s (-65.9%; +22.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 11.745 s | 41.717 s (+255.2%) | 15.224 s (-63.5%; +29.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.538 s | 33.637 s (+252.7%) | 12.500 s (-62.8%; +31.1%) |
| Testify Direct | `none` | 4 | 8.078 s | 33.039 s (+309.0%) | 10.665 s (-67.7%; +32.0%) |
| Testify Direct | `none` | 32 | 4.867 s | 20.067 s (+312.3%) | 6.100 s (-69.6%; +25.3%) |
| Testify Direct | `-race` | 4 | 12.691 s | 46.494 s (+266.4%) | 25.785 s (-44.5%; +103.2%) |
| Testify Direct | `-race` | 32 | 10.094 s | 33.293 s (+229.8%) | 29.650 s (-10.9%; +193.7%) |
| Testify Direct | `-cover` | 4 | 8.650 s | 39.414 s (+355.7%) | 11.138 s (-71.7%; +28.8%) |
| Testify Direct | `-cover` | 32 | 5.337 s | 21.922 s (+310.8%) | 6.650 s (-69.7%; +24.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 9.083 s | 34.215 s (+276.7%) | 12.126 s (-64.6%; +33.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 5.248 s | 28.446 s (+442.0%) | 6.380 s (-77.6%; +21.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 13.186 s | 53.793 s (+307.9%) | 16.168 s (-69.9%; +22.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 10.073 s | 29.693 s (+194.8%) | 13.026 s (-56.1%; +29.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.603 s | 30.824 s (+258.3%) | 10.671 s (-65.4%; +24.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.913 s | 20.015 s (+307.4%) | 6.600 s (-67.0%; +34.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.509 s | 39.152 s (+213.0%) | 16.189 s (-58.7%; +29.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.825 s | 29.736 s (+202.6%) | 13.095 s (-56.0%; +33.3%) |
| Testify External | `none` | 4 | 8.381 s | 30.092 s (+259.0%) | 10.380 s (-65.5%; +23.9%) |
| Testify External | `none` | 32 | 4.810 s | 18.167 s (+277.7%) | 6.595 s (-63.7%; +37.1%) |
| Testify External | `-race` | 4 | 12.450 s | 37.839 s (+203.9%) | 16.128 s (-57.4%; +29.5%) |
| Testify External | `-race` | 32 | 9.726 s | 27.976 s (+187.6%) | 13.087 s (-53.2%; +34.6%) |
| Testify External | `-cover` | 4 | 8.407 s | 34.683 s (+312.6%) | 10.129 s (-70.8%; +20.5%) |
| Testify External | `-cover` | 32 | 4.861 s | 21.347 s (+339.2%) | 6.090 s (-71.5%; +25.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 8.507 s | 31.696 s (+272.6%) | 10.103 s (-68.1%; +18.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 5.078 s | 18.402 s (+262.4%) | 6.305 s (-65.7%; +24.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 12.936 s | 40.899 s (+216.2%) | 15.436 s (-62.3%; +19.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.861 s | 27.088 s (+174.7%) | 12.509 s (-53.8%; +26.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.465 s | 29.977 s (+254.1%) | 10.189 s (-66.0%; +20.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.869 s | 17.912 s (+267.8%) | 6.206 s (-65.4%; +27.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.725 s | 38.705 s (+204.2%) | 15.540 s (-59.8%; +22.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.847 s | 28.139 s (+185.8%) | 12.419 s (-55.9%; +26.1%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.098 s | 0.366 s (+273.5%) | 0.168 s (-54.2%; +71.0%) |
| Gin | `none` | 32 | 0.097 s | 0.410 s (+322.2%) | 0.184 s (-55.0%; +90.0%) |
| Chi | `none` | 4 | 0.063 s | 0.329 s (+421.5%) | 0.120 s (-63.4%; +91.1%) |
| Chi | `none` | 32 | 0.067 s | 0.385 s (+476.1%) | 0.144 s (-62.5%; +116.1%) |
| Gin | `-race` | 4 | 0.098 s | 0.370 s (+279.1%) | 0.169 s (-54.3%; +73.3%) |
| Gin | `-race` | 32 | 0.098 s | 0.413 s (+323.5%) | 0.186 s (-55.1%; +90.4%) |
| Gin | `-cover` | 4 | 0.127 s | 0.514 s (+305.2%) | 0.200 s (-61.0%; +57.9%) |
| Gin | `-cover` | 32 | 0.125 s | 0.570 s (+358.0%) | 0.209 s (-63.4%; +67.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.130 s | 0.432 s (+231.9%) | 0.205 s (-52.7%; +57.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.464 s (+264.8%) | 0.213 s (-54.1%; +67.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.144 s | 0.466 s (+224.4%) | 0.293 s (-37.2%; +103.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.471 s (+270.6%) | 0.216 s (-54.2%; +69.9%) |
| Chi | `-race` | 4 | 0.062 s | 0.327 s (+428.8%) | 0.143 s (-56.4%; +130.6%) |
| Chi | `-race` | 32 | 0.067 s | 0.387 s (+476.2%) | 0.139 s (-64.0%; +107.3%) |
| Chi | `-cover` | 4 | 0.066 s | 0.419 s (+536.9%) | 0.125 s (-70.1%; +90.5%) |
| Chi | `-cover` | 32 | 0.070 s | 0.494 s (+605.6%) | 0.139 s (-71.8%; +99.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.371 s (+462.5%) | 0.146 s (-60.6%; +121.5%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.071 s | 0.430 s (+502.4%) | 0.143 s (-66.8%; +99.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.067 s | 0.370 s (+452.3%) | 0.147 s (-60.2%; +119.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.072 s | 0.432 s (+503.9%) | 0.143 s (-66.8%; +100.4%) |
| Testify Direct | `none` | 4 | 0.059 s | 0.314 s (+436.9%) | 0.122 s (-61.1%; +108.7%) |
| Testify Direct | `none` | 32 | 0.061 s | 0.358 s (+488.3%) | 0.131 s (-63.3%; +115.7%) |
| Testify Direct | `-race` | 4 | 0.056 s | 0.314 s (+456.1%) | 0.141 s (-55.3%; +148.8%) |
| Testify Direct | `-race` | 32 | 0.063 s | 0.388 s (+520.6%) | 0.132 s (-66.1%; +110.3%) |
| Testify Direct | `-cover` | 4 | 0.060 s | 0.385 s (+538.2%) | 0.161 s (-58.2%; +166.7%) |
| Testify Direct | `-cover` | 32 | 0.068 s | 0.472 s (+597.8%) | 0.139 s (-70.6%; +105.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.365 s (+451.6%) | 0.165 s (-54.9%; +148.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.067 s | 0.415 s (+516.9%) | 0.134 s (-67.8%; +98.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.065 s | 0.365 s (+459.7%) | 0.140 s (-61.7%; +114.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.064 s | 0.386 s (+507.2%) | 0.133 s (-65.6%; +109.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.322 s (+401.0%) | 0.127 s (-60.5%; +97.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.366 s (+469.1%) | 0.137 s (-62.7%; +112.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.319 s (+407.9%) | 0.148 s (-53.8%; +134.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.066 s | 0.366 s (+458.5%) | 0.142 s (-61.3%; +116.0%) |
| Testify External | `none` | 4 | 0.059 s | 0.314 s (+434.2%) | 0.120 s (-61.9%; +103.4%) |
| Testify External | `none` | 32 | 0.059 s | 0.358 s (+502.3%) | 0.130 s (-63.6%; +119.0%) |
| Testify External | `-race` | 4 | 0.058 s | 0.312 s (+438.9%) | 0.118 s (-62.0%; +104.6%) |
| Testify External | `-race` | 32 | 0.062 s | 0.354 s (+474.6%) | 0.134 s (-62.2%; +117.4%) |
| Testify External | `-cover` | 4 | 0.062 s | 0.376 s (+507.5%) | 0.121 s (-67.9%; +94.9%) |
| Testify External | `-cover` | 32 | 0.063 s | 0.435 s (+589.3%) | 0.131 s (-69.9%; +107.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.062 s | 0.335 s (+437.0%) | 0.122 s (-63.6%; +95.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.063 s | 0.394 s (+525.3%) | 0.133 s (-66.2%; +111.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.060 s | 0.341 s (+465.5%) | 0.121 s (-64.4%; +101.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.066 s | 0.395 s (+502.8%) | 0.134 s (-66.1%; +104.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.323 s (+404.0%) | 0.128 s (-60.4%; +99.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.364 s (+468.6%) | 0.137 s (-62.4%; +113.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.320 s (+406.8%) | 0.125 s (-61.0%; +97.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.368 s (+476.1%) | 0.136 s (-62.9%; +113.6%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.158 s | 5.302 s (+357.8%) | 2.224 s (-58.1%; +92.0%) |
| Gin | `none` | 32 | 1.213 s | 5.372 s (+342.7%) | 1.858 s (-65.4%; +53.1%) |
| Chi | `none` | 4 | 0.266 s | 1.810 s (+580.4%) | 0.393 s (-78.3%; +47.6%) |
| Chi | `none` | 32 | 0.272 s | 1.759 s (+547.0%) | 0.411 s (-76.6%; +51.3%) |
| Gin | `-race` | 4 | 1.987 s | 7.649 s (+284.9%) | 2.818 s (-63.2%; +41.8%) |
| Gin | `-race` | 32 | 1.949 s | 8.048 s (+313.0%) | 2.848 s (-64.6%; +46.1%) |
| Gin | `-cover` | 4 | 1.086 s | 5.733 s (+427.9%) | 2.616 s (-54.4%; +140.9%) |
| Gin | `-cover` | 32 | 1.143 s | 6.131 s (+436.2%) | 2.540 s (-58.6%; +122.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.245 s | 5.492 s (+341.0%) | 1.926 s (-64.9%; +54.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.281 s | 5.750 s (+349.0%) | 1.802 s (-68.7%; +40.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.012 s | 8.356 s (+315.3%) | 4.800 s (-42.6%; +138.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.258 s | 8.331 s (+268.9%) | 2.749 s (-67.0%; +21.7%) |
| Chi | `-race` | 4 | 0.361 s | 2.053 s (+468.9%) | 0.632 s (-69.2%; +75.0%) |
| Chi | `-race` | 32 | 0.365 s | 2.021 s (+453.6%) | 0.822 s (-59.3%; +125.1%) |
| Chi | `-cover` | 4 | 0.290 s | 2.012 s (+593.0%) | 0.403 s (-79.9%; +39.0%) |
| Chi | `-cover` | 32 | 0.285 s | 2.097 s (+636.1%) | 0.425 s (-79.7%; +49.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.287 s | 2.012 s (+600.1%) | 0.454 s (-77.5%; +57.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.977 s (+580.2%) | 0.442 s (-77.6%; +52.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.384 s | 2.132 s (+454.8%) | 0.735 s (-65.5%; +91.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.424 s | 2.296 s (+441.7%) | 0.682 s (-70.3%; +60.9%) |
| Testify Direct | `none` | 4 | 0.246 s | 1.622 s (+560.1%) | 0.374 s (-76.9%; +52.3%) |
| Testify Direct | `none` | 32 | 0.270 s | 1.795 s (+563.7%) | 0.395 s (-78.0%; +46.0%) |
| Testify Direct | `-race` | 4 | 0.335 s | 1.839 s (+449.3%) | 4.091 s (+122.5%; +1122.4%) |
| Testify Direct | `-race` | 32 | 0.381 s | 2.030 s (+432.5%) | 0.696 s (-65.7%; +82.4%) |
| Testify Direct | `-cover` | 4 | 0.271 s | 1.895 s (+599.4%) | 0.444 s (-76.6%; +63.7%) |
| Testify Direct | `-cover` | 32 | 0.291 s | 1.989 s (+582.3%) | 0.427 s (-78.5%; +46.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.304 s | 1.903 s (+526.8%) | 0.413 s (-78.3%; +35.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.965 s (+576.0%) | 0.421 s (-78.6%; +44.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.367 s | 1.929 s (+425.0%) | 0.567 s (-70.6%; +54.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.358 s | 2.024 s (+465.9%) | 0.687 s (-66.0%; +92.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.259 s | 1.753 s (+577.6%) | 0.381 s (-78.3%; +47.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.264 s | 1.968 s (+644.8%) | 0.428 s (-78.2%; +62.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.789 s (+426.6%) | 0.670 s (-62.6%; +97.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.359 s | 2.022 s (+463.0%) | 0.625 s (-69.1%; +74.0%) |
| Testify External | `none` | 4 | 0.239 s | 1.716 s (+618.1%) | 0.380 s (-77.8%; +59.2%) |
| Testify External | `none` | 32 | 0.256 s | 1.867 s (+629.4%) | 0.426 s (-77.2%; +66.3%) |
| Testify External | `-race` | 4 | 0.328 s | 1.821 s (+456.1%) | 0.567 s (-68.8%; +73.2%) |
| Testify External | `-race` | 32 | 0.341 s | 1.952 s (+472.8%) | 0.542 s (-72.2%; +59.0%) |
| Testify External | `-cover` | 4 | 0.248 s | 1.696 s (+582.5%) | 0.376 s (-77.8%; +51.2%) |
| Testify External | `-cover` | 32 | 0.262 s | 1.960 s (+646.8%) | 0.411 s (-79.0%; +56.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.257 s | 1.700 s (+562.2%) | 0.382 s (-77.5%; +48.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.270 s | 1.956 s (+625.8%) | 0.398 s (-79.7%; +47.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.345 s | 1.920 s (+457.1%) | 0.522 s (-72.8%; +51.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.352 s | 2.055 s (+484.5%) | 0.594 s (-71.1%; +68.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.258 s | 1.762 s (+583.7%) | 0.384 s (-78.2%; +49.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.270 s | 1.895 s (+602.2%) | 0.412 s (-78.2%; +52.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.840 s (+441.7%) | 0.615 s (-66.6%; +81.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.358 s | 2.009 s (+461.4%) | 0.648 s (-67.7%; +81.2%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.280 s | 2.810 s (+119.6%) | 1.368 s (-51.3%; +6.9%) |
| Gin | `none` | 32 | 1.160 s | 2.871 s (+147.6%) | 1.296 s (-54.9%; +11.7%) |
| Chi | `none` | 4 | 0.413 s | 1.794 s (+334.9%) | 0.523 s (-70.8%; +26.8%) |
| Chi | `none` | 32 | 0.409 s | 1.901 s (+364.2%) | 0.528 s (-72.2%; +29.0%) |
| Gin | `-race` | 4 | 2.120 s | 3.873 s (+82.7%) | 2.323 s (-40.0%; +9.6%) |
| Gin | `-race` | 32 | 2.182 s | 4.379 s (+100.7%) | 2.319 s (-47.0%; +6.3%) |
| Gin | `-cover` | 4 | 1.440 s | 3.115 s (+116.4%) | 1.854 s (-40.5%; +28.8%) |
| Gin | `-cover` | 32 | 1.338 s | 3.272 s (+144.6%) | 2.292 s (-30.0%; +71.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.533 s | 3.126 s (+103.9%) | 1.643 s (-47.4%; +7.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.351 s | 3.375 s (+149.8%) | 1.536 s (-54.5%; +13.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.451 s | 4.147 s (+69.2%) | 2.536 s (-38.8%; +3.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.473 s | 4.708 s (+90.4%) | 2.759 s (-41.4%; +11.6%) |
| Chi | `-race` | 4 | 0.723 s | 2.295 s (+217.5%) | 0.895 s (-61.0%; +23.9%) |
| Chi | `-race` | 32 | 0.750 s | 2.399 s (+219.9%) | 0.906 s (-62.2%; +20.9%) |
| Chi | `-cover` | 4 | 0.475 s | 2.002 s (+321.6%) | 0.583 s (-70.9%; +22.8%) |
| Chi | `-cover` | 32 | 0.465 s | 1.910 s (+310.7%) | 0.589 s (-69.1%; +26.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.479 s | 1.961 s (+309.1%) | 0.648 s (-66.9%; +35.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.487 s | 1.974 s (+305.1%) | 0.593 s (-69.9%; +21.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.818 s | 2.226 s (+172.2%) | 0.987 s (-55.6%; +20.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.851 s | 2.501 s (+194.0%) | 1.018 s (-59.3%; +19.7%) |
| Testify Direct | `none` | 4 | 0.285 s | 2.451 s (+760.6%) | 0.405 s (-83.5%; +42.1%) |
| Testify Direct | `none` | 32 | 0.287 s | 1.882 s (+554.8%) | 0.429 s (-77.2%; +49.3%) |
| Testify Direct | `-race` | 4 | 0.447 s | 2.297 s (+413.5%) | 3.433 s (+49.5%; +667.5%) |
| Testify Direct | `-race` | 32 | 0.468 s | 2.876 s (+515.0%) | 0.808 s (-71.9%; +72.8%) |
| Testify Direct | `-cover` | 4 | 0.332 s | 2.391 s (+621.1%) | 0.507 s (-78.8%; +53.0%) |
| Testify Direct | `-cover` | 32 | 0.336 s | 2.099 s (+524.0%) | 0.466 s (-77.8%; +38.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 2.076 s (+536.3%) | 0.457 s (-78.0%; +40.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.339 s | 2.229 s (+558.4%) | 0.461 s (-79.3%; +36.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.462 s | 2.537 s (+449.4%) | 0.706 s (-72.2%; +53.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.437 s | 2.183 s (+399.5%) | 0.720 s (-67.0%; +64.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.286 s | 1.915 s (+568.8%) | 0.433 s (-77.4%; +51.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.303 s | 2.036 s (+571.3%) | 0.470 s (-76.9%; +55.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.423 s | 2.010 s (+374.6%) | 0.709 s (-64.7%; +67.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.449 s | 2.246 s (+400.1%) | 0.695 s (-69.0%; +54.8%) |
| Testify External | `none` | 4 | 0.253 s | 1.742 s (+589.7%) | 0.390 s (-77.6%; +54.3%) |
| Testify External | `none` | 32 | 0.264 s | 1.933 s (+633.3%) | 0.440 s (-77.3%; +66.7%) |
| Testify External | `-race` | 4 | 0.340 s | 1.838 s (+440.7%) | 0.547 s (-70.2%; +61.0%) |
| Testify External | `-race` | 32 | 0.360 s | 2.089 s (+480.9%) | 0.553 s (-73.5%; +53.7%) |
| Testify External | `-cover` | 4 | 0.267 s | 1.868 s (+598.4%) | 0.396 s (-78.8%; +48.2%) |
| Testify External | `-cover` | 32 | 0.275 s | 1.972 s (+615.9%) | 0.417 s (-78.9%; +51.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.280 s | 1.884 s (+572.7%) | 0.392 s (-79.2%; +40.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.286 s | 2.043 s (+614.8%) | 0.418 s (-79.5%; +46.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.354 s | 1.992 s (+463.4%) | 0.549 s (-72.4%; +55.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.383 s | 2.090 s (+445.4%) | 0.613 s (-70.7%; +59.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.274 s | 1.811 s (+560.5%) | 0.390 s (-78.5%; +42.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.285 s | 1.993 s (+598.4%) | 0.418 s (-79.0%; +46.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.354 s | 1.928 s (+444.1%) | 0.539 s (-72.0%; +52.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.389 s | 2.126 s (+447.2%) | 0.593 s (-72.1%; +52.7%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.760 s | 1.161 s (+52.8%) | 0.817 s (-29.6%; +7.6%) |
| Gin | `none` | 32 | 0.651 s | 1.110 s (+70.4%) | 0.709 s (-36.2%; +8.8%) |
| Chi | `none` | 4 | 0.266 s | 0.657 s (+147.5%) | 0.318 s (-51.6%; +19.8%) |
| Chi | `none` | 32 | 0.250 s | 0.682 s (+173.1%) | 0.339 s (-50.2%; +36.0%) |
| Gin | `-race` | 4 | 1.368 s | 1.769 s (+29.2%) | 1.438 s (-18.7%; +5.1%) |
| Gin | `-race` | 32 | 1.411 s | 1.821 s (+29.1%) | 1.469 s (-19.3%; +4.1%) |
| Gin | `-cover` | 4 | 0.944 s | 1.579 s (+67.2%) | 0.989 s (-37.4%; +4.8%) |
| Gin | `-cover` | 32 | 0.823 s | 1.501 s (+82.4%) | 1.801 s (+20.0%; +118.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.982 s | 1.581 s (+61.1%) | 1.067 s (-32.5%; +8.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.868 s | 1.467 s (+68.9%) | 0.949 s (-35.3%; +9.3%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.665 s | 2.253 s (+35.3%) | 1.730 s (-23.2%; +3.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.667 s | 2.338 s (+40.2%) | 1.809 s (-22.6%; +8.5%) |
| Chi | `-race` | 4 | 0.491 s | 0.872 s (+77.6%) | 0.596 s (-31.7%; +21.3%) |
| Chi | `-race` | 32 | 0.520 s | 0.939 s (+80.5%) | 0.587 s (-37.5%; +12.8%) |
| Chi | `-cover` | 4 | 0.315 s | 0.795 s (+152.1%) | 0.375 s (-52.8%; +19.1%) |
| Chi | `-cover` | 32 | 0.299 s | 0.799 s (+167.1%) | 0.367 s (-54.1%; +22.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 0.774 s (+137.3%) | 0.441 s (-43.0%; +35.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.331 s | 0.796 s (+140.5%) | 0.381 s (-52.2%; +15.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.572 s | 0.996 s (+74.2%) | 0.664 s (-33.3%; +16.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.595 s | 2.099 s (+252.5%) | 0.676 s (-67.8%; +13.6%) |
| Testify Direct | `none` | 4 | 0.102 s | 0.656 s (+542.8%) | 0.162 s (-75.3%; +58.9%) |
| Testify Direct | `none` | 32 | 0.099 s | 1.002 s (+914.9%) | 0.171 s (-82.9%; +73.4%) |
| Testify Direct | `-race` | 4 | 0.154 s | 0.669 s (+334.7%) | 0.220 s (-67.1%; +43.1%) |
| Testify Direct | `-race` | 32 | 0.167 s | 0.736 s (+340.0%) | 0.269 s (-63.4%; +61.2%) |
| Testify Direct | `-cover` | 4 | 0.121 s | 0.698 s (+477.6%) | 0.203 s (-70.8%; +68.4%) |
| Testify Direct | `-cover` | 32 | 0.119 s | 0.775 s (+554.0%) | 0.190 s (-75.5%; +60.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.124 s | 1.310 s (+958.5%) | 0.191 s (-85.4%; +54.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.120 s | 0.728 s (+507.7%) | 0.186 s (-74.4%; +55.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.164 s | 0.776 s (+373.3%) | 0.234 s (-69.9%; +42.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.161 s | 0.725 s (+348.9%) | 0.231 s (-68.2%; +42.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.107 s | 0.589 s (+450.0%) | 0.177 s (-70.0%; +65.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.105 s | 0.650 s (+518.9%) | 0.181 s (-72.2%; +72.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.150 s | 0.619 s (+312.1%) | 0.210 s (-66.1%; +39.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.158 s | 0.706 s (+347.7%) | 0.244 s (-65.4%; +55.0%) |
| Testify External | `none` | 4 | 0.078 s | 0.546 s (+598.3%) | 0.137 s (-75.0%; +74.8%) |
| Testify External | `none` | 32 | 0.080 s | 0.614 s (+667.6%) | 0.163 s (-73.5%; +103.4%) |
| Testify External | `-race` | 4 | 0.078 s | 0.549 s (+607.0%) | 0.161 s (-70.7%; +106.8%) |
| Testify External | `-race` | 32 | 0.081 s | 0.611 s (+654.1%) | 0.151 s (-75.2%; +86.9%) |
| Testify External | `-cover` | 4 | 0.081 s | 0.624 s (+667.1%) | 0.144 s (-77.0%; +76.6%) |
| Testify External | `-cover` | 32 | 0.086 s | 1.060 s (+1136.5%) | 0.159 s (-85.0%; +85.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.086 s | 0.573 s (+568.0%) | 0.149 s (-74.0%; +73.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.088 s | 0.653 s (+644.6%) | 0.156 s (-76.1%; +78.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.085 s | 0.592 s (+596.4%) | 0.145 s (-75.6%; +70.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.089 s | 0.651 s (+631.3%) | 0.163 s (-75.0%; +82.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.082 s | 0.554 s (+572.3%) | 0.143 s (-74.2%; +73.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.084 s | 0.630 s (+650.7%) | 0.153 s (-75.8%; +81.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.084 s | 0.772 s (+821.5%) | 0.145 s (-81.2%; +73.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.086 s | 0.623 s (+622.8%) | 0.157 s (-74.8%; +82.4%) |

The original convergence control is retained evidence for its collection date. It is not a new host-stability check.
