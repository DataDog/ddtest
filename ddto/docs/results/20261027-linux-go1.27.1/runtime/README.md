# Runtime comparisons

Three measured executions follow one warmup at each CPU count. All package binaries
come from the original subject sources, without the build-edit markers.
Timings include startup, concurrent tests, settings and final delivery to loopback HTTP.
Receiver setup, decoding and compilation are outside the timed scope.

Native, Orchestrion, Mini and Mini deferred ran together, in rotating order,
from the same revision. Test inventories match Native; CI inventories use the
recorded SDK reference. Native Examples absent from that reference are checked against
the executable function/source table and added explicitly.

Orchestrion's percentage compares time against Native. Mini's percentages compare
Orchestrion first, then Native. Positive values mean more time; negative values mean less.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini | Mini deferred |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.189747 s | 0.244531 s (+28.9%) | 0.220352 s (-9.9%; +16.1%) | 0.224050 s (-8.4%; +18.1%) |
| Gin | `none` | 32 | 0.197496 s | 0.238528 s (+20.8%) | 0.218361 s (-8.5%; +10.6%) | 0.217293 s (-8.9%; +10.0%) |
| Chi | `none` | 4 | 26.141200 s | 26.180264 s (+0.1%) | 26.155881 s (-0.1%; +0.1%) | 26.156406 s (-0.1%; +0.1%) |
| Chi | `none` | 32 | 26.141928 s | 26.158359 s (+0.1%) | 26.154071 s (-0.0%; +0.0%) | 26.161188 s (+0.0%; +0.1%) |
| Gin | `-race` | 4 | 2.399424 s | FAIL 4/4 | 2.540887 s (vs Orchestrion unavailable; +5.9% vs Native) | 2.547634 s (vs Orchestrion unavailable; +6.2% vs Native) |
| Gin | `-race` | 32 | 1.417234 s | FAIL 4/4 | 1.548181 s (vs Orchestrion unavailable; +9.2% vs Native) | 1.552483 s (vs Orchestrion unavailable; +9.5% vs Native) |
| Gin | `-cover` | 4 | 0.193523 s | 0.491460 s (+154.0%) | 0.228745 s (-53.5%; +18.2%) | 0.233224 s (-52.5%; +20.5%) |
| Gin | `-cover` | 32 | 0.200504 s | 0.408320 s (+103.6%) | 0.227771 s (-44.2%; +13.6%) | 0.222946 s (-45.4%; +11.2%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.253945 s | 6.733022 s (+2551.4%) | 0.559686 s (-91.7%; +120.4%) | 0.555327 s (-91.8%; +118.7%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.263906 s | 6.833507 s (+2489.4%) | 0.497622 s (-92.7%; +88.6%) | 0.490508 s (-92.8%; +85.9%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 4.373225 s | FAIL 3/4 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 3.464253 s | FAIL 3/4 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 27.217195 s | 27.316479 s (+0.4%) | 27.267557 s (-0.2%; +0.2%) | 27.268468 s (-0.2%; +0.2%) |
| Chi | `-race` | 32 | 27.218182 s | 27.323183 s (+0.4%) | 27.280449 s (-0.2%; +0.2%) | 27.279931 s (-0.2%; +0.2%) |
| Chi | `-cover` | 4 | 26.136404 s | 26.235312 s (+0.4%) | 26.156020 s (-0.3%; +0.1%) | 26.153002 s (-0.3%; +0.1%) |
| Chi | `-cover` | 32 | 26.140345 s | 26.234413 s (+0.4%) | 26.162430 s (-0.3%; +0.1%) | 26.162607 s (-0.3%; +0.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 26.142036 s | 26.814884 s (+2.6%) | 26.239221 s (-2.1%; +0.4%) | 26.242925 s (-2.1%; +0.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 26.151264 s | 26.868956 s (+2.7%) | 26.237436 s (-2.4%; +0.3%) | 26.235773 s (-2.4%; +0.3%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 27.234881 s | 28.909904 s (+6.2%) | 27.402483 s (-5.2%; +0.6%) | 27.403504 s (-5.2%; +0.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 27.238645 s | 28.957277 s (+6.3%) | 27.398835 s (-5.4%; +0.6%) | 27.408764 s (-5.3%; +0.6%) |
| Testify Direct | `none` | 4 | 0.002854 s | 0.014925 s (+422.9%) | 0.010428 s (-30.1%; +265.4%) | 0.010546 s (-29.3%; +269.5%) |
| Testify Direct | `none` | 32 | 0.003185 s | 0.016874 s (+429.7%) | 0.011855 s (-29.7%; +272.2%) | 0.011521 s (-31.7%; +261.7%) |
| Testify Direct | `-race` | 4 | 1.012491 s | 1.047072 s (+3.4%) | 1.025652 s (-2.0%; +1.3%) | 1.026143 s (-2.0%; +1.3%) |
| Testify Direct | `-race` | 32 | 1.013260 s | 1.051256 s (+3.7%) | 1.028847 s (-2.1%; +1.5%) | 1.027797 s (-2.2%; +1.4%) |
| Testify Direct | `-cover` | 4 | 0.003313 s | 0.026283 s (+693.3%) | 0.010900 s (-58.5%; +229.0%) | 0.011177 s (-57.5%; +237.4%) |
| Testify Direct | `-cover` | 32 | 0.003558 s | 0.028184 s (+692.2%) | 0.011860 s (-57.9%; +233.3%) | 0.012453 s (-55.8%; +250.0%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.003425 s | 0.025674 s (+649.5%) | 0.021146 s (-17.6%; +517.4%) | 0.020891 s (-18.6%; +509.9%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.003649 s | 0.028813 s (+689.5%) | 0.023148 s (-19.7%; +534.3%) | 0.022654 s (-21.4%; +520.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.013338 s | 1.058891 s (+4.5%) | 1.037102 s (-2.1%; +2.3%) | 1.036885 s (-2.1%; +2.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.013939 s | 1.064704 s (+5.0%) | 1.041784 s (-2.2%; +2.7%) | 1.041358 s (-2.2%; +2.7%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.004080 s | 0.041748 s (+923.2%) | 0.037021 s (-11.3%; +807.3%) | 0.037930 s (-9.1%; +829.6%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.004501 s | 0.045384 s (+908.4%) | 0.039348 s (-13.3%; +774.3%) | 0.041142 s (-9.3%; +814.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.015787 s | 1.098133 s (+8.1%) | 1.076746 s (-1.9%; +6.0%) | 1.078681 s (-1.8%; +6.2%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.016848 s | 1.104561 s (+8.6%) | 1.084061 s (-1.9%; +6.6%) | 1.085597 s (-1.7%; +6.8%) |
| Testify External | `none` | 4 | 0.002879 s | 0.014796 s (+413.9%) | 0.010448 s (-29.4%; +262.9%) | 0.010295 s (-30.4%; +257.6%) |
| Testify External | `none` | 32 | 0.003148 s | 0.016865 s (+435.8%) | 0.011802 s (-30.0%; +274.9%) | 0.011919 s (-29.3%; +278.6%) |
| Testify External | `-race` | 4 | 1.012443 s | 1.047059 s (+3.4%) | 1.026178 s (-2.0%; +1.4%) | 1.026276 s (-2.0%; +1.4%) |
| Testify External | `-race` | 32 | 1.013060 s | 1.051367 s (+3.8%) | 1.028427 s (-2.2%; +1.5%) | 1.028101 s (-2.2%; +1.5%) |
| Testify External | `-cover` | 4 | 0.003098 s | 0.024109 s (+678.2%) | 0.010629 s (-55.9%; +243.1%) | 0.010589 s (-56.1%; +241.8%) |
| Testify External | `-cover` | 32 | 0.003424 s | 0.027477 s (+702.4%) | 0.012355 s (-55.0%; +260.8%) | 0.013088 s (-52.4%; +282.2%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.003290 s | 0.026501 s (+705.5%) | 0.021840 s (-17.6%; +563.8%) | 0.021144 s (-20.2%; +542.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.003489 s | 0.028923 s (+729.1%) | 0.023363 s (-19.2%; +569.7%) | 0.023171 s (-19.9%; +564.2%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.013005 s | 1.058745 s (+4.5%) | 1.036825 s (-2.1%; +2.4%) | 1.038533 s (-1.9%; +2.5%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.013985 s | 1.064254 s (+5.0%) | 1.043195 s (-2.0%; +2.9%) | 1.041607 s (-2.1%; +2.7%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.004115 s | 0.041661 s (+912.5%) | 0.037569 s (-9.8%; +813.0%) | 0.038238 s (-8.2%; +829.3%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.004350 s | 0.046354 s (+965.6%) | 0.040807 s (-12.0%; +838.1%) | 0.040771 s (-12.0%; +837.2%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.016169 s | 1.097673 s (+8.0%) | 1.077372 s (-1.8%; +6.0%) | 1.078587 s (-1.7%; +6.1%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.016639 s | 1.104211 s (+8.6%) | 1.083696 s (-1.9%; +6.6%) | 1.084777 s (-1.8%; +6.7%) |

## CPU, memory and dispersion

| Case | CPUs | Variant | Median (s) | Min-max (s) | CPU median (CPU-s) | Memory median (MiB) | Validation |
| --- | ---: | --- | ---: | --- | ---: | ---: | --- |
| gin | 4 | native | 0.189747 | 0.189112-0.192056 | 0.176254 | 35.08 | verified |
| gin | 4 | orchestrion | 0.244531 | 0.236172-0.247306 | 0.346597 | 55.38 | verified |
| gin | 4 | mini | 0.220352 | 0.218037-0.224416 | 0.266617 | 38.47 | verified |
| gin | 4 | mini-deferred | 0.224050 | 0.218294-0.227667 | 0.268106 | 42.02 | verified |
| gin | 32 | native | 0.197496 | 0.190584-0.203991 | 0.223474 | 42.71 | verified |
| gin | 32 | orchestrion | 0.238528 | 0.220584-0.243343 | 0.409430 | 81.25 | verified |
| gin | 32 | mini | 0.218361 | 0.215917-0.218904 | 0.341416 | 52.82 | verified |
| gin | 32 | mini-deferred | 0.217293 | 0.216939-0.224078 | 0.336442 | 53.66 | verified |
| chi | 4 | native | 26.141200 | 26.140814-26.146713 | 2.957290 | 34.20 | verified |
| chi | 4 | orchestrion | 26.180264 | 26.156183-26.192958 | 2.719731 | 57.09 | verified |
| chi | 4 | mini | 26.155881 | 26.149737-26.160222 | 3.331114 | 33.54 | verified |
| chi | 4 | mini-deferred | 26.156406 | 26.147813-26.157849 | 3.301731 | 44.99 | verified |
| chi | 32 | native | 26.141928 | 26.138300-26.142382 | 7.366204 | 106.09 | verified |
| chi | 32 | orchestrion | 26.158359 | 26.157809-26.159101 | 7.563627 | 133.81 | verified |
| chi | 32 | mini | 26.154071 | 26.151985-26.156803 | 9.793593 | 91.48 | verified |
| chi | 32 | mini-deferred | 26.161188 | 26.160442-26.161810 | 9.640326 | 94.08 | verified |
| gin--race | 4 | native | 2.399424 | 2.397295-2.399697 | 2.629246 | 169.39 | verified |
| gin--race | 4 | orchestrion | 2.137369 | 2.129112-2.137879 | 3.619600 | 326.70 | failed |
| gin--race | 4 | mini | 2.540887 | 2.528290-2.545763 | 2.999231 | 188.30 | verified |
| gin--race | 4 | mini-deferred | 2.547634 | 2.537451-2.549304 | 3.007191 | 189.93 | verified |
| gin--race | 32 | native | 1.417234 | 1.412574-1.424377 | 2.725570 | 264.85 | verified |
| gin--race | 32 | orchestrion | 1.699467 | 1.694300-1.710291 | 3.763945 | 398.85 | failed |
| gin--race | 32 | mini | 1.548181 | 1.542617-1.551829 | 3.157626 | 328.89 | verified |
| gin--race | 32 | mini-deferred | 1.552483 | 1.546347-1.552947 | 3.163458 | 324.06 | verified |
| gin--cover | 4 | native | 0.193523 | 0.189207-0.196929 | 0.198858 | 35.15 | verified |
| gin--cover | 4 | orchestrion | 0.491460 | 0.490286-0.499351 | 1.281019 | 94.90 | verified |
| gin--cover | 4 | mini | 0.228745 | 0.225167-0.229974 | 0.295526 | 37.32 | verified |
| gin--cover | 4 | mini-deferred | 0.233224 | 0.231472-0.234382 | 0.297948 | 39.07 | verified |
| gin--cover | 32 | native | 0.200504 | 0.200452-0.203663 | 0.249723 | 41.73 | verified |
| gin--cover | 32 | orchestrion | 0.408320 | 0.404297-0.416339 | 3.255863 | 172.20 | verified |
| gin--cover | 32 | mini | 0.227771 | 0.220358-0.233554 | 0.362042 | 52.78 | verified |
| gin--cover | 32 | mini-deferred | 0.222946 | 0.222400-0.224607 | 0.370408 | 53.48 | verified |
| gin--cover-client | 4 | native | 0.253945 | 0.253852-0.255221 | 0.385169 | 36.34 | verified |
| gin--cover-client | 4 | orchestrion | 6.733022 | 6.732994-6.754335 | 14.293983 | 89.76 | verified |
| gin--cover-client | 4 | mini | 0.559686 | 0.544581-0.565739 | 1.503971 | 83.13 | verified |
| gin--cover-client | 4 | mini-deferred | 0.555327 | 0.553776-0.571547 | 1.467634 | 84.57 | verified |
| gin--cover-client | 32 | native | 0.263906 | 0.263517-0.268367 | 0.433874 | 42.96 | verified |
| gin--cover-client | 32 | orchestrion | 6.833507 | 6.813361-6.842824 | 17.318569 | 167.95 | verified |
| gin--cover-client | 32 | mini | 0.497622 | 0.480081-0.503672 | 3.732056 | 147.78 | verified |
| gin--cover-client | 32 | mini-deferred | 0.490508 | 0.483507-0.512974 | 3.728726 | 147.82 | verified |
| gin--race-cover-client | 4 | native | 4.373225 | 4.352830-4.441780 | 8.337357 | 179.62 | verified |
| gin--race-cover-client | 4 | orchestrion | 19.809554 | 19.671301-20.098343 | 44.416915 | 311.16 | failed |
| gin--race-cover-client | 4 | mini | 5.031121 | 4.975827-5.231174 | 10.445793 | 205.46 | unverified |
| gin--race-cover-client | 4 | mini-deferred | 5.027697 | 4.920072-5.067150 | 10.410957 | 202.26 | unverified |
| gin--race-cover-client | 32 | native | 3.464253 | 3.381677-3.551533 | 8.630156 | 284.43 | verified |
| gin--race-cover-client | 32 | orchestrion | 19.099456 | 18.994280-19.126530 | 48.849503 | 468.72 | failed |
| gin--race-cover-client | 32 | mini | 4.116798 | 3.955164-4.235261 | 13.204732 | 374.66 | unverified |
| gin--race-cover-client | 32 | mini-deferred | 3.993936 | 3.952664-4.524472 | 12.789012 | 356.83 | unverified |
| chi--race | 4 | native | 27.217195 | 27.215597-27.219476 | 44.749685 | 2988.66 | verified |
| chi--race | 4 | orchestrion | 27.316479 | 27.304612-27.327353 | 45.070215 | 2950.28 | verified |
| chi--race | 4 | mini | 27.267557 | 27.264476-27.274008 | 45.384980 | 2996.73 | verified |
| chi--race | 4 | mini-deferred | 27.268468 | 27.264027-27.276159 | 45.463072 | 2992.97 | verified |
| chi--race | 32 | native | 27.218182 | 27.218158-27.220933 | 223.726344 | 3053.02 | verified |
| chi--race | 32 | orchestrion | 27.323183 | 27.322220-27.327727 | 145.388392 | 3089.02 | verified |
| chi--race | 32 | mini | 27.280449 | 27.279224-27.282378 | 227.943095 | 3078.61 | verified |
| chi--race | 32 | mini-deferred | 27.279931 | 27.275972-27.283057 | 228.317158 | 3067.76 | verified |
| chi--cover | 4 | native | 26.136404 | 26.135014-26.141560 | 2.979522 | 45.26 | verified |
| chi--cover | 4 | orchestrion | 26.235312 | 26.234903-26.239111 | 2.993786 | 75.24 | verified |
| chi--cover | 4 | mini | 26.156020 | 26.153116-26.158971 | 3.319741 | 55.37 | verified |
| chi--cover | 4 | mini-deferred | 26.153002 | 26.152242-26.157341 | 3.320799 | 47.64 | verified |
| chi--cover | 32 | native | 26.140345 | 26.139914-26.140802 | 8.752921 | 87.98 | verified |
| chi--cover | 32 | orchestrion | 26.234413 | 26.232821-26.237548 | 9.009237 | 140.05 | verified |
| chi--cover | 32 | mini | 26.162430 | 26.161832-26.164740 | 10.204857 | 99.02 | verified |
| chi--cover | 32 | mini-deferred | 26.162607 | 26.160084-26.163442 | 10.039064 | 111.61 | verified |
| chi--cover-client | 4 | native | 26.142036 | 26.137633-26.142358 | 3.349110 | 30.76 | verified |
| chi--cover-client | 4 | orchestrion | 26.814884 | 26.814486-26.815973 | 4.347233 | 65.17 | verified |
| chi--cover-client | 4 | mini | 26.239221 | 26.234292-26.241692 | 3.941200 | 42.19 | verified |
| chi--cover-client | 4 | mini-deferred | 26.242925 | 26.240353-26.243919 | 3.916927 | 41.89 | verified |
| chi--cover-client | 32 | native | 26.151264 | 26.145279-26.158023 | 12.297564 | 49.06 | verified |
| chi--cover-client | 32 | orchestrion | 26.868956 | 26.847254-26.882888 | 13.574969 | 107.81 | verified |
| chi--cover-client | 32 | mini | 26.237436 | 26.235921-26.243734 | 13.050775 | 61.26 | verified |
| chi--cover-client | 32 | mini-deferred | 26.235773 | 26.230264-26.240856 | 12.798046 | 67.32 | verified |
| chi--race-cover-client | 4 | native | 27.234881 | 27.231695-27.244361 | 54.987296 | 2921.71 | verified |
| chi--race-cover-client | 4 | orchestrion | 28.909904 | 28.905723-28.948150 | 58.906081 | 3060.79 | verified |
| chi--race-cover-client | 4 | mini | 27.402483 | 27.391270-27.408050 | 55.311593 | 2967.34 | verified |
| chi--race-cover-client | 4 | mini-deferred | 27.403504 | 27.398890-27.446460 | 55.919796 | 2925.80 | verified |
| chi--race-cover-client | 32 | native | 27.238645 | 27.234214-27.239165 | 242.207079 | 2914.63 | verified |
| chi--race-cover-client | 32 | orchestrion | 28.957277 | 28.944102-28.969507 | 188.968250 | 3023.20 | verified |
| chi--race-cover-client | 32 | mini | 27.398835 | 27.396808-27.405031 | 246.903712 | 2952.54 | verified |
| chi--race-cover-client | 32 | mini-deferred | 27.408764 | 27.399445-27.416776 | 245.440199 | 2956.56 | verified |
| testify-direct--normal | 4 | native | 0.002854 | 0.002831-0.003029 | 0.003627 | 11.10 | verified |
| testify-direct--normal | 4 | orchestrion | 0.014925 | 0.014772-0.015095 | 0.018991 | 17.45 | verified |
| testify-direct--normal | 4 | mini | 0.010428 | 0.010371-0.010750 | 0.011913 | 13.72 | verified |
| testify-direct--normal | 4 | mini-deferred | 0.010546 | 0.010275-0.010610 | 0.011658 | 13.16 | verified |
| testify-direct--normal | 32 | native | 0.003185 | 0.003155-0.003206 | 0.004668 | 12.01 | verified |
| testify-direct--normal | 32 | orchestrion | 0.016874 | 0.016251-0.017277 | 0.023640 | 20.63 | verified |
| testify-direct--normal | 32 | mini | 0.011855 | 0.011357-0.011857 | 0.014216 | 14.27 | verified |
| testify-direct--normal | 32 | mini-deferred | 0.011521 | 0.011475-0.012138 | 0.013556 | 14.55 | verified |
| testify-direct--race | 4 | native | 1.012491 | 1.012288-1.012711 | 0.012817 | 25.34 | verified |
| testify-direct--race | 4 | orchestrion | 1.047072 | 1.047057-1.047691 | 0.055828 | 52.18 | verified |
| testify-direct--race | 4 | mini | 1.025652 | 1.025385-1.026233 | 0.027939 | 32.66 | verified |
| testify-direct--race | 4 | mini-deferred | 1.026143 | 1.025548-1.026579 | 0.027321 | 31.65 | verified |
| testify-direct--race | 32 | native | 1.013260 | 1.013038-1.013279 | 0.014454 | 26.74 | verified |
| testify-direct--race | 32 | orchestrion | 1.051256 | 1.050609-1.051876 | 0.063180 | 56.70 | verified |
| testify-direct--race | 32 | mini | 1.028847 | 1.028781-1.029507 | 0.031506 | 35.01 | verified |
| testify-direct--race | 32 | mini-deferred | 1.027797 | 1.027637-1.028371 | 0.029793 | 34.59 | verified |
| testify-direct--cover | 4 | native | 0.003313 | 0.003188-0.003532 | 0.004420 | 11.08 | verified |
| testify-direct--cover | 4 | orchestrion | 0.026283 | 0.026104-0.026594 | 0.030690 | 19.50 | verified |
| testify-direct--cover | 4 | mini | 0.010900 | 0.010802-0.010942 | 0.012390 | 13.29 | verified |
| testify-direct--cover | 4 | mini-deferred | 0.011177 | 0.010999-0.011351 | 0.012651 | 13.04 | verified |
| testify-direct--cover | 32 | native | 0.003558 | 0.003526-0.003735 | 0.004963 | 11.75 | verified |
| testify-direct--cover | 32 | orchestrion | 0.028184 | 0.028025-0.030011 | 0.037968 | 23.22 | verified |
| testify-direct--cover | 32 | mini | 0.011860 | 0.011796-0.013054 | 0.014135 | 14.66 | verified |
| testify-direct--cover | 32 | mini-deferred | 0.012453 | 0.012296-0.012618 | 0.014395 | 15.36 | verified |
| testify-direct--cover-client | 4 | native | 0.003425 | 0.003185-0.003487 | 0.004496 | 11.10 | verified |
| testify-direct--cover-client | 4 | orchestrion | 0.025674 | 0.025619-0.025900 | 0.030685 | 19.67 | verified |
| testify-direct--cover-client | 4 | mini | 0.021146 | 0.020682-0.021436 | 0.022517 | 15.91 | verified |
| testify-direct--cover-client | 4 | mini-deferred | 0.020891 | 0.020108-0.021282 | 0.022391 | 15.54 | verified |
| testify-direct--cover-client | 32 | native | 0.003649 | 0.003613-0.003895 | 0.004585 | 12.48 | verified |
| testify-direct--cover-client | 32 | orchestrion | 0.028813 | 0.027633-0.028906 | 0.036371 | 22.71 | verified |
| testify-direct--cover-client | 32 | mini | 0.023148 | 0.023096-0.023409 | 0.025865 | 18.25 | verified |
| testify-direct--cover-client | 32 | mini-deferred | 0.022654 | 0.022288-0.022962 | 0.024912 | 17.73 | verified |
| testify-direct--race-cover-client | 4 | native | 1.013338 | 1.012990-1.013689 | 0.014184 | 25.76 | verified |
| testify-direct--race-cover-client | 4 | orchestrion | 1.058891 | 1.058788-1.059712 | 0.068567 | 53.08 | verified |
| testify-direct--race-cover-client | 4 | mini | 1.037102 | 1.036271-1.037167 | 0.039212 | 34.12 | verified |
| testify-direct--race-cover-client | 4 | mini-deferred | 1.036885 | 1.036709-1.037697 | 0.038574 | 33.29 | verified |
| testify-direct--race-cover-client | 32 | native | 1.013939 | 1.013653-1.014462 | 0.014995 | 27.54 | verified |
| testify-direct--race-cover-client | 32 | orchestrion | 1.064704 | 1.064422-1.064908 | 0.078692 | 57.99 | verified |
| testify-direct--race-cover-client | 32 | mini | 1.041784 | 1.041479-1.042364 | 0.047378 | 40.12 | verified |
| testify-direct--race-cover-client | 32 | mini-deferred | 1.041358 | 1.040768-1.043054 | 0.046628 | 39.06 | verified |
| testify-direct--cover-testing | 4 | native | 0.004080 | 0.003949-0.004319 | 0.004946 | 11.35 | verified |
| testify-direct--cover-testing | 4 | orchestrion | 0.041748 | 0.041155-0.042455 | 0.049179 | 20.06 | verified |
| testify-direct--cover-testing | 4 | mini | 0.037021 | 0.036572-0.037580 | 0.040182 | 16.26 | verified |
| testify-direct--cover-testing | 4 | mini-deferred | 0.037930 | 0.037603-0.038168 | 0.039954 | 15.83 | verified |
| testify-direct--cover-testing | 32 | native | 0.004501 | 0.004342-0.004679 | 0.005876 | 12.25 | verified |
| testify-direct--cover-testing | 32 | orchestrion | 0.045384 | 0.045312-0.045990 | 0.056458 | 23.94 | verified |
| testify-direct--cover-testing | 32 | mini | 0.039348 | 0.038926-0.039425 | 0.044912 | 18.35 | verified |
| testify-direct--cover-testing | 32 | mini-deferred | 0.041142 | 0.039565-0.041817 | 0.045633 | 19.19 | verified |
| testify-direct--race-cover-testing | 4 | native | 1.015787 | 1.015496-1.015854 | 0.016781 | 27.08 | verified |
| testify-direct--race-cover-testing | 4 | orchestrion | 1.098133 | 1.096598-1.099353 | 0.120124 | 62.39 | verified |
| testify-direct--race-cover-testing | 4 | mini | 1.076746 | 1.076534-1.078218 | 0.083155 | 39.04 | verified |
| testify-direct--race-cover-testing | 4 | mini-deferred | 1.078681 | 1.077937-1.079550 | 0.081585 | 37.60 | verified |
| testify-direct--race-cover-testing | 32 | native | 1.016848 | 1.016490-1.016934 | 0.018336 | 28.80 | verified |
| testify-direct--race-cover-testing | 32 | orchestrion | 1.104561 | 1.104465-1.105476 | 0.129940 | 67.11 | verified |
| testify-direct--race-cover-testing | 32 | mini | 1.084061 | 1.082878-1.084403 | 0.093767 | 44.86 | verified |
| testify-direct--race-cover-testing | 32 | mini-deferred | 1.085597 | 1.084647-1.086657 | 0.092076 | 43.41 | verified |
| testify-external--normal | 4 | native | 0.002879 | 0.002858-0.002891 | 0.003871 | 11.04 | verified |
| testify-external--normal | 4 | orchestrion | 0.014796 | 0.014608-0.014821 | 0.018728 | 17.47 | verified |
| testify-external--normal | 4 | mini | 0.010448 | 0.010402-0.010637 | 0.012325 | 13.31 | verified |
| testify-external--normal | 4 | mini-deferred | 0.010295 | 0.010043-0.010946 | 0.011609 | 13.34 | verified |
| testify-external--normal | 32 | native | 0.003148 | 0.003138-0.003264 | 0.004546 | 11.54 | verified |
| testify-external--normal | 32 | orchestrion | 0.016865 | 0.016659-0.017531 | 0.024274 | 20.31 | verified |
| testify-external--normal | 32 | mini | 0.011802 | 0.011267-0.012132 | 0.013482 | 15.30 | verified |
| testify-external--normal | 32 | mini-deferred | 0.011919 | 0.011133-0.012405 | 0.013218 | 14.62 | verified |
| testify-external--race | 4 | native | 1.012443 | 1.012083-1.012910 | 0.013470 | 25.51 | verified |
| testify-external--race | 4 | orchestrion | 1.047059 | 1.046926-1.047949 | 0.056183 | 51.45 | verified |
| testify-external--race | 4 | mini | 1.026178 | 1.025959-1.026462 | 0.027793 | 32.40 | verified |
| testify-external--race | 4 | mini-deferred | 1.026276 | 1.026067-1.026722 | 0.028021 | 31.66 | verified |
| testify-external--race | 32 | native | 1.013060 | 1.012893-1.013475 | 0.013908 | 26.75 | verified |
| testify-external--race | 32 | orchestrion | 1.051367 | 1.051247-1.051810 | 0.063862 | 56.18 | verified |
| testify-external--race | 32 | mini | 1.028427 | 1.027875-1.030758 | 0.031498 | 35.85 | verified |
| testify-external--race | 32 | mini-deferred | 1.028101 | 1.027141-1.028502 | 0.030239 | 34.30 | verified |
| testify-external--cover | 4 | native | 0.003098 | 0.003046-0.003107 | 0.004160 | 11.04 | verified |
| testify-external--cover | 4 | orchestrion | 0.024109 | 0.023821-0.024758 | 0.028684 | 19.23 | verified |
| testify-external--cover | 4 | mini | 0.010629 | 0.010559-0.010644 | 0.011920 | 13.19 | verified |
| testify-external--cover | 4 | mini-deferred | 0.010589 | 0.010443-0.010798 | 0.012463 | 13.41 | verified |
| testify-external--cover | 32 | native | 0.003424 | 0.003410-0.003470 | 0.004354 | 11.50 | verified |
| testify-external--cover | 32 | orchestrion | 0.027477 | 0.027332-0.027565 | 0.036073 | 22.65 | verified |
| testify-external--cover | 32 | mini | 0.012355 | 0.011606-0.012488 | 0.013885 | 14.96 | verified |
| testify-external--cover | 32 | mini-deferred | 0.013088 | 0.012347-0.013099 | 0.015055 | 15.34 | verified |
| testify-external--cover-client | 4 | native | 0.003290 | 0.003163-0.003399 | 0.004150 | 11.05 | verified |
| testify-external--cover-client | 4 | orchestrion | 0.026501 | 0.026437-0.027066 | 0.031089 | 18.97 | verified |
| testify-external--cover-client | 4 | mini | 0.021840 | 0.020261-0.021880 | 0.023370 | 16.01 | verified |
| testify-external--cover-client | 4 | mini-deferred | 0.021144 | 0.020733-0.021954 | 0.022534 | 15.56 | verified |
| testify-external--cover-client | 32 | native | 0.003489 | 0.003446-0.003566 | 0.004542 | 11.80 | verified |
| testify-external--cover-client | 32 | orchestrion | 0.028923 | 0.028106-0.029227 | 0.036793 | 22.53 | verified |
| testify-external--cover-client | 32 | mini | 0.023363 | 0.022227-0.023564 | 0.028288 | 17.88 | verified |
| testify-external--cover-client | 32 | mini-deferred | 0.023171 | 0.022743-0.023383 | 0.025499 | 18.35 | verified |
| testify-external--race-cover-client | 4 | native | 1.013005 | 1.012972-1.013063 | 0.014433 | 25.80 | verified |
| testify-external--race-cover-client | 4 | orchestrion | 1.058745 | 1.057665-1.058941 | 0.067603 | 54.01 | verified |
| testify-external--race-cover-client | 4 | mini | 1.036825 | 1.036644-1.037191 | 0.038685 | 34.20 | verified |
| testify-external--race-cover-client | 4 | mini-deferred | 1.038533 | 1.036892-1.038759 | 0.040082 | 33.04 | verified |
| testify-external--race-cover-client | 32 | native | 1.013985 | 1.013775-1.014149 | 0.014965 | 27.80 | verified |
| testify-external--race-cover-client | 32 | orchestrion | 1.064254 | 1.062938-1.064363 | 0.077305 | 58.64 | verified |
| testify-external--race-cover-client | 32 | mini | 1.043195 | 1.042611-1.043480 | 0.048220 | 39.50 | verified |
| testify-external--race-cover-client | 32 | mini-deferred | 1.041607 | 1.040857-1.042790 | 0.046578 | 38.21 | verified |
| testify-external--cover-testing | 4 | native | 0.004115 | 0.004071-0.004717 | 0.005142 | 11.83 | verified |
| testify-external--cover-testing | 4 | orchestrion | 0.041661 | 0.041147-0.042259 | 0.049324 | 20.15 | verified |
| testify-external--cover-testing | 4 | mini | 0.037569 | 0.037402-0.038191 | 0.040500 | 16.23 | verified |
| testify-external--cover-testing | 4 | mini-deferred | 0.038238 | 0.037293-0.038326 | 0.040064 | 16.20 | verified |
| testify-external--cover-testing | 32 | native | 0.004350 | 0.004259-0.004429 | 0.005664 | 12.50 | verified |
| testify-external--cover-testing | 32 | orchestrion | 0.046354 | 0.045626-0.046914 | 0.058242 | 24.00 | verified |
| testify-external--cover-testing | 32 | mini | 0.040807 | 0.039802-0.040971 | 0.045757 | 18.84 | verified |
| testify-external--cover-testing | 32 | mini-deferred | 0.040771 | 0.039619-0.041038 | 0.044692 | 19.31 | verified |
| testify-external--race-cover-testing | 4 | native | 1.016169 | 1.015583-1.016725 | 0.017181 | 27.57 | verified |
| testify-external--race-cover-testing | 4 | orchestrion | 1.097673 | 1.097521-1.097910 | 0.118468 | 62.36 | verified |
| testify-external--race-cover-testing | 4 | mini | 1.077372 | 1.076876-1.077963 | 0.082947 | 38.61 | verified |
| testify-external--race-cover-testing | 4 | mini-deferred | 1.078587 | 1.077667-1.079249 | 0.081733 | 37.68 | verified |
| testify-external--race-cover-testing | 32 | native | 1.016639 | 1.016554-1.018008 | 0.017906 | 28.87 | verified |
| testify-external--race-cover-testing | 32 | orchestrion | 1.104211 | 1.103596-1.105226 | 0.132445 | 67.27 | verified |
| testify-external--race-cover-testing | 32 | mini | 1.083696 | 1.083664-1.083942 | 0.094013 | 44.80 | verified |
| testify-external--race-cover-testing | 32 | mini-deferred | 1.084777 | 1.082697-1.085519 | 0.091847 | 42.86 | verified |

## Validation gaps

- gin--race/4/orchestrion: test process failed, raced, timed out or did not drain
- gin--race/32/orchestrion: test process failed, raced, timed out or did not drain
- gin--race-cover-client/4/orchestrion: historical SDK reference unavailable for this combination; test process failed, raced, timed out or did not drain
- gin--race-cover-client/4/mini: historical SDK reference unavailable for this combination
- gin--race-cover-client/4/mini-deferred: historical SDK reference unavailable for this combination
- gin--race-cover-client/32/orchestrion: historical SDK reference unavailable for this combination; test process failed, raced, timed out or did not drain
- gin--race-cover-client/32/mini: historical SDK reference unavailable for this combination
- gin--race-cover-client/32/mini-deferred: historical SDK reference unavailable for this combination

The observed-time table keeps successful and failed durations. Failed or unverified
groups have no comparative median in the main table. Individual CPU and memory values
remain in observations.csv. No slow observation is removed.

## Collections

Two collections from 2026-10-28 make up these results, both from ddtest `282b22a15776c3dfb1073d2e0197c78445c00d6f`. The first stopped at chi--cover-client with 32 CPUs: validating that group, `go tool objdump` stalled on a stat in the Go build cache. The second measured chi--cover-client again and the cases after it: chi--cover-client, chi--race-cover-client, testify-direct--cover, testify-direct--cover-client, testify-direct--cover-testing, testify-direct--normal, testify-direct--race, testify-direct--race-cover-client, testify-direct--race-cover-testing, testify-external--cover, testify-external--cover-client, testify-external--cover-testing, testify-external--normal, testify-external--race, testify-external--race-cover-client, testify-external--race-cover-testing. Each case comes from one collection; `runtime/collection-1` and `runtime/collection-2` in [raw.zip](../raw.zip) keep both, including the first's chi--cover-client groups, which are not used.
