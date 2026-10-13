# Gin with local Agent delivery and telemetry

Three measured runs follow one warmup at each CPU count. Native and Orchestrion retain their original observations; Mini uses the new revision. Real CI events and telemetry are sent through the local EVP/telemetry proxy.

Orchestrion's percentage compares the same metric against Native. Mini's percentages compare Orchestrion first, then Native.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.175121 s | 0.222531 s (+27.1%) | 0.220873 s (-0.7%; +26.1%) |
| Gin | `none` | 32 | 0.196113 s | 0.233637 s (+19.1%) | 0.213784 s (-8.5%; +9.0%) |

## Memory

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 41.4 MiB | 77.5 MiB (+87.1%) | 66.4 MiB (-14.3%; +60.3%) |
| Gin | `none` | 32 | 41.5 MiB | 78.2 MiB (+88.3%) | 84.1 MiB (+7.4%; +102.3%) |
