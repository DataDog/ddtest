// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package errcode

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorPreservesCodeAndCause(t *testing.T) {
	cause := errors.New("disk full")
	err := WithCode(PlanCacheWriteFailed, cause)

	if got := CodeOf(err); got != PlanCacheWriteFailed {
		t.Fatalf("CodeOf() = %q, want %q", got, PlanCacheWriteFailed)
	}
	if !errors.Is(err, cause) {
		t.Fatal("coded error does not preserve its cause")
	}
	if !strings.Contains(err.Error(), "[plan_cache_write_failed] disk full") {
		t.Fatalf("Error() = %q, want code and cause", err)
	}
}

func TestWithCodePreservesMoreSpecificNestedCode(t *testing.T) {
	cause := errors.New("discovery process failed")
	planErr := WithCode(PlanFullTestDiscoveryFailed, cause)
	runErr := WithCode(RunPlanningFailed, fmt.Errorf("failed to run planning phase: %w", planErr))

	if got := CodeOf(runErr); got != PlanFullTestDiscoveryFailed {
		t.Fatalf("CodeOf() = %q, want nested code %q", got, PlanFullTestDiscoveryFailed)
	}
	if !errors.Is(runErr, cause) {
		t.Fatal("nested coded error does not preserve its cause")
	}
}

func TestCodeOfSpecialValues(t *testing.T) {
	if got := CodeOf(nil); got != None {
		t.Fatalf("CodeOf(nil) = %q, want %q", got, None)
	}
	if got := CodeOf(errors.New("external failure")); got != Unknown {
		t.Fatalf("CodeOf(unclassified) = %q, want %q", got, Unknown)
	}
}

func TestFatalCodesAreUnique(t *testing.T) {
	codes := []Code{
		PlanPlatformDetectionFailed,
		PlanPlatformTagsCreationFailed,
		PlanRuntimeTagsInvalid,
		PlanFrameworkDetectionFailed,
		PlanOptimizationClientCreationFailed,
		PlanTestFilesResolutionFailed,
		PlanOptimizationClientInitializationFailed,
		PlanFullTestDiscoveryFailed,
		PlanFastTestDiscoveryFailed,
		PlanFullDiscoveryResultsProcessingFailed,
		PlanFastDiscoveryResultsProcessingFailed,
		PlanManifestWriteFailed,
		PlanCacheWriteFailed,
		PlanTestFilesWriteFailed,
		PlanSkippablePercentageWriteFailed,
		PlanParallelRunnersWriteFailed,
		PlanTestSplitsWriteFailed,
		RunPlanningFailed,
		RunPlanStatusCheckFailed,
		RunPlanLoadFailed,
		RunParallelRunnersReadFailed,
		RunParallelRunnersParseFailed,
		RunPlatformDetectionFailed,
		RunFrameworkDetectionFailed,
		RunSequentialTestFilesReadFailed,
		RunSequentialTestsFailed,
		RunParallelSplitsReadFailed,
		RunParallelTestFilesReadFailed,
		RunParallelTestsFailed,
		RunCINodeTestFilesMissing,
		RunCINodeTestFilesReadFailed,
		RunCINodeTestsFailed,
	}

	seen := make(map[Code]struct{}, len(codes))
	for _, code := range codes {
		if code == None || code == Unknown || code == "" {
			t.Fatalf("fatal error code uses reserved value %q", code)
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("duplicate fatal error code %q", code)
		}
		seen[code] = struct{}{}
	}
}
