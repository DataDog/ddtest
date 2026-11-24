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

Instead of carrying its metadata and context, a test source can name its
`output` directory, relative to `--root`, and let the command read them. The
command reads `bazel_target_metadata.json` from that directory, in parallel
with the other sources, and picks the context from the plan's
`context_selection`:

```json
{
  "context_selection": {
    "primary": "example",
    "by_repo": [{"repo": "other", "context": "other-context"}],
    "override": false,
    "require_repo_match": false
  },
  "sources": [
    {
      "id": "file-000001",
      "path": "pkg/example.topt/test.outputs/payloads/tests/tests-1.json",
      "kind": "test",
      "output": "pkg/example.topt/test.outputs"
    }
  ]
}
```

This follows the rules uploader. A missing sidecar gives no metadata. A linked
sidecar is refused with `bazel_metadata_unsafe`, and one that is not a JSON
object with `bazel_metadata_invalid`; the source then has no metadata. Numbers
keep the form that Python's `json` module would write. The source uses the
`primary` context when `override` is set, or when `by_repo` has at most one
entry and `require_repo_match` is not set. Otherwise it uses the first
`by_repo` entry for the sidecar's `bazel.test_optimization.repo_name`; when
there is no such value or entry, it warns with `context_repo_metadata_missing`
or `context_repo_not_found` and uses no context. A source with an `output`
cannot also set `context` or `bazel_metadata`, and a plan with such a source
must have a `context_selection` whose contexts are in `contexts`.

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

A source is deleted when all its requests are acknowledged. Bazel can leave its
output trees read-only: when deleting or replacing a source fails, the command
makes the source and its directory writable once and tries again, as the rules
uploader does. After a partial upload, the command retains the rejected events
or the prepared telemetry request for replay. Pass the retained telemetry source
to the same command to retry that request without applying its plan directives
again. `--keep-payloads` retains the original files. The JSON report records
each source's outcome in plan order. With `--batch-test-payloads`, each test
request, whether it carries several sources or one, as after a rejected batch,
appears once in `batch_requests` with the IDs of its sources. Each of those
sources counts the request in its `requests` and names it in `request_ids`, as
the rules uploader's Python batches do, so a failed batch still shows that
every source reached delivery; the report's request totals count each request
once.
The command exits nonzero when any source fails or is cancelled.

The embedded CITESTCYCLE schema is copied from
[the Python uploader's schema at 3f387af](https://github.com/DataDog/rules_test_optimization/blob/3f387af594565e6279dca7346c793da92aea032b/tools/core/schemas/agentless-schema.json).
When changing the wire contract, compare both engines against the same test
payloads and check the event and metadata values received at intake.

## Run the whole uploader

`ddtest instrument bazel post-process` runs the rules uploader's invocation in
one process, after the Python uploader acquires its workspace lock and loads
its configuration, expected targets, contexts, CODEOWNERS file and API key
fingerprint through Bazel's runfiles. It evaluates BEP or execution-log
freshness, stages BEP artifacts, waits for the outputs to settle and discovers
the payloads, selects the expected and fresh outputs, and sends them as
`upload` does, telemetry included. When the request carries the doctor's
configuration, it also applies the doctor's output checks to the selected
outputs: they are listed before the upload deletes what it sends, and each
payload's test facts come from the upload's own preparation, so no payload is
read twice. That preparation streams: instead of decoding each test payload
into maps and encoding its events again, it indexes the payload's bytes once,
writes each event in the same form with the enrichment applied, and checks the
schema, test facts and expected tags on the written events. The request
bodies are byte for byte those of `upload`, which keeps decoding; batched
uploads also keep the decoding path. A failed check fails the run, with the doctor's message and
exit status 1, but every fresh valid payload is still sent; `--dry-run` in
the request's configuration validates and sends nothing. It then retains the
payloads that it did not deliver, cleans its staging, and prints and writes
the uploader's schema v1 report.

The request on standard input is `PostRequest` in
`bazeldoctor/post_request.go`. Credentials come from the environment, as for
`upload`. The command prints the uploader's messages, the doctor's when it
checks, and the summary, and exits with the uploader's status. Before it
sends anything, an input that the Python code reads in its own way, such as an
unusual BEP line, makes it exit with status 75 having printed nothing, and the
Python uploader runs the invocation instead; it does so on Windows too. In the
report's performance section, `main_thread_cpu_seconds` is the process's CPU
time, which Go measures for all its threads together.
