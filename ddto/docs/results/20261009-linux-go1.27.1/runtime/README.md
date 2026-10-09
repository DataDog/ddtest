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
| Gin | `none` | 4 | 0.183472 s | 0.227665 s (+24.1%) | 0.248406 s (+9.1%; +35.4%) | 0.251560 s (+10.5%; +37.1%) |
| Gin | `none` | 32 | 0.192854 s | 0.235763 s (+22.2%) | 0.225299 s (-4.4%; +16.8%) | 0.228771 s (-3.0%; +18.6%) |
| Chi | `none` | 4 | 26.136864 s | 26.174048 s (+0.1%) | 26.161599 s (-0.0%; +0.1%) | 26.176719 s (+0.0%; +0.2%) |
| Chi | `none` | 32 | 26.141335 s | 26.179190 s (+0.1%) | 26.172962 s (-0.0%; +0.1%) | 26.179298 s (+0.0%; +0.1%) |
| Gin | `-race` | 4 | 2.040156 s | FAIL 5/6 | 2.693232 s (vs Orchestrion unavailable; +32.0% vs Native) | 2.680854 s (vs Orchestrion unavailable; +31.4% vs Native) |
| Gin | `-race` | 32 | 1.415086 s | FAIL 6/6 | 1.582640 s (vs Orchestrion unavailable; +11.8% vs Native) | 1.587447 s (vs Orchestrion unavailable; +12.2% vs Native) |
| Gin | `-cover` | 4 | 0.188422 s | 0.484403 s (+157.1%) | 0.271141 s (-44.0%; +43.9%) | 0.273019 s (-43.6%; +44.9%) |
| Gin | `-cover` | 32 | 0.198064 s | 0.410552 s (+107.3%) | 0.240136 s (-41.5%; +21.2%) | 0.231744 s (-43.6%; +17.0%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.253092 s | 6.616408 s (+2514.2%) | 7.732937 s (+16.9%; +2955.4%) | 8.017513 s (+21.2%; +3067.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.253249 s | 6.541288 s (+2482.9%) | 6.840932 s (+4.6%; +2601.3%) | 7.461775 s (+14.1%; +2846.4%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 3.415680 s | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 3.446807 s | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 27.214190 s | 27.322207 s (+0.4%) | 27.304056 s (-0.1%; +0.3%) | 27.304624 s (-0.1%; +0.3%) |
| Chi | `-race` | 32 | 27.218039 s | 27.324007 s (+0.4%) | 27.314174 s (-0.0%; +0.4%) | 27.289865 s (-0.1%; +0.3%) |
| Chi | `-cover` | 4 | 26.136957 s | 26.229098 s (+0.4%) | 26.178705 s (-0.2%; +0.2%) | 26.185012 s (-0.2%; +0.2%) |
| Chi | `-cover` | 32 | 26.140129 s | 26.233960 s (+0.4%) | 26.177565 s (-0.2%; +0.1%) | 26.163375 s (-0.3%; +0.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 26.139077 s | 26.788571 s (+2.5%) | 26.792990 s (+0.0%; +2.5%) | 26.854130 s (+0.2%; +2.7%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 26.143078 s | 26.843831 s (+2.7%) | 26.849861 s (+0.0%; +2.7%) | 26.924933 s (+0.3%; +3.0%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 27.227274 s | 28.864119 s (+6.0%) | 28.841073 s (-0.1%; +5.9%) | 29.179477 s (+1.1%; +7.2%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 27.233273 s | 28.933591 s (+6.2%) | 28.968658 s (+0.1%; +6.4%) | 29.473376 s (+1.9%; +8.2%) |
| Testify Direct | `none` | 4 | 0.002768 s | 0.014293 s (+416.4%) | 0.009830 s (-31.2%; +255.2%) | 0.009817 s (-31.3%; +254.7%) |
| Testify Direct | `none` | 32 | 0.003034 s | 0.016014 s (+427.8%) | 0.010971 s (-31.5%; +261.6%) | 0.010855 s (-32.2%; +257.7%) |
| Testify Direct | `-race` | 4 | 1.008293 s | 1.041778 s (+3.3%) | 1.021986 s (-1.9%; +1.4%) | 1.021930 s (-1.9%; +1.4%) |
| Testify Direct | `-race` | 32 | 1.008927 s | 1.045827 s (+3.7%) | 1.023457 s (-2.1%; +1.4%) | 1.023322 s (-2.2%; +1.4%) |
| Testify Direct | `-cover` | 4 | 0.003040 s | 0.023040 s (+658.0%) | 0.010447 s (-54.7%; +243.7%) | 0.010673 s (-53.7%; +251.1%) |
| Testify Direct | `-cover` | 32 | 0.003225 s | 0.025315 s (+685.0%) | 0.012056 s (-52.4%; +273.8%) | 0.011771 s (-53.5%; +265.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.003020 s | 0.024021 s (+695.3%) | 0.019850 s (-17.4%; +557.2%) | 0.020071 s (-16.4%; +564.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.003157 s | 0.026260 s (+731.7%) | 0.022349 s (-14.9%; +607.8%) | 0.021429 s (-18.4%; +578.7%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009339 s | 1.052694 s (+4.3%) | 1.033867 s (-1.8%; +2.4%) | 1.034081 s (-1.8%; +2.5%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009875 s | 1.057152 s (+4.7%) | 1.038497 s (-1.8%; +2.8%) | 1.037893 s (-1.8%; +2.8%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003584 s | 0.038847 s (+984.0%) | 0.035444 s (-8.8%; +889.1%) | 0.036915 s (-5.0%; +930.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.003901 s | 0.041828 s (+972.2%) | 0.037961 s (-9.2%; +873.1%) | 0.040610 s (-2.9%; +940.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.011706 s | 1.089557 s (+7.7%) | 1.073968 s (-1.4%; +6.2%) | 1.083518 s (-0.6%; +7.1%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012462 s | 1.097505 s (+8.4%) | 1.081431 s (-1.5%; +6.8%) | 1.090035 s (-0.7%; +7.7%) |
| Testify External | `none` | 4 | 0.002716 s | 0.014248 s (+424.6%) | 0.009660 s (-32.2%; +255.7%) | 0.009879 s (-30.7%; +263.7%) |
| Testify External | `none` | 32 | 0.002940 s | 0.016444 s (+459.4%) | 0.011059 s (-32.7%; +276.2%) | 0.010654 s (-35.2%; +262.4%) |
| Testify External | `-race` | 4 | 1.008352 s | 1.041991 s (+3.3%) | 1.022406 s (-1.9%; +1.4%) | 1.022081 s (-1.9%; +1.4%) |
| Testify External | `-race` | 32 | 1.009078 s | 1.046242 s (+3.7%) | 1.024275 s (-2.1%; +1.5%) | 1.024131 s (-2.1%; +1.5%) |
| Testify External | `-cover` | 4 | 0.002880 s | 0.023010 s (+699.1%) | 0.010401 s (-54.8%; +261.2%) | 0.010332 s (-55.1%; +258.8%) |
| Testify External | `-cover` | 32 | 0.003031 s | 0.025533 s (+742.3%) | 0.012098 s (-52.6%; +299.1%) | 0.011503 s (-54.9%; +279.5%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.003078 s | 0.023386 s (+659.8%) | 0.020097 s (-14.1%; +552.9%) | 0.020032 s (-14.3%; +550.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.003307 s | 0.025663 s (+676.0%) | 0.022204 s (-13.5%; +571.4%) | 0.021441 s (-16.5%; +548.3%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009322 s | 1.053227 s (+4.4%) | 1.033897 s (-1.8%; +2.4%) | 1.034363 s (-1.8%; +2.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009809 s | 1.058201 s (+4.8%) | 1.038764 s (-1.8%; +2.9%) | 1.038591 s (-1.9%; +2.9%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003702 s | 0.039288 s (+961.3%) | 0.035951 s (-8.5%; +871.2%) | 0.037090 s (-5.6%; +902.0%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.004002 s | 0.042201 s (+954.5%) | 0.038650 s (-8.4%; +865.8%) | 0.040414 s (-4.2%; +909.8%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.012068 s | 1.090319 s (+7.7%) | 1.073952 s (-1.5%; +6.1%) | 1.083894 s (-0.6%; +7.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012989 s | 1.098761 s (+8.5%) | 1.080224 s (-1.7%; +6.6%) | 1.089769 s (-0.8%; +7.6%) |

## CPU, memory and dispersion

| Case | CPUs | Variant | Median (s) | Min-max (s) | CPU median (CPU-s) | Memory median (MiB) | Validation |
| --- | ---: | --- | ---: | --- | ---: | ---: | --- |
| gin | 4 | native | 0.183472 | 0.178205-0.194327 | 0.170003 | 36.64 | verified |
| gin | 4 | orchestrion | 0.227665 | 0.219127-0.241448 | 0.348156 | 60.29 | verified |
| gin | 4 | mini | 0.248406 | 0.242070-0.261829 | 0.287926 | 52.29 | verified |
| gin | 4 | mini-deferred | 0.251560 | 0.230811-0.270776 | 0.291930 | 59.57 | verified |
| gin | 32 | native | 0.192854 | 0.190069-0.194346 | 0.217764 | 42.18 | verified |
| gin | 32 | orchestrion | 0.235763 | 0.230035-0.256558 | 0.439478 | 70.67 | verified |
| gin | 32 | mini | 0.225299 | 0.220876-0.230351 | 0.360110 | 80.36 | verified |
| gin | 32 | mini-deferred | 0.228771 | 0.225139-0.231033 | 0.357808 | 74.17 | verified |
| chi | 4 | native | 26.136864 | 26.135458-26.150430 | 2.901706 | 45.64 | verified |
| chi | 4 | orchestrion | 26.174048 | 26.152724-26.184653 | 2.682397 | 79.65 | verified |
| chi | 4 | mini | 26.161599 | 26.157970-26.168654 | 3.519760 | 60.49 | verified |
| chi | 4 | mini-deferred | 26.176719 | 26.154625-26.189019 | 3.517602 | 60.80 | verified |
| chi | 32 | native | 26.141335 | 26.139699-26.141874 | 7.661383 | 94.73 | verified |
| chi | 32 | orchestrion | 26.179190 | 26.160035-26.183551 | 7.197552 | 128.05 | verified |
| chi | 32 | mini | 26.172962 | 26.163985-26.187411 | 10.584302 | 95.99 | verified |
| chi | 32 | mini-deferred | 26.179298 | 26.157268-26.288782 | 10.625830 | 85.45 | verified |
| gin--race | 4 | native | 2.040156 | 2.034008-2.041493 | 2.627885 | 199.33 | verified |
| gin--race | 4 | orchestrion | 2.139835 | 2.131945-2.151618 | 3.691318 | 332.23 | failed |
| gin--race | 4 | mini | 2.693232 | 2.638739-2.782628 | 3.142573 | 206.81 | verified |
| gin--race | 4 | mini-deferred | 2.680854 | 2.665472-4.518540 | 3.240694 | 210.93 | verified |
| gin--race | 32 | native | 1.415086 | 1.407846-1.426243 | 2.725913 | 262.89 | verified |
| gin--race | 32 | orchestrion | 1.731117 | 1.724280-1.740767 | 3.893175 | 418.80 | failed |
| gin--race | 32 | mini | 1.582640 | 1.579303-1.599878 | 3.266354 | 392.66 | verified |
| gin--race | 32 | mini-deferred | 1.587447 | 1.572135-1.605306 | 3.261092 | 393.93 | verified |
| gin--cover | 4 | native | 0.188422 | 0.186399-0.190422 | 0.192585 | 35.87 | verified |
| gin--cover | 4 | orchestrion | 0.484403 | 0.471239-0.497941 | 1.289090 | 92.60 | verified |
| gin--cover | 4 | mini | 0.271141 | 0.263797-0.277324 | 0.340702 | 60.23 | verified |
| gin--cover | 4 | mini-deferred | 0.273019 | 0.259922-0.295516 | 0.362552 | 54.83 | verified |
| gin--cover | 32 | native | 0.198064 | 0.194113-0.219861 | 0.238151 | 42.11 | verified |
| gin--cover | 32 | orchestrion | 0.410552 | 0.404105-0.427463 | 3.361391 | 165.18 | verified |
| gin--cover | 32 | mini | 0.240136 | 0.226932-0.242677 | 0.401416 | 79.34 | verified |
| gin--cover | 32 | mini-deferred | 0.231744 | 0.231731-0.238835 | 0.393789 | 83.75 | verified |
| gin--cover-client | 4 | native | 0.253092 | 0.250583-0.263731 | 0.385578 | 35.73 | verified |
| gin--cover-client | 4 | orchestrion | 6.616408 | 6.582757-6.973707 | 14.125127 | 98.61 | verified |
| gin--cover-client | 4 | mini | 7.732937 | 7.193216-37.678580 | 16.448600 | 117.11 | verified |
| gin--cover-client | 4 | mini-deferred | 8.017513 | 7.735388-18.853571 | 15.870363 | 122.61 | verified |
| gin--cover-client | 32 | native | 0.253249 | 0.250254-0.262717 | 0.420659 | 52.08 | verified |
| gin--cover-client | 32 | orchestrion | 6.541288 | 6.516528-6.660464 | 16.820350 | 200.94 | verified |
| gin--cover-client | 32 | mini | 6.840932 | 6.687625-7.411690 | 18.091906 | 218.40 | verified |
| gin--cover-client | 32 | mini-deferred | 7.461775 | 7.403065-13.250562 | 18.311117 | 224.38 | verified |
| gin--race-cover-client | 4 | native | 3.415680 | 3.371987-3.544310 | 8.500800 | 243.92 | verified |
| gin--race-cover-client | 4 | orchestrion | 18.491987 | 18.428509-18.560299 | 43.477136 | 408.91 | failed |
| gin--race-cover-client | 4 | mini | 19.816303 | 19.627705-20.154370 | 44.813240 | 221.06 | unverified |
| gin--race-cover-client | 4 | mini-deferred | 24.002895 | 23.911785-24.267325 | 47.128764 | 212.66 | unverified |
| gin--race-cover-client | 32 | native | 3.446807 | 3.413063-3.583871 | 8.572765 | 283.77 | verified |
| gin--race-cover-client | 32 | orchestrion | 18.746791 | 18.707628-18.803462 | 48.207009 | 470.64 | failed |
| gin--race-cover-client | 32 | mini | 19.146428 | 19.089004-19.353408 | 49.408586 | 371.16 | unverified |
| gin--race-cover-client | 32 | mini-deferred | 23.889757 | 23.757340-24.407956 | 51.785920 | 371.69 | unverified |
| chi--race | 4 | native | 27.214190 | 27.209690-27.216559 | 44.707255 | 2958.75 | verified |
| chi--race | 4 | orchestrion | 27.322207 | 27.308320-27.325687 | 46.083466 | 2979.35 | verified |
| chi--race | 4 | mini | 27.304056 | 27.280845-27.319455 | 45.442856 | 3023.44 | verified |
| chi--race | 4 | mini-deferred | 27.304624 | 27.292682-27.308553 | 45.277771 | 3018.99 | verified |
| chi--race | 32 | native | 27.218039 | 27.214792-27.225713 | 225.164169 | 3068.80 | verified |
| chi--race | 32 | orchestrion | 27.324007 | 27.302995-27.329207 | 147.048678 | 3112.38 | verified |
| chi--race | 32 | mini | 27.314174 | 27.284726-27.318042 | 243.446948 | 3098.31 | verified |
| chi--race | 32 | mini-deferred | 27.289865 | 27.287857-27.331263 | 239.302944 | 3111.23 | verified |
| chi--cover | 4 | native | 26.136957 | 26.134604-26.140277 | 3.004046 | 43.00 | verified |
| chi--cover | 4 | orchestrion | 26.229098 | 26.224293-26.236508 | 3.008069 | 84.96 | verified |
| chi--cover | 4 | mini | 26.178705 | 26.158270-26.182340 | 3.418587 | 52.56 | verified |
| chi--cover | 4 | mini-deferred | 26.185012 | 26.180907-26.204015 | 3.486750 | 60.12 | verified |
| chi--cover | 32 | native | 26.140129 | 26.138273-26.144419 | 8.704006 | 88.64 | verified |
| chi--cover | 32 | orchestrion | 26.233960 | 26.228725-26.238064 | 8.804965 | 149.12 | verified |
| chi--cover | 32 | mini | 26.177565 | 26.158887-26.187216 | 10.020844 | 93.23 | verified |
| chi--cover | 32 | mini-deferred | 26.163375 | 26.156851-26.182973 | 10.149974 | 87.23 | verified |
| chi--cover-client | 4 | native | 26.139077 | 26.138678-26.143371 | 3.273207 | 45.12 | verified |
| chi--cover-client | 4 | orchestrion | 26.788571 | 26.783171-26.806840 | 4.292763 | 98.80 | verified |
| chi--cover-client | 4 | mini | 26.792990 | 26.788758-26.800287 | 4.843039 | 59.80 | verified |
| chi--cover-client | 4 | mini-deferred | 26.854130 | 26.850604-26.859927 | 4.832133 | 63.59 | verified |
| chi--cover-client | 32 | native | 26.143078 | 26.140160-26.143884 | 12.007906 | 84.12 | verified |
| chi--cover-client | 32 | orchestrion | 26.843831 | 26.825489-26.848656 | 12.536681 | 145.21 | verified |
| chi--cover-client | 32 | mini | 26.849861 | 26.838089-26.856414 | 14.442048 | 100.48 | verified |
| chi--cover-client | 32 | mini-deferred | 26.924933 | 26.903578-26.934125 | 14.333217 | 103.83 | verified |
| chi--race-cover-client | 4 | native | 27.227274 | 27.219629-27.232085 | 55.070241 | 2944.42 | verified |
| chi--race-cover-client | 4 | orchestrion | 28.864119 | 28.817819-28.912666 | 59.183012 | 3073.44 | verified |
| chi--race-cover-client | 4 | mini | 28.841073 | 28.822566-28.848130 | 57.568214 | 2480.98 | verified |
| chi--race-cover-client | 4 | mini-deferred | 29.179477 | 29.152961-29.330168 | 57.409206 | 2478.89 | verified |
| chi--race-cover-client | 32 | native | 27.233273 | 27.230656-27.235937 | 243.743850 | 2940.62 | verified |
| chi--race-cover-client | 32 | orchestrion | 28.933591 | 28.929454-28.939520 | 189.447900 | 3072.50 | verified |
| chi--race-cover-client | 32 | mini | 28.968658 | 28.944008-28.998972 | 268.598026 | 2503.12 | verified |
| chi--race-cover-client | 32 | mini-deferred | 29.473376 | 29.458146-29.520123 | 267.346168 | 2493.96 | verified |
| testify-direct--normal | 4 | native | 0.002768 | 0.002659-0.002871 | 0.003788 | 14.54 | verified |
| testify-direct--normal | 4 | orchestrion | 0.014293 | 0.013946-0.014714 | 0.018899 | 26.62 | verified |
| testify-direct--normal | 4 | mini | 0.009830 | 0.009736-0.012692 | 0.011117 | 17.25 | verified |
| testify-direct--normal | 4 | mini-deferred | 0.009817 | 0.009698-0.010175 | 0.010934 | 15.44 | verified |
| testify-direct--normal | 32 | native | 0.003034 | 0.002834-0.003637 | 0.004573 | 14.98 | verified |
| testify-direct--normal | 32 | orchestrion | 0.016014 | 0.015913-0.016351 | 0.023118 | 29.43 | verified |
| testify-direct--normal | 32 | mini | 0.010971 | 0.010742-0.011288 | 0.012983 | 19.25 | verified |
| testify-direct--normal | 32 | mini-deferred | 0.010855 | 0.010479-0.011538 | 0.012270 | 18.38 | verified |
| testify-direct--race | 4 | native | 1.008293 | 1.008201-1.008790 | 0.009414 | 34.08 | verified |
| testify-direct--race | 4 | orchestrion | 1.041778 | 1.040832-1.041896 | 0.051832 | 67.46 | verified |
| testify-direct--race | 4 | mini | 1.021986 | 1.021372-1.022070 | 0.023669 | 42.67 | verified |
| testify-direct--race | 4 | mini-deferred | 1.021930 | 1.021411-1.022692 | 0.023338 | 42.42 | verified |
| testify-direct--race | 32 | native | 1.008927 | 1.008471-1.009004 | 0.010023 | 37.21 | verified |
| testify-direct--race | 32 | orchestrion | 1.045827 | 1.045513-1.046870 | 0.063160 | 73.38 | verified |
| testify-direct--race | 32 | mini | 1.023457 | 1.022674-1.024191 | 0.025826 | 44.91 | verified |
| testify-direct--race | 32 | mini-deferred | 1.023322 | 1.023119-1.024010 | 0.025126 | 44.12 | verified |
| testify-direct--cover | 4 | native | 0.003040 | 0.002961-0.003208 | 0.003917 | 14.48 | verified |
| testify-direct--cover | 4 | orchestrion | 0.023040 | 0.022565-0.023689 | 0.026835 | 29.69 | verified |
| testify-direct--cover | 4 | mini | 0.010447 | 0.010324-0.010792 | 0.011987 | 17.49 | verified |
| testify-direct--cover | 4 | mini-deferred | 0.010673 | 0.010326-0.010832 | 0.012006 | 17.41 | verified |
| testify-direct--cover | 32 | native | 0.003225 | 0.003185-0.003667 | 0.004487 | 14.72 | verified |
| testify-direct--cover | 32 | orchestrion | 0.025315 | 0.024757-0.025785 | 0.034084 | 33.78 | verified |
| testify-direct--cover | 32 | mini | 0.012056 | 0.011135-0.012773 | 0.013315 | 20.41 | verified |
| testify-direct--cover | 32 | mini-deferred | 0.011771 | 0.011567-0.012939 | 0.013102 | 17.89 | verified |
| testify-direct--cover-client | 4 | native | 0.003020 | 0.002933-0.003363 | 0.003895 | 14.33 | verified |
| testify-direct--cover-client | 4 | orchestrion | 0.024021 | 0.023243-0.024309 | 0.028469 | 27.64 | verified |
| testify-direct--cover-client | 4 | mini | 0.019850 | 0.019397-0.020199 | 0.021331 | 22.24 | verified |
| testify-direct--cover-client | 4 | mini-deferred | 0.020071 | 0.019715-0.020205 | 0.021159 | 22.16 | verified |
| testify-direct--cover-client | 32 | native | 0.003157 | 0.003060-0.003179 | 0.004158 | 14.77 | verified |
| testify-direct--cover-client | 32 | orchestrion | 0.026260 | 0.025792-0.026927 | 0.034280 | 34.04 | verified |
| testify-direct--cover-client | 32 | mini | 0.022349 | 0.020643-0.023299 | 0.026308 | 25.32 | verified |
| testify-direct--cover-client | 32 | mini-deferred | 0.021429 | 0.021312-0.022181 | 0.023133 | 25.55 | verified |
| testify-direct--race-cover-client | 4 | native | 1.009339 | 1.008955-1.009570 | 0.010245 | 33.97 | verified |
| testify-direct--race-cover-client | 4 | orchestrion | 1.052694 | 1.051837-1.053167 | 0.061688 | 66.72 | verified |
| testify-direct--race-cover-client | 4 | mini | 1.033867 | 1.033279-1.034051 | 0.035717 | 45.40 | verified |
| testify-direct--race-cover-client | 4 | mini-deferred | 1.034081 | 1.033307-1.034225 | 0.035139 | 44.64 | verified |
| testify-direct--race-cover-client | 32 | native | 1.009875 | 1.009629-1.010124 | 0.011177 | 35.75 | verified |
| testify-direct--race-cover-client | 32 | orchestrion | 1.057152 | 1.055745-1.061098 | 0.072301 | 72.09 | verified |
| testify-direct--race-cover-client | 32 | mini | 1.038497 | 1.038166-1.038690 | 0.043902 | 51.64 | verified |
| testify-direct--race-cover-client | 32 | mini-deferred | 1.037893 | 1.036552-1.039392 | 0.041762 | 50.28 | verified |
| testify-direct--cover-testing | 4 | native | 0.003584 | 0.003514-0.003702 | 0.004669 | 14.81 | verified |
| testify-direct--cover-testing | 4 | orchestrion | 0.038847 | 0.038238-0.039334 | 0.045965 | 27.74 | verified |
| testify-direct--cover-testing | 4 | mini | 0.035444 | 0.034721-0.036485 | 0.039505 | 20.99 | verified |
| testify-direct--cover-testing | 4 | mini-deferred | 0.036915 | 0.036813-0.037939 | 0.039029 | 21.96 | verified |
| testify-direct--cover-testing | 32 | native | 0.003901 | 0.003769-0.004127 | 0.005086 | 14.96 | verified |
| testify-direct--cover-testing | 32 | orchestrion | 0.041828 | 0.039988-0.042617 | 0.053131 | 35.20 | verified |
| testify-direct--cover-testing | 32 | mini | 0.037961 | 0.037742-0.039615 | 0.045773 | 25.30 | verified |
| testify-direct--cover-testing | 32 | mini-deferred | 0.040610 | 0.040242-0.040671 | 0.045581 | 24.98 | verified |
| testify-direct--race-cover-testing | 4 | native | 1.011706 | 1.011577-1.011857 | 0.012413 | 35.34 | verified |
| testify-direct--race-cover-testing | 4 | orchestrion | 1.089557 | 1.088832-1.091163 | 0.109470 | 74.43 | verified |
| testify-direct--race-cover-testing | 4 | mini | 1.073968 | 1.073414-1.074565 | 0.087876 | 49.57 | verified |
| testify-direct--race-cover-testing | 4 | mini-deferred | 1.083518 | 1.082418-1.084022 | 0.086394 | 49.82 | verified |
| testify-direct--race-cover-testing | 32 | native | 1.012462 | 1.012319-1.012960 | 0.013447 | 36.73 | verified |
| testify-direct--race-cover-testing | 32 | orchestrion | 1.097505 | 1.096087-1.098500 | 0.126504 | 83.53 | verified |
| testify-direct--race-cover-testing | 32 | mini | 1.081431 | 1.080037-1.082955 | 0.099048 | 56.67 | verified |
| testify-direct--race-cover-testing | 32 | mini-deferred | 1.090035 | 1.088823-1.092258 | 0.097428 | 56.12 | verified |
| testify-external--normal | 4 | native | 0.002716 | 0.002639-0.002796 | 0.003568 | 14.34 | verified |
| testify-external--normal | 4 | orchestrion | 0.014248 | 0.013731-0.014540 | 0.019312 | 27.12 | verified |
| testify-external--normal | 4 | mini | 0.009660 | 0.009482-0.010047 | 0.011193 | 17.00 | verified |
| testify-external--normal | 4 | mini-deferred | 0.009879 | 0.009615-0.010237 | 0.010960 | 17.08 | verified |
| testify-external--normal | 32 | native | 0.002940 | 0.002921-0.003123 | 0.004025 | 14.48 | verified |
| testify-external--normal | 32 | orchestrion | 0.016444 | 0.016203-0.016616 | 0.025720 | 32.32 | verified |
| testify-external--normal | 32 | mini | 0.011059 | 0.010852-0.011353 | 0.012818 | 18.90 | verified |
| testify-external--normal | 32 | mini-deferred | 0.010654 | 0.010180-0.011201 | 0.012159 | 18.09 | verified |
| testify-external--race | 4 | native | 1.008352 | 1.008251-1.008555 | 0.009517 | 33.83 | verified |
| testify-external--race | 4 | orchestrion | 1.041991 | 1.041745-1.042499 | 0.055411 | 68.84 | verified |
| testify-external--race | 4 | mini | 1.022406 | 1.021872-1.023411 | 0.023997 | 42.92 | verified |
| testify-external--race | 4 | mini-deferred | 1.022081 | 1.021833-1.022561 | 0.023873 | 41.21 | verified |
| testify-external--race | 32 | native | 1.009078 | 1.008949-1.009489 | 0.010453 | 36.97 | verified |
| testify-external--race | 32 | orchestrion | 1.046242 | 1.045924-1.046899 | 0.064850 | 75.32 | verified |
| testify-external--race | 32 | mini | 1.024275 | 1.024094-1.024667 | 0.026311 | 45.23 | verified |
| testify-external--race | 32 | mini-deferred | 1.024131 | 1.022842-1.024399 | 0.026136 | 44.54 | verified |
| testify-external--cover | 4 | native | 0.002880 | 0.002857-0.003041 | 0.003944 | 14.23 | verified |
| testify-external--cover | 4 | orchestrion | 0.023010 | 0.022500-0.023789 | 0.027232 | 29.42 | verified |
| testify-external--cover | 4 | mini | 0.010401 | 0.010190-0.010863 | 0.011972 | 17.26 | verified |
| testify-external--cover | 4 | mini-deferred | 0.010332 | 0.009930-0.010525 | 0.011438 | 17.46 | verified |
| testify-external--cover | 32 | native | 0.003031 | 0.002962-0.003418 | 0.004082 | 15.02 | verified |
| testify-external--cover | 32 | orchestrion | 0.025533 | 0.025143-0.025954 | 0.032932 | 34.40 | verified |
| testify-external--cover | 32 | mini | 0.012098 | 0.011458-0.012592 | 0.013300 | 18.82 | verified |
| testify-external--cover | 32 | mini-deferred | 0.011503 | 0.010846-0.011745 | 0.013355 | 17.78 | verified |
| testify-external--cover-client | 4 | native | 0.003078 | 0.003060-0.003356 | 0.004318 | 14.33 | verified |
| testify-external--cover-client | 4 | orchestrion | 0.023386 | 0.022280-0.024013 | 0.027525 | 28.16 | verified |
| testify-external--cover-client | 4 | mini | 0.020097 | 0.019501-0.020528 | 0.021714 | 23.91 | verified |
| testify-external--cover-client | 4 | mini-deferred | 0.020032 | 0.019750-0.020785 | 0.021671 | 21.76 | verified |
| testify-external--cover-client | 32 | native | 0.003307 | 0.003124-0.003462 | 0.004624 | 15.01 | verified |
| testify-external--cover-client | 32 | orchestrion | 0.025663 | 0.025281-0.026668 | 0.033521 | 34.38 | verified |
| testify-external--cover-client | 32 | mini | 0.022204 | 0.021607-0.022542 | 0.027407 | 25.76 | verified |
| testify-external--cover-client | 32 | mini-deferred | 0.021441 | 0.020577-0.022203 | 0.023546 | 25.03 | verified |
| testify-external--race-cover-client | 4 | native | 1.009322 | 1.008989-1.009474 | 0.010397 | 32.80 | verified |
| testify-external--race-cover-client | 4 | orchestrion | 1.053227 | 1.052705-1.054280 | 0.061959 | 67.10 | verified |
| testify-external--race-cover-client | 4 | mini | 1.033897 | 1.033476-1.034238 | 0.035560 | 45.15 | verified |
| testify-external--race-cover-client | 4 | mini-deferred | 1.034363 | 1.033756-1.034938 | 0.036050 | 44.59 | verified |
| testify-external--race-cover-client | 32 | native | 1.009809 | 1.009439-1.010221 | 0.010980 | 35.51 | verified |
| testify-external--race-cover-client | 32 | orchestrion | 1.058201 | 1.056507-1.059143 | 0.072888 | 73.07 | verified |
| testify-external--race-cover-client | 32 | mini | 1.038764 | 1.038291-1.039223 | 0.043954 | 51.60 | verified |
| testify-external--race-cover-client | 32 | mini-deferred | 1.038591 | 1.037954-1.039391 | 0.043236 | 50.85 | verified |
| testify-external--cover-testing | 4 | native | 0.003702 | 0.003598-0.003749 | 0.004493 | 14.59 | verified |
| testify-external--cover-testing | 4 | orchestrion | 0.039288 | 0.037583-0.039342 | 0.045974 | 28.01 | verified |
| testify-external--cover-testing | 4 | mini | 0.035951 | 0.035450-0.036176 | 0.039765 | 24.17 | verified |
| testify-external--cover-testing | 4 | mini-deferred | 0.037090 | 0.036659-0.037351 | 0.039052 | 22.54 | verified |
| testify-external--cover-testing | 32 | native | 0.004002 | 0.003766-0.004048 | 0.005011 | 16.75 | verified |
| testify-external--cover-testing | 32 | orchestrion | 0.042201 | 0.041555-0.042875 | 0.054885 | 34.48 | verified |
| testify-external--cover-testing | 32 | mini | 0.038650 | 0.038195-0.039842 | 0.045846 | 25.59 | verified |
| testify-external--cover-testing | 32 | mini-deferred | 0.040414 | 0.039468-0.041616 | 0.046022 | 26.49 | verified |
| testify-external--race-cover-testing | 4 | native | 1.012068 | 1.011585-1.012211 | 0.012663 | 35.28 | verified |
| testify-external--race-cover-testing | 4 | orchestrion | 1.090319 | 1.088727-1.091354 | 0.110854 | 74.31 | verified |
| testify-external--race-cover-testing | 4 | mini | 1.073952 | 1.073677-1.078302 | 0.087959 | 49.44 | verified |
| testify-external--race-cover-testing | 4 | mini-deferred | 1.083894 | 1.082843-1.084314 | 0.087781 | 49.70 | verified |
| testify-external--race-cover-testing | 32 | native | 1.012989 | 1.012573-1.013907 | 0.013790 | 36.27 | verified |
| testify-external--race-cover-testing | 32 | orchestrion | 1.098761 | 1.096127-1.102928 | 0.126615 | 73.03 | verified |
| testify-external--race-cover-testing | 32 | mini | 1.080224 | 1.079214-1.081028 | 0.099655 | 58.78 | verified |
| testify-external--race-cover-testing | 32 | mini-deferred | 1.089769 | 1.088881-1.090347 | 0.096550 | 54.39 | verified |

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
