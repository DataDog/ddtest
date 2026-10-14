# Compile-only comparisons

Mini: `97ec6e32f98856c7af982e76410b12f440cf2fd1`, collected 2026-10-14. Native/Orchestrion: retained from 2026-10-04 at `9d4786fbd27f573bb68c5516bf23b336e8d7bdaa`.

All variants use `go version go1.27.1 linux/amd64`, the recorded subject versions, flags, CPU affinity and repetition counts. Each observation includes wall time, aggregate cgroup CPU and memory, command and exit status. Cold builds start with an empty Go build cache; module downloads and the OS page cache remain warm. The other scenarios reuse unchanged output, force a fresh link, edit a reachable test body or edit an unused constant.

Mini traces and symbols are checked again. Native and Orchestrion retain their original qualification. Those variants were not rerun. Dates differ, so small timing differences need the recorded ranges and uncertainty. Orchestrion's percentage compares time against Native. Mini's percentages compare Orchestrion first, then Native. All time cells are seconds.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 17.896 s | 51.532 s (+188.0%) | 21.012 s (-59.2%; +17.4%) |
| Gin | `none` | 32 | 10.844 s | 22.679 s (+109.1%) | 11.764 s (-48.1%; +8.5%) |
| Chi | `none` | 4 | 7.764 s | 30.543 s (+293.4%) | 10.845 s (-64.5%; +39.7%) |
| Chi | `none` | 32 | 4.576 s | 16.765 s (+266.3%) | 6.719 s (-59.9%; +46.8%) |
| Gin | `-race` | 4 | 27.873 s | 56.274 s (+101.9%) | 29.407 s (-47.7%; +5.5%) |
| Gin | `-race` | 32 | 23.244 s | 34.631 s (+49.0%) | 24.179 s (-30.2%; +4.0%) |
| Gin | `-cover` | 4 | 18.781 s | 66.330 s (+253.2%) | 21.835 s (-67.1%; +16.3%) |
| Gin | `-cover` | 32 | 10.933 s | 29.664 s (+171.3%) | 12.179 s (-58.9%; +11.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 19.328 s | 56.685 s (+193.3%) | 21.975 s (-61.2%; +13.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 12.161 s | 27.508 s (+126.2%) | 12.202 s (-55.6%; +0.3%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 29.271 s | 68.432 s (+133.8%) | 31.461 s (-54.0%; +7.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 24.174 s | 39.394 s (+63.0%) | 25.094 s (-36.3%; +3.8%) |
| Chi | `-race` | 4 | 11.393 s | 37.963 s (+233.2%) | 15.283 s (-59.7%; +34.1%) |
| Chi | `-race` | 32 | 9.318 s | 27.565 s (+195.8%) | 12.870 s (-53.3%; +38.1%) |
| Chi | `-cover` | 4 | 7.934 s | 38.145 s (+380.8%) | 11.055 s (-71.0%; +39.3%) |
| Chi | `-cover` | 32 | 4.815 s | 19.639 s (+307.9%) | 6.846 s (-65.1%; +42.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 8.179 s | 33.966 s (+315.3%) | 11.152 s (-67.2%; +36.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 4.946 s | 17.755 s (+259.0%) | 6.830 s (-61.5%; +38.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 11.745 s | 41.717 s (+255.2%) | 15.563 s (-62.7%; +32.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.538 s | 33.637 s (+252.7%) | 13.007 s (-61.3%; +36.4%) |
| Testify Direct | `none` | 4 | 8.078 s | 33.039 s (+309.0%) | 11.209 s (-66.1%; +38.8%) |
| Testify Direct | `none` | 32 | 4.867 s | 20.067 s (+312.3%) | 6.750 s (-66.4%; +38.7%) |
| Testify Direct | `-race` | 4 | 12.691 s | 46.494 s (+266.4%) | 15.921 s (-65.8%; +25.5%) |
| Testify Direct | `-race` | 32 | 10.094 s | 33.293 s (+229.8%) | 12.637 s (-62.0%; +25.2%) |
| Testify Direct | `-cover` | 4 | 8.650 s | 39.414 s (+355.7%) | 11.396 s (-71.1%; +31.7%) |
| Testify Direct | `-cover` | 32 | 5.337 s | 21.922 s (+310.8%) | 6.832 s (-68.8%; +28.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 9.083 s | 34.215 s (+276.7%) | 11.344 s (-66.8%; +24.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 5.248 s | 28.446 s (+442.0%) | 6.773 s (-76.2%; +29.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 13.186 s | 53.793 s (+307.9%) | 16.089 s (-70.1%; +22.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 10.073 s | 29.693 s (+194.8%) | 12.774 s (-57.0%; +26.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.603 s | 30.824 s (+258.3%) | 11.413 s (-63.0%; +32.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.913 s | 20.015 s (+307.4%) | 6.841 s (-65.8%; +39.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.509 s | 39.152 s (+213.0%) | 16.143 s (-58.8%; +29.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.825 s | 29.736 s (+202.6%) | 12.802 s (-56.9%; +30.3%) |
| Testify External | `none` | 4 | 8.381 s | 30.092 s (+259.0%) | 11.187 s (-62.8%; +33.5%) |
| Testify External | `none` | 32 | 4.810 s | 18.167 s (+277.7%) | 6.745 s (-62.9%; +40.2%) |
| Testify External | `-race` | 4 | 12.450 s | 37.839 s (+203.9%) | 15.826 s (-58.2%; +27.1%) |
| Testify External | `-race` | 32 | 9.726 s | 27.976 s (+187.6%) | 12.620 s (-54.9%; +29.8%) |
| Testify External | `-cover` | 4 | 8.407 s | 34.683 s (+312.6%) | 11.419 s (-67.1%; +35.8%) |
| Testify External | `-cover` | 32 | 4.861 s | 21.347 s (+339.2%) | 6.797 s (-68.2%; +39.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 8.507 s | 31.696 s (+272.6%) | 11.373 s (-64.1%; +33.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 5.078 s | 18.402 s (+262.4%) | 6.850 s (-62.8%; +34.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 12.936 s | 40.899 s (+216.2%) | 16.004 s (-60.9%; +23.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.861 s | 27.088 s (+174.7%) | 12.668 s (-53.2%; +28.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.465 s | 29.977 s (+254.1%) | 11.409 s (-61.9%; +34.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.869 s | 17.912 s (+267.8%) | 6.780 s (-62.1%; +39.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.725 s | 38.705 s (+204.2%) | 16.144 s (-58.3%; +26.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.847 s | 28.139 s (+185.8%) | 12.673 s (-55.0%; +28.7%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.098 s | 0.366 s (+273.5%) | 0.189 s (-48.2%; +93.3%) |
| Gin | `none` | 32 | 0.097 s | 0.410 s (+322.2%) | 0.207 s (-49.4%; +113.7%) |
| Chi | `none` | 4 | 0.063 s | 0.329 s (+421.5%) | 0.118 s (-64.0%; +87.7%) |
| Chi | `none` | 32 | 0.067 s | 0.385 s (+476.1%) | 0.128 s (-66.6%; +92.3%) |
| Gin | `-race` | 4 | 0.098 s | 0.370 s (+279.1%) | 0.204 s (-45.0%; +108.5%) |
| Gin | `-race` | 32 | 0.098 s | 0.413 s (+323.5%) | 0.223 s (-45.9%; +129.2%) |
| Gin | `-cover` | 4 | 0.127 s | 0.514 s (+305.2%) | 0.228 s (-55.7%; +79.7%) |
| Gin | `-cover` | 32 | 0.125 s | 0.570 s (+358.0%) | 0.240 s (-57.9%; +93.0%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.130 s | 0.432 s (+231.9%) | 0.233 s (-46.0%; +79.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.464 s (+264.8%) | 0.251 s (-45.9%; +97.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.144 s | 0.466 s (+224.4%) | 0.244 s (-47.7%; +69.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.471 s (+270.6%) | 0.256 s (-45.6%; +101.5%) |
| Chi | `-race` | 4 | 0.062 s | 0.327 s (+428.8%) | 0.130 s (-60.1%; +111.0%) |
| Chi | `-race` | 32 | 0.067 s | 0.387 s (+476.2%) | 0.142 s (-63.4%; +110.6%) |
| Chi | `-cover` | 4 | 0.066 s | 0.419 s (+536.9%) | 0.126 s (-70.0%; +91.2%) |
| Chi | `-cover` | 32 | 0.070 s | 0.494 s (+605.6%) | 0.137 s (-72.4%; +95.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.371 s (+462.5%) | 0.127 s (-65.7%; +92.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.071 s | 0.430 s (+502.4%) | 0.139 s (-67.8%; +94.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.067 s | 0.370 s (+452.3%) | 0.139 s (-62.5%; +107.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.072 s | 0.432 s (+503.9%) | 0.149 s (-65.5%; +108.1%) |
| Testify Direct | `none` | 4 | 0.059 s | 0.314 s (+436.9%) | 0.139 s (-55.7%; +137.9%) |
| Testify Direct | `none` | 32 | 0.061 s | 0.358 s (+488.3%) | 0.152 s (-57.5%; +150.0%) |
| Testify Direct | `-race` | 4 | 0.056 s | 0.314 s (+456.1%) | 0.176 s (-43.8%; +212.5%) |
| Testify Direct | `-race` | 32 | 0.063 s | 0.388 s (+520.6%) | 0.166 s (-57.4%; +164.6%) |
| Testify Direct | `-cover` | 4 | 0.060 s | 0.385 s (+538.2%) | 0.146 s (-62.1%; +141.7%) |
| Testify Direct | `-cover` | 32 | 0.068 s | 0.472 s (+597.8%) | 0.161 s (-65.9%; +138.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.365 s (+451.6%) | 0.148 s (-59.5%; +123.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.067 s | 0.415 s (+516.9%) | 0.162 s (-61.1%; +140.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.065 s | 0.365 s (+459.7%) | 0.159 s (-56.6%; +142.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.064 s | 0.386 s (+507.2%) | 0.173 s (-55.2%; +172.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.322 s (+401.0%) | 0.147 s (-54.4%; +128.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.366 s (+469.1%) | 0.162 s (-55.9%; +151.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.319 s (+407.9%) | 0.157 s (-50.8%; +149.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.066 s | 0.366 s (+458.5%) | 0.169 s (-54.0%; +157.0%) |
| Testify External | `none` | 4 | 0.059 s | 0.314 s (+434.2%) | 0.139 s (-55.7%; +136.8%) |
| Testify External | `none` | 32 | 0.059 s | 0.358 s (+502.3%) | 0.154 s (-57.0%; +159.1%) |
| Testify External | `-race` | 4 | 0.058 s | 0.312 s (+438.9%) | 0.148 s (-52.4%; +156.7%) |
| Testify External | `-race` | 32 | 0.062 s | 0.354 s (+474.6%) | 0.164 s (-53.7%; +166.2%) |
| Testify External | `-cover` | 4 | 0.062 s | 0.376 s (+507.5%) | 0.148 s (-60.6%; +139.3%) |
| Testify External | `-cover` | 32 | 0.063 s | 0.435 s (+589.3%) | 0.162 s (-62.9%; +155.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.062 s | 0.335 s (+437.0%) | 0.149 s (-55.6%; +138.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.063 s | 0.394 s (+525.3%) | 0.159 s (-59.6%; +152.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.060 s | 0.341 s (+465.5%) | 0.157 s (-53.9%; +160.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.066 s | 0.395 s (+502.8%) | 0.171 s (-56.8%; +160.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.323 s (+404.0%) | 0.149 s (-53.9%; +132.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.364 s (+468.6%) | 0.158 s (-56.6%; +146.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.320 s (+406.8%) | 0.160 s (-49.9%; +154.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.368 s (+476.1%) | 0.170 s (-53.7%; +166.5%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.158 s | 5.302 s (+357.8%) | 1.846 s (-65.2%; +59.4%) |
| Gin | `none` | 32 | 1.213 s | 5.372 s (+342.7%) | 1.976 s (-63.2%; +62.9%) |
| Chi | `none` | 4 | 0.266 s | 1.810 s (+580.4%) | 0.413 s (-77.2%; +55.4%) |
| Chi | `none` | 32 | 0.272 s | 1.759 s (+547.0%) | 0.425 s (-75.8%; +56.5%) |
| Gin | `-race` | 4 | 1.987 s | 7.649 s (+284.9%) | 3.146 s (-58.9%; +58.3%) |
| Gin | `-race` | 32 | 1.949 s | 8.048 s (+313.0%) | 2.861 s (-64.4%; +46.8%) |
| Gin | `-cover` | 4 | 1.086 s | 5.733 s (+427.9%) | 1.891 s (-67.0%; +74.1%) |
| Gin | `-cover` | 32 | 1.143 s | 6.131 s (+436.2%) | 1.993 s (-67.5%; +74.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.245 s | 5.492 s (+341.0%) | 1.884 s (-65.7%; +51.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.281 s | 5.750 s (+349.0%) | 1.706 s (-70.3%; +33.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.012 s | 8.356 s (+315.3%) | 2.811 s (-66.4%; +39.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.258 s | 8.331 s (+268.9%) | 2.641 s (-68.3%; +16.9%) |
| Chi | `-race` | 4 | 0.361 s | 2.053 s (+468.9%) | 0.679 s (-66.9%; +88.3%) |
| Chi | `-race` | 32 | 0.365 s | 2.021 s (+453.6%) | 0.618 s (-69.4%; +69.4%) |
| Chi | `-cover` | 4 | 0.290 s | 2.012 s (+593.0%) | 0.439 s (-78.2%; +51.0%) |
| Chi | `-cover` | 32 | 0.285 s | 2.097 s (+636.1%) | 0.441 s (-79.0%; +54.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.287 s | 2.012 s (+600.1%) | 0.493 s (-75.5%; +71.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.977 s (+580.2%) | 0.438 s (-77.8%; +50.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.384 s | 2.132 s (+454.8%) | 0.630 s (-70.4%; +64.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.424 s | 2.296 s (+441.7%) | 0.774 s (-66.3%; +82.6%) |
| Testify Direct | `none` | 4 | 0.246 s | 1.622 s (+560.1%) | 0.417 s (-74.3%; +69.6%) |
| Testify Direct | `none` | 32 | 0.270 s | 1.795 s (+563.7%) | 0.454 s (-74.7%; +67.9%) |
| Testify Direct | `-race` | 4 | 0.335 s | 1.839 s (+449.3%) | 0.560 s (-69.6%; +67.2%) |
| Testify Direct | `-race` | 32 | 0.381 s | 2.030 s (+432.5%) | 0.590 s (-70.9%; +54.8%) |
| Testify Direct | `-cover` | 4 | 0.271 s | 1.895 s (+599.4%) | 0.435 s (-77.0%; +60.7%) |
| Testify Direct | `-cover` | 32 | 0.291 s | 1.989 s (+582.3%) | 0.456 s (-77.1%; +56.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.304 s | 1.903 s (+526.8%) | 0.436 s (-77.1%; +43.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.965 s (+576.0%) | 0.455 s (-76.9%; +56.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.367 s | 1.929 s (+425.0%) | 0.590 s (-69.4%; +60.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.358 s | 2.024 s (+465.9%) | 0.611 s (-69.8%; +70.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.259 s | 1.753 s (+577.6%) | 0.436 s (-75.1%; +68.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.264 s | 1.968 s (+644.8%) | 0.463 s (-76.5%; +75.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.789 s (+426.6%) | 0.581 s (-67.5%; +71.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.359 s | 2.022 s (+463.0%) | 0.610 s (-69.8%; +69.8%) |
| Testify External | `none` | 4 | 0.239 s | 1.716 s (+618.1%) | 0.415 s (-75.8%; +73.6%) |
| Testify External | `none` | 32 | 0.256 s | 1.867 s (+629.4%) | 0.441 s (-76.4%; +72.1%) |
| Testify External | `-race` | 4 | 0.328 s | 1.821 s (+456.1%) | 0.567 s (-68.9%; +73.1%) |
| Testify External | `-race` | 32 | 0.341 s | 1.952 s (+472.8%) | 0.589 s (-69.8%; +72.8%) |
| Testify External | `-cover` | 4 | 0.248 s | 1.696 s (+582.5%) | 0.439 s (-74.1%; +76.5%) |
| Testify External | `-cover` | 32 | 0.262 s | 1.960 s (+646.8%) | 0.458 s (-76.7%; +74.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.257 s | 1.700 s (+562.2%) | 0.468 s (-72.5%; +82.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.270 s | 1.956 s (+625.8%) | 0.462 s (-76.4%; +71.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.345 s | 1.920 s (+457.1%) | 0.593 s (-69.1%; +72.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.352 s | 2.055 s (+484.5%) | 0.605 s (-70.6%; +72.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.258 s | 1.762 s (+583.7%) | 0.435 s (-75.3%; +68.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.270 s | 1.895 s (+602.2%) | 0.462 s (-75.6%; +71.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.840 s (+441.7%) | 0.588 s (-68.1%; +73.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.358 s | 2.009 s (+461.4%) | 0.615 s (-69.4%; +71.7%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.280 s | 2.810 s (+119.6%) | 1.399 s (-50.2%; +9.4%) |
| Gin | `none` | 32 | 1.160 s | 2.871 s (+147.6%) | 1.411 s (-50.9%; +21.7%) |
| Chi | `none` | 4 | 0.413 s | 1.794 s (+334.9%) | 0.577 s (-67.8%; +39.8%) |
| Chi | `none` | 32 | 0.409 s | 1.901 s (+364.2%) | 0.580 s (-69.5%; +41.6%) |
| Gin | `-race` | 4 | 2.120 s | 3.873 s (+82.7%) | 2.524 s (-34.8%; +19.0%) |
| Gin | `-race` | 32 | 2.182 s | 4.379 s (+100.7%) | 2.488 s (-43.2%; +14.1%) |
| Gin | `-cover` | 4 | 1.440 s | 3.115 s (+116.4%) | 1.770 s (-43.2%; +22.9%) |
| Gin | `-cover` | 32 | 1.338 s | 3.272 s (+144.6%) | 1.650 s (-49.6%; +23.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.533 s | 3.126 s (+103.9%) | 1.606 s (-48.6%; +4.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.351 s | 3.375 s (+149.8%) | 1.649 s (-51.1%; +22.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.451 s | 4.147 s (+69.2%) | 2.982 s (-28.1%; +21.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.473 s | 4.708 s (+90.4%) | 2.880 s (-38.8%; +16.5%) |
| Chi | `-race` | 4 | 0.723 s | 2.295 s (+217.5%) | 0.929 s (-59.5%; +28.5%) |
| Chi | `-race` | 32 | 0.750 s | 2.399 s (+219.9%) | 0.956 s (-60.1%; +27.5%) |
| Chi | `-cover` | 4 | 0.475 s | 2.002 s (+321.6%) | 0.645 s (-67.8%; +35.9%) |
| Chi | `-cover` | 32 | 0.465 s | 1.910 s (+310.7%) | 0.630 s (-67.0%; +35.5%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.479 s | 1.961 s (+309.1%) | 0.648 s (-67.0%; +35.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.487 s | 1.974 s (+305.1%) | 0.660 s (-66.6%; +35.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.818 s | 2.226 s (+172.2%) | 1.022 s (-54.1%; +25.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.851 s | 2.501 s (+194.0%) | 1.048 s (-58.1%; +23.2%) |
| Testify Direct | `none` | 4 | 0.285 s | 2.451 s (+760.6%) | 0.463 s (-81.1%; +62.6%) |
| Testify Direct | `none` | 32 | 0.287 s | 1.882 s (+554.8%) | 0.479 s (-74.5%; +66.8%) |
| Testify Direct | `-race` | 4 | 0.447 s | 2.297 s (+413.5%) | 0.651 s (-71.6%; +45.6%) |
| Testify Direct | `-race` | 32 | 0.468 s | 2.876 s (+515.0%) | 0.695 s (-75.8%; +48.6%) |
| Testify Direct | `-cover` | 4 | 0.332 s | 2.391 s (+621.1%) | 0.479 s (-80.0%; +44.6%) |
| Testify Direct | `-cover` | 32 | 0.336 s | 2.099 s (+524.0%) | 0.507 s (-75.8%; +50.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 2.076 s (+536.3%) | 0.485 s (-76.6%; +48.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.339 s | 2.229 s (+558.4%) | 0.506 s (-77.3%; +49.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.462 s | 2.537 s (+449.4%) | 0.669 s (-73.6%; +44.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.437 s | 2.183 s (+399.5%) | 0.705 s (-67.7%; +61.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.286 s | 1.915 s (+568.8%) | 0.491 s (-74.4%; +71.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.303 s | 2.036 s (+571.3%) | 0.498 s (-75.6%; +64.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.423 s | 2.010 s (+374.6%) | 0.672 s (-66.6%; +58.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.449 s | 2.246 s (+400.1%) | 0.700 s (-68.8%; +55.8%) |
| Testify External | `none` | 4 | 0.253 s | 1.742 s (+589.7%) | 0.432 s (-75.2%; +70.9%) |
| Testify External | `none` | 32 | 0.264 s | 1.933 s (+633.3%) | 0.462 s (-76.1%; +75.1%) |
| Testify External | `-race` | 4 | 0.340 s | 1.838 s (+440.7%) | 0.589 s (-67.9%; +73.3%) |
| Testify External | `-race` | 32 | 0.360 s | 2.089 s (+480.9%) | 0.621 s (-70.3%; +72.6%) |
| Testify External | `-cover` | 4 | 0.267 s | 1.868 s (+598.4%) | 0.458 s (-75.5%; +71.3%) |
| Testify External | `-cover` | 32 | 0.275 s | 1.972 s (+615.9%) | 0.474 s (-76.0%; +72.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.280 s | 1.884 s (+572.7%) | 0.511 s (-72.9%; +82.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.286 s | 2.043 s (+614.8%) | 0.478 s (-76.6%; +67.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.354 s | 1.992 s (+463.4%) | 0.612 s (-69.3%; +73.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.383 s | 2.090 s (+445.4%) | 0.623 s (-70.2%; +62.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.274 s | 1.811 s (+560.5%) | 0.456 s (-74.8%; +66.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.285 s | 1.993 s (+598.4%) | 0.473 s (-76.3%; +65.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.354 s | 1.928 s (+444.1%) | 0.587 s (-69.6%; +65.6%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.389 s | 2.126 s (+447.2%) | 0.644 s (-69.7%; +65.8%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.760 s | 1.161 s (+52.8%) | 0.940 s (-19.0%; +23.7%) |
| Gin | `none` | 32 | 0.651 s | 1.110 s (+70.4%) | 0.838 s (-24.5%; +28.7%) |
| Chi | `none` | 4 | 0.266 s | 0.657 s (+147.5%) | 0.363 s (-44.8%; +36.6%) |
| Chi | `none` | 32 | 0.250 s | 0.682 s (+173.1%) | 0.375 s (-45.0%; +50.1%) |
| Gin | `-race` | 4 | 1.368 s | 1.769 s (+29.2%) | 1.586 s (-10.3%; +15.9%) |
| Gin | `-race` | 32 | 1.411 s | 1.821 s (+29.1%) | 1.641 s (-9.9%; +16.3%) |
| Gin | `-cover` | 4 | 0.944 s | 1.579 s (+67.2%) | 1.127 s (-28.7%; +19.3%) |
| Gin | `-cover` | 32 | 0.823 s | 1.501 s (+82.4%) | 1.006 s (-33.0%; +22.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.982 s | 1.581 s (+61.1%) | 1.206 s (-23.7%; +22.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.868 s | 1.467 s (+68.9%) | 1.086 s (-26.0%; +25.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.665 s | 2.253 s (+35.3%) | 1.904 s (-15.5%; +14.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.667 s | 2.338 s (+40.2%) | 1.964 s (-16.0%; +17.8%) |
| Chi | `-race` | 4 | 0.491 s | 0.872 s (+77.6%) | 0.597 s (-31.5%; +21.6%) |
| Chi | `-race` | 32 | 0.520 s | 0.939 s (+80.5%) | 0.636 s (-32.3%; +22.1%) |
| Chi | `-cover` | 4 | 0.315 s | 0.795 s (+152.1%) | 0.423 s (-46.7%; +34.4%) |
| Chi | `-cover` | 32 | 0.299 s | 0.799 s (+167.1%) | 0.412 s (-48.4%; +37.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 0.774 s (+137.3%) | 0.434 s (-43.9%; +33.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.331 s | 0.796 s (+140.5%) | 0.432 s (-45.7%; +30.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.572 s | 0.996 s (+74.2%) | 0.686 s (-31.2%; +19.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.595 s | 2.099 s (+252.5%) | 0.728 s (-65.3%; +22.3%) |
| Testify Direct | `none` | 4 | 0.102 s | 0.656 s (+542.8%) | 0.199 s (-69.7%; +94.6%) |
| Testify Direct | `none` | 32 | 0.099 s | 1.002 s (+914.9%) | 0.212 s (-78.9%; +114.6%) |
| Testify Direct | `-race` | 4 | 0.154 s | 0.669 s (+334.7%) | 0.247 s (-63.0%; +60.8%) |
| Testify Direct | `-race` | 32 | 0.167 s | 0.736 s (+340.0%) | 0.271 s (-63.1%; +62.3%) |
| Testify Direct | `-cover` | 4 | 0.121 s | 0.698 s (+477.6%) | 0.206 s (-70.4%; +70.7%) |
| Testify Direct | `-cover` | 32 | 0.119 s | 0.775 s (+554.0%) | 0.220 s (-71.6%; +85.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.124 s | 1.310 s (+958.5%) | 0.211 s (-83.9%; +70.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.120 s | 0.728 s (+507.7%) | 0.223 s (-69.3%; +86.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.164 s | 0.776 s (+373.3%) | 0.264 s (-66.0%; +60.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.161 s | 0.725 s (+348.9%) | 0.290 s (-60.0%; +79.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.107 s | 0.589 s (+450.0%) | 0.204 s (-65.4%; +90.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.105 s | 0.650 s (+518.9%) | 0.214 s (-67.1%; +103.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.150 s | 0.619 s (+312.1%) | 0.262 s (-57.7%; +74.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.158 s | 0.706 s (+347.7%) | 0.285 s (-59.7%; +80.4%) |
| Testify External | `none` | 4 | 0.078 s | 0.546 s (+598.3%) | 0.167 s (-69.5%; +113.0%) |
| Testify External | `none` | 32 | 0.080 s | 0.614 s (+667.6%) | 0.182 s (-70.3%; +127.7%) |
| Testify External | `-race` | 4 | 0.078 s | 0.549 s (+607.0%) | 0.177 s (-67.8%; +127.7%) |
| Testify External | `-race` | 32 | 0.081 s | 0.611 s (+654.1%) | 0.195 s (-68.0%; +141.4%) |
| Testify External | `-cover` | 4 | 0.081 s | 0.624 s (+667.1%) | 0.178 s (-71.4%; +119.2%) |
| Testify External | `-cover` | 32 | 0.086 s | 1.060 s (+1136.5%) | 0.195 s (-81.7%; +126.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.086 s | 0.573 s (+568.0%) | 0.205 s (-64.2%; +139.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.088 s | 0.653 s (+644.6%) | 0.196 s (-70.0%; +123.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.085 s | 0.592 s (+596.4%) | 0.189 s (-68.1%; +121.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.089 s | 0.651 s (+631.3%) | 0.202 s (-68.9%; +127.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.082 s | 0.554 s (+572.3%) | 0.175 s (-68.4%; +112.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.084 s | 0.630 s (+650.7%) | 0.186 s (-70.4%; +122.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.084 s | 0.772 s (+821.5%) | 0.189 s (-75.5%; +125.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.086 s | 0.623 s (+622.8%) | 0.203 s (-67.5%; +135.3%) |

The original convergence control is retained evidence for its collection date. It is not a new host-stability check.
