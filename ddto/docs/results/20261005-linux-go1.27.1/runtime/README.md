# Runtime of the prebuilt test binaries

Times are medians in seconds, using five measured runs after one warmup. Each
run executes all packages with their original flags. The clock includes package
startup, settings, tests and final delivery; it excludes compilation, CLI
preparation, receiver startup and offline decoding.

Agentless gzip/MessagePack requests go to a successful loopback receiver.
Telemetry is disabled in this matrix; the Agent control checks it separately.
Mini deferred runs the same executable as Mini with deferred delivery enabled.
Percentages compare total Orchestrion time first, then Native time.

A failed repetition, including warmup, disqualifies that variant's median.
`FAIL n/6` reports its failed attempts. `UNVERIFIED` means execution passed but a
required reference comparison is missing. Every duration remains in
[observations.csv](observations.csv); CPU, memory peaks and ranges are below.

Go's default `-covermode=set` produces aggregate coverage. Per-test CI uploads
require count or atomic mode in both the SDK and Mini. Passing Mini race runs
can use the non-race SDK event inventory only when Native inventories match and
coverage is absent. Coverage groups without a successful SDK oracle remain
unverified. See [the comparison limits](../../../benchmarks.md#runtime-limits-and-failures).

| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.183472 s | 0.227665 s | 0.230596 s (+1.3%; +25.7%) | 0.210650 s (-7.5%; +14.8%) | 0.372718 s (+63.7%; +103.1%) |
| Gin | `none` | 32 | 0.192854 s | 0.235763 s | 0.239290 s (+1.5%; +24.1%) | 0.219925 s (-6.7%; +14.0%) | 0.405775 s (+72.1%; +110.4%) |
| Chi | `none` | 4 | 26.136864 s | 26.174048 s | 26.177771 s (+0.0%; +0.2%) | 26.175779 s (+0.0%; +0.1%) | 26.186206 s (+0.0%; +0.2%) |
| Chi | `none` | 32 | 26.141335 s | 26.179190 s | 26.182586 s (+0.0%; +0.2%) | 26.156964 s (-0.1%; +0.1%) | 26.172730 s (-0.0%; +0.1%) |
| Gin | `-race` | 4 | 2.040156 s | FAIL 5/6 | FAIL 6/6 | 2.096352 s (vs Orchestrion unavailable; +2.8% vs Native) | 2.151701 s (vs Orchestrion unavailable; +5.5% vs Native) |
| Gin | `-race` | 32 | 1.415086 s | FAIL 6/6 | FAIL 6/6 | 1.576364 s (vs Orchestrion unavailable; +11.4% vs Native) | 2.119058 s (vs Orchestrion unavailable; +49.7% vs Native) |
| Gin | `-cover` | 4 | 0.188422 s | 0.484403 s | 0.499982 s (+3.2%; +165.4%) | 0.474904 s (-2.0%; +152.0%) | 0.639221 s (+32.0%; +239.2%) |
| Gin | `-cover` | 32 | 0.198064 s | 0.410552 s | 0.419116 s (+2.1%; +111.6%) | 0.399688 s (-2.6%; +101.8%) | 0.578001 s (+40.8%; +191.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.253092 s | 6.616408 s | 6.572407 s (-0.7%; +2496.8%) | 6.768918 s (+2.3%; +2574.5%) | 7.246165 s (+9.5%; +2763.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.253249 s | 6.541288 s | 6.617863 s (+1.2%; +2513.2%) | 6.853520 s (+4.8%; +2606.2%) | 7.281139 s (+11.3%; +2775.1%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 3.415680 s | FAIL 5/6 | FAIL 6/6 | FAIL 6/6 | FAIL 3/6 |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 3.446807 s | FAIL 5/6 | FAIL 6/6 | FAIL 2/6 | FAIL 4/6 |
| Chi | `-race` | 4 | 27.214190 s | 27.322207 s | 27.304652 s (-0.1%; +0.3%) | 27.302216 s (-0.1%; +0.3%) | 27.333221 s (+0.0%; +0.4%) |
| Chi | `-race` | 32 | 27.218039 s | 27.324007 s | 27.318418 s (-0.0%; +0.4%) | 27.305109 s (-0.1%; +0.3%) | 27.318877 s (-0.0%; +0.4%) |
| Chi | `-cover` | 4 | 26.136957 s | 26.229098 s | 26.231160 s (+0.0%; +0.4%) | 26.229944 s (+0.0%; +0.4%) | 26.243247 s (+0.1%; +0.4%) |
| Chi | `-cover` | 32 | 26.140129 s | 26.233960 s | 26.236293 s (+0.0%; +0.4%) | 26.231442 s (-0.0%; +0.3%) | 26.243466 s (+0.0%; +0.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 26.139077 s | 26.788571 s | 26.795046 s (+0.0%; +2.5%) | 26.796430 s (+0.0%; +2.5%) | 26.868350 s (+0.3%; +2.8%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 26.143078 s | 26.843831 s | 26.853596 s (+0.0%; +2.7%) | 26.848687 s (+0.0%; +2.7%) | 26.914561 s (+0.3%; +3.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 27.227274 s | 28.864119 s | 28.872932 s (+0.0%; +6.0%) | 28.817076 s (-0.2%; +5.8%) | 29.002213 s (+0.5%; +6.5%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 27.233273 s | 28.933591 s | 28.957669 s (+0.1%; +6.3%) | 28.943650 s (+0.0%; +6.3%) | 29.125580 s (+0.7%; +6.9%) |
| Testify Direct | `none` | 4 | 0.002768 s | 0.014293 s | 0.014375 s (+0.6%; +419.4%) | 0.009877 s (-30.9%; +256.9%) | 0.010434 s (-27.0%; +277.0%) |
| Testify Direct | `none` | 32 | 0.003034 s | 0.016014 s | 0.015778 s (-1.5%; +420.0%) | 0.011020 s (-31.2%; +263.2%) | 0.011532 s (-28.0%; +280.1%) |
| Testify Direct | `-race` | 4 | 1.008293 s | 1.041778 s | 1.041911 s (+0.0%; +3.3%) | 1.021002 s (-2.0%; +1.3%) | 1.023241 s (-1.8%; +1.5%) |
| Testify Direct | `-race` | 32 | 1.008927 s | 1.045827 s | 1.046037 s (+0.0%; +3.7%) | 1.024066 s (-2.1%; +1.5%) | 1.026128 s (-1.9%; +1.7%) |
| Testify Direct | `-cover` | 4 | 0.003040 s | 0.023040 s | 0.022887 s (-0.7%; +653.0%) | 0.018788 s (-18.5%; +518.1%) | 0.019584 s (-15.0%; +544.3%) |
| Testify Direct | `-cover` | 32 | 0.003225 s | 0.025315 s | 0.024856 s (-1.8%; +670.7%) | 0.020611 s (-18.6%; +539.1%) | 0.020988 s (-17.1%; +550.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.003020 s | 0.024021 s | 0.023262 s (-3.2%; +670.2%) | 0.020026 s (-16.6%; +563.0%) | 0.020783 s (-13.5%; +588.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.003157 s | 0.026260 s | 0.025903 s (-1.4%; +720.4%) | 0.022356 s (-14.9%; +608.0%) | 0.023393 s (-10.9%; +640.9%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009339 s | 1.052694 s | 1.052015 s (-0.1%; +4.2%) | 1.033860 s (-1.8%; +2.4%) | 1.036696 s (-1.5%; +2.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009875 s | 1.057152 s | 1.056525 s (-0.1%; +4.6%) | 1.037662 s (-1.8%; +2.8%) | 1.039850 s (-1.6%; +3.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003584 s | 0.038847 s | 0.039359 s (+1.3%; +998.3%) | 0.035194 s (-9.4%; +882.1%) | 0.036004 s (-7.3%; +904.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.003901 s | 0.041828 s | 0.041439 s (-0.9%; +962.2%) | 0.038645 s (-7.6%; +890.6%) | 0.039003 s (-6.8%; +899.8%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.011706 s | 1.089557 s | 1.089747 s (+0.0%; +7.7%) | 1.073814 s (-1.4%; +6.1%) | 1.075661 s (-1.3%; +6.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012462 s | 1.097505 s | 1.095781 s (-0.2%; +8.2%) | 1.079619 s (-1.6%; +6.6%) | 1.081836 s (-1.4%; +6.9%) |
| Testify External | `none` | 4 | 0.002716 s | 0.014248 s | 0.014253 s (+0.0%; +424.8%) | 0.009969 s (-30.0%; +267.1%) | 0.010055 s (-29.4%; +270.2%) |
| Testify External | `none` | 32 | 0.002940 s | 0.016444 s | 0.015532 s (-5.5%; +428.4%) | 0.011040 s (-32.9%; +275.6%) | 0.011582 s (-29.6%; +294.0%) |
| Testify External | `-race` | 4 | 1.008352 s | 1.041991 s | 1.042802 s (+0.1%; +3.4%) | 1.022056 s (-1.9%; +1.4%) | 1.023050 s (-1.8%; +1.5%) |
| Testify External | `-race` | 32 | 1.009078 s | 1.046242 s | 1.046637 s (+0.0%; +3.7%) | 1.023915 s (-2.1%; +1.5%) | 1.025231 s (-2.0%; +1.6%) |
| Testify External | `-cover` | 4 | 0.002880 s | 0.023010 s | 0.023562 s (+2.4%; +718.2%) | 0.018956 s (-17.6%; +558.3%) | 0.019208 s (-16.5%; +567.0%) |
| Testify External | `-cover` | 32 | 0.003031 s | 0.025533 s | 0.025173 s (-1.4%; +730.4%) | 0.020540 s (-19.6%; +577.6%) | 0.021280 s (-16.7%; +602.0%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.003078 s | 0.023386 s | 0.024714 s (+5.7%; +703.0%) | 0.020176 s (-13.7%; +555.5%) | 0.020872 s (-10.8%; +578.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.003307 s | 0.025663 s | 0.025959 s (+1.2%; +684.9%) | 0.022701 s (-11.5%; +586.4%) | 0.022932 s (-10.6%; +593.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009322 s | 1.053227 s | 1.053197 s (-0.0%; +4.3%) | 1.033920 s (-1.8%; +2.4%) | 1.036165 s (-1.6%; +2.7%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009809 s | 1.058201 s | 1.057683 s (-0.0%; +4.7%) | 1.038404 s (-1.9%; +2.8%) | 1.039951 s (-1.7%; +3.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003702 s | 0.039288 s | 0.038986 s (-0.8%; +953.2%) | 0.035703 s (-9.1%; +864.5%) | 0.036444 s (-7.2%; +884.5%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.004002 s | 0.042201 s | 0.042303 s (+0.2%; +957.0%) | 0.038654 s (-8.4%; +865.9%) | 0.039151 s (-7.2%; +878.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.012068 s | 1.090319 s | 1.091829 s (+0.1%; +7.9%) | 1.073767 s (-1.5%; +6.1%) | 1.076341 s (-1.3%; +6.4%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012989 s | 1.098761 s | 1.097942 s (-0.1%; +8.4%) | 1.079703 s (-1.7%; +6.6%) | 1.083401 s (-1.4%; +7.0%) |

## CPU, memory and dispersion

| Case | CPUs | Variant | Wall median (s) | Min–max (s) | CV | CPU median (CPU-s) | Peak median (MiB) |
| --- | ---: | --- | ---: | --- | ---: | ---: | ---: |
| gin | 4 | native | 0.183472 | 0.178205–0.194327 | 3.18% | 0.170003 | 36.64 |
| gin | 4 | orchestrion | 0.227665 | 0.219127–0.241448 | 3.70% | 0.348156 | 60.29 |
| gin | 4 | sdk | 0.230596 | 0.223941–0.245280 | 3.32% | 0.365246 | 60.77 |
| gin | 4 | mini | 0.210650 | 0.203791–0.214231 | 1.71% | 0.278226 | 42.35 |
| gin | 4 | mini-deferred | 0.372718 | 0.367020–0.378893 | 1.20% | 0.501949 | 43.52 |
| gin | 32 | native | 0.192854 | 0.190069–0.194346 | 0.78% | 0.217764 | 42.18 |
| gin | 32 | orchestrion | 0.235763 | 0.230035–0.256558 | 3.80% | 0.439478 | 70.67 |
| gin | 32 | sdk | 0.239290 | 0.230608–0.254580 | 3.95% | 0.444977 | 75.07 |
| gin | 32 | mini | 0.219925 | 0.213324–0.224825 | 1.73% | 0.348720 | 55.27 |
| gin | 32 | mini-deferred | 0.405775 | 0.402314–0.409181 | 0.57% | 0.649741 | 50.46 |
| chi | 4 | native | 26.136864 | 26.135458–26.150430 | 0.02% | 2.901706 | 45.64 |
| chi | 4 | orchestrion | 26.174048 | 26.152724–26.184653 | 0.05% | 2.682397 | 79.65 |
| chi | 4 | sdk | 26.177771 | 26.172329–26.183149 | 0.02% | 2.702088 | 78.06 |
| chi | 4 | mini | 26.175779 | 26.150835–26.181562 | 0.04% | 3.237420 | 47.36 |
| chi | 4 | mini-deferred | 26.186206 | 26.162265–26.188441 | 0.04% | 3.212222 | 46.58 |
| chi | 32 | native | 26.141335 | 26.139699–26.141874 | 0.00% | 7.661383 | 94.73 |
| chi | 32 | orchestrion | 26.179190 | 26.160035–26.183551 | 0.04% | 7.197552 | 128.05 |
| chi | 32 | sdk | 26.182586 | 26.159719–26.187045 | 0.04% | 7.271155 | 116.96 |
| chi | 32 | mini | 26.156964 | 26.152704–26.183745 | 0.04% | 9.404130 | 91.67 |
| chi | 32 | mini-deferred | 26.172730 | 26.168914–26.192003 | 0.04% | 9.274012 | 89.56 |
| gin--race | 4 | native | 2.040156 | 2.034008–2.041493 | 0.14% | 2.627885 | 199.33 |
| gin--race | 4 | mini | 2.096352 | 2.094513–2.096698 | 0.04% | 3.082379 | 245.68 |
| gin--race | 4 | mini-deferred | 2.151701 | 2.146966–2.159195 | 0.21% | 4.068872 | 250.47 |
| gin--race | 32 | native | 1.415086 | 1.407846–1.426243 | 0.43% | 2.725913 | 262.89 |
| gin--race | 32 | mini | 1.576364 | 1.564418–1.589533 | 0.56% | 3.223940 | 334.59 |
| gin--race | 32 | mini-deferred | 2.119058 | 2.107336–2.132033 | 0.38% | 4.546597 | 345.30 |
| gin--cover | 4 | native | 0.188422 | 0.186399–0.190422 | 0.74% | 0.192585 | 35.87 |
| gin--cover | 4 | orchestrion | 0.484403 | 0.471239–0.497941 | 2.10% | 1.289090 | 92.60 |
| gin--cover | 4 | sdk | 0.499982 | 0.490171–0.511056 | 1.63% | 1.350209 | 95.57 |
| gin--cover | 4 | mini | 0.474904 | 0.461517–0.493224 | 2.29% | 1.261065 | 83.11 |
| gin--cover | 4 | mini-deferred | 0.639221 | 0.629444–0.651226 | 1.21% | 1.461969 | 81.26 |
| gin--cover | 32 | native | 0.198064 | 0.194113–0.219861 | 4.51% | 0.238151 | 42.11 |
| gin--cover | 32 | orchestrion | 0.410552 | 0.404105–0.427463 | 2.15% | 3.361391 | 165.18 |
| gin--cover | 32 | sdk | 0.419116 | 0.413752–0.423465 | 0.82% | 3.205708 | 165.59 |
| gin--cover | 32 | mini | 0.399688 | 0.385027–0.411557 | 2.33% | 3.081564 | 147.93 |
| gin--cover | 32 | mini-deferred | 0.578001 | 0.565816–0.582116 | 1.03% | 3.444198 | 146.17 |
| gin--cover-client | 4 | native | 0.253092 | 0.250583–0.263731 | 1.89% | 0.385578 | 35.73 |
| gin--cover-client | 4 | orchestrion | 6.616408 | 6.582757–6.973707 | 2.22% | 14.125127 | 98.61 |
| gin--cover-client | 4 | sdk | 6.572407 | 6.558565–6.710659 | 0.86% | 14.069594 | 96.92 |
| gin--cover-client | 4 | mini | 6.768918 | 6.747240–6.812278 | 0.36% | 14.732680 | 83.75 |
| gin--cover-client | 4 | mini-deferred | 7.246165 | 7.209120–7.265966 | 0.27% | 15.643945 | 93.53 |
| gin--cover-client | 32 | native | 0.253249 | 0.250254–0.262717 | 1.73% | 0.420659 | 52.08 |
| gin--cover-client | 32 | orchestrion | 6.541288 | 6.516528–6.660464 | 0.88% | 16.820350 | 200.94 |
| gin--cover-client | 32 | sdk | 6.617863 | 6.521504–6.699226 | 1.04% | 16.922844 | 219.23 |
| gin--cover-client | 32 | mini | 6.853520 | 6.732431–6.890794 | 0.87% | 18.301174 | 178.40 |
| gin--cover-client | 32 | mini-deferred | 7.281139 | 7.241229–7.313180 | 0.39% | 19.689764 | 187.24 |
| gin--race-cover-client | 4 | native | 3.415680 | 3.371987–3.544310 | 1.80% | 8.500800 | 243.92 |
| gin--race-cover-client | 32 | native | 3.446807 | 3.413063–3.583871 | 1.79% | 8.572765 | 283.77 |
| chi--race | 4 | native | 27.214190 | 27.209690–27.216559 | 0.01% | 44.707255 | 2958.75 |
| chi--race | 4 | orchestrion | 27.322207 | 27.308320–27.325687 | 0.03% | 46.083466 | 2979.35 |
| chi--race | 4 | sdk | 27.304652 | 27.303556–27.328894 | 0.04% | 45.404279 | 2944.86 |
| chi--race | 4 | mini | 27.302216 | 27.289656–27.320302 | 0.04% | 44.555409 | 3020.15 |
| chi--race | 4 | mini-deferred | 27.333221 | 27.306167–27.340592 | 0.04% | 44.578702 | 3004.79 |
| chi--race | 32 | native | 27.218039 | 27.214792–27.225713 | 0.01% | 225.164169 | 3068.80 |
| chi--race | 32 | orchestrion | 27.324007 | 27.302995–27.329207 | 0.03% | 147.048678 | 3112.38 |
| chi--race | 32 | sdk | 27.318418 | 27.299527–27.322215 | 0.03% | 143.743666 | 3085.54 |
| chi--race | 32 | mini | 27.305109 | 27.287864–27.312639 | 0.03% | 227.140372 | 3084.61 |
| chi--race | 32 | mini-deferred | 27.318877 | 27.314549–27.348793 | 0.06% | 227.704097 | 3081.49 |
| chi--cover | 4 | native | 26.136957 | 26.134604–26.140277 | 0.01% | 3.004046 | 43.00 |
| chi--cover | 4 | orchestrion | 26.229098 | 26.224293–26.236508 | 0.02% | 3.008069 | 84.96 |
| chi--cover | 4 | sdk | 26.231160 | 26.224957–26.253542 | 0.04% | 2.959473 | 78.74 |
| chi--cover | 4 | mini | 26.229944 | 26.225924–26.238728 | 0.02% | 3.519490 | 58.29 |
| chi--cover | 4 | mini-deferred | 26.243247 | 26.234837–26.245355 | 0.01% | 3.532782 | 58.17 |
| chi--cover | 32 | native | 26.140129 | 26.138273–26.144419 | 0.01% | 8.704006 | 88.64 |
| chi--cover | 32 | orchestrion | 26.233960 | 26.228725–26.238064 | 0.01% | 8.804965 | 149.12 |
| chi--cover | 32 | sdk | 26.236293 | 26.231837–26.238085 | 0.01% | 8.867446 | 147.90 |
| chi--cover | 32 | mini | 26.231442 | 26.229743–26.238987 | 0.01% | 10.476382 | 95.81 |
| chi--cover | 32 | mini-deferred | 26.243466 | 26.235743–26.246480 | 0.01% | 10.485787 | 95.96 |
| chi--cover-client | 4 | native | 26.139077 | 26.138678–26.143371 | 0.01% | 3.273207 | 45.12 |
| chi--cover-client | 4 | orchestrion | 26.788571 | 26.783171–26.806840 | 0.03% | 4.292763 | 98.80 |
| chi--cover-client | 4 | sdk | 26.795046 | 26.790915–26.804016 | 0.02% | 4.250981 | 99.52 |
| chi--cover-client | 4 | mini | 26.796430 | 26.789946–26.799152 | 0.01% | 4.845265 | 60.59 |
| chi--cover-client | 4 | mini-deferred | 26.868350 | 26.862813–26.870602 | 0.01% | 4.878566 | 60.30 |
| chi--cover-client | 32 | native | 26.143078 | 26.140160–26.143884 | 0.00% | 12.007906 | 84.12 |
| chi--cover-client | 32 | orchestrion | 26.843831 | 26.825489–26.848656 | 0.03% | 12.536681 | 145.21 |
| chi--cover-client | 32 | sdk | 26.853596 | 26.848795–26.857056 | 0.01% | 12.398900 | 158.67 |
| chi--cover-client | 32 | mini | 26.848687 | 26.830327–26.860377 | 0.05% | 14.506745 | 103.17 |
| chi--cover-client | 32 | mini-deferred | 26.914561 | 26.896222–26.916055 | 0.03% | 14.661549 | 112.73 |
| chi--race-cover-client | 4 | native | 27.227274 | 27.219629–27.232085 | 0.02% | 55.070241 | 2944.42 |
| chi--race-cover-client | 4 | orchestrion | 28.864119 | 28.817819–28.912666 | 0.11% | 59.183012 | 3073.44 |
| chi--race-cover-client | 4 | sdk | 28.872932 | 28.866283–28.915522 | 0.07% | 58.978038 | 3083.43 |
| chi--race-cover-client | 4 | mini | 28.817076 | 28.799664–28.852029 | 0.07% | 58.706481 | 3029.80 |
| chi--race-cover-client | 4 | mini-deferred | 29.002213 | 29.000003–29.012589 | 0.02% | 58.794469 | 3017.00 |
| chi--race-cover-client | 32 | native | 27.233273 | 27.230656–27.235937 | 0.01% | 243.743850 | 2940.62 |
| chi--race-cover-client | 32 | orchestrion | 28.933591 | 28.929454–28.939520 | 0.01% | 189.447900 | 3072.50 |
| chi--race-cover-client | 32 | sdk | 28.957669 | 28.945239–28.983483 | 0.05% | 193.396603 | 3064.31 |
| chi--race-cover-client | 32 | mini | 28.943650 | 28.924669–28.971610 | 0.05% | 255.632777 | 3017.67 |
| chi--race-cover-client | 32 | mini-deferred | 29.125580 | 29.116072–29.155837 | 0.05% | 254.267561 | 3004.95 |
| testify-direct--normal | 4 | native | 0.002768 | 0.002659–0.002871 | 2.53% | 0.003788 | 14.54 |
| testify-direct--normal | 4 | orchestrion | 0.014293 | 0.013946–0.014714 | 1.88% | 0.018899 | 26.62 |
| testify-direct--normal | 4 | sdk | 0.014375 | 0.014206–0.014734 | 1.29% | 0.019443 | 26.96 |
| testify-direct--normal | 4 | mini | 0.009877 | 0.009728–0.010733 | 3.69% | 0.010946 | 17.29 |
| testify-direct--normal | 4 | mini-deferred | 0.010434 | 0.009924–0.010529 | 2.29% | 0.011442 | 17.11 |
| testify-direct--normal | 32 | native | 0.003034 | 0.002834–0.003637 | 8.72% | 0.004573 | 14.98 |
| testify-direct--normal | 32 | orchestrion | 0.016014 | 0.015913–0.016351 | 1.01% | 0.023118 | 29.43 |
| testify-direct--normal | 32 | sdk | 0.015778 | 0.015482–0.016463 | 2.04% | 0.023070 | 29.16 |
| testify-direct--normal | 32 | mini | 0.011020 | 0.010584–0.011488 | 2.62% | 0.012558 | 18.26 |
| testify-direct--normal | 32 | mini-deferred | 0.011532 | 0.010782–0.011913 | 3.28% | 0.012893 | 18.47 |
| testify-direct--race | 4 | native | 1.008293 | 1.008201–1.008790 | 0.02% | 0.009414 | 34.08 |
| testify-direct--race | 4 | orchestrion | 1.041778 | 1.040832–1.041896 | 0.04% | 0.051832 | 67.46 |
| testify-direct--race | 4 | sdk | 1.041911 | 1.041521–1.042658 | 0.04% | 0.055231 | 69.18 |
| testify-direct--race | 4 | mini | 1.021002 | 1.020847–1.021151 | 0.01% | 0.022562 | 41.17 |
| testify-direct--race | 4 | mini-deferred | 1.023241 | 1.022279–1.023537 | 0.05% | 0.024094 | 41.94 |
| testify-direct--race | 32 | native | 1.008927 | 1.008471–1.009004 | 0.02% | 0.010023 | 37.21 |
| testify-direct--race | 32 | orchestrion | 1.045827 | 1.045513–1.046870 | 0.05% | 0.063160 | 73.38 |
| testify-direct--race | 32 | sdk | 1.046037 | 1.045296–1.046502 | 0.04% | 0.062852 | 75.82 |
| testify-direct--race | 32 | mini | 1.024066 | 1.022986–1.024268 | 0.04% | 0.026288 | 43.18 |
| testify-direct--race | 32 | mini-deferred | 1.026128 | 1.024449–1.027724 | 0.11% | 0.030004 | 47.86 |
| testify-direct--cover | 4 | native | 0.003040 | 0.002961–0.003208 | 2.98% | 0.003917 | 14.48 |
| testify-direct--cover | 4 | orchestrion | 0.023040 | 0.022565–0.023689 | 1.79% | 0.026835 | 29.69 |
| testify-direct--cover | 4 | sdk | 0.022887 | 0.022457–0.023643 | 1.70% | 0.027497 | 27.96 |
| testify-direct--cover | 4 | mini | 0.018788 | 0.018451–0.019688 | 2.38% | 0.020218 | 22.16 |
| testify-direct--cover | 4 | mini-deferred | 0.019584 | 0.018999–0.020223 | 2.13% | 0.020606 | 22.32 |
| testify-direct--cover | 32 | native | 0.003225 | 0.003185–0.003667 | 5.53% | 0.004487 | 14.72 |
| testify-direct--cover | 32 | orchestrion | 0.025315 | 0.024757–0.025785 | 1.61% | 0.034084 | 33.78 |
| testify-direct--cover | 32 | sdk | 0.024856 | 0.024614–0.026174 | 2.23% | 0.032235 | 33.84 |
| testify-direct--cover | 32 | mini | 0.020611 | 0.020004–0.021779 | 3.00% | 0.022576 | 24.44 |
| testify-direct--cover | 32 | mini-deferred | 0.020988 | 0.020389–0.022384 | 3.26% | 0.022545 | 25.31 |
| testify-direct--cover-client | 4 | native | 0.003020 | 0.002933–0.003363 | 4.84% | 0.003895 | 14.33 |
| testify-direct--cover-client | 4 | orchestrion | 0.024021 | 0.023243–0.024309 | 1.77% | 0.028469 | 27.64 |
| testify-direct--cover-client | 4 | sdk | 0.023262 | 0.022882–0.024251 | 2.12% | 0.027709 | 27.87 |
| testify-direct--cover-client | 4 | mini | 0.020026 | 0.019770–0.021130 | 2.45% | 0.021250 | 20.42 |
| testify-direct--cover-client | 4 | mini-deferred | 0.020783 | 0.020209–0.021087 | 1.47% | 0.022511 | 20.47 |
| testify-direct--cover-client | 32 | native | 0.003157 | 0.003060–0.003179 | 1.35% | 0.004158 | 14.77 |
| testify-direct--cover-client | 32 | orchestrion | 0.026260 | 0.025792–0.026927 | 1.82% | 0.034280 | 34.04 |
| testify-direct--cover-client | 32 | sdk | 0.025903 | 0.025878–0.026560 | 0.99% | 0.034435 | 34.00 |
| testify-direct--cover-client | 32 | mini | 0.022356 | 0.022148–0.022865 | 1.21% | 0.025944 | 25.40 |
| testify-direct--cover-client | 32 | mini-deferred | 0.023393 | 0.023005–0.024036 | 1.78% | 0.027248 | 24.41 |
| testify-direct--race-cover-client | 4 | native | 1.009339 | 1.008955–1.009570 | 0.02% | 0.010245 | 33.97 |
| testify-direct--race-cover-client | 4 | orchestrion | 1.052694 | 1.051837–1.053167 | 0.04% | 0.061688 | 66.72 |
| testify-direct--race-cover-client | 4 | sdk | 1.052015 | 1.051465–1.052801 | 0.05% | 0.060964 | 66.77 |
| testify-direct--race-cover-client | 4 | mini | 1.033860 | 1.032869–1.035084 | 0.07% | 0.035686 | 44.86 |
| testify-direct--race-cover-client | 4 | mini-deferred | 1.036696 | 1.036023–1.037022 | 0.03% | 0.039392 | 46.55 |
| testify-direct--race-cover-client | 32 | native | 1.009875 | 1.009629–1.010124 | 0.02% | 0.011177 | 35.75 |
| testify-direct--race-cover-client | 32 | orchestrion | 1.057152 | 1.055745–1.061098 | 0.18% | 0.072301 | 72.09 |
| testify-direct--race-cover-client | 32 | sdk | 1.056525 | 1.054949–1.057320 | 0.08% | 0.071111 | 72.62 |
| testify-direct--race-cover-client | 32 | mini | 1.037662 | 1.037126–1.038433 | 0.05% | 0.042803 | 51.58 |
| testify-direct--race-cover-client | 32 | mini-deferred | 1.039850 | 1.039037–1.040281 | 0.04% | 0.044133 | 52.50 |
| testify-direct--cover-testing | 4 | native | 0.003584 | 0.003514–0.003702 | 1.89% | 0.004669 | 14.81 |
| testify-direct--cover-testing | 4 | orchestrion | 0.038847 | 0.038238–0.039334 | 0.90% | 0.045965 | 27.74 |
| testify-direct--cover-testing | 4 | sdk | 0.039359 | 0.038258–0.039430 | 1.13% | 0.046200 | 28.18 |
| testify-direct--cover-testing | 4 | mini | 0.035194 | 0.034664–0.036278 | 1.60% | 0.038721 | 22.22 |
| testify-direct--cover-testing | 4 | mini-deferred | 0.036004 | 0.035984–0.036376 | 0.41% | 0.039909 | 22.29 |
| testify-direct--cover-testing | 32 | native | 0.003901 | 0.003769–0.004127 | 3.16% | 0.005086 | 14.96 |
| testify-direct--cover-testing | 32 | orchestrion | 0.041828 | 0.039988–0.042617 | 2.49% | 0.053131 | 35.20 |
| testify-direct--cover-testing | 32 | sdk | 0.041439 | 0.041101–0.042385 | 1.10% | 0.052759 | 33.56 |
| testify-direct--cover-testing | 32 | mini | 0.038645 | 0.037890–0.039299 | 1.19% | 0.045422 | 25.90 |
| testify-direct--cover-testing | 32 | mini-deferred | 0.039003 | 0.038813–0.039477 | 0.64% | 0.046998 | 25.54 |
| testify-direct--race-cover-testing | 4 | native | 1.011706 | 1.011577–1.011857 | 0.01% | 0.012413 | 35.34 |
| testify-direct--race-cover-testing | 4 | orchestrion | 1.089557 | 1.088832–1.091163 | 0.08% | 0.109470 | 74.43 |
| testify-direct--race-cover-testing | 4 | sdk | 1.089747 | 1.089120–1.090846 | 0.05% | 0.109904 | 74.61 |
| testify-direct--race-cover-testing | 4 | mini | 1.073814 | 1.073295–1.074226 | 0.03% | 0.087452 | 50.00 |
| testify-direct--race-cover-testing | 4 | mini-deferred | 1.075661 | 1.074566–1.075960 | 0.05% | 0.089427 | 49.68 |
| testify-direct--race-cover-testing | 32 | native | 1.012462 | 1.012319–1.012960 | 0.02% | 0.013447 | 36.73 |
| testify-direct--race-cover-testing | 32 | orchestrion | 1.097505 | 1.096087–1.098500 | 0.08% | 0.126504 | 83.53 |
| testify-direct--race-cover-testing | 32 | sdk | 1.095781 | 1.093971–1.097399 | 0.10% | 0.120445 | 82.14 |
| testify-direct--race-cover-testing | 32 | mini | 1.079619 | 1.078223–1.080822 | 0.09% | 0.099291 | 58.10 |
| testify-direct--race-cover-testing | 32 | mini-deferred | 1.081836 | 1.080402–1.083203 | 0.11% | 0.099757 | 56.34 |
| testify-external--normal | 4 | native | 0.002716 | 0.002639–0.002796 | 2.47% | 0.003568 | 14.34 |
| testify-external--normal | 4 | orchestrion | 0.014248 | 0.013731–0.014540 | 1.90% | 0.019312 | 27.12 |
| testify-external--normal | 4 | sdk | 0.014253 | 0.014002–0.014452 | 1.03% | 0.019124 | 26.36 |
| testify-external--normal | 4 | mini | 0.009969 | 0.009547–0.010300 | 2.66% | 0.010805 | 17.29 |
| testify-external--normal | 4 | mini-deferred | 0.010055 | 0.009838–0.010511 | 2.69% | 0.011095 | 17.29 |
| testify-external--normal | 32 | native | 0.002940 | 0.002921–0.003123 | 2.56% | 0.004025 | 14.48 |
| testify-external--normal | 32 | orchestrion | 0.016444 | 0.016203–0.016616 | 1.00% | 0.025720 | 32.32 |
| testify-external--normal | 32 | sdk | 0.015532 | 0.015252–0.016371 | 2.56% | 0.023414 | 29.33 |
| testify-external--normal | 32 | mini | 0.011040 | 0.010792–0.011342 | 1.91% | 0.012645 | 18.31 |
| testify-external--normal | 32 | mini-deferred | 0.011582 | 0.011295–0.011870 | 1.64% | 0.012783 | 18.77 |
| testify-external--race | 4 | native | 1.008352 | 1.008251–1.008555 | 0.01% | 0.009517 | 33.83 |
| testify-external--race | 4 | orchestrion | 1.041991 | 1.041745–1.042499 | 0.03% | 0.055411 | 68.84 |
| testify-external--race | 4 | sdk | 1.042802 | 1.042221–1.042971 | 0.02% | 0.055650 | 69.46 |
| testify-external--race | 4 | mini | 1.022056 | 1.021716–1.022331 | 0.02% | 0.023667 | 41.20 |
| testify-external--race | 4 | mini-deferred | 1.023050 | 1.022622–1.023642 | 0.04% | 0.024326 | 41.45 |
| testify-external--race | 32 | native | 1.009078 | 1.008949–1.009489 | 0.02% | 0.010453 | 36.97 |
| testify-external--race | 32 | orchestrion | 1.046242 | 1.045924–1.046899 | 0.03% | 0.064850 | 75.32 |
| testify-external--race | 32 | sdk | 1.046637 | 1.044925–1.046957 | 0.07% | 0.066655 | 75.58 |
| testify-external--race | 32 | mini | 1.023915 | 1.023371–1.024168 | 0.03% | 0.025689 | 43.72 |
| testify-external--race | 32 | mini-deferred | 1.025231 | 1.023807–1.026174 | 0.08% | 0.026569 | 43.36 |
| testify-external--cover | 4 | native | 0.002880 | 0.002857–0.003041 | 2.80% | 0.003944 | 14.23 |
| testify-external--cover | 4 | orchestrion | 0.023010 | 0.022500–0.023789 | 1.81% | 0.027232 | 29.42 |
| testify-external--cover | 4 | sdk | 0.023562 | 0.022216–0.023593 | 2.28% | 0.027470 | 29.68 |
| testify-external--cover | 4 | mini | 0.018956 | 0.018741–0.019893 | 2.11% | 0.020294 | 22.20 |
| testify-external--cover | 4 | mini-deferred | 0.019208 | 0.019148–0.019582 | 0.86% | 0.020609 | 22.41 |
| testify-external--cover | 32 | native | 0.003031 | 0.002962–0.003418 | 5.22% | 0.004082 | 15.02 |
| testify-external--cover | 32 | orchestrion | 0.025533 | 0.025143–0.025954 | 1.26% | 0.032932 | 34.40 |
| testify-external--cover | 32 | sdk | 0.025173 | 0.024562–0.026050 | 2.27% | 0.032541 | 32.29 |
| testify-external--cover | 32 | mini | 0.020540 | 0.020035–0.021013 | 1.92% | 0.022795 | 25.23 |
| testify-external--cover | 32 | mini-deferred | 0.021280 | 0.020099–0.021437 | 2.91% | 0.022833 | 25.05 |
| testify-external--cover-client | 4 | native | 0.003078 | 0.003060–0.003356 | 3.51% | 0.004318 | 14.33 |
| testify-external--cover-client | 4 | orchestrion | 0.023386 | 0.022280–0.024013 | 2.54% | 0.027525 | 28.16 |
| testify-external--cover-client | 4 | sdk | 0.024714 | 0.023672–0.027718 | 5.66% | 0.029645 | 27.87 |
| testify-external--cover-client | 4 | mini | 0.020176 | 0.019611–0.020224 | 1.14% | 0.021305 | 20.50 |
| testify-external--cover-client | 4 | mini-deferred | 0.020872 | 0.020719–0.024693 | 7.15% | 0.022610 | 21.96 |
| testify-external--cover-client | 32 | native | 0.003307 | 0.003124–0.003462 | 3.51% | 0.004624 | 15.01 |
| testify-external--cover-client | 32 | orchestrion | 0.025663 | 0.025281–0.026668 | 1.81% | 0.033521 | 34.38 |
| testify-external--cover-client | 32 | sdk | 0.025959 | 0.025629–0.026537 | 1.31% | 0.034478 | 34.05 |
| testify-external--cover-client | 32 | mini | 0.022701 | 0.021883–0.024025 | 3.03% | 0.027106 | 24.08 |
| testify-external--cover-client | 32 | mini-deferred | 0.022932 | 0.022815–0.023800 | 1.56% | 0.026943 | 25.01 |
| testify-external--race-cover-client | 4 | native | 1.009322 | 1.008989–1.009474 | 0.02% | 0.010397 | 32.80 |
| testify-external--race-cover-client | 4 | orchestrion | 1.053227 | 1.052705–1.054280 | 0.06% | 0.061959 | 67.10 |
| testify-external--race-cover-client | 4 | sdk | 1.053197 | 1.052257–1.053986 | 0.06% | 0.062476 | 67.01 |
| testify-external--race-cover-client | 4 | mini | 1.033920 | 1.033379–1.034471 | 0.04% | 0.035651 | 45.68 |
| testify-external--race-cover-client | 4 | mini-deferred | 1.036165 | 1.035391–1.037535 | 0.07% | 0.038960 | 46.91 |
| testify-external--race-cover-client | 32 | native | 1.009809 | 1.009439–1.010221 | 0.03% | 0.010980 | 35.51 |
| testify-external--race-cover-client | 32 | orchestrion | 1.058201 | 1.056507–1.059143 | 0.08% | 0.072888 | 73.07 |
| testify-external--race-cover-client | 32 | sdk | 1.057683 | 1.056282–1.057900 | 0.06% | 0.071439 | 71.84 |
| testify-external--race-cover-client | 32 | mini | 1.038404 | 1.037158–1.038980 | 0.06% | 0.043914 | 52.55 |
| testify-external--race-cover-client | 32 | mini-deferred | 1.039951 | 1.038743–1.040406 | 0.06% | 0.045213 | 51.38 |
| testify-external--cover-testing | 4 | native | 0.003702 | 0.003598–0.003749 | 1.50% | 0.004493 | 14.59 |
| testify-external--cover-testing | 4 | orchestrion | 0.039288 | 0.037583–0.039342 | 1.74% | 0.045974 | 28.01 |
| testify-external--cover-testing | 4 | sdk | 0.038986 | 0.038754–0.039621 | 0.82% | 0.045566 | 29.02 |
| testify-external--cover-testing | 4 | mini | 0.035703 | 0.035105–0.035857 | 0.94% | 0.039363 | 22.79 |
| testify-external--cover-testing | 4 | mini-deferred | 0.036444 | 0.035946–0.036608 | 0.72% | 0.039987 | 22.18 |
| testify-external--cover-testing | 32 | native | 0.004002 | 0.003766–0.004048 | 2.64% | 0.005011 | 16.75 |
| testify-external--cover-testing | 32 | orchestrion | 0.042201 | 0.041555–0.042875 | 1.22% | 0.054885 | 34.48 |
| testify-external--cover-testing | 32 | sdk | 0.042303 | 0.041320–0.043213 | 1.65% | 0.055006 | 34.84 |
| testify-external--cover-testing | 32 | mini | 0.038654 | 0.036623–0.038951 | 2.26% | 0.045757 | 25.54 |
| testify-external--cover-testing | 32 | mini-deferred | 0.039151 | 0.038820–0.040637 | 1.69% | 0.048355 | 25.85 |
| testify-external--race-cover-testing | 4 | native | 1.012068 | 1.011585–1.012211 | 0.02% | 0.012663 | 35.28 |
| testify-external--race-cover-testing | 4 | orchestrion | 1.090319 | 1.088727–1.091354 | 0.09% | 0.110854 | 74.31 |
| testify-external--race-cover-testing | 4 | sdk | 1.091829 | 1.090302–1.092218 | 0.08% | 0.112610 | 74.59 |
| testify-external--race-cover-testing | 4 | mini | 1.073767 | 1.073305–1.076029 | 0.09% | 0.088045 | 49.72 |
| testify-external--race-cover-testing | 4 | mini-deferred | 1.076341 | 1.075587–1.076512 | 0.04% | 0.090053 | 49.61 |
| testify-external--race-cover-testing | 32 | native | 1.012989 | 1.012573–1.013907 | 0.05% | 0.013790 | 36.27 |
| testify-external--race-cover-testing | 32 | orchestrion | 1.098761 | 1.096127–1.102928 | 0.20% | 0.126615 | 73.03 |
| testify-external--race-cover-testing | 32 | sdk | 1.097942 | 1.095962–1.101184 | 0.16% | 0.126941 | 78.79 |
| testify-external--race-cover-testing | 32 | mini | 1.079703 | 1.078079–1.084329 | 0.19% | 0.097527 | 56.07 |
| testify-external--race-cover-testing | 32 | mini-deferred | 1.083401 | 1.080649–1.085247 | 0.14% | 0.103183 | 55.17 |

## Failures and incomplete combinations

- gin--race/4/orchestrion: 5 failed observations: race detector reported an error
- gin--race/4/sdk: 6 failed observations: race detector reported an error
- gin--race/32/orchestrion: 6 failed observations: race detector reported an error
- gin--race/32/sdk: 6 failed observations: race detector reported an error
- gin--race-cover-client/4/orchestrion: 5 failed observations: SDK reference unavailable for this failed-oracle combination; race detector reported an error
- gin--race-cover-client/4/sdk: 6 failed observations: race detector reported an error
- gin--race-cover-client/4/mini: 6 failed observations: race detector reported an error
- gin--race-cover-client/4/mini-deferred: 3 failed observations: SDK reference unavailable for this failed-oracle combination; race detector reported an error
- gin--race-cover-client/32/orchestrion: 5 failed observations: SDK reference unavailable for this failed-oracle combination; race detector reported an error
- gin--race-cover-client/32/sdk: 6 failed observations: race detector reported an error
- gin--race-cover-client/32/mini: 2 failed observations: SDK reference unavailable for this failed-oracle combination; race detector reported an error
- gin--race-cover-client/32/mini-deferred: 4 failed observations: SDK reference unavailable for this failed-oracle combination; race detector reported an error

48/48 cells attempted with all original repetitions; 44/48 cells pass every variant and repetition.
192 original set-mode harness notices revalidated against the pinned SDK and Native; no test or input changed.
This is Linux/amd64 loopback evidence. No live Datadog intake is measured. Chi has long built-in sleeps; race binaries include the Go race shutdown delay.

## Execution and delivered event counts

Only variants with all six attempts verified appear below. Failed and unverified attempts remain in the raw ledger.
Actual results include examples; CI test event counts follow the pinned SDK behavior.
The CI count column contains test, suite, module, session and additional span events by their wire type.
[All per-run durations and validation outcomes](observations.csv).

| Case | CPUs | Variant | Actual test results | CI event counts | Per-test coverage items | Event gzip bytes median | Event uncompressed bytes median |
| --- | ---: | --- | ---: | --- | --- | ---: | ---: |
| gin | 4 | native | 699 | `{}` | [0] | 0 | 0 |
| gin | 4 | orchestrion | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 131885 | 1668838 |
| gin | 4 | sdk | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 130597 | 1663139 |
| gin | 4 | mini | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 111902 | 1028119 |
| gin | 4 | mini-deferred | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 671138 | 1380118 |
| gin | 32 | native | 699 | `{}` | [0] | 0 | 0 |
| gin | 32 | orchestrion | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 129339 | 1668914 |
| gin | 32 | sdk | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 130520 | 1662863 |
| gin | 32 | mini | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 111958 | 1028885 |
| gin | 32 | mini-deferred | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 672052 | 1381094 |
| chi | 4 | native | 270 | `{}` | [0] | 0 | 0 |
| chi | 4 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 57300 | 671632 |
| chi | 4 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 57334 | 669255 |
| chi | 4 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 44944 | 416038 |
| chi | 4 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 79511 | 437767 |
| chi | 32 | native | 270 | `{}` | [0] | 0 | 0 |
| chi | 32 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 56532 | 671637 |
| chi | 32 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 56551 | 669364 |
| chi | 32 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 44820 | 416342 |
| chi | 32 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 79576 | 438081 |
| gin--race | 4 | native | 699 | `{}` | [0] | 0 | 0 |
| gin--race | 4 | mini | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 111540 | 1028943 |
| gin--race | 4 | mini-deferred | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 673460 | 1380810 |
| gin--race | 32 | native | 699 | `{}` | [0] | 0 | 0 |
| gin--race | 32 | mini | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 111719 | 1029697 |
| gin--race | 32 | mini-deferred | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 672768 | 1381564 |
| gin--cover | 4 | native | 699 | `{}` | [0] | 0 | 0 |
| gin--cover | 4 | orchestrion | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 127455 | 1667572 |
| gin--cover | 4 | sdk | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 127547 | 1661877 |
| gin--cover | 4 | mini | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 112082 | 1028399 |
| gin--cover | 4 | mini-deferred | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 671053 | 1380358 |
| gin--cover | 32 | native | 699 | `{}` | [0] | 0 | 0 |
| gin--cover | 32 | orchestrion | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 126596 | 1668036 |
| gin--cover | 32 | sdk | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 127209 | 1661467 |
| gin--cover | 32 | mini | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 111940 | 1029171 |
| gin--cover | 32 | mini-deferred | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [0] | 672958 | 1381350 |
| gin--cover-client | 4 | native | 699 | `{}` | [0] | 0 | 0 |
| gin--cover-client | 4 | orchestrion | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [612] | 131719 | 1668901 |
| gin--cover-client | 4 | sdk | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [612] | 131311 | 1662524 |
| gin--cover-client | 4 | mini | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [612] | 112970 | 1029031 |
| gin--cover-client | 4 | mini-deferred | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [612] | 672316 | 1380896 |
| gin--cover-client | 32 | native | 699 | `{}` | [0] | 0 | 0 |
| gin--cover-client | 32 | orchestrion | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [612] | 131574 | 1669509 |
| gin--cover-client | 32 | sdk | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [612] | 131610 | 1663390 |
| gin--cover-client | 32 | mini | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [612] | 112891 | 1029789 |
| gin--cover-client | 32 | mini-deferred | 699 | `{"test": 699, "test_module_end": 6, "test_session_end": 6, "test_suite_end": 37}` | [612] | 672849 | 1381654 |
| gin--race-cover-client | 4 | native | 699 | `{}` | [0] | 0 | 0 |
| gin--race-cover-client | 32 | native | 699 | `{}` | [0] | 0 | 0 |
| chi--race | 4 | native | 270 | `{}` | [0] | 0 | 0 |
| chi--race | 4 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 61055 | 672532 |
| chi--race | 4 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 60152 | 670133 |
| chi--race | 4 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 44854 | 416316 |
| chi--race | 4 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 79872 | 438041 |
| chi--race | 32 | native | 270 | `{}` | [0] | 0 | 0 |
| chi--race | 32 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 60253 | 672651 |
| chi--race | 32 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 60403 | 670439 |
| chi--race | 32 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 44942 | 416612 |
| chi--race | 32 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 79829 | 438337 |
| chi--cover | 4 | native | 270 | `{}` | [0] | 0 | 0 |
| chi--cover | 4 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 57369 | 671506 |
| chi--cover | 4 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 56503 | 669021 |
| chi--cover | 4 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 44890 | 416110 |
| chi--cover | 4 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 79668 | 437845 |
| chi--cover | 32 | native | 270 | `{}` | [0] | 0 | 0 |
| chi--cover | 32 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 56451 | 671552 |
| chi--cover | 32 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 57393 | 669719 |
| chi--cover | 32 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 44978 | 416422 |
| chi--cover | 32 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [0] | 79810 | 438163 |
| chi--cover-client | 4 | native | 270 | `{}` | [0] | 0 | 0 |
| chi--cover-client | 4 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 57286 | 671604 |
| chi--cover-client | 4 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 57344 | 669387 |
| chi--cover-client | 4 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 44761 | 416136 |
| chi--cover-client | 4 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 153242 | 486793 |
| chi--cover-client | 32 | native | 270 | `{}` | [0] | 0 | 0 |
| chi--cover-client | 32 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 57453 | 672062 |
| chi--cover-client | 32 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 56483 | 669407 |
| chi--cover-client | 32 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 44785 | 416456 |
| chi--cover-client | 32 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 153504 | 487099 |
| chi--race-cover-client | 4 | native | 270 | `{}` | [0] | 0 | 0 |
| chi--race-cover-client | 4 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 56839 | 671536 |
| chi--race-cover-client | 4 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 57594 | 669346 |
| chi--race-cover-client | 4 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 44919 | 416356 |
| chi--race-cover-client | 4 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 153818 | 487011 |
| chi--race-cover-client | 32 | native | 270 | `{}` | [0] | 0 | 0 |
| chi--race-cover-client | 32 | orchestrion | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 57776 | 672006 |
| chi--race-cover-client | 32 | sdk | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 57750 | 669817 |
| chi--race-cover-client | 32 | mini | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 45163 | 416652 |
| chi--race-cover-client | 32 | mini-deferred | 270 | `{"test": 268, "test_module_end": 2, "test_session_end": 2, "test_suite_end": 22}` | [117] | 153572 | 487307 |
| testify-direct--normal | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--normal | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3124 | 13283 |
| testify-direct--normal | 4 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3128 | 13235 |
| testify-direct--normal | 4 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1830 | 8827 |
| testify-direct--normal | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2563 | 9132 |
| testify-direct--normal | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--normal | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3131 | 13290 |
| testify-direct--normal | 32 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3130 | 13242 |
| testify-direct--normal | 32 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1838 | 8834 |
| testify-direct--normal | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2588 | 9139 |
| testify-direct--race | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--race | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3119 | 13283 |
| testify-direct--race | 4 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 4056 | 13540 |
| testify-direct--race | 4 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1840 | 8827 |
| testify-direct--race | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2596 | 9132 |
| testify-direct--race | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--race | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3049 | 13288 |
| testify-direct--race | 32 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3879 | 13539 |
| testify-direct--race | 32 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1839 | 8834 |
| testify-direct--race | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2593 | 9139 |
| testify-direct--cover | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--cover | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2090 | 13016 |
| testify-direct--cover | 4 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2086 | 12966 |
| testify-direct--cover | 4 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1882 | 8865 |
| testify-direct--cover | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2618 | 9170 |
| testify-direct--cover | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--cover | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2104 | 13023 |
| testify-direct--cover | 32 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2082 | 12975 |
| testify-direct--cover | 32 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1869 | 8872 |
| testify-direct--cover | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2618 | 9177 |
| testify-direct--cover-client | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--cover-client | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2093 | 13016 |
| testify-direct--cover-client | 4 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2085 | 12968 |
| testify-direct--cover-client | 4 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1865 | 8865 |
| testify-direct--cover-client | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2610 | 9170 |
| testify-direct--cover-client | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--cover-client | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2089 | 13023 |
| testify-direct--cover-client | 32 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2089 | 12975 |
| testify-direct--cover-client | 32 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1839 | 8872 |
| testify-direct--cover-client | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2604 | 9177 |
| testify-direct--race-cover-client | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--race-cover-client | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2087 | 13016 |
| testify-direct--race-cover-client | 4 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2099 | 12968 |
| testify-direct--race-cover-client | 4 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1856 | 8865 |
| testify-direct--race-cover-client | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2604 | 9170 |
| testify-direct--race-cover-client | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--race-cover-client | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2102 | 13023 |
| testify-direct--race-cover-client | 32 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2102 | 12973 |
| testify-direct--race-cover-client | 32 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1875 | 8872 |
| testify-direct--race-cover-client | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2610 | 9177 |
| testify-direct--cover-testing | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--cover-testing | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2094 | 13016 |
| testify-direct--cover-testing | 4 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2082 | 12968 |
| testify-direct--cover-testing | 4 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1858 | 8865 |
| testify-direct--cover-testing | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2592 | 9170 |
| testify-direct--cover-testing | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--cover-testing | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2108 | 13021 |
| testify-direct--cover-testing | 32 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2096 | 12975 |
| testify-direct--cover-testing | 32 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1875 | 8872 |
| testify-direct--cover-testing | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2618 | 9177 |
| testify-direct--race-cover-testing | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--race-cover-testing | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2112 | 13016 |
| testify-direct--race-cover-testing | 4 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2098 | 12968 |
| testify-direct--race-cover-testing | 4 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1859 | 8865 |
| testify-direct--race-cover-testing | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2647 | 9170 |
| testify-direct--race-cover-testing | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-direct--race-cover-testing | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2119 | 13023 |
| testify-direct--race-cover-testing | 32 | sdk | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2101 | 12975 |
| testify-direct--race-cover-testing | 32 | mini | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1882 | 8872 |
| testify-direct--race-cover-testing | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 1, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2624 | 9177 |
| testify-external--normal | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--normal | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3302 | 15228 |
| testify-external--normal | 4 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3295 | 15172 |
| testify-external--normal | 4 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1969 | 9895 |
| testify-external--normal | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2747 | 10200 |
| testify-external--normal | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--normal | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3321 | 15233 |
| testify-external--normal | 32 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 3293 | 15180 |
| testify-external--normal | 32 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1984 | 9903 |
| testify-external--normal | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2751 | 10208 |
| testify-external--race | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--race | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 4224 | 15533 |
| testify-external--race | 4 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 4245 | 15477 |
| testify-external--race | 4 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1981 | 9895 |
| testify-external--race | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2738 | 10200 |
| testify-external--race | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--race | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 4241 | 15541 |
| testify-external--race | 32 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 4210 | 15485 |
| testify-external--race | 32 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1995 | 9903 |
| testify-external--race | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2737 | 10208 |
| testify-external--cover | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--cover | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2265 | 14957 |
| testify-external--cover | 4 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2252 | 14905 |
| testify-external--cover | 4 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 1991 | 9933 |
| testify-external--cover | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2753 | 10238 |
| testify-external--cover | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--cover | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2266 | 14962 |
| testify-external--cover | 32 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2256 | 14911 |
| testify-external--cover | 32 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2020 | 9941 |
| testify-external--cover | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [0] | 2761 | 10246 |
| testify-external--cover-client | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--cover-client | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2252 | 14956 |
| testify-external--cover-client | 4 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2256 | 14905 |
| testify-external--cover-client | 4 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1999 | 9933 |
| testify-external--cover-client | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2765 | 10238 |
| testify-external--cover-client | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--cover-client | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2273 | 14969 |
| testify-external--cover-client | 32 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2275 | 14910 |
| testify-external--cover-client | 32 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 1999 | 9941 |
| testify-external--cover-client | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2763 | 10246 |
| testify-external--race-cover-client | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--race-cover-client | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2263 | 14961 |
| testify-external--race-cover-client | 4 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2269 | 14902 |
| testify-external--race-cover-client | 4 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2021 | 9933 |
| testify-external--race-cover-client | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2750 | 10238 |
| testify-external--race-cover-client | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--race-cover-client | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2283 | 14969 |
| testify-external--race-cover-client | 32 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2272 | 14913 |
| testify-external--race-cover-client | 32 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2030 | 9941 |
| testify-external--race-cover-client | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2758 | 10246 |
| testify-external--cover-testing | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--cover-testing | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2274 | 14959 |
| testify-external--cover-testing | 4 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2261 | 14905 |
| testify-external--cover-testing | 4 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2028 | 9933 |
| testify-external--cover-testing | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2764 | 10238 |
| testify-external--cover-testing | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--cover-testing | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2282 | 14969 |
| testify-external--cover-testing | 32 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2270 | 14913 |
| testify-external--cover-testing | 32 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2015 | 9941 |
| testify-external--cover-testing | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2739 | 10246 |
| testify-external--race-cover-testing | 4 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--race-cover-testing | 4 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2286 | 14959 |
| testify-external--race-cover-testing | 4 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2295 | 14902 |
| testify-external--race-cover-testing | 4 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2034 | 9933 |
| testify-external--race-cover-testing | 4 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2757 | 10238 |
| testify-external--race-cover-testing | 32 | native | 2 | `{}` | [0] | 0 | 0 |
| testify-external--race-cover-testing | 32 | orchestrion | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2295 | 14969 |
| testify-external--race-cover-testing | 32 | sdk | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2287 | 14913 |
| testify-external--race-cover-testing | 32 | mini | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2017 | 9941 |
| testify-external--race-cover-testing | 32 | mini-deferred | 2 | `{"test": 2, "test_module_end": 2, "test_session_end": 1, "test_suite_end": 2}` | [1] | 2783 | 10246 |
