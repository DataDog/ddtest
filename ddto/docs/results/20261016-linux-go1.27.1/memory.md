# Aggregate memory comparisons

Each cell is the median of the per-run `memory.peak` values, in MiB
(1 MiB = 1,048,576 bytes). The cgroup includes all build or test processes,
detached daemons, nested builds and the measurement helper. Charged file-cache
pages and kernel memory are included. This is neither a Go heap measurement
nor the sum of independently observed process RSS peaks. Runtime receivers run
outside the measured cgroup. Warmups and build qualification commands are excluded.

Orchestrion's percentage compares memory against Native. Mini's percentages
compare memory against Orchestrion first and Native second. Failed variants
have no comparative median; their individual peaks remain in the raw records.

## Cold compilation

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 1674.3 MiB | 2480.6 MiB (+48.2%) | 1472.1 MiB (-40.7%; -12.1%) |
| Gin | `none` | 32 | 1851.3 MiB | 2639.9 MiB (+42.6%) | 1765.1 MiB (-33.1%; -4.7%) |
| Chi | `none` | 4 | 464.5 MiB | 1292.0 MiB (+178.1%) | 521.9 MiB (-59.6%; +12.4%) |
| Chi | `none` | 32 | 699.1 MiB | 2021.4 MiB (+189.2%) | 695.7 MiB (-65.6%; -0.5%) |
| Gin | `-race` | 4 | 1694.1 MiB | 2697.3 MiB (+59.2%) | 1682.0 MiB (-37.6%; -0.7%) |
| Gin | `-race` | 32 | 1738.9 MiB | 2693.4 MiB (+54.9%) | 1713.5 MiB (-36.4%; -1.5%) |
| Gin | `-cover` | 4 | 1685.3 MiB | 2624.1 MiB (+55.7%) | 1745.7 MiB (-33.5%; +3.6%) |
| Gin | `-cover` | 32 | 1850.4 MiB | 2862.9 MiB (+54.7%) | 1781.5 MiB (-37.8%; -3.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1685.4 MiB | 2598.8 MiB (+54.2%) | 1729.2 MiB (-33.5%; +2.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1890.4 MiB | 2713.6 MiB (+43.5%) | 1779.1 MiB (-34.4%; -5.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1756.8 MiB | 2792.1 MiB (+58.9%) | 1755.3 MiB (-37.1%; -0.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1790.8 MiB | 2726.9 MiB (+52.3%) | 1773.7 MiB (-35.0%; -1.0%) |
| Chi | `-race` | 4 | 415.8 MiB | 1390.6 MiB (+234.4%) | 536.6 MiB (-61.4%; +29.1%) |
| Chi | `-race` | 32 | 665.4 MiB | 1674.4 MiB (+151.6%) | 591.7 MiB (-64.7%; -11.1%) |
| Chi | `-cover` | 4 | 466.0 MiB | 1308.2 MiB (+180.8%) | 526.4 MiB (-59.8%; +13.0%) |
| Chi | `-cover` | 32 | 746.3 MiB | 2152.3 MiB (+188.4%) | 672.1 MiB (-68.8%; -9.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 479.6 MiB | 1306.7 MiB (+172.4%) | 528.8 MiB (-59.5%; +10.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 762.6 MiB | 2137.0 MiB (+180.2%) | 713.1 MiB (-66.6%; -6.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 431.9 MiB | 1395.4 MiB (+223.1%) | 544.2 MiB (-61.0%; +26.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 642.9 MiB | 1608.4 MiB (+150.2%) | 598.7 MiB (-62.8%; -6.9%) |
| Testify Direct | `none` | 4 | 446.0 MiB | 1015.8 MiB (+127.8%) | 522.9 MiB (-48.5%; +17.2%) |
| Testify Direct | `none` | 32 | 732.8 MiB | 1319.7 MiB (+80.1%) | 677.4 MiB (-48.7%; -7.6%) |
| Testify Direct | `-race` | 4 | 400.5 MiB | 1029.6 MiB (+157.1%) | 541.1 MiB (-47.4%; +35.1%) |
| Testify Direct | `-race` | 32 | 594.0 MiB | 1182.8 MiB (+99.1%) | 605.8 MiB (-48.8%; +2.0%) |
| Testify Direct | `-cover` | 4 | 409.4 MiB | 992.5 MiB (+142.4%) | 528.6 MiB (-46.7%; +29.1%) |
| Testify Direct | `-cover` | 32 | 697.3 MiB | 1444.5 MiB (+107.2%) | 696.4 MiB (-51.8%; -0.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 404.1 MiB | 992.9 MiB (+145.7%) | 527.1 MiB (-46.9%; +30.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 662.7 MiB | 1405.3 MiB (+112.0%) | 719.9 MiB (-48.8%; +8.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 395.1 MiB | 968.7 MiB (+145.1%) | 539.8 MiB (-44.3%; +36.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 591.0 MiB | 1196.5 MiB (+102.5%) | 608.5 MiB (-49.1%; +3.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 436.9 MiB | 1012.0 MiB (+131.6%) | 529.8 MiB (-47.6%; +21.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 728.2 MiB | 1374.6 MiB (+88.8%) | 714.8 MiB (-48.0%; -1.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 412.4 MiB | 1093.2 MiB (+165.1%) | 544.0 MiB (-50.2%; +31.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 638.6 MiB | 1247.0 MiB (+95.3%) | 606.5 MiB (-51.4%; -5.0%) |
| Testify External | `none` | 4 | 436.4 MiB | 1007.9 MiB (+131.0%) | 525.3 MiB (-47.9%; +20.4%) |
| Testify External | `none` | 32 | 711.6 MiB | 1358.1 MiB (+90.9%) | 705.0 MiB (-48.1%; -0.9%) |
| Testify External | `-race` | 4 | 395.3 MiB | 1080.2 MiB (+173.2%) | 541.3 MiB (-49.9%; +36.9%) |
| Testify External | `-race` | 32 | 627.3 MiB | 1207.5 MiB (+92.5%) | 613.7 MiB (-49.2%; -2.2%) |
| Testify External | `-cover` | 4 | 420.4 MiB | 1014.6 MiB (+141.4%) | 529.6 MiB (-47.8%; +26.0%) |
| Testify External | `-cover` | 32 | 748.0 MiB | 1348.5 MiB (+80.3%) | 741.9 MiB (-45.0%; -0.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 420.3 MiB | 1014.8 MiB (+141.4%) | 531.0 MiB (-47.7%; +26.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 692.0 MiB | 1306.1 MiB (+88.7%) | 694.6 MiB (-46.8%; +0.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 408.0 MiB | 1071.2 MiB (+162.6%) | 547.2 MiB (-48.9%; +34.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 612.2 MiB | 1206.7 MiB (+97.1%) | 617.7 MiB (-48.8%; +0.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 455.9 MiB | 1025.8 MiB (+125.0%) | 531.3 MiB (-48.2%; +16.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 694.1 MiB | 1293.6 MiB (+86.4%) | 704.6 MiB (-45.5%; +1.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 414.6 MiB | 1089.6 MiB (+162.8%) | 551.1 MiB (-49.4%; +32.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 617.8 MiB | 1241.4 MiB (+100.9%) | 609.2 MiB (-50.9%; -1.4%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 49.9 MiB | 85.4 MiB (+71.1%) | 54.0 MiB (-36.7%; +8.3%) |
| Gin | `none` | 32 | 67.0 MiB | 117.3 MiB (+75.1%) | 66.6 MiB (-43.2%; -0.6%) |
| Chi | `none` | 4 | 41.8 MiB | 73.6 MiB (+75.9%) | 44.8 MiB (-39.1%; +7.1%) |
| Chi | `none` | 32 | 57.0 MiB | 108.3 MiB (+90.1%) | 55.5 MiB (-48.8%; -2.6%) |
| Gin | `-race` | 4 | 50.3 MiB | 86.4 MiB (+71.6%) | 56.4 MiB (-34.7%; +12.0%) |
| Gin | `-race` | 32 | 68.8 MiB | 119.4 MiB (+73.5%) | 70.2 MiB (-41.2%; +1.9%) |
| Gin | `-cover` | 4 | 66.9 MiB | 99.0 MiB (+47.9%) | 65.7 MiB (-33.6%; -1.8%) |
| Gin | `-cover` | 32 | 87.1 MiB | 148.8 MiB (+70.8%) | 81.3 MiB (-45.4%; -6.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 65.6 MiB | 109.4 MiB (+66.7%) | 65.8 MiB (-39.9%; +0.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 90.0 MiB | 139.0 MiB (+54.4%) | 81.1 MiB (-41.7%; -9.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 68.5 MiB | 100.4 MiB (+46.6%) | 68.6 MiB (-31.7%; +0.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 91.2 MiB | 142.7 MiB (+56.4%) | 84.8 MiB (-40.5%; -7.0%) |
| Chi | `-race` | 4 | 41.9 MiB | 79.0 MiB (+88.5%) | 48.1 MiB (-39.2%; +14.7%) |
| Chi | `-race` | 32 | 60.1 MiB | 109.4 MiB (+82.0%) | 59.7 MiB (-45.5%; -0.8%) |
| Chi | `-cover` | 4 | 42.0 MiB | 77.0 MiB (+83.6%) | 45.5 MiB (-41.0%; +8.4%) |
| Chi | `-cover` | 32 | 62.5 MiB | 118.0 MiB (+88.9%) | 55.8 MiB (-52.7%; -10.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 43.7 MiB | 79.8 MiB (+82.3%) | 45.6 MiB (-42.8%; +4.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 58.7 MiB | 117.1 MiB (+99.5%) | 56.7 MiB (-51.5%; -3.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 43.4 MiB | 72.6 MiB (+67.2%) | 49.0 MiB (-32.5%; +12.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 114.4 MiB (+94.5%) | 60.2 MiB (-47.4%; +2.3%) |
| Testify Direct | `none` | 4 | 41.5 MiB | 72.3 MiB (+74.2%) | 43.8 MiB (-39.4%; +5.6%) |
| Testify Direct | `none` | 32 | 58.0 MiB | 104.4 MiB (+80.0%) | 55.5 MiB (-46.9%; -4.4%) |
| Testify Direct | `-race` | 4 | 41.8 MiB | 73.3 MiB (+75.2%) | 48.6 MiB (-33.7%; +16.1%) |
| Testify Direct | `-race` | 32 | 46.2 MiB | 75.9 MiB (+64.3%) | 58.4 MiB (-23.0%; +26.6%) |
| Testify Direct | `-cover` | 4 | 42.6 MiB | 68.1 MiB (+59.9%) | 44.8 MiB (-34.2%; +5.2%) |
| Testify Direct | `-cover` | 32 | 43.6 MiB | 74.6 MiB (+71.1%) | 56.3 MiB (-24.6%; +29.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 33.8 MiB | 59.4 MiB (+75.7%) | 45.0 MiB (-24.1%; +33.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 43.2 MiB | 73.6 MiB (+70.2%) | 53.8 MiB (-26.8%; +24.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 35.4 MiB | 56.8 MiB (+60.4%) | 48.2 MiB (-15.1%; +36.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 57.5 MiB | 103.5 MiB (+79.9%) | 57.8 MiB (-44.2%; +0.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.5 MiB | 77.2 MiB (+85.9%) | 46.9 MiB (-39.3%; +12.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.2 MiB | 106.0 MiB (+85.3%) | 57.7 MiB (-45.6%; +0.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.4 MiB | 78.4 MiB (+89.6%) | 50.6 MiB (-35.4%; +22.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 104.6 MiB (+80.8%) | 60.6 MiB (-42.0%; +4.8%) |
| Testify External | `none` | 4 | 41.8 MiB | 72.4 MiB (+73.1%) | 43.9 MiB (-39.3%; +5.0%) |
| Testify External | `none` | 32 | 54.3 MiB | 102.7 MiB (+89.3%) | 54.6 MiB (-46.8%; +0.6%) |
| Testify External | `-race` | 4 | 42.4 MiB | 71.8 MiB (+69.3%) | 48.2 MiB (-32.8%; +13.8%) |
| Testify External | `-race` | 32 | 58.1 MiB | 107.8 MiB (+85.6%) | 57.8 MiB (-46.4%; -0.4%) |
| Testify External | `-cover` | 4 | 43.4 MiB | 72.4 MiB (+67.1%) | 45.3 MiB (-37.4%; +4.6%) |
| Testify External | `-cover` | 32 | 57.3 MiB | 110.1 MiB (+92.0%) | 55.4 MiB (-49.6%; -3.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 42.0 MiB | 74.4 MiB (+77.0%) | 45.0 MiB (-39.5%; +7.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 94.2 MiB (+60.1%) | 55.7 MiB (-40.9%; -5.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 41.5 MiB | 70.1 MiB (+68.9%) | 48.5 MiB (-30.8%; +16.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 56.6 MiB | 102.4 MiB (+81.0%) | 58.7 MiB (-42.7%; +3.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.7 MiB | 74.4 MiB (+74.4%) | 45.7 MiB (-38.6%; +7.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 108.6 MiB (+87.4%) | 57.4 MiB (-47.2%; -1.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.0 MiB | 77.3 MiB (+84.0%) | 49.6 MiB (-35.8%; +18.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 60.0 MiB | 105.9 MiB (+76.4%) | 60.6 MiB (-42.7%; +1.1%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 340.3 MiB | 816.5 MiB (+139.9%) | 438.6 MiB (-46.3%; +28.9%) |
| Gin | `none` | 32 | 411.5 MiB | 1141.9 MiB (+177.5%) | 489.2 MiB (-57.2%; +18.9%) |
| Chi | `none` | 4 | 137.0 MiB | 377.7 MiB (+175.8%) | 162.6 MiB (-57.0%; +18.7%) |
| Chi | `none` | 32 | 182.8 MiB | 429.9 MiB (+135.2%) | 179.6 MiB (-58.2%; -1.7%) |
| Gin | `-race` | 4 | 429.4 MiB | 928.1 MiB (+116.1%) | 508.2 MiB (-45.2%; +18.3%) |
| Gin | `-race` | 32 | 512.1 MiB | 1479.3 MiB (+188.9%) | 566.9 MiB (-61.7%; +10.7%) |
| Gin | `-cover` | 4 | 351.6 MiB | 787.4 MiB (+123.9%) | 428.0 MiB (-45.6%; +21.7%) |
| Gin | `-cover` | 32 | 405.2 MiB | 1147.3 MiB (+183.2%) | 487.4 MiB (-57.5%; +20.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 352.6 MiB | 797.0 MiB (+126.0%) | 421.6 MiB (-47.1%; +19.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 411.7 MiB | 1158.4 MiB (+181.4%) | 489.6 MiB (-57.7%; +18.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 436.7 MiB | 1000.6 MiB (+129.1%) | 512.7 MiB (-48.8%; +17.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 505.7 MiB | 1390.4 MiB (+174.9%) | 566.8 MiB (-59.2%; +12.1%) |
| Chi | `-race` | 4 | 150.7 MiB | 434.1 MiB (+188.0%) | 180.3 MiB (-58.5%; +19.6%) |
| Chi | `-race` | 32 | 178.9 MiB | 461.2 MiB (+157.8%) | 194.1 MiB (-57.9%; +8.5%) |
| Chi | `-cover` | 4 | 140.1 MiB | 375.4 MiB (+168.0%) | 167.7 MiB (-55.3%; +19.8%) |
| Chi | `-cover` | 32 | 182.2 MiB | 429.8 MiB (+135.9%) | 183.9 MiB (-57.2%; +0.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 141.1 MiB | 376.5 MiB (+166.9%) | 168.1 MiB (-55.3%; +19.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 184.0 MiB | 441.0 MiB (+139.6%) | 182.6 MiB (-58.6%; -0.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 151.1 MiB | 415.6 MiB (+175.0%) | 181.5 MiB (-56.3%; +20.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 184.3 MiB | 456.5 MiB (+147.7%) | 195.5 MiB (-57.2%; +6.1%) |
| Testify Direct | `none` | 4 | 86.3 MiB | 242.6 MiB (+181.2%) | 108.8 MiB (-55.2%; +26.1%) |
| Testify Direct | `none` | 32 | 116.8 MiB | 266.2 MiB (+127.9%) | 118.7 MiB (-55.4%; +1.6%) |
| Testify Direct | `-race` | 4 | 95.1 MiB | 269.0 MiB (+183.0%) | 117.8 MiB (-56.2%; +23.9%) |
| Testify Direct | `-race` | 32 | 97.7 MiB | 265.3 MiB (+171.6%) | 128.3 MiB (-51.7%; +31.3%) |
| Testify Direct | `-cover` | 4 | 84.6 MiB | 229.6 MiB (+171.3%) | 110.3 MiB (-52.0%; +30.3%) |
| Testify Direct | `-cover` | 32 | 93.9 MiB | 235.9 MiB (+151.3%) | 120.3 MiB (-49.0%; +28.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 84.9 MiB | 222.9 MiB (+162.5%) | 110.8 MiB (-50.3%; +30.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 91.3 MiB | 234.6 MiB (+156.9%) | 118.2 MiB (-49.6%; +29.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 89.5 MiB | 253.9 MiB (+183.7%) | 119.4 MiB (-53.0%; +33.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 116.8 MiB | 294.0 MiB (+151.8%) | 127.7 MiB (-56.6%; +9.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.5 MiB | 241.9 MiB (+173.3%) | 110.4 MiB (-54.4%; +24.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 122.2 MiB | 261.9 MiB (+114.3%) | 118.2 MiB (-54.9%; -3.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 95.1 MiB | 263.1 MiB (+176.8%) | 118.7 MiB (-54.9%; +24.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.7 MiB | 294.0 MiB (+147.7%) | 129.4 MiB (-56.0%; +9.0%) |
| Testify External | `none` | 4 | 86.0 MiB | 236.9 MiB (+175.4%) | 109.0 MiB (-54.0%; +26.7%) |
| Testify External | `none` | 32 | 116.0 MiB | 264.0 MiB (+127.6%) | 118.4 MiB (-55.1%; +2.1%) |
| Testify External | `-race` | 4 | 93.8 MiB | 265.0 MiB (+182.5%) | 117.4 MiB (-55.7%; +25.1%) |
| Testify External | `-race` | 32 | 117.4 MiB | 294.0 MiB (+150.4%) | 128.7 MiB (-56.2%; +9.6%) |
| Testify External | `-cover` | 4 | 87.4 MiB | 244.0 MiB (+179.3%) | 109.5 MiB (-55.1%; +25.3%) |
| Testify External | `-cover` | 32 | 119.0 MiB | 265.1 MiB (+122.7%) | 119.6 MiB (-54.9%; +0.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 87.4 MiB | 242.2 MiB (+177.1%) | 109.5 MiB (-54.8%; +25.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 115.3 MiB | 258.6 MiB (+124.3%) | 117.2 MiB (-54.7%; +1.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 92.5 MiB | 265.1 MiB (+186.6%) | 121.5 MiB (-54.2%; +31.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 118.8 MiB | 290.6 MiB (+144.5%) | 127.4 MiB (-56.2%; +7.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.2 MiB | 241.6 MiB (+173.8%) | 109.8 MiB (-54.5%; +24.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.5 MiB | 266.3 MiB (+124.7%) | 117.4 MiB (-55.9%; -1.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 93.3 MiB | 270.2 MiB (+189.7%) | 120.9 MiB (-55.3%; +29.6%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 115.7 MiB | 293.2 MiB (+153.4%) | 128.6 MiB (-56.1%; +11.2%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 230.3 MiB | 347.5 MiB (+50.9%) | 230.3 MiB (-33.7%; +0.0%) |
| Gin | `none` | 32 | 282.6 MiB | 366.2 MiB (+29.6%) | 270.8 MiB (-26.1%; -4.2%) |
| Chi | `none` | 4 | 130.8 MiB | 234.5 MiB (+79.3%) | 132.5 MiB (-43.5%; +1.3%) |
| Chi | `none` | 32 | 180.2 MiB | 266.9 MiB (+48.1%) | 157.4 MiB (-41.0%; -12.7%) |
| Gin | `-race` | 4 | 228.7 MiB | 384.3 MiB (+68.0%) | 219.3 MiB (-42.9%; -4.1%) |
| Gin | `-race` | 32 | 243.3 MiB | 403.4 MiB (+65.8%) | 232.3 MiB (-42.4%; -4.5%) |
| Gin | `-cover` | 4 | 244.2 MiB | 375.7 MiB (+53.8%) | 242.7 MiB (-35.4%; -0.6%) |
| Gin | `-cover` | 32 | 317.7 MiB | 466.9 MiB (+47.0%) | 295.4 MiB (-36.7%; -7.0%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 254.0 MiB | 399.8 MiB (+57.4%) | 253.0 MiB (-36.7%; -0.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 323.7 MiB | 479.7 MiB (+48.2%) | 302.3 MiB (-37.0%; -6.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 240.8 MiB | 394.1 MiB (+63.7%) | 243.8 MiB (-38.1%; +1.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 262.1 MiB | 413.7 MiB (+57.8%) | 257.9 MiB (-37.7%; -1.6%) |
| Chi | `-race` | 4 | 102.7 MiB | 254.8 MiB (+148.0%) | 118.2 MiB (-53.6%; +15.0%) |
| Chi | `-race` | 32 | 129.1 MiB | 292.0 MiB (+126.2%) | 129.0 MiB (-55.8%; -0.1%) |
| Chi | `-cover` | 4 | 137.5 MiB | 234.7 MiB (+70.7%) | 140.9 MiB (-40.0%; +2.5%) |
| Chi | `-cover` | 32 | 174.1 MiB | 271.3 MiB (+55.8%) | 159.6 MiB (-41.2%; -8.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.1 MiB | 235.6 MiB (+68.2%) | 143.0 MiB (-39.3%; +2.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 174.9 MiB | 270.1 MiB (+54.4%) | 164.9 MiB (-39.0%; -5.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.9 MiB | 256.6 MiB (+131.4%) | 120.3 MiB (-53.1%; +8.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 137.9 MiB | 295.3 MiB (+114.2%) | 132.1 MiB (-55.3%; -4.2%) |
| Testify Direct | `none` | 4 | 88.6 MiB | 241.8 MiB (+172.9%) | 110.9 MiB (-54.1%; +25.2%) |
| Testify Direct | `none` | 32 | 133.5 MiB | 262.4 MiB (+96.5%) | 120.1 MiB (-54.2%; -10.0%) |
| Testify Direct | `-race` | 4 | 91.0 MiB | 254.4 MiB (+179.6%) | 119.7 MiB (-53.0%; +31.5%) |
| Testify Direct | `-race` | 32 | 100.3 MiB | 266.4 MiB (+165.5%) | 129.8 MiB (-51.3%; +29.3%) |
| Testify Direct | `-cover` | 4 | 89.2 MiB | 231.8 MiB (+159.7%) | 113.2 MiB (-51.2%; +26.8%) |
| Testify Direct | `-cover` | 32 | 95.2 MiB | 239.4 MiB (+151.5%) | 121.7 MiB (-49.2%; +27.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 81.1 MiB | 220.3 MiB (+171.7%) | 109.9 MiB (-50.1%; +35.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 95.5 MiB | 237.3 MiB (+148.6%) | 119.8 MiB (-49.5%; +25.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 87.9 MiB | 255.9 MiB (+191.2%) | 123.1 MiB (-51.9%; +40.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 121.1 MiB | 293.7 MiB (+142.5%) | 131.7 MiB (-55.2%; +8.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 89.5 MiB | 244.8 MiB (+173.6%) | 110.1 MiB (-55.0%; +23.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 132.9 MiB | 260.3 MiB (+95.9%) | 119.5 MiB (-54.1%; -10.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 99.7 MiB | 271.5 MiB (+172.4%) | 122.4 MiB (-54.9%; +22.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.1 MiB | 294.6 MiB (+149.4%) | 131.8 MiB (-55.3%; +11.6%) |
| Testify External | `none` | 4 | 87.9 MiB | 241.9 MiB (+175.1%) | 106.3 MiB (-56.0%; +20.9%) |
| Testify External | `none` | 32 | 133.3 MiB | 255.6 MiB (+91.8%) | 119.4 MiB (-53.3%; -10.4%) |
| Testify External | `-race` | 4 | 94.6 MiB | 264.6 MiB (+179.7%) | 117.7 MiB (-55.5%; +24.4%) |
| Testify External | `-race` | 32 | 117.4 MiB | 287.2 MiB (+144.7%) | 127.6 MiB (-55.6%; +8.7%) |
| Testify External | `-cover` | 4 | 89.9 MiB | 247.8 MiB (+175.6%) | 108.8 MiB (-56.1%; +21.0%) |
| Testify External | `-cover` | 32 | 145.0 MiB | 266.8 MiB (+84.0%) | 116.9 MiB (-56.2%; -19.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 88.2 MiB | 242.6 MiB (+175.1%) | 110.5 MiB (-54.5%; +25.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 131.1 MiB | 251.2 MiB (+91.7%) | 115.9 MiB (-53.9%; -11.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 94.4 MiB | 268.0 MiB (+184.0%) | 119.4 MiB (-55.5%; +26.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 117.9 MiB | 286.8 MiB (+143.2%) | 128.7 MiB (-55.1%; +9.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 87.1 MiB | 246.8 MiB (+183.3%) | 111.8 MiB (-54.7%; +28.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 126.7 MiB | 259.0 MiB (+104.4%) | 118.0 MiB (-54.4%; -6.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 92.9 MiB | 263.9 MiB (+184.1%) | 120.1 MiB (-54.5%; +29.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 113.8 MiB | 287.0 MiB (+152.1%) | 127.8 MiB (-55.5%; +12.3%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 229.9 MiB | 257.4 MiB (+12.0%) | 233.3 MiB (-9.4%; +1.5%) |
| Gin | `none` | 32 | 288.6 MiB | 334.6 MiB (+16.0%) | 268.3 MiB (-19.8%; -7.0%) |
| Chi | `none` | 4 | 128.9 MiB | 158.0 MiB (+22.6%) | 128.6 MiB (-18.6%; -0.2%) |
| Chi | `none` | 32 | 178.0 MiB | 220.6 MiB (+23.9%) | 163.2 MiB (-26.0%; -8.3%) |
| Gin | `-race` | 4 | 232.3 MiB | 253.0 MiB (+8.9%) | 220.1 MiB (-13.0%; -5.3%) |
| Gin | `-race` | 32 | 244.6 MiB | 288.1 MiB (+17.8%) | 240.9 MiB (-16.4%; -1.5%) |
| Gin | `-cover` | 4 | 245.2 MiB | 384.0 MiB (+56.6%) | 241.8 MiB (-37.0%; -1.4%) |
| Gin | `-cover` | 32 | 320.4 MiB | 455.4 MiB (+42.1%) | 300.4 MiB (-34.0%; -6.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 259.5 MiB | 401.5 MiB (+54.7%) | 253.3 MiB (-36.9%; -2.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 312.0 MiB | 492.1 MiB (+57.7%) | 306.2 MiB (-37.8%; -1.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 247.5 MiB | 383.5 MiB (+55.0%) | 253.4 MiB (-33.9%; +2.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 264.1 MiB | 435.3 MiB (+64.8%) | 263.9 MiB (-39.4%; -0.1%) |
| Chi | `-race` | 4 | 104.0 MiB | 129.6 MiB (+24.7%) | 106.8 MiB (-17.6%; +2.8%) |
| Chi | `-race` | 32 | 126.6 MiB | 155.1 MiB (+22.5%) | 124.2 MiB (-19.9%; -1.9%) |
| Chi | `-cover` | 4 | 135.4 MiB | 165.6 MiB (+22.3%) | 140.7 MiB (-15.0%; +3.9%) |
| Chi | `-cover` | 32 | 176.3 MiB | 213.5 MiB (+21.1%) | 162.2 MiB (-24.0%; -8.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.5 MiB | 168.0 MiB (+19.6%) | 143.6 MiB (-14.5%; +2.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 172.0 MiB | 208.7 MiB (+21.3%) | 162.4 MiB (-22.2%; -5.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.2 MiB | 141.9 MiB (+28.7%) | 118.8 MiB (-16.3%; +7.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 134.1 MiB | 174.5 MiB (+30.2%) | 129.2 MiB (-25.9%; -3.6%) |
| Testify Direct | `none` | 4 | 80.0 MiB | 101.2 MiB (+26.4%) | 75.5 MiB (-25.4%; -5.6%) |
| Testify Direct | `none` | 32 | 133.1 MiB | 146.7 MiB (+10.3%) | 98.3 MiB (-33.0%; -26.1%) |
| Testify Direct | `-race` | 4 | 63.1 MiB | 82.3 MiB (+30.6%) | 70.7 MiB (-14.1%; +12.2%) |
| Testify Direct | `-race` | 32 | 64.4 MiB | 84.2 MiB (+30.8%) | 81.6 MiB (-3.1%; +26.8%) |
| Testify Direct | `-cover` | 4 | 67.5 MiB | 82.8 MiB (+22.7%) | 78.0 MiB (-5.9%; +15.5%) |
| Testify Direct | `-cover` | 32 | 93.8 MiB | 101.3 MiB (+8.1%) | 99.5 MiB (-1.8%; +6.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 67.5 MiB | 82.2 MiB (+21.7%) | 80.2 MiB (-2.4%; +18.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 85.8 MiB | 102.2 MiB (+19.1%) | 101.7 MiB (-0.5%; +18.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 55.9 MiB | 70.2 MiB (+25.7%) | 70.4 MiB (+0.2%; +26.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 93.2 MiB | 123.9 MiB (+32.9%) | 81.3 MiB (-34.4%; -12.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 77.2 MiB | 110.4 MiB (+43.0%) | 77.1 MiB (-30.1%; -0.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 123.9 MiB | 128.1 MiB (+3.3%) | 101.4 MiB (-20.8%; -18.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 73.7 MiB | 91.5 MiB (+24.2%) | 70.2 MiB (-23.3%; -4.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 88.8 MiB | 124.8 MiB (+40.5%) | 81.6 MiB (-34.6%; -8.0%) |
| Testify External | `none` | 4 | 57.1 MiB | 87.7 MiB (+53.6%) | 56.2 MiB (-35.9%; -1.5%) |
| Testify External | `none` | 32 | 87.9 MiB | 144.4 MiB (+64.2%) | 75.0 MiB (-48.1%; -14.7%) |
| Testify External | `-race` | 4 | 56.2 MiB | 90.7 MiB (+61.3%) | 57.9 MiB (-36.2%; +3.0%) |
| Testify External | `-race` | 32 | 85.1 MiB | 129.7 MiB (+52.4%) | 77.2 MiB (-40.5%; -9.3%) |
| Testify External | `-cover` | 4 | 57.0 MiB | 97.6 MiB (+71.3%) | 57.1 MiB (-41.5%; +0.1%) |
| Testify External | `-cover` | 32 | 90.2 MiB | 115.7 MiB (+28.3%) | 78.4 MiB (-32.3%; -13.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 57.8 MiB | 86.3 MiB (+49.3%) | 57.0 MiB (-33.9%; -1.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 102.4 MiB | 128.9 MiB (+25.8%) | 75.0 MiB (-41.8%; -26.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 53.8 MiB | 84.4 MiB (+56.9%) | 58.2 MiB (-31.1%; +8.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 82.3 MiB | 119.7 MiB (+45.4%) | 76.5 MiB (-36.1%; -7.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 54.9 MiB | 94.0 MiB (+71.3%) | 57.3 MiB (-39.0%; +4.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 97.0 MiB | 137.5 MiB (+41.8%) | 75.9 MiB (-44.8%; -21.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 56.8 MiB | 93.1 MiB (+64.0%) | 61.2 MiB (-34.2%; +7.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 84.5 MiB | 123.5 MiB (+46.1%) | 79.1 MiB (-36.0%; -6.5%) |

## Runtime

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 36.6 MiB | 60.3 MiB (+64.5%) | 38.9 MiB (-35.4%; +6.3%) | 38.6 MiB (-36.0%; +5.3%) |
| Gin | `none` | 32 | 42.2 MiB | 70.7 MiB (+67.6%) | 53.1 MiB (-24.9%; +25.8%) | 51.1 MiB (-27.7%; +21.2%) |
| Chi | `none` | 4 | 45.6 MiB | 79.7 MiB (+74.5%) | 32.1 MiB (-59.7%; -29.6%) | 31.5 MiB (-60.5%; -31.0%) |
| Chi | `none` | 32 | 94.7 MiB | 128.0 MiB (+35.2%) | 57.5 MiB (-55.1%; -39.3%) | 67.8 MiB (-47.0%; -28.4%) |
| Gin | `-race` | 4 | 199.3 MiB | FAIL 5/6 | 187.3 MiB (vs Orchestrion unavailable; -6.0% vs Native) | 187.9 MiB (vs Orchestrion unavailable; -5.7% vs Native) |
| Gin | `-race` | 32 | 262.9 MiB | FAIL 6/6 | 324.4 MiB (vs Orchestrion unavailable; +23.4% vs Native) | 323.6 MiB (vs Orchestrion unavailable; +23.1% vs Native) |
| Gin | `-cover` | 4 | 35.9 MiB | 92.6 MiB (+158.2%) | 40.7 MiB (-56.1%; +13.4%) | 40.4 MiB (-56.4%; +12.7%) |
| Gin | `-cover` | 32 | 42.1 MiB | 165.2 MiB (+292.3%) | 52.5 MiB (-68.2%; +24.6%) | 52.8 MiB (-68.1%; +25.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 35.7 MiB | 98.6 MiB (+176.0%) | 75.1 MiB (-23.8%; +110.3%) | 74.6 MiB (-24.4%; +108.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 52.1 MiB | 200.9 MiB (+285.8%) | 144.2 MiB (-28.2%; +176.9%) | 146.3 MiB (-27.2%; +181.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 243.9 MiB | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 283.8 MiB | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 2958.8 MiB | 2979.4 MiB (+0.7%) | 3005.7 MiB (+0.9%; +1.6%) | 3004.1 MiB (+0.8%; +1.5%) |
| Chi | `-race` | 32 | 3068.8 MiB | 3112.4 MiB (+1.4%) | 3051.0 MiB (-2.0%; -0.6%) | 3050.7 MiB (-2.0%; -0.6%) |
| Chi | `-cover` | 4 | 43.0 MiB | 85.0 MiB (+97.6%) | 32.6 MiB (-61.7%; -24.2%) | 32.2 MiB (-62.1%; -25.0%) |
| Chi | `-cover` | 32 | 88.6 MiB | 149.1 MiB (+68.2%) | 59.1 MiB (-60.3%; -33.3%) | 67.8 MiB (-54.6%; -23.5%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 45.1 MiB | 98.8 MiB (+119.0%) | 41.7 MiB (-57.8%; -7.5%) | 41.3 MiB (-58.2%; -8.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 84.1 MiB | 145.2 MiB (+72.6%) | 63.7 MiB (-56.1%; -24.3%) | 67.1 MiB (-53.8%; -20.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 2944.4 MiB | 3073.4 MiB (+4.4%) | 2964.3 MiB (-3.6%; +0.7%) | 2955.4 MiB (-3.8%; +0.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 2940.6 MiB | 3072.5 MiB (+4.5%) | 2953.3 MiB (-3.9%; +0.4%) | 2947.4 MiB (-4.1%; +0.2%) |
| Testify Direct | `none` | 4 | 14.5 MiB | 26.6 MiB (+83.0%) | 13.4 MiB (-49.6%; -7.8%) | 13.0 MiB (-51.2%; -10.7%) |
| Testify Direct | `none` | 32 | 15.0 MiB | 29.4 MiB (+96.5%) | 15.4 MiB (-47.5%; +3.2%) | 14.6 MiB (-50.5%; -2.7%) |
| Testify Direct | `-race` | 4 | 34.1 MiB | 67.5 MiB (+98.0%) | 31.9 MiB (-52.7%; -6.3%) | 31.2 MiB (-53.7%; -8.4%) |
| Testify Direct | `-race` | 32 | 37.2 MiB | 73.4 MiB (+97.2%) | 35.6 MiB (-51.5%; -4.3%) | 34.0 MiB (-53.7%; -8.8%) |
| Testify Direct | `-cover` | 4 | 14.5 MiB | 29.7 MiB (+105.0%) | 13.2 MiB (-55.5%; -8.7%) | 13.4 MiB (-54.8%; -7.4%) |
| Testify Direct | `-cover` | 32 | 14.7 MiB | 33.8 MiB (+129.5%) | 14.8 MiB (-56.2%; +0.5%) | 16.6 MiB (-50.8%; +13.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 27.6 MiB (+92.9%) | 15.6 MiB (-43.4%; +9.2%) | 15.5 MiB (-43.9%; +8.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 14.8 MiB | 34.0 MiB (+130.6%) | 18.6 MiB (-45.3%; +26.0%) | 17.7 MiB (-48.1%; +19.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 34.0 MiB | 66.7 MiB (+96.4%) | 34.1 MiB (-48.8%; +0.5%) | 32.8 MiB (-50.9%; -3.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.8 MiB | 72.1 MiB (+101.7%) | 39.4 MiB (-45.4%; +10.2%) | 38.5 MiB (-46.6%; +7.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.8 MiB | 27.7 MiB (+87.3%) | 15.9 MiB (-42.8%; +7.2%) | 15.8 MiB (-42.9%; +7.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 15.0 MiB | 35.2 MiB (+135.2%) | 19.6 MiB (-44.4%; +30.7%) | 19.2 MiB (-45.3%; +28.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.4 MiB (+110.6%) | 39.4 MiB (-47.1%; +11.4%) | 38.0 MiB (-48.9%; +7.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.7 MiB | 83.5 MiB (+127.4%) | 44.9 MiB (-46.2%; +22.3%) | 43.4 MiB (-48.1%; +18.1%) |
| Testify External | `none` | 4 | 14.3 MiB | 27.1 MiB (+89.1%) | 13.2 MiB (-51.2%; -7.7%) | 13.0 MiB (-52.2%; -9.6%) |
| Testify External | `none` | 32 | 14.5 MiB | 32.3 MiB (+123.1%) | 15.1 MiB (-53.2%; +4.5%) | 15.1 MiB (-53.3%; +4.2%) |
| Testify External | `-race` | 4 | 33.8 MiB | 68.8 MiB (+103.5%) | 32.5 MiB (-52.8%; -4.1%) | 31.5 MiB (-54.3%; -6.9%) |
| Testify External | `-race` | 32 | 37.0 MiB | 75.3 MiB (+103.7%) | 35.2 MiB (-53.2%; -4.7%) | 33.8 MiB (-55.2%; -8.7%) |
| Testify External | `-cover` | 4 | 14.2 MiB | 29.4 MiB (+106.7%) | 13.4 MiB (-54.3%; -5.7%) | 13.3 MiB (-54.8%; -6.7%) |
| Testify External | `-cover` | 32 | 15.0 MiB | 34.4 MiB (+129.0%) | 14.9 MiB (-56.8%; -1.0%) | 14.6 MiB (-57.6%; -3.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 28.2 MiB (+96.5%) | 15.9 MiB (-43.4%; +11.2%) | 15.5 MiB (-45.0%; +8.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 15.0 MiB | 34.4 MiB (+129.0%) | 18.5 MiB (-46.0%; +23.5%) | 18.3 MiB (-46.7%; +22.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 32.8 MiB | 67.1 MiB (+104.6%) | 34.4 MiB (-48.7%; +4.9%) | 33.0 MiB (-50.9%; +0.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.5 MiB | 73.1 MiB (+105.8%) | 39.6 MiB (-45.8%; +11.6%) | 38.7 MiB (-47.0%; +9.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.6 MiB | 28.0 MiB (+91.9%) | 16.0 MiB (-42.9%; +9.6%) | 16.3 MiB (-41.9%; +11.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 16.8 MiB | 34.5 MiB (+105.9%) | 19.0 MiB (-44.8%; +13.7%) | 19.0 MiB (-44.8%; +13.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.3 MiB (+110.7%) | 39.5 MiB (-46.8%; +12.0%) | 38.1 MiB (-48.7%; +8.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.3 MiB | 73.0 MiB (+101.3%) | 45.0 MiB (-38.4%; +24.0%) | 43.6 MiB (-40.3%; +20.3%) |

## Gin: local Agent delivery and telemetry

Three measured runs follow one warmup.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 41.4 MiB | 77.5 MiB (+87.1%) | 39.3 MiB (-49.3%; -5.2%) |
| Gin | `none` | 32 | 41.5 MiB | 78.2 MiB (+88.3%) | 54.6 MiB (-30.2%; +31.4%) |
