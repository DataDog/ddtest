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
| Gin | `none` | 4 | 0.183472 s | 0.227665 s (+24.1%) | 0.212212 s (-6.8%; +15.7%) | 0.212700 s (-6.6%; +15.9%) |
| Gin | `none` | 32 | 0.192854 s | 0.235763 s (+22.2%) | 0.217107 s (-7.9%; +12.6%) | 0.217153 s (-7.9%; +12.6%) |
| Chi | `none` | 4 | 26.136864 s | 26.174048 s (+0.1%) | 26.160613 s (-0.1%; +0.1%) | 26.161298 s (-0.0%; +0.1%) |
| Chi | `none` | 32 | 26.141335 s | 26.179190 s (+0.1%) | 26.165334 s (-0.1%; +0.1%) | 26.164379 s (-0.1%; +0.1%) |
| Gin | `-race` | 4 | 2.040156 s | FAIL 5/6 | 2.548028 s (vs Orchestrion unavailable; +24.9% vs Native) | 2.551075 s (vs Orchestrion unavailable; +25.0% vs Native) |
| Gin | `-race` | 32 | 1.415086 s | FAIL 6/6 | 1.550106 s (vs Orchestrion unavailable; +9.5% vs Native) | 1.548417 s (vs Orchestrion unavailable; +9.4% vs Native) |
| Gin | `-cover` | 4 | 0.188422 s | 0.484403 s (+157.1%) | 0.229043 s (-52.7%; +21.6%) | 0.229758 s (-52.6%; +21.9%) |
| Gin | `-cover` | 32 | 0.198064 s | 0.410552 s (+107.3%) | 0.223583 s (-45.5%; +12.9%) | 0.229629 s (-44.1%; +15.9%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.253092 s | 6.616408 s (+2514.2%) | 6.533885 s (-1.2%; +2481.6%) | 6.774886 s (+2.4%; +2576.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.253249 s | 6.541288 s (+2482.9%) | 6.705614 s (+2.5%; +2547.8%) | 6.898903 s (+5.5%; +2624.2%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 3.415680 s | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 3.446807 s | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 27.214190 s | 27.322207 s (+0.4%) | 27.276910 s (-0.2%; +0.2%) | 27.286580 s (-0.1%; +0.3%) |
| Chi | `-race` | 32 | 27.218039 s | 27.324007 s (+0.4%) | 27.280807 s (-0.2%; +0.2%) | 27.277332 s (-0.2%; +0.2%) |
| Chi | `-cover` | 4 | 26.136957 s | 26.229098 s (+0.4%) | 26.161430 s (-0.3%; +0.1%) | 26.159615 s (-0.3%; +0.1%) |
| Chi | `-cover` | 32 | 26.140129 s | 26.233960 s (+0.4%) | 26.165263 s (-0.3%; +0.1%) | 26.166138 s (-0.3%; +0.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 26.139077 s | 26.788571 s (+2.5%) | 26.777817 s (-0.0%; +2.4%) | 26.797732 s (+0.0%; +2.5%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 26.143078 s | 26.843831 s (+2.7%) | 26.819621 s (-0.1%; +2.6%) | 26.850099 s (+0.0%; +2.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 27.227274 s | 28.864119 s (+6.0%) | 28.716832 s (-0.5%; +5.5%) | 28.789722 s (-0.3%; +5.7%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 27.233273 s | 28.933591 s (+6.2%) | 28.908935 s (-0.1%; +6.2%) | 29.041531 s (+0.4%; +6.6%) |
| Testify Direct | `none` | 4 | 0.002768 s | 0.014293 s (+416.4%) | 0.009515 s (-33.4%; +243.8%) | 0.009644 s (-32.5%; +248.5%) |
| Testify Direct | `none` | 32 | 0.003034 s | 0.016014 s (+427.8%) | 0.010918 s (-31.8%; +259.8%) | 0.010853 s (-32.2%; +257.7%) |
| Testify Direct | `-race` | 4 | 1.008293 s | 1.041778 s (+3.3%) | 1.021698 s (-1.9%; +1.3%) | 1.020994 s (-2.0%; +1.3%) |
| Testify Direct | `-race` | 32 | 1.008927 s | 1.045827 s (+3.7%) | 1.023413 s (-2.1%; +1.4%) | 1.022892 s (-2.2%; +1.4%) |
| Testify Direct | `-cover` | 4 | 0.003040 s | 0.023040 s (+658.0%) | 0.010441 s (-54.7%; +243.5%) | 0.010529 s (-54.3%; +246.4%) |
| Testify Direct | `-cover` | 32 | 0.003225 s | 0.025315 s (+685.0%) | 0.011787 s (-53.4%; +265.5%) | 0.011557 s (-54.3%; +258.4%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.003020 s | 0.024021 s (+695.3%) | 0.020023 s (-16.6%; +563.0%) | 0.020623 s (-14.1%; +582.8%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.003157 s | 0.026260 s (+731.7%) | 0.022257 s (-15.2%; +604.9%) | 0.022361 s (-14.8%; +608.2%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009339 s | 1.052694 s (+4.3%) | 1.033642 s (-1.8%; +2.4%) | 1.033759 s (-1.8%; +2.4%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009875 s | 1.057152 s (+4.7%) | 1.038362 s (-1.8%; +2.8%) | 1.038035 s (-1.8%; +2.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003584 s | 0.038847 s (+984.0%) | 0.035832 s (-7.8%; +899.9%) | 0.036422 s (-6.2%; +916.4%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.003901 s | 0.041828 s (+972.2%) | 0.038853 s (-7.1%; +895.9%) | 0.038621 s (-7.7%; +890.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.011706 s | 1.089557 s (+7.7%) | 1.072982 s (-1.5%; +6.1%) | 1.075422 s (-1.3%; +6.3%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012462 s | 1.097505 s (+8.4%) | 1.079401 s (-1.6%; +6.6%) | 1.081436 s (-1.5%; +6.8%) |
| Testify External | `none` | 4 | 0.002716 s | 0.014248 s (+424.6%) | 0.009501 s (-33.3%; +249.8%) | 0.009585 s (-32.7%; +252.9%) |
| Testify External | `none` | 32 | 0.002940 s | 0.016444 s (+459.4%) | 0.010779 s (-34.4%; +266.7%) | 0.010826 s (-34.2%; +268.3%) |
| Testify External | `-race` | 4 | 1.008352 s | 1.041991 s (+3.3%) | 1.021821 s (-1.9%; +1.3%) | 1.021212 s (-2.0%; +1.3%) |
| Testify External | `-race` | 32 | 1.009078 s | 1.046242 s (+3.7%) | 1.023707 s (-2.2%; +1.4%) | 1.023094 s (-2.2%; +1.4%) |
| Testify External | `-cover` | 4 | 0.002880 s | 0.023010 s (+699.1%) | 0.010221 s (-55.6%; +254.9%) | 0.010069 s (-56.2%; +249.7%) |
| Testify External | `-cover` | 32 | 0.003031 s | 0.025533 s (+742.3%) | 0.011604 s (-54.6%; +282.8%) | 0.011218 s (-56.1%; +270.1%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.003078 s | 0.023386 s (+659.8%) | 0.020124 s (-14.0%; +553.8%) | 0.019986 s (-14.5%; +549.3%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.003307 s | 0.025663 s (+676.0%) | 0.022016 s (-14.2%; +565.7%) | 0.022025 s (-14.2%; +566.0%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009322 s | 1.053227 s (+4.4%) | 1.033643 s (-1.9%; +2.4%) | 1.033264 s (-1.9%; +2.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009809 s | 1.058201 s (+4.8%) | 1.038577 s (-1.9%; +2.8%) | 1.038700 s (-1.8%; +2.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003702 s | 0.039288 s (+961.3%) | 0.035499 s (-9.6%; +859.0%) | 0.036283 s (-7.6%; +880.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.004002 s | 0.042201 s (+954.5%) | 0.038483 s (-8.8%; +861.6%) | 0.039018 s (-7.5%; +875.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.012068 s | 1.090319 s (+7.7%) | 1.073191 s (-1.6%; +6.0%) | 1.075557 s (-1.4%; +6.3%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012989 s | 1.098761 s (+8.5%) | 1.078846 s (-1.8%; +6.5%) | 1.080935 s (-1.6%; +6.7%) |

## CPU, memory and dispersion

| Case | CPUs | Variant | Median (s) | Min-max (s) | CPU median (CPU-s) | Memory median (MiB) | Validation |
| --- | ---: | --- | ---: | --- | ---: | ---: | --- |
| gin | 4 | native | 0.183472 | 0.178205-0.194327 | 0.170003 | 36.64 | verified |
| gin | 4 | orchestrion | 0.227665 | 0.219127-0.241448 | 0.348156 | 60.29 | verified |
| gin | 4 | mini | 0.212212 | 0.205568-0.216526 | 0.264910 | 50.88 | verified |
| gin | 4 | mini-deferred | 0.212700 | 0.209112-0.217745 | 0.263090 | 50.23 | verified |
| gin | 32 | native | 0.192854 | 0.190069-0.194346 | 0.217764 | 42.18 | verified |
| gin | 32 | orchestrion | 0.235763 | 0.230035-0.256558 | 0.439478 | 70.67 | verified |
| gin | 32 | mini | 0.217107 | 0.213256-0.225774 | 0.339883 | 59.07 | verified |
| gin | 32 | mini-deferred | 0.217153 | 0.213359-0.219139 | 0.339319 | 60.40 | verified |
| chi | 4 | native | 26.136864 | 26.135458-26.150430 | 2.901706 | 45.64 | verified |
| chi | 4 | orchestrion | 26.174048 | 26.152724-26.184653 | 2.682397 | 79.65 | verified |
| chi | 4 | mini | 26.160613 | 26.151292-26.162010 | 3.363877 | 35.01 | verified |
| chi | 4 | mini-deferred | 26.161298 | 26.152153-26.163739 | 3.363660 | 33.34 | verified |
| chi | 32 | native | 26.141335 | 26.139699-26.141874 | 7.661383 | 94.73 | verified |
| chi | 32 | orchestrion | 26.179190 | 26.160035-26.183551 | 7.197552 | 128.05 | verified |
| chi | 32 | mini | 26.165334 | 26.160060-26.168320 | 9.947548 | 55.91 | verified |
| chi | 32 | mini-deferred | 26.164379 | 26.162192-26.173237 | 10.192807 | 56.33 | verified |
| gin--race | 4 | native | 2.040156 | 2.034008-2.041493 | 2.627885 | 199.33 | verified |
| gin--race | 4 | orchestrion | 2.139835 | 2.131945-2.151618 | 3.691318 | 332.23 | failed |
| gin--race | 4 | mini | 2.548028 | 2.545483-2.549260 | 3.024765 | 185.50 | verified |
| gin--race | 4 | mini-deferred | 2.551075 | 2.546749-2.556982 | 3.034454 | 191.71 | verified |
| gin--race | 32 | native | 1.415086 | 1.407846-1.426243 | 2.725913 | 262.89 | verified |
| gin--race | 32 | orchestrion | 1.731117 | 1.724280-1.740767 | 3.893175 | 418.80 | failed |
| gin--race | 32 | mini | 1.550106 | 1.547234-1.558122 | 3.150730 | 333.50 | verified |
| gin--race | 32 | mini-deferred | 1.548417 | 1.545931-1.564152 | 3.147409 | 333.32 | verified |
| gin--cover | 4 | native | 0.188422 | 0.186399-0.190422 | 0.192585 | 35.87 | verified |
| gin--cover | 4 | orchestrion | 0.484403 | 0.471239-0.497941 | 1.289090 | 92.60 | verified |
| gin--cover | 4 | mini | 0.229043 | 0.226136-0.233777 | 0.308632 | 40.89 | verified |
| gin--cover | 4 | mini-deferred | 0.229758 | 0.227680-0.241132 | 0.314849 | 41.33 | verified |
| gin--cover | 32 | native | 0.198064 | 0.194113-0.219861 | 0.238151 | 42.11 | verified |
| gin--cover | 32 | orchestrion | 0.410552 | 0.404105-0.427463 | 3.361391 | 165.18 | verified |
| gin--cover | 32 | mini | 0.223583 | 0.220214-0.227121 | 0.366299 | 56.68 | verified |
| gin--cover | 32 | mini-deferred | 0.229629 | 0.224711-0.231841 | 0.382594 | 54.81 | verified |
| gin--cover-client | 4 | native | 0.253092 | 0.250583-0.263731 | 0.385578 | 35.73 | verified |
| gin--cover-client | 4 | orchestrion | 6.616408 | 6.582757-6.973707 | 14.125127 | 98.61 | verified |
| gin--cover-client | 4 | mini | 6.533885 | 6.504458-6.552752 | 13.558669 | 77.66 | verified |
| gin--cover-client | 4 | mini-deferred | 6.774886 | 6.745705-6.800246 | 13.666110 | 73.69 | verified |
| gin--cover-client | 32 | native | 0.253249 | 0.250254-0.262717 | 0.420659 | 52.08 | verified |
| gin--cover-client | 32 | orchestrion | 6.541288 | 6.516528-6.660464 | 16.820350 | 200.94 | verified |
| gin--cover-client | 32 | mini | 6.705614 | 6.544968-6.722174 | 16.848079 | 150.02 | verified |
| gin--cover-client | 32 | mini-deferred | 6.898903 | 6.740939-6.912882 | 16.637406 | 149.93 | verified |
| gin--race-cover-client | 4 | native | 3.415680 | 3.371987-3.544310 | 8.500800 | 243.92 | verified |
| gin--race-cover-client | 4 | orchestrion | 18.491987 | 18.428509-18.560299 | 43.477136 | 408.91 | failed |
| gin--race-cover-client | 4 | mini | 19.373379 | 19.330340-19.867019 | 39.683920 | 169.04 | unverified |
| gin--race-cover-client | 4 | mini-deferred | 20.567075 | 20.530466-20.605798 | 40.200490 | 165.60 | unverified |
| gin--race-cover-client | 32 | native | 3.446807 | 3.413063-3.583871 | 8.572765 | 283.77 | verified |
| gin--race-cover-client | 32 | orchestrion | 18.746791 | 18.707628-18.803462 | 48.207009 | 470.64 | failed |
| gin--race-cover-client | 32 | mini | 18.790077 | 18.756092-19.262925 | 43.596123 | 264.54 | unverified |
| gin--race-cover-client | 32 | mini-deferred | 20.049785 | 20.006566-20.116980 | 43.861082 | 261.48 | unverified |
| chi--race | 4 | native | 27.214190 | 27.209690-27.216559 | 44.707255 | 2958.75 | verified |
| chi--race | 4 | orchestrion | 27.322207 | 27.308320-27.325687 | 46.083466 | 2979.35 | verified |
| chi--race | 4 | mini | 27.276910 | 27.267425-27.285220 | 44.820791 | 3005.96 | verified |
| chi--race | 4 | mini-deferred | 27.286580 | 27.269748-27.290435 | 44.995848 | 3022.78 | verified |
| chi--race | 32 | native | 27.218039 | 27.214792-27.225713 | 225.164169 | 3068.80 | verified |
| chi--race | 32 | orchestrion | 27.324007 | 27.302995-27.329207 | 147.048678 | 3112.38 | verified |
| chi--race | 32 | mini | 27.280807 | 27.276698-27.289141 | 228.295738 | 3085.49 | verified |
| chi--race | 32 | mini-deferred | 27.277332 | 27.274217-27.285813 | 227.228814 | 3081.89 | verified |
| chi--cover | 4 | native | 26.136957 | 26.134604-26.140277 | 3.004046 | 43.00 | verified |
| chi--cover | 4 | orchestrion | 26.229098 | 26.224293-26.236508 | 3.008069 | 84.96 | verified |
| chi--cover | 4 | mini | 26.161430 | 26.152901-26.166097 | 3.327569 | 48.03 | verified |
| chi--cover | 4 | mini-deferred | 26.159615 | 26.152858-26.163462 | 3.290297 | 48.79 | verified |
| chi--cover | 32 | native | 26.140129 | 26.138273-26.144419 | 8.704006 | 88.64 | verified |
| chi--cover | 32 | orchestrion | 26.233960 | 26.228725-26.238064 | 8.804965 | 149.12 | verified |
| chi--cover | 32 | mini | 26.165263 | 26.161570-26.174858 | 9.957099 | 96.50 | verified |
| chi--cover | 32 | mini-deferred | 26.166138 | 26.162386-26.176481 | 10.117473 | 88.34 | verified |
| chi--cover-client | 4 | native | 26.139077 | 26.138678-26.143371 | 3.273207 | 45.12 | verified |
| chi--cover-client | 4 | orchestrion | 26.788571 | 26.783171-26.806840 | 4.292763 | 98.80 | verified |
| chi--cover-client | 4 | mini | 26.777817 | 26.772653-26.780116 | 4.756470 | 60.83 | verified |
| chi--cover-client | 4 | mini-deferred | 26.797732 | 26.792998-26.819479 | 4.740245 | 58.68 | verified |
| chi--cover-client | 32 | native | 26.143078 | 26.140160-26.143884 | 12.007906 | 84.12 | verified |
| chi--cover-client | 32 | orchestrion | 26.843831 | 26.825489-26.848656 | 12.536681 | 145.21 | verified |
| chi--cover-client | 32 | mini | 26.819621 | 26.810861-26.826310 | 14.510381 | 98.94 | verified |
| chi--cover-client | 32 | mini-deferred | 26.850099 | 26.846024-26.857647 | 14.272653 | 105.16 | verified |
| chi--race-cover-client | 4 | native | 27.227274 | 27.219629-27.232085 | 55.070241 | 2944.42 | verified |
| chi--race-cover-client | 4 | orchestrion | 28.864119 | 28.817819-28.912666 | 59.183012 | 3073.44 | verified |
| chi--race-cover-client | 4 | mini | 28.716832 | 28.705132-28.742120 | 56.722376 | 2471.74 | verified |
| chi--race-cover-client | 4 | mini-deferred | 28.789722 | 28.772986-28.827578 | 56.864542 | 2471.97 | verified |
| chi--race-cover-client | 32 | native | 27.233273 | 27.230656-27.235937 | 243.743850 | 2940.62 | verified |
| chi--race-cover-client | 32 | orchestrion | 28.933591 | 28.929454-28.939520 | 189.447900 | 3072.50 | verified |
| chi--race-cover-client | 32 | mini | 28.908935 | 28.906287-28.934649 | 250.837357 | 2478.73 | verified |
| chi--race-cover-client | 32 | mini-deferred | 29.041531 | 29.040381-29.051438 | 250.455422 | 2478.05 | verified |
| testify-direct--normal | 4 | native | 0.002768 | 0.002659-0.002871 | 0.003788 | 14.54 | verified |
| testify-direct--normal | 4 | orchestrion | 0.014293 | 0.013946-0.014714 | 0.018899 | 26.62 | verified |
| testify-direct--normal | 4 | mini | 0.009515 | 0.009401-0.009707 | 0.010728 | 17.28 | verified |
| testify-direct--normal | 4 | mini-deferred | 0.009644 | 0.009403-0.009908 | 0.010732 | 15.66 | verified |
| testify-direct--normal | 32 | native | 0.003034 | 0.002834-0.003637 | 0.004573 | 14.98 | verified |
| testify-direct--normal | 32 | orchestrion | 0.016014 | 0.015913-0.016351 | 0.023118 | 29.43 | verified |
| testify-direct--normal | 32 | mini | 0.010918 | 0.010649-0.011108 | 0.012294 | 18.34 | verified |
| testify-direct--normal | 32 | mini-deferred | 0.010853 | 0.010512-0.011138 | 0.012279 | 18.32 | verified |
| testify-direct--race | 4 | native | 1.008293 | 1.008201-1.008790 | 0.009414 | 34.08 | verified |
| testify-direct--race | 4 | orchestrion | 1.041778 | 1.040832-1.041896 | 0.051832 | 67.46 | verified |
| testify-direct--race | 4 | mini | 1.021698 | 1.021574-1.022312 | 0.023468 | 42.90 | verified |
| testify-direct--race | 4 | mini-deferred | 1.020994 | 1.020698-1.021020 | 0.022090 | 40.41 | verified |
| testify-direct--race | 32 | native | 1.008927 | 1.008471-1.009004 | 0.010023 | 37.21 | verified |
| testify-direct--race | 32 | orchestrion | 1.045827 | 1.045513-1.046870 | 0.063160 | 73.38 | verified |
| testify-direct--race | 32 | mini | 1.023413 | 1.022903-1.023625 | 0.025453 | 44.98 | verified |
| testify-direct--race | 32 | mini-deferred | 1.022892 | 1.022667-1.023859 | 0.025112 | 44.57 | verified |
| testify-direct--cover | 4 | native | 0.003040 | 0.002961-0.003208 | 0.003917 | 14.48 | verified |
| testify-direct--cover | 4 | orchestrion | 0.023040 | 0.022565-0.023689 | 0.026835 | 29.69 | verified |
| testify-direct--cover | 4 | mini | 0.010441 | 0.010219-0.010759 | 0.011815 | 17.45 | verified |
| testify-direct--cover | 4 | mini-deferred | 0.010529 | 0.010195-0.010928 | 0.011333 | 17.21 | verified |
| testify-direct--cover | 32 | native | 0.003225 | 0.003185-0.003667 | 0.004487 | 14.72 | verified |
| testify-direct--cover | 32 | orchestrion | 0.025315 | 0.024757-0.025785 | 0.034084 | 33.78 | verified |
| testify-direct--cover | 32 | mini | 0.011787 | 0.011508-0.012167 | 0.013413 | 18.96 | verified |
| testify-direct--cover | 32 | mini-deferred | 0.011557 | 0.011332-0.011768 | 0.013208 | 18.07 | verified |
| testify-direct--cover-client | 4 | native | 0.003020 | 0.002933-0.003363 | 0.003895 | 14.33 | verified |
| testify-direct--cover-client | 4 | orchestrion | 0.024021 | 0.023243-0.024309 | 0.028469 | 27.64 | verified |
| testify-direct--cover-client | 4 | mini | 0.020023 | 0.019671-0.020254 | 0.021074 | 22.23 | verified |
| testify-direct--cover-client | 4 | mini-deferred | 0.020623 | 0.020099-0.021179 | 0.021888 | 22.03 | verified |
| testify-direct--cover-client | 32 | native | 0.003157 | 0.003060-0.003179 | 0.004158 | 14.77 | verified |
| testify-direct--cover-client | 32 | orchestrion | 0.026260 | 0.025792-0.026927 | 0.034280 | 34.04 | verified |
| testify-direct--cover-client | 32 | mini | 0.022257 | 0.021428-0.022570 | 0.026106 | 25.03 | verified |
| testify-direct--cover-client | 32 | mini-deferred | 0.022361 | 0.020941-0.023764 | 0.024359 | 25.00 | verified |
| testify-direct--race-cover-client | 4 | native | 1.009339 | 1.008955-1.009570 | 0.010245 | 33.97 | verified |
| testify-direct--race-cover-client | 4 | orchestrion | 1.052694 | 1.051837-1.053167 | 0.061688 | 66.72 | verified |
| testify-direct--race-cover-client | 4 | mini | 1.033642 | 1.033093-1.033700 | 0.035833 | 43.15 | verified |
| testify-direct--race-cover-client | 4 | mini-deferred | 1.033759 | 1.032732-1.034078 | 0.035101 | 42.62 | verified |
| testify-direct--race-cover-client | 32 | native | 1.009875 | 1.009629-1.010124 | 0.011177 | 35.75 | verified |
| testify-direct--race-cover-client | 32 | orchestrion | 1.057152 | 1.055745-1.061098 | 0.072301 | 72.09 | verified |
| testify-direct--race-cover-client | 32 | mini | 1.038362 | 1.038001-1.038740 | 0.043908 | 49.28 | verified |
| testify-direct--race-cover-client | 32 | mini-deferred | 1.038035 | 1.037785-1.038170 | 0.042488 | 48.98 | verified |
| testify-direct--cover-testing | 4 | native | 0.003584 | 0.003514-0.003702 | 0.004669 | 14.81 | verified |
| testify-direct--cover-testing | 4 | orchestrion | 0.038847 | 0.038238-0.039334 | 0.045965 | 27.74 | verified |
| testify-direct--cover-testing | 4 | mini | 0.035832 | 0.035082-0.036555 | 0.038413 | 22.75 | verified |
| testify-direct--cover-testing | 4 | mini-deferred | 0.036422 | 0.035926-0.037211 | 0.038388 | 22.29 | verified |
| testify-direct--cover-testing | 32 | native | 0.003901 | 0.003769-0.004127 | 0.005086 | 14.96 | verified |
| testify-direct--cover-testing | 32 | orchestrion | 0.041828 | 0.039988-0.042617 | 0.053131 | 35.20 | verified |
| testify-direct--cover-testing | 32 | mini | 0.038853 | 0.037893-0.039873 | 0.045322 | 25.45 | verified |
| testify-direct--cover-testing | 32 | mini-deferred | 0.038621 | 0.038047-0.040618 | 0.043374 | 25.08 | verified |
| testify-direct--race-cover-testing | 4 | native | 1.011706 | 1.011577-1.011857 | 0.012413 | 35.34 | verified |
| testify-direct--race-cover-testing | 4 | orchestrion | 1.089557 | 1.088832-1.091163 | 0.109470 | 74.43 | verified |
| testify-direct--race-cover-testing | 4 | mini | 1.072982 | 1.072380-1.074612 | 0.078765 | 46.15 | verified |
| testify-direct--race-cover-testing | 4 | mini-deferred | 1.075422 | 1.074759-1.075986 | 0.078340 | 44.85 | verified |
| testify-direct--race-cover-testing | 32 | native | 1.012462 | 1.012319-1.012960 | 0.013447 | 36.73 | verified |
| testify-direct--race-cover-testing | 32 | orchestrion | 1.097505 | 1.096087-1.098500 | 0.126504 | 83.53 | verified |
| testify-direct--race-cover-testing | 32 | mini | 1.079401 | 1.078276-1.079876 | 0.089117 | 53.07 | verified |
| testify-direct--race-cover-testing | 32 | mini-deferred | 1.081436 | 1.080178-1.081621 | 0.087436 | 52.13 | verified |
| testify-external--normal | 4 | native | 0.002716 | 0.002639-0.002796 | 0.003568 | 14.34 | verified |
| testify-external--normal | 4 | orchestrion | 0.014248 | 0.013731-0.014540 | 0.019312 | 27.12 | verified |
| testify-external--normal | 4 | mini | 0.009501 | 0.009417-0.009686 | 0.010761 | 17.33 | verified |
| testify-external--normal | 4 | mini-deferred | 0.009585 | 0.009324-0.009797 | 0.010575 | 17.05 | verified |
| testify-external--normal | 32 | native | 0.002940 | 0.002921-0.003123 | 0.004025 | 14.48 | verified |
| testify-external--normal | 32 | orchestrion | 0.016444 | 0.016203-0.016616 | 0.025720 | 32.32 | verified |
| testify-external--normal | 32 | mini | 0.010779 | 0.010509-0.011662 | 0.012295 | 19.05 | verified |
| testify-external--normal | 32 | mini-deferred | 0.010826 | 0.010408-0.011082 | 0.012280 | 18.62 | verified |
| testify-external--race | 4 | native | 1.008352 | 1.008251-1.008555 | 0.009517 | 33.83 | verified |
| testify-external--race | 4 | orchestrion | 1.041991 | 1.041745-1.042499 | 0.055411 | 68.84 | verified |
| testify-external--race | 4 | mini | 1.021821 | 1.021291-1.022248 | 0.023434 | 43.24 | verified |
| testify-external--race | 4 | mini-deferred | 1.021212 | 1.021001-1.021603 | 0.022782 | 42.26 | verified |
| testify-external--race | 32 | native | 1.009078 | 1.008949-1.009489 | 0.010453 | 36.97 | verified |
| testify-external--race | 32 | orchestrion | 1.046242 | 1.045924-1.046899 | 0.064850 | 75.32 | verified |
| testify-external--race | 32 | mini | 1.023707 | 1.022536-1.025432 | 0.025600 | 45.43 | verified |
| testify-external--race | 32 | mini-deferred | 1.023094 | 1.022862-1.023756 | 0.024940 | 42.80 | verified |
| testify-external--cover | 4 | native | 0.002880 | 0.002857-0.003041 | 0.003944 | 14.23 | verified |
| testify-external--cover | 4 | orchestrion | 0.023010 | 0.022500-0.023789 | 0.027232 | 29.42 | verified |
| testify-external--cover | 4 | mini | 0.010221 | 0.010036-0.010336 | 0.011240 | 17.25 | verified |
| testify-external--cover | 4 | mini-deferred | 0.010069 | 0.009731-0.010223 | 0.010996 | 16.70 | verified |
| testify-external--cover | 32 | native | 0.003031 | 0.002962-0.003418 | 0.004082 | 15.02 | verified |
| testify-external--cover | 32 | orchestrion | 0.025533 | 0.025143-0.025954 | 0.032932 | 34.40 | verified |
| testify-external--cover | 32 | mini | 0.011604 | 0.011110-0.011738 | 0.013095 | 18.62 | verified |
| testify-external--cover | 32 | mini-deferred | 0.011218 | 0.011127-0.011601 | 0.012901 | 18.48 | verified |
| testify-external--cover-client | 4 | native | 0.003078 | 0.003060-0.003356 | 0.004318 | 14.33 | verified |
| testify-external--cover-client | 4 | orchestrion | 0.023386 | 0.022280-0.024013 | 0.027525 | 28.16 | verified |
| testify-external--cover-client | 4 | mini | 0.020124 | 0.019871-0.020351 | 0.021120 | 22.16 | verified |
| testify-external--cover-client | 4 | mini-deferred | 0.019986 | 0.019905-0.020413 | 0.021373 | 22.24 | verified |
| testify-external--cover-client | 32 | native | 0.003307 | 0.003124-0.003462 | 0.004624 | 15.01 | verified |
| testify-external--cover-client | 32 | orchestrion | 0.025663 | 0.025281-0.026668 | 0.033521 | 34.38 | verified |
| testify-external--cover-client | 32 | mini | 0.022016 | 0.020607-0.022414 | 0.025959 | 25.50 | verified |
| testify-external--cover-client | 32 | mini-deferred | 0.022025 | 0.021613-0.022778 | 0.023705 | 25.38 | verified |
| testify-external--race-cover-client | 4 | native | 1.009322 | 1.008989-1.009474 | 0.010397 | 32.80 | verified |
| testify-external--race-cover-client | 4 | orchestrion | 1.053227 | 1.052705-1.054280 | 0.061959 | 67.10 | verified |
| testify-external--race-cover-client | 4 | mini | 1.033643 | 1.033446-1.033799 | 0.035776 | 42.87 | verified |
| testify-external--race-cover-client | 4 | mini-deferred | 1.033264 | 1.032991-1.034672 | 0.034851 | 42.49 | verified |
| testify-external--race-cover-client | 32 | native | 1.009809 | 1.009439-1.010221 | 0.010980 | 35.51 | verified |
| testify-external--race-cover-client | 32 | orchestrion | 1.058201 | 1.056507-1.059143 | 0.072888 | 73.07 | verified |
| testify-external--race-cover-client | 32 | mini | 1.038577 | 1.038084-1.038707 | 0.043902 | 50.12 | verified |
| testify-external--race-cover-client | 32 | mini-deferred | 1.038700 | 1.035825-1.038999 | 0.043318 | 48.61 | verified |
| testify-external--cover-testing | 4 | native | 0.003702 | 0.003598-0.003749 | 0.004493 | 14.59 | verified |
| testify-external--cover-testing | 4 | orchestrion | 0.039288 | 0.037583-0.039342 | 0.045974 | 28.01 | verified |
| testify-external--cover-testing | 4 | mini | 0.035499 | 0.035423-0.036204 | 0.037951 | 22.43 | verified |
| testify-external--cover-testing | 4 | mini-deferred | 0.036283 | 0.035845-0.036569 | 0.037997 | 21.88 | verified |
| testify-external--cover-testing | 32 | native | 0.004002 | 0.003766-0.004048 | 0.005011 | 16.75 | verified |
| testify-external--cover-testing | 32 | orchestrion | 0.042201 | 0.041555-0.042875 | 0.054885 | 34.48 | verified |
| testify-external--cover-testing | 32 | mini | 0.038483 | 0.037832-0.038730 | 0.044593 | 25.36 | verified |
| testify-external--cover-testing | 32 | mini-deferred | 0.039018 | 0.038375-0.039232 | 0.043817 | 24.51 | verified |
| testify-external--race-cover-testing | 4 | native | 1.012068 | 1.011585-1.012211 | 0.012663 | 35.28 | verified |
| testify-external--race-cover-testing | 4 | orchestrion | 1.090319 | 1.088727-1.091354 | 0.110854 | 74.31 | verified |
| testify-external--race-cover-testing | 4 | mini | 1.073191 | 1.071837-1.073542 | 0.078189 | 45.55 | verified |
| testify-external--race-cover-testing | 4 | mini-deferred | 1.075557 | 1.074623-1.076167 | 0.077790 | 45.37 | verified |
| testify-external--race-cover-testing | 32 | native | 1.012989 | 1.012573-1.013907 | 0.013790 | 36.27 | verified |
| testify-external--race-cover-testing | 32 | orchestrion | 1.098761 | 1.096127-1.102928 | 0.126615 | 73.03 | verified |
| testify-external--race-cover-testing | 32 | mini | 1.078846 | 1.077643-1.080614 | 0.088680 | 54.84 | verified |
| testify-external--race-cover-testing | 32 | mini-deferred | 1.080935 | 1.079511-1.082662 | 0.086280 | 51.20 | verified |

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
