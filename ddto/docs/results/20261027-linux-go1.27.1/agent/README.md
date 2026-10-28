# Gin with local Agent delivery and telemetry

Three measured runs follow one warmup at each CPU count, for Native, Orchestrion and POC Mini together. Real CI events and telemetry are sent through the local EVP/telemetry proxy.

Orchestrion's percentage compares the same metric against Native. Mini's percentages compare Orchestrion first, then Native.

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 0.178635 s | 0.242013 s (+35.5%) | 0.203024 s (-16.1%; +13.7%) |
| Gin | `none` | 32 | 0.199985 s | 0.239453 s (+19.7%) | 0.228835 s (-4.4%; +14.4%) |

## Memory

| Project | Flags | CPUs | Native | Orchestrion | POC Mini |
| --- | --- | ---: | ---: | ---: | ---: |
| Gin | `none` | 4 | 40.6 MiB | 84.3 MiB (+107.5%) | 60.8 MiB (-27.9%; +49.6%) |
| Gin | `none` | 32 | 42.6 MiB | 82.0 MiB (+92.6%) | 80.0 MiB (-2.4%; +87.9%) |
