// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testoptimization

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/git/gittest"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/telemetry"
)

type optimizationTelemetryClient struct {
	mu      sync.Mutex
	metrics map[string]float64
}

type optimizationTelemetryMetric struct {
	client *optimizationTelemetryClient
	name   string
}

func (c *optimizationTelemetryClient) Count(name string, _ []string) telemetry.Metric {
	return &optimizationTelemetryMetric{client: c, name: name}
}

func (c *optimizationTelemetryClient) Distribution(name string, _ []string) telemetry.Metric {
	return &optimizationTelemetryMetric{client: c, name: name}
}

func (c *optimizationTelemetryClient) Flush(context.Context) error {
	return nil
}

func (m *optimizationTelemetryMetric) Submit(value float64) {
	m.client.mu.Lock()
	defer m.client.mu.Unlock()
	m.client.metrics[m.name] += value
}

func (c *optimizationTelemetryClient) value(name string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metrics[name]
}

func TestNewTestOptimizationClientWithTelemetryWiresBackendTransport(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/libraries/tests/services/setting" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", constants.ContentTypeJSON)
		_, _ = w.Write([]byte(`{"data":{"attributes":{"tests_skipping":true}}}`))
	}))
	defer server.Close()

	environment.ResetCITags()
	t.Cleanup(environment.ResetCITags)
	environment.AddCITagsMap(map[string]string{
		constants.GitRepositoryURL: "https://github.com/DataDog/ddtest.git",
		constants.GitCommitSHA:     "sha",
		constants.GitBranch:        "main",
	})
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.TestOptimizationAgentlessURLEnvironmentVariable, server.URL)
	t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")

	recorder := &optimizationTelemetryClient{metrics: make(map[string]float64)}
	client := NewTestOptimizationClientWithTelemetry(settings.TestSkippingLevelSuite, recorder)
	transport := client.newAPITransport("service", settings.TestSkippingLevelSuite)
	if transport == nil {
		t.Fatal("telemetry constructor returned a nil backend transport")
	}
	if _, err := transport.GetSettings(); err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}

	if got := recorder.value("git_requests.settings"); got != 1 {
		t.Errorf("settings request count = %v, want 1", got)
	}
	if got := recorder.value("git_requests.settings_response"); got != 1 {
		t.Errorf("settings response count = %v, want 1", got)
	}
	if _, ok := recorder.metrics["git_requests.settings_ms"]; !ok {
		t.Error("settings request duration was not recorded")
	}
	if commits := client.gitCommands.GetLastLocalGitCommitShas(); len(commits) == 0 {
		t.Fatal("telemetry-aware Git runner returned no commits")
	}
	if got := recorder.value("git.command"); got != 1 {
		t.Errorf("Git command count = %v, want 1", got)
	}
	if _, ok := recorder.metrics["git.command_ms"]; !ok {
		t.Error("Git command duration was not recorded")
	}
}
