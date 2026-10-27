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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Pytest validates native results and controlled features. Ruby retains its
// reporting-only telemetry contract.
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
		{"pytest", "python -m pytest", map[string]string{"requirements.txt": "pytest==8.4.2\nddtrace==4.16.0\n", "pytest.ini": "[pytest]\nmarkers = slow\naddopts = -k test_adds\n", "app.py": "def add(a, b):\n    return a + b\n", "test_one.py": "import pytest\nfrom app import add\n@pytest.mark.slow\ndef test_adds():\n    assert add(1, 1) == 2\n"}},
		{"rspec", "bundle exec rspec", map[string]string{"Gemfile": "source 'https://rubygems.org'\ngem 'rspec', '3.13.1'\n", "spec/one_spec.rb": "RSpec.describe 'addition' do\n  it('adds') { expect(1 + 1).to eq(2) }\nend\n"}},
		{"minitest", "bundle exec rake test", map[string]string{"Gemfile": "source 'https://rubygems.org'\ngem 'rake', '13.3.0'\ngem 'minitest', '5.25.5'\n", "Rakefile": "require 'rake/testtask'\nRake::TestTask.new(:test) { |t| t.pattern = 'test/**/*_test.rb' }\n", "test/one_test.rb": "require 'minitest/autorun'\nclass AdditionTest < Minitest::Test\n  def test_adds\n    assert_equal 2, 1 + 1\n  end\nend\n"}},
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
			pythonVersion := ""
			if fixture.name == "pytest" {
				// The disposable project must not inherit the launcher's CI workspace.
				t.Setenv("GITHUB_ACTIONS", "true")
				t.Setenv("GITHUB_WORKSPACE", t.TempDir())
				workspace, err := filepath.EvalSymlinks(root)
				require.NoError(t, err)
				env = append(env, "GITHUB_WORKSPACE="+workspace)
				venv := filepath.Join(t.TempDir(), "venv")
				integrationCommand(t, ctx, root, nil, "python3", "-m", "venv", venv)
				env = append(env, "PATH="+filepath.Join(venv, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "VIRTUAL_ENV="+venv)
				integrationCommand(t, ctx, root, env, filepath.Join(venv, "bin", "python"), "-m", "pip", "install", "-r", "requirements.txt")
				pythonVersion = strings.TrimSpace(integrationCommand(t, ctx, root, env, filepath.Join(venv, "bin", "python"), "-c", "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')"))
				env = append(env, "DD_GIT_REPOSITORY_URL=https://github.com/example/ddtest-pytest-fixture", "DD_GIT_COMMIT_SHA=0123456789012345678901234567890123456789", "DD_GIT_BRANCH=main", "DD_GIT_COMMIT_MESSAGE=pytest validation fixture")
			} else {
				env = append(env, "BUNDLE_PATH="+filepath.Join(t.TempDir(), "bundle"), "BUNDLE_USER_HOME="+t.TempDir())
				integrationCommand(t, ctx, root, env, "bundle", "lock")
			}

			integrationFile(t, root, ".github/workflows/test.yml", "jobs:\n  test:\n    steps:\n      - run: "+fixture.command+"\n")
			for _, flags := range [][]string{nil} {
				args := append([]string{"onboard", "--framework", fixture.name}, flags...)
				instructions := integrationCommand(t, ctx, root, env, binary, args...)
				for _, invalid := range []string{"NODE_OPTIONS", "js-tracer-version", "Jest", "Vitest", "package.json", "telemetry_only", "UNVALIDATED", "INCOMPLETE", "--agent", "--format", "summary.final_response"} {
					require.NotContains(t, instructions, invalid)
				}
				if fixture.name == "pytest" {
					require.Contains(t, instructions, "--all --check-only --yes")
					require.Contains(t, instructions, "baseline/reporting-only comparison")
				} else {
					require.Contains(t, instructions, "Run the local, credential-free setup check")
					require.NotContains(t, instructions, "scratch files")
				}
			}
			if fixture.name == "pytest" {
				integrationFile(t, root, ".github/workflows/test.yml", "jobs:\n  test:\n    steps:\n      - uses: actions/setup-python@v6\n        with:\n          python-version: '"+pythonVersion+"'\n      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: python\n          python-tracer-version: '4.16.0'\n      - run: "+fixture.command+"\n")
			}
			for _, mode := range []struct {
				name string
				args []string
			}{
				{"plain", nil},
				{"keyword selection", []string{"--command", "python -m pytest -k test_adds"}},
				{"marker selection", []string{"--command", "python -m pytest -m slow"}},
				{"attached module", []string{"--command", "python -mpytest -k test_adds"}},
			} {
				if fixture.name != "pytest" && mode.name != "plain" {
					continue
				}
				t.Run(mode.name, func(t *testing.T) {
					retainIntegrationReports(t, root, fixture.name+"-"+mode.name)
					args := []string{"testdrive", "--framework", fixture.name, "--command", fixture.command, "--yes"}
					args = append(args, mode.args...)
					command := exec.CommandContext(ctx, binary, args...)
					command.Dir = root
					command.Env = integrationEnvironment(env)
					output, err := command.CombinedOutput()
					require.NoError(t, err, "%s", output)
					if fixture.name == "pytest" {
						require.Contains(t, string(output), "Validation verdict: PASSED")
					} else {
						require.Contains(t, string(output), "Test events received.")
						require.NotContains(t, string(output), "INCOMPLETE")
					}
					data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
					require.NoError(t, err, string(output))
					var report struct {
						Framework     string
						Success       bool
						Compatibility struct{ Status string }
						Features      []struct{ Name, Status string }
						Summary       struct{ Status string }
						Runs          []struct {
							Name           string
							ExitCode       int `json:"exit_code"`
							Instrumented   bool
							TestEventCount int `json:"test_event_count"`
							Coverage       *struct {
								Status       string
								CoveredTests int `json:"covered_tests"`
							}
						}
					}
					require.NoError(t, json.Unmarshal(data, &report))
					require.Equal(t, fixture.name, report.Framework)
					require.True(t, report.Success)
					var fields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(data, &fields))
					require.NotContains(t, string(data), "INCOMPLETE")
					reportingIndex := 0
					if fixture.name == "pytest" {
						require.NotContains(t, fields, "telemetry_only")
						require.Equal(t, "PASSED", report.Summary.Status)
						require.Equal(t, "compatible", report.Compatibility.Status)
						require.Len(t, report.Features, 6)
						for _, feature := range report.Features {
							require.Equal(t, "passed", feature.Status, feature.Name)
						}
						require.GreaterOrEqual(t, len(report.Runs), 2)
						require.NotNil(t, report.Runs[1].Coverage)
						require.Equal(t, "observed", report.Runs[1].Coverage.Status)
						require.Positive(t, report.Runs[1].Coverage.CoveredTests)
						require.Equal(t, "baseline", report.Runs[0].Name)
						require.False(t, report.Runs[0].Instrumented)
						require.Zero(t, report.Runs[0].ExitCode)
						reportingIndex = 1
					} else {
						require.JSONEq(t, "true", string(fields["telemetry_only"]))
						require.Contains(t, string(fields["summary"]), "UNVALIDATED (TELEMETRY ONLY)")
						require.Len(t, report.Runs, 1)
					}
					reporting := report.Runs[reportingIndex]
					require.Equal(t, "reporting-only", reporting.Name)
					require.True(t, reporting.Instrumented)
					require.Zero(t, reporting.ExitCode, string(output))
					require.Positive(t, reporting.TestEventCount)
					for name, contents := range fixture.files {
						after, err := os.ReadFile(filepath.Join(root, name))
						require.NoError(t, err)
						if name == "Gemfile" {
							require.Contains(t, string(after), "datadog-ci", "Ruby installation updates its bundle")
						} else {
							require.Equal(t, contents, string(after), name)
						}
					}
					entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
					require.NoError(t, err)
					if fixture.name == "pytest" {
						require.Len(t, entries, 2)
					} else {
						require.Len(t, entries, 3)
						require.DirExists(t, filepath.Join(root, ".testoptimization/testdrive"))
					}
					if fixture.name != "pytest" {
						require.Contains(t, string(output), "Saved diagnostics")
					}
					require.FileExists(t, filepath.Join(root, ".testoptimization/report.html"))
				})
			}
		})
	}
}
