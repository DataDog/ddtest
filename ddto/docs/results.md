# Initial local results

Local checks ran on Arch Linux/amd64 with `go1.27.0-X:nodwarf5`, the unchanged
SDK `v2.11.0-rc.1`, and frozen Orchestrion commit `5c24783fcd76`. The differential
suite passed, including actual SDK policy/retry behavior, race and coverage builds,
result caching, and user overlay composition. The workflow separately tests official
Go releases on Linux, macOS and Windows; consult its run for current CI status.

## Compilation measurements

Every timing is retained in [benchmark-local.json](benchmark-local.json).
These are wall-clock times for `go test -c` on the small fixture, with
`GOMAXPROCS=8`, identical dependency graphs and alternating variant order.
Runtime and SDK network flushing are excluded. Each cold variant has its own
fresh Go build cache. Warm variants reuse their corresponding cache.

| Scenario | Native median [range], seconds | POC median [range], seconds | Orchestrion median [range], seconds | Runs per variant |
| --- | --- | --- | --- | --- |
| Cold compile | 17.746 [10.432–25.059] | 13.368 [10.635–16.100] | 25.853 [21.418–30.288] | 2 |
| Unchanged warm compile | 0.089 [0.086–3.302] | 1.748 [1.629–1.948] | 2.367 [0.347–3.791] | 5 |
| Comment-only edit | 1.262 [0.091–1.276] | 1.681 [1.614–1.779] | 2.333 [0.542–2.494] | 5 |

The cold POC observations are lower than Orchestrion's two observations. The
native and warm results vary substantially; these measurements do not establish
a stable percentage improvement, nor a production-application performance claim.
The initial warm comparison also shared its output file between variants.
Overwriting another variant binary forced additional links; the corrected
incremental run below is the relevant warm measurement. These original data
remain published rather than being discarded.

The initial edit benchmark changed a comment. Those rows are explicitly retained
as `comment-only`; they do not measure a developer's function-body edit. The current
script uses body edits for future runs. No full-process CPU or aggregate memory
measurement was collected, and binary size is not semantic-equivalence proof.

The initial instrumented fixture binaries were approximately 26.87 MB with both
tools, versus 18.31 MB native. Runtime dependency size remains because both tools
link the same SDK. Compilation work avoided by the specialized front-end does not
remove that runtime dependency graph.

## Corrected incremental comparison

[benchmark-incremental.json](benchmark-incremental.json) retains all 30 measurements
from a follow-up run. Each variant has a separate output directory and the same
binary basename. The run reuses its corresponding previous Go cache, updates all
variants to the same fixture sources, warms them once, then alternates order for
five unchanged runs and five identical function-body edits. SDK and Orchestrion
versions and GOMAXPROCS remain unchanged. There are no additional cold measurements.

| Scenario | Native median [range], seconds | POC median [range], seconds | Orchestrion median [range], seconds |
| --- | --- | --- | --- |
| Unchanged warm compile | 0.086 [0.085–0.089] | 0.123 [0.122–0.127] | 0.341 [0.339–0.342] |
| Function-body edit | 1.288 [1.260–1.562] | 1.774 [1.640–1.787] | 2.613 [2.490–2.684] |

In this fixture the POC adds approximately 37 ms over native unchanged compilation,
compared with 255 ms for Orchestrion. Editing and recompiling has lower observed
POC wall time than Orchestrion in all five pairs. These are promising local
observations, with a small sample and no production-application or cross-machine
performance claim. Runtime event compatibility is established separately by the
SDK differential tests, not by these timings. The driver still queries packages
and validates native testing sources each invocation; that front-end cost remains.

## Decision supported by this POC

The SDK's existing testing hooks can be driven by a small standard-library-only
overlay tool without changing the SDK. The differential tests provide bounded
compatibility evidence for the exercised scenarios. The next performance decision
needs repeated measurements on a representative application with stable controls,
including unchanged runs and real edits. Broad instrumentation and future Go/SDK
versions are outside the present evidence.
