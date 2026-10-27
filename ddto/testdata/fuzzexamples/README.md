# SDK Fuzz and Examples fixtures

These workloads and their assertions come from DataDog/dd-trace-go PR #5442
at `870449702d0a0cea26a6223eefe2f0a198069d79`. [SOURCE.json](SOURCE.json)
records every original path, original hash and local hash. [LICENSE](LICENSE)
is the upstream Apache-2.0 license.

The integration harness copies these sources into isolated SDK and Mini modules.
It relocates helper imports; Mini also relocates runtime imports. The mock intake
exports wire events and optional coverage observations. Covered variants add a
production helper and calls to it without changing the original assertions.
The runtime, workloads and original expectations remain separate from the
wire comparator. See [Fuzz and Examples](../../docs/fuzz-examples.md) for cases,
commands and update checks.

The local `app/cache_control_test.go` initializer applies the intake-owned read
cache in each process, before the original TestMain takes a child/worker branch.
The adapted mock allocates a directory per intake and passes it to children.
`internal/mockci/cache_scope_test.go` checks distinct roots and exact environment
restoration. The cache remains active within an intake. These controls prevent
independent loopback servers from sharing policies when a TCP port is reused.
The original SDK workload assertions remain unchanged. `.gitattributes` retains
LF source bytes on Windows as well as Unix.
