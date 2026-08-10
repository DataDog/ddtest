// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package errcode provides stable, machine-readable codes for fatal ddtest
// command errors.
package errcode

import (
	"errors"
	"fmt"
)

// Code identifies an actionable fatal error condition.
type Code string

const (
	None    Code = "none"
	Unknown Code = "unknown"

	PlanPlatformDetectionFailed                Code = "plan_platform_detection_failed"
	PlanPlatformTagsCreationFailed             Code = "plan_platform_tags_creation_failed"
	PlanRuntimeTagsInvalid                     Code = "plan_runtime_tags_invalid"
	PlanFrameworkDetectionFailed               Code = "plan_framework_detection_failed"
	PlanOptimizationClientCreationFailed       Code = "plan_optimization_client_creation_failed"
	PlanTestFilesResolutionFailed              Code = "plan_test_files_resolution_failed"
	PlanOptimizationClientInitializationFailed Code = "plan_optimization_client_initialization_failed"
	PlanFullTestDiscoveryFailed                Code = "plan_full_test_discovery_failed"
	PlanFastTestDiscoveryFailed                Code = "plan_fast_test_discovery_failed"
	PlanFullDiscoveryResultsProcessingFailed   Code = "plan_full_discovery_results_processing_failed"
	PlanFastDiscoveryResultsProcessingFailed   Code = "plan_fast_discovery_results_processing_failed"
	PlanManifestWriteFailed                    Code = "plan_manifest_write_failed"
	PlanCacheWriteFailed                       Code = "plan_cache_write_failed"
	PlanTestFilesWriteFailed                   Code = "plan_test_files_write_failed"
	PlanSkippablePercentageWriteFailed         Code = "plan_skippable_percentage_write_failed"
	PlanParallelRunnersWriteFailed             Code = "plan_parallel_runners_write_failed"
	PlanTestSplitsWriteFailed                  Code = "plan_test_splits_write_failed"
	RunPlanningFailed                          Code = "run_planning_failed"
	RunPlanStatusCheckFailed                   Code = "run_plan_status_check_failed"
	RunPlanLoadFailed                          Code = "run_plan_load_failed"
	RunParallelRunnersReadFailed               Code = "run_parallel_runners_read_failed"
	RunParallelRunnersParseFailed              Code = "run_parallel_runners_parse_failed"
	RunPlatformDetectionFailed                 Code = "run_platform_detection_failed"
	RunFrameworkDetectionFailed                Code = "run_framework_detection_failed"
	RunSequentialTestFilesReadFailed           Code = "run_sequential_test_files_read_failed"
	RunSequentialTestsFailed                   Code = "run_sequential_tests_failed"
	RunParallelSplitsReadFailed                Code = "run_parallel_splits_read_failed"
	RunParallelTestFilesReadFailed             Code = "run_parallel_test_files_read_failed"
	RunParallelTestsFailed                     Code = "run_parallel_tests_failed"
	RunCINodeTestFilesMissing                  Code = "run_ci_node_test_files_missing"
	RunCINodeTestFilesReadFailed               Code = "run_ci_node_test_files_read_failed"
	RunCINodeTestsFailed                       Code = "run_ci_node_tests_failed"
)

// Error associates a stable code with an underlying error while preserving
// standard Go error-chain behavior.
type Error struct {
	Code  Code
	cause error
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.cause)
}

func (e *Error) Unwrap() error {
	return e.cause
}

// WithCode associates code with err. An existing ddtest error code deeper in
// the chain wins, so callers can add context without hiding a more precise
// code from the original failure point.
func WithCode(code Code, err error) error {
	if err == nil {
		return nil
	}
	if CodeOf(err) != Unknown {
		return err
	}
	return &Error{Code: code, cause: err}
}

// New creates a coded error without a separate underlying cause.
func New(code Code, message string) error {
	return &Error{Code: code, cause: errors.New(message)}
}

// CodeOf returns the first ddtest error code in err's chain. Successful
// operations use None and unclassified external errors use Unknown.
func CodeOf(err error) Code {
	if err == nil {
		return None
	}

	var codedErr *Error
	if errors.As(err, &codedErr) && codedErr != nil && codedErr.Code != "" {
		return codedErr.Code
	}
	return Unknown
}
