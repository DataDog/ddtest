// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetry

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/git"
)

type recordedMetric struct {
	kind  string
	name  string
	tags  []string
	value float64
}

type recordingClient struct {
	metrics []recordedMetric
}

type recordingMetric struct {
	client *recordingClient
	kind   string
	name   string
	tags   []string
}

func (c *recordingClient) Count(name string, tags []string) Metric {
	return &recordingMetric{client: c, kind: "count", name: name, tags: slices.Clone(tags)}
}

func (c *recordingClient) Distribution(name string, tags []string) Metric {
	return &recordingMetric{client: c, kind: "distribution", name: name, tags: slices.Clone(tags)}
}

func (c *recordingClient) Flush(context.Context) error { return nil }

func (m *recordingMetric) Submit(value float64) {
	m.client.metrics = append(m.client.metrics, recordedMetric{
		kind:  m.kind,
		name:  m.name,
		tags:  m.tags,
		value: value,
	})
}

func TestRequestErrorTags(t *testing.T) {
	tests := []struct {
		statusCode int
		want       []string
	}{
		{statusCode: 0, want: []string{"error_type:network"}},
		{statusCode: 400, want: []string{"error_type:status_code_4xx_response", "status_code:400"}},
		{statusCode: 401, want: []string{"error_type:status_code_4xx_response", "status_code:401"}},
		{statusCode: 403, want: []string{"error_type:status_code_4xx_response", "status_code:403"}},
		{statusCode: 404, want: []string{"error_type:status_code_4xx_response", "status_code:404"}},
		{statusCode: 408, want: []string{"error_type:status_code_4xx_response", "status_code:408"}},
		{statusCode: 429, want: []string{"error_type:status_code_4xx_response", "status_code:429"}},
		{statusCode: 418, want: []string{"error_type:status_code_4xx_response"}},
		{statusCode: 503, want: []string{"error_type:status_code_5xx_response"}},
		{statusCode: 302, want: []string{"error_type:status_code"}},
	}

	for _, test := range tests {
		if got := requestErrorTags(test.statusCode); !slices.Equal(got, test.want) {
			t.Errorf("requestErrorTags(%d) = %v, want %v", test.statusCode, got, test.want)
		}
	}
}

