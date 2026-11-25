# Inspect Bazel test outputs

`ddtest instrument bazel inspect` lists the payloads and target sidecars of
Bazel `test.outputs` directories and reads them in parallel, one output per
CPU. The rules_test_optimization doctor uses it before validating, so that it
does not read and parse every payload on one core. A payload's test facts come
from an index of its bytes, which checks its syntax as `encoding/json` does
without decoding its values into maps. The doctor keeps its checks,
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
files of an output are in directory order. Paths are not cleaned, so a link
followed by `..` leads where it does on disk, as in the doctor's Python.
`--workers N` bounds the outputs inspected at once.

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

## Discover the uploader's payloads

`ddtest instrument bazel discover` finds the `test.outputs` directories below
the testlogs roots in its request and the payload files that the
rules_test_optimization uploader sends from them, walking and listing
directories in parallel. The uploader resolves and deduplicates the roots,
passes them with its `max_depth` and the output keys that BEP staging selected,
and builds its tasks from the report:

```json
{"schema_version": 1, "scan_roots": [{"path": "/abs/bazel-testlogs", "staged": false}], "max_depth": 0, "staged_output_keys": []}
```

```json
{
  "schema_version": 1,
  "outputs": [{"path": "/abs/bazel-testlogs/pkg/target.topt/test.outputs", "output_key": "pkg/target.topt/test.outputs", "scan_root": "/abs/bazel-testlogs", "staged": false}],
  "sources": [{"output": 0, "payload_type": "test", "directory": "tests", "name": "tests-1.json"}],
  "warnings": []
}
```

The report follows the uploader's `discover_file_tasks`. A
`bazel_target_metadata.json` or `outputs.zip` marks an output and ends the
descent; a `test.outputs` directory, or a retry `attempt_N.outputs` with
payloads, is otherwise an output unless it holds another. A retry output's key
ends in `/test.outputs`, like the output it replaced. Outputs are ordered by
key, and a selected staged output replaces a local one with the same key.
Sources are the regular `.json` and `.msgpack` files, compared without case, of
`payloads/tests`, `payloads/coverage` and `payloads/telemetry`, in that order
and by name. A payload directory must be real and resolve inside its output: a
link, or on Windows a junction, below the output is refused with
`payload_symlink_skipped`, as is a linked source file.

The doctor also passes the roots of the targets that it expects, as
`target_roots`. The report then lists, in the same order, whether each root
exists and the outputs below it, found as above except that the root itself is
never an output, as the doctor's `find_test_output_directories(root,
root_is_target=True)` finds them. Target roots are walked in parallel with one
another and add no sources:

```json
{"schema_version": 1, "scan_roots": [], "max_depth": 0, "staged_output_keys": [], "target_roots": ["/abs/bazel-testlogs/pkg/target.topt"]}
```

```json
{"schema_version": 1, "outputs": [], "sources": [], "warnings": [],
 "targets": [{"root": "/abs/bazel-testlogs/pkg/target.topt", "exists": true, "outputs": ["/abs/bazel-testlogs/pkg/target.topt/test.outputs"]}]}
```

## Evaluate BEP freshness

`ddtest instrument bazel freshness` reads the Build Event Protocol JSON files
that the rules_test_optimization doctor and uploader use to tell fresh test
outputs from cached ones. It parses their events in parallel and reports what
the doctor's `_parse_bep_freshness` computes from them: the fresh and cached
output keys, the remote-only outputs, the executions, the platform-skipped
targets and the completeness errors of `bep_topology_errors`. The caller
resolves and deduplicates the files and checks that they exist:

```json
{"schema_version": 1, "files": ["/abs/.topt/bazel-bep.json"], "references": false}
```

With `references` set, the report lists every artifact reference, which BEP
artifact staging reads; otherwise it counts them and lists their labels. Each
execution names the stageable carriers whose identity the caller resolves
against its own workspace, execroot and output base, and each document gives
its build UUID, so that the caller can name a document without one by its
path. The report's comments in `bep.go` describe the compact arrays.

The command handles the BEP that Bazel writes. When a file holds anything that
the Python reads in its own way, such as an invalid or unusual JSON line, text
other than ASCII in the fields that it compares, a record that makes the
doctor fail, or two copies of one build that differ, the report's `status` is
`fallback` and the caller evaluates the files itself, so that its warnings and
failures stay the same.

## Validate the outputs

`ddtest instrument bazel validate` runs the rules_test_optimization doctor's
checks in one process: it finds the expected targets' outputs, evaluates BEP
freshness, stages BEP artifacts, inspects the outputs and validates them, and
writes the doctor's report and support bundle. The doctor still parses its
arguments, loads its configuration and resolves the paths that only it can
find through Bazel's runfiles, and writes the request:

```json
{"schema_version": 1, "args": {"freshness_mode": "required", "...": "..."},
 "config_path": "/abs/doctor.config.json", "config": {"expected_targets": []},
 "bep_files": ["/abs/.topt/bazel-bep.json"], "expected_targets_file": "",
 "execroot_roots": [], "output_base_roots": []}
```

`Request` in `bazeldoctor/request.go` documents every field. When
`execroot_roots` and `output_base_roots` are empty, ddtest computes them as the
doctor's `_execroot_roots()` and `_output_base_roots()` do, from
`DD_TEST_OPTIMIZATION_DOCTOR_EXECROOT`, `DD_TEST_OPTIMIZATION_BAZEL_OUTPUT_BASE`
and the working directory. Like the other
commands, it reads the request from standard input and writes one JSON result
to standard output: how the run ended and the messages that the doctor
prints, such as `{"status": "done", "exit_code": 1, "stdout": "", "stderr":
"[dd-test-optimization-doctor] ..."}`. Like `upload`, it stops on SIGINT or
SIGTERM, after removing what it staged.

When an input needs the Python doctor's own reading, such as invalid JSON, an
operating system error whose text the doctor prints, a URL or zip member that
Python parses in its own way, or a custom support bundle collector, the status
is `fallback`, the messages are empty, and the doctor validates the outputs
itself, writing its own report. The command answers `fallback` on Windows, where Python resolves paths
differently, and for a request from newer rules: a field that this ddtest does
not know, or a schema version above 1.
