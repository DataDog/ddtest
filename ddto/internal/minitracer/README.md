# Native CI event client

This is POC-owned code, separate from the SDK extraction. The CI wire schema
is adapted from `ddtrace/tracer/civisibility_tslv.go` at the base recorded in
[`../thirdparty/dd-trace-go/SOURCE.json`](../thirdparty/dd-trace-go/SOURCE.json).
Its original Apache-2.0 license and notices are retained there.

| File | Responsibility |
| --- | --- |
| `client.go` | Explicit configuration, immutable common metadata, client construction |
| `batch.go` | Event queue, bounded payloads, byte accounting, serialization, flush/close and error state |
| `runtime.go` | Environment configuration and the process-wide client used by testing hooks |
| `span.go` | The span's event, mutable until Finish seals it in place, its CI identity and its context |
| `test_span.go` | Test events started from shared option lists and per-test values, without an option list for each test |
| `entries.go` | Metadata and metric sets: slices up to 32 entries, maps beyond, encoded as msgpack maps |
| `common_tags.go` | Immutable CI strings, effective byte accounting and event-kind metadata projection |
| `events.go` | CI-only wire schema; generated serialization lives in `events_msgp.go` |

`Finish` seals the span's own event once, and the client queues it; setters
cannot change it afterward. `sendMu` serializes delivery and owns the reusable
payload buffer. `mu` protects queue/error state. In ordinary delivery, a
finisher at the queue bound waits for a sender while deliveries succeed; after a
failed delivery, which keeps its batch, work beyond the bound is rejected.
Deferred delivery can buffer multiple batches until idle; it bounds each
outgoing payload but not the pending queue's total size. `Close` prevents new
events before the final flush, which sends up to `maxConcurrentSends` payloads
at once in both modes. Readers are sealed by `citransport` before buffers or
compressors are reused. Keep these rules together when changing synchronization
or pooling.

The SDK port binds common CI/Git/system strings once per snapshot revision.
Spans retain that read-only base and their own overrides. Getters check local
text/metrics first; delivery lifts compatible defaults without mutating either
map. An overridden key falls back to local strings for peers, so a masked value
cannot consume the event's byte budget. Mixed snapshots fall back per event kind.
`Client.add` records whether an event overrides its snapshot, and delivery
searches only those events for keys to keep out of the shared defaults.
Batch accounting counts a kind's shared snapshot once until an event of that
kind would fall back, and then counts every event's snapshot. Generic child
spans receive no CI defaults. See
[the metadata contract](../../docs/delivery.md#payload-level-common-metadata).

The [architecture guide](../../docs/architecture.md#native-event-ownership)
shows the event and delivery lifetimes. The
[performance guide](../../docs/performance.md#the-ownership-rules-behind-reuse)
explains the allocation changes and the constraints behind buffer reuse.

No APM sampler, security, profiling or remote configuration is hosted here.
An explicit client does not inherit process-global environment configuration.
Changing this code does not update upstream copies automatically; see the
[shared source update procedure](../thirdparty/README.md).
