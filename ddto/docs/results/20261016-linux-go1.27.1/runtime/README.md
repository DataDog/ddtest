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
| Gin | `none` | 4 | 0.183472 s | 0.227665 s (+24.1%) | 0.221166 s (-2.9%; +20.5%) | 0.227677 s (+0.0%; +24.1%) |
| Gin | `none` | 32 | 0.192854 s | 0.235763 s (+22.2%) | 0.214014 s (-9.2%; +11.0%) | 0.223292 s (-5.3%; +15.8%) |
| Chi | `none` | 4 | 26.136864 s | 26.174048 s (+0.1%) | 26.160106 s (-0.1%; +0.1%) | 26.157522 s (-0.1%; +0.1%) |
| Chi | `none` | 32 | 26.141335 s | 26.179190 s (+0.1%) | 26.171993 s (-0.0%; +0.1%) | 26.166768 s (-0.0%; +0.1%) |
| Gin | `-race` | 4 | 2.040156 s | FAIL 5/6 | 2.541368 s (vs Orchestrion unavailable; +24.6% vs Native) | 2.547998 s (vs Orchestrion unavailable; +24.9% vs Native) |
| Gin | `-race` | 32 | 1.415086 s | FAIL 6/6 | 1.548575 s (vs Orchestrion unavailable; +9.4% vs Native) | 1.551032 s (vs Orchestrion unavailable; +9.6% vs Native) |
| Gin | `-cover` | 4 | 0.188422 s | 0.484403 s (+157.1%) | 0.231282 s (-52.3%; +22.7%) | 0.233100 s (-51.9%; +23.7%) |
| Gin | `-cover` | 32 | 0.198064 s | 0.410552 s (+107.3%) | 0.227094 s (-44.7%; +14.7%) | 0.231899 s (-43.5%; +17.1%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 4 | 0.253092 s | 6.616408 s (+2514.2%) | 0.560605 s (-91.5%; +121.5%) | 0.563781 s (-91.5%; +122.8%) |
| Gin | `-coverpkg=./... -covermode=atomic` | 32 | 0.253249 s | 6.541288 s (+2482.9%) | 0.509960 s (-92.2%; +101.4%) | 0.505929 s (-92.3%; +99.8%) |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 4 | 3.415680 s | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Gin | `-race -coverpkg=./... -covermode=atomic` | 32 | 3.446807 s | FAIL 5/6 | UNVERIFIED | UNVERIFIED |
| Chi | `-race` | 4 | 27.214190 s | 27.322207 s (+0.4%) | 27.276766 s (-0.2%; +0.2%) | 27.284308 s (-0.1%; +0.3%) |
| Chi | `-race` | 32 | 27.218039 s | 27.324007 s (+0.4%) | 27.286288 s (-0.1%; +0.3%) | 27.285077 s (-0.1%; +0.2%) |
| Chi | `-cover` | 4 | 26.136957 s | 26.229098 s (+0.4%) | 26.168143 s (-0.2%; +0.1%) | 26.161219 s (-0.3%; +0.1%) |
| Chi | `-cover` | 32 | 26.140129 s | 26.233960 s (+0.4%) | 26.170426 s (-0.2%; +0.1%) | 26.170937 s (-0.2%; +0.1%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 4 | 26.139077 s | 26.788571 s (+2.5%) | 26.248139 s (-2.0%; +0.4%) | 26.246414 s (-2.0%; +0.4%) |
| Chi | `-coverpkg=./... -covermode=atomic` | 32 | 26.143078 s | 26.843831 s (+2.7%) | 26.239460 s (-2.3%; +0.4%) | 26.241645 s (-2.2%; +0.4%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 4 | 27.227274 s | 28.864119 s (+6.0%) | 27.395419 s (-5.1%; +0.6%) | 27.397021 s (-5.1%; +0.6%) |
| Chi | `-race -coverpkg=./... -covermode=atomic` | 32 | 27.233273 s | 28.933591 s (+6.2%) | 27.405601 s (-5.3%; +0.6%) | 27.406239 s (-5.3%; +0.6%) |
| Testify Direct | `none` | 4 | 0.002768 s | 0.014293 s (+416.4%) | 0.010578 s (-26.0%; +282.2%) | 0.010303 s (-27.9%; +272.3%) |
| Testify Direct | `none` | 32 | 0.003034 s | 0.016014 s (+427.8%) | 0.011907 s (-25.6%; +292.4%) | 0.011984 s (-25.2%; +295.0%) |
| Testify Direct | `-race` | 4 | 1.008293 s | 1.041778 s (+3.3%) | 1.025876 s (-1.5%; +1.7%) | 1.025270 s (-1.6%; +1.7%) |
| Testify Direct | `-race` | 32 | 1.008927 s | 1.045827 s (+3.7%) | 1.028390 s (-1.7%; +1.9%) | 1.027895 s (-1.7%; +1.9%) |
| Testify Direct | `-cover` | 4 | 0.003040 s | 0.023040 s (+658.0%) | 0.011351 s (-50.7%; +273.4%) | 0.011195 s (-51.4%; +268.3%) |
| Testify Direct | `-cover` | 32 | 0.003225 s | 0.025315 s (+685.0%) | 0.012596 s (-50.2%; +290.6%) | 0.013626 s (-46.2%; +322.5%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 4 | 0.003020 s | 0.024021 s (+695.3%) | 0.021358 s (-11.1%; +607.1%) | 0.020860 s (-13.2%; +590.6%) |
| Testify Direct | `-coverpkg=./... -covermode=atomic` | 32 | 0.003157 s | 0.026260 s (+731.7%) | 0.023602 s (-10.1%; +647.5%) | 0.023122 s (-12.0%; +632.3%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009339 s | 1.052694 s (+4.3%) | 1.037941 s (-1.4%; +2.8%) | 1.037264 s (-1.5%; +2.8%) |
| Testify Direct | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009875 s | 1.057152 s (+4.7%) | 1.042663 s (-1.4%; +3.2%) | 1.042000 s (-1.4%; +3.2%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003584 s | 0.038847 s (+984.0%) | 0.037733 s (-2.9%; +953.0%) | 0.038349 s (-1.3%; +970.1%) |
| Testify Direct | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.003901 s | 0.041828 s (+972.2%) | 0.042160 s (+0.8%; +980.7%) | 0.041862 s (+0.1%; +973.0%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.011706 s | 1.089557 s (+7.7%) | 1.080158 s (-0.9%; +6.8%) | 1.081994 s (-0.7%; +6.9%) |
| Testify Direct | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012462 s | 1.097505 s (+8.4%) | 1.086677 s (-1.0%; +7.3%) | 1.088049 s (-0.9%; +7.5%) |
| Testify External | `none` | 4 | 0.002716 s | 0.014248 s (+424.6%) | 0.010423 s (-26.8%; +283.8%) | 0.010840 s (-23.9%; +299.1%) |
| Testify External | `none` | 32 | 0.002940 s | 0.016444 s (+459.4%) | 0.011769 s (-28.4%; +300.3%) | 0.012057 s (-26.7%; +310.1%) |
| Testify External | `-race` | 4 | 1.008352 s | 1.041991 s (+3.3%) | 1.025686 s (-1.6%; +1.7%) | 1.025604 s (-1.6%; +1.7%) |
| Testify External | `-race` | 32 | 1.009078 s | 1.046242 s (+3.7%) | 1.027621 s (-1.8%; +1.8%) | 1.027284 s (-1.8%; +1.8%) |
| Testify External | `-cover` | 4 | 0.002880 s | 0.023010 s (+699.1%) | 0.011240 s (-51.2%; +290.3%) | 0.011280 s (-51.0%; +291.7%) |
| Testify External | `-cover` | 32 | 0.003031 s | 0.025533 s (+742.3%) | 0.012375 s (-51.5%; +308.2%) | 0.012446 s (-51.3%; +310.6%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 4 | 0.003078 s | 0.023386 s (+659.8%) | 0.021211 s (-9.3%; +589.1%) | 0.021293 s (-9.0%; +591.8%) |
| Testify External | `-coverpkg=./... -covermode=atomic` | 32 | 0.003307 s | 0.025663 s (+676.0%) | 0.024039 s (-6.3%; +626.9%) | 0.023428 s (-8.7%; +608.4%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 4 | 1.009322 s | 1.053227 s (+4.4%) | 1.037830 s (-1.5%; +2.8%) | 1.037691 s (-1.5%; +2.8%) |
| Testify External | `-race -coverpkg=./... -covermode=atomic` | 32 | 1.009809 s | 1.058201 s (+4.8%) | 1.042533 s (-1.5%; +3.2%) | 1.042598 s (-1.5%; +3.2%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 0.003702 s | 0.039288 s (+961.3%) | 0.038265 s (-2.6%; +933.7%) | 0.038890 s (-1.0%; +950.6%) |
| Testify External | `-coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 0.004002 s | 0.042201 s (+954.5%) | 0.039964 s (-5.3%; +898.6%) | 0.041221 s (-2.3%; +930.0%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 4 | 1.012068 s | 1.090319 s (+7.7%) | 1.080123 s (-0.9%; +6.7%) | 1.082105 s (-0.8%; +6.9%) |
| Testify External | `-race -coverpkg=testing,github.com/stretchr/testify/suite -covermode=atomic` | 32 | 1.012989 s | 1.098761 s (+8.5%) | 1.086099 s (-1.2%; +7.2%) | 1.088775 s (-0.9%; +7.5%) |

## CPU, memory and dispersion

| Case | CPUs | Variant | Median (s) | Min-max (s) | CPU median (CPU-s) | Memory median (MiB) | Validation |
| --- | ---: | --- | ---: | --- | ---: | ---: | --- |
| gin | 4 | native | 0.183472 | 0.178205-0.194327 | 0.170003 | 36.64 | verified |
| gin | 4 | orchestrion | 0.227665 | 0.219127-0.241448 | 0.348156 | 60.29 | verified |
| gin | 4 | mini | 0.221166 | 0.215479-0.227618 | 0.268603 | 38.94 | verified |
| gin | 4 | mini-deferred | 0.227677 | 0.219489-0.229827 | 0.270444 | 38.57 | verified |
| gin | 32 | native | 0.192854 | 0.190069-0.194346 | 0.217764 | 42.18 | verified |
| gin | 32 | orchestrion | 0.235763 | 0.230035-0.256558 | 0.439478 | 70.67 | verified |
| gin | 32 | mini | 0.214014 | 0.213298-0.221803 | 0.340767 | 53.06 | verified |
| gin | 32 | mini-deferred | 0.223292 | 0.216560-0.230714 | 0.343338 | 51.11 | verified |
| chi | 4 | native | 26.136864 | 26.135458-26.150430 | 2.901706 | 45.64 | verified |
| chi | 4 | orchestrion | 26.174048 | 26.152724-26.184653 | 2.682397 | 79.65 | verified |
| chi | 4 | mini | 26.160106 | 26.155936-26.162124 | 3.343646 | 32.11 | verified |
| chi | 4 | mini-deferred | 26.157522 | 26.151713-26.160512 | 3.345258 | 31.50 | verified |
| chi | 32 | native | 26.141335 | 26.139699-26.141874 | 7.661383 | 94.73 | verified |
| chi | 32 | orchestrion | 26.179190 | 26.160035-26.183551 | 7.197552 | 128.05 | verified |
| chi | 32 | mini | 26.171993 | 26.161075-26.175786 | 9.774344 | 57.54 | verified |
| chi | 32 | mini-deferred | 26.166768 | 26.159284-26.170947 | 9.386073 | 67.84 | verified |
| gin--race | 4 | native | 2.040156 | 2.034008-2.041493 | 2.627885 | 199.33 | verified |
| gin--race | 4 | orchestrion | 2.139835 | 2.131945-2.151618 | 3.691318 | 332.23 | failed |
| gin--race | 4 | mini | 2.541368 | 2.540242-2.546020 | 3.016259 | 187.30 | verified |
| gin--race | 4 | mini-deferred | 2.547998 | 2.544824-2.557280 | 3.016887 | 187.95 | verified |
| gin--race | 32 | native | 1.415086 | 1.407846-1.426243 | 2.725913 | 262.89 | verified |
| gin--race | 32 | orchestrion | 1.731117 | 1.724280-1.740767 | 3.893175 | 418.80 | failed |
| gin--race | 32 | mini | 1.548575 | 1.543527-1.552248 | 3.150642 | 324.38 | verified |
| gin--race | 32 | mini-deferred | 1.551032 | 1.545629-1.563439 | 3.152718 | 323.59 | verified |
| gin--cover | 4 | native | 0.188422 | 0.186399-0.190422 | 0.192585 | 35.87 | verified |
| gin--cover | 4 | orchestrion | 0.484403 | 0.471239-0.497941 | 1.289090 | 92.60 | verified |
| gin--cover | 4 | mini | 0.231282 | 0.228178-0.235987 | 0.306593 | 40.68 | verified |
| gin--cover | 4 | mini-deferred | 0.233100 | 0.228613-0.254800 | 0.311846 | 40.41 | verified |
| gin--cover | 32 | native | 0.198064 | 0.194113-0.219861 | 0.238151 | 42.11 | verified |
| gin--cover | 32 | orchestrion | 0.410552 | 0.404105-0.427463 | 3.361391 | 165.18 | verified |
| gin--cover | 32 | mini | 0.227094 | 0.219207-0.242688 | 0.383302 | 52.45 | verified |
| gin--cover | 32 | mini-deferred | 0.231899 | 0.224670-0.244031 | 0.376850 | 52.77 | verified |
| gin--cover-client | 4 | native | 0.253092 | 0.250583-0.263731 | 0.385578 | 35.73 | verified |
| gin--cover-client | 4 | orchestrion | 6.616408 | 6.582757-6.973707 | 14.125127 | 98.61 | verified |
| gin--cover-client | 4 | mini | 0.560605 | 0.547049-0.943424 | 1.459276 | 75.14 | verified |
| gin--cover-client | 4 | mini-deferred | 0.563781 | 0.548120-0.613295 | 1.436661 | 74.58 | verified |
| gin--cover-client | 32 | native | 0.253249 | 0.250254-0.262717 | 0.420659 | 52.08 | verified |
| gin--cover-client | 32 | orchestrion | 6.541288 | 6.516528-6.660464 | 16.820350 | 200.94 | verified |
| gin--cover-client | 32 | mini | 0.509960 | 0.488582-0.540983 | 3.617413 | 144.21 | verified |
| gin--cover-client | 32 | mini-deferred | 0.505929 | 0.493252-0.546977 | 3.673037 | 146.35 | verified |
| gin--race-cover-client | 4 | native | 3.415680 | 3.371987-3.544310 | 8.500800 | 243.92 | verified |
| gin--race-cover-client | 4 | orchestrion | 18.491987 | 18.428509-18.560299 | 43.477136 | 408.91 | failed |
| gin--race-cover-client | 4 | mini | 4.957409 | 4.905800-4.993903 | 10.257831 | 204.17 | unverified |
| gin--race-cover-client | 4 | mini-deferred | 4.987601 | 4.953057-5.007484 | 10.280607 | 203.30 | unverified |
| gin--race-cover-client | 32 | native | 3.446807 | 3.413063-3.583871 | 8.572765 | 283.77 | verified |
| gin--race-cover-client | 32 | orchestrion | 18.746791 | 18.707628-18.803462 | 48.207009 | 470.64 | failed |
| gin--race-cover-client | 32 | mini | 4.000094 | 3.967352-4.062077 | 12.865393 | 367.20 | unverified |
| gin--race-cover-client | 32 | mini-deferred | 4.064897 | 3.969853-4.411017 | 12.973836 | 357.88 | unverified |
| chi--race | 4 | native | 27.214190 | 27.209690-27.216559 | 44.707255 | 2958.75 | verified |
| chi--race | 4 | orchestrion | 27.322207 | 27.308320-27.325687 | 46.083466 | 2979.35 | verified |
| chi--race | 4 | mini | 27.276766 | 27.270591-27.282670 | 45.419520 | 3005.70 | verified |
| chi--race | 4 | mini-deferred | 27.284308 | 27.277527-27.305274 | 45.539197 | 3004.08 | verified |
| chi--race | 32 | native | 27.218039 | 27.214792-27.225713 | 225.164169 | 3068.80 | verified |
| chi--race | 32 | orchestrion | 27.324007 | 27.302995-27.329207 | 147.048678 | 3112.38 | verified |
| chi--race | 32 | mini | 27.286288 | 27.276613-27.307723 | 225.150914 | 3051.04 | verified |
| chi--race | 32 | mini-deferred | 27.285077 | 27.274404-27.288356 | 226.882232 | 3050.72 | verified |
| chi--cover | 4 | native | 26.136957 | 26.134604-26.140277 | 3.004046 | 43.00 | verified |
| chi--cover | 4 | orchestrion | 26.229098 | 26.224293-26.236508 | 3.008069 | 84.96 | verified |
| chi--cover | 4 | mini | 26.168143 | 26.158390-26.173946 | 3.368572 | 32.58 | verified |
| chi--cover | 4 | mini-deferred | 26.161219 | 26.157676-26.179650 | 3.385814 | 32.23 | verified |
| chi--cover | 32 | native | 26.140129 | 26.138273-26.144419 | 8.704006 | 88.64 | verified |
| chi--cover | 32 | orchestrion | 26.233960 | 26.228725-26.238064 | 8.804965 | 149.12 | verified |
| chi--cover | 32 | mini | 26.170426 | 26.166217-26.175024 | 10.259996 | 59.14 | verified |
| chi--cover | 32 | mini-deferred | 26.170937 | 26.164867-26.173984 | 10.059879 | 67.77 | verified |
| chi--cover-client | 4 | native | 26.139077 | 26.138678-26.143371 | 3.273207 | 45.12 | verified |
| chi--cover-client | 4 | orchestrion | 26.788571 | 26.783171-26.806840 | 4.292763 | 98.80 | verified |
| chi--cover-client | 4 | mini | 26.248139 | 26.247945-26.249719 | 3.934348 | 41.73 | verified |
| chi--cover-client | 4 | mini-deferred | 26.246414 | 26.241065-26.262794 | 3.920240 | 41.33 | verified |
| chi--cover-client | 32 | native | 26.143078 | 26.140160-26.143884 | 12.007906 | 84.12 | verified |
| chi--cover-client | 32 | orchestrion | 26.843831 | 26.825489-26.848656 | 12.536681 | 145.21 | verified |
| chi--cover-client | 32 | mini | 26.239460 | 26.238101-26.243169 | 13.064733 | 63.70 | verified |
| chi--cover-client | 32 | mini-deferred | 26.241645 | 26.234443-26.249900 | 13.018299 | 67.09 | verified |
| chi--race-cover-client | 4 | native | 27.227274 | 27.219629-27.232085 | 55.070241 | 2944.42 | verified |
| chi--race-cover-client | 4 | orchestrion | 28.864119 | 28.817819-28.912666 | 59.183012 | 3073.44 | verified |
| chi--race-cover-client | 4 | mini | 27.395419 | 27.391294-27.406065 | 56.277055 | 2964.26 | verified |
| chi--race-cover-client | 4 | mini-deferred | 27.397021 | 27.394261-27.406678 | 55.926466 | 2955.44 | verified |
| chi--race-cover-client | 32 | native | 27.233273 | 27.230656-27.235937 | 243.743850 | 2940.62 | verified |
| chi--race-cover-client | 32 | orchestrion | 28.933591 | 28.929454-28.939520 | 189.447900 | 3072.50 | verified |
| chi--race-cover-client | 32 | mini | 27.405601 | 27.404084-27.411396 | 248.436016 | 2953.30 | verified |
| chi--race-cover-client | 32 | mini-deferred | 27.406239 | 27.398156-27.415702 | 251.059180 | 2947.39 | verified |
| testify-direct--normal | 4 | native | 0.002768 | 0.002659-0.002871 | 0.003788 | 14.54 | verified |
| testify-direct--normal | 4 | orchestrion | 0.014293 | 0.013946-0.014714 | 0.018899 | 26.62 | verified |
| testify-direct--normal | 4 | mini | 0.010578 | 0.010220-0.010702 | 0.012066 | 13.41 | verified |
| testify-direct--normal | 4 | mini-deferred | 0.010303 | 0.010244-0.010767 | 0.011788 | 12.98 | verified |
| testify-direct--normal | 32 | native | 0.003034 | 0.002834-0.003637 | 0.004573 | 14.98 | verified |
| testify-direct--normal | 32 | orchestrion | 0.016014 | 0.015913-0.016351 | 0.023118 | 29.43 | verified |
| testify-direct--normal | 32 | mini | 0.011907 | 0.011249-0.012411 | 0.013814 | 15.45 | verified |
| testify-direct--normal | 32 | mini-deferred | 0.011984 | 0.011704-0.012326 | 0.013613 | 14.57 | verified |
| testify-direct--race | 4 | native | 1.008293 | 1.008201-1.008790 | 0.009414 | 34.08 | verified |
| testify-direct--race | 4 | orchestrion | 1.041778 | 1.040832-1.041896 | 0.051832 | 67.46 | verified |
| testify-direct--race | 4 | mini | 1.025876 | 1.025238-1.026258 | 0.027825 | 31.94 | verified |
| testify-direct--race | 4 | mini-deferred | 1.025270 | 1.024996-1.026049 | 0.026965 | 31.21 | verified |
| testify-direct--race | 32 | native | 1.008927 | 1.008471-1.009004 | 0.010023 | 37.21 | verified |
| testify-direct--race | 32 | orchestrion | 1.045827 | 1.045513-1.046870 | 0.063160 | 73.38 | verified |
| testify-direct--race | 32 | mini | 1.028390 | 1.027206-1.028530 | 0.030940 | 35.61 | verified |
| testify-direct--race | 32 | mini-deferred | 1.027895 | 1.027016-1.028236 | 0.029547 | 33.95 | verified |
| testify-direct--cover | 4 | native | 0.003040 | 0.002961-0.003208 | 0.003917 | 14.48 | verified |
| testify-direct--cover | 4 | orchestrion | 0.023040 | 0.022565-0.023689 | 0.026835 | 29.69 | verified |
| testify-direct--cover | 4 | mini | 0.011351 | 0.011126-0.011714 | 0.012566 | 13.22 | verified |
| testify-direct--cover | 4 | mini-deferred | 0.011195 | 0.010672-0.011670 | 0.012316 | 13.41 | verified |
| testify-direct--cover | 32 | native | 0.003225 | 0.003185-0.003667 | 0.004487 | 14.72 | verified |
| testify-direct--cover | 32 | orchestrion | 0.025315 | 0.024757-0.025785 | 0.034084 | 33.78 | verified |
| testify-direct--cover | 32 | mini | 0.012596 | 0.011915-0.012619 | 0.014358 | 14.79 | verified |
| testify-direct--cover | 32 | mini-deferred | 0.013626 | 0.011862-0.013886 | 0.017538 | 16.62 | verified |
| testify-direct--cover-client | 4 | native | 0.003020 | 0.002933-0.003363 | 0.003895 | 14.33 | verified |
| testify-direct--cover-client | 4 | orchestrion | 0.024021 | 0.023243-0.024309 | 0.028469 | 27.64 | verified |
| testify-direct--cover-client | 4 | mini | 0.021358 | 0.020803-0.021781 | 0.022858 | 15.64 | verified |
| testify-direct--cover-client | 4 | mini-deferred | 0.020860 | 0.020413-0.021923 | 0.022266 | 15.50 | verified |
| testify-direct--cover-client | 32 | native | 0.003157 | 0.003060-0.003179 | 0.004158 | 14.77 | verified |
| testify-direct--cover-client | 32 | orchestrion | 0.026260 | 0.025792-0.026927 | 0.034280 | 34.04 | verified |
| testify-direct--cover-client | 32 | mini | 0.023602 | 0.023157-0.024614 | 0.026373 | 18.61 | verified |
| testify-direct--cover-client | 32 | mini-deferred | 0.023122 | 0.022501-0.023857 | 0.024815 | 17.68 | verified |
| testify-direct--race-cover-client | 4 | native | 1.009339 | 1.008955-1.009570 | 0.010245 | 33.97 | verified |
| testify-direct--race-cover-client | 4 | orchestrion | 1.052694 | 1.051837-1.053167 | 0.061688 | 66.72 | verified |
| testify-direct--race-cover-client | 4 | mini | 1.037941 | 1.037064-1.039049 | 0.039900 | 34.15 | verified |
| testify-direct--race-cover-client | 4 | mini-deferred | 1.037264 | 1.036744-1.038362 | 0.039233 | 32.79 | verified |
| testify-direct--race-cover-client | 32 | native | 1.009875 | 1.009629-1.010124 | 0.011177 | 35.75 | verified |
| testify-direct--race-cover-client | 32 | orchestrion | 1.057152 | 1.055745-1.061098 | 0.072301 | 72.09 | verified |
| testify-direct--race-cover-client | 32 | mini | 1.042663 | 1.041491-1.043822 | 0.048285 | 39.39 | verified |
| testify-direct--race-cover-client | 32 | mini-deferred | 1.042000 | 1.040464-1.042380 | 0.046758 | 38.48 | verified |
| testify-direct--cover-testing | 4 | native | 0.003584 | 0.003514-0.003702 | 0.004669 | 14.81 | verified |
| testify-direct--cover-testing | 4 | orchestrion | 0.038847 | 0.038238-0.039334 | 0.045965 | 27.74 | verified |
| testify-direct--cover-testing | 4 | mini | 0.037733 | 0.037225-0.038738 | 0.040555 | 15.87 | verified |
| testify-direct--cover-testing | 4 | mini-deferred | 0.038349 | 0.038084-0.039598 | 0.039945 | 15.84 | verified |
| testify-direct--cover-testing | 32 | native | 0.003901 | 0.003769-0.004127 | 0.005086 | 14.96 | verified |
| testify-direct--cover-testing | 32 | orchestrion | 0.041828 | 0.039988-0.042617 | 0.053131 | 35.20 | verified |
| testify-direct--cover-testing | 32 | mini | 0.042160 | 0.041208-0.043015 | 0.048336 | 19.56 | verified |
| testify-direct--cover-testing | 32 | mini-deferred | 0.041862 | 0.040461-0.042467 | 0.045516 | 19.23 | verified |
| testify-direct--race-cover-testing | 4 | native | 1.011706 | 1.011577-1.011857 | 0.012413 | 35.34 | verified |
| testify-direct--race-cover-testing | 4 | orchestrion | 1.089557 | 1.088832-1.091163 | 0.109470 | 74.43 | verified |
| testify-direct--race-cover-testing | 4 | mini | 1.080158 | 1.078874-1.081119 | 0.086539 | 39.36 | verified |
| testify-direct--race-cover-testing | 4 | mini-deferred | 1.081994 | 1.081158-1.082810 | 0.084808 | 38.05 | verified |
| testify-direct--race-cover-testing | 32 | native | 1.012462 | 1.012319-1.012960 | 0.013447 | 36.73 | verified |
| testify-direct--race-cover-testing | 32 | orchestrion | 1.097505 | 1.096087-1.098500 | 0.126504 | 83.53 | verified |
| testify-direct--race-cover-testing | 32 | mini | 1.086677 | 1.085784-1.087249 | 0.096884 | 44.93 | verified |
| testify-direct--race-cover-testing | 32 | mini-deferred | 1.088049 | 1.086194-1.090203 | 0.093995 | 43.38 | verified |
| testify-external--normal | 4 | native | 0.002716 | 0.002639-0.002796 | 0.003568 | 14.34 | verified |
| testify-external--normal | 4 | orchestrion | 0.014248 | 0.013731-0.014540 | 0.019312 | 27.12 | verified |
| testify-external--normal | 4 | mini | 0.010423 | 0.010327-0.010759 | 0.011925 | 13.24 | verified |
| testify-external--normal | 4 | mini-deferred | 0.010840 | 0.010157-0.011598 | 0.012173 | 12.96 | verified |
| testify-external--normal | 32 | native | 0.002940 | 0.002921-0.003123 | 0.004025 | 14.48 | verified |
| testify-external--normal | 32 | orchestrion | 0.016444 | 0.016203-0.016616 | 0.025720 | 32.32 | verified |
| testify-external--normal | 32 | mini | 0.011769 | 0.011373-0.012320 | 0.013755 | 15.14 | verified |
| testify-external--normal | 32 | mini-deferred | 0.012057 | 0.011351-0.012444 | 0.013467 | 15.09 | verified |
| testify-external--race | 4 | native | 1.008352 | 1.008251-1.008555 | 0.009517 | 33.83 | verified |
| testify-external--race | 4 | orchestrion | 1.041991 | 1.041745-1.042499 | 0.055411 | 68.84 | verified |
| testify-external--race | 4 | mini | 1.025686 | 1.025412-1.025968 | 0.027369 | 32.46 | verified |
| testify-external--race | 4 | mini-deferred | 1.025604 | 1.025472-1.026645 | 0.027287 | 31.48 | verified |
| testify-external--race | 32 | native | 1.009078 | 1.008949-1.009489 | 0.010453 | 36.97 | verified |
| testify-external--race | 32 | orchestrion | 1.046242 | 1.045924-1.046899 | 0.064850 | 75.32 | verified |
| testify-external--race | 32 | mini | 1.027621 | 1.027294-1.027856 | 0.030284 | 35.22 | verified |
| testify-external--race | 32 | mini-deferred | 1.027284 | 1.027160-1.027692 | 0.029053 | 33.75 | verified |
| testify-external--cover | 4 | native | 0.002880 | 0.002857-0.003041 | 0.003944 | 14.23 | verified |
| testify-external--cover | 4 | orchestrion | 0.023010 | 0.022500-0.023789 | 0.027232 | 29.42 | verified |
| testify-external--cover | 4 | mini | 0.011240 | 0.010942-0.011318 | 0.012464 | 13.43 | verified |
| testify-external--cover | 4 | mini-deferred | 0.011280 | 0.010953-0.011377 | 0.012153 | 13.29 | verified |
| testify-external--cover | 32 | native | 0.003031 | 0.002962-0.003418 | 0.004082 | 15.02 | verified |
| testify-external--cover | 32 | orchestrion | 0.025533 | 0.025143-0.025954 | 0.032932 | 34.40 | verified |
| testify-external--cover | 32 | mini | 0.012375 | 0.011867-0.012853 | 0.014392 | 14.87 | verified |
| testify-external--cover | 32 | mini-deferred | 0.012446 | 0.011721-0.013453 | 0.013895 | 14.57 | verified |
| testify-external--cover-client | 4 | native | 0.003078 | 0.003060-0.003356 | 0.004318 | 14.33 | verified |
| testify-external--cover-client | 4 | orchestrion | 0.023386 | 0.022280-0.024013 | 0.027525 | 28.16 | verified |
| testify-external--cover-client | 4 | mini | 0.021211 | 0.020749-0.022081 | 0.023107 | 15.93 | verified |
| testify-external--cover-client | 4 | mini-deferred | 0.021293 | 0.020200-0.022173 | 0.022763 | 15.48 | verified |
| testify-external--cover-client | 32 | native | 0.003307 | 0.003124-0.003462 | 0.004624 | 15.01 | verified |
| testify-external--cover-client | 32 | orchestrion | 0.025663 | 0.025281-0.026668 | 0.033521 | 34.38 | verified |
| testify-external--cover-client | 32 | mini | 0.024039 | 0.022671-0.024934 | 0.026136 | 18.55 | verified |
| testify-external--cover-client | 32 | mini-deferred | 0.023428 | 0.022949-0.024261 | 0.025938 | 18.33 | verified |
| testify-external--race-cover-client | 4 | native | 1.009322 | 1.008989-1.009474 | 0.010397 | 32.80 | verified |
| testify-external--race-cover-client | 4 | orchestrion | 1.053227 | 1.052705-1.054280 | 0.061959 | 67.10 | verified |
| testify-external--race-cover-client | 4 | mini | 1.037830 | 1.036772-1.038502 | 0.039877 | 34.41 | verified |
| testify-external--race-cover-client | 4 | mini-deferred | 1.037691 | 1.037508-1.037796 | 0.039121 | 32.96 | verified |
| testify-external--race-cover-client | 32 | native | 1.009809 | 1.009439-1.010221 | 0.010980 | 35.51 | verified |
| testify-external--race-cover-client | 32 | orchestrion | 1.058201 | 1.056507-1.059143 | 0.072888 | 73.07 | verified |
| testify-external--race-cover-client | 32 | mini | 1.042533 | 1.041504-1.043475 | 0.048161 | 39.63 | verified |
| testify-external--race-cover-client | 32 | mini-deferred | 1.042598 | 1.040091-1.043764 | 0.046845 | 38.73 | verified |
| testify-external--cover-testing | 4 | native | 0.003702 | 0.003598-0.003749 | 0.004493 | 14.59 | verified |
| testify-external--cover-testing | 4 | orchestrion | 0.039288 | 0.037583-0.039342 | 0.045974 | 28.01 | verified |
| testify-external--cover-testing | 4 | mini | 0.038265 | 0.036869-0.039034 | 0.041073 | 15.99 | verified |
| testify-external--cover-testing | 4 | mini-deferred | 0.038890 | 0.037984-0.039513 | 0.040115 | 16.27 | verified |
| testify-external--cover-testing | 32 | native | 0.004002 | 0.003766-0.004048 | 0.005011 | 16.75 | verified |
| testify-external--cover-testing | 32 | orchestrion | 0.042201 | 0.041555-0.042875 | 0.054885 | 34.48 | verified |
| testify-external--cover-testing | 32 | mini | 0.039964 | 0.039892-0.040792 | 0.047231 | 19.04 | verified |
| testify-external--cover-testing | 32 | mini-deferred | 0.041221 | 0.040511-0.041906 | 0.046478 | 19.05 | verified |
| testify-external--race-cover-testing | 4 | native | 1.012068 | 1.011585-1.012211 | 0.012663 | 35.28 | verified |
| testify-external--race-cover-testing | 4 | orchestrion | 1.090319 | 1.088727-1.091354 | 0.110854 | 74.31 | verified |
| testify-external--race-cover-testing | 4 | mini | 1.080123 | 1.079581-1.080536 | 0.085772 | 39.53 | verified |
| testify-external--race-cover-testing | 4 | mini-deferred | 1.082105 | 1.080715-1.083293 | 0.084346 | 38.12 | verified |
| testify-external--race-cover-testing | 32 | native | 1.012989 | 1.012573-1.013907 | 0.013790 | 36.27 | verified |
| testify-external--race-cover-testing | 32 | orchestrion | 1.098761 | 1.096127-1.102928 | 0.126615 | 73.03 | verified |
| testify-external--race-cover-testing | 32 | mini | 1.086099 | 1.085029-1.087387 | 0.097001 | 44.97 | verified |
| testify-external--race-cover-testing | 32 | mini-deferred | 1.088775 | 1.088377-1.089499 | 0.094805 | 43.62 | verified |

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