func TestCIVisibilityRequestMetrics(t *testing.T) {
	client := &recordingClient{}
	duration := 1500 * time.Millisecond

	GitRequestsSearchCommits(client, true)
	GitRequestsSearchCommitsErrors(client, 404)
	GitRequestsSearchCommitsMs(client, true, duration)
	GitRequestsObjectsPack(client, false)
	GitRequestsObjectsPackErrors(client, 0)
	GitRequestsObjectsPackMs(client, duration)
	GitRequestsObjectsPackBytes(client, 42)
	GitRequestsObjectsPackFiles(client, 2)
	GitRequestsSettings(client, false)
	GitRequestsSettingsErrors(client, 503)
	GitRequestsSettingsMs(client, duration)
	ITRSkippableTestsRequest(client, true)
	ITRSkippableTestsRequestErrors(client, 418)
	ITRSkippableTestsRequestMs(client, duration)
	ITRSkippableTestsResponseBytes(client, true, 43)
	ITRSkippableTestsResponseTests(client, 3)
	ITRSkippableTestsResponseSuites(client, 2)
	ITRSkippableTestsIsEmpty(client)
	KnownTestsRequest(client, false)
	KnownTestsRequestErrors(client, 0)
	KnownTestsRequestMs(client, duration)
	KnownTestsResponseBytes(client, false, 44)
	KnownTestsResponseTests(client, 4)
	TestManagementTestsRequest(client, true)
	TestManagementTestsRequestErrors(client, 400)
	TestManagementTestsRequestMs(client, duration)
	TestManagementTestsResponseBytes(client, true, 45)
	TestManagementTestsResponseTests(client, 5)
	TestSuiteDurationsRequest(client, true)
	TestSuiteDurationsRequestErrors(client, 429)
	TestSuiteDurationsRequestMs(client, duration)
	TestSuiteDurationsResponseBytes(client, true, 46)
	TestSuiteDurationsResponseSuites(client, 6)
	TestSuiteDurationsIsEmpty(client)

	want := []recordedMetric{
		{kind: "count", name: "git_requests.search_commits", tags: []string{"rq_compressed:true"}, value: 1},
		{kind: "count", name: "git_requests.search_commits_errors", tags: []string{"error_type:status_code_4xx_response", "status_code:404"}, value: 1},
		{kind: "distribution", name: "git_requests.search_commits_ms", tags: []string{"rs_compressed:true"}, value: 1500},
		{kind: "count", name: "git_requests.objects_pack", value: 1},
		{kind: "count", name: "git_requests.objects_pack_errors", tags: []string{"error_type:network"}, value: 1},
		{kind: "distribution", name: "git_requests.objects_pack_ms", value: 1500},
		{kind: "distribution", name: "git_requests.objects_pack_bytes", value: 42},
		{kind: "distribution", name: "git_requests.objects_pack_files", value: 2},
		{kind: "count", name: "git_requests.settings", value: 1},
		{kind: "count", name: "git_requests.settings_errors", tags: []string{"error_type:status_code_5xx_response"}, value: 1},
		{kind: "distribution", name: "git_requests.settings_ms", value: 1500},
		{kind: "count", name: "itr_skippable_tests.request", tags: []string{"rq_compressed:true"}, value: 1},
		{kind: "count", name: "itr_skippable_tests.request_errors", tags: []string{"error_type:status_code_4xx_response"}, value: 1},
		{kind: "distribution", name: "itr_skippable_tests.request_ms", value: 1500},
		{kind: "distribution", name: "itr_skippable_tests.response_bytes", tags: []string{"rs_compressed:true"}, value: 43},
		{kind: "count", name: "itr_skippable_tests.response_tests", value: 3},
		{kind: "count", name: "itr_skippable_tests.response_suites", value: 2},
		{kind: "count", name: "itr_skippable_tests.is_empty", value: 1},
		{kind: "count", name: "known_tests.request", value: 1},
		{kind: "count", name: "known_tests.request_errors", tags: []string{"error_type:network"}, value: 1},
		{kind: "distribution", name: "known_tests.request_ms", value: 1500},
		{kind: "distribution", name: "known_tests.response_bytes", value: 44},
		{kind: "distribution", name: "known_tests.response_tests", value: 4},
		{kind: "count", name: "test_management_tests.request", tags: []string{"rq_compressed:true"}, value: 1},
		{kind: "count", name: "test_management_tests.request_errors", tags: []string{"error_type:status_code_4xx_response", "status_code:400"}, value: 1},
		{kind: "distribution", name: "test_management_tests.request_ms", value: 1500},
		{kind: "distribution", name: "test_management_tests.response_bytes", tags: []string{"rs_compressed:true"}, value: 45},
		{kind: "distribution", name: "test_management_tests.response_tests", value: 5},
		{kind: "count", name: "test_suite_durations.request", tags: []string{"rq_compressed:true"}, value: 1},
		{kind: "count", name: "test_suite_durations.request_errors", tags: []string{"error_type:status_code_4xx_response", "status_code:429"}, value: 1},
		{kind: "distribution", name: "test_suite_durations.request_ms", value: 1500},
		{kind: "distribution", name: "test_suite_durations.response_bytes", tags: []string{"rs_compressed:true"}, value: 46},
		{kind: "distribution", name: "test_suite_durations.response_suites", value: 6},
		{kind: "count", name: "test_suite_durations.is_empty", value: 1},
	}
	assertRecordedMetrics(t, client.metrics, want)
}

func TestCIVisibilitySettingsAndITRMetrics(t *testing.T) {
	client := &recordingClient{}
	GitRequestsSettingsResponse(client, SettingsResponse{
		CodeCoverageEnabled:        true,
		ITRSkippingEnabled:         true,
		EarlyFlakeDetectionEnabled: true,
		FlakyTestRetriesEnabled:    true,
		TestManagementEnabled:      true,
	})
	ITRSkipped(client, EventTypeTest, 2)

	want := []recordedMetric{
		{
			kind:  "count",
			name:  "git_requests.settings_response",
			tags:  []string{"coverage_enabled", "itrskip_enabled", "early_flake_detection_enabled:true", "flaky_test_retries_enabled:true", "test_management_enabled:true"},
			value: 1,
		},
		{kind: "count", name: "itr_skipped", tags: []string{"event_type:test"}, value: 2},
	}
	assertRecordedMetrics(t, client.metrics, want)

	// A nil client is intentionally safe for optional telemetry wiring.
	ITRSkipped(nil, EventTypeTest, 1)
}

