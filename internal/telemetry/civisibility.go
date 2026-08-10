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

	"github.com/DataDog/ddtest/internal/errcode"
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

// TestDiscoveryMode identifies the discovery strategy selected by the planner.
type TestDiscoveryMode string

const (
	TestDiscoveryModeFull TestDiscoveryMode = "full"
	TestDiscoveryModeFast TestDiscoveryMode = "fast"
)

// PlanningDecisionReason identifies the constraint that determined the
// selected parallel runner split.
type PlanningDecisionReason string

const (
	PlanningDecisionNoRunnableTests             PlanningDecisionReason = "no_runnable_tests"
	PlanningDecisionSingleRunnerOnly            PlanningDecisionReason = "single_runner_only"
	PlanningDecisionLowestScore                 PlanningDecisionReason = "lowest_score"
	PlanningDecisionTargetMetLowestScore        PlanningDecisionReason = "target_met_lowest_score"
	PlanningDecisionTargetMetChangedSelection   PlanningDecisionReason = "target_met_changed_selection"
	PlanningDecisionTargetUnreachableLowestWall PlanningDecisionReason = "target_unreachable_lowest_wall_time"
)

// PlanningTargetStatus identifies whether a configured target time affected
// the planning decision.
type PlanningTargetStatus string

const (
	PlanningTargetDisabled PlanningTargetStatus = "disabled"
	PlanningTargetMet      PlanningTargetStatus = "met"
	PlanningTargetMissed   PlanningTargetStatus = "missed"
)

// PlanningAttributes contains the bounded dimensions common to planning
// metrics.
type PlanningAttributes struct {
	Platform         string
	Framework        string
	TestSkippingMode string
	DiscoveryMode    TestDiscoveryMode
	TIAEnabled       bool
}

// PlanningMetrics contains the outcome of one completed planning operation.
type PlanningMetrics struct {
	Attributes                PlanningAttributes
	DecisionReason            PlanningDecisionReason
	TargetStatus              PlanningTargetStatus
	DiscoveredTestFiles       int
	RunnableTestFiles         int
	FullySkippedTestFiles     int
	BackendDurationTestFiles  int
	DefaultDurationTestFiles  int
	EstimatedTimeSavedPercent float64
	ParallelRunners           int
	ExpectedFullRuntime       time.Duration
	ExpectedRunnableRuntime   time.Duration
	ExpectedWallTime          time.Duration
	SplitImbalancePercent     float64
	DisabledTests             int
	UnskippableMarkerSuites   int
}

// CLICommandAttributes describes the resolved configuration attached to CLI
// command telemetry.
type CLICommandAttributes struct {
	Platform         string
	Framework        string
	TestSkippingMode string
}

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

func CLICommand(client Client, commandType CLICommandType, exitCode int, errorCode errcode.Code, attributes CLICommandAttributes) {
	count(client, "cli.command", cliCommandTags(commandType, exitCode, errorCode, attributes), 1)
}

func CLICommandMs(client Client, commandType CLICommandType, exitCode int, errorCode errcode.Code, attributes CLICommandAttributes, duration time.Duration) {
	distribution(client, "cli.command_ms", cliCommandTags(commandType, exitCode, errorCode, attributes), milliseconds(duration))
}

func TestDiscovery(client Client, mode TestDiscoveryMode, success bool, platform, framework string, duration time.Duration, discovered int) {
	tags := []string{
		"discovery_mode:" + string(mode),
		"success:" + strconv.FormatBool(success),
		"platform:" + platform,
		"framework:" + framework,
	}
	distribution(client, "test_discovery.duration_ms", tags, milliseconds(duration))

	switch mode {
	case TestDiscoveryModeFull:
		distribution(client, "test_discovery.tests", tags, float64(discovered))
	case TestDiscoveryModeFast:
		distribution(client, "test_discovery.test_files", tags, float64(discovered))
	}
}

// Planning records the decisions and estimates from one completed plan.
func Planning(client Client, metrics PlanningMetrics) {
	commonTags := planningTags(metrics.Attributes)
	count(client, "planning.decision", appendPlanningTags(commonTags,
		"reason:"+string(metrics.DecisionReason),
		"target_status:"+string(metrics.TargetStatus),
	), 1)

	distribution(client, "planning.test_files", appendPlanningTags(commonTags, "state:discovered"), float64(metrics.DiscoveredTestFiles))
	distribution(client, "planning.test_files", appendPlanningTags(commonTags, "state:runnable"), float64(metrics.RunnableTestFiles))
	distribution(client, "planning.test_files", appendPlanningTags(commonTags, "state:fully_skipped"), float64(metrics.FullySkippedTestFiles))
	distribution(client, "planning.estimated_time_saved_pct", commonTags, metrics.EstimatedTimeSavedPercent)
	distribution(client, "planning.test_file_durations", appendPlanningTags(commonTags, "source:backend"), float64(metrics.BackendDurationTestFiles))
	distribution(client, "planning.test_file_durations", appendPlanningTags(commonTags, "source:default"), float64(metrics.DefaultDurationTestFiles))
	distribution(client, "planning.parallel_runners", commonTags, float64(metrics.ParallelRunners))
	distribution(client, "planning.expected_full_runtime_ms", commonTags, milliseconds(metrics.ExpectedFullRuntime))
	distribution(client, "planning.expected_runnable_runtime_ms", commonTags, milliseconds(metrics.ExpectedRunnableRuntime))
	distribution(client, "planning.expected_wall_time_ms", commonTags, milliseconds(metrics.ExpectedWallTime))
	distribution(client, "planning.split_imbalance_pct", commonTags, metrics.SplitImbalancePercent)
	distribution(client, "planning.disabled_tests", commonTags, float64(metrics.DisabledTests))
	distribution(client, "planning.forced_run_suites", commonTags, float64(metrics.UnskippableMarkerSuites))
}

func planningTags(attributes PlanningAttributes) []string {
	return []string{
		"platform:" + attributes.Platform,
		"framework:" + attributes.Framework,
		"test_skipping_mode:" + attributes.TestSkippingMode,
		"discovery_mode:" + string(attributes.DiscoveryMode),
		"tia_enabled:" + strconv.FormatBool(attributes.TIAEnabled),
	}
}

func appendPlanningTags(tags []string, additional ...string) []string {
	result := make([]string, 0, len(tags)+len(additional))
	result = append(result, tags...)
	return append(result, additional...)
}

func cliCommandTags(commandType CLICommandType, exitCode int, errorCode errcode.Code, attributes CLICommandAttributes) []string {
	return []string{
		"command:" + string(commandType),
		"exit_code:" + strconv.Itoa(exitCode),
		"error_code:" + string(errorCode),
		"platform:" + attributes.Platform,
		"framework:" + attributes.Framework,
		"test_skipping_mode:" + attributes.TestSkippingMode,
	}
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
