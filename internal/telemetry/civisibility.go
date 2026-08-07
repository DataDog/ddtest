// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetry

import (
	"errors"
	"os/exec"
	"strconv"
	"time"

	"github.com/DataDog/ddtest/internal/git"
)

// EventType identifies the CI Visibility event affected by an ITR decision.
type EventType string

const (
	EventTypeTest  EventType = "test"
	EventTypeSuite EventType = "suite"
)

// CLICommandType identifies a top-level ddtest command.
type CLICommandType string

const (
	CLICommandPlan CLICommandType = "plan"
	CLICommandRun  CLICommandType = "run"
)

// SettingsResponse describes the settings flags represented in telemetry.
type SettingsResponse struct {
	CodeCoverageEnabled        bool
	ITRSkippingEnabled         bool
	EarlyFlakeDetectionEnabled bool
	FlakyTestRetriesEnabled    bool
	TestManagementEnabled      bool
}

func count(client Client, name string, tags []string, value float64) {
	if client != nil {
		client.Count(name, tags).Submit(value)
	}
}

func distribution(client Client, name string, tags []string, value float64) {
	if client != nil {
		client.Distribution(name, tags).Submit(value)
	}
}

func requestCompressedTags(compressed bool) []string {
	if compressed {
		return []string{"rq_compressed:true"}
	}
	return nil
}

func responseCompressedTags(compressed bool) []string {
	if compressed {
		return []string{"rs_compressed:true"}
	}
	return nil
}

func requestErrorTags(statusCode int) []string {
	switch statusCode {
	case 0:
		return []string{"error_type:network"}
	case 400, 401, 403, 404, 408, 429:
		return []string{"error_type:status_code_4xx_response", "status_code:" + strconv.Itoa(statusCode)}
	default:
		if statusCode >= 500 && statusCode < 600 {
			return []string{"error_type:status_code_5xx_response"}
		}
		if statusCode >= 400 && statusCode < 500 {
			return []string{"error_type:status_code_4xx_response"}
		}
		return []string{"error_type:status_code"}
	}
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Milliseconds())
}

func GitRequestsSearchCommits(client Client, requestCompressed bool) {
	count(client, "git_requests.search_commits", requestCompressedTags(requestCompressed), 1)
}

// NewGitCommandTelemetry adapts client to Git's command telemetry interface.
func NewGitCommandTelemetry(client Client) git.CommandTelemetry {
	return gitCommandTelemetry{client: client}
}

type gitCommandTelemetry struct {
	client Client
}

var _ git.CommandTelemetry = gitCommandTelemetry{}

func (t gitCommandTelemetry) Command(commandType git.CommandType) {
	GitCommand(t.client, commandType)
}

func (t gitCommandTelemetry) CommandError(commandType git.CommandType, err error) {
	GitCommandErrors(t.client, commandType, err)
}

func (t gitCommandTelemetry) CommandDuration(commandType git.CommandType, duration time.Duration) {
	GitCommandMs(t.client, commandType, duration)
}

func GitCommand(client Client, commandType git.CommandType) {
	count(client, "git.command", []string{"command:" + string(commandType)}, 1)
}

func GitCommandErrors(client Client, commandType git.CommandType, err error) {
	if err == nil {
		return
	}
	tags := []string{"command:" + string(commandType)}
	tags = append(tags, gitCommandErrorTags(err)...)
	count(client, "git.command_errors", tags, 1)
}

func GitCommandMs(client Client, commandType git.CommandType, duration time.Duration) {
	distribution(client, "git.command_ms", []string{"command:" + string(commandType)}, milliseconds(duration))
}

func CLICommand(client Client, commandType CLICommandType, exitCode int) {
	count(client, "cli.command", cliCommandTags(commandType, exitCode), 1)
}

func CLICommandMs(client Client, commandType CLICommandType, exitCode int, duration time.Duration) {
	distribution(client, "cli.command_ms", cliCommandTags(commandType, exitCode), milliseconds(duration))
}

func cliCommandTags(commandType CLICommandType, exitCode int) []string {
	return []string{"command:" + string(commandType), "exit_code:" + strconv.Itoa(exitCode)}
}

func gitCommandErrorTags(err error) []string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return []string{"exit_code:missing"}
	}
	return []string{"exit_code:" + gitCommandExitCode(exitErr.ExitCode())}
}

func gitCommandExitCode(exitCode int) string {
	switch exitCode {
	case -1, 1, 2, 127, 128, 129:
		return strconv.Itoa(exitCode)
	default:
		return "unknown"
	}
}

func GitRequestsSearchCommitsErrors(client Client, statusCode int) {
	count(client, "git_requests.search_commits_errors", requestErrorTags(statusCode), 1)
}

func GitRequestsSearchCommitsMs(client Client, responseCompressed bool, duration time.Duration) {
	distribution(client, "git_requests.search_commits_ms", responseCompressedTags(responseCompressed), milliseconds(duration))
}

func GitRequestsObjectsPack(client Client, requestCompressed bool) {
	count(client, "git_requests.objects_pack", requestCompressedTags(requestCompressed), 1)
}

func GitRequestsObjectsPackErrors(client Client, statusCode int) {
	count(client, "git_requests.objects_pack_errors", requestErrorTags(statusCode), 1)
}

func GitRequestsObjectsPackMs(client Client, duration time.Duration) {
	distribution(client, "git_requests.objects_pack_ms", nil, milliseconds(duration))
}

