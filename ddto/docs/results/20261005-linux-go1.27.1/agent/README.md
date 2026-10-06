# Gin runtime with Agent delivery and CI telemetry

This control runs every saved Gin package binary with CI telemetry enabled.
There are three measured runs after one warmup at each CPU count. Test-cycle
events and telemetry use one local HTTP EVP proxy, without credentials or an
external fallback. Agent delivery is uncompressed; the main runtime matrix
measures Agentless gzip delivery separately.

The clock covers test execution and final delivery. The recorded observations
keep durations, event counts, request sizes, CPU time and aggregate memory peaks.
This is a loopback protocol check, not acceptance by a deployed Datadog Agent.

| Project | CPUs | Native | Orchestrion | POC SDK | POC Mini |
| --- | ---: | ---: | ---: | ---: | ---: |
| Gin | 4 | 0.175121 s | 0.222531 s | 0.224727 s (+1.0%; +28.3%) | 0.201225 s (-9.6%; +14.9%) |
| Gin | 32 | 0.196113 s | 0.233637 s | 0.243795 s (+4.3%; +24.3%) | 0.222311 s (-4.8%; +13.4%) |

Three repetitions have limited precision. Read the recorded ranges and CPU totals before interpreting small changes.
