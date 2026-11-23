# Inspect Bazel test outputs

`ddtest instrument bazel inspect` lists the payloads and target sidecars of
Bazel `test.outputs` directories and decodes them in parallel, one output per
CPU. The rules_test_optimization doctor uses it before validating, so that it
does not read and parse every payload on one core. The doctor keeps its checks,
messages and report; the command only reports what each file contains.

It reads one request from standard input and writes one report to standard
output:

```json
{"schema_version": 1, "outputs": ["/abs/bazel-testlogs/pkg/target.topt/test.outputs"]}
```

```json
{
  "schema_version": 1,
  "outputs": [
    {
      "path": "/abs/bazel-testlogs/pkg/target.topt/test.outputs",
      "json_payloads": [
        {"path": "payloads/tests/tests-1.json", "decoded": true, "test_events": 12, "empty_session": false}
      ],
      "msgpack_payloads": [],
      "metadata": [
        {"path": "bazel_target_metadata.json", "document": {"bazel.target": "//pkg:target.topt"}}
      ]
    }
  ]
}
```

Outputs keep the request's order. File paths are relative to their output
and slash-separated, so the doctor joins them to the path it requested; the
files of an output are in directory order. `--workers N` bounds the outputs
inspected at once.

The listing finds what the doctor's `Path.rglob` calls found. JSON and msgpack
payloads are the files below `payloads` named `*.json`, `*.msgpack` or
`*.msgpack.gz`; sidecars are the files named `bazel_target_metadata.json`
anywhere below the output. The walk enters real directories only, keeps links
to regular files, follows a linked `payloads` directory as the root of its
listing, and matches names without case only on Windows.

For a payload that decodes, `test_events` and `empty_session` give what the
doctor's `test_result_event_count` and `completed_empty_test_session` return,
including their Python comparisons: a session ID must be an integer above
zero, an exit code any number equal to zero, and an error zero or `false`. A
sidecar's `document` is the file's JSON as written, without a byte order mark.

A file that is unreadable, is not UTF-8, or is not JSON that Go decodes has
`"decoded": false`, or no `document`. The doctor reads it itself, so its error
messages and its acceptance of JSON that only Python decodes, such as `NaN`,
do not change.
