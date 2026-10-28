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
| Gin | `none` | 4 | 1643.1 MiB | 2370.4 MiB (+44.3%) | 1763.6 MiB (-25.6%; +7.3%) |
| Gin | `none` | 32 | 1787.8 MiB | 2847.4 MiB (+59.3%) | 1809.0 MiB (-36.5%; +1.2%) |
| Chi | `none` | 4 | 429.9 MiB | 1242.9 MiB (+189.1%) | 529.6 MiB (-57.4%; +23.2%) |
| Chi | `none` | 32 | 630.4 MiB | 1988.0 MiB (+215.3%) | 693.5 MiB (-65.1%; +10.0%) |
| Gin | `-race` | 4 | 1616.8 MiB | 2654.4 MiB (+64.2%) | 1696.5 MiB (-36.1%; +4.9%) |
| Gin | `-race` | 32 | 1636.2 MiB | 2585.0 MiB (+58.0%) | 1731.3 MiB (-33.0%; +5.8%) |
| Gin | `-cover` | 4 | 1656.9 MiB | 2707.5 MiB (+63.4%) | 1754.5 MiB (-35.2%; +5.9%) |
| Gin | `-cover` | 32 | 1782.9 MiB | 2671.6 MiB (+49.8%) | 1785.9 MiB (-33.2%; +0.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 1666.1 MiB | 2408.0 MiB (+44.5%) | 1797.8 MiB (-25.3%; +7.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 1809.9 MiB | 2745.5 MiB (+51.7%) | 1797.5 MiB (-34.5%; -0.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 1692.9 MiB | 2789.9 MiB (+64.8%) | 1764.3 MiB (-36.8%; +4.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 1682.0 MiB | 2695.6 MiB (+60.3%) | 1774.3 MiB (-34.2%; +5.5%) |
| Chi | `-race` | 4 | 401.8 MiB | 1328.4 MiB (+230.6%) | 549.0 MiB (-58.7%; +36.6%) |
| Chi | `-race` | 32 | 562.5 MiB | 1535.0 MiB (+172.9%) | 591.1 MiB (-61.5%; +5.1%) |
| Chi | `-cover` | 4 | 437.2 MiB | 1258.6 MiB (+187.9%) | 538.2 MiB (-57.2%; +23.1%) |
| Chi | `-cover` | 32 | 639.4 MiB | 2012.1 MiB (+214.7%) | 673.8 MiB (-66.5%; +5.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 440.0 MiB | 1257.4 MiB (+185.8%) | 541.0 MiB (-57.0%; +23.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 656.2 MiB | 1957.6 MiB (+198.3%) | 668.4 MiB (-65.9%; +1.9%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 426.8 MiB | 1335.4 MiB (+212.9%) | 553.7 MiB (-58.5%; +29.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 568.0 MiB | 1546.5 MiB (+172.3%) | 606.4 MiB (-60.8%; +6.8%) |
| Testify Direct | `none` | 4 | 425.1 MiB | 994.6 MiB (+134.0%) | 529.0 MiB (-46.8%; +24.4%) |
| Testify Direct | `none` | 32 | 669.6 MiB | 1258.7 MiB (+88.0%) | 703.4 MiB (-44.1%; +5.1%) |
| Testify Direct | `-race` | 4 | 384.7 MiB | 1054.1 MiB (+174.0%) | 543.0 MiB (-48.5%; +41.2%) |
| Testify Direct | `-race` | 32 | 593.5 MiB | 1248.8 MiB (+110.4%) | 619.7 MiB (-50.4%; +4.4%) |
| Testify Direct | `-cover` | 4 | 406.3 MiB | 999.7 MiB (+146.0%) | 535.5 MiB (-46.4%; +31.8%) |
| Testify Direct | `-cover` | 32 | 724.1 MiB | 1386.6 MiB (+91.5%) | 696.3 MiB (-49.8%; -3.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 401.5 MiB | 1002.5 MiB (+149.7%) | 535.1 MiB (-46.6%; +33.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 679.3 MiB | 1398.2 MiB (+105.8%) | 758.6 MiB (-45.7%; +11.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 395.9 MiB | 1061.3 MiB (+168.1%) | 549.3 MiB (-48.2%; +38.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 594.7 MiB | 1246.1 MiB (+109.5%) | 612.0 MiB (-50.9%; +2.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 438.7 MiB | 1004.6 MiB (+129.0%) | 534.8 MiB (-46.8%; +21.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 692.9 MiB | 1271.0 MiB (+83.4%) | 673.5 MiB (-47.0%; -2.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 392.0 MiB | 1072.6 MiB (+173.6%) | 551.7 MiB (-48.6%; +40.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 598.6 MiB | 1252.2 MiB (+109.2%) | 620.0 MiB (-50.5%; +3.6%) |
| Testify External | `none` | 4 | 426.7 MiB | 988.6 MiB (+131.7%) | 529.2 MiB (-46.5%; +24.0%) |
| Testify External | `none` | 32 | 646.9 MiB | 1281.9 MiB (+98.2%) | 712.1 MiB (-44.5%; +10.1%) |
| Testify External | `-race` | 4 | 383.1 MiB | 1055.8 MiB (+175.6%) | 544.4 MiB (-48.4%; +42.1%) |
| Testify External | `-race` | 32 | 582.4 MiB | 1252.9 MiB (+115.1%) | 609.4 MiB (-51.4%; +4.6%) |
| Testify External | `-cover` | 4 | 394.3 MiB | 992.3 MiB (+151.7%) | 532.4 MiB (-46.3%; +35.0%) |
| Testify External | `-cover` | 32 | 680.8 MiB | 1364.5 MiB (+100.4%) | 736.8 MiB (-46.0%; +8.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 393.5 MiB | 997.7 MiB (+153.6%) | 533.6 MiB (-46.5%; +35.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 676.1 MiB | 1351.6 MiB (+99.9%) | 719.0 MiB (-46.8%; +6.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 387.9 MiB | 1056.5 MiB (+172.4%) | 548.3 MiB (-48.1%; +41.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 595.8 MiB | 1237.0 MiB (+107.6%) | 619.6 MiB (-49.9%; +4.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 438.1 MiB | 1000.5 MiB (+128.4%) | 536.1 MiB (-46.4%; +22.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 683.2 MiB | 1346.9 MiB (+97.1%) | 717.7 MiB (-46.7%; +5.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 388.6 MiB | 1070.3 MiB (+175.5%) | 557.3 MiB (-47.9%; +43.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 593.2 MiB | 1249.8 MiB (+110.7%) | 616.9 MiB (-50.6%; +4.0%) |

## Cached compilation — unchanged output reused

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 54.7 MiB | 72.5 MiB (+32.5%) | 64.3 MiB (-11.3%; +17.5%) |
| Gin | `none` | 32 | 57.6 MiB | 86.2 MiB (+49.7%) | 70.5 MiB (-18.2%; +22.4%) |
| Chi | `none` | 4 | 33.8 MiB | 58.7 MiB (+73.5%) | 46.2 MiB (-21.4%; +36.5%) |
| Chi | `none` | 32 | 48.1 MiB | 78.6 MiB (+63.5%) | 61.4 MiB (-21.9%; +27.7%) |
| Gin | `-race` | 4 | 47.8 MiB | 73.3 MiB (+53.3%) | 70.0 MiB (-4.4%; +46.5%) |
| Gin | `-race` | 32 | 54.5 MiB | 85.9 MiB (+57.5%) | 74.0 MiB (-13.9%; +35.7%) |
| Gin | `-cover` | 4 | 56.3 MiB | 77.8 MiB (+38.3%) | 70.1 MiB (-9.9%; +24.6%) |
| Gin | `-cover` | 32 | 69.9 MiB | 100.2 MiB (+43.4%) | 81.0 MiB (-19.2%; +15.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 57.4 MiB | 76.3 MiB (+33.0%) | 67.8 MiB (-11.0%; +18.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 70.0 MiB | 91.3 MiB (+30.4%) | 84.8 MiB (-7.2%; +21.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 60.7 MiB | 76.1 MiB (+25.3%) | 76.3 MiB (+0.3%; +25.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 68.7 MiB | 92.5 MiB (+34.7%) | 85.7 MiB (-7.4%; +24.8%) |
| Chi | `-race` | 4 | 36.2 MiB | 58.4 MiB (+61.1%) | 54.4 MiB (-6.8%; +50.2%) |
| Chi | `-race` | 32 | 46.5 MiB | 76.3 MiB (+64.1%) | 58.9 MiB (-22.8%; +26.7%) |
| Chi | `-cover` | 4 | 36.1 MiB | 61.6 MiB (+70.4%) | 48.3 MiB (-21.5%; +33.7%) |
| Chi | `-cover` | 32 | 45.5 MiB | 76.9 MiB (+68.8%) | 58.3 MiB (-24.2%; +28.0%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 36.5 MiB | 62.1 MiB (+70.3%) | 48.4 MiB (-22.0%; +32.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 46.7 MiB | 82.0 MiB (+75.5%) | 59.7 MiB (-27.2%; +27.8%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 40.3 MiB | 61.4 MiB (+52.3%) | 56.5 MiB (-8.1%; +40.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 45.5 MiB | 79.1 MiB (+74.1%) | 63.5 MiB (-19.8%; +39.6%) |
| Testify Direct | `none` | 4 | 34.4 MiB | 58.3 MiB (+69.4%) | 46.8 MiB (-19.6%; +36.2%) |
| Testify Direct | `none` | 32 | 44.9 MiB | 74.8 MiB (+66.4%) | 57.4 MiB (-23.3%; +27.7%) |
| Testify Direct | `-race` | 4 | 34.6 MiB | 59.2 MiB (+71.2%) | 50.8 MiB (-14.2%; +46.9%) |
| Testify Direct | `-race` | 32 | 43.5 MiB | 76.6 MiB (+75.8%) | 61.6 MiB (-19.6%; +41.4%) |
| Testify Direct | `-cover` | 4 | 33.8 MiB | 57.8 MiB (+71.2%) | 45.9 MiB (-20.6%; +36.0%) |
| Testify Direct | `-cover` | 32 | 46.4 MiB | 77.9 MiB (+67.9%) | 58.8 MiB (-24.5%; +26.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 35.3 MiB | 57.1 MiB (+61.9%) | 47.1 MiB (-17.5%; +33.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 42.1 MiB | 73.7 MiB (+75.0%) | 58.5 MiB (-20.6%; +39.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 35.7 MiB | 58.5 MiB (+64.0%) | 51.6 MiB (-11.8%; +44.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 44.8 MiB | 73.6 MiB (+64.0%) | 60.1 MiB (-18.3%; +34.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 33.4 MiB | 56.6 MiB (+69.5%) | 49.2 MiB (-13.0%; +47.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 45.5 MiB | 75.5 MiB (+65.9%) | 62.4 MiB (-17.3%; +37.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 34.1 MiB | 58.9 MiB (+72.7%) | 53.5 MiB (-9.2%; +56.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 43.4 MiB | 73.3 MiB (+68.7%) | 65.1 MiB (-11.2%; +49.8%) |
| Testify External | `none` | 4 | 32.6 MiB | 57.4 MiB (+76.4%) | 43.6 MiB (-24.0%; +34.0%) |
| Testify External | `none` | 32 | 40.5 MiB | 71.4 MiB (+76.4%) | 57.8 MiB (-19.0%; +42.8%) |
| Testify External | `-race` | 4 | 34.1 MiB | 56.0 MiB (+64.5%) | 49.2 MiB (-12.2%; +44.4%) |
| Testify External | `-race` | 32 | 42.6 MiB | 73.3 MiB (+72.0%) | 58.9 MiB (-19.6%; +38.3%) |
| Testify External | `-cover` | 4 | 34.5 MiB | 56.5 MiB (+63.7%) | 44.4 MiB (-21.4%; +28.6%) |
| Testify External | `-cover` | 32 | 44.5 MiB | 73.1 MiB (+64.1%) | 57.5 MiB (-21.3%; +29.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 33.4 MiB | 56.9 MiB (+70.4%) | 45.4 MiB (-20.2%; +36.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 41.2 MiB | 73.4 MiB (+78.2%) | 58.1 MiB (-20.9%; +41.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 33.8 MiB | 58.8 MiB (+74.0%) | 51.0 MiB (-13.2%; +51.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 43.3 MiB | 74.2 MiB (+71.5%) | 61.0 MiB (-17.9%; +40.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 34.0 MiB | 57.4 MiB (+68.9%) | 46.9 MiB (-18.3%; +37.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 43.3 MiB | 71.9 MiB (+66.1%) | 60.1 MiB (-16.5%; +38.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 33.8 MiB | 57.0 MiB (+68.9%) | 54.1 MiB (-5.2%; +60.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 43.8 MiB | 71.9 MiB (+64.1%) | 64.7 MiB (-10.0%; +47.7%) |

## Warm dependencies — forced fresh link

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 320.4 MiB | 797.7 MiB (+149.0%) | 435.1 MiB (-45.5%; +35.8%) |
| Gin | `none` | 32 | 359.7 MiB | 1026.9 MiB (+185.5%) | 491.8 MiB (-52.1%; +36.7%) |
| Chi | `none` | 4 | 124.8 MiB | 342.1 MiB (+174.2%) | 171.8 MiB (-49.8%; +37.7%) |
| Chi | `none` | 32 | 144.5 MiB | 359.7 MiB (+148.9%) | 185.0 MiB (-48.6%; +28.0%) |
| Gin | `-race` | 4 | 375.9 MiB | 896.3 MiB (+138.4%) | 506.8 MiB (-43.5%; +34.8%) |
| Gin | `-race` | 32 | 416.0 MiB | 1224.3 MiB (+194.3%) | 619.8 MiB (-49.4%; +49.0%) |
| Gin | `-cover` | 4 | 330.9 MiB | 756.0 MiB (+128.5%) | 381.0 MiB (-49.6%; +15.1%) |
| Gin | `-cover` | 32 | 363.7 MiB | 1067.3 MiB (+193.5%) | 495.9 MiB (-53.5%; +36.4%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 369.9 MiB | 743.5 MiB (+101.0%) | 417.5 MiB (-43.9%; +12.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 402.4 MiB | 1074.6 MiB (+167.1%) | 458.7 MiB (-57.3%; +14.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 407.8 MiB | 866.4 MiB (+112.5%) | 476.9 MiB (-45.0%; +16.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 469.7 MiB | 1415.0 MiB (+201.2%) | 566.2 MiB (-60.0%; +20.5%) |
| Chi | `-race` | 4 | 144.9 MiB | 390.4 MiB (+169.4%) | 186.3 MiB (-52.3%; +28.5%) |
| Chi | `-race` | 32 | 161.5 MiB | 416.3 MiB (+157.8%) | 202.8 MiB (-51.3%; +25.6%) |
| Chi | `-cover` | 4 | 131.7 MiB | 340.4 MiB (+158.6%) | 171.3 MiB (-49.7%; +30.1%) |
| Chi | `-cover` | 32 | 145.8 MiB | 378.2 MiB (+159.4%) | 185.3 MiB (-51.0%; +27.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 133.7 MiB | 359.4 MiB (+168.7%) | 173.4 MiB (-51.8%; +29.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 147.4 MiB | 375.0 MiB (+154.5%) | 180.9 MiB (-51.8%; +22.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 144.4 MiB | 379.0 MiB (+162.4%) | 190.7 MiB (-49.7%; +32.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 158.0 MiB | 411.5 MiB (+160.5%) | 201.0 MiB (-51.2%; +27.3%) |
| Testify Direct | `none` | 4 | 80.2 MiB | 220.1 MiB (+174.2%) | 112.8 MiB (-48.7%; +40.6%) |
| Testify Direct | `none` | 32 | 94.4 MiB | 237.8 MiB (+151.9%) | 121.2 MiB (-49.0%; +28.4%) |
| Testify Direct | `-race` | 4 | 89.9 MiB | 252.3 MiB (+180.5%) | 121.6 MiB (-51.8%; +35.2%) |
| Testify Direct | `-race` | 32 | 96.8 MiB | 264.6 MiB (+173.3%) | 133.9 MiB (-49.4%; +38.3%) |
| Testify Direct | `-cover` | 4 | 81.6 MiB | 222.2 MiB (+172.1%) | 113.0 MiB (-49.1%; +38.4%) |
| Testify Direct | `-cover` | 32 | 94.9 MiB | 239.7 MiB (+152.6%) | 121.5 MiB (-49.3%; +28.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 85.4 MiB | 223.2 MiB (+161.4%) | 112.8 MiB (-49.5%; +32.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 94.3 MiB | 237.1 MiB (+151.4%) | 121.0 MiB (-49.0%; +28.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 90.7 MiB | 252.9 MiB (+178.9%) | 123.0 MiB (-51.3%; +35.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 97.7 MiB | 270.1 MiB (+176.5%) | 128.4 MiB (-52.5%; +31.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 82.5 MiB | 223.1 MiB (+170.4%) | 110.2 MiB (-50.6%; +33.5%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 92.9 MiB | 236.1 MiB (+154.2%) | 122.6 MiB (-48.1%; +32.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 89.0 MiB | 252.8 MiB (+184.0%) | 115.8 MiB (-54.2%; +30.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 96.9 MiB | 267.2 MiB (+175.8%) | 127.9 MiB (-52.1%; +32.0%) |
| Testify External | `none` | 4 | 79.9 MiB | 215.7 MiB (+169.9%) | 107.8 MiB (-50.0%; +34.9%) |
| Testify External | `none` | 32 | 90.7 MiB | 233.0 MiB (+156.9%) | 122.3 MiB (-47.5%; +34.8%) |
| Testify External | `-race` | 4 | 87.5 MiB | 251.5 MiB (+187.6%) | 119.6 MiB (-52.5%; +36.7%) |
| Testify External | `-race` | 32 | 98.0 MiB | 264.9 MiB (+170.3%) | 127.6 MiB (-51.8%; +30.2%) |
| Testify External | `-cover` | 4 | 81.5 MiB | 219.4 MiB (+169.1%) | 108.3 MiB (-50.6%; +32.9%) |
| Testify External | `-cover` | 32 | 94.9 MiB | 234.3 MiB (+146.8%) | 120.3 MiB (-48.6%; +26.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 81.3 MiB | 217.8 MiB (+167.8%) | 110.6 MiB (-49.2%; +36.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 92.2 MiB | 234.4 MiB (+154.3%) | 119.6 MiB (-49.0%; +29.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 88.0 MiB | 254.6 MiB (+189.3%) | 121.9 MiB (-52.1%; +38.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 95.3 MiB | 266.2 MiB (+179.3%) | 129.5 MiB (-51.3%; +35.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 84.9 MiB | 218.1 MiB (+156.9%) | 110.7 MiB (-49.2%; +30.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 93.1 MiB | 239.2 MiB (+156.8%) | 117.5 MiB (-50.9%; +26.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 89.2 MiB | 251.3 MiB (+181.6%) | 119.6 MiB (-52.4%; +34.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 99.1 MiB | 268.1 MiB (+170.4%) | 130.9 MiB (-51.2%; +32.0%) |

## Incremental compilation — reachable test-body edit

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 225.2 MiB | 317.7 MiB (+41.1%) | 238.4 MiB (-24.9%; +5.9%) |
| Gin | `none` | 32 | 257.1 MiB | 331.1 MiB (+28.8%) | 274.9 MiB (-17.0%; +6.9%) |
| Chi | `none` | 4 | 122.6 MiB | 208.4 MiB (+70.0%) | 135.2 MiB (-35.1%; +10.3%) |
| Chi | `none` | 32 | 160.4 MiB | 227.5 MiB (+41.9%) | 165.3 MiB (-27.3%; +3.1%) |
| Gin | `-race` | 4 | 213.2 MiB | 359.8 MiB (+68.8%) | 227.5 MiB (-36.8%; +6.7%) |
| Gin | `-race` | 32 | 219.9 MiB | 374.2 MiB (+70.2%) | 235.1 MiB (-37.2%; +6.9%) |
| Gin | `-cover` | 4 | 233.8 MiB | 352.9 MiB (+51.0%) | 251.8 MiB (-28.6%; +7.7%) |
| Gin | `-cover` | 32 | 283.1 MiB | 408.1 MiB (+44.1%) | 293.6 MiB (-28.1%; +3.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 243.1 MiB | 378.8 MiB (+55.8%) | 260.8 MiB (-31.2%; +7.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 286.4 MiB | 427.0 MiB (+49.1%) | 312.6 MiB (-26.8%; +9.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 237.6 MiB | 369.2 MiB (+55.4%) | 256.6 MiB (-30.5%; +8.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 245.8 MiB | 381.7 MiB (+55.3%) | 261.5 MiB (-31.5%; +6.3%) |
| Chi | `-race` | 4 | 94.6 MiB | 245.6 MiB (+159.6%) | 121.6 MiB (-50.5%; +28.5%) |
| Chi | `-race` | 32 | 106.8 MiB | 260.8 MiB (+144.3%) | 130.9 MiB (-49.8%; +22.6%) |
| Chi | `-cover` | 4 | 131.7 MiB | 211.3 MiB (+60.4%) | 144.5 MiB (-31.6%; +9.7%) |
| Chi | `-cover` | 32 | 146.2 MiB | 228.8 MiB (+56.5%) | 161.7 MiB (-29.3%; +10.6%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 133.1 MiB | 206.8 MiB (+55.4%) | 148.8 MiB (-28.0%; +11.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 152.3 MiB | 231.2 MiB (+51.8%) | 165.1 MiB (-28.6%; +8.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 103.6 MiB | 249.4 MiB (+140.8%) | 122.4 MiB (-50.9%; +18.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 111.2 MiB | 263.2 MiB (+136.7%) | 134.5 MiB (-48.9%; +20.9%) |
| Testify Direct | `none` | 4 | 86.4 MiB | 219.5 MiB (+154.1%) | 113.4 MiB (-48.3%; +31.4%) |
| Testify Direct | `none` | 32 | 95.6 MiB | 237.7 MiB (+148.8%) | 120.6 MiB (-49.3%; +26.2%) |
| Testify Direct | `-race` | 4 | 89.6 MiB | 254.8 MiB (+184.5%) | 123.5 MiB (-51.5%; +37.9%) |
| Testify Direct | `-race` | 32 | 99.7 MiB | 270.8 MiB (+171.7%) | 128.5 MiB (-52.5%; +29.0%) |
| Testify Direct | `-cover` | 4 | 84.4 MiB | 229.9 MiB (+172.4%) | 115.2 MiB (-49.9%; +36.5%) |
| Testify Direct | `-cover` | 32 | 97.0 MiB | 242.0 MiB (+149.5%) | 123.7 MiB (-48.9%; +27.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 87.1 MiB | 224.5 MiB (+157.7%) | 117.2 MiB (-47.8%; +34.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 94.2 MiB | 237.4 MiB (+152.0%) | 122.9 MiB (-48.2%; +30.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 93.2 MiB | 250.0 MiB (+168.2%) | 124.0 MiB (-50.4%; +33.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 98.5 MiB | 270.2 MiB (+174.3%) | 134.9 MiB (-50.1%; +37.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 86.9 MiB | 220.7 MiB (+154.1%) | 116.1 MiB (-47.4%; +33.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 94.2 MiB | 239.5 MiB (+154.4%) | 124.9 MiB (-47.9%; +32.6%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 87.5 MiB | 258.8 MiB (+195.8%) | 124.6 MiB (-51.8%; +42.4%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 97.7 MiB | 272.4 MiB (+178.7%) | 135.1 MiB (-50.4%; +38.2%) |
| Testify External | `none` | 4 | 84.2 MiB | 215.1 MiB (+155.4%) | 109.8 MiB (-49.0%; +30.4%) |
| Testify External | `none` | 32 | 89.1 MiB | 232.1 MiB (+160.4%) | 120.1 MiB (-48.2%; +34.7%) |
| Testify External | `-race` | 4 | 88.6 MiB | 247.8 MiB (+179.6%) | 121.3 MiB (-51.0%; +36.9%) |
| Testify External | `-race` | 32 | 95.4 MiB | 268.4 MiB (+181.3%) | 129.2 MiB (-51.9%; +35.4%) |
| Testify External | `-cover` | 4 | 84.7 MiB | 223.8 MiB (+164.3%) | 112.5 MiB (-49.7%; +32.9%) |
| Testify External | `-cover` | 32 | 92.1 MiB | 235.3 MiB (+155.4%) | 118.0 MiB (-49.8%; +28.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 83.1 MiB | 219.7 MiB (+164.4%) | 113.0 MiB (-48.6%; +36.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 91.0 MiB | 234.3 MiB (+157.4%) | 119.9 MiB (-48.8%; +31.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 87.4 MiB | 252.2 MiB (+188.4%) | 118.4 MiB (-53.0%; +35.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 97.2 MiB | 269.4 MiB (+177.3%) | 131.5 MiB (-51.2%; +35.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 82.3 MiB | 226.8 MiB (+175.6%) | 111.5 MiB (-50.8%; +35.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 90.8 MiB | 236.0 MiB (+159.8%) | 118.1 MiB (-49.9%; +30.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 90.2 MiB | 252.4 MiB (+179.8%) | 121.2 MiB (-52.0%; +34.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 95.6 MiB | 268.6 MiB (+181.0%) | 133.1 MiB (-50.5%; +39.2%) |

## Unused-constant edit — diagnostic

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 225.9 MiB | 246.7 MiB (+9.2%) | 241.4 MiB (-2.1%; +6.8%) |
| Gin | `none` | 32 | 276.4 MiB | 288.6 MiB (+4.4%) | 279.6 MiB (-3.1%; +1.1%) |
| Chi | `none` | 4 | 123.3 MiB | 137.0 MiB (+11.2%) | 141.2 MiB (+3.0%; +14.5%) |
| Chi | `none` | 32 | 171.6 MiB | 167.5 MiB (-2.4%) | 181.6 MiB (+8.4%; +5.8%) |
| Gin | `-race` | 4 | 214.0 MiB | 231.6 MiB (+8.2%) | 236.3 MiB (+2.0%; +10.4%) |
| Gin | `-race` | 32 | 225.1 MiB | 245.6 MiB (+9.1%) | 246.6 MiB (+0.4%; +9.5%) |
| Gin | `-cover` | 4 | 235.1 MiB | 367.2 MiB (+56.2%) | 249.9 MiB (-31.9%; +6.3%) |
| Gin | `-cover` | 32 | 286.7 MiB | 424.9 MiB (+48.2%) | 302.0 MiB (-28.9%; +5.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 250.6 MiB | 364.8 MiB (+45.6%) | 259.5 MiB (-28.9%; +3.5%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 301.5 MiB | 433.6 MiB (+43.8%) | 306.1 MiB (-29.4%; +1.5%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 234.9 MiB | 363.1 MiB (+54.6%) | 253.7 MiB (-30.1%; +8.0%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 246.0 MiB | 377.6 MiB (+53.5%) | 261.7 MiB (-30.7%; +6.4%) |
| Chi | `-race` | 4 | 102.1 MiB | 115.8 MiB (+13.4%) | 118.1 MiB (+2.0%; +15.7%) |
| Chi | `-race` | 32 | 109.3 MiB | 127.7 MiB (+16.9%) | 126.5 MiB (-0.9%; +15.8%) |
| Chi | `-cover` | 4 | 130.3 MiB | 146.2 MiB (+12.2%) | 148.9 MiB (+1.8%; +14.3%) |
| Chi | `-cover` | 32 | 147.2 MiB | 169.5 MiB (+15.1%) | 161.5 MiB (-4.7%; +9.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 135.5 MiB | 148.3 MiB (+9.4%) | 146.8 MiB (-1.0%; +8.3%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 153.1 MiB | 171.7 MiB (+12.1%) | 167.2 MiB (-2.6%; +9.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 113.6 MiB | 125.2 MiB (+10.2%) | 122.2 MiB (-2.4%; +7.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 115.6 MiB | 132.5 MiB (+14.6%) | 131.3 MiB (-0.9%; +13.6%) |
| Testify Direct | `none` | 4 | 67.5 MiB | 80.6 MiB (+19.5%) | 79.8 MiB (-1.1%; +18.2%) |
| Testify Direct | `none` | 32 | 87.8 MiB | 102.5 MiB (+16.7%) | 107.6 MiB (+4.9%; +22.5%) |
| Testify Direct | `-race` | 4 | 56.2 MiB | 68.4 MiB (+21.7%) | 73.9 MiB (+8.0%; +31.5%) |
| Testify Direct | `-race` | 32 | 71.6 MiB | 87.2 MiB (+21.7%) | 85.2 MiB (-2.3%; +18.9%) |
| Testify Direct | `-cover` | 4 | 67.9 MiB | 81.8 MiB (+20.4%) | 77.2 MiB (-5.6%; +13.7%) |
| Testify Direct | `-cover` | 32 | 91.7 MiB | 102.3 MiB (+11.5%) | 104.9 MiB (+2.6%; +14.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 69.4 MiB | 82.7 MiB (+19.0%) | 80.2 MiB (-2.9%; +15.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 83.8 MiB | 111.5 MiB (+33.0%) | 101.1 MiB (-9.3%; +20.6%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 58.4 MiB | 71.8 MiB (+22.9%) | 73.2 MiB (+2.1%; +25.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 67.3 MiB | 86.2 MiB (+28.2%) | 81.3 MiB (-5.8%; +20.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 68.4 MiB | 81.6 MiB (+19.2%) | 80.5 MiB (-1.4%; +17.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 85.0 MiB | 101.8 MiB (+19.9%) | 101.4 MiB (-0.5%; +19.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 56.6 MiB | 68.6 MiB (+21.3%) | 74.6 MiB (+8.7%; +31.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 67.6 MiB | 83.8 MiB (+23.9%) | 80.4 MiB (-4.0%; +18.9%) |
| Testify External | `none` | 4 | 43.0 MiB | 65.1 MiB (+51.4%) | 55.8 MiB (-14.4%; +29.7%) |
| Testify External | `none` | 32 | 61.2 MiB | 85.7 MiB (+40.0%) | 75.8 MiB (-11.5%; +23.9%) |
| Testify External | `-race` | 4 | 46.2 MiB | 65.4 MiB (+41.6%) | 61.2 MiB (-6.4%; +32.5%) |
| Testify External | `-race` | 32 | 57.5 MiB | 82.7 MiB (+43.7%) | 79.0 MiB (-4.5%; +37.2%) |
| Testify External | `-cover` | 4 | 45.7 MiB | 64.2 MiB (+40.3%) | 58.1 MiB (-9.4%; +27.1%) |
| Testify External | `-cover` | 32 | 64.2 MiB | 90.5 MiB (+41.0%) | 79.1 MiB (-12.7%; +23.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 44.8 MiB | 66.7 MiB (+48.9%) | 57.0 MiB (-14.6%; +27.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 63.5 MiB | 89.8 MiB (+41.5%) | 76.9 MiB (-14.3%; +21.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 47.7 MiB | 65.0 MiB (+36.3%) | 65.6 MiB (+0.8%; +37.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 59.4 MiB | 85.4 MiB (+43.8%) | 79.6 MiB (-6.8%; +34.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 45.5 MiB | 66.6 MiB (+46.3%) | 59.4 MiB (-10.9%; +30.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 57.8 MiB | 81.6 MiB (+41.3%) | 73.8 MiB (-9.5%; +27.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 45.1 MiB | 66.9 MiB (+48.4%) | 66.4 MiB (-0.9%; +47.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 61.7 MiB | 82.4 MiB (+33.5%) | 78.0 MiB (-5.3%; +26.4%) |

## Runtime

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 35.1 MiB | 55.4 MiB (+57.9%) | 38.5 MiB (-30.5%; +9.7%) | 42.0 MiB (-24.1%; +19.8%) |
| Gin | `none` | 32 | 42.7 MiB | 81.3 MiB (+90.2%) | 52.8 MiB (-35.0%; +23.6%) | 53.7 MiB (-34.0%; +25.6%) |
| Chi | `none` | 4 | 34.2 MiB | 57.1 MiB (+67.0%) | 33.5 MiB (-41.3%; -1.9%) | 45.0 MiB (-21.2%; +31.6%) |
| Chi | `none` | 32 | 106.1 MiB | 133.8 MiB (+26.1%) | 91.5 MiB (-31.6%; -13.8%) | 94.1 MiB (-29.7%; -11.3%) |
| Gin | `-race` | 4 | 169.4 MiB | FAIL 4/4 | 188.3 MiB (vs Orchestrion unavailable; +11.2% vs Native) | 189.9 MiB (vs Orchestrion unavailable; +12.1% vs Native) |
| Gin | `-race` | 32 | 264.9 MiB | FAIL 4/4 | 328.9 MiB (vs Orchestrion unavailable; +24.2% vs Native) | 324.1 MiB (vs Orchestrion unavailable; +22.4% vs Native) |
| Gin | `-cover` | 4 | 35.1 MiB | 94.9 MiB (+170.0%) | 37.3 MiB (-60.7%; +6.2%) | 39.1 MiB (-58.8%; +11.2%) |
| Gin | `-cover` | 32 | 41.7 MiB | 172.2 MiB (+312.7%) | 52.8 MiB (-69.3%; +26.5%) | 53.5 MiB (-68.9%; +28.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 36.3 MiB | 89.8 MiB (+147.0%) | 83.1 MiB (-7.4%; +128.8%) | 84.6 MiB (-5.8%; +132.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 43.0 MiB | 167.9 MiB (+291.0%) | 147.8 MiB (-12.0%; +244.0%) | 147.8 MiB (-12.0%; +244.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 179.6 MiB | FAIL 3/4 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 284.4 MiB | FAIL 3/4 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 2988.7 MiB | 2950.3 MiB (-1.3%) | 2996.7 MiB (+1.6%; +0.3%) | 2993.0 MiB (+1.4%; +0.1%) |
| Chi | `-race` | 32 | 3053.0 MiB | 3089.0 MiB (+1.2%) | 3078.6 MiB (-0.3%; +0.8%) | 3067.8 MiB (-0.7%; +0.5%) |
| Chi | `-cover` | 4 | 45.3 MiB | 75.2 MiB (+66.2%) | 55.4 MiB (-26.4%; +22.3%) | 47.6 MiB (-36.7%; +5.2%) |
| Chi | `-cover` | 32 | 88.0 MiB | 140.1 MiB (+59.2%) | 99.0 MiB (-29.3%; +12.5%) | 111.6 MiB (-20.3%; +26.9%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 30.8 MiB | 65.2 MiB (+111.8%) | 42.2 MiB (-35.3%; +37.2%) | 41.9 MiB (-35.7%; +36.2%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 49.1 MiB | 107.8 MiB (+119.8%) | 61.3 MiB (-43.2%; +24.9%) | 67.3 MiB (-37.6%; +37.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 2921.7 MiB | 3060.8 MiB (+4.8%) | 2967.3 MiB (-3.1%; +1.6%) | 2925.8 MiB (-4.4%; +0.1%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 2914.6 MiB | 3023.2 MiB (+3.7%) | 2952.5 MiB (-2.3%; +1.3%) | 2956.6 MiB (-2.2%; +1.4%) |
| Testify Direct | `none` | 4 | 11.1 MiB | 17.4 MiB (+57.2%) | 13.7 MiB (-21.4%; +23.6%) | 13.2 MiB (-24.6%; +18.5%) |
| Testify Direct | `none` | 32 | 12.0 MiB | 20.6 MiB (+71.8%) | 14.3 MiB (-30.8%; +18.8%) | 14.6 MiB (-29.4%; +21.2%) |
| Testify Direct | `-race` | 4 | 25.3 MiB | 52.2 MiB (+105.9%) | 32.7 MiB (-37.4%; +28.9%) | 31.7 MiB (-39.3%; +24.9%) |
| Testify Direct | `-race` | 32 | 26.7 MiB | 56.7 MiB (+112.0%) | 35.0 MiB (-38.3%; +30.9%) | 34.6 MiB (-39.0%; +29.3%) |
| Testify Direct | `-cover` | 4 | 11.1 MiB | 19.5 MiB (+75.9%) | 13.3 MiB (-31.9%; +19.9%) | 13.0 MiB (-33.1%; +17.7%) |
| Testify Direct | `-cover` | 32 | 11.8 MiB | 23.2 MiB (+97.6%) | 14.7 MiB (-36.9%; +24.8%) | 15.4 MiB (-33.9%; +30.7%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 11.1 MiB | 19.7 MiB (+77.2%) | 15.9 MiB (-19.1%; +43.3%) | 15.5 MiB (-21.0%; +39.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 12.5 MiB | 22.7 MiB (+81.9%) | 18.2 MiB (-19.7%; +46.2%) | 17.7 MiB (-22.0%; +42.0%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 25.8 MiB | 53.1 MiB (+106.1%) | 34.1 MiB (-35.7%; +32.5%) | 33.3 MiB (-37.3%; +29.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 27.5 MiB | 58.0 MiB (+110.6%) | 40.1 MiB (-30.8%; +45.7%) | 39.1 MiB (-32.6%; +41.9%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 11.4 MiB | 20.1 MiB (+76.7%) | 16.3 MiB (-18.9%; +43.3%) | 15.8 MiB (-21.1%; +39.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 12.2 MiB | 23.9 MiB (+95.5%) | 18.3 MiB (-23.4%; +49.8%) | 19.2 MiB (-19.8%; +56.7%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 27.1 MiB | 62.4 MiB (+130.4%) | 39.0 MiB (-37.4%; +44.2%) | 37.6 MiB (-39.7%; +38.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 28.8 MiB | 67.1 MiB (+133.0%) | 44.9 MiB (-33.2%; +55.8%) | 43.4 MiB (-35.3%; +50.7%) |
| Testify External | `none` | 4 | 11.0 MiB | 17.5 MiB (+58.3%) | 13.3 MiB (-23.8%; +20.6%) | 13.3 MiB (-23.7%; +20.9%) |
| Testify External | `none` | 32 | 11.5 MiB | 20.3 MiB (+76.0%) | 15.3 MiB (-24.7%; +32.6%) | 14.6 MiB (-28.0%; +26.7%) |
| Testify External | `-race` | 4 | 25.5 MiB | 51.4 MiB (+101.7%) | 32.4 MiB (-37.0%; +27.0%) | 31.7 MiB (-38.5%; +24.1%) |
| Testify External | `-race` | 32 | 26.8 MiB | 56.2 MiB (+110.0%) | 35.8 MiB (-36.2%; +34.0%) | 34.3 MiB (-39.0%; +28.2%) |
| Testify External | `-cover` | 4 | 11.0 MiB | 19.2 MiB (+74.3%) | 13.2 MiB (-31.4%; +19.5%) | 13.4 MiB (-30.3%; +21.6%) |
| Testify External | `-cover` | 32 | 11.5 MiB | 22.7 MiB (+97.0%) | 15.0 MiB (-34.0%; +30.1%) | 15.3 MiB (-32.3%; +33.4%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 11.1 MiB | 19.0 MiB (+71.7%) | 16.0 MiB (-15.6%; +44.9%) | 15.6 MiB (-18.0%; +40.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 11.8 MiB | 22.5 MiB (+91.0%) | 17.9 MiB (-20.7%; +51.5%) | 18.3 MiB (-18.6%; +55.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 25.8 MiB | 54.0 MiB (+109.3%) | 34.2 MiB (-36.7%; +32.5%) | 33.0 MiB (-38.8%; +28.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 27.8 MiB | 58.6 MiB (+111.0%) | 39.5 MiB (-32.6%; +42.1%) | 38.2 MiB (-34.8%; +37.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 11.8 MiB | 20.1 MiB (+70.3%) | 16.2 MiB (-19.5%; +37.2%) | 16.2 MiB (-19.6%; +36.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 12.5 MiB | 24.0 MiB (+92.1%) | 18.8 MiB (-21.5%; +50.7%) | 19.3 MiB (-19.6%; +54.5%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 27.6 MiB | 62.4 MiB (+126.2%) | 38.6 MiB (-38.1%; +40.0%) | 37.7 MiB (-39.6%; +36.7%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 28.9 MiB | 67.3 MiB (+133.0%) | 44.8 MiB (-33.4%; +55.2%) | 42.9 MiB (-36.3%; +48.5%) |

## Gin: local Agent delivery and telemetry

Three measured runs follow one warmup.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 40.6 MiB | 84.3 MiB (+107.5%) | 60.8 MiB (-27.9%; +49.6%) |
| Gin | `none` | 32 | 42.6 MiB | 82.0 MiB (+92.6%) | 80.0 MiB (-2.4%; +87.9%) |
