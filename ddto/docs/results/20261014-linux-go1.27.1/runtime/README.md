# Runtime comparisons

Five measured executions follow one warmup at each CPU count. All package binaries
come from the original subject sources, without the build-edit markers.
Timings include startup, concurrent tests, settings and final delivery to loopback HTTP.
Receiver setup, decoding and compilation are outside the timed scope.

Native and Orchestrion observations retain their original dates. Mini and Mini deferred
use the new source revision. Test inventories match Native; CI inventories use the
recorded SDK reference. Native Examples absent from that reference are checked against
the executable function/source table and added explicitly.

Orchestrion's percentage compares time against Native. Mini's percentages compare
Orchestrion first, then Native. Positive values mean more time; negative values mean less.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.183472 s | 0.227665 s (+24.1%) | 0.224209 s (-1.5%; +22.2%) | 0.224119 s (-1.6%; +22.2%) |
| Gin | `none` | 32 | 0.192854 s | 0.235763 s (+22.2%) | 0.218374 s (-7.4%; +13.2%) | 0.219304 s (-7.0%; +13.7%) |
| Chi | `none` | 4 | 26.136864 s | 26.174048 s (+0.1%) | 26.163049 s (-0.0%; +0.1%) | 26.160689 s (-0.1%; +0.1%) |
| Chi | `none` | 32 | 26.141335 s | 26.179190 s (+0.1%) | 26.168752 s (-0.0%; +0.1%) | 26.165971 s (-0.1%; +0.1%) |
| Gin | `-race` | 4 | 2.040156 s | FAIL 5/6 | 2.548710 s (vs Orchestrion unavailable; +24.9% vs Native) | 2.552877 s (vs Orchestrion unavailable; +25.1% vs Native) |
| Gin | `-race` | 32 | 1.415086 s | FAIL 6/6 | 1.555953 s (vs Orchestrion unavailable; +10.0% vs Native) | 1.550647 s (vs Orchestrion unavailable; +9.6% vs Native) |
| Gin | `-cover` | 4 | 0.188422 s | 0.484403 s (+157.1%) | 0.233435 s (-51.8%; +23.9%) | 0.237227 s (-51.0%; +25.9%) |
| Gin | `-cover` | 32 | 0.198064 s | 0.410552 s (+107.3%) | 0.232722 s (-43.3%; +17.5%) | 0.237104 s (-42.2%; +19.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.253092 s | 6.616408 s (+2514.2%) | 0.561075 s (-91.5%; +121.7%) | 0.565073 s (-91.5%; +123.3%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.253249 s | 6.541288 s (+2482.9%) | 0.487369 s (-92.5%; +92.4%) | 0.500610 s (-92.3%; +97.7%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 3.415680 s | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 3.446807 s | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 27.214190 s | 27.322207 s (+0.4%) | 27.280423 s (-0.2%; +0.2%) | 27.282419 s (-0.1%; +0.3%) |
| Chi | `-race` | 32 | 27.218039 s | 27.324007 s (+0.4%) | 27.293276 s (-0.1%; +0.3%) | 27.292168 s (-0.1%; +0.3%) |
| Chi | `-cover` | 4 | 26.136957 s | 26.229098 s (+0.4%) | 26.160834 s (-0.3%; +0.1%) | 26.164155 s (-0.2%; +0.1%) |
| Chi | `-cover` | 32 | 26.140129 s | 26.233960 s (+0.4%) | 26.169136 s (-0.2%; +0.1%) | 26.169738 s (-0.2%; +0.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 26.139077 s | 26.788571 s (+2.5%) | 26.244140 s (-2.0%; +0.4%) | 26.245785 s (-2.0%; +0.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 26.143078 s | 26.843831 s (+2.7%) | 26.238254 s (-2.3%; +0.4%) | 26.241371 s (-2.2%; +0.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 27.227274 s | 28.864119 s (+6.0%) | 27.403622 s (-5.1%; +0.6%) | 27.398130 s (-5.1%; +0.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 27.233273 s | 28.933591 s (+6.2%) | 27.411815 s (-5.3%; +0.7%) | 27.407331 s (-5.3%; +0.6%) |
| Testify Direct | `none` | 4 | 0.002768 s | 0.014293 s (+416.4%) | 0.010316 s (-27.8%; +272.7%) | 0.010287 s (-28.0%; +271.7%) |
| Testify Direct | `none` | 32 | 0.003034 s | 0.016014 s (+427.8%) | 0.011607 s (-27.5%; +282.5%) | 0.011626 s (-27.4%; +283.2%) |
| Testify Direct | `-race` | 4 | 1.008293 s | 1.041778 s (+3.3%) | 1.026020 s (-1.5%; +1.8%) | 1.025599 s (-1.6%; +1.7%) |
| Testify Direct | `-race` | 32 | 1.008927 s | 1.045827 s (+3.7%) | 1.027508 s (-1.8%; +1.8%) | 1.028021 s (-1.7%; +1.9%) |
| Testify Direct | `-cover` | 4 | 0.003040 s | 0.023040 s (+658.0%) | 0.011266 s (-51.1%; +270.6%) | 0.011262 s (-51.1%; +270.5%) |
| Testify Direct | `-cover` | 32 | 0.003225 s | 0.025315 s (+685.0%) | 0.012213 s (-51.8%; +278.7%) | 0.012225 s (-51.7%; +279.1%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.003020 s | 0.024021 s (+695.3%) | 0.020745 s (-13.6%; +586.9%) | 0.020971 s (-12.7%; +594.3%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.003157 s | 0.026260 s (+731.7%) | 0.022974 s (-12.5%; +627.6%) | 0.022947 s (-12.6%; +626.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009339 s | 1.052694 s (+4.3%) | 1.037093 s (-1.5%; +2.7%) | 1.036695 s (-1.5%; +2.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009875 s | 1.057152 s (+4.7%) | 1.042235 s (-1.4%; +3.2%) | 1.041333 s (-1.5%; +3.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003584 s | 0.038847 s (+984.0%) | 0.037935 s (-2.3%; +958.6%) | 0.038452 s (-1.0%; +973.0%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.003901 s | 0.041828 s (+972.2%) | 0.040687 s (-2.7%; +942.9%) | 0.041921 s (+0.2%; +974.5%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.011706 s | 1.089557 s (+7.7%) | 1.078333 s (-1.0%; +6.6%) | 1.081612 s (-0.7%; +6.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012462 s | 1.097505 s (+8.4%) | 1.085835 s (-1.1%; +7.2%) | 1.088139 s (-0.9%; +7.5%) |
| Testify External | `none` | 4 | 0.002716 s | 0.014248 s (+424.6%) | 0.010397 s (-27.0%; +282.8%) | 0.010297 s (-27.7%; +279.1%) |
| Testify External | `none` | 32 | 0.002940 s | 0.016444 s (+459.4%) | 0.011686 s (-28.9%; +297.5%) | 0.012089 s (-26.5%; +311.2%) |
| Testify External | `-race` | 4 | 1.008352 s | 1.041991 s (+3.3%) | 1.025633 s (-1.6%; +1.7%) | 1.025437 s (-1.6%; +1.7%) |
| Testify External | `-race` | 32 | 1.009078 s | 1.046242 s (+3.7%) | 1.027293 s (-1.8%; +1.8%) | 1.027613 s (-1.8%; +1.8%) |
| Testify External | `-cover` | 4 | 0.002880 s | 0.023010 s (+699.1%) | 0.011026 s (-52.1%; +282.9%) | 0.011211 s (-51.3%; +289.3%) |
| Testify External | `-cover` | 32 | 0.003031 s | 0.025533 s (+742.3%) | 0.012056 s (-52.8%; +297.7%) | 0.012354 s (-51.6%; +307.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.003078 s | 0.023386 s (+659.8%) | 0.021202 s (-9.3%; +588.8%) | 0.021519 s (-8.0%; +599.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.003307 s | 0.025663 s (+676.0%) | 0.023707 s (-7.6%; +616.8%) | 0.023253 s (-9.4%; +603.1%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009322 s | 1.053227 s (+4.4%) | 1.037943 s (-1.5%; +2.8%) | 1.037827 s (-1.5%; +2.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009809 s | 1.058201 s (+4.8%) | 1.041939 s (-1.5%; +3.2%) | 1.042157 s (-1.5%; +3.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003702 s | 0.039288 s (+961.3%) | 0.037996 s (-3.3%; +926.4%) | 0.038586 s (-1.8%; +942.4%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.004002 s | 0.042201 s (+954.5%) | 0.039977 s (-5.3%; +898.9%) | 0.041015 s (-2.8%; +924.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.012068 s | 1.090319 s (+7.7%) | 1.078481 s (-1.1%; +6.6%) | 1.080881 s (-0.9%; +6.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012989 s | 1.098761 s (+8.5%) | 1.085626 s (-1.2%; +7.2%) | 1.088437 s (-0.9%; +7.4%) |

## CPU, memory and dispersion

| Case | CPUs | Variant | Median (s) | Min-max (s) | CPU median (CPU-s) | Memory median (MiB) | Validation |
| --- | ---: | --- | ---: | --- | ---: | ---: | --- |
| gin | 4 | native | 0.183472 | 0.178205-0.194327 | 0.170003 | 36.64 | verified |
| gin | 4 | orchestrion | 0.227665 | 0.219127-0.241448 | 0.348156 | 60.29 | verified |
| gin | 4 | mini | 0.224209 | 0.221934-0.229843 | 0.275137 | 39.89 | verified |
| gin | 4 | mini-deferred | 0.224119 | 0.219779-0.229503 | 0.277260 | 41.47 | verified |
| gin | 32 | native | 0.192854 | 0.190069-0.194346 | 0.217764 | 42.18 | verified |
| gin | 32 | orchestrion | 0.235763 | 0.230035-0.256558 | 0.439478 | 70.67 | verified |
| gin | 32 | mini | 0.218374 | 0.217932-0.226677 | 0.348167 | 52.21 | verified |
| gin | 32 | mini-deferred | 0.219304 | 0.214182-0.222238 | 0.348488 | 52.83 | verified |
| chi | 4 | native | 26.136864 | 26.135458-26.150430 | 2.901706 | 45.64 | verified |
| chi | 4 | orchestrion | 26.174048 | 26.152724-26.184653 | 2.682397 | 79.65 | verified |
| chi | 4 | mini | 26.163049 | 26.158541-26.171587 | 3.394675 | 33.16 | verified |
| chi | 4 | mini-deferred | 26.160689 | 26.158122-26.173473 | 3.368864 | 33.46 | verified |
| chi | 32 | native | 26.141335 | 26.139699-26.141874 | 7.661383 | 94.73 | verified |
| chi | 32 | orchestrion | 26.179190 | 26.160035-26.183551 | 7.197552 | 128.05 | verified |
| chi | 32 | mini | 26.168752 | 26.160635-26.175572 | 9.799380 | 60.04 | verified |
| chi | 32 | mini-deferred | 26.165971 | 26.160419-26.166630 | 9.984294 | 55.69 | verified |
| gin--race | 4 | native | 2.040156 | 2.034008-2.041493 | 2.627885 | 199.33 | verified |
| gin--race | 4 | orchestrion | 2.139835 | 2.131945-2.151618 | 3.691318 | 332.23 | failed |
| gin--race | 4 | mini | 2.548710 | 2.547289-2.557986 | 3.036144 | 184.71 | verified |
| gin--race | 4 | mini-deferred | 2.552877 | 2.545253-2.555804 | 3.034824 | 187.53 | verified |
| gin--race | 32 | native | 1.415086 | 1.407846-1.426243 | 2.725913 | 262.89 | verified |
| gin--race | 32 | orchestrion | 1.731117 | 1.724280-1.740767 | 3.893175 | 418.80 | failed |
| gin--race | 32 | mini | 1.555953 | 1.550772-1.561246 | 3.166268 | 330.12 | verified |
| gin--race | 32 | mini-deferred | 1.550647 | 1.548454-1.554886 | 3.162465 | 328.75 | verified |
| gin--cover | 4 | native | 0.188422 | 0.186399-0.190422 | 0.192585 | 35.87 | verified |
| gin--cover | 4 | orchestrion | 0.484403 | 0.471239-0.497941 | 1.289090 | 92.60 | verified |
| gin--cover | 4 | mini | 0.233435 | 0.231560-0.240010 | 0.320100 | 39.36 | verified |
| gin--cover | 4 | mini-deferred | 0.237227 | 0.230467-0.245401 | 0.322703 | 42.57 | verified |
| gin--cover | 32 | native | 0.198064 | 0.194113-0.219861 | 0.238151 | 42.11 | verified |
| gin--cover | 32 | orchestrion | 0.410552 | 0.404105-0.427463 | 3.361391 | 165.18 | verified |
| gin--cover | 32 | mini | 0.232722 | 0.228245-0.242878 | 0.396459 | 53.44 | verified |
| gin--cover | 32 | mini-deferred | 0.237104 | 0.235008-0.238879 | 0.396192 | 52.32 | verified |
| gin--cover-client | 4 | native | 0.253092 | 0.250583-0.263731 | 0.385578 | 35.73 | verified |
| gin--cover-client | 4 | orchestrion | 6.616408 | 6.582757-6.973707 | 14.125127 | 98.61 | verified |
| gin--cover-client | 4 | mini | 0.561075 | 0.544638-0.584461 | 1.461174 | 76.03 | verified |
| gin--cover-client | 4 | mini-deferred | 0.565073 | 0.553191-0.573223 | 1.467804 | 75.97 | verified |
| gin--cover-client | 32 | native | 0.253249 | 0.250254-0.262717 | 0.420659 | 52.08 | verified |
| gin--cover-client | 32 | orchestrion | 6.541288 | 6.516528-6.660464 | 16.820350 | 200.94 | verified |
| gin--cover-client | 32 | mini | 0.487369 | 0.478221-0.496094 | 3.561389 | 146.32 | verified |
| gin--cover-client | 32 | mini-deferred | 0.500610 | 0.490546-0.503082 | 3.590350 | 147.68 | verified |
| gin--race-cover-client | 4 | native | 3.415680 | 3.371987-3.544310 | 8.500800 | 243.92 | verified |
| gin--race-cover-client | 4 | orchestrion | 18.491987 | 18.428509-18.560299 | 43.477136 | 408.91 | failed |
| gin--race-cover-client | 4 | mini | 4.964158 | 4.877433-4.977608 | 10.181950 | 206.87 | unverified |
| gin--race-cover-client | 4 | mini-deferred | 4.965381 | 4.843177-5.070978 | 10.197185 | 206.34 | unverified |
| gin--race-cover-client | 32 | native | 3.446807 | 3.413063-3.583871 | 8.572765 | 283.77 | verified |
| gin--race-cover-client | 32 | orchestrion | 18.746791 | 18.707628-18.803462 | 48.207009 | 470.64 | failed |
| gin--race-cover-client | 32 | mini | 3.947026 | 3.926807-4.094434 | 12.626688 | 370.15 | unverified |
| gin--race-cover-client | 32 | mini-deferred | 4.461270 | 3.963207-4.611670 | 14.088541 | 362.39 | unverified |
| chi--race | 4 | native | 27.214190 | 27.209690-27.216559 | 44.707255 | 2958.75 | verified |
| chi--race | 4 | orchestrion | 27.322207 | 27.308320-27.325687 | 46.083466 | 2979.35 | verified |
| chi--race | 4 | mini | 27.280423 | 27.278088-27.285716 | 45.359647 | 3000.27 | verified |
| chi--race | 4 | mini-deferred | 27.282419 | 27.277078-27.291571 | 45.427100 | 3004.50 | verified |
| chi--race | 32 | native | 27.218039 | 27.214792-27.225713 | 225.164169 | 3068.80 | verified |
| chi--race | 32 | orchestrion | 27.324007 | 27.302995-27.329207 | 147.048678 | 3112.38 | verified |
| chi--race | 32 | mini | 27.293276 | 27.285246-27.296972 | 227.270219 | 3051.20 | verified |
| chi--race | 32 | mini-deferred | 27.292168 | 27.280939-27.301079 | 226.570041 | 3055.86 | verified |
| chi--cover | 4 | native | 26.136957 | 26.134604-26.140277 | 3.004046 | 43.00 | verified |
| chi--cover | 4 | orchestrion | 26.229098 | 26.224293-26.236508 | 3.008069 | 84.96 | verified |
| chi--cover | 4 | mini | 26.160834 | 26.158171-26.162594 | 3.396063 | 32.44 | verified |
| chi--cover | 4 | mini-deferred | 26.164155 | 26.162571-26.173292 | 3.408547 | 34.05 | verified |
| chi--cover | 32 | native | 26.140129 | 26.138273-26.144419 | 8.704006 | 88.64 | verified |
| chi--cover | 32 | orchestrion | 26.233960 | 26.228725-26.238064 | 8.804965 | 149.12 | verified |
| chi--cover | 32 | mini | 26.169136 | 26.163319-26.176006 | 10.397668 | 58.58 | verified |
| chi--cover | 32 | mini-deferred | 26.169738 | 26.164764-26.171105 | 10.452665 | 55.57 | verified |
| chi--cover-client | 4 | native | 26.139077 | 26.138678-26.143371 | 3.273207 | 45.12 | verified |
| chi--cover-client | 4 | orchestrion | 26.788571 | 26.783171-26.806840 | 4.292763 | 98.80 | verified |
| chi--cover-client | 4 | mini | 26.244140 | 26.238695-26.249861 | 3.975677 | 41.71 | verified |
| chi--cover-client | 4 | mini-deferred | 26.245785 | 26.242690-26.256912 | 3.983209 | 41.52 | verified |
| chi--cover-client | 32 | native | 26.143078 | 26.140160-26.143884 | 12.007906 | 84.12 | verified |
| chi--cover-client | 32 | orchestrion | 26.843831 | 26.825489-26.848656 | 12.536681 | 145.21 | verified |
| chi--cover-client | 32 | mini | 26.238254 | 26.235380-26.241510 | 12.989995 | 63.57 | verified |
| chi--cover-client | 32 | mini-deferred | 26.241371 | 26.239275-26.245709 | 12.883299 | 62.21 | verified |
| chi--race-cover-client | 4 | native | 27.227274 | 27.219629-27.232085 | 55.070241 | 2944.42 | verified |
| chi--race-cover-client | 4 | orchestrion | 28.864119 | 28.817819-28.912666 | 59.183012 | 3073.44 | verified |
| chi--race-cover-client | 4 | mini | 27.403622 | 27.392530-27.416527 | 56.097243 | 2976.54 | verified |
| chi--race-cover-client | 4 | mini-deferred | 27.398130 | 27.396222-27.402884 | 55.994649 | 2958.44 | verified |
| chi--race-cover-client | 32 | native | 27.233273 | 27.230656-27.235937 | 243.743850 | 2940.62 | verified |
| chi--race-cover-client | 32 | orchestrion | 28.933591 | 28.929454-28.939520 | 189.447900 | 3072.50 | verified |
| chi--race-cover-client | 32 | mini | 27.411815 | 27.405618-27.418247 | 249.936795 | 2963.07 | verified |
| chi--race-cover-client | 32 | mini-deferred | 27.407331 | 27.402626-27.408729 | 248.917618 | 2954.38 | verified |
| testify-direct--normal | 4 | native | 0.002768 | 0.002659-0.002871 | 0.003788 | 14.54 | verified |
| testify-direct--normal | 4 | orchestrion | 0.014293 | 0.013946-0.014714 | 0.018899 | 26.62 | verified |
| testify-direct--normal | 4 | mini | 0.010316 | 0.010099-0.010549 | 0.011484 | 13.18 | verified |
| testify-direct--normal | 4 | mini-deferred | 0.010287 | 0.010081-0.010532 | 0.011856 | 12.91 | verified |
| testify-direct--normal | 32 | native | 0.003034 | 0.002834-0.003637 | 0.004573 | 14.98 | verified |
| testify-direct--normal | 32 | orchestrion | 0.016014 | 0.015913-0.016351 | 0.023118 | 29.43 | verified |
| testify-direct--normal | 32 | mini | 0.011607 | 0.011458-0.011834 | 0.013570 | 15.31 | verified |
| testify-direct--normal | 32 | mini-deferred | 0.011626 | 0.011381-0.011766 | 0.012932 | 14.54 | verified |
| testify-direct--race | 4 | native | 1.008293 | 1.008201-1.008790 | 0.009414 | 34.08 | verified |
| testify-direct--race | 4 | orchestrion | 1.041778 | 1.040832-1.041896 | 0.051832 | 67.46 | verified |
| testify-direct--race | 4 | mini | 1.026020 | 1.024744-1.026678 | 0.027584 | 32.16 | verified |
| testify-direct--race | 4 | mini-deferred | 1.025599 | 1.024884-1.025857 | 0.026892 | 31.45 | verified |
| testify-direct--race | 32 | native | 1.008927 | 1.008471-1.009004 | 0.010023 | 37.21 | verified |
| testify-direct--race | 32 | orchestrion | 1.045827 | 1.045513-1.046870 | 0.063160 | 73.38 | verified |
| testify-direct--race | 32 | mini | 1.027508 | 1.027160-1.028697 | 0.029533 | 34.98 | verified |
| testify-direct--race | 32 | mini-deferred | 1.028021 | 1.027539-1.028678 | 0.029642 | 33.61 | verified |
| testify-direct--cover | 4 | native | 0.003040 | 0.002961-0.003208 | 0.003917 | 14.48 | verified |
| testify-direct--cover | 4 | orchestrion | 0.023040 | 0.022565-0.023689 | 0.026835 | 29.69 | verified |
| testify-direct--cover | 4 | mini | 0.011266 | 0.010803-0.011705 | 0.012615 | 13.49 | verified |
| testify-direct--cover | 4 | mini-deferred | 0.011262 | 0.010826-0.011430 | 0.012555 | 13.40 | verified |
| testify-direct--cover | 32 | native | 0.003225 | 0.003185-0.003667 | 0.004487 | 14.72 | verified |
| testify-direct--cover | 32 | orchestrion | 0.025315 | 0.024757-0.025785 | 0.034084 | 33.78 | verified |
| testify-direct--cover | 32 | mini | 0.012213 | 0.011580-0.012474 | 0.014039 | 14.66 | verified |
| testify-direct--cover | 32 | mini-deferred | 0.012225 | 0.011660-0.012289 | 0.013506 | 14.53 | verified |
| testify-direct--cover-client | 4 | native | 0.003020 | 0.002933-0.003363 | 0.003895 | 14.33 | verified |
| testify-direct--cover-client | 4 | orchestrion | 0.024021 | 0.023243-0.024309 | 0.028469 | 27.64 | verified |
| testify-direct--cover-client | 4 | mini | 0.020745 | 0.020066-0.021324 | 0.021919 | 15.75 | verified |
| testify-direct--cover-client | 4 | mini-deferred | 0.020971 | 0.020760-0.021398 | 0.022418 | 15.41 | verified |
| testify-direct--cover-client | 32 | native | 0.003157 | 0.003060-0.003179 | 0.004158 | 14.77 | verified |
| testify-direct--cover-client | 32 | orchestrion | 0.026260 | 0.025792-0.026927 | 0.034280 | 34.04 | verified |
| testify-direct--cover-client | 32 | mini | 0.022974 | 0.022502-0.023904 | 0.024434 | 18.49 | verified |
| testify-direct--cover-client | 32 | mini-deferred | 0.022947 | 0.021733-0.023297 | 0.024544 | 17.66 | verified |
| testify-direct--race-cover-client | 4 | native | 1.009339 | 1.008955-1.009570 | 0.010245 | 33.97 | verified |
| testify-direct--race-cover-client | 4 | orchestrion | 1.052694 | 1.051837-1.053167 | 0.061688 | 66.72 | verified |
| testify-direct--race-cover-client | 4 | mini | 1.037093 | 1.036816-1.037815 | 0.038641 | 33.48 | verified |
| testify-direct--race-cover-client | 4 | mini-deferred | 1.036695 | 1.036091-1.037624 | 0.038317 | 32.43 | verified |
| testify-direct--race-cover-client | 32 | native | 1.009875 | 1.009629-1.010124 | 0.011177 | 35.75 | verified |
| testify-direct--race-cover-client | 32 | orchestrion | 1.057152 | 1.055745-1.061098 | 0.072301 | 72.09 | verified |
| testify-direct--race-cover-client | 32 | mini | 1.042235 | 1.040336-1.043508 | 0.047670 | 38.71 | verified |
| testify-direct--race-cover-client | 32 | mini-deferred | 1.041333 | 1.039428-1.043038 | 0.043147 | 36.59 | verified |
| testify-direct--cover-testing | 4 | native | 0.003584 | 0.003514-0.003702 | 0.004669 | 14.81 | verified |
| testify-direct--cover-testing | 4 | orchestrion | 0.038847 | 0.038238-0.039334 | 0.045965 | 27.74 | verified |
| testify-direct--cover-testing | 4 | mini | 0.037935 | 0.037386-0.038268 | 0.040589 | 15.93 | verified |
| testify-direct--cover-testing | 4 | mini-deferred | 0.038452 | 0.037830-0.041053 | 0.040579 | 16.02 | verified |
| testify-direct--cover-testing | 32 | native | 0.003901 | 0.003769-0.004127 | 0.005086 | 14.96 | verified |
| testify-direct--cover-testing | 32 | orchestrion | 0.041828 | 0.039988-0.042617 | 0.053131 | 35.20 | verified |
| testify-direct--cover-testing | 32 | mini | 0.040687 | 0.039547-0.042434 | 0.047424 | 19.34 | verified |
| testify-direct--cover-testing | 32 | mini-deferred | 0.041921 | 0.041558-0.042368 | 0.046683 | 18.88 | verified |
| testify-direct--race-cover-testing | 4 | native | 1.011706 | 1.011577-1.011857 | 0.012413 | 35.34 | verified |
| testify-direct--race-cover-testing | 4 | orchestrion | 1.089557 | 1.088832-1.091163 | 0.109470 | 74.43 | verified |
| testify-direct--race-cover-testing | 4 | mini | 1.078333 | 1.077533-1.079253 | 0.084063 | 38.30 | verified |
| testify-direct--race-cover-testing | 4 | mini-deferred | 1.081612 | 1.080443-1.082376 | 0.083644 | 37.71 | verified |
| testify-direct--race-cover-testing | 32 | native | 1.012462 | 1.012319-1.012960 | 0.013447 | 36.73 | verified |
| testify-direct--race-cover-testing | 32 | orchestrion | 1.097505 | 1.096087-1.098500 | 0.126504 | 83.53 | verified |
| testify-direct--race-cover-testing | 32 | mini | 1.085835 | 1.085436-1.086781 | 0.095656 | 44.43 | verified |
| testify-direct--race-cover-testing | 32 | mini-deferred | 1.088139 | 1.087387-1.088481 | 0.093910 | 42.30 | verified |
| testify-external--normal | 4 | native | 0.002716 | 0.002639-0.002796 | 0.003568 | 14.34 | verified |
| testify-external--normal | 4 | orchestrion | 0.014248 | 0.013731-0.014540 | 0.019312 | 27.12 | verified |
| testify-external--normal | 4 | mini | 0.010397 | 0.010235-0.010644 | 0.011619 | 13.34 | verified |
| testify-external--normal | 4 | mini-deferred | 0.010297 | 0.010005-0.010801 | 0.011791 | 13.16 | verified |
| testify-external--normal | 32 | native | 0.002940 | 0.002921-0.003123 | 0.004025 | 14.48 | verified |
| testify-external--normal | 32 | orchestrion | 0.016444 | 0.016203-0.016616 | 0.025720 | 32.32 | verified |
| testify-external--normal | 32 | mini | 0.011686 | 0.011320-0.011920 | 0.013491 | 14.68 | verified |
| testify-external--normal | 32 | mini-deferred | 0.012089 | 0.011482-0.012349 | 0.014009 | 15.13 | verified |
| testify-external--race | 4 | native | 1.008352 | 1.008251-1.008555 | 0.009517 | 33.83 | verified |
| testify-external--race | 4 | orchestrion | 1.041991 | 1.041745-1.042499 | 0.055411 | 68.84 | verified |
| testify-external--race | 4 | mini | 1.025633 | 1.025024-1.026290 | 0.026985 | 32.26 | verified |
| testify-external--race | 4 | mini-deferred | 1.025437 | 1.024959-1.026033 | 0.026743 | 31.25 | verified |
| testify-external--race | 32 | native | 1.009078 | 1.008949-1.009489 | 0.010453 | 36.97 | verified |
| testify-external--race | 32 | orchestrion | 1.046242 | 1.045924-1.046899 | 0.064850 | 75.32 | verified |
| testify-external--race | 32 | mini | 1.027293 | 1.026940-1.028019 | 0.029741 | 34.51 | verified |
| testify-external--race | 32 | mini-deferred | 1.027613 | 1.027116-1.028158 | 0.029217 | 33.96 | verified |
| testify-external--cover | 4 | native | 0.002880 | 0.002857-0.003041 | 0.003944 | 14.23 | verified |
| testify-external--cover | 4 | orchestrion | 0.023010 | 0.022500-0.023789 | 0.027232 | 29.42 | verified |
| testify-external--cover | 4 | mini | 0.011026 | 0.010732-0.011468 | 0.012413 | 13.41 | verified |
| testify-external--cover | 4 | mini-deferred | 0.011211 | 0.010833-0.011449 | 0.012033 | 13.26 | verified |
| testify-external--cover | 32 | native | 0.003031 | 0.002962-0.003418 | 0.004082 | 15.02 | verified |
| testify-external--cover | 32 | orchestrion | 0.025533 | 0.025143-0.025954 | 0.032932 | 34.40 | verified |
| testify-external--cover | 32 | mini | 0.012056 | 0.011895-0.012283 | 0.014067 | 14.84 | verified |
| testify-external--cover | 32 | mini-deferred | 0.012354 | 0.011870-0.013646 | 0.013680 | 14.67 | verified |
| testify-external--cover-client | 4 | native | 0.003078 | 0.003060-0.003356 | 0.004318 | 14.33 | verified |
| testify-external--cover-client | 4 | orchestrion | 0.023386 | 0.022280-0.024013 | 0.027525 | 28.16 | verified |
| testify-external--cover-client | 4 | mini | 0.021202 | 0.020715-0.021758 | 0.022551 | 15.66 | verified |
| testify-external--cover-client | 4 | mini-deferred | 0.021519 | 0.020915-0.022010 | 0.022898 | 15.49 | verified |
| testify-external--cover-client | 32 | native | 0.003307 | 0.003124-0.003462 | 0.004624 | 15.01 | verified |
| testify-external--cover-client | 32 | orchestrion | 0.025663 | 0.025281-0.026668 | 0.033521 | 34.38 | verified |
| testify-external--cover-client | 32 | mini | 0.023707 | 0.022164-0.024550 | 0.027916 | 17.47 | verified |
| testify-external--cover-client | 32 | mini-deferred | 0.023253 | 0.022543-0.023911 | 0.025159 | 17.73 | verified |
| testify-external--race-cover-client | 4 | native | 1.009322 | 1.008989-1.009474 | 0.010397 | 32.80 | verified |
| testify-external--race-cover-client | 4 | orchestrion | 1.053227 | 1.052705-1.054280 | 0.061959 | 67.10 | verified |
| testify-external--race-cover-client | 4 | mini | 1.037943 | 1.037176-1.038069 | 0.039777 | 33.52 | verified |
| testify-external--race-cover-client | 4 | mini-deferred | 1.037827 | 1.037319-1.038360 | 0.039091 | 33.16 | verified |
| testify-external--race-cover-client | 32 | native | 1.009809 | 1.009439-1.010221 | 0.010980 | 35.51 | verified |
| testify-external--race-cover-client | 32 | orchestrion | 1.058201 | 1.056507-1.059143 | 0.072888 | 73.07 | verified |
| testify-external--race-cover-client | 32 | mini | 1.041939 | 1.041066-1.043752 | 0.047681 | 39.25 | verified |
| testify-external--race-cover-client | 32 | mini-deferred | 1.042157 | 1.041579-1.042575 | 0.046114 | 38.35 | verified |
| testify-external--cover-testing | 4 | native | 0.003702 | 0.003598-0.003749 | 0.004493 | 14.59 | verified |
| testify-external--cover-testing | 4 | orchestrion | 0.039288 | 0.037583-0.039342 | 0.045974 | 28.01 | verified |
| testify-external--cover-testing | 4 | mini | 0.037996 | 0.037726-0.038326 | 0.040722 | 15.77 | verified |
| testify-external--cover-testing | 4 | mini-deferred | 0.038586 | 0.038095-0.039423 | 0.040537 | 15.95 | verified |
| testify-external--cover-testing | 32 | native | 0.004002 | 0.003766-0.004048 | 0.005011 | 16.75 | verified |
| testify-external--cover-testing | 32 | orchestrion | 0.042201 | 0.041555-0.042875 | 0.054885 | 34.48 | verified |
| testify-external--cover-testing | 32 | mini | 0.039977 | 0.039817-0.041452 | 0.047167 | 19.45 | verified |
| testify-external--cover-testing | 32 | mini-deferred | 0.041015 | 0.040688-0.041681 | 0.044769 | 19.07 | verified |
| testify-external--race-cover-testing | 4 | native | 1.012068 | 1.011585-1.012211 | 0.012663 | 35.28 | verified |
| testify-external--race-cover-testing | 4 | orchestrion | 1.090319 | 1.088727-1.091354 | 0.110854 | 74.31 | verified |
| testify-external--race-cover-testing | 4 | mini | 1.078481 | 1.078415-1.080286 | 0.085073 | 38.59 | verified |
| testify-external--race-cover-testing | 4 | mini-deferred | 1.080881 | 1.080678-1.081074 | 0.083455 | 37.45 | verified |
| testify-external--race-cover-testing | 32 | native | 1.012989 | 1.012573-1.013907 | 0.013790 | 36.27 | verified |
| testify-external--race-cover-testing | 32 | orchestrion | 1.098761 | 1.096127-1.102928 | 0.126615 | 73.03 | verified |
| testify-external--race-cover-testing | 32 | mini | 1.085626 | 1.085402-1.086783 | 0.095836 | 44.41 | verified |
| testify-external--race-cover-testing | 32 | mini-deferred | 1.088437 | 1.087166-1.089332 | 0.094090 | 42.22 | verified |

## Validation gaps

- gin--race/4/orchestrion: race detector reported an error
- gin--race/32/orchestrion: race detector reported an error
- gin--race-cover-client/4/orchestrion: SDK reference unavailable for this failed-oracle combination; race detector reported an error
- gin--race-cover-client/4/mini: historical SDK reference unavailable for this combination
- gin--race-cover-client/4/mini-deferred: historical SDK reference unavailable for this combination
- gin--race-cover-client/32/orchestrion: SDK reference unavailable for this failed-oracle combination; race detector reported an error
- gin--race-cover-client/32/mini: historical SDK reference unavailable for this combination
- gin--race-cover-client/32/mini-deferred: historical SDK reference unavailable for this combination

The observed-time table keeps successful and failed durations. Failed or unverified
groups have no comparative median in the main table. Individual CPU and memory values
remain in observations.csv. No slow observation is removed.
