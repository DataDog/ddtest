# Maintainer guide

Start with [architecture](architecture.md). Its diagrams follow a test from CLI
preparation to event delivery and show which package owns each step. Then choose
the guide for the work you need to do:

| You need to... | Read |
| --- | --- |
| Follow a test from CLI invocation to CI event delivery | [Architecture and diagrams](architecture.md) |
| Maintain Testify callers, supported versions or the coverage bridge | [Testify design and compatibility](testify.md) |
| Use deferred delivery or maintain automatic goleak support | [Delivery checkpoints and goleak](delivery.md) |
| Update the SDK, MessagePack codecs or platform subset | [Source updates and maintenance](maintenance.md) |
| Change allocations, batching, compression or build preparation | [Performance and ownership constraints](performance.md) |
| Configure Mini or use its public API | [Native runtime usage](mini-runtime.md) |
| Compare CI features and policy combinations with the SDK | [Feature parity and event counts](ci-parity.md) |
| Check what compatibility tests establish | [Validation contract](validation.md) |
| Compare repeated execution of the complete CI matrix | [Whole-matrix timing](ci-parity.md#repeated-whole-matrix-timing) |
| Understand Testify discovery and selective tool dispatch | [Testify design](testify.md) and [preparation constraints](performance.md) |
| Repeat compile benchmarks or regenerate their tables | [Build benchmark runner and protocol](build-benchmarks.md) |
| Compare build time, runtime and memory | [Latest benchmark comparison](benchmarks.md) |
| Pick up deferred performance work | [Optimization backlog](optimization-backlog.md) |

The source record lives beside each incorporated library in
[`internal/thirdparty`](../internal/thirdparty/README.md). Its manifests identify
the exact upstream revision, original paths, licenses and local changes.
The code in `internal/minitracer`, `internal/citransport`, `internal/runner` and
`internal/instrument` belongs to this POC.

The guides describe the current implementation. Benchmark manifests record the
exact inputs used for measurement; editing a guide does not rerun a benchmark.

The [latest dataset](results/20261005-linux-go1.27.1/README.md) contains 6,224
comparative builds, 1,200 measured runtime groups, memory peaks and twelve
rounds of the 115-case parity comparison. Four Gin race cells have real runtime
failures; their records remain explicit. Only this benchmark series is checked in.
