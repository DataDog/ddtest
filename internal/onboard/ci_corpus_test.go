// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Source-only evaluation against pinned public archives. No package install,
// repository script, browser or test command is executed by this harness.
func TestCIProviderOpenSourceDiscovery(t *testing.T) {
	manifest := os.Getenv("DDTEST_CI_PROVIDER_PROJECTS")
	if manifest == "" {
		t.Skip("set DDTEST_CI_PROVIDER_PROJECTS to a pinned source-archive manifest")
	}
	data, err := os.ReadFile(manifest)
	require.NoError(t, err)
	var projects []struct{ Provider, Repo, Ref, Framework, Path string }
	require.NoError(t, json.Unmarshal(data, &projects))
	var results []map[string]any
	for _, project := range projects {
		t.Run(project.Provider, func(t *testing.T) {
			selectProviderForTest(t, project.Provider)
			scope, err := DiscoverWorkflowScope(project.Path, project.Framework)
			require.NoError(t, err)
			require.NotEmpty(t, scope.Commands, "public CI must expose at least one test command")
			workflows, readErr := readWorkflows(project.Path)
			require.NoError(t, readErr)
			var inventory []map[string]any
			for _, workflow := range workflows {
				for name, job := range workflow.Jobs {
					consumer := false
					for _, step := range job.Steps {
						consumer = consumer || resolveTestStep(project.Path, workflow, job, step, "javascript", project.Framework).Matched
					}
					if consumer {
						inventory = append(inventory, map[string]any{"file": workflow.Path, "job": name, "node": job.Node, "node_source": job.NodeSource, "setup_gaps": job.Gaps})
					}
				}
			}
			plan, planErr := PlanJavaScriptSetup(project.Path, project.Framework, "6.20.1", "datadoghq.com", ">=22")
			result := map[string]any{"provider": project.Provider, "repository": project.Repo, "revision": project.Ref, "framework": project.Framework, "scope": scope, "runtime_inventory": inventory, "project_tests_executed": false, "dependencies_installed": false}
			if planErr != nil {
				result["setup_gap"] = planErr.Error()
			} else {
				diff, err := plan.Diff()
				require.NoError(t, err)
				result["setup_preview"] = diff
			}
			results = append(results, result)
		})
	}
	encoded, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	output := filepath.Join(filepath.Dir(manifest), "discovery-results.json")
	require.NoError(t, os.WriteFile(output, append(encoded, '\n'), 0600))
}
