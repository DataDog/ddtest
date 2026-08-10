# DDTest error codes

Fatal `ddtest plan` and `ddtest run` errors include a stable error code in the
form `[error_code] error message`. The same value is reported by the
`error_code` tag on the `ddtest.cli.command` and `ddtest.cli.command_ms`
telemetry metrics.

Error codes identify the actionable failure point while the rest of the error
message and its wrapped Go error retain the specific OS, platform, framework,
or API cause. Existing codes must not be reused for a different condition.

## Special telemetry values

| Code | Meaning |
| --- | --- |
| `none` | The command completed successfully. |
| `unknown` | An error from an external or injected implementation did not contain a DDTest error code. Production plan and run failure paths should not use this value. |

## Planning errors

| Code | Condition |
| --- | --- |
| `plan_platform_detection_failed` | The configured platform could not be selected or did not pass its sanity check. |
| `plan_platform_tags_creation_failed` | Runtime or operating-system tags could not be collected from the selected platform. |
| `plan_runtime_tags_invalid` | The `runtime-tags` override could not be parsed. |
| `plan_framework_detection_failed` | The configured test framework is not supported or could not be initialized for the selected platform. |
| `plan_optimization_client_creation_failed` | The Test Optimization client could not be created. |
| `plan_test_files_resolution_failed` | The test include/exclude patterns could not be resolved or the test-file scan failed. |
| `plan_optimization_client_initialization_failed` | The Test Optimization client failed during initialization. |
| `plan_full_test_discovery_failed` | Required full test discovery failed while strict discovery was enabled. |
| `plan_fast_test_discovery_failed` | Fast test-file discovery failed and no full-discovery result was available. |
| `plan_full_discovery_results_processing_failed` | Full-discovery results could not be matched to the configured test selection. |
| `plan_fast_discovery_results_processing_failed` | Fast-discovery results could not be matched to the configured test selection. |
| `plan_manifest_write_failed` | The Test Optimization manifest could not be written. |
| `plan_cache_write_failed` | The Test Optimization plan cache could not be stored. |
| `plan_test_files_write_failed` | The selected test-files artifact could not be written. |
| `plan_skippable_percentage_write_failed` | The skippable-percentage artifact could not be written. |
| `plan_parallel_runners_write_failed` | The parallel-runner-count artifact could not be written. |
| `plan_test_splits_write_failed` | Test split artifacts could not be created or written. |

## Run errors

| Code | Condition |
| --- | --- |
| `run_planning_failed` | The automatic planning phase returned an unclassified error. A classified planning failure retains its more precise `plan_*` code. |
| `run_plan_status_check_failed` | DDTest could not check whether planning artifacts exist. |
| `run_plan_load_failed` | The Test Optimization plan cache could not be loaded. |
| `run_parallel_runners_read_failed` | The parallel-runner-count artifact could not be read. |
| `run_parallel_runners_parse_failed` | The parallel-runner-count artifact did not contain a valid integer. |
| `run_platform_detection_failed` | The configured platform could not be selected or did not pass its sanity check before running tests. |
| `run_framework_detection_failed` | The configured test framework is not supported or could not be initialized before running tests. |
| `run_sequential_test_files_read_failed` | The sequential test-files artifact could not be read. |
| `run_sequential_tests_failed` | The test framework failed while running the sequential test batch. |
| `run_parallel_splits_read_failed` | The test-splits directory could not be read. |
| `run_parallel_test_files_read_failed` | A test split file could not be read. |
| `run_parallel_tests_failed` | The test framework failed in a local parallel worker. |
| `run_ci_node_test_files_missing` | The requested CI-node split file does not exist. |
| `run_ci_node_test_files_read_failed` | The requested CI-node split file could not be read. |
| `run_ci_node_tests_failed` | The test framework failed in a CI-node worker. |
