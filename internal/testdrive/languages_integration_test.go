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

// Python/Ruby currently collect real telemetry; they do not claim the paired
// comparison or feature validation implemented for JavaScript frameworks.
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
		{"rspec", "bundle exec rspec", map[string]string{"Gemfile": "source 'https://rubygems.org'\ngem 'rspec', '3.13.1'\n", "spec/one_spec.rb": "RSpec.describe 'addition' do\n  it('adds') { expect(1 + 1).to eq(2) }\nend\n"}},
		{"minitest", "bundle exec rake test", map[string]string{"Gemfile": "source 'https://rubygems.org'\ngem 'rake', '13.3.0'\ngem 'minitest', '5.25.5'\n", "Rakefile": "require 'rake/testtask'\nRake::TestTask.new(:test) { |t| t.pattern = 'test/**/*_test.rb' }\n", "test/one_test.rb": "require 'minitest/autorun'\nclass AdditionTest < Minitest::Test\n  def test_adds\n    assert_equal 2, 1 + 1\n  end\nend\n"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project")
			require.NoError(t, os.MkdirAll(root, 0755))
			retainIntegrationReports(t, root, fixture.name)
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
			} else {
				env = append(env, "BUNDLE_PATH="+filepath.Join(t.TempDir(), "bundle"), "BUNDLE_USER_HOME="+t.TempDir())
				integrationCommand(t, ctx, root, env, "bundle", "lock")
			}
			command := exec.CommandContext(ctx, binary, "testdrive", "--framework", fixture.name, "--command", fixture.command, "--yes")
			command.Dir = root
			command.Env = integrationEnvironment(env)
			output, err := command.CombinedOutput()
			require.Error(t, err, "telemetry alone must not declare onboarding complete: %s", output)
			data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
			require.NoError(t, err, string(output))
			var report struct {
				Framework     string
				Success       bool
				Summary       struct{ Status string }
				Compatibility struct{ Status string }
				Features      []struct{ Name, Status string }
				Runs          []struct {
					Name           string
					ExitCode       int `json:"exit_code"`
					Instrumented   bool
					TestEventCount int `json:"test_event_count"`
				}
			}
			require.NoError(t, json.Unmarshal(data, &report))
			require.Equal(t, fixture.name, report.Framework)
			require.False(t, report.Success)
			require.Equal(t, "INCOMPLETE", report.Summary.Status)
			require.Equal(t, "inconclusive", report.Compatibility.Status)
			require.Len(t, report.Features, 1)
			require.Equal(t, "unvalidated", report.Features[0].Status)
			require.Len(t, report.Runs, 1)
			require.Equal(t, "reporting-only", report.Runs[0].Name)
			require.True(t, report.Runs[0].Instrumented)
			require.Zero(t, report.Runs[0].ExitCode, string(output))
			require.Equal(t, 1, report.Runs[0].TestEventCount)
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
			require.Len(t, entries, 2)
			require.FileExists(t, filepath.Join(root, ".testoptimization/report.html"))
		})
	}
}
