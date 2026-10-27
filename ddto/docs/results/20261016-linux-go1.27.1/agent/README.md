# Gin with local Agent delivery and telemetry

Three measured runs follow one warmup at each CPU count. Native and Orchestrion retain their original observations; Mini uses the new revision. Real CI events and telemetry are sent through the local EVP/telemetry proxy.

Orchestrion's percentage compares the same metric against Native. Mini's percentages compare Orchestrion first, then Native.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.175121 s | 0.222531 s (+27.1%) | 0.225816 s (+1.5%; +28.9%) |
| Gin | `none` | 32 | 0.196113 s | 0.233637 s (+19.1%) | 0.219190 s (-6.2%; +11.8%) |

## Memory

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 41.4 MiB | 77.5 MiB (+87.1%) | 39.3 MiB (-49.3%; -5.2%) |
| Gin | `none` | 32 | 41.5 MiB | 78.2 MiB (+88.3%) | 54.6 MiB (-30.2%; +31.4%) |
