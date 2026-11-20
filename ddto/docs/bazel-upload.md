# Upload Bazel test payloads

`ddtest instrument bazel upload` processes an invocation's selected test, coverage and
telemetry files. It enriches test events from the supplied Bazel and CI metadata,
validates the CITESTCYCLE schema, and sends the prepared requests to Datadog.
Use `--dry-run` to prepare the same requests while keeping the source files and
skipping HTTP.

The command takes a JSON upload plan. The Bazel integration owns BEP selection
and materializes any remote outputs before constructing this plan. Each plan
must have exclusive ownership of the listed sources during the invocation.
Paths are relative to `--root` and refer to regular files. List sources in the
desired report order. A command invocation processes only the files in its plan.

```json
{
  "schema_version": 1,
  "rules_version": "2.1.0-rc.1",
  "runtime_id": "one-id-per-invocation",
  "codeowners": ".github/CODEOWNERS",
  "contexts": {
    "example": {
      "git.repository_url": "https://github.com/example/project",
      "git.commit.sha": "abc123"
    }
  },
  "sources": [
    {
      "id": "file-000001",
      "path": "pkg/example.topt/test.outputs/payloads/tests/tests-1.json",
      "kind": "test",
      "context": "example",
      "bazel_metadata": {
        "bazel.target": "//pkg:example.topt",
        "bazel.package": "pkg"
      }
    }
  ]
}
```

Omit `codeowners` when there is no owner file. The uploader uses the same
CODEOWNERS parser as `ddtest instrument`. Set `codeowners_dialect` to
`"github"` or `"gitlab"` when the file is staged under a different name or
location. The default is GitHub except for files under `.gitlab/`.
Each source can name one context.
Set `kind` to `test` for test JSON, `coverage` for coverage JSON or MessagePack,
or `telemetry` for JSON telemetry. A telemetry source may set
`telemetry.environment`, `telemetry.append_messages`, `telemetry.provider_suffix`
or `telemetry.synthetic` when the integration has resolved its telemetry replay
for the invocation.

```sh
ddtest instrument bazel upload \
  --root ./bazel-testlogs \
  --plan ./upload-plan.json \
  --validate-enrichment \
  --batch-test-payloads \
  --dry-run \
  --report-json ./upload-report.json
```

For an agentless upload, set `DD_API_KEY` and `DD_SITE` and omit `--dry-run`.
Set `DD_TEST_OPTIMIZATION_AGENT_URL` to send via the EVP proxy.
`DD_TEST_OPTIMIZATION_AGENTLESS_URL` overrides the agentless intake base URL and
is useful with a local test server.

The command reads the rules uploader's variables for the options it is not
given: `DD_TEST_OPTIMIZATION_KEEP_PAYLOADS`,
`DD_TEST_OPTIMIZATION_BATCH_TEST_PAYLOADS`, `DD_TEST_OPTIMIZATION_GZIP` and
`DD_TEST_OPTIMIZATION_WORKERS`. They accept `true`, `false`, `yes`, `no`, `1`
and `0`, or a positive integer for the workers; any other value stops the
command before it reads the plan. Workers reuse their HTTP connections unless
`DD_TEST_OPTIMIZATION_HTTP_KEEP_ALIVE` is false.

A request is attempted up to four times. The command retries 408, 429 and 5xx
responses and transport errors, except a certificate that fails verification,
and waits for the intake's `Retry-After`, at most a minute, or two seconds.

The schema diagnostics are warnings. With `--validate-enrichment`, a missing
required enrichment tag fails its source before upload. By default that check
requires `git.repository_url`, `git.commit.sha`, `bazel.target`, and
`bazel.package`. Pass one or more `--expected-enriched-tag` options to replace
that list.

The worker count defaults to the available Go runtime parallelism. Set
`--workers` or `DD_TEST_OPTIMIZATION_WORKERS` to control concurrency: that many
workers read, enrich and validate sources, taken in plan order, while twice as
many senders, and at least 32, deliver the requests over a shared HTTP
transport. A sender mostly waits for the intake, so latency does not hold up the
preparation, and many small coverage requests overlap. With batching, test
sources with the same context and envelope share a request, up to 32 sources and
the request size limit, as they become ready: a slow source does not hold up the
others, and sources of other kinds or batch keys between them do not split a
batch. Which sources share a request can therefore differ between two runs of
the same plan. Batches preserve each event's runtime ID and split back into
individual sources after a definitive batch rejection, which reads those sources
again. A source leaves memory once its requests are encoded, so at most four
sources per worker are decoded at once, or eight per worker plus 64 with
batching, whatever the intake latency.

A source is deleted when all its requests are acknowledged. After a partial
upload, the command retains the rejected events or the prepared telemetry
request for replay. Pass the retained telemetry source to the same command to
retry that request without applying its plan directives again.
`--keep-payloads` retains the original files. The JSON report records each
source's outcome in plan order. The command exits nonzero when any source fails
or is cancelled.

The embedded CITESTCYCLE schema is copied from
[the Python uploader's schema at 3f387af](https://github.com/DataDog/rules_test_optimization/blob/3f387af594565e6279dca7346c793da92aea032b/tools/core/schemas/agentless-schema.json).
When changing the wire contract, compare both engines against the same test
payloads and check the event and metadata values received at intake.
