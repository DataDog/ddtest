// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Two projects share a TS file but have different environments and match rules.
// Pin a tracer whose skipped suite names differ from executed suite names.
// Skipping must use source paths and retain that reporting difference.
func TestJestProjectsAndSkippingPathDiagnostics(t *testing.T) {
	requireNPMIntegration(t)
	ctx, cancel := context.WithTimeout(t.Context(), fixtureTimeout)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	integrationCommand(t, ctx, root, nil, "git", "init", "-q")
	integrationFile(t, root, "package.json", `{"private":true,"devDependencies":{"jest":"30.4.2","jest-environment-jsdom":"30.4.1","@swc/jest":"0.2.39","dd-trace":"6.17.0"}}`)
	integrationFile(t, root, "src/useForm.server.test.ts", `test('works', () => expect(1).toBe(1));`)
	config := `module.exports = {projects: [
 {displayName:'Web',rootDir:'.',roots:['<rootDir>/src'],testMatch:['**/*.test.ts'],transform:{'^.+\\.tsx?$':'@swc/jest'},testEnvironment:'jsdom'},
 {displayName:'Server',rootDir:'.',roots:['<rootDir>/src'],testMatch:['**/+([a-zA-Z]).server.test.ts'],transform:{'^.+\\.tsx?$':'@swc/jest'},testEnvironment:'node'}
]};`
	integrationFile(t, root, "scripts/jest/jest.config.js", config)
	integrationCommand(t, ctx, root, nil, "npm", "install", "--no-audit", "--no-fund")
	for _, aligned := range []bool{false, true} {
		command := "./node_modules/.bin/jest --config scripts/jest/jest.config.js --runInBand --watchman=false"
		if aligned {
			// Diagnostic positive control only. ddtest must not change this itself.
			command += " --rootDir ."
		}
		cmd := exec.CommandContext(ctx, binary, "testdrive", "--framework", "jest", "--yes", "--command", command)
		cmd.Dir, cmd.Env = root, append(os.Environ(), "PWD="+root)
		output, runErr := cmd.CombinedOutput()
		data, err := os.ReadFile(filepath.Join(root, ".testoptimization", "testdrive.json"))
		require.NoError(t, err, string(output))
		var report struct {
			Success  bool                                             `json:"success"`
			Features []struct{ Name, Status, Project, Reason string } `json:"features"`
			Projects []struct {
				Project    string
				Discovered bool
			} `json:"project_checks"`
			Runs []struct {
				Name, Project, Command string
				Skipping               *struct {
					SettingsRequests  int    `json:"settings_requests"`
					SkippableRequests int    `json:"skippable_requests"`
					PathMismatch      bool   `json:"suite_path_mismatch"`
					SuiteNameChanged  bool   `json:"suite_name_changed"`
					SkippedByITR      bool   `json:"skipped_by_itr"`
					SourceFile        string `json:"source_file"`
					ReturnedSuite     string `json:"returned_suite"`
					ExecutedTests     int    `json:"executed_tests"`
				} `json:"skipping"`
			} `json:"runs"`
		}
		require.NoError(t, json.Unmarshal(data, &report))
		require.True(t, report.Success, string(data))
		require.NoError(t, runErr, string(data))
		require.Len(t, report.Projects, 2, string(data))
		for _, project := range report.Projects {
			require.True(t, project.Discovered, string(data))
		}
		require.Len(t, report.Features, 12, string(data))
		for _, feature := range report.Features {
			if feature.Name == "skipping" && !aligned {
				require.Contains(t, feature.Reason, "different test.suite")
			}
			require.Equal(t, "passed", feature.Status, string(data))
			require.Contains(t, []string{"Web", "Server"}, feature.Project)
		}
		require.Len(t, report.Runs, 18)
		for _, run := range report.Runs {
			if run.Name == "skipping" {
				require.NotNil(t, run.Skipping)
				require.Positive(t, run.Skipping.SettingsRequests)
				require.Positive(t, run.Skipping.SkippableRequests)
				require.False(t, run.Skipping.PathMismatch)
				require.Equal(t, !aligned, run.Skipping.SuiteNameChanged)
				require.True(t, run.Skipping.SkippedByITR)
				require.Equal(t, run.Skipping.SourceFile, run.Skipping.ReturnedSuite)
				require.Contains(t, run.Skipping.ReturnedSuite, "src/")
				require.NotContains(t, run.Skipping.ReturnedSuite, "../")
				require.Zero(t, run.Skipping.ExecutedTests)
			}
			if run.Project != "" {
				require.Contains(t, run.Command, "--selectProjects "+run.Project)
			}
		}
		entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		probes, err := filepath.Glob(filepath.Join(root, "src/ddtest*"))
		require.NoError(t, err)
		require.Empty(t, probes)
		after, err := os.ReadFile(filepath.Join(root, "scripts/jest/jest.config.js"))
		require.NoError(t, err)
		require.Equal(t, config, string(after))
	}
}
