// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// The public Python CLI exercises paired validation, source coverage and all
// six feature controls, including temporary tracer installation and cleanup.
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
		{"pytest", "python -m pytest", map[string]string{"requirements.txt": "pytest==8.4.2\n", "pytest.ini": "[pytest]\n", "app.py": "def add(a, b):\n    return a + b\n", "test_one.py": "from app import add\ndef test_adds():\n    assert add(1, 1) == 2\n"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project")
			require.NoError(t, os.MkdirAll(root, 0755))
			integrationCommand(t, ctx, root, nil, "git", "init", "-q")
			integrationFile(t, root, ".gitignore", "/.testoptimization/\n__pycache__/\n.pytest_cache/\n")
			for name, contents := range fixture.files {
				integrationFile(t, root, name, contents)
			}
			env := []string{
				"DD_GIT_REPOSITORY_URL=https://example.com/ddtest-pytest-fixture.git",
				"DD_GIT_COMMIT_SHA=0123456789012345678901234567890123456789",
				"DD_GIT_BRANCH=main", "DD_GIT_COMMIT_MESSAGE=pytest validation fixture",
			}
			if fixture.name == "pytest" {
				venv := filepath.Join(t.TempDir(), "venv")
				integrationCommand(t, ctx, root, nil, "python3", "-m", "venv", venv)
				env = append(env, "PATH="+filepath.Join(venv, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "VIRTUAL_ENV="+venv)
				integrationCommand(t, ctx, root, env, filepath.Join(venv, "bin", "python"), "-m", "pip", "install", "-r", "requirements.txt")
			}

			integrationFile(t, root, ".github/workflows/test.yml", "jobs:\n  test:\n    steps:\n      - run: "+fixture.command+"\n")
			// The human instructions keep the manual steps; they mention
			// testdrive --agent only as the standalone agent mode.
			instructions := integrationCommand(t, ctx, root, env, binary, "onboard", "--framework", fixture.name, "--instructions")
			for _, invalid := range []string{"NODE_OPTIONS", "js-tracer-version", "Jest", "Vitest", "package.json", "telemetry_only", "UNVALIDATED", "INCOMPLETE", "--agent --all", "--format", "summary.final_response", "scratch files"} {
				require.NotContains(t, instructions, invalid)
			}
			require.Contains(t, instructions, "Required local validation configurations")
			require.Contains(t, instructions, "python-tracer-version")
			require.Contains(t, instructions, "--all --check-only")
			// A piped onboard is a coding agent's: before CI runs the setup it
			// gets the structured handoff, whose resume_command is the
			// standalone testdrive --agent validation.
			handoff, err := pythonHandoff(ctx, root, env, binary, "onboard", "--framework", fixture.name)
			require.Error(t, err, "setup is still required")
			require.Equal(t, "needs_input", handoff.Status)
			require.Equal(t, "ci_setup_required", handoff.Code)
			require.Equal(t, []string{"testdrive", "--framework", fixture.name, "--all", "--agent", "--yes"}, handoff.ResumeCommand[1:7])
			for _, text := range []string{"python-tracer-version", "final_response unchanged", "never COMPLETE LOCALLY"} {
				require.Contains(t, handoff.Instruction, text)
			}
			require.NotContains(t, handoff.Instruction, "--check-only")
			for _, mode := range []struct {
				name string
				args []string
			}{
				{"plain", nil},
			} {
				t.Run(mode.name, func(t *testing.T) {
					integrationFile(t, root, ".github/workflows/test.yml", "jobs:\n  test:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, api_key: '${{ secrets.DD_API_KEY }}', python-tracer-version: '4.16.0'}\n      - run: "+fixture.command+"\n")
					// With the complete setup in CI, the agent goes straight to validation.
					handoff, err := pythonHandoff(ctx, root, env, binary, "onboard", "--framework", fixture.name, "--agent")
					require.NoError(t, err, handoff.Reason)
					require.Equal(t, "ready", handoff.Status)
					require.Equal(t, []string{"testdrive", "--framework", fixture.name, "--all", "--agent", "--yes"}, handoff.NextCommand[1:7])
					require.Contains(t, handoff.Reason, "never COMPLETE LOCALLY")
					retainIntegrationReports(t, root, fixture.name+"-"+mode.name)
					args := []string{"testdrive", "--framework", fixture.name, "--command", fixture.command, "--tracer-version", "4.16.0", "--yes"}
					args = append(args, mode.args...)
					command := exec.CommandContext(ctx, binary, args...)
					command.Dir = root
					command.Env = integrationEnvironment(env)
					output, err := command.CombinedOutput()
					require.NoError(t, err, "paired validation and all controls must pass: %s", output)
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
					require.NotContains(t, fields, "telemetry_only")
					require.Contains(t, string(fields["summary"]), "PASSED")
					require.NotContains(t, string(data), "INCOMPLETE")
					require.GreaterOrEqual(t, len(report.Runs), 2)
					require.Equal(t, "baseline", report.Runs[0].Name)
					require.False(t, report.Runs[0].Instrumented)
					require.Zero(t, report.Runs[0].ExitCode, string(output))
					require.Equal(t, "reporting-only", report.Runs[1].Name)
					require.True(t, report.Runs[1].Instrumented)
					require.Positive(t, report.Runs[1].TestEventCount)
					var features []struct{ Status string }
					require.NoError(t, json.Unmarshal(fields["features"], &features))
					require.Len(t, features, 6)
					for _, feature := range features {
						require.Equal(t, "passed", feature.Status)
					}
					for name, contents := range fixture.files {
						after, err := os.ReadFile(filepath.Join(root, name))
						require.NoError(t, err)
						require.Equal(t, contents, string(after), name)
					}
					require.NoDirExists(t, filepath.Join(root, ".testoptimization/testdrive"))
					require.FileExists(t, filepath.Join(root, ".testoptimization/report.html"))
				})
			}
		})
	}
	for _, name := range []string{"rspec", "minitest"} {
		t.Run(name, func(t *testing.T) { runRubyValidationIntegration(t, name) })
	}
}

// pythonHandoff runs an agent-facing onboard command and decodes its one JSON
// result from stdout; a needs_input result exits non-zero.
func pythonHandoff(ctx context.Context, root string, env []string, binary string, args ...string) (onboard.AgentResult, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = root
	command.Env = integrationEnvironment(env)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	var result onboard.AgentResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return result, fmt.Errorf("%v %v: %w\nstdout=%s\nstderr=%s", binary, args, err, stdout.String(), stderr.String())
	}
	return result, runErr
}
