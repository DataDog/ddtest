// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package constants

const (
	// TestOptimizationEnabledEnvironmentVariable indicates if Test Optimization mode is enabled.
	TestOptimizationEnabledEnvironmentVariable = "DD_CIVISIBILITY_ENABLED"

	// TestOptimizationAgentlessEnabledEnvironmentVariable indicates if Test Optimization agentless mode is enabled.
	TestOptimizationAgentlessEnabledEnvironmentVariable = "DD_CIVISIBILITY_AGENTLESS_ENABLED"

	// TestOptimizationAgentlessURLEnvironmentVariable forces the agentless URL to a custom one.
	TestOptimizationAgentlessURLEnvironmentVariable = "DD_CIVISIBILITY_AGENTLESS_URL"

	// APIKeyEnvironmentVariable indicates the API key to be used for agentless intake.
	APIKeyEnvironmentVariable = "DD_API_KEY"

	// TestOptimizationTestSessionNameEnvironmentVariable indicates the test session name to be used on Test Optimization payloads.
	TestOptimizationTestSessionNameEnvironmentVariable = "DD_TEST_SESSION_NAME"

	// TestOptimizationFlakyRetryEnabledEnvironmentVariable is a kill-switch that explicitly disables retries even if enabled remotely.
	TestOptimizationFlakyRetryEnabledEnvironmentVariable = "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED"

	// TestOptimizationManagementEnabledEnvironmentVariable indicates if the test management feature is enabled.
	TestOptimizationManagementEnabledEnvironmentVariable = "DD_TEST_MANAGEMENT_ENABLED"

	// TestOptimizationAttemptToFixRetriesEnvironmentVariable indicates the maximum number of retries for the attempt to fix a test.
	TestOptimizationAttemptToFixRetriesEnvironmentVariable = "DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES"
)
