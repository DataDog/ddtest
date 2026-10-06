# Aggregate memory comparisons

Each cell is the median of the per-run `memory.peak` values, in MiB
(1 MiB = 1,048,576 bytes). The cgroup includes all build or test processes,
detached daemons, nested builds and the measurement helper. Charged file-cache
pages and kernel memory are included. This is neither a Go heap measurement
nor the sum of independently observed process RSS peaks. Runtime receivers run
outside the measured cgroup. Warmups and build qualification commands are excluded.

The percentages compare the same memory metric against Orchestrion first and
Native second. Failed variants have no comparative median; their individual
peaks remain in the raw records.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1674.3 MiB | 2480.6 MiB | 2108.6 MiB (-15.0%; +25.9%) | 1741.7 MiB (-29.8%; +4.0%) |
| Gin | `none` | 32 | 1851.3 MiB | 2639.9 MiB | 2256.8 MiB (-14.5%; +21.9%) | 1831.3 MiB (-30.6%; -1.1%) |
| Chi | `none` | 4 | 464.5 MiB | 1292.0 MiB | 952.7 MiB (-26.3%; +105.1%) | 518.9 MiB (-59.8%; +11.7%) |
| Chi | `none` | 32 | 699.1 MiB | 2021.4 MiB | 1253.3 MiB (-38.0%; +79.3%) | 712.6 MiB (-64.7%; +1.9%) |
| Gin | `-race` | 4 | 1694.1 MiB | 2697.3 MiB | 2111.4 MiB (-21.7%; +24.6%) | 1754.4 MiB (-35.0%; +3.6%) |
| Gin | `-race` | 32 | 1738.9 MiB | 2693.4 MiB | 2171.2 MiB (-19.4%; +24.9%) | 1825.0 MiB (-32.2%; +4.9%) |
| Gin | `-cover` | 4 | 1685.3 MiB | 2624.1 MiB | 2102.7 MiB (-19.9%; +24.8%) | 1741.8 MiB (-33.6%; +3.4%) |
| Gin | `-cover` | 32 | 1850.4 MiB | 2862.9 MiB | 2211.4 MiB (-22.8%; +19.5%) | 1863.6 MiB (-34.9%; +0.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1685.4 MiB | 2598.8 MiB | 2110.9 MiB (-18.8%; +25.2%) | 1744.1 MiB (-32.9%; +3.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1890.4 MiB | 2713.6 MiB | 2129.1 MiB (-21.5%; +12.6%) | 1876.3 MiB (-30.9%; -0.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1756.8 MiB | 2792.1 MiB | 2153.0 MiB (-22.9%; +22.6%) | 1824.1 MiB (-34.7%; +3.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1790.8 MiB | 2726.9 MiB | 2207.4 MiB (-19.0%; +23.3%) | 1875.7 MiB (-31.2%; +4.7%) |
| Chi | `-race` | 4 | 415.8 MiB | 1390.6 MiB | 1081.2 MiB (-22.2%; +160.0%) | 562.4 MiB (-59.6%; +35.3%) |
| Chi | `-race` | 32 | 665.4 MiB | 1674.4 MiB | 1114.2 MiB (-33.5%; +67.4%) | 697.5 MiB (-58.3%; +4.8%) |
| Chi | `-cover` | 4 | 466.0 MiB | 1308.2 MiB | 945.7 MiB (-27.7%; +103.0%) | 524.9 MiB (-59.9%; +12.7%) |
| Chi | `-cover` | 32 | 746.3 MiB | 2152.3 MiB | 1261.1 MiB (-41.4%; +69.0%) | 765.5 MiB (-64.4%; +2.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 479.6 MiB | 1306.7 MiB | 961.0 MiB (-26.5%; +100.4%) | 528.1 MiB (-59.6%; +10.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 762.6 MiB | 2137.0 MiB | 1354.6 MiB (-36.6%; +77.6%) | 788.0 MiB (-63.1%; +3.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 431.9 MiB | 1395.4 MiB | 1085.2 MiB (-22.2%; +151.3%) | 566.2 MiB (-59.4%; +31.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 642.9 MiB | 1608.4 MiB | 1120.2 MiB (-30.4%; +74.2%) | 682.5 MiB (-57.6%; +6.2%) |
| Testify Direct | `none` | 4 | 446.0 MiB | 1015.8 MiB | 924.8 MiB (-9.0%; +107.4%) | 512.2 MiB (-49.6%; +14.8%) |
| Testify Direct | `none` | 32 | 732.8 MiB | 1319.7 MiB | 1210.6 MiB (-8.3%; +65.2%) | 715.0 MiB (-45.8%; -2.4%) |
| Testify Direct | `-race` | 4 | 400.5 MiB | 1029.6 MiB | 945.3 MiB (-8.2%; +136.0%) | 558.0 MiB (-45.8%; +39.3%) |
| Testify Direct | `-race` | 32 | 594.0 MiB | 1182.8 MiB | 954.3 MiB (-19.3%; +60.7%) | 626.8 MiB (-47.0%; +5.5%) |
| Testify Direct | `-cover` | 4 | 409.4 MiB | 992.5 MiB | 916.0 MiB (-7.7%; +123.8%) | 514.3 MiB (-48.2%; +25.6%) |
| Testify Direct | `-cover` | 32 | 697.3 MiB | 1444.5 MiB | 1208.4 MiB (-16.3%; +73.3%) | 702.0 MiB (-51.4%; +0.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 404.1 MiB | 992.9 MiB | 912.9 MiB (-8.1%; +125.9%) | 509.7 MiB (-48.7%; +26.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 662.7 MiB | 1405.3 MiB | 1174.5 MiB (-16.4%; +77.2%) | 706.4 MiB (-49.7%; +6.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 395.1 MiB | 968.7 MiB | 946.9 MiB (-2.2%; +139.6%) | 547.8 MiB (-43.4%; +38.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 591.0 MiB | 1196.5 MiB | 972.9 MiB (-18.7%; +64.6%) | 621.0 MiB (-48.1%; +5.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 436.9 MiB | 1012.0 MiB | 909.9 MiB (-10.1%; +108.3%) | 515.9 MiB (-49.0%; +18.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 728.2 MiB | 1374.6 MiB | 1198.0 MiB (-12.9%; +64.5%) | 731.1 MiB (-46.8%; +0.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 412.4 MiB | 1093.2 MiB | 953.7 MiB (-12.8%; +131.3%) | 557.6 MiB (-49.0%; +35.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 638.6 MiB | 1247.0 MiB | 986.4 MiB (-20.9%; +54.5%) | 642.2 MiB (-48.5%; +0.6%) |
| Testify External | `none` | 4 | 436.4 MiB | 1007.9 MiB | 904.8 MiB (-10.2%; +107.3%) | 505.5 MiB (-49.8%; +15.8%) |
| Testify External | `none` | 32 | 711.6 MiB | 1358.1 MiB | 1273.8 MiB (-6.2%; +79.0%) | 730.3 MiB (-46.2%; +2.6%) |
| Testify External | `-race` | 4 | 395.3 MiB | 1080.2 MiB | 946.0 MiB (-12.4%; +139.3%) | 555.6 MiB (-48.6%; +40.5%) |
| Testify External | `-race` | 32 | 627.3 MiB | 1207.5 MiB | 983.7 MiB (-18.5%; +56.8%) | 640.0 MiB (-47.0%; +2.0%) |
| Testify External | `-cover` | 4 | 420.4 MiB | 1014.6 MiB | 924.9 MiB (-8.8%; +120.0%) | 517.0 MiB (-49.0%; +23.0%) |
| Testify External | `-cover` | 32 | 748.0 MiB | 1348.5 MiB | 1225.5 MiB (-9.1%; +63.8%) | 741.0 MiB (-45.1%; -0.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 420.3 MiB | 1014.8 MiB | 925.3 MiB (-8.8%; +120.1%) | 512.6 MiB (-49.5%; +22.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 692.0 MiB | 1306.1 MiB | 1193.5 MiB (-8.6%; +72.5%) | 728.2 MiB (-44.2%; +5.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 408.0 MiB | 1071.2 MiB | 949.2 MiB (-11.4%; +132.7%) | 550.6 MiB (-48.6%; +35.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 612.2 MiB | 1206.7 MiB | 977.1 MiB (-19.0%; +59.6%) | 622.2 MiB (-48.4%; +1.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 455.9 MiB | 1025.8 MiB | 923.9 MiB (-9.9%; +102.7%) | 511.6 MiB (-50.1%; +12.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 694.1 MiB | 1293.6 MiB | 1226.5 MiB (-5.2%; +76.7%) | 699.9 MiB (-45.9%; +0.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 414.6 MiB | 1089.6 MiB | 962.2 MiB (-11.7%; +132.1%) | 561.2 MiB (-48.5%; +35.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 617.8 MiB | 1241.4 MiB | 987.5 MiB (-20.5%; +59.8%) | 642.6 MiB (-48.2%; +4.0%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 49.9 MiB | 85.4 MiB | 73.0 MiB (-14.5%; +46.3%) | 61.2 MiB (-28.3%; +22.7%) |
| Gin | `none` | 32 | 67.0 MiB | 117.3 MiB | 93.3 MiB (-20.5%; +39.3%) | 81.1 MiB (-30.9%; +21.0%) |
| Chi | `none` | 4 | 41.8 MiB | 73.6 MiB | 64.4 MiB (-12.5%; +54.0%) | 51.6 MiB (-29.9%; +23.3%) |
| Chi | `none` | 32 | 57.0 MiB | 108.3 MiB | 85.5 MiB (-21.1%; +50.0%) | 70.7 MiB (-34.8%; +24.0%) |
| Gin | `-race` | 4 | 50.3 MiB | 86.4 MiB | 71.6 MiB (-17.2%; +42.2%) | 61.9 MiB (-28.4%; +22.9%) |
| Gin | `-race` | 32 | 68.8 MiB | 119.4 MiB | 95.0 MiB (-20.4%; +38.0%) | 82.2 MiB (-31.1%; +19.5%) |
| Gin | `-cover` | 4 | 66.9 MiB | 99.0 MiB | 89.4 MiB (-9.7%; +33.6%) | 78.4 MiB (-20.8%; +17.1%) |
| Gin | `-cover` | 32 | 87.1 MiB | 148.8 MiB | 118.7 MiB (-20.2%; +36.3%) | 103.7 MiB (-30.3%; +19.0%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 65.6 MiB | 109.4 MiB | 89.4 MiB (-18.3%; +36.3%) | 77.5 MiB (-29.2%; +18.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 90.0 MiB | 139.0 MiB | 118.3 MiB (-14.9%; +31.4%) | 104.0 MiB (-25.1%; +15.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 68.5 MiB | 100.4 MiB | 88.2 MiB (-12.1%; +28.8%) | 77.1 MiB (-23.2%; +12.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 91.2 MiB | 142.7 MiB | 115.4 MiB (-19.1%; +26.5%) | 103.1 MiB (-27.8%; +13.0%) |
| Chi | `-race` | 4 | 41.9 MiB | 79.0 MiB | 64.9 MiB (-17.9%; +54.7%) | 52.8 MiB (-33.2%; +25.9%) |
| Chi | `-race` | 32 | 60.1 MiB | 109.4 MiB | 86.6 MiB (-20.8%; +44.1%) | 71.9 MiB (-34.3%; +19.5%) |
| Chi | `-cover` | 4 | 42.0 MiB | 77.0 MiB | 64.4 MiB (-16.4%; +53.4%) | 53.2 MiB (-30.9%; +26.8%) |
| Chi | `-cover` | 32 | 62.5 MiB | 118.0 MiB | 87.2 MiB (-26.1%; +39.6%) | 73.2 MiB (-37.9%; +17.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 43.7 MiB | 79.8 MiB | 63.9 MiB (-19.9%; +46.0%) | 52.6 MiB (-34.1%; +20.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 58.7 MiB | 117.1 MiB | 88.1 MiB (-24.7%; +50.1%) | 70.0 MiB (-40.2%; +19.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 43.4 MiB | 72.6 MiB | 65.9 MiB (-9.2%; +51.8%) | 53.1 MiB (-26.9%; +22.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 114.4 MiB | 87.1 MiB (-23.9%; +48.0%) | 71.2 MiB (-37.8%; +21.0%) |
| Testify Direct | `none` | 4 | 41.5 MiB | 72.3 MiB | 63.5 MiB (-12.2%; +52.9%) | 51.7 MiB (-28.5%; +24.5%) |
| Testify Direct | `none` | 32 | 58.0 MiB | 104.4 MiB | 84.5 MiB (-19.0%; +45.8%) | 75.9 MiB (-27.3%; +30.9%) |
| Testify Direct | `-race` | 4 | 41.8 MiB | 73.3 MiB | 65.8 MiB (-10.2%; +57.4%) | 52.2 MiB (-28.7%; +24.9%) |
| Testify Direct | `-race` | 32 | 46.2 MiB | 75.9 MiB | 67.7 MiB (-10.8%; +46.6%) | 53.1 MiB (-30.0%; +15.0%) |
| Testify Direct | `-cover` | 4 | 42.6 MiB | 68.1 MiB | 64.4 MiB (-5.4%; +51.3%) | 51.5 MiB (-24.4%; +20.9%) |
| Testify Direct | `-cover` | 32 | 43.6 MiB | 74.6 MiB | 66.4 MiB (-11.0%; +52.2%) | 52.6 MiB (-29.6%; +20.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 33.8 MiB | 59.4 MiB | 52.8 MiB (-11.1%; +56.2%) | 40.6 MiB (-31.6%; +20.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 43.2 MiB | 73.6 MiB | 66.3 MiB (-9.8%; +53.5%) | 51.2 MiB (-30.3%; +18.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 35.4 MiB | 56.8 MiB | 53.5 MiB (-5.7%; +51.2%) | 42.0 MiB (-26.1%; +18.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 57.5 MiB | 103.5 MiB | 83.6 MiB (-19.3%; +45.2%) | 72.2 MiB (-30.2%; +25.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.5 MiB | 77.2 MiB | 64.2 MiB (-16.8%; +54.6%) | 51.6 MiB (-33.2%; +24.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.2 MiB | 106.0 MiB | 84.1 MiB (-20.7%; +47.0%) | 74.1 MiB (-30.1%; +29.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.4 MiB | 78.4 MiB | 67.5 MiB (-13.9%; +63.3%) | 55.6 MiB (-29.1%; +34.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 104.6 MiB | 83.7 MiB (-20.0%; +44.6%) | 75.3 MiB (-28.0%; +30.2%) |
| Testify External | `none` | 4 | 41.8 MiB | 72.4 MiB | 63.3 MiB (-12.5%; +51.5%) | 52.4 MiB (-27.6%; +25.3%) |
| Testify External | `none` | 32 | 54.3 MiB | 102.7 MiB | 88.9 MiB (-13.4%; +63.8%) | 73.7 MiB (-28.2%; +35.9%) |
| Testify External | `-race` | 4 | 42.4 MiB | 71.8 MiB | 65.2 MiB (-9.2%; +53.7%) | 51.7 MiB (-28.0%; +21.9%) |
| Testify External | `-race` | 32 | 58.1 MiB | 107.8 MiB | 84.9 MiB (-21.2%; +46.3%) | 71.8 MiB (-33.4%; +23.7%) |
| Testify External | `-cover` | 4 | 43.4 MiB | 72.4 MiB | 67.5 MiB (-6.8%; +55.7%) | 51.8 MiB (-28.4%; +19.6%) |
| Testify External | `-cover` | 32 | 57.3 MiB | 110.1 MiB | 84.8 MiB (-22.9%; +48.0%) | 74.5 MiB (-32.3%; +30.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 42.0 MiB | 74.4 MiB | 65.5 MiB (-11.9%; +56.0%) | 52.4 MiB (-29.5%; +24.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 94.2 MiB | 76.0 MiB (-19.3%; +29.2%) | 72.5 MiB (-23.0%; +23.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 41.5 MiB | 70.1 MiB | 65.6 MiB (-6.5%; +57.9%) | 51.7 MiB (-26.3%; +24.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 56.6 MiB | 102.4 MiB | 79.9 MiB (-21.9%; +41.3%) | 70.4 MiB (-31.3%; +24.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.7 MiB | 74.4 MiB | 63.7 MiB (-14.4%; +49.2%) | 53.6 MiB (-27.9%; +25.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 108.6 MiB | 84.8 MiB (-21.9%; +46.4%) | 74.5 MiB (-31.4%; +28.6%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.0 MiB | 77.3 MiB | 64.1 MiB (-17.0%; +52.6%) | 56.7 MiB (-26.7%; +34.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 60.0 MiB | 105.9 MiB | 90.4 MiB (-14.6%; +50.7%) | 77.8 MiB (-26.5%; +29.7%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 340.3 MiB | 816.5 MiB | 793.5 MiB (-2.8%; +133.2%) | 446.5 MiB (-45.3%; +31.2%) |
| Gin | `none` | 32 | 411.5 MiB | 1141.9 MiB | 1030.5 MiB (-9.8%; +150.4%) | 525.6 MiB (-54.0%; +27.7%) |
| Chi | `none` | 4 | 137.0 MiB | 377.7 MiB | 338.7 MiB (-10.3%; +147.3%) | 170.4 MiB (-54.9%; +24.4%) |
| Chi | `none` | 32 | 182.8 MiB | 429.9 MiB | 373.6 MiB (-13.1%; +104.4%) | 200.1 MiB (-53.5%; +9.5%) |
| Gin | `-race` | 4 | 429.4 MiB | 928.1 MiB | 952.2 MiB (+2.6%; +121.7%) | 502.3 MiB (-45.9%; +17.0%) |
| Gin | `-race` | 32 | 512.1 MiB | 1479.3 MiB | 1313.2 MiB (-11.2%; +156.5%) | 607.1 MiB (-59.0%; +18.6%) |
| Gin | `-cover` | 4 | 351.6 MiB | 787.4 MiB | 773.4 MiB (-1.8%; +119.9%) | 447.8 MiB (-43.1%; +27.4%) |
| Gin | `-cover` | 32 | 405.2 MiB | 1147.3 MiB | 1004.7 MiB (-12.4%; +148.0%) | 526.1 MiB (-54.1%; +29.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 352.6 MiB | 797.0 MiB | 806.2 MiB (+1.2%; +128.6%) | 450.0 MiB (-43.5%; +27.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 411.7 MiB | 1158.4 MiB | 1033.3 MiB (-10.8%; +151.0%) | 525.7 MiB (-54.6%; +27.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 436.7 MiB | 1000.6 MiB | 915.6 MiB (-8.5%; +109.7%) | 515.6 MiB (-48.5%; +18.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 505.7 MiB | 1390.4 MiB | 1254.5 MiB (-9.8%; +148.1%) | 590.5 MiB (-57.5%; +16.8%) |
| Chi | `-race` | 4 | 150.7 MiB | 434.1 MiB | 364.2 MiB (-16.1%; +141.6%) | 183.4 MiB (-57.8%; +21.7%) |
| Chi | `-race` | 32 | 178.9 MiB | 461.2 MiB | 415.1 MiB (-10.0%; +132.1%) | 219.7 MiB (-52.4%; +22.8%) |
| Chi | `-cover` | 4 | 140.1 MiB | 375.4 MiB | 345.9 MiB (-7.9%; +146.9%) | 173.2 MiB (-53.9%; +23.6%) |
| Chi | `-cover` | 32 | 182.2 MiB | 429.8 MiB | 382.9 MiB (-10.9%; +110.1%) | 204.1 MiB (-52.5%; +12.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 141.1 MiB | 376.5 MiB | 339.2 MiB (-9.9%; +140.4%) | 173.2 MiB (-54.0%; +22.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 184.0 MiB | 441.0 MiB | 380.9 MiB (-13.6%; +107.0%) | 203.3 MiB (-53.9%; +10.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 151.1 MiB | 415.6 MiB | 372.3 MiB (-10.4%; +146.4%) | 188.1 MiB (-54.7%; +24.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 184.3 MiB | 456.5 MiB | 423.8 MiB (-7.2%; +130.0%) | 219.8 MiB (-51.9%; +19.3%) |
| Testify Direct | `none` | 4 | 86.3 MiB | 242.6 MiB | 227.8 MiB (-6.1%; +164.0%) | 111.6 MiB (-54.0%; +29.4%) |
| Testify Direct | `none` | 32 | 116.8 MiB | 266.2 MiB | 242.5 MiB (-8.9%; +107.6%) | 133.0 MiB (-50.0%; +13.9%) |
| Testify Direct | `-race` | 4 | 95.1 MiB | 269.0 MiB | 249.3 MiB (-7.3%; +162.3%) | 120.3 MiB (-55.3%; +26.6%) |
| Testify Direct | `-race` | 32 | 97.7 MiB | 265.3 MiB | 255.5 MiB (-3.7%; +161.6%) | 121.5 MiB (-54.2%; +24.4%) |
| Testify Direct | `-cover` | 4 | 84.6 MiB | 229.6 MiB | 216.3 MiB (-5.8%; +155.6%) | 111.7 MiB (-51.4%; +32.0%) |
| Testify Direct | `-cover` | 32 | 93.9 MiB | 235.9 MiB | 222.2 MiB (-5.8%; +136.8%) | 116.4 MiB (-50.6%; +24.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 84.9 MiB | 222.9 MiB | 212.3 MiB (-4.7%; +150.1%) | 102.6 MiB (-54.0%; +20.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 91.3 MiB | 234.6 MiB | 222.8 MiB (-5.0%; +144.1%) | 114.6 MiB (-51.1%; +25.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 89.5 MiB | 253.9 MiB | 244.2 MiB (-3.8%; +172.9%) | 114.1 MiB (-55.1%; +27.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 116.8 MiB | 294.0 MiB | 276.6 MiB (-5.9%; +136.8%) | 147.2 MiB (-49.9%; +26.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.5 MiB | 241.9 MiB | 225.2 MiB (-6.9%; +154.5%) | 113.8 MiB (-53.0%; +28.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 122.2 MiB | 261.9 MiB | 244.9 MiB (-6.5%; +100.4%) | 139.3 MiB (-46.8%; +14.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 95.1 MiB | 263.1 MiB | 253.8 MiB (-3.5%; +167.0%) | 121.3 MiB (-53.9%; +27.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.7 MiB | 294.0 MiB | 274.8 MiB (-6.5%; +131.6%) | 146.4 MiB (-50.2%; +23.4%) |
| Testify External | `none` | 4 | 86.0 MiB | 236.9 MiB | 223.8 MiB (-5.6%; +160.1%) | 112.9 MiB (-52.3%; +31.2%) |
| Testify External | `none` | 32 | 116.0 MiB | 264.0 MiB | 245.2 MiB (-7.1%; +111.5%) | 136.8 MiB (-48.2%; +17.9%) |
| Testify External | `-race` | 4 | 93.8 MiB | 265.0 MiB | 243.1 MiB (-8.2%; +159.2%) | 120.4 MiB (-54.6%; +28.3%) |
| Testify External | `-race` | 32 | 117.4 MiB | 294.0 MiB | 273.1 MiB (-7.1%; +132.6%) | 146.2 MiB (-50.3%; +24.5%) |
| Testify External | `-cover` | 4 | 87.4 MiB | 244.0 MiB | 227.0 MiB (-7.0%; +159.8%) | 116.5 MiB (-52.3%; +33.3%) |
| Testify External | `-cover` | 32 | 119.0 MiB | 265.1 MiB | 249.1 MiB (-6.0%; +109.2%) | 138.6 MiB (-47.7%; +16.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 87.4 MiB | 242.2 MiB | 228.8 MiB (-5.5%; +161.8%) | 113.8 MiB (-53.0%; +30.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 115.3 MiB | 258.6 MiB | 244.2 MiB (-5.6%; +111.8%) | 136.2 MiB (-47.3%; +18.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 92.5 MiB | 265.1 MiB | 252.3 MiB (-4.8%; +172.7%) | 118.5 MiB (-55.3%; +28.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 118.8 MiB | 290.6 MiB | 273.3 MiB (-5.9%; +130.0%) | 141.0 MiB (-51.5%; +18.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.2 MiB | 241.6 MiB | 228.8 MiB (-5.3%; +159.3%) | 116.5 MiB (-51.8%; +32.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.5 MiB | 266.3 MiB | 248.1 MiB (-6.8%; +109.3%) | 136.6 MiB (-48.7%; +15.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 93.3 MiB | 270.2 MiB | 254.6 MiB (-5.8%; +173.0%) | 122.4 MiB (-54.7%; +31.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 115.7 MiB | 293.2 MiB | 273.6 MiB (-6.7%; +136.5%) | 147.0 MiB (-49.9%; +27.0%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 230.3 MiB | 347.5 MiB | 329.3 MiB (-5.3%; +43.0%) | 240.5 MiB (-30.8%; +4.5%) |
| Gin | `none` | 32 | 282.6 MiB | 366.2 MiB | 343.4 MiB (-6.2%; +21.5%) | 297.4 MiB (-18.8%; +5.2%) |
| Chi | `none` | 4 | 130.8 MiB | 234.5 MiB | 222.1 MiB (-5.3%; +69.8%) | 140.1 MiB (-40.2%; +7.2%) |
| Chi | `none` | 32 | 180.2 MiB | 266.9 MiB | 241.6 MiB (-9.5%; +34.1%) | 187.7 MiB (-29.7%; +4.1%) |
| Gin | `-race` | 4 | 228.7 MiB | 384.3 MiB | 367.9 MiB (-4.3%; +60.8%) | 234.5 MiB (-39.0%; +2.5%) |
| Gin | `-race` | 32 | 243.3 MiB | 403.4 MiB | 381.6 MiB (-5.4%; +56.9%) | 254.5 MiB (-36.9%; +4.6%) |
| Gin | `-cover` | 4 | 244.2 MiB | 375.7 MiB | 325.0 MiB (-13.5%; +33.1%) | 255.7 MiB (-31.9%; +4.7%) |
| Gin | `-cover` | 32 | 317.7 MiB | 466.9 MiB | 353.3 MiB (-24.3%; +11.2%) | 326.9 MiB (-30.0%; +2.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 254.0 MiB | 399.8 MiB | 328.2 MiB (-17.9%; +29.2%) | 269.9 MiB (-32.5%; +6.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 323.7 MiB | 479.7 MiB | 353.5 MiB (-26.3%; +9.2%) | 347.8 MiB (-27.5%; +7.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 240.8 MiB | 394.1 MiB | 371.5 MiB (-5.7%; +54.3%) | 249.0 MiB (-36.8%; +3.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 262.1 MiB | 413.7 MiB | 393.5 MiB (-4.9%; +50.1%) | 273.1 MiB (-34.0%; +4.2%) |
| Chi | `-race` | 4 | 102.7 MiB | 254.8 MiB | 244.6 MiB (-4.0%; +138.0%) | 121.5 MiB (-52.3%; +18.2%) |
| Chi | `-race` | 32 | 129.1 MiB | 292.0 MiB | 274.4 MiB (-6.0%; +112.6%) | 149.4 MiB (-48.8%; +15.8%) |
| Chi | `-cover` | 4 | 137.5 MiB | 234.7 MiB | 220.4 MiB (-6.1%; +60.3%) | 150.1 MiB (-36.1%; +9.2%) |
| Chi | `-cover` | 32 | 174.1 MiB | 271.3 MiB | 250.2 MiB (-7.8%; +43.7%) | 187.7 MiB (-30.8%; +7.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.1 MiB | 235.6 MiB | 219.1 MiB (-7.0%; +56.4%) | 151.3 MiB (-35.8%; +8.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 174.9 MiB | 270.1 MiB | 249.1 MiB (-7.8%; +42.4%) | 189.1 MiB (-30.0%; +8.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.9 MiB | 256.6 MiB | 238.2 MiB (-7.2%; +114.8%) | 124.0 MiB (-51.7%; +11.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 137.9 MiB | 295.3 MiB | 289.5 MiB (-2.0%; +110.0%) | 148.3 MiB (-49.8%; +7.6%) |
| Testify Direct | `none` | 4 | 88.6 MiB | 241.8 MiB | 229.4 MiB (-5.2%; +158.8%) | 116.8 MiB (-51.7%; +31.8%) |
| Testify Direct | `none` | 32 | 133.5 MiB | 262.4 MiB | 244.9 MiB (-6.7%; +83.4%) | 151.0 MiB (-42.5%; +13.1%) |
| Testify Direct | `-race` | 4 | 91.0 MiB | 254.4 MiB | 247.0 MiB (-2.9%; +171.4%) | 112.6 MiB (-55.7%; +23.8%) |
| Testify Direct | `-race` | 32 | 100.3 MiB | 266.4 MiB | 252.1 MiB (-5.4%; +151.3%) | 126.5 MiB (-52.5%; +26.1%) |
| Testify Direct | `-cover` | 4 | 89.2 MiB | 231.8 MiB | 216.0 MiB (-6.8%; +142.1%) | 108.8 MiB (-53.0%; +22.0%) |
| Testify Direct | `-cover` | 32 | 95.2 MiB | 239.4 MiB | 226.1 MiB (-5.5%; +137.5%) | 117.7 MiB (-50.8%; +23.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 81.1 MiB | 220.3 MiB | 213.2 MiB (-3.2%; +162.9%) | 106.3 MiB (-51.8%; +31.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 95.5 MiB | 237.3 MiB | 222.0 MiB (-6.5%; +132.6%) | 116.3 MiB (-51.0%; +21.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 87.9 MiB | 255.9 MiB | 245.1 MiB (-4.2%; +178.9%) | 115.0 MiB (-55.1%; +30.9%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 121.1 MiB | 293.7 MiB | 275.9 MiB (-6.1%; +127.8%) | 145.0 MiB (-50.6%; +19.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 89.5 MiB | 244.8 MiB | 226.1 MiB (-7.7%; +152.7%) | 117.3 MiB (-52.1%; +31.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 132.9 MiB | 260.3 MiB | 245.3 MiB (-5.8%; +84.6%) | 150.9 MiB (-42.0%; +13.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 99.7 MiB | 271.5 MiB | 257.7 MiB (-5.1%; +158.5%) | 122.7 MiB (-54.8%; +23.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.1 MiB | 294.6 MiB | 279.3 MiB (-5.2%; +136.5%) | 146.0 MiB (-50.4%; +23.7%) |
| Testify External | `none` | 4 | 87.9 MiB | 241.9 MiB | 220.3 MiB (-8.9%; +150.6%) | 113.6 MiB (-53.0%; +29.2%) |
| Testify External | `none` | 32 | 133.3 MiB | 255.6 MiB | 243.9 MiB (-4.6%; +83.0%) | 143.9 MiB (-43.7%; +7.9%) |
| Testify External | `-race` | 4 | 94.6 MiB | 264.6 MiB | 256.9 MiB (-2.9%; +171.6%) | 118.1 MiB (-55.3%; +24.9%) |
| Testify External | `-race` | 32 | 117.4 MiB | 287.2 MiB | 275.0 MiB (-4.3%; +134.3%) | 142.4 MiB (-50.4%; +21.4%) |
| Testify External | `-cover` | 4 | 89.9 MiB | 247.8 MiB | 230.3 MiB (-7.1%; +156.1%) | 113.2 MiB (-54.3%; +25.9%) |
| Testify External | `-cover` | 32 | 145.0 MiB | 266.8 MiB | 250.2 MiB (-6.2%; +72.5%) | 160.9 MiB (-39.7%; +11.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 88.2 MiB | 242.6 MiB | 225.5 MiB (-7.1%; +155.7%) | 116.3 MiB (-52.0%; +31.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 131.1 MiB | 251.2 MiB | 239.0 MiB (-4.8%; +82.4%) | 148.8 MiB (-40.7%; +13.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 94.4 MiB | 268.0 MiB | 254.5 MiB (-5.0%; +169.7%) | 118.8 MiB (-55.7%; +25.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 117.9 MiB | 286.8 MiB | 276.3 MiB (-3.7%; +134.4%) | 142.4 MiB (-50.3%; +20.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 87.1 MiB | 246.8 MiB | 231.2 MiB (-6.3%; +165.4%) | 115.9 MiB (-53.1%; +33.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 126.7 MiB | 259.0 MiB | 242.0 MiB (-6.6%; +91.0%) | 150.7 MiB (-41.8%; +18.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 92.9 MiB | 263.9 MiB | 256.7 MiB (-2.7%; +176.3%) | 121.4 MiB (-54.0%; +30.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 113.8 MiB | 287.0 MiB | 279.8 MiB (-2.5%; +145.8%) | 144.0 MiB (-49.8%; +26.5%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 229.9 MiB | 257.4 MiB | 257.8 MiB (+0.1%; +12.1%) | 239.2 MiB (-7.1%; +4.1%) |
| Gin | `none` | 32 | 288.6 MiB | 334.6 MiB | 319.2 MiB (-4.6%; +10.6%) | 296.8 MiB (-11.3%; +2.9%) |
| Chi | `none` | 4 | 128.9 MiB | 158.0 MiB | 162.4 MiB (+2.8%; +26.0%) | 139.8 MiB (-11.5%; +8.4%) |
| Chi | `none` | 32 | 178.0 MiB | 220.6 MiB | 217.0 MiB (-1.6%; +21.9%) | 197.7 MiB (-10.4%; +11.1%) |
| Gin | `-race` | 4 | 232.3 MiB | 253.0 MiB | 256.3 MiB (+1.3%; +10.4%) | 248.6 MiB (-1.7%; +7.0%) |
| Gin | `-race` | 32 | 244.6 MiB | 288.1 MiB | 274.7 MiB (-4.7%; +12.3%) | 266.6 MiB (-7.5%; +9.0%) |
| Gin | `-cover` | 4 | 245.2 MiB | 384.0 MiB | 273.7 MiB (-28.7%; +11.6%) | 254.2 MiB (-33.8%; +3.7%) |
| Gin | `-cover` | 32 | 320.4 MiB | 455.4 MiB | 344.2 MiB (-24.4%; +7.4%) | 327.4 MiB (-28.1%; +2.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 259.5 MiB | 401.5 MiB | 283.8 MiB (-29.3%; +9.3%) | 266.0 MiB (-33.7%; +2.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 312.0 MiB | 492.1 MiB | 351.0 MiB (-28.7%; +12.5%) | 354.0 MiB (-28.1%; +13.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 247.5 MiB | 383.5 MiB | 281.6 MiB (-26.6%; +13.8%) | 260.0 MiB (-32.2%; +5.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 264.1 MiB | 435.3 MiB | 295.3 MiB (-32.2%; +11.8%) | 278.2 MiB (-36.1%; +5.4%) |
| Chi | `-race` | 4 | 104.0 MiB | 129.6 MiB | 129.0 MiB (-0.5%; +24.1%) | 116.6 MiB (-10.1%; +12.1%) |
| Chi | `-race` | 32 | 126.6 MiB | 155.1 MiB | 151.4 MiB (-2.4%; +19.5%) | 139.9 MiB (-9.8%; +10.5%) |
| Chi | `-cover` | 4 | 135.4 MiB | 165.6 MiB | 167.5 MiB (+1.2%; +23.7%) | 157.7 MiB (-4.8%; +16.4%) |
| Chi | `-cover` | 32 | 176.3 MiB | 213.5 MiB | 201.2 MiB (-5.7%; +14.2%) | 187.9 MiB (-12.0%; +6.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.5 MiB | 168.0 MiB | 167.4 MiB (-0.3%; +19.2%) | 154.8 MiB (-7.9%; +10.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 172.0 MiB | 208.7 MiB | 206.5 MiB (-1.1%; +20.0%) | 187.5 MiB (-10.2%; +9.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.2 MiB | 141.9 MiB | 136.3 MiB (-3.9%; +23.7%) | 119.5 MiB (-15.8%; +8.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 134.1 MiB | 174.5 MiB | 164.8 MiB (-5.6%; +22.9%) | 149.6 MiB (-14.3%; +11.6%) |
| Testify Direct | `none` | 4 | 80.0 MiB | 101.2 MiB | 106.7 MiB (+5.4%; +33.3%) | 87.4 MiB (-13.6%; +9.2%) |
| Testify Direct | `none` | 32 | 133.1 MiB | 146.7 MiB | 149.9 MiB (+2.1%; +12.6%) | 135.5 MiB (-7.7%; +1.8%) |
| Testify Direct | `-race` | 4 | 63.1 MiB | 82.3 MiB | 81.0 MiB (-1.6%; +28.4%) | 72.0 MiB (-12.6%; +14.2%) |
| Testify Direct | `-race` | 32 | 64.4 MiB | 84.2 MiB | 91.5 MiB (+8.6%; +42.0%) | 74.4 MiB (-11.7%; +15.5%) |
| Testify Direct | `-cover` | 4 | 67.5 MiB | 82.8 MiB | 89.6 MiB (+8.2%; +32.8%) | 75.1 MiB (-9.3%; +11.3%) |
| Testify Direct | `-cover` | 32 | 93.8 MiB | 101.3 MiB | 114.7 MiB (+13.2%; +22.4%) | 94.5 MiB (-6.7%; +0.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 67.5 MiB | 82.2 MiB | 93.9 MiB (+14.3%; +39.1%) | 74.3 MiB (-9.6%; +10.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 85.8 MiB | 102.2 MiB | 112.7 MiB (+10.3%; +31.3%) | 92.0 MiB (-9.9%; +7.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 55.9 MiB | 70.2 MiB | 80.7 MiB (+15.0%; +44.5%) | 63.4 MiB (-9.7%; +13.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 93.2 MiB | 123.9 MiB | 113.3 MiB (-8.6%; +21.5%) | 98.7 MiB (-20.3%; +5.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 77.2 MiB | 110.4 MiB | 114.6 MiB (+3.8%; +48.5%) | 88.9 MiB (-19.5%; +15.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 123.9 MiB | 128.1 MiB | 143.8 MiB (+12.3%; +16.0%) | 142.9 MiB (+11.6%; +15.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 73.7 MiB | 91.5 MiB | 99.7 MiB (+9.0%; +35.3%) | 81.7 MiB (-10.8%; +10.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 88.8 MiB | 124.8 MiB | 112.6 MiB (-9.7%; +26.9%) | 100.7 MiB (-19.2%; +13.5%) |
| Testify External | `none` | 4 | 57.1 MiB | 87.7 MiB | 82.3 MiB (-6.1%; +44.2%) | 66.5 MiB (-24.1%; +16.5%) |
| Testify External | `none` | 32 | 87.9 MiB | 144.4 MiB | 118.8 MiB (-17.7%; +35.1%) | 111.9 MiB (-22.5%; +27.2%) |
| Testify External | `-race` | 4 | 56.2 MiB | 90.7 MiB | 79.9 MiB (-11.9%; +42.1%) | 65.7 MiB (-27.6%; +16.9%) |
| Testify External | `-race` | 32 | 85.1 MiB | 129.7 MiB | 121.7 MiB (-6.2%; +43.0%) | 100.3 MiB (-22.7%; +17.8%) |
| Testify External | `-cover` | 4 | 57.0 MiB | 97.6 MiB | 85.2 MiB (-12.7%; +49.5%) | 69.0 MiB (-29.4%; +21.0%) |
| Testify External | `-cover` | 32 | 90.2 MiB | 115.7 MiB | 125.5 MiB (+8.5%; +39.2%) | 110.8 MiB (-4.3%; +22.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 57.8 MiB | 86.3 MiB | 82.7 MiB (-4.1%; +43.2%) | 68.2 MiB (-20.9%; +18.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 102.4 MiB | 128.9 MiB | 115.7 MiB (-10.2%; +13.0%) | 112.9 MiB (-12.4%; +10.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 53.8 MiB | 84.4 MiB | 81.2 MiB (-3.7%; +51.1%) | 65.2 MiB (-22.7%; +21.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 82.3 MiB | 119.7 MiB | 109.6 MiB (-8.4%; +33.1%) | 102.5 MiB (-14.3%; +24.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 54.9 MiB | 94.0 MiB | 79.4 MiB (-15.5%; +44.8%) | 66.6 MiB (-29.2%; +21.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 97.0 MiB | 137.5 MiB | 124.4 MiB (-9.5%; +28.3%) | 106.7 MiB (-22.4%; +10.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 56.8 MiB | 93.1 MiB | 78.0 MiB (-16.2%; +37.5%) | 64.1 MiB (-31.2%; +12.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 84.5 MiB | 123.5 MiB | 106.1 MiB (-14.1%; +25.5%) | 91.1 MiB (-26.2%; +7.8%) |

## Runtime of original test executables

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 36.6 MiB | 60.3 MiB | 60.8 MiB (+0.8%; +65.8%) | 42.4 MiB (-29.8%; +15.6%) | 43.5 MiB (-27.8%; +18.8%) |
| Gin | `none` | 32 | 42.2 MiB | 70.7 MiB | 75.1 MiB (+6.2%; +78.0%) | 55.3 MiB (-21.8%; +31.0%) | 50.5 MiB (-28.6%; +19.6%) |
| Chi | `none` | 4 | 45.6 MiB | 79.7 MiB | 78.1 MiB (-2.0%; +71.0%) | 47.4 MiB (-40.5%; +3.8%) | 46.6 MiB (-41.5%; +2.1%) |
| Chi | `none` | 32 | 94.7 MiB | 128.0 MiB | 117.0 MiB (-8.7%; +23.5%) | 91.7 MiB (-28.4%; -3.2%) | 89.6 MiB (-30.1%; -5.5%) |
| Gin | `-race` | 4 | 199.3 MiB | FAIL 5/6 | FAIL 6/6 | 245.7 MiB (vs Orchestrion unavailable; +23.3% vs Native) | 250.5 MiB (vs Orchestrion unavailable; +25.7% vs Native) |
| Gin | `-race` | 32 | 262.9 MiB | FAIL 6/6 | FAIL 6/6 | 334.6 MiB (vs Orchestrion unavailable; +27.3% vs Native) | 345.3 MiB (vs Orchestrion unavailable; +31.3% vs Native) |
| Gin | `-cover` | 4 | 35.9 MiB | 92.6 MiB | 95.6 MiB (+3.2%; +166.4%) | 83.1 MiB (-10.3%; +131.7%) | 81.3 MiB (-12.2%; +126.5%) |
| Gin | `-cover` | 32 | 42.1 MiB | 165.2 MiB | 165.6 MiB (+0.2%; +293.3%) | 147.9 MiB (-10.4%; +251.3%) | 146.2 MiB (-11.5%; +247.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 35.7 MiB | 98.6 MiB | 96.9 MiB (-1.7%; +171.3%) | 83.8 MiB (-15.1%; +134.4%) | 93.5 MiB (-5.1%; +161.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 52.1 MiB | 200.9 MiB | 219.2 MiB (+9.1%; +320.9%) | 178.4 MiB (-11.2%; +242.5%) | 187.2 MiB (-6.8%; +259.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 243.9 MiB | FAIL 5/6 | FAIL 6/6 | FAIL 6/6 | FAIL 3/6 |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 283.8 MiB | FAIL 5/6 | FAIL 6/6 | FAIL 2/6 | FAIL 4/6 |
| Chi | `-race` | 4 | 2958.8 MiB | 2979.4 MiB | 2944.9 MiB (-1.2%; -0.5%) | 3020.1 MiB (+1.4%; +2.1%) | 3004.8 MiB (+0.9%; +1.6%) |
| Chi | `-race` | 32 | 3068.8 MiB | 3112.4 MiB | 3085.5 MiB (-0.9%; +0.5%) | 3084.6 MiB (-0.9%; +0.5%) | 3081.5 MiB (-1.0%; +0.4%) |
| Chi | `-cover` | 4 | 43.0 MiB | 85.0 MiB | 78.7 MiB (-7.3%; +83.1%) | 58.3 MiB (-31.4%; +35.5%) | 58.2 MiB (-31.5%; +35.3%) |
| Chi | `-cover` | 32 | 88.6 MiB | 149.1 MiB | 147.9 MiB (-0.8%; +66.8%) | 95.8 MiB (-35.8%; +8.1%) | 96.0 MiB (-35.6%; +8.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 45.1 MiB | 98.8 MiB | 99.5 MiB (+0.7%; +120.6%) | 60.6 MiB (-38.7%; +34.3%) | 60.3 MiB (-39.0%; +33.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 84.1 MiB | 145.2 MiB | 158.7 MiB (+9.3%; +88.6%) | 103.2 MiB (-29.0%; +22.6%) | 112.7 MiB (-22.4%; +34.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 2944.4 MiB | 3073.4 MiB | 3083.4 MiB (+0.3%; +4.7%) | 3029.8 MiB (-1.4%; +2.9%) | 3017.0 MiB (-1.8%; +2.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 2940.6 MiB | 3072.5 MiB | 3064.3 MiB (-0.3%; +4.2%) | 3017.7 MiB (-1.8%; +2.6%) | 3004.9 MiB (-2.2%; +2.2%) |
| Testify Direct | `none` | 4 | 14.5 MiB | 26.6 MiB | 27.0 MiB (+1.3%; +85.4%) | 17.3 MiB (-35.1%; +18.9%) | 17.1 MiB (-35.7%; +17.7%) |
| Testify Direct | `none` | 32 | 15.0 MiB | 29.4 MiB | 29.2 MiB (-0.9%; +94.7%) | 18.3 MiB (-37.9%; +21.9%) | 18.5 MiB (-37.2%; +23.3%) |
| Testify Direct | `-race` | 4 | 34.1 MiB | 67.5 MiB | 69.2 MiB (+2.6%; +103.0%) | 41.2 MiB (-39.0%; +20.8%) | 41.9 MiB (-37.8%; +23.1%) |
| Testify Direct | `-race` | 32 | 37.2 MiB | 73.4 MiB | 75.8 MiB (+3.3%; +103.8%) | 43.2 MiB (-41.2%; +16.0%) | 47.9 MiB (-34.8%; +28.6%) |
| Testify Direct | `-cover` | 4 | 14.5 MiB | 29.7 MiB | 28.0 MiB (-5.8%; +93.1%) | 22.2 MiB (-25.4%; +53.1%) | 22.3 MiB (-24.8%; +54.1%) |
| Testify Direct | `-cover` | 32 | 14.7 MiB | 33.8 MiB | 33.8 MiB (+0.2%; +129.9%) | 24.4 MiB (-27.7%; +66.0%) | 25.3 MiB (-25.1%; +71.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 27.6 MiB | 27.9 MiB (+0.8%; +94.5%) | 20.4 MiB (-26.1%; +42.5%) | 20.5 MiB (-25.9%; +42.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 14.8 MiB | 34.0 MiB | 34.0 MiB (-0.1%; +130.3%) | 25.4 MiB (-25.4%; +72.0%) | 24.4 MiB (-28.3%; +65.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 34.0 MiB | 66.7 MiB | 66.8 MiB (+0.1%; +96.5%) | 44.9 MiB (-32.8%; +32.1%) | 46.6 MiB (-30.2%; +37.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.8 MiB | 72.1 MiB | 72.6 MiB (+0.7%; +103.1%) | 51.6 MiB (-28.5%; +44.3%) | 52.5 MiB (-27.2%; +46.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.8 MiB | 27.7 MiB | 28.2 MiB (+1.6%; +90.3%) | 22.2 MiB (-19.9%; +50.1%) | 22.3 MiB (-19.7%; +50.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 15.0 MiB | 35.2 MiB | 33.6 MiB (-4.6%; +124.3%) | 25.9 MiB (-26.4%; +73.1%) | 25.5 MiB (-27.4%; +70.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.4 MiB | 74.6 MiB (+0.2%; +111.1%) | 50.0 MiB (-32.8%; +41.5%) | 49.7 MiB (-33.3%; +40.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.7 MiB | 83.5 MiB | 82.1 MiB (-1.7%; +123.6%) | 58.1 MiB (-30.4%; +58.2%) | 56.3 MiB (-32.5%; +53.4%) |
| Testify External | `none` | 4 | 14.3 MiB | 27.1 MiB | 26.4 MiB (-2.8%; +83.8%) | 17.3 MiB (-36.2%; +20.6%) | 17.3 MiB (-36.2%; +20.6%) |
| Testify External | `none` | 32 | 14.5 MiB | 32.3 MiB | 29.3 MiB (-9.2%; +102.5%) | 18.3 MiB (-43.4%; +26.4%) | 18.8 MiB (-41.9%; +29.6%) |
| Testify External | `-race` | 4 | 33.8 MiB | 68.8 MiB | 69.5 MiB (+0.9%; +105.3%) | 41.2 MiB (-40.1%; +21.8%) | 41.4 MiB (-39.8%; +22.5%) |
| Testify External | `-race` | 32 | 37.0 MiB | 75.3 MiB | 75.6 MiB (+0.4%; +104.4%) | 43.7 MiB (-42.0%; +18.2%) | 43.4 MiB (-42.4%; +17.3%) |
| Testify External | `-cover` | 4 | 14.2 MiB | 29.4 MiB | 29.7 MiB (+0.9%; +108.5%) | 22.2 MiB (-24.5%; +56.0%) | 22.4 MiB (-23.8%; +57.5%) |
| Testify External | `-cover` | 32 | 15.0 MiB | 34.4 MiB | 32.3 MiB (-6.1%; +115.0%) | 25.2 MiB (-26.7%; +67.9%) | 25.1 MiB (-27.2%; +66.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 28.2 MiB | 27.9 MiB (-1.0%; +94.5%) | 20.5 MiB (-27.2%; +43.0%) | 22.0 MiB (-22.0%; +53.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 15.0 MiB | 34.4 MiB | 34.0 MiB (-1.0%; +126.8%) | 24.1 MiB (-30.0%; +60.4%) | 25.0 MiB (-27.2%; +66.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 32.8 MiB | 67.1 MiB | 67.0 MiB (-0.1%; +104.3%) | 45.7 MiB (-31.9%; +39.3%) | 46.9 MiB (-30.1%; +43.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.5 MiB | 73.1 MiB | 71.8 MiB (-1.7%; +102.3%) | 52.6 MiB (-28.1%; +48.0%) | 51.4 MiB (-29.7%; +44.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.6 MiB | 28.0 MiB | 29.0 MiB (+3.6%; +98.8%) | 22.8 MiB (-18.6%; +56.1%) | 22.2 MiB (-20.8%; +52.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 16.8 MiB | 34.5 MiB | 34.8 MiB (+1.0%; +108.0%) | 25.5 MiB (-25.9%; +52.5%) | 25.8 MiB (-25.0%; +54.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.3 MiB | 74.6 MiB (+0.4%; +111.4%) | 49.7 MiB (-33.1%; +40.9%) | 49.6 MiB (-33.2%; +40.6%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.3 MiB | 73.0 MiB | 78.8 MiB (+7.9%; +117.2%) | 56.1 MiB (-23.2%; +54.6%) | 55.2 MiB (-24.5%; +52.1%) |

## Gin: local Agent delivery with telemetry

Three measured runs after one warmup.

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 41.4 MiB | 77.5 MiB | 84.3 MiB (+8.7%; +103.4%) | 62.5 MiB (-19.4%; +50.7%) |
| Gin | `none` | 32 | 41.5 MiB | 78.2 MiB | 76.4 MiB (-2.4%; +83.8%) | 51.8 MiB (-33.9%; +24.6%) |
