// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package constants

const (
	// TestOptimizationEnabledEnvironmentVariable indicates if Test Optimization mode is enabled.
	// This environment variable should be set to "1" or "true" to enable Test Optimization mode.
	TestOptimizationEnabledEnvironmentVariable = "DD_CIVISIBILITY_ENABLED"

	// TestOptimizationAgentlessEnabledEnvironmentVariable indicates if Test Optimization agentless mode is enabled.
	// This environment variable should be set to "1" or "true" to enable agentless mode for Test Optimization, where traces
	// are sent directly to Datadog without using a local agent.
	TestOptimizationAgentlessEnabledEnvironmentVariable = "DD_CIVISIBILITY_AGENTLESS_ENABLED"

	// TestOptimizationAgentlessURLEnvironmentVariable forces the agentless URL to a custom one.
	// This environment variable allows you to specify a custom URL for the agentless intake in Test Optimization mode.
	TestOptimizationAgentlessURLEnvironmentVariable = "DD_CIVISIBILITY_AGENTLESS_URL"

	// APIKeyEnvironmentVariable indicates the API key to be used for agentless intake.
	// This environment variable should be set to your Datadog API key, allowing the agentless mode to authenticate and
	// send data directly to the Datadog platform.
	APIKeyEnvironmentVariable = "DD_API_KEY"

	// TestOptimizationTestSessionNameEnvironmentVariable indicates the test session name to be used on Test Optimization payloads.
	TestOptimizationTestSessionNameEnvironmentVariable = "DD_TEST_SESSION_NAME"

	// TestOptimizationFlakyRetryEnabledEnvironmentVariable kill-switch that allows to explicitly disable retries even if the remote setting is enabled.
	// This environment variable should be set to "0" or "false" to disable the flaky retry feature.
	TestOptimizationFlakyRetryEnabledEnvironmentVariable = "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED"

	// TestOptimizationManagementEnabledEnvironmentVariable indicates if the test management feature is enabled.
	TestOptimizationManagementEnabledEnvironmentVariable = "DD_TEST_MANAGEMENT_ENABLED"

	// TestOptimizationAttemptToFixRetriesEnvironmentVariable indicates the maximum number of retries for the attempt to fix a test.
	TestOptimizationAttemptToFixRetriesEnvironmentVariable = "DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES"

	// TestOptimizationEnvironmentDataFilePath is the environment variable that holds the path to the file containing the environmental data.
	TestOptimizationEnvironmentDataFilePath = "DD_TEST_OPTIMIZATION_ENV_DATA_FILE"
)
