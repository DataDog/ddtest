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
	"time"

	"github.com/stretchr/testify/require"
)

// Python preserves reporting-only checks; Ruby exercises native paired
// validation, source coverage and all six feature controls.
func TestPublicPythonRubyTestdrives(t *testing.T) {
	if os.Getenv("DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST=1")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 18*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	for _, fixture := range []struct {
		name, command string
		files         map[string]string
	}{
		{"pytest", "python -m pytest", map[string]string{"requirements.txt": "pytest==8.4.2\n", "pytest.ini": "[pytest]\n", "test_one.py": "def test_adds():\n    assert 1 + 1 == 2\n"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project")
			require.NoError(t, os.MkdirAll(root, 0755))
			integrationCommand(t, ctx, root, nil, "git", "init", "-q")
			integrationFile(t, root, ".gitignore", "/.testoptimization/\n__pycache__/\n.pytest_cache/\n")
			for name, contents := range fixture.files {
				integrationFile(t, root, name, contents)
			}
			env := []string{}
			if fixture.name == "pytest" {
				venv := filepath.Join(t.TempDir(), "venv")
				integrationCommand(t, ctx, root, nil, "python3", "-m", "venv", venv)
				env = append(env, "PATH="+filepath.Join(venv, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "VIRTUAL_ENV="+venv)
				integrationCommand(t, ctx, root, env, filepath.Join(venv, "bin", "python"), "-m", "pip", "install", "-r", "requirements.txt")
			}

			integrationFile(t, root, ".github/workflows/test.yml", "jobs:\n  test:\n    steps:\n      - run: "+fixture.command+"\n")
			for _, flags := range [][]string{nil} {
				args := append([]string{"onboard", "--framework", fixture.name}, flags...)
				instructions := integrationCommand(t, ctx, root, env, binary, args...)
				for _, invalid := range []string{"NODE_OPTIONS", "js-tracer-version", "Jest", "Vitest", "package.json", "telemetry_only", "UNVALIDATED", "INCOMPLETE", "--agent", "--format", "summary.final_response", "scratch files"} {
					require.NotContains(t, instructions, invalid)
				}
				require.Contains(t, instructions, "Run the local, credential-free setup check")
			}
			for _, mode := range []struct {
				name string
				args []string
			}{
				{"plain", nil},
			} {
				t.Run(mode.name, func(t *testing.T) {
					retainIntegrationReports(t, root, fixture.name+"-"+mode.name)
					args := []string{"testdrive", "--framework", fixture.name, "--command", fixture.command, "--yes"}
					args = append(args, mode.args...)
					command := exec.CommandContext(ctx, binary, args...)
					command.Dir = root
					command.Env = integrationEnvironment(env)
					output, err := command.CombinedOutput()
					require.NoError(t, err, "a passing command with events retains main's exit 0: %s", output)
					require.Contains(t, string(output), "Test events received.")
					require.NotContains(t, string(output), "INCOMPLETE")
					data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
					require.NoError(t, err, string(output))
					var report struct {
						Framework string
						Success   bool
						Runs      []struct {
							Name           string
							ExitCode       int `json:"exit_code"`
							Instrumented   bool
							TestEventCount int `json:"test_event_count"`
						}
					}
					require.NoError(t, json.Unmarshal(data, &report))
					require.Equal(t, fixture.name, report.Framework)
					require.True(t, report.Success)
					var fields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(data, &fields))
					require.JSONEq(t, `true`, string(fields["telemetry_only"]))
					require.Contains(t, string(fields["summary"]), "UNVALIDATED (TELEMETRY ONLY)")
					require.NotContains(t, string(data), "INCOMPLETE")
					require.Len(t, report.Runs, 1)
					require.Equal(t, "reporting-only", report.Runs[0].Name)
					require.True(t, report.Runs[0].Instrumented)
					require.Zero(t, report.Runs[0].ExitCode, string(output))
					require.Positive(t, report.Runs[0].TestEventCount)
					for name, contents := range fixture.files {
						after, err := os.ReadFile(filepath.Join(root, name))
						require.NoError(t, err)
						require.Equal(t, contents, string(after), name)
					}
					entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
					require.NoError(t, err)
					require.Len(t, entries, 3)
					require.DirExists(t, filepath.Join(root, ".testoptimization/testdrive"))
					require.Contains(t, string(output), "Saved diagnostics")
					require.FileExists(t, filepath.Join(root, ".testoptimization/report.html"))
				})
			}
		})
	}
	for _, name := range []string{"rspec", "minitest"} {
		t.Run(name, func(t *testing.T) { runRubyValidationIntegration(t, name) })
	}
}
