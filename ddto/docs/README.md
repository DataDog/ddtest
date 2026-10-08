# Documentation

Start with the [README](../README.md) to install `ddtest`, configure delivery
and run your first tests. These guides cover the current implementation.
Measurement reports describe the exact revisions in their manifests.

## Using ddtest

| Task | Guide |
| --- | --- |
| Configure Mini, provide its runtime or use the Go API | [Runtime configuration and API](mini-runtime.md) |
| Use Testify suites or a replacement fork | [Testify support](testify.md) |
| Choose deferred delivery or understand goleak integration | [Delivery and leak checks](delivery.md) |
| Report fuzz seeds and executable examples | [Fuzz and Examples](fuzz-examples.md) |
| Build tests with Orchestrion | [Orchestrion composition](orchestrion.md) |
| Associate SDK operations with a Mini test | [SDK span copies](sdk-span-mirror.md) |
| Name package services from CODEOWNERS | [Service configuration](codeowners-service.md) |
| Diagnose build or runtime delays | [CLI and runtime timings](cli-debug.md) |

## Maintaining the code

| Task | Guide |
| --- | --- |
| Follow the build, event and delivery lifetimes | [Architecture and diagrams](architecture.md) |
| Update incorporated SDK, codec or platform sources | [Source maintenance](maintenance.md) |
| Understand the SDK comparison and remaining gaps | [Feature parity](ci-parity.md) |
| Choose checks for a change | [Validation contract](validation.md) |
| Maintain ownership-rule parsing | [CODEOWNERS implementation](codeownership.md) |
| Profile CPU, allocations or build time | [Performance and profiling](performance.md) |
| Choose a focused performance change | [Optimization backlog](optimization-backlog.md) |
| Read the retained measurements | [Build, runtime and memory comparison](benchmarks.md) |
| Collect builds or regenerate tables | [Benchmark runner](build-benchmarks.md) |

[`internal/thirdparty`](../internal/thirdparty/README.md) records upstream
repositories, commits, licenses and source hashes. SDK adaptations have a
separate [maintenance record](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md).
Keep those records and compatibility fixtures when replacing benchmark data.
