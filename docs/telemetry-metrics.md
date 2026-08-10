# DDTest telemetry metrics

This document tracks DDTest-specific metrics that must be added to the
[`civisibility` namespace in `common_metrics.json`](https://github.com/DataDog/dd-go/blob/prod/trace/apps/tracer-telemetry-intake/telemetry-metrics/static/common_metrics.json).
The names below are the metric names emitted in telemetry payloads; the intake
adds the `dd.instrumentation_telemetry_data.civisibility.` prefix. All metrics
are common metrics and are not sent to customer organizations.

## Pending allowlist additions

| Metric | Type | Data type | Allowed tags | Description |
| --- | --- | --- | --- | --- |
| `cli.command` | count | command | `command`, `exit_code`, `error_code`, `platform`, `framework`, `test_skipping_mode` | Number of completed top-level ddtest commands. `command` is `plan` or `run`; `exit_code` is `0` or `1`; `error_code` is a value from the [DDTest error code catalog](error-codes.md); the remaining tags contain the resolved CLI configuration. |
| `cli.command_ms` | distribution | milliseconds | `command`, `exit_code`, `error_code`, `platform`, `framework`, `test_skipping_mode` | Duration of a top-level ddtest command, tagged by command, exit code, error code, and resolved CLI configuration. |
| `itr_skippable_tests.is_empty` | count | responses | None | Number of successful skippable-tests fetches that returned zero skippable tests or suites. |
| `planning.decision` | count | plans | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled`, `reason`, `target_status` | Number of completed plans. `reason` explains the constraint that selected the parallel runner split; `target_status` is `disabled`, `met`, or `missed`. |
| `planning.test_files` | distribution | test files | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled`, `state` | Number of test files at each planning stage. `state` is `discovered`, `runnable`, or `fully_skipped`. |
| `planning.estimated_time_saved_pct` | distribution | percentage | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Estimated percentage of test runtime saved by skipping decisions. |
| `planning.test_file_durations` | distribution | test files | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled`, `source` | Number of runnable test files weighted using `backend` durations or `default` estimates. |
| `planning.parallel_runners` | distribution | runners | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Number of parallel runners selected by the planner. |
| `planning.expected_full_runtime_ms` | distribution | milliseconds | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Estimated serial runtime of all discovered test files before skipping. |
| `planning.expected_runnable_runtime_ms` | distribution | milliseconds | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Estimated serial runtime after skipping decisions. |
| `planning.expected_wall_time_ms` | distribution | milliseconds | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Estimated wall time for the selected parallel runner split. |
| `planning.split_imbalance_pct` | distribution | percentage | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Difference between the most- and least-loaded runners as a percentage of expected wall time. |
| `planning.disabled_tests` | distribution | tests | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Number of Test Management-disabled tests applied during planning. |
| `planning.forced_run_suites` | distribution | suites | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Number of otherwise-skippable suites kept runnable by an unskippable marker. |
| `test_discovery.duration_ms` | distribution | milliseconds | `discovery_mode`, `success`, `platform`, `framework` | Duration of the discovery strategy selected by the planner. `discovery_mode` is `full` for test discovery or `fast` for test-file discovery; `success` reports whether that selected strategy completed successfully. |
| `test_discovery.tests` | distribution | tests | `discovery_mode`, `success`, `platform`, `framework` | Number of tests returned by selected full test discovery. Emitted only with `discovery_mode:full`. |
| `test_discovery.test_files` | distribution | test files | `discovery_mode`, `success`, `platform`, `framework` | Number of test files returned by selected fast test-file discovery. Emitted only with `discovery_mode:fast`. |
| `test_suite_durations.request` | count | requests | `rq_compressed` | Number of requests sent to the test suite durations endpoint, regardless of success. |
| `test_suite_durations.request_errors` | count | requests | `error_type`, `status_code` | Number of terminal test suite durations request errors. `status_code` is emitted only for 400, 401, 403, 404, 408, and 429 responses. |
| `test_suite_durations.request_ms` | distribution | milliseconds | None | Time to receive a terminal response from the test suite durations endpoint. |
| `test_suite_durations.response_bytes` | distribution | bytes | `rs_compressed` | Wire size of a response page from the test suite durations endpoint. |
| `test_suite_durations.response_suites` | distribution | suites | None | Total number of test suites returned across all response pages. |
| `test_suite_durations.is_empty` | count | responses | None | Number of successful test suite durations fetches that returned zero test suites. |

### Planning tag values

- `reason`: `no_runnable_tests`, `single_runner_only`, `lowest_score`,
  `target_met_lowest_score`, `target_met_changed_selection`, or
  `target_unreachable_lowest_wall_time`.
- `target_status`: `disabled`, `met`, or `missed`.
- `state`: `discovered`, `runnable`, or `fully_skipped`.
- `source`: `backend` or `default`.
- `tia_enabled`: `true` or `false`, representing whether TIA skipping was
  effective after applying backend settings and framework capabilities.