func TestGitCommandMetrics(t *testing.T) {
	client := &recordingClient{}
	GitCommand(client, git.CommandGetObjects)
	GitCommandMs(client, git.CommandGetObjects, 1500*time.Millisecond)
	GitCommandErrors(client, git.CommandGetObjects, errors.New("git unavailable"))
	GitCommandErrors(client, git.CommandGetObjects, nil)

	want := []recordedMetric{
		{kind: "count", name: "git.command", tags: []string{"command:get_objects"}, value: 1},
		{kind: "distribution", name: "git.command_ms", tags: []string{"command:get_objects"}, value: 1500},
		{kind: "count", name: "git.command_errors", tags: []string{"command:get_objects", "exit_code:missing"}, value: 1},
	}
	assertRecordedMetrics(t, client.metrics, want)
}

func TestCLICommandMetrics(t *testing.T) {
	client := &recordingClient{}
	attributes := CLICommandAttributes{
		Platform:         "ruby",
		Framework:        "rspec",
		TestSkippingMode: "suite",
	}
	CLICommand(client, CLICommandPlan, 0, attributes)
	CLICommandMs(client, CLICommandPlan, 0, attributes, 1500*time.Millisecond)
	CLICommand(client, CLICommandRun, 1, attributes)
	CLICommandMs(client, CLICommandRun, 1, attributes, 2*time.Second)
	tags := func(command, exitCode string) []string {
		return []string{
			"command:" + command,
			"exit_code:" + exitCode,
			"platform:ruby",
			"framework:rspec",
			"test_skipping_mode:suite",
		}
	}

	want := []recordedMetric{
		{kind: "count", name: "cli.command", tags: tags("plan", "0"), value: 1},
		{kind: "distribution", name: "cli.command_ms", tags: tags("plan", "0"), value: 1500},
		{kind: "count", name: "cli.command", tags: tags("run", "1"), value: 1},
		{kind: "distribution", name: "cli.command_ms", tags: tags("run", "1"), value: 2000},
	}
	assertRecordedMetrics(t, client.metrics, want)
}

func TestGitCommandExitCode(t *testing.T) {
	for _, test := range []struct {
		exitCode int
		want     string
	}{
		{exitCode: -1, want: "-1"},
		{exitCode: 1, want: "1"},
		{exitCode: 2, want: "2"},
		{exitCode: 127, want: "127"},
		{exitCode: 128, want: "128"},
		{exitCode: 129, want: "129"},
		{exitCode: 3, want: "unknown"},
	} {
		if got := gitCommandExitCode(test.exitCode); got != test.want {
			t.Errorf("gitCommandExitCode(%d) = %q, want %q", test.exitCode, got, test.want)
		}
	}
}

func TestGitCommandErrorTagsFromExitError(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 1").Run()
	if got := gitCommandErrorTags(err); !slices.Equal(got, []string{"exit_code:1"}) {
		t.Fatalf("gitCommandErrorTags() = %v, want [exit_code:1]", got)
	}
}

func TestGitCommandTypes(t *testing.T) {
	client := &recordingClient{}
	tests := []struct {
		command git.CommandType
		tag     string
	}{
		{command: git.CommandGetRemote, tag: "command:get_remote"},
		{command: git.CommandGetRemoteUpstreamTracking, tag: "command:get_remote_upstream_tracking"},
		{command: git.CommandGetHead, tag: "command:get_head"},
		{command: git.CommandGetBranch, tag: "command:get_branch"},
		{command: git.CommandCheckShallow, tag: "command:check_shallow"},
		{command: git.CommandUnshallow, tag: "command:unshallow"},
		{command: git.CommandGetLocalCommits, tag: "command:get_local_commits"},
		{command: git.CommandGetObjects, tag: "command:get_objects"},
		{command: git.CommandPackObjects, tag: "command:pack_objects"},
	}
	for _, test := range tests {
		GitCommand(client, test.command)
	}
	if len(client.metrics) != len(tests) {
		t.Fatalf("recorded %d command metrics, want %d", len(client.metrics), len(tests))
	}
	for i, test := range tests {
		if got := client.metrics[i].tags; !slices.Equal(got, []string{test.tag}) {
			t.Errorf("command %q tags = %v, want [%s]", test.command, got, test.tag)
		}
	}
}

func assertRecordedMetrics(t *testing.T, got, want []recordedMetric) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("recorded %d metrics, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].kind != want[i].kind || got[i].name != want[i].name || got[i].value != want[i].value || !slices.Equal(got[i].tags, want[i].tags) {
			t.Errorf("metric %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}
