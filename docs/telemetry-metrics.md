# DDTest telemetry metrics

This document tracks DDTest-specific metrics that must be added to the
[`civisibility` namespace in `common_metrics.json`](https://github.com/DataDog/dd-go/blob/prod/trace/apps/tracer-telemetry-intake/telemetry-metrics/static/common_metrics.json).
The names below are the metric names emitted in telemetry payloads; the intake
adds the `dd.instrumentation_telemetry_data.civisibility.` prefix. All metrics
are common metrics and are not sent to customer organizations.

## Pending allowlist additions

| Metric | Type | Data type | Allowed tags | Description |
| --- | --- | --- | --- | --- |
| `itr_skippable_tests.is_empty` | count | responses | None | Number of successful skippable-tests fetches that returned zero skippable tests or suites. |
| `test_suite_durations.request` | count | requests | `rq_compressed` | Number of requests sent to the test suite durations endpoint, regardless of success. |
| `test_suite_durations.request_errors` | count | requests | `error_type`, `status_code` | Number of terminal test suite durations request errors. `status_code` is emitted only for 400, 401, 403, 404, 408, and 429 responses. |
| `test_suite_durations.request_ms` | distribution | milliseconds | None | Time to receive a terminal response from the test suite durations endpoint. |
| `test_suite_durations.response_bytes` | distribution | bytes | `rs_compressed` | Wire size of a response page from the test suite durations endpoint. |
| `test_suite_durations.response_suites` | distribution | suites | None | Total number of test suites returned across all response pages. |
| `test_suite_durations.is_empty` | count | responses | None | Number of successful test suite durations fetches that returned zero test suites. |
