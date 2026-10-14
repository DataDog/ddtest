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
| Gin | `none` | 4 | 1674.3 MiB | 2480.6 MiB (+48.2%) | 1731.3 MiB (-30.2%; +3.4%) |
| Gin | `none` | 32 | 1851.3 MiB | 2639.9 MiB (+42.6%) | 1769.2 MiB (-33.0%; -4.4%) |
| Chi | `none` | 4 | 464.5 MiB | 1292.0 MiB (+178.1%) | 515.8 MiB (-60.1%; +11.0%) |
| Chi | `none` | 32 | 699.1 MiB | 2021.4 MiB (+189.2%) | 675.1 MiB (-66.6%; -3.4%) |
| Gin | `-race` | 4 | 1694.1 MiB | 2697.3 MiB (+59.2%) | 1685.2 MiB (-37.5%; -0.5%) |
| Gin | `-race` | 32 | 1738.9 MiB | 2693.4 MiB (+54.9%) | 1719.1 MiB (-36.2%; -1.1%) |
| Gin | `-cover` | 4 | 1685.3 MiB | 2624.1 MiB (+55.7%) | 1735.3 MiB (-33.9%; +3.0%) |
| Gin | `-cover` | 32 | 1850.4 MiB | 2862.9 MiB (+54.7%) | 1756.3 MiB (-38.7%; -5.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1685.4 MiB | 2598.8 MiB (+54.2%) | 1758.9 MiB (-32.3%; +4.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1890.4 MiB | 2713.6 MiB (+43.5%) | 1748.4 MiB (-35.6%; -7.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1756.8 MiB | 2792.1 MiB (+58.9%) | 1759.7 MiB (-37.0%; +0.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1790.8 MiB | 2726.9 MiB (+52.3%) | 1767.4 MiB (-35.2%; -1.3%) |
| Chi | `-race` | 4 | 415.8 MiB | 1390.6 MiB (+234.4%) | 537.0 MiB (-61.4%; +29.2%) |
| Chi | `-race` | 32 | 665.4 MiB | 1674.4 MiB (+151.6%) | 584.7 MiB (-65.1%; -12.1%) |
| Chi | `-cover` | 4 | 466.0 MiB | 1308.2 MiB (+180.8%) | 524.2 MiB (-59.9%; +12.5%) |
| Chi | `-cover` | 32 | 746.3 MiB | 2152.3 MiB (+188.4%) | 685.6 MiB (-68.1%; -8.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 479.6 MiB | 1306.7 MiB (+172.4%) | 524.9 MiB (-59.8%; +9.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 762.6 MiB | 2137.0 MiB (+180.2%) | 700.2 MiB (-67.2%; -8.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 431.9 MiB | 1395.4 MiB (+223.1%) | 541.3 MiB (-61.2%; +25.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 642.9 MiB | 1608.4 MiB (+150.2%) | 606.4 MiB (-62.3%; -5.7%) |
| Testify Direct | `none` | 4 | 446.0 MiB | 1015.8 MiB (+127.8%) | 514.1 MiB (-49.4%; +15.3%) |
| Testify Direct | `none` | 32 | 732.8 MiB | 1319.7 MiB (+80.1%) | 668.9 MiB (-49.3%; -8.7%) |
| Testify Direct | `-race` | 4 | 400.5 MiB | 1029.6 MiB (+157.1%) | 535.8 MiB (-48.0%; +33.8%) |
| Testify Direct | `-race` | 32 | 594.0 MiB | 1182.8 MiB (+99.1%) | 611.0 MiB (-48.3%; +2.9%) |
| Testify Direct | `-cover` | 4 | 409.4 MiB | 992.5 MiB (+142.4%) | 523.3 MiB (-47.3%; +27.8%) |
| Testify Direct | `-cover` | 32 | 697.3 MiB | 1444.5 MiB (+107.2%) | 750.9 MiB (-48.0%; +7.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 404.1 MiB | 992.9 MiB (+145.7%) | 521.9 MiB (-47.4%; +29.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 662.7 MiB | 1405.3 MiB (+112.0%) | 700.8 MiB (-50.1%; +5.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 395.1 MiB | 968.7 MiB (+145.1%) | 542.7 MiB (-44.0%; +37.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 591.0 MiB | 1196.5 MiB (+102.5%) | 621.6 MiB (-48.0%; +5.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 436.9 MiB | 1012.0 MiB (+131.6%) | 524.0 MiB (-48.2%; +19.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 728.2 MiB | 1374.6 MiB (+88.8%) | 702.3 MiB (-48.9%; -3.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 412.4 MiB | 1093.2 MiB (+165.1%) | 542.9 MiB (-50.3%; +31.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 638.6 MiB | 1247.0 MiB (+95.3%) | 601.8 MiB (-51.7%; -5.8%) |
| Testify External | `none` | 4 | 436.4 MiB | 1007.9 MiB (+131.0%) | 520.6 MiB (-48.3%; +19.3%) |
| Testify External | `none` | 32 | 711.6 MiB | 1358.1 MiB (+90.9%) | 716.9 MiB (-47.2%; +0.7%) |
| Testify External | `-race` | 4 | 395.3 MiB | 1080.2 MiB (+173.2%) | 534.3 MiB (-50.5%; +35.2%) |
| Testify External | `-race` | 32 | 627.3 MiB | 1207.5 MiB (+92.5%) | 606.2 MiB (-49.8%; -3.4%) |
| Testify External | `-cover` | 4 | 420.4 MiB | 1014.6 MiB (+141.4%) | 525.9 MiB (-48.2%; +25.1%) |
| Testify External | `-cover` | 32 | 748.0 MiB | 1348.5 MiB (+80.3%) | 725.8 MiB (-46.2%; -3.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 420.3 MiB | 1014.8 MiB (+141.4%) | 527.4 MiB (-48.0%; +25.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 692.0 MiB | 1306.1 MiB (+88.7%) | 701.1 MiB (-46.3%; +1.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 408.0 MiB | 1071.2 MiB (+162.6%) | 541.7 MiB (-49.4%; +32.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 612.2 MiB | 1206.7 MiB (+97.1%) | 620.1 MiB (-48.6%; +1.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 455.9 MiB | 1025.8 MiB (+125.0%) | 526.3 MiB (-48.7%; +15.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 694.1 MiB | 1293.6 MiB (+86.4%) | 688.1 MiB (-46.8%; -0.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 414.6 MiB | 1089.6 MiB (+162.8%) | 545.0 MiB (-50.0%; +31.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 617.8 MiB | 1241.4 MiB (+100.9%) | 617.3 MiB (-50.3%; -0.1%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 49.9 MiB | 85.4 MiB (+71.1%) | 52.0 MiB (-39.1%; +4.2%) |
| Gin | `none` | 32 | 67.0 MiB | 117.3 MiB (+75.1%) | 64.9 MiB (-44.6%; -3.1%) |
| Chi | `none` | 4 | 41.8 MiB | 73.6 MiB (+75.9%) | 42.5 MiB (-42.3%; +1.5%) |
| Chi | `none` | 32 | 57.0 MiB | 108.3 MiB (+90.1%) | 52.9 MiB (-51.2%; -7.2%) |
| Gin | `-race` | 4 | 50.3 MiB | 86.4 MiB (+71.6%) | 55.9 MiB (-35.4%; +11.0%) |
| Gin | `-race` | 32 | 68.8 MiB | 119.4 MiB (+73.5%) | 68.2 MiB (-42.9%; -1.0%) |
| Gin | `-cover` | 4 | 66.9 MiB | 99.0 MiB (+47.9%) | 65.1 MiB (-34.2%; -2.7%) |
| Gin | `-cover` | 32 | 87.1 MiB | 148.8 MiB (+70.8%) | 80.2 MiB (-46.1%; -8.0%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 65.6 MiB | 109.4 MiB (+66.7%) | 64.5 MiB (-41.1%; -1.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 90.0 MiB | 139.0 MiB (+54.4%) | 80.9 MiB (-41.8%; -10.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 68.5 MiB | 100.4 MiB (+46.6%) | 68.8 MiB (-31.5%; +0.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 91.2 MiB | 142.7 MiB (+56.4%) | 82.2 MiB (-42.4%; -9.9%) |
| Chi | `-race` | 4 | 41.9 MiB | 79.0 MiB (+88.5%) | 47.6 MiB (-39.7%; +13.6%) |
| Chi | `-race` | 32 | 60.1 MiB | 109.4 MiB (+82.0%) | 58.0 MiB (-46.9%; -3.5%) |
| Chi | `-cover` | 4 | 42.0 MiB | 77.0 MiB (+83.6%) | 45.4 MiB (-41.1%; +8.2%) |
| Chi | `-cover` | 32 | 62.5 MiB | 118.0 MiB (+88.9%) | 55.7 MiB (-52.7%; -10.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 43.7 MiB | 79.8 MiB (+82.3%) | 44.1 MiB (-44.7%; +0.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 58.7 MiB | 117.1 MiB (+99.5%) | 55.5 MiB (-52.6%; -5.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 43.4 MiB | 72.6 MiB (+67.2%) | 48.4 MiB (-33.3%; +11.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 114.4 MiB (+94.5%) | 59.6 MiB (-47.9%; +1.3%) |
| Testify Direct | `none` | 4 | 41.5 MiB | 72.3 MiB (+74.2%) | 41.9 MiB (-42.0%; +1.0%) |
| Testify Direct | `none` | 32 | 58.0 MiB | 104.4 MiB (+80.0%) | 54.0 MiB (-48.2%; -6.8%) |
| Testify Direct | `-race` | 4 | 41.8 MiB | 73.3 MiB (+75.2%) | 48.6 MiB (-33.7%; +16.2%) |
| Testify Direct | `-race` | 32 | 46.2 MiB | 75.9 MiB (+64.3%) | 58.4 MiB (-23.1%; +26.4%) |
| Testify Direct | `-cover` | 4 | 42.6 MiB | 68.1 MiB (+59.9%) | 44.2 MiB (-35.1%; +3.8%) |
| Testify Direct | `-cover` | 32 | 43.6 MiB | 74.6 MiB (+71.1%) | 54.9 MiB (-26.5%; +25.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 33.8 MiB | 59.4 MiB (+75.7%) | 43.7 MiB (-26.5%; +29.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 43.2 MiB | 73.6 MiB (+70.2%) | 54.1 MiB (-26.5%; +25.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 35.4 MiB | 56.8 MiB (+60.4%) | 48.2 MiB (-15.1%; +36.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 57.5 MiB | 103.5 MiB (+79.9%) | 57.2 MiB (-44.7%; -0.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.5 MiB | 77.2 MiB (+85.9%) | 43.8 MiB (-43.3%; +5.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.2 MiB | 106.0 MiB (+85.3%) | 55.5 MiB (-47.6%; -2.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.4 MiB | 78.4 MiB (+89.6%) | 48.8 MiB (-37.8%; +18.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 104.6 MiB (+80.8%) | 60.6 MiB (-42.1%; +4.8%) |
| Testify External | `none` | 4 | 41.8 MiB | 72.4 MiB (+73.1%) | 43.3 MiB (-40.2%; +3.6%) |
| Testify External | `none` | 32 | 54.3 MiB | 102.7 MiB (+89.3%) | 53.2 MiB (-48.2%; -1.9%) |
| Testify External | `-race` | 4 | 42.4 MiB | 71.8 MiB (+69.3%) | 46.9 MiB (-34.7%; +10.6%) |
| Testify External | `-race` | 32 | 58.1 MiB | 107.8 MiB (+85.6%) | 57.5 MiB (-46.7%; -1.0%) |
| Testify External | `-cover` | 4 | 43.4 MiB | 72.4 MiB (+67.1%) | 43.7 MiB (-39.7%; +0.8%) |
| Testify External | `-cover` | 32 | 57.3 MiB | 110.1 MiB (+92.0%) | 55.0 MiB (-50.0%; -4.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 42.0 MiB | 74.4 MiB (+77.0%) | 44.5 MiB (-40.1%; +5.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 94.2 MiB (+60.1%) | 54.5 MiB (-42.1%; -7.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 41.5 MiB | 70.1 MiB (+68.9%) | 47.9 MiB (-31.7%; +15.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 56.6 MiB | 102.4 MiB (+81.0%) | 57.6 MiB (-43.7%; +1.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.7 MiB | 74.4 MiB (+74.4%) | 44.0 MiB (-40.8%; +3.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 108.6 MiB (+87.4%) | 56.2 MiB (-48.3%; -3.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.0 MiB | 77.3 MiB (+84.0%) | 49.3 MiB (-36.2%; +17.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 60.0 MiB | 105.9 MiB (+76.4%) | 60.3 MiB (-43.0%; +0.6%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 340.3 MiB | 816.5 MiB (+139.9%) | 421.8 MiB (-48.3%; +23.9%) |
| Gin | `none` | 32 | 411.5 MiB | 1141.9 MiB (+177.5%) | 482.4 MiB (-57.8%; +17.2%) |
| Chi | `none` | 4 | 137.0 MiB | 377.7 MiB (+175.8%) | 158.4 MiB (-58.1%; +15.7%) |
| Chi | `none` | 32 | 182.8 MiB | 429.9 MiB (+135.2%) | 178.3 MiB (-58.5%; -2.5%) |
| Gin | `-race` | 4 | 429.4 MiB | 928.1 MiB (+116.1%) | 477.5 MiB (-48.6%; +11.2%) |
| Gin | `-race` | 32 | 512.1 MiB | 1479.3 MiB (+188.9%) | 561.5 MiB (-62.0%; +9.7%) |
| Gin | `-cover` | 4 | 351.6 MiB | 787.4 MiB (+123.9%) | 423.4 MiB (-46.2%; +20.4%) |
| Gin | `-cover` | 32 | 405.2 MiB | 1147.3 MiB (+183.2%) | 488.1 MiB (-57.5%; +20.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 352.6 MiB | 797.0 MiB (+126.0%) | 441.1 MiB (-44.7%; +25.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 411.7 MiB | 1158.4 MiB (+181.4%) | 485.9 MiB (-58.1%; +18.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 436.7 MiB | 1000.6 MiB (+129.1%) | 512.8 MiB (-48.7%; +17.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 505.7 MiB | 1390.4 MiB (+174.9%) | 567.1 MiB (-59.2%; +12.1%) |
| Chi | `-race` | 4 | 150.7 MiB | 434.1 MiB (+188.0%) | 177.2 MiB (-59.2%; +17.6%) |
| Chi | `-race` | 32 | 178.9 MiB | 461.2 MiB (+157.8%) | 191.4 MiB (-58.5%; +7.0%) |
| Chi | `-cover` | 4 | 140.1 MiB | 375.4 MiB (+168.0%) | 162.2 MiB (-56.8%; +15.8%) |
| Chi | `-cover` | 32 | 182.2 MiB | 429.8 MiB (+135.9%) | 180.2 MiB (-58.1%; -1.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 141.1 MiB | 376.5 MiB (+166.9%) | 165.0 MiB (-56.2%; +16.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 184.0 MiB | 441.0 MiB (+139.6%) | 180.2 MiB (-59.1%; -2.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 151.1 MiB | 415.6 MiB (+175.0%) | 182.3 MiB (-56.1%; +20.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 184.3 MiB | 456.5 MiB (+147.7%) | 195.0 MiB (-57.3%; +5.8%) |
| Testify Direct | `none` | 4 | 86.3 MiB | 242.6 MiB (+181.2%) | 105.7 MiB (-56.5%; +22.5%) |
| Testify Direct | `none` | 32 | 116.8 MiB | 266.2 MiB (+127.9%) | 115.4 MiB (-56.6%; -1.2%) |
| Testify Direct | `-race` | 4 | 95.1 MiB | 269.0 MiB (+183.0%) | 116.6 MiB (-56.7%; +22.7%) |
| Testify Direct | `-race` | 32 | 97.7 MiB | 265.3 MiB (+171.6%) | 128.3 MiB (-51.7%; +31.3%) |
| Testify Direct | `-cover` | 4 | 84.6 MiB | 229.6 MiB (+171.3%) | 108.6 MiB (-52.7%; +28.3%) |
| Testify Direct | `-cover` | 32 | 93.9 MiB | 235.9 MiB (+151.3%) | 118.7 MiB (-49.7%; +26.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 84.9 MiB | 222.9 MiB (+162.5%) | 109.1 MiB (-51.1%; +28.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 91.3 MiB | 234.6 MiB (+156.9%) | 117.7 MiB (-49.8%; +28.9%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 89.5 MiB | 253.9 MiB (+183.7%) | 117.5 MiB (-53.7%; +31.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 116.8 MiB | 294.0 MiB (+151.8%) | 127.7 MiB (-56.6%; +9.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.5 MiB | 241.9 MiB (+173.3%) | 108.4 MiB (-55.2%; +22.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 122.2 MiB | 261.9 MiB (+114.3%) | 117.3 MiB (-55.2%; -4.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 95.1 MiB | 263.1 MiB (+176.8%) | 117.9 MiB (-55.2%; +24.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.7 MiB | 294.0 MiB (+147.7%) | 127.9 MiB (-56.5%; +7.8%) |
| Testify External | `none` | 4 | 86.0 MiB | 236.9 MiB (+175.4%) | 107.4 MiB (-54.7%; +24.8%) |
| Testify External | `none` | 32 | 116.0 MiB | 264.0 MiB (+127.6%) | 116.0 MiB (-56.0%; +0.1%) |
| Testify External | `-race` | 4 | 93.8 MiB | 265.0 MiB (+182.5%) | 117.3 MiB (-55.7%; +25.1%) |
| Testify External | `-race` | 32 | 117.4 MiB | 294.0 MiB (+150.4%) | 126.2 MiB (-57.1%; +7.5%) |
| Testify External | `-cover` | 4 | 87.4 MiB | 244.0 MiB (+179.3%) | 107.9 MiB (-55.8%; +23.5%) |
| Testify External | `-cover` | 32 | 119.0 MiB | 265.1 MiB (+122.7%) | 118.3 MiB (-55.4%; -0.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 87.4 MiB | 242.2 MiB (+177.1%) | 107.9 MiB (-55.5%; +23.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 115.3 MiB | 258.6 MiB (+124.3%) | 116.2 MiB (-55.1%; +0.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 92.5 MiB | 265.1 MiB (+186.6%) | 118.9 MiB (-55.1%; +28.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 118.8 MiB | 290.6 MiB (+144.5%) | 127.1 MiB (-56.3%; +6.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.2 MiB | 241.6 MiB (+173.8%) | 106.8 MiB (-55.8%; +21.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.5 MiB | 266.3 MiB (+124.7%) | 116.0 MiB (-56.4%; -2.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 93.3 MiB | 270.2 MiB (+189.7%) | 115.4 MiB (-57.3%; +23.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 115.7 MiB | 293.2 MiB (+153.4%) | 127.5 MiB (-56.5%; +10.2%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 230.3 MiB | 347.5 MiB (+50.9%) | 228.0 MiB (-34.4%; -1.0%) |
| Gin | `none` | 32 | 282.6 MiB | 366.2 MiB (+29.6%) | 266.0 MiB (-27.4%; -5.9%) |
| Chi | `none` | 4 | 130.8 MiB | 234.5 MiB (+79.3%) | 129.7 MiB (-44.7%; -0.8%) |
| Chi | `none` | 32 | 180.2 MiB | 266.9 MiB (+48.1%) | 155.9 MiB (-41.6%; -13.5%) |
| Gin | `-race` | 4 | 228.7 MiB | 384.3 MiB (+68.0%) | 218.7 MiB (-43.1%; -4.4%) |
| Gin | `-race` | 32 | 243.3 MiB | 403.4 MiB (+65.8%) | 236.0 MiB (-41.5%; -3.0%) |
| Gin | `-cover` | 4 | 244.2 MiB | 375.7 MiB (+53.8%) | 242.8 MiB (-35.4%; -0.6%) |
| Gin | `-cover` | 32 | 317.7 MiB | 466.9 MiB (+47.0%) | 296.8 MiB (-36.4%; -6.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 254.0 MiB | 399.8 MiB (+57.4%) | 255.1 MiB (-36.2%; +0.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 323.7 MiB | 479.7 MiB (+48.2%) | 306.2 MiB (-36.2%; -5.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 240.8 MiB | 394.1 MiB (+63.7%) | 246.3 MiB (-37.5%; +2.3%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 262.1 MiB | 413.7 MiB (+57.8%) | 256.7 MiB (-37.9%; -2.1%) |
| Chi | `-race` | 4 | 102.7 MiB | 254.8 MiB (+148.0%) | 116.7 MiB (-54.2%; +13.6%) |
| Chi | `-race` | 32 | 129.1 MiB | 292.0 MiB (+126.2%) | 125.9 MiB (-56.9%; -2.5%) |
| Chi | `-cover` | 4 | 137.5 MiB | 234.7 MiB (+70.7%) | 137.6 MiB (-41.4%; +0.1%) |
| Chi | `-cover` | 32 | 174.1 MiB | 271.3 MiB (+55.8%) | 156.8 MiB (-42.2%; -10.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.1 MiB | 235.6 MiB (+68.2%) | 143.2 MiB (-39.2%; +2.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 174.9 MiB | 270.1 MiB (+54.4%) | 162.9 MiB (-39.7%; -6.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.9 MiB | 256.6 MiB (+131.4%) | 118.5 MiB (-53.8%; +6.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 137.9 MiB | 295.3 MiB (+114.2%) | 129.4 MiB (-56.2%; -6.1%) |
| Testify Direct | `none` | 4 | 88.6 MiB | 241.8 MiB (+172.9%) | 109.0 MiB (-54.9%; +23.0%) |
| Testify Direct | `none` | 32 | 133.5 MiB | 262.4 MiB (+96.5%) | 115.9 MiB (-55.8%; -13.2%) |
| Testify Direct | `-race` | 4 | 91.0 MiB | 254.4 MiB (+179.6%) | 118.9 MiB (-53.3%; +30.7%) |
| Testify Direct | `-race` | 32 | 100.3 MiB | 266.4 MiB (+165.5%) | 128.4 MiB (-51.8%; +28.0%) |
| Testify Direct | `-cover` | 4 | 89.2 MiB | 231.8 MiB (+159.7%) | 110.6 MiB (-52.3%; +23.9%) |
| Testify Direct | `-cover` | 32 | 95.2 MiB | 239.4 MiB (+151.5%) | 119.1 MiB (-50.3%; +25.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 81.1 MiB | 220.3 MiB (+171.7%) | 110.9 MiB (-49.6%; +36.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 95.5 MiB | 237.3 MiB (+148.6%) | 119.0 MiB (-49.9%; +24.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 87.9 MiB | 255.9 MiB (+191.2%) | 121.9 MiB (-52.4%; +38.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 121.1 MiB | 293.7 MiB (+142.5%) | 130.3 MiB (-55.6%; +7.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 89.5 MiB | 244.8 MiB (+173.6%) | 108.9 MiB (-55.5%; +21.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 132.9 MiB | 260.3 MiB (+95.9%) | 118.8 MiB (-54.3%; -10.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 99.7 MiB | 271.5 MiB (+172.4%) | 121.3 MiB (-55.3%; +21.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.1 MiB | 294.6 MiB (+149.4%) | 128.7 MiB (-56.3%; +8.9%) |
| Testify External | `none` | 4 | 87.9 MiB | 241.9 MiB (+175.1%) | 106.8 MiB (-55.8%; +21.5%) |
| Testify External | `none` | 32 | 133.3 MiB | 255.6 MiB (+91.8%) | 116.6 MiB (-54.4%; -12.5%) |
| Testify External | `-race` | 4 | 94.6 MiB | 264.6 MiB (+179.7%) | 117.0 MiB (-55.8%; +23.7%) |
| Testify External | `-race` | 32 | 117.4 MiB | 287.2 MiB (+144.7%) | 128.1 MiB (-55.4%; +9.1%) |
| Testify External | `-cover` | 4 | 89.9 MiB | 247.8 MiB (+175.6%) | 107.6 MiB (-56.6%; +19.7%) |
| Testify External | `-cover` | 32 | 145.0 MiB | 266.8 MiB (+84.0%) | 117.2 MiB (-56.1%; -19.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 88.2 MiB | 242.6 MiB (+175.1%) | 112.3 MiB (-53.7%; +27.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 131.1 MiB | 251.2 MiB (+91.7%) | 116.3 MiB (-53.7%; -11.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 94.4 MiB | 268.0 MiB (+184.0%) | 119.2 MiB (-55.5%; +26.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 117.9 MiB | 286.8 MiB (+143.2%) | 125.8 MiB (-56.1%; +6.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 87.1 MiB | 246.8 MiB (+183.3%) | 107.8 MiB (-56.3%; +23.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 126.7 MiB | 259.0 MiB (+104.4%) | 113.5 MiB (-56.2%; -10.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 92.9 MiB | 263.9 MiB (+184.1%) | 119.3 MiB (-54.8%; +28.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 113.8 MiB | 287.0 MiB (+152.1%) | 127.7 MiB (-55.5%; +12.2%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 229.9 MiB | 257.4 MiB (+12.0%) | 231.5 MiB (-10.1%; +0.7%) |
| Gin | `none` | 32 | 288.6 MiB | 334.6 MiB (+16.0%) | 270.6 MiB (-19.1%; -6.2%) |
| Chi | `none` | 4 | 128.9 MiB | 158.0 MiB (+22.6%) | 130.0 MiB (-17.7%; +0.8%) |
| Chi | `none` | 32 | 178.0 MiB | 220.6 MiB (+23.9%) | 156.8 MiB (-28.9%; -11.9%) |
| Gin | `-race` | 4 | 232.3 MiB | 253.0 MiB (+8.9%) | 222.5 MiB (-12.0%; -4.2%) |
| Gin | `-race` | 32 | 244.6 MiB | 288.1 MiB (+17.8%) | 238.4 MiB (-17.3%; -2.5%) |
| Gin | `-cover` | 4 | 245.2 MiB | 384.0 MiB (+56.6%) | 248.6 MiB (-35.3%; +1.4%) |
| Gin | `-cover` | 32 | 320.4 MiB | 455.4 MiB (+42.1%) | 291.9 MiB (-35.9%; -8.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 259.5 MiB | 401.5 MiB (+54.7%) | 263.4 MiB (-34.4%; +1.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 312.0 MiB | 492.1 MiB (+57.7%) | 305.2 MiB (-38.0%; -2.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 247.5 MiB | 383.5 MiB (+55.0%) | 241.0 MiB (-37.1%; -2.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 264.1 MiB | 435.3 MiB (+64.8%) | 262.5 MiB (-39.7%; -0.6%) |
| Chi | `-race` | 4 | 104.0 MiB | 129.6 MiB (+24.7%) | 108.3 MiB (-16.4%; +4.2%) |
| Chi | `-race` | 32 | 126.6 MiB | 155.1 MiB (+22.5%) | 121.4 MiB (-21.8%; -4.2%) |
| Chi | `-cover` | 4 | 135.4 MiB | 165.6 MiB (+22.3%) | 136.7 MiB (-17.4%; +0.9%) |
| Chi | `-cover` | 32 | 176.3 MiB | 213.5 MiB (+21.1%) | 156.6 MiB (-26.6%; -11.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.5 MiB | 168.0 MiB (+19.6%) | 146.2 MiB (-13.0%; +4.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 172.0 MiB | 208.7 MiB (+21.3%) | 160.3 MiB (-23.2%; -6.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.2 MiB | 141.9 MiB (+28.7%) | 118.1 MiB (-16.8%; +7.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 134.1 MiB | 174.5 MiB (+30.2%) | 126.4 MiB (-27.6%; -5.7%) |
| Testify Direct | `none` | 4 | 80.0 MiB | 101.2 MiB (+26.4%) | 74.8 MiB (-26.1%; -6.5%) |
| Testify Direct | `none` | 32 | 133.1 MiB | 146.7 MiB (+10.3%) | 98.1 MiB (-33.2%; -26.3%) |
| Testify Direct | `-race` | 4 | 63.1 MiB | 82.3 MiB (+30.6%) | 70.5 MiB (-14.4%; +11.8%) |
| Testify Direct | `-race` | 32 | 64.4 MiB | 84.2 MiB (+30.8%) | 79.5 MiB (-5.6%; +23.5%) |
| Testify Direct | `-cover` | 4 | 67.5 MiB | 82.8 MiB (+22.7%) | 76.3 MiB (-7.8%; +13.1%) |
| Testify Direct | `-cover` | 32 | 93.8 MiB | 101.3 MiB (+8.1%) | 101.0 MiB (-0.3%; +7.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 67.5 MiB | 82.2 MiB (+21.7%) | 76.9 MiB (-6.5%; +13.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 85.8 MiB | 102.2 MiB (+19.1%) | 98.9 MiB (-3.2%; +15.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 55.9 MiB | 70.2 MiB (+25.7%) | 67.3 MiB (-4.2%; +20.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 93.2 MiB | 123.9 MiB (+32.9%) | 80.1 MiB (-35.4%; -14.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 77.2 MiB | 110.4 MiB (+43.0%) | 77.3 MiB (-29.9%; +0.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 123.9 MiB | 128.1 MiB (+3.3%) | 98.7 MiB (-23.0%; -20.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 73.7 MiB | 91.5 MiB (+24.2%) | 69.8 MiB (-23.8%; -5.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 88.8 MiB | 124.8 MiB (+40.5%) | 81.3 MiB (-34.8%; -8.4%) |
| Testify External | `none` | 4 | 57.1 MiB | 87.7 MiB (+53.6%) | 53.7 MiB (-38.7%; -5.9%) |
| Testify External | `none` | 32 | 87.9 MiB | 144.4 MiB (+64.2%) | 72.7 MiB (-49.7%; -17.4%) |
| Testify External | `-race` | 4 | 56.2 MiB | 90.7 MiB (+61.3%) | 57.0 MiB (-37.2%; +1.3%) |
| Testify External | `-race` | 32 | 85.1 MiB | 129.7 MiB (+52.4%) | 77.1 MiB (-40.6%; -9.4%) |
| Testify External | `-cover` | 4 | 57.0 MiB | 97.6 MiB (+71.3%) | 54.7 MiB (-44.0%; -4.0%) |
| Testify External | `-cover` | 32 | 90.2 MiB | 115.7 MiB (+28.3%) | 74.0 MiB (-36.0%; -17.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 57.8 MiB | 86.3 MiB (+49.3%) | 60.6 MiB (-29.8%; +4.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 102.4 MiB | 128.9 MiB (+25.8%) | 74.8 MiB (-42.0%; -27.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 53.8 MiB | 84.4 MiB (+56.9%) | 58.5 MiB (-30.7%; +8.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 82.3 MiB | 119.7 MiB (+45.4%) | 77.9 MiB (-34.9%; -5.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 54.9 MiB | 94.0 MiB (+71.3%) | 61.0 MiB (-35.1%; +11.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 97.0 MiB | 137.5 MiB (+41.8%) | 74.6 MiB (-45.7%; -23.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 56.8 MiB | 93.1 MiB (+64.0%) | 58.4 MiB (-37.3%; +2.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 84.5 MiB | 123.5 MiB (+46.1%) | 78.0 MiB (-36.9%; -7.8%) |

## Runtime

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 36.6 MiB | 60.3 MiB (+64.5%) | 39.9 MiB (-33.8%; +8.9%) | 41.5 MiB (-31.2%; +13.2%) |
| Gin | `none` | 32 | 42.2 MiB | 70.7 MiB (+67.6%) | 52.2 MiB (-26.1%; +23.8%) | 52.8 MiB (-25.2%; +25.3%) |
| Chi | `none` | 4 | 45.6 MiB | 79.7 MiB (+74.5%) | 33.2 MiB (-58.4%; -27.3%) | 33.5 MiB (-58.0%; -26.7%) |
| Chi | `none` | 32 | 94.7 MiB | 128.0 MiB (+35.2%) | 60.0 MiB (-53.1%; -36.6%) | 55.7 MiB (-56.5%; -41.2%) |
| Gin | `-race` | 4 | 199.3 MiB | FAIL 5/6 | 184.7 MiB (vs Orchestrion unavailable; -7.3% vs Native) | 187.5 MiB (vs Orchestrion unavailable; -5.9% vs Native) |
| Gin | `-race` | 32 | 262.9 MiB | FAIL 6/6 | 330.1 MiB (vs Orchestrion unavailable; +25.6% vs Native) | 328.7 MiB (vs Orchestrion unavailable; +25.0% vs Native) |
| Gin | `-cover` | 4 | 35.9 MiB | 92.6 MiB (+158.2%) | 39.4 MiB (-57.5%; +9.7%) | 42.6 MiB (-54.0%; +18.7%) |
| Gin | `-cover` | 32 | 42.1 MiB | 165.2 MiB (+292.3%) | 53.4 MiB (-67.6%; +26.9%) | 52.3 MiB (-68.3%; +24.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 35.7 MiB | 98.6 MiB (+176.0%) | 76.0 MiB (-22.9%; +112.8%) | 76.0 MiB (-23.0%; +112.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 52.1 MiB | 200.9 MiB (+285.8%) | 146.3 MiB (-27.2%; +180.9%) | 147.7 MiB (-26.5%; +183.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 243.9 MiB | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 283.8 MiB | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 2958.8 MiB | 2979.4 MiB (+0.7%) | 3000.3 MiB (+0.7%; +1.4%) | 3004.5 MiB (+0.8%; +1.5%) |
| Chi | `-race` | 32 | 3068.8 MiB | 3112.4 MiB (+1.4%) | 3051.2 MiB (-2.0%; -0.6%) | 3055.9 MiB (-1.8%; -0.4%) |
| Chi | `-cover` | 4 | 43.0 MiB | 85.0 MiB (+97.6%) | 32.4 MiB (-61.8%; -24.6%) | 34.1 MiB (-59.9%; -20.8%) |
| Chi | `-cover` | 32 | 88.6 MiB | 149.1 MiB (+68.2%) | 58.6 MiB (-60.7%; -33.9%) | 55.6 MiB (-62.7%; -37.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 45.1 MiB | 98.8 MiB (+119.0%) | 41.7 MiB (-57.8%; -7.6%) | 41.5 MiB (-58.0%; -8.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 84.1 MiB | 145.2 MiB (+72.6%) | 63.6 MiB (-56.2%; -24.4%) | 62.2 MiB (-57.2%; -26.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 2944.4 MiB | 3073.4 MiB (+4.4%) | 2976.5 MiB (-3.2%; +1.1%) | 2958.4 MiB (-3.7%; +0.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 2940.6 MiB | 3072.5 MiB (+4.5%) | 2963.1 MiB (-3.6%; +0.8%) | 2954.4 MiB (-3.8%; +0.5%) |
| Testify Direct | `none` | 4 | 14.5 MiB | 26.6 MiB (+83.0%) | 13.2 MiB (-50.5%; -9.4%) | 12.9 MiB (-51.5%; -11.3%) |
| Testify Direct | `none` | 32 | 15.0 MiB | 29.4 MiB (+96.5%) | 15.3 MiB (-48.0%; +2.2%) | 14.5 MiB (-50.6%; -2.9%) |
| Testify Direct | `-race` | 4 | 34.1 MiB | 67.5 MiB (+98.0%) | 32.2 MiB (-52.3%; -5.6%) | 31.5 MiB (-53.4%; -7.7%) |
| Testify Direct | `-race` | 32 | 37.2 MiB | 73.4 MiB (+97.2%) | 35.0 MiB (-52.3%; -6.0%) | 33.6 MiB (-54.2%; -9.7%) |
| Testify Direct | `-cover` | 4 | 14.5 MiB | 29.7 MiB (+105.0%) | 13.5 MiB (-54.6%; -6.8%) | 13.4 MiB (-54.9%; -7.5%) |
| Testify Direct | `-cover` | 32 | 14.7 MiB | 33.8 MiB (+129.5%) | 14.7 MiB (-56.6%; -0.4%) | 14.5 MiB (-57.0%; -1.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 27.6 MiB (+92.9%) | 15.8 MiB (-43.0%; +10.0%) | 15.4 MiB (-44.2%; +7.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 14.8 MiB | 34.0 MiB (+130.6%) | 18.5 MiB (-45.7%; +25.2%) | 17.7 MiB (-48.1%; +19.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 34.0 MiB | 66.7 MiB (+96.4%) | 33.5 MiB (-49.8%; -1.4%) | 32.4 MiB (-51.4%; -4.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.8 MiB | 72.1 MiB (+101.7%) | 38.7 MiB (-46.3%; +8.3%) | 36.6 MiB (-49.2%; +2.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.8 MiB | 27.7 MiB (+87.3%) | 15.9 MiB (-42.6%; +7.6%) | 16.0 MiB (-42.2%; +8.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 15.0 MiB | 35.2 MiB (+135.2%) | 19.3 MiB (-45.0%; +29.2%) | 18.9 MiB (-46.3%; +26.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.4 MiB (+110.6%) | 38.3 MiB (-48.5%; +8.4%) | 37.7 MiB (-49.3%; +6.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.7 MiB | 83.5 MiB (+127.4%) | 44.4 MiB (-46.8%; +21.0%) | 42.3 MiB (-49.4%; +15.2%) |
| Testify External | `none` | 4 | 14.3 MiB | 27.1 MiB (+89.1%) | 13.3 MiB (-50.8%; -7.0%) | 13.2 MiB (-51.5%; -8.3%) |
| Testify External | `none` | 32 | 14.5 MiB | 32.3 MiB (+123.1%) | 14.7 MiB (-54.6%; +1.4%) | 15.1 MiB (-53.2%; +4.5%) |
| Testify External | `-race` | 4 | 33.8 MiB | 68.8 MiB (+103.5%) | 32.3 MiB (-53.1%; -4.6%) | 31.3 MiB (-54.6%; -7.6%) |
| Testify External | `-race` | 32 | 37.0 MiB | 75.3 MiB (+103.7%) | 34.5 MiB (-54.2%; -6.7%) | 34.0 MiB (-54.9%; -8.1%) |
| Testify External | `-cover` | 4 | 14.2 MiB | 29.4 MiB (+106.7%) | 13.4 MiB (-54.4%; -5.8%) | 13.3 MiB (-54.9%; -6.9%) |
| Testify External | `-cover` | 32 | 15.0 MiB | 34.4 MiB (+129.0%) | 14.8 MiB (-56.9%; -1.2%) | 14.7 MiB (-57.4%; -2.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 28.2 MiB (+96.5%) | 15.7 MiB (-44.4%; +9.3%) | 15.5 MiB (-45.0%; +8.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 15.0 MiB | 34.4 MiB (+129.0%) | 17.5 MiB (-49.2%; +16.4%) | 17.7 MiB (-48.4%; +18.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 32.8 MiB | 67.1 MiB (+104.6%) | 33.5 MiB (-50.0%; +2.2%) | 33.2 MiB (-50.6%; +1.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.5 MiB | 73.1 MiB (+105.8%) | 39.2 MiB (-46.3%; +10.5%) | 38.4 MiB (-47.5%; +8.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.6 MiB | 28.0 MiB (+91.9%) | 15.8 MiB (-43.7%; +8.0%) | 15.9 MiB (-43.1%; +9.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 16.8 MiB | 34.5 MiB (+105.9%) | 19.5 MiB (-43.6%; +16.1%) | 19.1 MiB (-44.7%; +13.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.3 MiB (+110.7%) | 38.6 MiB (-48.1%; +9.4%) | 37.5 MiB (-49.6%; +6.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.3 MiB | 73.0 MiB (+101.3%) | 44.4 MiB (-39.2%; +22.4%) | 42.2 MiB (-42.2%; +16.4%) |

## Gin: local Agent delivery and telemetry

Three measured runs follow one warmup.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 41.4 MiB | 77.5 MiB (+87.1%) | 42.0 MiB (-45.8%; +1.3%) |
| Gin | `none` | 32 | 41.5 MiB | 78.2 MiB (+88.3%) | 54.0 MiB (-31.0%; +30.0%) |
