# DDTest telemetry metrics

This document tracks DDTest-specific metrics that must be added to the
[`civisibility` namespace in `common_metrics.json`](https://github.com/DataDog/dd-go/blob/prod/trace/apps/tracer-telemetry-intake/telemetry-metrics/static/common_metrics.json).
The names below are the metric names emitted in telemetry payloads; the intake
adds the `dd.instrumentation_telemetry_data.civisibility.` prefix. All metrics
are common metrics and are not sent to customer organizations. DDTest-owned
metrics use the `ddtest.` prefix; the shared `test_suite_durations.*` API
metrics retain their existing names.

## Pending allowlist additions

| Metric | Type | Data type | Allowed tags | Description |
| --- | --- | --- | --- | --- |
| `ddtest.cli.command` | count | command | `command`, `exit_code`, `error_code`, `platform`, `framework`, `test_skipping_mode` | Number of completed top-level ddtest commands. `command` is `plan` or `run`; `exit_code` is `0` or `1`; `error_code` is a value from the [DDTest error code catalog](error-codes.md); the remaining tags contain the resolved CLI configuration. |
| `ddtest.cli.command_ms` | distribution | milliseconds | `command`, `exit_code`, `error_code`, `platform`, `framework`, `test_skipping_mode` | Duration of a top-level ddtest command, tagged by command, exit code, error code, and resolved CLI configuration. |
| `ddtest.git.command` | count | command | `command` | Number of Git commands executed by ddtest. |
| `ddtest.git.command_errors` | count | command | `command`, `exit_code` | Number of Git command failures, tagged by command and bounded exit code. |
| `ddtest.git.command_ms` | distribution | milliseconds | `command` | Duration of a Git command. |
| `ddtest.git_requests.objects_pack` | count | requests | `rq_compressed` | Number of requests sent to the Git object-pack endpoint. |
| `ddtest.git_requests.objects_pack_errors` | count | requests | `error_type`, `status_code` | Number of terminal Git object-pack request errors. |
| `ddtest.git_requests.objects_pack_ms` | distribution | milliseconds | None | Time to receive a terminal Git object-pack response. |
| `ddtest.git_requests.objects_pack_bytes` | distribution | bytes | None | Sum of object-pack file sizes in one payload. |
| `ddtest.git_requests.objects_pack_files` | distribution | files | None | Number of files in one object-pack payload. |
| `ddtest.git_requests.search_commits` | count | requests | `rq_compressed` | Number of requests sent to the search-commits endpoint. |
| `ddtest.git_requests.search_commits_errors` | count | requests | `error_type`, `status_code` | Number of terminal search-commits request errors. |
| `ddtest.git_requests.search_commits_ms` | distribution | milliseconds | `rs_compressed` | Time to receive a terminal search-commits response. |
| `ddtest.git_requests.settings` | count | requests | `rq_compressed` | Number of requests sent to the settings endpoint. |
| `ddtest.git_requests.settings_errors` | count | requests | `error_type`, `status_code` | Number of terminal settings request errors. |
| `ddtest.git_requests.settings_response` | count | responses | `coverage_enabled`, `itrskip_enabled`, `early_flake_detection_enabled`, `flaky_test_retries_enabled`, `test_management_enabled` | Number of settings responses, tagged with the enabled backend features. |
| `ddtest.git_requests.settings_ms` | distribution | milliseconds | None | Time to receive a terminal settings response. |
| `ddtest.itr_skippable_tests.request` | count | requests | `rq_compressed` | Number of requests sent to the skippable-tests endpoint. |
| `ddtest.itr_skippable_tests.request_errors` | count | requests | `error_type`, `status_code` | Number of terminal skippable-tests request errors. |
| `ddtest.itr_skippable_tests.request_ms` | distribution | milliseconds | None | Time to receive a terminal skippable-tests response. |
| `ddtest.itr_skippable_tests.response_bytes` | distribution | bytes | `rs_compressed` | Wire size of a skippable-tests response page. |
| `ddtest.itr_skippable_tests.response_tests` | count | tests | None | Number of tests returned by the skippable-tests endpoint. |
| `ddtest.itr_skippable_tests.response_suites` | count | suites | None | Number of suites returned by the skippable-tests endpoint. |
| `ddtest.itr_skippable_tests.is_empty` | count | responses | None | Number of successful skippable-tests fetches that returned zero skippable tests or suites. |
| `ddtest.itr_skipped` | count | events | `event_type` | Number of tests or suites skipped by TIA. |
| `ddtest.known_tests.request` | count | requests | `rq_compressed` | Number of requests sent to the known-tests endpoint. |
| `ddtest.known_tests.request_errors` | count | requests | `error_type`, `status_code` | Number of terminal known-tests request errors. |
| `ddtest.known_tests.request_ms` | distribution | milliseconds | None | Time to receive a terminal known-tests response. |
| `ddtest.known_tests.response_bytes` | distribution | bytes | `rs_compressed` | Wire size of a known-tests response page. |
| `ddtest.known_tests.response_tests` | distribution | tests | None | Number of tests returned by the known-tests endpoint. |
| `ddtest.planning.decision` | count | plans | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled`, `reason`, `target_status` | Number of completed plans. `reason` explains the constraint that selected the parallel runner split; `target_status` is `disabled`, `met`, or `missed`. |
| `ddtest.planning.test_files` | distribution | test files | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled`, `state` | Number of test files at each planning stage. `state` is `discovered`, `runnable`, or `fully_skipped`. |
| `ddtest.planning.estimated_time_saved_pct` | distribution | percentage | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Estimated percentage of test runtime saved by skipping decisions. |
| `ddtest.planning.test_file_durations` | distribution | test files | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled`, `source` | Number of runnable test files weighted using `backend` durations or `default` estimates. |
| `ddtest.planning.parallel_runners` | distribution | runners | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Number of parallel runners selected by the planner. |
| `ddtest.planning.expected_full_runtime_ms` | distribution | milliseconds | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Estimated serial runtime of all discovered test files before skipping. |
| `ddtest.planning.expected_runnable_runtime_ms` | distribution | milliseconds | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Estimated serial runtime after skipping decisions. |
| `ddtest.planning.expected_wall_time_ms` | distribution | milliseconds | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Estimated wall time for the selected parallel runner split. |
| `ddtest.planning.split_imbalance_pct` | distribution | percentage | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Difference between the most- and least-loaded runners as a percentage of expected wall time. |
| `ddtest.planning.disabled_tests` | distribution | tests | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Number of Test Management-disabled tests applied during planning. |
| `ddtest.planning.forced_run_suites` | distribution | suites | `platform`, `framework`, `test_skipping_mode`, `discovery_mode`, `tia_enabled` | Number of otherwise-skippable suites kept runnable by an unskippable marker. |
| `ddtest.test_discovery.duration_ms` | distribution | milliseconds | `discovery_mode`, `success`, `platform`, `framework` | Duration of the discovery strategy selected by the planner. `discovery_mode` is `full` for test discovery or `fast` for test-file discovery; `success` reports whether that selected strategy completed successfully. |
| `ddtest.test_discovery.tests` | distribution | tests | `discovery_mode`, `success`, `platform`, `framework` | Number of tests returned by selected full test discovery. Emitted only with `discovery_mode:full`. |
| `ddtest.test_discovery.test_files` | distribution | test files | `discovery_mode`, `success`, `platform`, `framework` | Number of test files returned by selected fast test-file discovery. Emitted only with `discovery_mode:fast`. |
| `ddtest.test_management_tests.request` | count | requests | `rq_compressed` | Number of requests sent to the Test Management endpoint. |
| `ddtest.test_management_tests.request_errors` | count | requests | `error_type`, `status_code` | Number of terminal Test Management request errors. |
| `ddtest.test_management_tests.request_ms` | distribution | milliseconds | None | Time to receive a terminal Test Management response. |
| `ddtest.test_management_tests.response_bytes` | distribution | bytes | `rs_compressed` | Wire size of a Test Management response page. |
| `ddtest.test_management_tests.response_tests` | distribution | tests | None | Number of tests returned by the Test Management endpoint. |
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
