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
| Gin | `none` | 4 | 1674.3 MiB | 2480.6 MiB (+48.2%) | 1759.7 MiB (-29.1%; +5.1%) |
| Gin | `none` | 32 | 1851.3 MiB | 2639.9 MiB (+42.6%) | 1858.7 MiB (-29.6%; +0.4%) |
| Chi | `none` | 4 | 464.5 MiB | 1292.0 MiB (+178.1%) | 533.3 MiB (-58.7%; +14.8%) |
| Chi | `none` | 32 | 699.1 MiB | 2021.4 MiB (+189.2%) | 755.6 MiB (-62.6%; +8.1%) |
| Gin | `-race` | 4 | 1694.1 MiB | 2697.3 MiB (+59.2%) | 1747.5 MiB (-35.2%; +3.2%) |
| Gin | `-race` | 32 | 1738.9 MiB | 2693.4 MiB (+54.9%) | 1837.4 MiB (-31.8%; +5.7%) |
| Gin | `-cover` | 4 | 1685.3 MiB | 2624.1 MiB (+55.7%) | 1851.6 MiB (-29.4%; +9.9%) |
| Gin | `-cover` | 32 | 1850.4 MiB | 2862.9 MiB (+54.7%) | 1906.9 MiB (-33.4%; +3.0%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1685.4 MiB | 2598.8 MiB (+54.2%) | 1785.0 MiB (-31.3%; +5.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1890.4 MiB | 2713.6 MiB (+43.5%) | 1926.4 MiB (-29.0%; +1.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1756.8 MiB | 2792.1 MiB (+58.9%) | 1810.0 MiB (-35.2%; +3.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1790.8 MiB | 2726.9 MiB (+52.3%) | 1868.6 MiB (-31.5%; +4.3%) |
| Chi | `-race` | 4 | 415.8 MiB | 1390.6 MiB (+234.4%) | 572.1 MiB (-58.9%; +37.6%) |
| Chi | `-race` | 32 | 665.4 MiB | 1674.4 MiB (+151.6%) | 691.7 MiB (-58.7%; +4.0%) |
| Chi | `-cover` | 4 | 466.0 MiB | 1308.2 MiB (+180.8%) | 537.1 MiB (-58.9%; +15.3%) |
| Chi | `-cover` | 32 | 746.3 MiB | 2152.3 MiB (+188.4%) | 801.7 MiB (-62.8%; +7.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 479.6 MiB | 1306.7 MiB (+172.4%) | 532.6 MiB (-59.2%; +11.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 762.6 MiB | 2137.0 MiB (+180.2%) | 814.5 MiB (-61.9%; +6.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 431.9 MiB | 1395.4 MiB (+223.1%) | 583.6 MiB (-58.2%; +35.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 642.9 MiB | 1608.4 MiB (+150.2%) | 688.3 MiB (-57.2%; +7.1%) |
| Testify Direct | `none` | 4 | 446.0 MiB | 1015.8 MiB (+127.8%) | 524.4 MiB (-48.4%; +17.6%) |
| Testify Direct | `none` | 32 | 732.8 MiB | 1319.7 MiB (+80.1%) | 803.0 MiB (-39.2%; +9.6%) |
| Testify Direct | `-race` | 4 | 400.5 MiB | 1029.6 MiB (+157.1%) | 538.1 MiB (-47.7%; +34.3%) |
| Testify Direct | `-race` | 32 | 594.0 MiB | 1182.8 MiB (+99.1%) | 723.1 MiB (-38.9%; +21.7%) |
| Testify Direct | `-cover` | 4 | 409.4 MiB | 992.5 MiB (+142.4%) | 535.2 MiB (-46.1%; +30.7%) |
| Testify Direct | `-cover` | 32 | 697.3 MiB | 1444.5 MiB (+107.2%) | 745.5 MiB (-48.4%; +6.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 404.1 MiB | 992.9 MiB (+145.7%) | 526.5 MiB (-47.0%; +30.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 662.7 MiB | 1405.3 MiB (+112.0%) | 730.8 MiB (-48.0%; +10.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 395.1 MiB | 968.7 MiB (+145.1%) | 581.6 MiB (-40.0%; +47.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 591.0 MiB | 1196.5 MiB (+102.5%) | 633.1 MiB (-47.1%; +7.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 436.9 MiB | 1012.0 MiB (+131.6%) | 532.1 MiB (-47.4%; +21.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 728.2 MiB | 1374.6 MiB (+88.8%) | 709.0 MiB (-48.4%; -2.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 412.4 MiB | 1093.2 MiB (+165.1%) | 565.2 MiB (-48.3%; +37.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 638.6 MiB | 1247.0 MiB (+95.3%) | 627.7 MiB (-49.7%; -1.7%) |
| Testify External | `none` | 4 | 436.4 MiB | 1007.9 MiB (+131.0%) | 524.7 MiB (-47.9%; +20.2%) |
| Testify External | `none` | 32 | 711.6 MiB | 1358.1 MiB (+90.9%) | 769.6 MiB (-43.3%; +8.2%) |
| Testify External | `-race` | 4 | 395.3 MiB | 1080.2 MiB (+173.2%) | 564.2 MiB (-47.8%; +42.7%) |
| Testify External | `-race` | 32 | 627.3 MiB | 1207.5 MiB (+92.5%) | 645.8 MiB (-46.5%; +2.9%) |
| Testify External | `-cover` | 4 | 420.4 MiB | 1014.6 MiB (+141.4%) | 532.2 MiB (-47.5%; +26.6%) |
| Testify External | `-cover` | 32 | 748.0 MiB | 1348.5 MiB (+80.3%) | 810.4 MiB (-39.9%; +8.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 420.3 MiB | 1014.8 MiB (+141.4%) | 532.7 MiB (-47.5%; +26.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 692.0 MiB | 1306.1 MiB (+88.7%) | 798.2 MiB (-38.9%; +15.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 408.0 MiB | 1071.2 MiB (+162.6%) | 580.4 MiB (-45.8%; +42.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 612.2 MiB | 1206.7 MiB (+97.1%) | 727.9 MiB (-39.7%; +18.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 455.9 MiB | 1025.8 MiB (+125.0%) | 538.2 MiB (-47.5%; +18.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 694.1 MiB | 1293.6 MiB (+86.4%) | 804.8 MiB (-37.8%; +16.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 414.6 MiB | 1089.6 MiB (+162.8%) | 590.8 MiB (-45.8%; +42.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 617.8 MiB | 1241.4 MiB (+100.9%) | 724.2 MiB (-41.7%; +17.2%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 49.9 MiB | 85.4 MiB (+71.1%) | 60.8 MiB (-28.8%; +21.9%) |
| Gin | `none` | 32 | 67.0 MiB | 117.3 MiB (+75.1%) | 81.3 MiB (-30.7%; +21.4%) |
| Chi | `none` | 4 | 41.8 MiB | 73.6 MiB (+75.9%) | 52.8 MiB (-28.2%; +26.2%) |
| Chi | `none` | 32 | 57.0 MiB | 108.3 MiB (+90.1%) | 73.1 MiB (-32.5%; +28.3%) |
| Gin | `-race` | 4 | 50.3 MiB | 86.4 MiB (+71.6%) | 61.3 MiB (-29.1%; +21.7%) |
| Gin | `-race` | 32 | 68.8 MiB | 119.4 MiB (+73.5%) | 83.8 MiB (-29.8%; +21.7%) |
| Gin | `-cover` | 4 | 66.9 MiB | 99.0 MiB (+47.9%) | 77.9 MiB (-21.3%; +16.4%) |
| Gin | `-cover` | 32 | 87.1 MiB | 148.8 MiB (+70.8%) | 104.7 MiB (-29.7%; +20.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 65.6 MiB | 109.4 MiB (+66.7%) | 77.0 MiB (-29.6%; +17.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 90.0 MiB | 139.0 MiB (+54.4%) | 105.6 MiB (-24.0%; +17.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 68.5 MiB | 100.4 MiB (+46.6%) | 64.7 MiB (-35.6%; -5.6%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 91.2 MiB | 142.7 MiB (+56.4%) | 105.5 MiB (-26.1%; +15.6%) |
| Chi | `-race` | 4 | 41.9 MiB | 79.0 MiB (+88.5%) | 55.0 MiB (-30.4%; +31.2%) |
| Chi | `-race` | 32 | 60.1 MiB | 109.4 MiB (+82.0%) | 73.0 MiB (-33.3%; +21.5%) |
| Chi | `-cover` | 4 | 42.0 MiB | 77.0 MiB (+83.6%) | 51.3 MiB (-33.4%; +22.2%) |
| Chi | `-cover` | 32 | 62.5 MiB | 118.0 MiB (+88.9%) | 74.8 MiB (-36.6%; +19.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 43.7 MiB | 79.8 MiB (+82.3%) | 55.1 MiB (-31.0%; +25.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 58.7 MiB | 117.1 MiB (+99.5%) | 73.6 MiB (-37.2%; +25.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 43.4 MiB | 72.6 MiB (+67.2%) | 55.4 MiB (-23.6%; +27.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 114.4 MiB (+94.5%) | 72.6 MiB (-36.6%; +23.3%) |
| Testify Direct | `none` | 4 | 41.5 MiB | 72.3 MiB (+74.2%) | 54.0 MiB (-25.3%; +30.1%) |
| Testify Direct | `none` | 32 | 58.0 MiB | 104.4 MiB (+80.0%) | 75.9 MiB (-27.3%; +30.9%) |
| Testify Direct | `-race` | 4 | 41.8 MiB | 73.3 MiB (+75.2%) | 52.6 MiB (-28.2%; +25.8%) |
| Testify Direct | `-race` | 32 | 46.2 MiB | 75.9 MiB (+64.3%) | 78.6 MiB (+3.6%; +70.3%) |
| Testify Direct | `-cover` | 4 | 42.6 MiB | 68.1 MiB (+59.9%) | 55.4 MiB (-18.6%; +30.3%) |
| Testify Direct | `-cover` | 32 | 43.6 MiB | 74.6 MiB (+71.1%) | 74.1 MiB (-0.7%; +69.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 33.8 MiB | 59.4 MiB (+75.7%) | 55.9 MiB (-5.9%; +65.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 43.2 MiB | 73.6 MiB (+70.2%) | 73.1 MiB (-0.7%; +69.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 35.4 MiB | 56.8 MiB (+60.4%) | 57.0 MiB (+0.5%; +61.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 57.5 MiB | 103.5 MiB (+79.9%) | 74.0 MiB (-28.5%; +28.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.5 MiB | 77.2 MiB (+85.9%) | 54.3 MiB (-29.7%; +30.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.2 MiB | 106.0 MiB (+85.3%) | 72.2 MiB (-31.9%; +26.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.4 MiB | 78.4 MiB (+89.6%) | 56.8 MiB (-27.6%; +37.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 104.6 MiB (+80.8%) | 60.9 MiB (-41.8%; +5.3%) |
| Testify External | `none` | 4 | 41.8 MiB | 72.4 MiB (+73.1%) | 52.7 MiB (-27.2%; +26.0%) |
| Testify External | `none` | 32 | 54.3 MiB | 102.7 MiB (+89.3%) | 69.1 MiB (-32.7%; +27.4%) |
| Testify External | `-race` | 4 | 42.4 MiB | 71.8 MiB (+69.3%) | 53.8 MiB (-25.0%; +27.0%) |
| Testify External | `-race` | 32 | 58.1 MiB | 107.8 MiB (+85.6%) | 75.3 MiB (-30.1%; +29.7%) |
| Testify External | `-cover` | 4 | 43.4 MiB | 72.4 MiB (+67.1%) | 53.8 MiB (-25.8%; +24.0%) |
| Testify External | `-cover` | 32 | 57.3 MiB | 110.1 MiB (+92.0%) | 73.5 MiB (-33.2%; +28.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 42.0 MiB | 74.4 MiB (+77.0%) | 53.6 MiB (-28.0%; +27.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 94.2 MiB (+60.1%) | 73.3 MiB (-22.2%; +24.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 41.5 MiB | 70.1 MiB (+68.9%) | 53.2 MiB (-24.2%; +28.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 56.6 MiB | 102.4 MiB (+81.0%) | 73.0 MiB (-28.7%; +29.1%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.7 MiB | 74.4 MiB (+74.4%) | 54.5 MiB (-26.8%; +27.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 108.6 MiB (+87.4%) | 78.1 MiB (-28.1%; +34.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.0 MiB | 77.3 MiB (+84.0%) | 57.3 MiB (-25.9%; +36.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 60.0 MiB | 105.9 MiB (+76.4%) | 76.3 MiB (-27.9%; +27.1%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 340.3 MiB | 816.5 MiB (+139.9%) | 448.5 MiB (-45.1%; +31.8%) |
| Gin | `none` | 32 | 411.5 MiB | 1141.9 MiB (+177.5%) | 527.4 MiB (-53.8%; +28.2%) |
| Chi | `none` | 4 | 137.0 MiB | 377.7 MiB (+175.8%) | 172.5 MiB (-54.3%; +25.9%) |
| Chi | `none` | 32 | 182.8 MiB | 429.9 MiB (+135.2%) | 202.0 MiB (-53.0%; +10.5%) |
| Gin | `-race` | 4 | 429.4 MiB | 928.1 MiB (+116.1%) | 530.6 MiB (-42.8%; +23.6%) |
| Gin | `-race` | 32 | 512.1 MiB | 1479.3 MiB (+188.9%) | 605.0 MiB (-59.1%; +18.1%) |
| Gin | `-cover` | 4 | 351.6 MiB | 787.4 MiB (+123.9%) | 454.4 MiB (-42.3%; +29.2%) |
| Gin | `-cover` | 32 | 405.2 MiB | 1147.3 MiB (+183.2%) | 540.8 MiB (-52.9%; +33.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 352.6 MiB | 797.0 MiB (+126.0%) | 464.2 MiB (-41.8%; +31.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 411.7 MiB | 1158.4 MiB (+181.4%) | 531.7 MiB (-54.1%; +29.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 436.7 MiB | 1000.6 MiB (+129.1%) | 521.6 MiB (-47.9%; +19.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 505.7 MiB | 1390.4 MiB (+174.9%) | 644.4 MiB (-53.7%; +27.4%) |
| Chi | `-race` | 4 | 150.7 MiB | 434.1 MiB (+188.0%) | 188.1 MiB (-56.7%; +24.8%) |
| Chi | `-race` | 32 | 178.9 MiB | 461.2 MiB (+157.8%) | 219.1 MiB (-52.5%; +22.5%) |
| Chi | `-cover` | 4 | 140.1 MiB | 375.4 MiB (+168.0%) | 175.1 MiB (-53.4%; +25.0%) |
| Chi | `-cover` | 32 | 182.2 MiB | 429.8 MiB (+135.9%) | 210.6 MiB (-51.0%; +15.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 141.1 MiB | 376.5 MiB (+166.9%) | 180.2 MiB (-52.1%; +27.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 184.0 MiB | 441.0 MiB (+139.6%) | 205.4 MiB (-53.4%; +11.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 151.1 MiB | 415.6 MiB (+175.0%) | 194.2 MiB (-53.3%; +28.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 184.3 MiB | 456.5 MiB (+147.7%) | 230.3 MiB (-49.6%; +25.0%) |
| Testify Direct | `none` | 4 | 86.3 MiB | 242.6 MiB (+181.2%) | 111.1 MiB (-54.2%; +28.7%) |
| Testify Direct | `none` | 32 | 116.8 MiB | 266.2 MiB (+127.9%) | 140.1 MiB (-47.4%; +19.9%) |
| Testify Direct | `-race` | 4 | 95.1 MiB | 269.0 MiB (+183.0%) | 122.0 MiB (-54.7%; +28.3%) |
| Testify Direct | `-race` | 32 | 97.7 MiB | 265.3 MiB (+171.6%) | 147.2 MiB (-44.5%; +50.7%) |
| Testify Direct | `-cover` | 4 | 84.6 MiB | 229.6 MiB (+171.3%) | 117.6 MiB (-48.8%; +39.0%) |
| Testify Direct | `-cover` | 32 | 93.9 MiB | 235.9 MiB (+151.3%) | 141.2 MiB (-40.1%; +50.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 84.9 MiB | 222.9 MiB (+162.5%) | 118.9 MiB (-46.7%; +40.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 91.3 MiB | 234.6 MiB (+156.9%) | 142.0 MiB (-39.5%; +55.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 89.5 MiB | 253.9 MiB (+183.7%) | 125.9 MiB (-50.4%; +40.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 116.8 MiB | 294.0 MiB (+151.8%) | 143.9 MiB (-51.1%; +23.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.5 MiB | 241.9 MiB (+173.3%) | 114.6 MiB (-52.6%; +29.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 122.2 MiB | 261.9 MiB (+114.3%) | 131.0 MiB (-50.0%; +7.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 95.1 MiB | 263.1 MiB (+176.8%) | 128.9 MiB (-51.0%; +35.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.7 MiB | 294.0 MiB (+147.7%) | 130.2 MiB (-55.7%; +9.7%) |
| Testify External | `none` | 4 | 86.0 MiB | 236.9 MiB (+175.4%) | 113.0 MiB (-52.3%; +31.3%) |
| Testify External | `none` | 32 | 116.0 MiB | 264.0 MiB (+127.6%) | 121.5 MiB (-54.0%; +4.8%) |
| Testify External | `-race` | 4 | 93.8 MiB | 265.0 MiB (+182.5%) | 126.4 MiB (-52.3%; +34.7%) |
| Testify External | `-race` | 32 | 117.4 MiB | 294.0 MiB (+150.4%) | 148.7 MiB (-49.4%; +26.6%) |
| Testify External | `-cover` | 4 | 87.4 MiB | 244.0 MiB (+179.3%) | 118.8 MiB (-51.3%; +35.9%) |
| Testify External | `-cover` | 32 | 119.0 MiB | 265.1 MiB (+122.7%) | 140.5 MiB (-47.0%; +18.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 87.4 MiB | 242.2 MiB (+177.1%) | 119.0 MiB (-50.9%; +36.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 115.3 MiB | 258.6 MiB (+124.3%) | 137.9 MiB (-46.7%; +19.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 92.5 MiB | 265.1 MiB (+186.6%) | 122.9 MiB (-53.6%; +32.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 118.8 MiB | 290.6 MiB (+144.5%) | 147.7 MiB (-49.2%; +24.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.2 MiB | 241.6 MiB (+173.8%) | 114.9 MiB (-52.4%; +30.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.5 MiB | 266.3 MiB (+124.7%) | 140.7 MiB (-47.2%; +18.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 93.3 MiB | 270.2 MiB (+189.7%) | 123.6 MiB (-54.2%; +32.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 115.7 MiB | 293.2 MiB (+153.4%) | 148.7 MiB (-49.3%; +28.5%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 230.3 MiB | 347.5 MiB (+50.9%) | 240.9 MiB (-30.7%; +4.6%) |
| Gin | `none` | 32 | 282.6 MiB | 366.2 MiB (+29.6%) | 296.7 MiB (-19.0%; +5.0%) |
| Chi | `none` | 4 | 130.8 MiB | 234.5 MiB (+79.3%) | 140.4 MiB (-40.1%; +7.4%) |
| Chi | `none` | 32 | 180.2 MiB | 266.9 MiB (+48.1%) | 190.0 MiB (-28.8%; +5.4%) |
| Gin | `-race` | 4 | 228.7 MiB | 384.3 MiB (+68.0%) | 237.7 MiB (-38.2%; +3.9%) |
| Gin | `-race` | 32 | 243.3 MiB | 403.4 MiB (+65.8%) | 258.7 MiB (-35.9%; +6.4%) |
| Gin | `-cover` | 4 | 244.2 MiB | 375.7 MiB (+53.8%) | 256.1 MiB (-31.8%; +4.9%) |
| Gin | `-cover` | 32 | 317.7 MiB | 466.9 MiB (+47.0%) | 334.3 MiB (-28.4%; +5.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 254.0 MiB | 399.8 MiB (+57.4%) | 274.6 MiB (-31.3%; +8.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 323.7 MiB | 479.7 MiB (+48.2%) | 342.4 MiB (-28.6%; +5.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 240.8 MiB | 394.1 MiB (+63.7%) | 259.9 MiB (-34.1%; +7.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 262.1 MiB | 413.7 MiB (+57.8%) | 279.8 MiB (-32.4%; +6.7%) |
| Chi | `-race` | 4 | 102.7 MiB | 254.8 MiB (+148.0%) | 123.8 MiB (-51.4%; +20.5%) |
| Chi | `-race` | 32 | 129.1 MiB | 292.0 MiB (+126.2%) | 150.6 MiB (-48.4%; +16.6%) |
| Chi | `-cover` | 4 | 137.5 MiB | 234.7 MiB (+70.7%) | 152.6 MiB (-35.0%; +11.0%) |
| Chi | `-cover` | 32 | 174.1 MiB | 271.3 MiB (+55.8%) | 189.8 MiB (-30.1%; +9.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.1 MiB | 235.6 MiB (+68.2%) | 154.7 MiB (-34.4%; +10.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 174.9 MiB | 270.1 MiB (+54.4%) | 190.9 MiB (-29.3%; +9.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.9 MiB | 256.6 MiB (+131.4%) | 127.8 MiB (-50.2%; +15.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 137.9 MiB | 295.3 MiB (+114.2%) | 148.5 MiB (-49.7%; +7.7%) |
| Testify Direct | `none` | 4 | 88.6 MiB | 241.8 MiB (+172.9%) | 117.1 MiB (-51.6%; +32.2%) |
| Testify Direct | `none` | 32 | 133.5 MiB | 262.4 MiB (+96.5%) | 154.1 MiB (-41.3%; +15.4%) |
| Testify Direct | `-race` | 4 | 91.0 MiB | 254.4 MiB (+179.6%) | 123.1 MiB (-51.6%; +35.4%) |
| Testify Direct | `-race` | 32 | 100.3 MiB | 266.4 MiB (+165.5%) | 149.6 MiB (-43.8%; +49.1%) |
| Testify Direct | `-cover` | 4 | 89.2 MiB | 231.8 MiB (+159.7%) | 120.1 MiB (-48.2%; +34.6%) |
| Testify Direct | `-cover` | 32 | 95.2 MiB | 239.4 MiB (+151.5%) | 145.1 MiB (-39.4%; +52.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 81.1 MiB | 220.3 MiB (+171.7%) | 120.2 MiB (-45.4%; +48.2%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 95.5 MiB | 237.3 MiB (+148.6%) | 144.6 MiB (-39.1%; +51.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 87.9 MiB | 255.9 MiB (+191.2%) | 123.7 MiB (-51.6%; +40.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 121.1 MiB | 293.7 MiB (+142.5%) | 146.5 MiB (-50.1%; +20.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 89.5 MiB | 244.8 MiB (+173.6%) | 118.1 MiB (-51.8%; +32.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 132.9 MiB | 260.3 MiB (+95.9%) | 131.2 MiB (-49.6%; -1.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 99.7 MiB | 271.5 MiB (+172.4%) | 125.7 MiB (-53.7%; +26.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.1 MiB | 294.6 MiB (+149.4%) | 135.9 MiB (-53.9%; +15.0%) |
| Testify External | `none` | 4 | 87.9 MiB | 241.9 MiB (+175.1%) | 113.9 MiB (-52.9%; +29.5%) |
| Testify External | `none` | 32 | 133.3 MiB | 255.6 MiB (+91.8%) | 123.2 MiB (-51.8%; -7.6%) |
| Testify External | `-race` | 4 | 94.6 MiB | 264.6 MiB (+179.7%) | 123.1 MiB (-53.5%; +30.2%) |
| Testify External | `-race` | 32 | 117.4 MiB | 287.2 MiB (+144.7%) | 145.9 MiB (-49.2%; +24.3%) |
| Testify External | `-cover` | 4 | 89.9 MiB | 247.8 MiB (+175.6%) | 114.6 MiB (-53.8%; +27.4%) |
| Testify External | `-cover` | 32 | 145.0 MiB | 266.8 MiB (+84.0%) | 158.1 MiB (-40.7%; +9.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 88.2 MiB | 242.6 MiB (+175.1%) | 113.0 MiB (-53.4%; +28.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 131.1 MiB | 251.2 MiB (+91.7%) | 162.0 MiB (-35.5%; +23.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 94.4 MiB | 268.0 MiB (+184.0%) | 122.6 MiB (-54.3%; +29.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 117.9 MiB | 286.8 MiB (+143.2%) | 151.6 MiB (-47.1%; +28.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 87.1 MiB | 246.8 MiB (+183.3%) | 117.9 MiB (-52.2%; +35.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 126.7 MiB | 259.0 MiB (+104.4%) | 165.7 MiB (-36.0%; +30.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 92.9 MiB | 263.9 MiB (+184.1%) | 121.2 MiB (-54.1%; +30.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 113.8 MiB | 287.0 MiB (+152.1%) | 146.6 MiB (-48.9%; +28.8%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 229.9 MiB | 257.4 MiB (+12.0%) | 238.5 MiB (-7.3%; +3.8%) |
| Gin | `none` | 32 | 288.6 MiB | 334.6 MiB (+16.0%) | 295.1 MiB (-11.8%; +2.3%) |
| Chi | `none` | 4 | 128.9 MiB | 158.0 MiB (+22.6%) | 140.4 MiB (-11.1%; +8.9%) |
| Chi | `none` | 32 | 178.0 MiB | 220.6 MiB (+23.9%) | 183.1 MiB (-17.0%; +2.8%) |
| Gin | `-race` | 4 | 232.3 MiB | 253.0 MiB (+8.9%) | 241.8 MiB (-4.4%; +4.1%) |
| Gin | `-race` | 32 | 244.6 MiB | 288.1 MiB (+17.8%) | 269.8 MiB (-6.4%; +10.3%) |
| Gin | `-cover` | 4 | 245.2 MiB | 384.0 MiB (+56.6%) | 257.8 MiB (-32.9%; +5.1%) |
| Gin | `-cover` | 32 | 320.4 MiB | 455.4 MiB (+42.1%) | 334.1 MiB (-26.6%; +4.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 259.5 MiB | 401.5 MiB (+54.7%) | 266.5 MiB (-33.6%; +2.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 312.0 MiB | 492.1 MiB (+57.7%) | 327.9 MiB (-33.4%; +5.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 247.5 MiB | 383.5 MiB (+55.0%) | 264.7 MiB (-31.0%; +7.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 264.1 MiB | 435.3 MiB (+64.8%) | 287.4 MiB (-34.0%; +8.8%) |
| Chi | `-race` | 4 | 104.0 MiB | 129.6 MiB (+24.7%) | 123.0 MiB (-5.1%; +18.4%) |
| Chi | `-race` | 32 | 126.6 MiB | 155.1 MiB (+22.5%) | 136.5 MiB (-12.0%; +7.8%) |
| Chi | `-cover` | 4 | 135.4 MiB | 165.6 MiB (+22.3%) | 150.8 MiB (-8.9%; +11.4%) |
| Chi | `-cover` | 32 | 176.3 MiB | 213.5 MiB (+21.1%) | 192.9 MiB (-9.7%; +9.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.5 MiB | 168.0 MiB (+19.6%) | 150.8 MiB (-10.3%; +7.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 172.0 MiB | 208.7 MiB (+21.3%) | 193.3 MiB (-7.3%; +12.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.2 MiB | 141.9 MiB (+28.7%) | 120.9 MiB (-14.8%; +9.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 134.1 MiB | 174.5 MiB (+30.2%) | 141.2 MiB (-19.1%; +5.4%) |
| Testify Direct | `none` | 4 | 80.0 MiB | 101.2 MiB (+26.4%) | 88.6 MiB (-12.5%; +10.7%) |
| Testify Direct | `none` | 32 | 133.1 MiB | 146.7 MiB (+10.3%) | 142.7 MiB (-2.8%; +7.2%) |
| Testify Direct | `-race` | 4 | 63.1 MiB | 82.3 MiB (+30.6%) | 74.3 MiB (-9.7%; +17.9%) |
| Testify Direct | `-race` | 32 | 64.4 MiB | 84.2 MiB (+30.8%) | 102.1 MiB (+21.2%; +58.6%) |
| Testify Direct | `-cover` | 4 | 67.5 MiB | 82.8 MiB (+22.7%) | 87.8 MiB (+5.9%; +30.0%) |
| Testify Direct | `-cover` | 32 | 93.8 MiB | 101.3 MiB (+8.1%) | 142.9 MiB (+41.0%; +52.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 67.5 MiB | 82.2 MiB (+21.7%) | 95.6 MiB (+16.3%; +41.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 85.8 MiB | 102.2 MiB (+19.1%) | 129.6 MiB (+26.9%; +51.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 55.9 MiB | 70.2 MiB (+25.7%) | 83.2 MiB (+18.5%; +49.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 93.2 MiB | 123.9 MiB (+32.9%) | 99.3 MiB (-19.9%; +6.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 77.2 MiB | 110.4 MiB (+43.0%) | 89.9 MiB (-18.6%; +16.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 123.9 MiB | 128.1 MiB (+3.3%) | 115.2 MiB (-10.0%; -7.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 73.7 MiB | 91.5 MiB (+24.2%) | 78.5 MiB (-14.2%; +6.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 88.8 MiB | 124.8 MiB (+40.5%) | 82.0 MiB (-34.3%; -7.6%) |
| Testify External | `none` | 4 | 57.1 MiB | 87.7 MiB (+53.6%) | 76.5 MiB (-12.8%; +34.0%) |
| Testify External | `none` | 32 | 87.9 MiB | 144.4 MiB (+64.2%) | 79.3 MiB (-45.1%; -9.9%) |
| Testify External | `-race` | 4 | 56.2 MiB | 90.7 MiB (+61.3%) | 71.6 MiB (-21.1%; +27.3%) |
| Testify External | `-race` | 32 | 85.1 MiB | 129.7 MiB (+52.4%) | 105.9 MiB (-18.3%; +24.5%) |
| Testify External | `-cover` | 4 | 57.0 MiB | 97.6 MiB (+71.3%) | 65.3 MiB (-33.1%; +14.6%) |
| Testify External | `-cover` | 32 | 90.2 MiB | 115.7 MiB (+28.3%) | 112.6 MiB (-2.7%; +24.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 57.8 MiB | 86.3 MiB (+49.3%) | 69.0 MiB (-20.0%; +19.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 102.4 MiB | 128.9 MiB (+25.8%) | 107.8 MiB (-16.4%; +5.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 53.8 MiB | 84.4 MiB (+56.9%) | 65.6 MiB (-22.3%; +22.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 82.3 MiB | 119.7 MiB (+45.4%) | 96.0 MiB (-19.8%; +16.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 54.9 MiB | 94.0 MiB (+71.3%) | 67.6 MiB (-28.1%; +23.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 97.0 MiB | 137.5 MiB (+41.8%) | 106.7 MiB (-22.4%; +10.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 56.8 MiB | 93.1 MiB (+64.0%) | 62.1 MiB (-33.3%; +9.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 84.5 MiB | 123.5 MiB (+46.1%) | 97.2 MiB (-21.3%; +15.0%) |

## Runtime

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 36.6 MiB | 60.3 MiB (+64.5%) | 52.3 MiB (-13.3%; +42.7%) | 59.6 MiB (-1.2%; +62.6%) |
| Gin | `none` | 32 | 42.2 MiB | 70.7 MiB (+67.6%) | 80.4 MiB (+13.7%; +90.5%) | 74.2 MiB (+4.9%; +75.9%) |
| Chi | `none` | 4 | 45.6 MiB | 79.7 MiB (+74.5%) | 60.5 MiB (-24.1%; +32.6%) | 60.8 MiB (-23.7%; +33.2%) |
| Chi | `none` | 32 | 94.7 MiB | 128.0 MiB (+35.2%) | 96.0 MiB (-25.0%; +1.3%) | 85.5 MiB (-33.3%; -9.8%) |
| Gin | `-race` | 4 | 199.3 MiB | FAIL 5/6 | 206.8 MiB (vs Orchestrion unavailable; +3.8% vs Native) | 210.9 MiB (vs Orchestrion unavailable; +5.8% vs Native) |
| Gin | `-race` | 32 | 262.9 MiB | FAIL 6/6 | 392.7 MiB (vs Orchestrion unavailable; +49.4% vs Native) | 393.9 MiB (vs Orchestrion unavailable; +49.8% vs Native) |
| Gin | `-cover` | 4 | 35.9 MiB | 92.6 MiB (+158.2%) | 60.2 MiB (-35.0%; +67.9%) | 54.8 MiB (-40.8%; +52.8%) |
| Gin | `-cover` | 32 | 42.1 MiB | 165.2 MiB (+292.3%) | 79.3 MiB (-52.0%; +88.4%) | 83.8 MiB (-49.3%; +98.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 35.7 MiB | 98.6 MiB (+176.0%) | 117.1 MiB (+18.8%; +227.8%) | 122.6 MiB (+24.3%; +243.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 52.1 MiB | 200.9 MiB (+285.8%) | 218.4 MiB (+8.7%; +319.3%) | 224.4 MiB (+11.7%; +330.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 243.9 MiB | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 283.8 MiB | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 2958.8 MiB | 2979.4 MiB (+0.7%) | 3023.4 MiB (+1.5%; +2.2%) | 3019.0 MiB (+1.3%; +2.0%) |
| Chi | `-race` | 32 | 3068.8 MiB | 3112.4 MiB (+1.4%) | 3098.3 MiB (-0.5%; +1.0%) | 3111.2 MiB (-0.0%; +1.4%) |
| Chi | `-cover` | 4 | 43.0 MiB | 85.0 MiB (+97.6%) | 52.6 MiB (-38.1%; +22.2%) | 60.1 MiB (-29.2%; +39.8%) |
| Chi | `-cover` | 32 | 88.6 MiB | 149.1 MiB (+68.2%) | 93.2 MiB (-37.5%; +5.2%) | 87.2 MiB (-41.5%; -1.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 45.1 MiB | 98.8 MiB (+119.0%) | 59.8 MiB (-39.5%; +32.5%) | 63.6 MiB (-35.6%; +40.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 84.1 MiB | 145.2 MiB (+72.6%) | 100.5 MiB (-30.8%; +19.4%) | 103.8 MiB (-28.5%; +23.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 2944.4 MiB | 3073.4 MiB (+4.4%) | 2481.0 MiB (-19.3%; -15.7%) | 2478.9 MiB (-19.3%; -15.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 2940.6 MiB | 3072.5 MiB (+4.5%) | 2503.1 MiB (-18.5%; -14.9%) | 2494.0 MiB (-18.8%; -15.2%) |
| Testify Direct | `none` | 4 | 14.5 MiB | 26.6 MiB (+83.0%) | 17.3 MiB (-35.2%; +18.6%) | 15.4 MiB (-42.0%; +6.2%) |
| Testify Direct | `none` | 32 | 15.0 MiB | 29.4 MiB (+96.5%) | 19.2 MiB (-34.6%; +28.5%) | 18.4 MiB (-37.5%; +22.7%) |
| Testify Direct | `-race` | 4 | 34.1 MiB | 67.5 MiB (+98.0%) | 42.7 MiB (-36.7%; +25.2%) | 42.4 MiB (-37.1%; +24.5%) |
| Testify Direct | `-race` | 32 | 37.2 MiB | 73.4 MiB (+97.2%) | 44.9 MiB (-38.8%; +20.7%) | 44.1 MiB (-39.9%; +18.6%) |
| Testify Direct | `-cover` | 4 | 14.5 MiB | 29.7 MiB (+105.0%) | 17.5 MiB (-41.1%; +20.8%) | 17.4 MiB (-41.4%; +20.2%) |
| Testify Direct | `-cover` | 32 | 14.7 MiB | 33.8 MiB (+129.5%) | 20.4 MiB (-39.6%; +38.7%) | 17.9 MiB (-47.0%; +21.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 27.6 MiB (+92.9%) | 22.2 MiB (-19.5%; +55.2%) | 22.2 MiB (-19.8%; +54.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 14.8 MiB | 34.0 MiB (+130.6%) | 25.3 MiB (-25.6%; +71.5%) | 25.6 MiB (-24.9%; +73.1%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 34.0 MiB | 66.7 MiB (+96.4%) | 45.4 MiB (-31.9%; +33.6%) | 44.6 MiB (-33.1%; +31.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.8 MiB | 72.1 MiB (+101.7%) | 51.6 MiB (-28.4%; +44.4%) | 50.3 MiB (-30.3%; +40.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.8 MiB | 27.7 MiB (+87.3%) | 21.0 MiB (-24.3%; +41.8%) | 22.0 MiB (-20.8%; +48.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 15.0 MiB | 35.2 MiB (+135.2%) | 25.3 MiB (-28.1%; +69.0%) | 25.0 MiB (-29.0%; +66.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.4 MiB (+110.6%) | 49.6 MiB (-33.4%; +40.3%) | 49.8 MiB (-33.1%; +41.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.7 MiB | 83.5 MiB (+127.4%) | 56.7 MiB (-32.2%; +54.3%) | 56.1 MiB (-32.8%; +52.8%) |
| Testify External | `none` | 4 | 14.3 MiB | 27.1 MiB (+89.1%) | 17.0 MiB (-37.3%; +18.6%) | 17.1 MiB (-37.0%; +19.1%) |
| Testify External | `none` | 32 | 14.5 MiB | 32.3 MiB (+123.1%) | 18.9 MiB (-41.5%; +30.5%) | 18.1 MiB (-44.0%; +24.9%) |
| Testify External | `-race` | 4 | 33.8 MiB | 68.8 MiB (+103.5%) | 42.9 MiB (-37.7%; +26.9%) | 41.2 MiB (-40.1%; +21.8%) |
| Testify External | `-race` | 32 | 37.0 MiB | 75.3 MiB (+103.7%) | 45.2 MiB (-39.9%; +22.3%) | 44.5 MiB (-40.9%; +20.5%) |
| Testify External | `-cover` | 4 | 14.2 MiB | 29.4 MiB (+106.7%) | 17.3 MiB (-41.3%; +21.3%) | 17.5 MiB (-40.6%; +22.7%) |
| Testify External | `-cover` | 32 | 15.0 MiB | 34.4 MiB (+129.0%) | 18.8 MiB (-45.3%; +25.3%) | 17.8 MiB (-48.3%; +18.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 28.2 MiB (+96.5%) | 23.9 MiB (-15.1%; +66.9%) | 21.8 MiB (-22.7%; +51.9%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 15.0 MiB | 34.4 MiB (+129.0%) | 25.8 MiB (-25.1%; +71.6%) | 25.0 MiB (-27.2%; +66.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 32.8 MiB | 67.1 MiB (+104.6%) | 45.2 MiB (-32.7%; +37.7%) | 44.6 MiB (-33.6%; +35.9%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.5 MiB | 73.1 MiB (+105.8%) | 51.6 MiB (-29.4%; +45.3%) | 50.8 MiB (-30.4%; +43.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.6 MiB | 28.0 MiB (+91.9%) | 24.2 MiB (-13.7%; +65.6%) | 22.5 MiB (-19.5%; +54.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 16.8 MiB | 34.5 MiB (+105.9%) | 25.6 MiB (-25.8%; +52.8%) | 26.5 MiB (-23.2%; +58.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.3 MiB (+110.7%) | 49.4 MiB (-33.5%; +40.1%) | 49.7 MiB (-33.1%; +40.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.3 MiB | 73.0 MiB (+101.3%) | 58.8 MiB (-19.5%; +62.0%) | 54.4 MiB (-25.5%; +49.9%) |

## Gin: local Agent delivery and telemetry

Three measured runs follow one warmup.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 41.4 MiB | 77.5 MiB (+87.1%) | 64.8 MiB (-16.4%; +56.4%) |
| Gin | `none` | 32 | 41.5 MiB | 78.2 MiB (+88.3%) | 72.9 MiB (-6.8%; +75.5%) |
