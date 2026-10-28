# Documentation

Start with the [README](../README.md) to install ddtest, configure delivery
and run your first tests with `ddtest instrument`. Coding agents have their own
guide in [AGENTS.md](../AGENTS.md). These guides cover the current
implementation. Measurement reports describe the exact revisions in their
manifests.

`ddto` is the internal name of the instrumentation, of this module and of its
packages, and the maintenance guides use it for the component; the commands
that they show run as `ddtest instrument <arguments>`.
[Changes from the standalone ddto](../README.md#changes-from-the-standalone-ddto)
lists the other names that changed. The optimization plan and backlog keep the
`ddto` commands of the runs that they recorded.

## Using ddtest instrument

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
| See what the October 2026 optimization phases measured and changed | [Optimization plan](optimization-plan.md) |
| Read the retained measurements | [Build, runtime and memory comparison](benchmarks.md) |
| Collect builds or regenerate tables | [Benchmark runner](build-benchmarks.md) |
| Compare Go's counters with experimental file coverage | [Per-test coverage modes](https://github.com/tonyredondo/dd-ci-testing-poc/blob/8cc70ca41bdf7f2f5ed276a691ea582f6f15250e/docs/results/file-coverage-20261017-linux-go1.27/README.md) |
| Compare Mini's event recording before and after a change | [Mini event recording](https://github.com/tonyredondo/dd-ci-testing-poc/blob/8cc70ca41bdf7f2f5ed276a691ea582f6f15250e/docs/results/minitracer-20261016-linux-go1.27/README.md), [allocations](https://github.com/tonyredondo/dd-ci-testing-poc/blob/8cc70ca41bdf7f2f5ed276a691ea582f6f15250e/docs/results/minitracer-allocs-20261016-linux-go1.27/README.md), [test events without option lists](https://github.com/tonyredondo/dd-ci-testing-poc/blob/8cc70ca41bdf7f2f5ed276a691ea582f6f15250e/docs/results/minitracer-test-span-20261016-linux-go1.27/README.md), [per-test lookups and options](https://github.com/tonyredondo/dd-ci-testing-poc/blob/8cc70ca41bdf7f2f5ed276a691ea582f6f15250e/docs/results/minitracer-test-path-20261016-linux-go1.27/README.md), [`t.Helper()` only for wrapped tests](https://github.com/tonyredondo/dd-ci-testing-poc/blob/8cc70ca41bdf7f2f5ed276a691ea582f6f15250e/docs/results/minitracer-test-helper-20261017-linux-go1.27/README.md), [all of them together](https://github.com/tonyredondo/dd-ci-testing-poc/blob/8cc70ca41bdf7f2f5ed276a691ea582f6f15250e/docs/results/minitracer-cumulative-20261017-linux-go1.27/README.md) |

[`internal/thirdparty`](../internal/thirdparty/README.md) records upstream
repositories, commits, licenses and source hashes. SDK adaptations have a
separate [maintenance record](../internal/thirdparty/dd-trace-go/ADAPTATIONS.md).
Keep those records and compatibility fixtures when replacing benchmark data.
