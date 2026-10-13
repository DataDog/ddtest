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
| Gin | `none` | 4 | 1674.3 MiB | 2480.6 MiB (+48.2%) | 1739.5 MiB (-29.9%; +3.9%) |
| Gin | `none` | 32 | 1851.3 MiB | 2639.9 MiB (+42.6%) | 1794.3 MiB (-32.0%; -3.1%) |
| Chi | `none` | 4 | 464.5 MiB | 1292.0 MiB (+178.1%) | 521.7 MiB (-59.6%; +12.3%) |
| Chi | `none` | 32 | 699.1 MiB | 2021.4 MiB (+189.2%) | 679.2 MiB (-66.4%; -2.8%) |
| Gin | `-race` | 4 | 1694.1 MiB | 2697.3 MiB (+59.2%) | 1703.9 MiB (-36.8%; +0.6%) |
| Gin | `-race` | 32 | 1738.9 MiB | 2693.4 MiB (+54.9%) | 1721.9 MiB (-36.1%; -1.0%) |
| Gin | `-cover` | 4 | 1685.3 MiB | 2624.1 MiB (+55.7%) | 1752.9 MiB (-33.2%; +4.0%) |
| Gin | `-cover` | 32 | 1850.4 MiB | 2862.9 MiB (+54.7%) | 1812.1 MiB (-36.7%; -2.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1685.4 MiB | 2598.8 MiB (+54.2%) | 1800.6 MiB (-30.7%; +6.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1890.4 MiB | 2713.6 MiB (+43.5%) | 1813.8 MiB (-33.2%; -4.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1756.8 MiB | 2792.1 MiB (+58.9%) | 1763.1 MiB (-36.9%; +0.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1790.8 MiB | 2726.9 MiB (+52.3%) | 1791.1 MiB (-34.3%; +0.0%) |
| Chi | `-race` | 4 | 415.8 MiB | 1390.6 MiB (+234.4%) | 557.6 MiB (-59.9%; +34.1%) |
| Chi | `-race` | 32 | 665.4 MiB | 1674.4 MiB (+151.6%) | 604.8 MiB (-63.9%; -9.1%) |
| Chi | `-cover` | 4 | 466.0 MiB | 1308.2 MiB (+180.8%) | 532.8 MiB (-59.3%; +14.3%) |
| Chi | `-cover` | 32 | 746.3 MiB | 2152.3 MiB (+188.4%) | 695.8 MiB (-67.7%; -6.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 479.6 MiB | 1306.7 MiB (+172.4%) | 530.5 MiB (-59.4%; +10.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 762.6 MiB | 2137.0 MiB (+180.2%) | 690.7 MiB (-67.7%; -9.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 431.9 MiB | 1395.4 MiB (+223.1%) | 542.9 MiB (-61.1%; +25.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 642.9 MiB | 1608.4 MiB (+150.2%) | 599.6 MiB (-62.7%; -6.7%) |
| Testify Direct | `none` | 4 | 446.0 MiB | 1015.8 MiB (+127.8%) | 519.7 MiB (-48.8%; +16.5%) |
| Testify Direct | `none` | 32 | 732.8 MiB | 1319.7 MiB (+80.1%) | 723.1 MiB (-45.2%; -1.3%) |
| Testify Direct | `-race` | 4 | 400.5 MiB | 1029.6 MiB (+157.1%) | 539.8 MiB (-47.6%; +34.8%) |
| Testify Direct | `-race` | 32 | 594.0 MiB | 1182.8 MiB (+99.1%) | 631.6 MiB (-46.6%; +6.3%) |
| Testify Direct | `-cover` | 4 | 409.4 MiB | 992.5 MiB (+142.4%) | 524.0 MiB (-47.2%; +28.0%) |
| Testify Direct | `-cover` | 32 | 697.3 MiB | 1444.5 MiB (+107.2%) | 770.1 MiB (-46.7%; +10.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 404.1 MiB | 992.9 MiB (+145.7%) | 524.5 MiB (-47.2%; +29.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 662.7 MiB | 1405.3 MiB (+112.0%) | 740.9 MiB (-47.3%; +11.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 395.1 MiB | 968.7 MiB (+145.1%) | 544.7 MiB (-43.8%; +37.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 591.0 MiB | 1196.5 MiB (+102.5%) | 622.8 MiB (-47.9%; +5.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 436.9 MiB | 1012.0 MiB (+131.6%) | 530.8 MiB (-47.5%; +21.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 728.2 MiB | 1374.6 MiB (+88.8%) | 712.1 MiB (-48.2%; -2.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 412.4 MiB | 1093.2 MiB (+165.1%) | 547.3 MiB (-49.9%; +32.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 638.6 MiB | 1247.0 MiB (+95.3%) | 638.0 MiB (-48.8%; -0.1%) |
| Testify External | `none` | 4 | 436.4 MiB | 1007.9 MiB (+131.0%) | 518.3 MiB (-48.6%; +18.8%) |
| Testify External | `none` | 32 | 711.6 MiB | 1358.1 MiB (+90.9%) | 745.6 MiB (-45.1%; +4.8%) |
| Testify External | `-race` | 4 | 395.3 MiB | 1080.2 MiB (+173.2%) | 561.6 MiB (-48.0%; +42.0%) |
| Testify External | `-race` | 32 | 627.3 MiB | 1207.5 MiB (+92.5%) | 621.8 MiB (-48.5%; -0.9%) |
| Testify External | `-cover` | 4 | 420.4 MiB | 1014.6 MiB (+141.4%) | 531.8 MiB (-47.6%; +26.5%) |
| Testify External | `-cover` | 32 | 748.0 MiB | 1348.5 MiB (+80.3%) | 735.1 MiB (-45.5%; -1.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 420.3 MiB | 1014.8 MiB (+141.4%) | 529.4 MiB (-47.8%; +26.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 692.0 MiB | 1306.1 MiB (+88.7%) | 718.4 MiB (-45.0%; +3.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 408.0 MiB | 1071.2 MiB (+162.6%) | 559.9 MiB (-47.7%; +37.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 612.2 MiB | 1206.7 MiB (+97.1%) | 630.2 MiB (-47.8%; +2.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 455.9 MiB | 1025.8 MiB (+125.0%) | 527.8 MiB (-48.6%; +15.8%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 694.1 MiB | 1293.6 MiB (+86.4%) | 719.9 MiB (-44.4%; +3.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 414.6 MiB | 1089.6 MiB (+162.8%) | 554.7 MiB (-49.1%; +33.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 617.8 MiB | 1241.4 MiB (+100.9%) | 639.7 MiB (-48.5%; +3.6%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 49.9 MiB | 85.4 MiB (+71.1%) | 62.7 MiB (-26.5%; +25.8%) |
| Gin | `none` | 32 | 67.0 MiB | 117.3 MiB (+75.1%) | 81.8 MiB (-30.3%; +22.1%) |
| Chi | `none` | 4 | 41.8 MiB | 73.6 MiB (+75.9%) | 54.5 MiB (-25.9%; +30.3%) |
| Chi | `none` | 32 | 57.0 MiB | 108.3 MiB (+90.1%) | 70.5 MiB (-34.9%; +23.7%) |
| Gin | `-race` | 4 | 50.3 MiB | 86.4 MiB (+71.6%) | 64.7 MiB (-25.2%; +28.5%) |
| Gin | `-race` | 32 | 68.8 MiB | 119.4 MiB (+73.5%) | 86.8 MiB (-27.3%; +26.1%) |
| Gin | `-cover` | 4 | 66.9 MiB | 99.0 MiB (+47.9%) | 78.5 MiB (-20.7%; +17.4%) |
| Gin | `-cover` | 32 | 87.1 MiB | 148.8 MiB (+70.8%) | 104.8 MiB (-29.6%; +20.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 65.6 MiB | 109.4 MiB (+66.7%) | 79.2 MiB (-27.6%; +20.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 90.0 MiB | 139.0 MiB (+54.4%) | 90.0 MiB (-35.2%; +0.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 68.5 MiB | 100.4 MiB (+46.6%) | 83.7 MiB (-16.7%; +22.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 91.2 MiB | 142.7 MiB (+56.4%) | 97.1 MiB (-31.9%; +6.4%) |
| Chi | `-race` | 4 | 41.9 MiB | 79.0 MiB (+88.5%) | 59.3 MiB (-25.0%; +41.4%) |
| Chi | `-race` | 32 | 60.1 MiB | 109.4 MiB (+82.0%) | 77.1 MiB (-29.5%; +28.2%) |
| Chi | `-cover` | 4 | 42.0 MiB | 77.0 MiB (+83.6%) | 54.1 MiB (-29.7%; +29.0%) |
| Chi | `-cover` | 32 | 62.5 MiB | 118.0 MiB (+88.9%) | 64.9 MiB (-45.0%; +3.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 43.7 MiB | 79.8 MiB (+82.3%) | 53.2 MiB (-33.2%; +21.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 58.7 MiB | 117.1 MiB (+99.5%) | 70.6 MiB (-39.7%; +20.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 43.4 MiB | 72.6 MiB (+67.2%) | 50.2 MiB (-30.8%; +15.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 114.4 MiB (+94.5%) | 76.5 MiB (-33.1%; +30.0%) |
| Testify Direct | `none` | 4 | 41.5 MiB | 72.3 MiB (+74.2%) | 67.3 MiB (-6.8%; +62.3%) |
| Testify Direct | `none` | 32 | 58.0 MiB | 104.4 MiB (+80.0%) | 84.1 MiB (-19.4%; +45.1%) |
| Testify Direct | `-race` | 4 | 41.8 MiB | 73.3 MiB (+75.2%) | 70.8 MiB (-3.3%; +69.4%) |
| Testify Direct | `-race` | 32 | 46.2 MiB | 75.9 MiB (+64.3%) | 99.6 MiB (+31.4%; +115.8%) |
| Testify Direct | `-cover` | 4 | 42.6 MiB | 68.1 MiB (+59.9%) | 63.0 MiB (-7.5%; +48.0%) |
| Testify Direct | `-cover` | 32 | 43.6 MiB | 74.6 MiB (+71.1%) | 91.4 MiB (+22.5%; +109.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 33.8 MiB | 59.4 MiB (+75.7%) | 49.4 MiB (-16.7%; +46.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 43.2 MiB | 73.6 MiB (+70.2%) | 95.4 MiB (+29.6%; +120.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 35.4 MiB | 56.8 MiB (+60.4%) | 66.2 MiB (+16.7%; +87.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 57.5 MiB | 103.5 MiB (+79.9%) | 97.0 MiB (-6.3%; +68.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.5 MiB | 77.2 MiB (+85.9%) | 64.4 MiB (-16.6%; +55.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.2 MiB | 106.0 MiB (+85.3%) | 91.2 MiB (-14.0%; +59.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 41.4 MiB | 78.4 MiB (+89.6%) | 69.8 MiB (-11.1%; +68.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 104.6 MiB (+80.8%) | 99.0 MiB (-5.3%; +71.2%) |
| Testify External | `none` | 4 | 41.8 MiB | 72.4 MiB (+73.1%) | 45.6 MiB (-37.0%; +9.1%) |
| Testify External | `none` | 32 | 54.3 MiB | 102.7 MiB (+89.3%) | 84.8 MiB (-17.5%; +56.2%) |
| Testify External | `-race` | 4 | 42.4 MiB | 71.8 MiB (+69.3%) | 69.6 MiB (-3.0%; +64.3%) |
| Testify External | `-race` | 32 | 58.1 MiB | 107.8 MiB (+85.6%) | 90.3 MiB (-16.2%; +55.6%) |
| Testify External | `-cover` | 4 | 43.4 MiB | 72.4 MiB (+67.1%) | 65.0 MiB (-10.2%; +50.0%) |
| Testify External | `-cover` | 32 | 57.3 MiB | 110.1 MiB (+92.0%) | 94.3 MiB (-14.3%; +64.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 42.0 MiB | 74.4 MiB (+77.0%) | 63.4 MiB (-14.8%; +50.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 58.8 MiB | 94.2 MiB (+60.1%) | 90.8 MiB (-3.7%; +54.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 41.5 MiB | 70.1 MiB (+68.9%) | 72.8 MiB (+3.7%; +75.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 56.6 MiB | 102.4 MiB (+81.0%) | 92.0 MiB (-10.2%; +62.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.7 MiB | 74.4 MiB (+74.4%) | 50.3 MiB (-32.3%; +18.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.9 MiB | 108.6 MiB (+87.4%) | 88.5 MiB (-18.5%; +52.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 42.0 MiB | 77.3 MiB (+84.0%) | 77.5 MiB (+0.2%; +84.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 60.0 MiB | 105.9 MiB (+76.4%) | 102.8 MiB (-2.9%; +71.3%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 340.3 MiB | 816.5 MiB (+139.9%) | 450.4 MiB (-44.8%; +32.3%) |
| Gin | `none` | 32 | 411.5 MiB | 1141.9 MiB (+177.5%) | 500.1 MiB (-56.2%; +21.5%) |
| Chi | `none` | 4 | 137.0 MiB | 377.7 MiB (+175.8%) | 173.9 MiB (-54.0%; +27.0%) |
| Chi | `none` | 32 | 182.8 MiB | 429.9 MiB (+135.2%) | 195.2 MiB (-54.6%; +6.8%) |
| Gin | `-race` | 4 | 429.4 MiB | 928.1 MiB (+116.1%) | 478.7 MiB (-48.4%; +11.5%) |
| Gin | `-race` | 32 | 512.1 MiB | 1479.3 MiB (+188.9%) | 577.1 MiB (-61.0%; +12.7%) |
| Gin | `-cover` | 4 | 351.6 MiB | 787.4 MiB (+123.9%) | 450.5 MiB (-42.8%; +28.1%) |
| Gin | `-cover` | 32 | 405.2 MiB | 1147.3 MiB (+183.2%) | 507.4 MiB (-55.8%; +25.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 352.6 MiB | 797.0 MiB (+126.0%) | 455.9 MiB (-42.8%; +29.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 411.7 MiB | 1158.4 MiB (+181.4%) | 490.4 MiB (-57.7%; +19.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 436.7 MiB | 1000.6 MiB (+129.1%) | 522.0 MiB (-47.8%; +19.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 505.7 MiB | 1390.4 MiB (+174.9%) | 583.1 MiB (-58.1%; +15.3%) |
| Chi | `-race` | 4 | 150.7 MiB | 434.1 MiB (+188.0%) | 186.9 MiB (-56.9%; +24.0%) |
| Chi | `-race` | 32 | 178.9 MiB | 461.2 MiB (+157.8%) | 209.6 MiB (-54.5%; +17.2%) |
| Chi | `-cover` | 4 | 140.1 MiB | 375.4 MiB (+168.0%) | 169.2 MiB (-54.9%; +20.8%) |
| Chi | `-cover` | 32 | 182.2 MiB | 429.8 MiB (+135.9%) | 181.5 MiB (-57.8%; -0.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 141.1 MiB | 376.5 MiB (+166.9%) | 173.8 MiB (-53.8%; +23.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 184.0 MiB | 441.0 MiB (+139.6%) | 196.8 MiB (-55.4%; +6.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 151.1 MiB | 415.6 MiB (+175.0%) | 184.5 MiB (-55.6%; +22.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 184.3 MiB | 456.5 MiB (+147.7%) | 210.3 MiB (-53.9%; +14.1%) |
| Testify Direct | `none` | 4 | 86.3 MiB | 242.6 MiB (+181.2%) | 113.7 MiB (-53.1%; +31.8%) |
| Testify Direct | `none` | 32 | 116.8 MiB | 266.2 MiB (+127.9%) | 117.1 MiB (-56.0%; +0.3%) |
| Testify Direct | `-race` | 4 | 95.1 MiB | 269.0 MiB (+183.0%) | 122.7 MiB (-54.4%; +29.1%) |
| Testify Direct | `-race` | 32 | 97.7 MiB | 265.3 MiB (+171.6%) | 140.6 MiB (-47.0%; +43.9%) |
| Testify Direct | `-cover` | 4 | 84.6 MiB | 229.6 MiB (+171.3%) | 114.2 MiB (-50.3%; +34.9%) |
| Testify Direct | `-cover` | 32 | 93.9 MiB | 235.9 MiB (+151.3%) | 135.2 MiB (-42.7%; +44.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 84.9 MiB | 222.9 MiB (+162.5%) | 109.6 MiB (-50.9%; +29.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 91.3 MiB | 234.6 MiB (+156.9%) | 116.3 MiB (-50.4%; +27.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 89.5 MiB | 253.9 MiB (+183.7%) | 123.8 MiB (-51.2%; +38.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 116.8 MiB | 294.0 MiB (+151.8%) | 145.0 MiB (-50.7%; +24.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.5 MiB | 241.9 MiB (+173.3%) | 117.9 MiB (-51.3%; +33.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 122.2 MiB | 261.9 MiB (+114.3%) | 121.7 MiB (-53.5%; -0.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 95.1 MiB | 263.1 MiB (+176.8%) | 125.8 MiB (-52.2%; +32.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.7 MiB | 294.0 MiB (+147.7%) | 143.4 MiB (-51.2%; +20.9%) |
| Testify External | `none` | 4 | 86.0 MiB | 236.9 MiB (+175.4%) | 107.7 MiB (-54.6%; +25.1%) |
| Testify External | `none` | 32 | 116.0 MiB | 264.0 MiB (+127.6%) | 129.3 MiB (-51.0%; +11.5%) |
| Testify External | `-race` | 4 | 93.8 MiB | 265.0 MiB (+182.5%) | 127.5 MiB (-51.9%; +35.9%) |
| Testify External | `-race` | 32 | 117.4 MiB | 294.0 MiB (+150.4%) | 144.1 MiB (-51.0%; +22.7%) |
| Testify External | `-cover` | 4 | 87.4 MiB | 244.0 MiB (+179.3%) | 114.5 MiB (-53.1%; +31.1%) |
| Testify External | `-cover` | 32 | 119.0 MiB | 265.1 MiB (+122.7%) | 130.6 MiB (-50.7%; +9.7%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 87.4 MiB | 242.2 MiB (+177.1%) | 113.8 MiB (-53.0%; +30.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 115.3 MiB | 258.6 MiB (+124.3%) | 133.8 MiB (-48.3%; +16.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 92.5 MiB | 265.1 MiB (+186.6%) | 127.3 MiB (-52.0%; +37.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 118.8 MiB | 290.6 MiB (+144.5%) | 144.1 MiB (-50.4%; +21.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 88.2 MiB | 241.6 MiB (+173.8%) | 108.5 MiB (-55.1%; +22.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.5 MiB | 266.3 MiB (+124.7%) | 133.8 MiB (-49.8%; +12.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 93.3 MiB | 270.2 MiB (+189.7%) | 125.4 MiB (-53.6%; +34.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 115.7 MiB | 293.2 MiB (+153.4%) | 143.7 MiB (-51.0%; +24.2%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 230.3 MiB | 347.5 MiB (+50.9%) | 241.1 MiB (-30.6%; +4.7%) |
| Gin | `none` | 32 | 282.6 MiB | 366.2 MiB (+29.6%) | 284.9 MiB (-22.2%; +0.8%) |
| Chi | `none` | 4 | 130.8 MiB | 234.5 MiB (+79.3%) | 142.4 MiB (-39.3%; +8.9%) |
| Chi | `none` | 32 | 180.2 MiB | 266.9 MiB (+48.1%) | 165.0 MiB (-38.2%; -8.5%) |
| Gin | `-race` | 4 | 228.7 MiB | 384.3 MiB (+68.0%) | 226.8 MiB (-41.0%; -0.8%) |
| Gin | `-race` | 32 | 243.3 MiB | 403.4 MiB (+65.8%) | 259.6 MiB (-35.6%; +6.7%) |
| Gin | `-cover` | 4 | 244.2 MiB | 375.7 MiB (+53.8%) | 256.1 MiB (-31.8%; +4.8%) |
| Gin | `-cover` | 32 | 317.7 MiB | 466.9 MiB (+47.0%) | 313.6 MiB (-32.8%; -1.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 254.0 MiB | 399.8 MiB (+57.4%) | 268.1 MiB (-33.0%; +5.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 323.7 MiB | 479.7 MiB (+48.2%) | 321.9 MiB (-32.9%; -0.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 240.8 MiB | 394.1 MiB (+63.7%) | 261.0 MiB (-33.8%; +8.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 262.1 MiB | 413.7 MiB (+57.8%) | 273.9 MiB (-33.8%; +4.5%) |
| Chi | `-race` | 4 | 102.7 MiB | 254.8 MiB (+148.0%) | 125.1 MiB (-50.9%; +21.8%) |
| Chi | `-race` | 32 | 129.1 MiB | 292.0 MiB (+126.2%) | 147.0 MiB (-49.7%; +13.9%) |
| Chi | `-cover` | 4 | 137.5 MiB | 234.7 MiB (+70.7%) | 147.9 MiB (-37.0%; +7.6%) |
| Chi | `-cover` | 32 | 174.1 MiB | 271.3 MiB (+55.8%) | 179.5 MiB (-33.9%; +3.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.1 MiB | 235.6 MiB (+68.2%) | 152.8 MiB (-35.1%; +9.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 174.9 MiB | 270.1 MiB (+54.4%) | 177.8 MiB (-34.2%; +1.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.9 MiB | 256.6 MiB (+131.4%) | 124.1 MiB (-51.6%; +11.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 137.9 MiB | 295.3 MiB (+114.2%) | 149.8 MiB (-49.3%; +8.7%) |
| Testify Direct | `none` | 4 | 88.6 MiB | 241.8 MiB (+172.9%) | 118.3 MiB (-51.1%; +33.5%) |
| Testify Direct | `none` | 32 | 133.5 MiB | 262.4 MiB (+96.5%) | 132.6 MiB (-49.5%; -0.7%) |
| Testify Direct | `-race` | 4 | 91.0 MiB | 254.4 MiB (+179.6%) | 128.5 MiB (-49.5%; +41.2%) |
| Testify Direct | `-race` | 32 | 100.3 MiB | 266.4 MiB (+165.5%) | 143.3 MiB (-46.2%; +42.8%) |
| Testify Direct | `-cover` | 4 | 89.2 MiB | 231.8 MiB (+159.7%) | 119.7 MiB (-48.4%; +34.1%) |
| Testify Direct | `-cover` | 32 | 95.2 MiB | 239.4 MiB (+151.5%) | 133.6 MiB (-44.2%; +40.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 81.1 MiB | 220.3 MiB (+171.7%) | 110.8 MiB (-49.7%; +36.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 95.5 MiB | 237.3 MiB (+148.6%) | 119.6 MiB (-49.6%; +25.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 87.9 MiB | 255.9 MiB (+191.2%) | 124.8 MiB (-51.2%; +42.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 121.1 MiB | 293.7 MiB (+142.5%) | 146.0 MiB (-50.3%; +20.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 89.5 MiB | 244.8 MiB (+173.6%) | 121.1 MiB (-50.5%; +35.3%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 132.9 MiB | 260.3 MiB (+95.9%) | 136.8 MiB (-47.4%; +3.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 99.7 MiB | 271.5 MiB (+172.4%) | 128.8 MiB (-52.6%; +29.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 118.1 MiB | 294.6 MiB (+149.4%) | 146.6 MiB (-50.2%; +24.1%) |
| Testify External | `none` | 4 | 87.9 MiB | 241.9 MiB (+175.1%) | 108.0 MiB (-55.4%; +22.8%) |
| Testify External | `none` | 32 | 133.3 MiB | 255.6 MiB (+91.8%) | 137.9 MiB (-46.0%; +3.5%) |
| Testify External | `-race` | 4 | 94.6 MiB | 264.6 MiB (+179.7%) | 127.2 MiB (-51.9%; +34.4%) |
| Testify External | `-race` | 32 | 117.4 MiB | 287.2 MiB (+144.7%) | 143.2 MiB (-50.2%; +22.0%) |
| Testify External | `-cover` | 4 | 89.9 MiB | 247.8 MiB (+175.6%) | 115.8 MiB (-53.2%; +28.8%) |
| Testify External | `-cover` | 32 | 145.0 MiB | 266.8 MiB (+84.0%) | 138.1 MiB (-48.2%; -4.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 88.2 MiB | 242.6 MiB (+175.1%) | 115.9 MiB (-52.2%; +31.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 131.1 MiB | 251.2 MiB (+91.7%) | 129.1 MiB (-48.6%; -1.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 94.4 MiB | 268.0 MiB (+184.0%) | 126.1 MiB (-52.9%; +33.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 117.9 MiB | 286.8 MiB (+143.2%) | 127.7 MiB (-55.5%; +8.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 87.1 MiB | 246.8 MiB (+183.3%) | 110.3 MiB (-55.3%; +26.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 126.7 MiB | 259.0 MiB (+104.4%) | 122.0 MiB (-52.9%; -3.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 92.9 MiB | 263.9 MiB (+184.1%) | 125.2 MiB (-52.5%; +34.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 113.8 MiB | 287.0 MiB (+152.1%) | 148.6 MiB (-48.2%; +30.6%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 229.9 MiB | 257.4 MiB (+12.0%) | 242.7 MiB (-5.7%; +5.6%) |
| Gin | `none` | 32 | 288.6 MiB | 334.6 MiB (+16.0%) | 284.3 MiB (-15.0%; -1.5%) |
| Chi | `none` | 4 | 128.9 MiB | 158.0 MiB (+22.6%) | 144.1 MiB (-8.8%; +11.8%) |
| Chi | `none` | 32 | 178.0 MiB | 220.6 MiB (+23.9%) | 179.7 MiB (-18.5%; +0.9%) |
| Gin | `-race` | 4 | 232.3 MiB | 253.0 MiB (+8.9%) | 234.2 MiB (-7.4%; +0.8%) |
| Gin | `-race` | 32 | 244.6 MiB | 288.1 MiB (+17.8%) | 247.9 MiB (-14.0%; +1.3%) |
| Gin | `-cover` | 4 | 245.2 MiB | 384.0 MiB (+56.6%) | 259.2 MiB (-32.5%; +5.7%) |
| Gin | `-cover` | 32 | 320.4 MiB | 455.4 MiB (+42.1%) | 308.8 MiB (-32.2%; -3.6%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 259.5 MiB | 401.5 MiB (+54.7%) | 264.7 MiB (-34.1%; +2.0%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 312.0 MiB | 492.1 MiB (+57.7%) | 317.7 MiB (-35.4%; +1.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 247.5 MiB | 383.5 MiB (+55.0%) | 258.3 MiB (-32.6%; +4.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 264.1 MiB | 435.3 MiB (+64.8%) | 281.6 MiB (-35.3%; +6.6%) |
| Chi | `-race` | 4 | 104.0 MiB | 129.6 MiB (+24.7%) | 120.1 MiB (-7.4%; +15.5%) |
| Chi | `-race` | 32 | 126.6 MiB | 155.1 MiB (+22.5%) | 144.8 MiB (-6.6%; +14.4%) |
| Chi | `-cover` | 4 | 135.4 MiB | 165.6 MiB (+22.3%) | 150.2 MiB (-9.3%; +10.9%) |
| Chi | `-cover` | 32 | 176.3 MiB | 213.5 MiB (+21.1%) | 178.8 MiB (-16.3%; +1.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 140.5 MiB | 168.0 MiB (+19.6%) | 152.8 MiB (-9.0%; +8.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 172.0 MiB | 208.7 MiB (+21.3%) | 182.8 MiB (-12.4%; +6.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 110.2 MiB | 141.9 MiB (+28.7%) | 123.5 MiB (-12.9%; +12.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 134.1 MiB | 174.5 MiB (+30.2%) | 151.5 MiB (-13.2%; +13.0%) |
| Testify Direct | `none` | 4 | 80.0 MiB | 101.2 MiB (+26.4%) | 90.0 MiB (-11.1%; +12.4%) |
| Testify Direct | `none` | 32 | 133.1 MiB | 146.7 MiB (+10.3%) | 98.0 MiB (-33.2%; -26.3%) |
| Testify Direct | `-race` | 4 | 63.1 MiB | 82.3 MiB (+30.6%) | 81.1 MiB (-1.5%; +28.6%) |
| Testify Direct | `-race` | 32 | 64.4 MiB | 84.2 MiB (+30.8%) | 109.3 MiB (+29.8%; +69.8%) |
| Testify Direct | `-cover` | 4 | 67.5 MiB | 82.8 MiB (+22.7%) | 92.1 MiB (+11.1%; +36.4%) |
| Testify Direct | `-cover` | 32 | 93.8 MiB | 101.3 MiB (+8.1%) | 125.3 MiB (+23.7%; +33.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 67.5 MiB | 82.2 MiB (+21.7%) | 80.8 MiB (-1.7%; +19.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 85.8 MiB | 102.2 MiB (+19.1%) | 113.2 MiB (+10.8%; +31.9%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 55.9 MiB | 70.2 MiB (+25.7%) | 80.6 MiB (+14.7%; +44.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 93.2 MiB | 123.9 MiB (+32.9%) | 106.7 MiB (-13.9%; +14.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 77.2 MiB | 110.4 MiB (+43.0%) | 92.7 MiB (-16.0%; +20.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 123.9 MiB | 128.1 MiB (+3.3%) | 132.8 MiB (+3.7%; +7.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 73.7 MiB | 91.5 MiB (+24.2%) | 83.8 MiB (-8.4%; +13.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 88.8 MiB | 124.8 MiB (+40.5%) | 105.8 MiB (-15.2%; +19.2%) |
| Testify External | `none` | 4 | 57.1 MiB | 87.7 MiB (+53.6%) | 59.8 MiB (-31.8%; +4.7%) |
| Testify External | `none` | 32 | 87.9 MiB | 144.4 MiB (+64.2%) | 113.1 MiB (-21.7%; +28.6%) |
| Testify External | `-race` | 4 | 56.2 MiB | 90.7 MiB (+61.3%) | 77.6 MiB (-14.5%; +37.9%) |
| Testify External | `-race` | 32 | 85.1 MiB | 129.7 MiB (+52.4%) | 127.7 MiB (-1.6%; +50.0%) |
| Testify External | `-cover` | 4 | 57.0 MiB | 97.6 MiB (+71.3%) | 84.8 MiB (-13.1%; +48.8%) |
| Testify External | `-cover` | 32 | 90.2 MiB | 115.7 MiB (+28.3%) | 91.4 MiB (-21.0%; +1.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 57.8 MiB | 86.3 MiB (+49.3%) | 77.7 MiB (-9.9%; +34.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 102.4 MiB | 128.9 MiB (+25.8%) | 93.9 MiB (-27.1%; -8.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 53.8 MiB | 84.4 MiB (+56.9%) | 83.7 MiB (-0.9%; +55.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 82.3 MiB | 119.7 MiB (+45.4%) | 85.4 MiB (-28.7%; +3.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 54.9 MiB | 94.0 MiB (+71.3%) | 69.8 MiB (-25.8%; +27.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 97.0 MiB | 137.5 MiB (+41.8%) | 92.8 MiB (-32.5%; -4.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 56.8 MiB | 93.1 MiB (+64.0%) | 80.7 MiB (-13.3%; +42.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 84.5 MiB | 123.5 MiB (+46.1%) | 91.7 MiB (-25.7%; +8.5%) |

## Runtime

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 36.6 MiB | 60.3 MiB (+64.5%) | 50.9 MiB (-15.6%; +38.9%) | 50.2 MiB (-16.7%; +37.1%) |
| Gin | `none` | 32 | 42.2 MiB | 70.7 MiB (+67.6%) | 59.1 MiB (-16.4%; +40.0%) | 60.4 MiB (-14.5%; +43.2%) |
| Chi | `none` | 4 | 45.6 MiB | 79.7 MiB (+74.5%) | 35.0 MiB (-56.0%; -23.3%) | 33.3 MiB (-58.1%; -27.0%) |
| Chi | `none` | 32 | 94.7 MiB | 128.0 MiB (+35.2%) | 55.9 MiB (-56.3%; -41.0%) | 56.3 MiB (-56.0%; -40.5%) |
| Gin | `-race` | 4 | 199.3 MiB | FAIL 5/6 | 185.5 MiB (vs Orchestrion unavailable; -6.9% vs Native) | 191.7 MiB (vs Orchestrion unavailable; -3.8% vs Native) |
| Gin | `-race` | 32 | 262.9 MiB | FAIL 6/6 | 333.5 MiB (vs Orchestrion unavailable; +26.9% vs Native) | 333.3 MiB (vs Orchestrion unavailable; +26.8% vs Native) |
| Gin | `-cover` | 4 | 35.9 MiB | 92.6 MiB (+158.2%) | 40.9 MiB (-55.8%; +14.0%) | 41.3 MiB (-55.4%; +15.2%) |
| Gin | `-cover` | 32 | 42.1 MiB | 165.2 MiB (+292.3%) | 56.7 MiB (-65.7%; +34.6%) | 54.8 MiB (-66.8%; +30.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 35.7 MiB | 98.6 MiB (+176.0%) | 77.7 MiB (-21.2%; +117.4%) | 73.7 MiB (-25.3%; +106.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 52.1 MiB | 200.9 MiB (+285.8%) | 150.0 MiB (-25.3%; +188.1%) | 149.9 MiB (-25.4%; +187.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 243.9 MiB | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 283.8 MiB | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 2958.8 MiB | 2979.4 MiB (+0.7%) | 3006.0 MiB (+0.9%; +1.6%) | 3022.8 MiB (+1.5%; +2.2%) |
| Chi | `-race` | 32 | 3068.8 MiB | 3112.4 MiB (+1.4%) | 3085.5 MiB (-0.9%; +0.5%) | 3081.9 MiB (-1.0%; +0.4%) |
| Chi | `-cover` | 4 | 43.0 MiB | 85.0 MiB (+97.6%) | 48.0 MiB (-43.5%; +11.7%) | 48.8 MiB (-42.6%; +13.5%) |
| Chi | `-cover` | 32 | 88.6 MiB | 149.1 MiB (+68.2%) | 96.5 MiB (-35.3%; +8.9%) | 88.3 MiB (-40.8%; -0.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 45.1 MiB | 98.8 MiB (+119.0%) | 60.8 MiB (-38.4%; +34.8%) | 58.7 MiB (-40.6%; +30.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 84.1 MiB | 145.2 MiB (+72.6%) | 98.9 MiB (-31.9%; +17.6%) | 105.2 MiB (-27.6%; +25.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 2944.4 MiB | 3073.4 MiB (+4.4%) | 2471.7 MiB (-19.6%; -16.1%) | 2472.0 MiB (-19.6%; -16.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 2940.6 MiB | 3072.5 MiB (+4.5%) | 2478.7 MiB (-19.3%; -15.7%) | 2478.0 MiB (-19.3%; -15.7%) |
| Testify Direct | `none` | 4 | 14.5 MiB | 26.6 MiB (+83.0%) | 17.3 MiB (-35.1%; +18.8%) | 15.7 MiB (-41.2%; +7.7%) |
| Testify Direct | `none` | 32 | 15.0 MiB | 29.4 MiB (+96.5%) | 18.3 MiB (-37.7%; +22.5%) | 18.3 MiB (-37.7%; +22.3%) |
| Testify Direct | `-race` | 4 | 34.1 MiB | 67.5 MiB (+98.0%) | 42.9 MiB (-36.4%; +25.9%) | 40.4 MiB (-40.1%; +18.6%) |
| Testify Direct | `-race` | 32 | 37.2 MiB | 73.4 MiB (+97.2%) | 45.0 MiB (-38.7%; +20.9%) | 44.6 MiB (-39.3%; +19.8%) |
| Testify Direct | `-cover` | 4 | 14.5 MiB | 29.7 MiB (+105.0%) | 17.5 MiB (-41.2%; +20.5%) | 17.2 MiB (-42.0%; +18.8%) |
| Testify Direct | `-cover` | 32 | 14.7 MiB | 33.8 MiB (+129.5%) | 19.0 MiB (-43.9%; +28.8%) | 18.1 MiB (-46.5%; +22.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 27.6 MiB (+92.9%) | 22.2 MiB (-19.5%; +55.2%) | 22.0 MiB (-20.3%; +53.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 14.8 MiB | 34.0 MiB (+130.6%) | 25.0 MiB (-26.5%; +69.5%) | 25.0 MiB (-26.6%; +69.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 34.0 MiB | 66.7 MiB (+96.4%) | 43.2 MiB (-35.3%; +27.0%) | 42.6 MiB (-36.1%; +25.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.8 MiB | 72.1 MiB (+101.7%) | 49.3 MiB (-31.6%; +37.8%) | 49.0 MiB (-32.1%; +37.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.8 MiB | 27.7 MiB (+87.3%) | 22.8 MiB (-18.0%; +53.7%) | 22.3 MiB (-19.6%; +50.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 15.0 MiB | 35.2 MiB (+135.2%) | 25.4 MiB (-27.7%; +70.0%) | 25.1 MiB (-28.7%; +67.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.4 MiB (+110.6%) | 46.1 MiB (-38.0%; +30.6%) | 44.8 MiB (-39.7%; +26.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.7 MiB | 83.5 MiB (+127.4%) | 53.1 MiB (-36.5%; +44.5%) | 52.1 MiB (-37.6%; +41.9%) |
| Testify External | `none` | 4 | 14.3 MiB | 27.1 MiB (+89.1%) | 17.3 MiB (-36.1%; +20.8%) | 17.1 MiB (-37.1%; +18.9%) |
| Testify External | `none` | 32 | 14.5 MiB | 32.3 MiB (+123.1%) | 19.0 MiB (-41.1%; +31.5%) | 18.6 MiB (-42.4%; +28.6%) |
| Testify External | `-race` | 4 | 33.8 MiB | 68.8 MiB (+103.5%) | 43.2 MiB (-37.2%; +27.8%) | 42.3 MiB (-38.6%; +24.9%) |
| Testify External | `-race` | 32 | 37.0 MiB | 75.3 MiB (+103.7%) | 45.4 MiB (-39.7%; +22.9%) | 42.8 MiB (-43.2%; +15.8%) |
| Testify External | `-cover` | 4 | 14.2 MiB | 29.4 MiB (+106.7%) | 17.2 MiB (-41.4%; +21.2%) | 16.7 MiB (-43.2%; +17.3%) |
| Testify External | `-cover` | 32 | 15.0 MiB | 34.4 MiB (+129.0%) | 18.6 MiB (-45.9%; +23.9%) | 18.5 MiB (-46.3%; +23.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 14.3 MiB | 28.2 MiB (+96.5%) | 22.2 MiB (-21.3%; +54.7%) | 22.2 MiB (-21.0%; +55.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 15.0 MiB | 34.4 MiB (+129.0%) | 25.5 MiB (-25.8%; +69.9%) | 25.4 MiB (-26.2%; +69.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 32.8 MiB | 67.1 MiB (+104.6%) | 42.9 MiB (-36.1%; +30.7%) | 42.5 MiB (-36.7%; +29.6%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 35.5 MiB | 73.1 MiB (+105.8%) | 50.1 MiB (-31.4%; +41.2%) | 48.6 MiB (-33.5%; +36.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 14.6 MiB | 28.0 MiB (+91.9%) | 22.4 MiB (-19.9%; +53.7%) | 21.9 MiB (-21.9%; +49.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 16.8 MiB | 34.5 MiB (+105.9%) | 25.4 MiB (-26.5%; +51.4%) | 24.5 MiB (-28.9%; +46.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 35.3 MiB | 74.3 MiB (+110.7%) | 45.6 MiB (-38.7%; +29.1%) | 45.4 MiB (-39.0%; +28.6%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 36.3 MiB | 73.0 MiB (+101.3%) | 54.8 MiB (-24.9%; +51.2%) | 51.2 MiB (-29.9%; +41.2%) |

## Gin: local Agent delivery and telemetry

Three measured runs follow one warmup.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 41.4 MiB | 77.5 MiB (+87.1%) | 66.4 MiB (-14.3%; +60.3%) |
| Gin | `none` | 32 | 41.5 MiB | 78.2 MiB (+88.3%) | 84.1 MiB (+7.4%; +102.3%) |
