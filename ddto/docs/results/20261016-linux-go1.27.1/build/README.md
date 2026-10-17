# Compile-only comparisons

Mini: `bfcf898db959aea62e14ef04aedd56d0e4ac226e`, collected 2026-10-16. Native/Orchestrion: retained from 2026-10-04 at `9d4786fbd27f573bb68c5516bf23b336e8d7bdaa`.

All variants use `go version go1.27.1 linux/amd64`, the recorded subject versions, flags, CPU affinity and repetition counts. Each observation includes wall time, aggregate cgroup CPU and memory, command and exit status. Cold builds start with an empty Go build cache; module downloads and the OS page cache remain warm. The other scenarios reuse unchanged output, force a fresh link, edit a reachable test body or edit an unused constant.

Mini traces and symbols are checked again. Native and Orchestrion retain their original qualification. Those variants were not rerun. Dates differ, so small timing differences need the recorded ranges and uncertainty. Orchestrion's percentage compares time against Native. Mini's percentages compare Orchestrion first, then Native. All time cells are seconds.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 17.896 s | 51.532 s (+188.0%) | 23.451 s (-54.5%; +31.0%) |
| Gin | `none` | 32 | 10.844 s | 22.679 s (+109.1%) | 13.343 s (-41.2%; +23.0%) |
| Chi | `none` | 4 | 7.764 s | 30.543 s (+293.4%) | 10.999 s (-64.0%; +41.7%) |
| Chi | `none` | 32 | 4.576 s | 16.765 s (+266.3%) | 6.868 s (-59.0%; +50.1%) |
| Gin | `-race` | 4 | 27.873 s | 56.274 s (+101.9%) | 30.129 s (-46.5%; +8.1%) |
| Gin | `-race` | 32 | 23.244 s | 34.631 s (+49.0%) | 24.732 s (-28.6%; +6.4%) |
| Gin | `-cover` | 4 | 18.781 s | 66.330 s (+253.2%) | 21.918 s (-67.0%; +16.7%) |
| Gin | `-cover` | 32 | 10.933 s | 29.664 s (+171.3%) | 13.575 s (-54.2%; +24.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 19.328 s | 56.685 s (+193.3%) | 22.239 s (-60.8%; +15.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 12.161 s | 27.508 s (+126.2%) | 12.865 s (-53.2%; +5.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 29.271 s | 68.432 s (+133.8%) | 31.375 s (-54.2%; +7.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 24.174 s | 39.394 s (+63.0%) | 25.750 s (-34.6%; +6.5%) |
| Chi | `-race` | 4 | 11.393 s | 37.963 s (+233.2%) | 15.835 s (-58.3%; +39.0%) |
| Chi | `-race` | 32 | 9.318 s | 27.565 s (+195.8%) | 13.143 s (-52.3%; +41.0%) |
| Chi | `-cover` | 4 | 7.934 s | 38.145 s (+380.8%) | 11.323 s (-70.3%; +42.7%) |
| Chi | `-cover` | 32 | 4.815 s | 19.639 s (+307.9%) | 6.923 s (-64.8%; +43.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 8.179 s | 33.966 s (+315.3%) | 11.328 s (-66.6%; +38.5%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 4.946 s | 17.755 s (+259.0%) | 7.972 s (-55.1%; +61.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 11.745 s | 41.717 s (+255.2%) | 15.916 s (-61.8%; +35.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.538 s | 33.637 s (+252.7%) | 13.292 s (-60.5%; +39.4%) |
| Testify Direct | `none` | 4 | 8.078 s | 33.039 s (+309.0%) | 11.387 s (-65.5%; +41.0%) |
| Testify Direct | `none` | 32 | 4.867 s | 20.067 s (+312.3%) | 6.960 s (-65.3%; +43.0%) |
| Testify Direct | `-race` | 4 | 12.691 s | 46.494 s (+266.4%) | 16.288 s (-65.0%; +28.3%) |
| Testify Direct | `-race` | 32 | 10.094 s | 33.293 s (+229.8%) | 12.956 s (-61.1%; +28.3%) |
| Testify Direct | `-cover` | 4 | 8.650 s | 39.414 s (+355.7%) | 11.653 s (-70.4%; +34.7%) |
| Testify Direct | `-cover` | 32 | 5.337 s | 21.922 s (+310.8%) | 7.903 s (-63.9%; +48.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 9.083 s | 34.215 s (+276.7%) | 11.608 s (-66.1%; +27.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 5.248 s | 28.446 s (+442.0%) | 7.100 s (-75.0%; +35.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 13.186 s | 53.793 s (+307.9%) | 16.471 s (-69.4%; +24.9%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 10.073 s | 29.693 s (+194.8%) | 13.067 s (-56.0%; +29.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.603 s | 30.824 s (+258.3%) | 11.652 s (-62.2%; +35.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.913 s | 20.015 s (+307.4%) | 7.050 s (-64.8%; +43.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.509 s | 39.152 s (+213.0%) | 16.471 s (-57.9%; +31.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.825 s | 29.736 s (+202.6%) | 13.597 s (-54.3%; +38.4%) |
| Testify External | `none` | 4 | 8.381 s | 30.092 s (+259.0%) | 11.470 s (-61.9%; +36.9%) |
| Testify External | `none` | 32 | 4.810 s | 18.167 s (+277.7%) | 6.917 s (-61.9%; +43.8%) |
| Testify External | `-race` | 4 | 12.450 s | 37.839 s (+203.9%) | 16.164 s (-57.3%; +29.8%) |
| Testify External | `-race` | 32 | 9.726 s | 27.976 s (+187.6%) | 12.919 s (-53.8%; +32.8%) |
| Testify External | `-cover` | 4 | 8.407 s | 34.683 s (+312.6%) | 11.620 s (-66.5%; +38.2%) |
| Testify External | `-cover` | 32 | 4.861 s | 21.347 s (+339.2%) | 7.155 s (-66.5%; +47.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 8.507 s | 31.696 s (+272.6%) | 11.621 s (-63.3%; +36.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 5.078 s | 18.402 s (+262.4%) | 7.309 s (-60.3%; +43.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 12.936 s | 40.899 s (+216.2%) | 16.345 s (-60.0%; +26.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 9.861 s | 27.088 s (+174.7%) | 13.076 s (-51.7%; +32.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 8.465 s | 29.977 s (+254.1%) | 11.574 s (-61.4%; +36.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 4.869 s | 17.912 s (+267.8%) | 7.105 s (-60.3%; +45.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 12.725 s | 38.705 s (+204.2%) | 16.474 s (-57.4%; +29.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 9.847 s | 28.139 s (+185.8%) | 13.027 s (-53.7%; +32.3%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.098 s | 0.366 s (+273.5%) | 0.220 s (-40.0%; +124.2%) |
| Gin | `none` | 32 | 0.097 s | 0.410 s (+322.2%) | 0.239 s (-41.8%; +145.7%) |
| Chi | `none` | 4 | 0.063 s | 0.329 s (+421.5%) | 0.145 s (-55.8%; +130.7%) |
| Chi | `none` | 32 | 0.067 s | 0.385 s (+476.1%) | 0.161 s (-58.2%; +141.0%) |
| Gin | `-race` | 4 | 0.098 s | 0.370 s (+279.1%) | 0.233 s (-37.1%; +138.5%) |
| Gin | `-race` | 32 | 0.098 s | 0.413 s (+323.5%) | 0.256 s (-38.0%; +162.7%) |
| Gin | `-cover` | 4 | 0.127 s | 0.514 s (+305.2%) | 0.255 s (-50.4%; +100.9%) |
| Gin | `-cover` | 32 | 0.125 s | 0.570 s (+358.0%) | 0.274 s (-51.9%; +120.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.130 s | 0.432 s (+231.9%) | 0.260 s (-39.9%; +99.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.464 s (+264.8%) | 0.274 s (-41.0%; +115.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.144 s | 0.466 s (+224.4%) | 0.274 s (-41.4%; +90.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.127 s | 0.471 s (+270.6%) | 0.295 s (-37.4%; +131.9%) |
| Chi | `-race` | 4 | 0.062 s | 0.327 s (+428.8%) | 0.158 s (-51.8%; +154.9%) |
| Chi | `-race` | 32 | 0.067 s | 0.387 s (+476.2%) | 0.174 s (-55.1%; +158.9%) |
| Chi | `-cover` | 4 | 0.066 s | 0.419 s (+536.9%) | 0.155 s (-63.0%; +135.5%) |
| Chi | `-cover` | 32 | 0.070 s | 0.494 s (+605.6%) | 0.168 s (-66.1%; +139.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.371 s (+462.5%) | 0.154 s (-58.4%; +134.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.071 s | 0.430 s (+502.4%) | 0.171 s (-60.3%; +139.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.067 s | 0.370 s (+452.3%) | 0.167 s (-54.8%; +149.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.072 s | 0.432 s (+503.9%) | 0.182 s (-57.9%; +154.3%) |
| Testify Direct | `none` | 4 | 0.059 s | 0.314 s (+436.9%) | 0.165 s (-47.5%; +181.9%) |
| Testify Direct | `none` | 32 | 0.061 s | 0.358 s (+488.3%) | 0.183 s (-48.7%; +201.7%) |
| Testify Direct | `-race` | 4 | 0.056 s | 0.314 s (+456.1%) | 0.177 s (-43.6%; +213.6%) |
| Testify Direct | `-race` | 32 | 0.063 s | 0.388 s (+520.6%) | 0.198 s (-48.9%; +216.8%) |
| Testify Direct | `-cover` | 4 | 0.060 s | 0.385 s (+538.2%) | 0.174 s (-54.9%; +187.9%) |
| Testify Direct | `-cover` | 32 | 0.068 s | 0.472 s (+597.8%) | 0.194 s (-58.9%; +187.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.066 s | 0.365 s (+451.6%) | 0.176 s (-51.8%; +166.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.067 s | 0.415 s (+516.9%) | 0.192 s (-53.7%; +185.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.065 s | 0.365 s (+459.7%) | 0.188 s (-48.5%; +188.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.064 s | 0.386 s (+507.2%) | 0.207 s (-46.4%; +225.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.322 s (+401.0%) | 0.175 s (-45.5%; +172.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.366 s (+469.1%) | 0.190 s (-48.1%; +195.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.319 s (+407.9%) | 0.188 s (-41.1%; +199.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.066 s | 0.366 s (+458.5%) | 0.204 s (-44.2%; +211.5%) |
| Testify External | `none` | 4 | 0.059 s | 0.314 s (+434.2%) | 0.168 s (-46.5%; +185.6%) |
| Testify External | `none` | 32 | 0.059 s | 0.358 s (+502.3%) | 0.183 s (-48.8%; +208.4%) |
| Testify External | `-race` | 4 | 0.058 s | 0.312 s (+438.9%) | 0.181 s (-42.0%; +212.4%) |
| Testify External | `-race` | 32 | 0.062 s | 0.354 s (+474.6%) | 0.199 s (-43.9%; +222.5%) |
| Testify External | `-cover` | 4 | 0.062 s | 0.376 s (+507.5%) | 0.175 s (-53.5%; +182.5%) |
| Testify External | `-cover` | 32 | 0.063 s | 0.435 s (+589.3%) | 0.192 s (-55.9%; +204.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.062 s | 0.335 s (+437.0%) | 0.177 s (-47.2%; +183.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.063 s | 0.394 s (+525.3%) | 0.190 s (-51.8%; +201.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.060 s | 0.341 s (+465.5%) | 0.189 s (-44.4%; +214.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.066 s | 0.395 s (+502.8%) | 0.201 s (-49.0%; +207.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.064 s | 0.323 s (+404.0%) | 0.177 s (-45.4%; +175.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.364 s (+468.6%) | 0.189 s (-48.1%; +195.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.063 s | 0.320 s (+406.8%) | 0.190 s (-40.7%; +200.6%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.064 s | 0.368 s (+476.1%) | 0.203 s (-44.8%; +217.9%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.158 s | 5.302 s (+357.8%) | 3.203 s (-39.6%; +176.5%) |
| Gin | `none` | 32 | 1.213 s | 5.372 s (+342.7%) | 2.588 s (-51.8%; +113.3%) |
| Chi | `none` | 4 | 0.266 s | 1.810 s (+580.4%) | 0.551 s (-69.6%; +107.0%) |
| Chi | `none` | 32 | 0.272 s | 1.759 s (+547.0%) | 0.542 s (-69.2%; +99.2%) |
| Gin | `-race` | 4 | 1.987 s | 7.649 s (+284.9%) | 3.294 s (-56.9%; +65.8%) |
| Gin | `-race` | 32 | 1.949 s | 8.048 s (+313.0%) | 2.868 s (-64.4%; +47.2%) |
| Gin | `-cover` | 4 | 1.086 s | 5.733 s (+427.9%) | 2.316 s (-59.6%; +113.3%) |
| Gin | `-cover` | 32 | 1.143 s | 6.131 s (+436.2%) | 2.114 s (-65.5%; +84.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.245 s | 5.492 s (+341.0%) | 2.358 s (-57.1%; +89.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.281 s | 5.750 s (+349.0%) | 1.979 s (-65.6%; +54.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.012 s | 8.356 s (+315.3%) | 3.677 s (-56.0%; +82.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.258 s | 8.331 s (+268.9%) | 3.150 s (-62.2%; +39.5%) |
| Chi | `-race` | 4 | 0.361 s | 2.053 s (+468.9%) | 0.718 s (-65.0%; +99.1%) |
| Chi | `-race` | 32 | 0.365 s | 2.021 s (+453.6%) | 0.645 s (-68.1%; +76.7%) |
| Chi | `-cover` | 4 | 0.290 s | 2.012 s (+593.0%) | 0.480 s (-76.1%; +65.4%) |
| Chi | `-cover` | 32 | 0.285 s | 2.097 s (+636.1%) | 0.506 s (-75.9%; +77.5%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.287 s | 2.012 s (+600.1%) | 0.472 s (-76.6%; +64.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.977 s (+580.2%) | 0.496 s (-74.9%; +70.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.384 s | 2.132 s (+454.8%) | 0.705 s (-66.9%; +83.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.424 s | 2.296 s (+441.7%) | 0.691 s (-69.9%; +63.1%) |
| Testify Direct | `none` | 4 | 0.246 s | 1.622 s (+560.1%) | 0.443 s (-72.7%; +80.1%) |
| Testify Direct | `none` | 32 | 0.270 s | 1.795 s (+563.7%) | 0.489 s (-72.8%; +80.7%) |
| Testify Direct | `-race` | 4 | 0.335 s | 1.839 s (+449.3%) | 0.669 s (-63.6%; +99.9%) |
| Testify Direct | `-race` | 32 | 0.381 s | 2.030 s (+432.5%) | 0.657 s (-67.6%; +72.4%) |
| Testify Direct | `-cover` | 4 | 0.271 s | 1.895 s (+599.4%) | 0.474 s (-75.0%; +75.1%) |
| Testify Direct | `-cover` | 32 | 0.291 s | 1.989 s (+582.3%) | 0.514 s (-74.2%; +76.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.304 s | 1.903 s (+526.8%) | 0.474 s (-75.1%; +56.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.291 s | 1.965 s (+576.0%) | 0.508 s (-74.2%; +74.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.367 s | 1.929 s (+425.0%) | 0.644 s (-66.6%; +75.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.358 s | 2.024 s (+465.9%) | 0.666 s (-67.1%; +86.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.259 s | 1.753 s (+577.6%) | 0.466 s (-73.4%; +80.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.264 s | 1.968 s (+644.8%) | 0.499 s (-74.6%; +88.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.789 s (+426.6%) | 0.624 s (-65.1%; +83.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.359 s | 2.022 s (+463.0%) | 0.684 s (-66.1%; +90.6%) |
| Testify External | `none` | 4 | 0.239 s | 1.716 s (+618.1%) | 0.459 s (-73.3%; +92.1%) |
| Testify External | `none` | 32 | 0.256 s | 1.867 s (+629.4%) | 0.480 s (-74.3%; +87.5%) |
| Testify External | `-race` | 4 | 0.328 s | 1.821 s (+456.1%) | 0.629 s (-65.5%; +92.1%) |
| Testify External | `-race` | 32 | 0.341 s | 1.952 s (+472.8%) | 0.709 s (-63.7%; +108.1%) |
| Testify External | `-cover` | 4 | 0.248 s | 1.696 s (+582.5%) | 0.466 s (-72.5%; +87.5%) |
| Testify External | `-cover` | 32 | 0.262 s | 1.960 s (+646.8%) | 0.496 s (-74.7%; +88.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.257 s | 1.700 s (+562.2%) | 0.470 s (-72.3%; +83.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.270 s | 1.956 s (+625.8%) | 0.493 s (-74.8%; +83.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.345 s | 1.920 s (+457.1%) | 0.660 s (-65.6%; +91.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.352 s | 2.055 s (+484.5%) | 0.649 s (-68.4%; +84.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.258 s | 1.762 s (+583.7%) | 0.474 s (-73.1%; +83.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.270 s | 1.895 s (+602.2%) | 0.497 s (-73.8%; +84.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.340 s | 1.840 s (+441.7%) | 0.676 s (-63.3%; +98.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.358 s | 2.009 s (+461.4%) | 0.696 s (-65.3%; +94.6%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1.280 s | 2.810 s (+119.6%) | 1.622 s (-42.3%; +26.7%) |
| Gin | `none` | 32 | 1.160 s | 2.871 s (+147.6%) | 1.439 s (-49.9%; +24.1%) |
| Chi | `none` | 4 | 0.413 s | 1.794 s (+334.9%) | 0.606 s (-66.2%; +46.9%) |
| Chi | `none` | 32 | 0.409 s | 1.901 s (+364.2%) | 0.638 s (-66.4%; +55.9%) |
| Gin | `-race` | 4 | 2.120 s | 3.873 s (+82.7%) | 2.689 s (-30.6%; +26.8%) |
| Gin | `-race` | 32 | 2.182 s | 4.379 s (+100.7%) | 2.587 s (-40.9%; +18.6%) |
| Gin | `-cover` | 4 | 1.440 s | 3.115 s (+116.4%) | 1.796 s (-42.4%; +24.7%) |
| Gin | `-cover` | 32 | 1.338 s | 3.272 s (+144.6%) | 1.572 s (-52.0%; +17.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1.533 s | 3.126 s (+103.9%) | 1.828 s (-41.5%; +19.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1.351 s | 3.375 s (+149.8%) | 1.759 s (-47.9%; +30.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 2.451 s | 4.147 s (+69.2%) | 2.919 s (-29.6%; +19.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 2.473 s | 4.708 s (+90.4%) | 2.833 s (-39.8%; +14.6%) |
| Chi | `-race` | 4 | 0.723 s | 2.295 s (+217.5%) | 0.980 s (-57.3%; +35.6%) |
| Chi | `-race` | 32 | 0.750 s | 2.399 s (+219.9%) | 0.977 s (-59.3%; +30.3%) |
| Chi | `-cover` | 4 | 0.475 s | 2.002 s (+321.6%) | 0.672 s (-66.4%; +41.5%) |
| Chi | `-cover` | 32 | 0.465 s | 1.910 s (+310.7%) | 0.681 s (-64.4%; +46.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.479 s | 1.961 s (+309.1%) | 0.681 s (-65.3%; +42.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.487 s | 1.974 s (+305.1%) | 0.742 s (-62.4%; +52.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.818 s | 2.226 s (+172.2%) | 1.083 s (-51.3%; +32.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.851 s | 2.501 s (+194.0%) | 1.085 s (-56.6%; +27.5%) |
| Testify Direct | `none` | 4 | 0.285 s | 2.451 s (+760.6%) | 0.496 s (-79.8%; +74.3%) |
| Testify Direct | `none` | 32 | 0.287 s | 1.882 s (+554.8%) | 0.511 s (-72.8%; +78.0%) |
| Testify Direct | `-race` | 4 | 0.447 s | 2.297 s (+413.5%) | 0.740 s (-67.8%; +65.5%) |
| Testify Direct | `-race` | 32 | 0.468 s | 2.876 s (+515.0%) | 0.757 s (-73.7%; +61.8%) |
| Testify Direct | `-cover` | 4 | 0.332 s | 2.391 s (+621.1%) | 0.554 s (-76.8%; +67.2%) |
| Testify Direct | `-cover` | 32 | 0.336 s | 2.099 s (+524.0%) | 0.547 s (-74.0%; +62.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 2.076 s (+536.3%) | 0.520 s (-75.0%; +59.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.339 s | 2.229 s (+558.4%) | 0.552 s (-75.2%; +63.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.462 s | 2.537 s (+449.4%) | 0.716 s (-71.8%; +55.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.437 s | 2.183 s (+399.5%) | 0.824 s (-62.3%; +88.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.286 s | 1.915 s (+568.8%) | 0.515 s (-73.1%; +80.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.303 s | 2.036 s (+571.3%) | 0.537 s (-73.6%; +77.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.423 s | 2.010 s (+374.6%) | 0.727 s (-63.8%; +71.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.449 s | 2.246 s (+400.1%) | 0.745 s (-66.8%; +65.9%) |
| Testify External | `none` | 4 | 0.253 s | 1.742 s (+589.7%) | 0.471 s (-73.0%; +86.3%) |
| Testify External | `none` | 32 | 0.264 s | 1.933 s (+633.3%) | 0.502 s (-74.0%; +90.5%) |
| Testify External | `-race` | 4 | 0.340 s | 1.838 s (+440.7%) | 0.658 s (-64.2%; +93.7%) |
| Testify External | `-race` | 32 | 0.360 s | 2.089 s (+480.9%) | 0.686 s (-67.2%; +90.7%) |
| Testify External | `-cover` | 4 | 0.267 s | 1.868 s (+598.4%) | 0.489 s (-73.8%; +83.0%) |
| Testify External | `-cover` | 32 | 0.275 s | 1.972 s (+615.9%) | 0.521 s (-73.6%; +89.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.280 s | 1.884 s (+572.7%) | 0.488 s (-74.1%; +74.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.286 s | 2.043 s (+614.8%) | 0.507 s (-75.2%; +77.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.354 s | 1.992 s (+463.4%) | 0.670 s (-66.4%; +89.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.383 s | 2.090 s (+445.4%) | 0.691 s (-67.0%; +80.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.274 s | 1.811 s (+560.5%) | 0.485 s (-73.2%; +76.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.285 s | 1.993 s (+598.4%) | 0.510 s (-74.4%; +78.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.354 s | 1.928 s (+444.1%) | 0.664 s (-65.6%; +87.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.389 s | 2.126 s (+447.2%) | 0.698 s (-67.2%; +79.7%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.760 s | 1.161 s (+52.8%) | 0.971 s (-16.3%; +27.8%) |
| Gin | `none` | 32 | 0.651 s | 1.110 s (+70.4%) | 0.866 s (-22.0%; +33.0%) |
| Chi | `none` | 4 | 0.266 s | 0.657 s (+147.5%) | 0.396 s (-39.7%; +49.2%) |
| Chi | `none` | 32 | 0.250 s | 0.682 s (+173.1%) | 0.395 s (-42.0%; +58.4%) |
| Gin | `-race` | 4 | 1.368 s | 1.769 s (+29.2%) | 1.620 s (-8.4%; +18.3%) |
| Gin | `-race` | 32 | 1.411 s | 1.821 s (+29.1%) | 1.656 s (-9.1%; +17.4%) |
| Gin | `-cover` | 4 | 0.944 s | 1.579 s (+67.2%) | 1.184 s (-25.0%; +25.4%) |
| Gin | `-cover` | 32 | 0.823 s | 1.501 s (+82.4%) | 1.042 s (-30.6%; +26.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.982 s | 1.581 s (+61.1%) | 1.221 s (-22.8%; +24.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.868 s | 1.467 s (+68.9%) | 1.104 s (-24.7%; +27.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.665 s | 2.253 s (+35.3%) | 3.680 s (+63.3%; +121.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.667 s | 2.338 s (+40.2%) | 1.990 s (-14.9%; +19.4%) |
| Chi | `-race` | 4 | 0.491 s | 0.872 s (+77.6%) | 0.626 s (-28.2%; +27.5%) |
| Chi | `-race` | 32 | 0.520 s | 0.939 s (+80.5%) | 0.661 s (-29.6%; +27.1%) |
| Chi | `-cover` | 4 | 0.315 s | 0.795 s (+152.1%) | 0.452 s (-43.1%; +43.4%) |
| Chi | `-cover` | 32 | 0.299 s | 0.799 s (+167.1%) | 0.441 s (-44.9%; +47.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 0.326 s | 0.774 s (+137.3%) | 0.460 s (-40.6%; +41.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 0.331 s | 0.796 s (+140.5%) | 0.479 s (-39.8%; +44.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.572 s | 0.996 s (+74.2%) | 0.725 s (-27.2%; +26.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.595 s | 2.099 s (+252.5%) | 0.768 s (-63.4%; +28.9%) |
| Testify Direct | `none` | 4 | 0.102 s | 0.656 s (+542.8%) | 0.223 s (-66.1%; +118.1%) |
| Testify Direct | `none` | 32 | 0.099 s | 1.002 s (+914.9%) | 0.241 s (-76.0%; +143.9%) |
| Testify Direct | `-race` | 4 | 0.154 s | 0.669 s (+334.7%) | 0.286 s (-57.2%; +85.9%) |
| Testify Direct | `-race` | 32 | 0.167 s | 0.736 s (+340.0%) | 0.308 s (-58.1%; +84.3%) |
| Testify Direct | `-cover` | 4 | 0.121 s | 0.698 s (+477.6%) | 0.493 s (-29.3%; +308.2%) |
| Testify Direct | `-cover` | 32 | 0.119 s | 0.775 s (+554.0%) | 0.258 s (-66.8%; +117.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.124 s | 1.310 s (+958.5%) | 0.245 s (-81.3%; +97.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.120 s | 0.728 s (+507.7%) | 0.249 s (-65.8%; +108.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.164 s | 0.776 s (+373.3%) | 0.295 s (-62.0%; +80.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.161 s | 0.725 s (+348.9%) | 0.325 s (-55.1%; +101.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.107 s | 0.589 s (+450.0%) | 0.237 s (-59.8%; +121.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.105 s | 0.650 s (+518.9%) | 0.243 s (-62.6%; +131.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.150 s | 0.619 s (+312.1%) | 0.285 s (-53.9%; +90.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.158 s | 0.706 s (+347.7%) | 0.313 s (-55.6%; +98.7%) |
| Testify External | `none` | 4 | 0.078 s | 0.546 s (+598.3%) | 0.197 s (-64.0%; +151.4%) |
| Testify External | `none` | 32 | 0.080 s | 0.614 s (+667.6%) | 0.213 s (-65.4%; +165.8%) |
| Testify External | `-race` | 4 | 0.078 s | 0.549 s (+607.0%) | 0.211 s (-61.5%; +171.9%) |
| Testify External | `-race` | 32 | 0.081 s | 0.611 s (+654.1%) | 0.231 s (-62.2%; +185.0%) |
| Testify External | `-cover` | 4 | 0.081 s | 0.624 s (+667.1%) | 0.204 s (-67.4%; +150.4%) |
| Testify External | `-cover` | 32 | 0.086 s | 1.060 s (+1136.5%) | 0.230 s (-78.3%; +168.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.086 s | 0.573 s (+568.0%) | 0.212 s (-63.1%; +146.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.088 s | 0.653 s (+644.6%) | 0.230 s (-64.8%; +162.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 0.085 s | 0.592 s (+596.4%) | 0.222 s (-62.6%; +160.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 0.089 s | 0.651 s (+631.3%) | 0.240 s (-63.2%; +169.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.082 s | 0.554 s (+572.3%) | 0.214 s (-61.5%; +158.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.084 s | 0.630 s (+650.7%) | 0.219 s (-65.2%; +161.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.084 s | 0.772 s (+821.5%) | 0.218 s (-71.7%; +160.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.086 s | 0.623 s (+622.8%) | 0.236 s (-62.1%; +173.8%) |

The original convergence control is retained evidence for its collection date. It is not a new host-stability check.
