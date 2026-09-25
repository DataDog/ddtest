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

// Exercise real Jest CLI parsing, including an existing CLI threshold, and
// verify that probes preserve coverage telemetry while bypassing suite quotas.
func TestJestCoverageProbeValidation(t *testing.T) {
	requireNPMIntegration(t)
	ctx, cancel := context.WithTimeout(t.Context(), fixtureTimeout)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	integrationCommand(t, ctx, root, nil, "git", "init", "-q")
	integrationFile(t, root, "package.json", `{"scripts":{"test":"jest --runInBand"},"devDependencies":{"jest":"`+jestVersion+`"}}`)
	integrationFile(t, root, "sum.js", `module.exports = (a, b) => a + b;`)
	integrationFile(t, root, "sum.test.js", `test('adds', () => expect(require('./sum')(1, 2)).toBe(3));`)
	config := `module.exports = {collectCoverage: true, collectCoverageFrom: ['sum.js'], coverageReporters: ['text'], coverageThreshold: {global: {lines: 100, statements: 100}}};`
	integrationFile(t, root, "jest.config.js", config)
	integrationCommand(t, ctx, root, nil, "npm", "install", "--no-audit", "--no-fund")
	for _, threshold := range []string{"100", "101"} {
		t.Run(threshold, func(t *testing.T) {
			command := `npm test -- --coverageThreshold='{"global":{"lines":` + threshold + `}}'`
			cmd := exec.CommandContext(ctx, binary, "testdrive", "--framework", "jest", "--yes", "--command", command)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PWD="+root)
			output, runErr := cmd.CombinedOutput()
			require.Contains(t, string(output), "Keep this JSON report")
			data, err := os.ReadFile(filepath.Join(root, ".testoptimization", "testdrive.json"))
			require.NoError(t, err)
			var report struct {
				Success       bool `json:"success"`
				Compatibility struct {
					Status string `json:"status"`
				} `json:"compatibility"`
				Features []struct {
					Status string `json:"status"`
				} `json:"features"`
				Runs []struct {
					Name               string `json:"name"`
					Command            string `json:"command"`
					ExitCode           int    `json:"exit_code"`
					ProbeMode          string `json:"probe_mode"`
					ThresholdsDisabled bool   `json:"probe_coverage_thresholds_disabled"`
					Diagnostic         string `json:"diagnostic"`
				} `json:"runs"`
			}
			require.NoError(t, json.Unmarshal(data, &report))
			for _, run := range report.Runs {
				if run.Diagnostic != "" {
					t.Log(run.Name, run.Diagnostic)
				}
			}
			require.NoError(t, runErr, string(data))
			require.True(t, report.Success, string(data))
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Len(t, report.Features, 6)
			for _, feature := range report.Features {
				require.Equal(t, "passed", feature.Status)
			}
			require.Len(t, report.Runs, 10)
			for _, run := range report.Runs {
				require.Equal(t, run.ProbeMode != "", run.ThresholdsDisabled)
				if run.ProbeMode == "" {
					require.NotContains(t, run.Command, "--coverageThreshold={}")
					if threshold == "101" {
						require.Equal(t, 1, run.ExitCode)
						require.Contains(t, run.Diagnostic, "threshold (101%)")
					} else {
						require.Zero(t, run.ExitCode)
						require.Empty(t, run.Diagnostic)
					}
				}
			}
			entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
			require.NoError(t, err)
			require.Len(t, entries, 1)
			probes, err := filepath.Glob(filepath.Join(root, "*ddtest*"))
			require.NoError(t, err)
			require.Empty(t, probes)
			after, err := os.ReadFile(filepath.Join(root, "jest.config.js"))
			require.NoError(t, err)
			require.Equal(t, config, string(after))
		})
	}
}