func GitRequestsObjectsPackBytes(client Client, value int64) {
	distribution(client, "git_requests.objects_pack_bytes", nil, float64(value))
}

func GitRequestsObjectsPackFiles(client Client, value int) {
	distribution(client, "git_requests.objects_pack_files", nil, float64(value))
}

func GitRequestsSettings(client Client, requestCompressed bool) {
	count(client, "git_requests.settings", requestCompressedTags(requestCompressed), 1)
}

func GitRequestsSettingsErrors(client Client, statusCode int) {
	count(client, "git_requests.settings_errors", requestErrorTags(statusCode), 1)
}

func GitRequestsSettingsResponse(client Client, response SettingsResponse) {
	tags := make([]string, 0, 5)
	if response.CodeCoverageEnabled {
		tags = append(tags, "coverage_enabled")
	}
	if response.ITRSkippingEnabled {
		tags = append(tags, "itrskip_enabled")
	}
	if response.EarlyFlakeDetectionEnabled {
		tags = append(tags, "early_flake_detection_enabled:true")
	}
	if response.FlakyTestRetriesEnabled {
		tags = append(tags, "flaky_test_retries_enabled:true")
	}
	if response.TestManagementEnabled {
		tags = append(tags, "test_management_enabled:true")
	}
	count(client, "git_requests.settings_response", tags, 1)
}

func GitRequestsSettingsMs(client Client, duration time.Duration) {
	distribution(client, "git_requests.settings_ms", nil, milliseconds(duration))
}

func ITRSkippableTestsRequest(client Client, requestCompressed bool) {
	count(client, "itr_skippable_tests.request", requestCompressedTags(requestCompressed), 1)
}

func ITRSkippableTestsRequestErrors(client Client, statusCode int) {
	count(client, "itr_skippable_tests.request_errors", requestErrorTags(statusCode), 1)
}

func ITRSkippableTestsResponseTests(client Client, value int) {
	count(client, "itr_skippable_tests.response_tests", nil, float64(value))
}

func ITRSkippableTestsResponseSuites(client Client, value int) {
	count(client, "itr_skippable_tests.response_suites", nil, float64(value))
}

func ITRSkippableTestsIsEmpty(client Client) {
	count(client, "itr_skippable_tests.is_empty", nil, 1)
}

func ITRSkippableTestsRequestMs(client Client, duration time.Duration) {
	distribution(client, "itr_skippable_tests.request_ms", nil, milliseconds(duration))
}

func ITRSkippableTestsResponseBytes(client Client, responseCompressed bool, value int) {
	distribution(client, "itr_skippable_tests.response_bytes", responseCompressedTags(responseCompressed), float64(value))
}

func ITRSkipped(client Client, eventType EventType, value int) {
	count(client, "itr_skipped", []string{"event_type:" + string(eventType)}, float64(value))
}

func KnownTestsRequest(client Client, requestCompressed bool) {
	count(client, "known_tests.request", requestCompressedTags(requestCompressed), 1)
}

func KnownTestsRequestErrors(client Client, statusCode int) {
	count(client, "known_tests.request_errors", requestErrorTags(statusCode), 1)
}

func KnownTestsRequestMs(client Client, duration time.Duration) {
	distribution(client, "known_tests.request_ms", nil, milliseconds(duration))
}

func KnownTestsResponseBytes(client Client, responseCompressed bool, value int) {
	distribution(client, "known_tests.response_bytes", responseCompressedTags(responseCompressed), float64(value))
}

func KnownTestsResponseTests(client Client, value int) {
	distribution(client, "known_tests.response_tests", nil, float64(value))
}

func TestManagementTestsRequest(client Client, requestCompressed bool) {
	count(client, "test_management_tests.request", requestCompressedTags(requestCompressed), 1)
}

func TestManagementTestsRequestErrors(client Client, statusCode int) {
	count(client, "test_management_tests.request_errors", requestErrorTags(statusCode), 1)
}

func TestManagementTestsRequestMs(client Client, duration time.Duration) {
	distribution(client, "test_management_tests.request_ms", nil, milliseconds(duration))
}

func TestManagementTestsResponseBytes(client Client, responseCompressed bool, value int) {
	distribution(client, "test_management_tests.response_bytes", responseCompressedTags(responseCompressed), float64(value))
}

func TestManagementTestsResponseTests(client Client, value int) {
	distribution(client, "test_management_tests.response_tests", nil, float64(value))
}

func TestSuiteDurationsRequest(client Client, requestCompressed bool) {
	count(client, "test_suite_durations.request", requestCompressedTags(requestCompressed), 1)
}

func TestSuiteDurationsRequestErrors(client Client, statusCode int) {
	count(client, "test_suite_durations.request_errors", requestErrorTags(statusCode), 1)
}

func TestSuiteDurationsRequestMs(client Client, duration time.Duration) {
	distribution(client, "test_suite_durations.request_ms", nil, milliseconds(duration))
}

func TestSuiteDurationsResponseBytes(client Client, responseCompressed bool, value int) {
	distribution(client, "test_suite_durations.response_bytes", responseCompressedTags(responseCompressed), float64(value))
}

func TestSuiteDurationsResponseSuites(client Client, value int) {
	distribution(client, "test_suite_durations.response_suites", nil, float64(value))
}

func TestSuiteDurationsIsEmpty(client Client) {
	count(client, "test_suite_durations.is_empty", nil, 1)
}
