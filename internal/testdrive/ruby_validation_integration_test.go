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

// Uses already installed gems; no registry downloads or real credentials.
// DDTEST_RUN_RUBY_INTEGRATION_TEST=1 go test ./internal/testdrive -run TestRubyValidationIntegration -v
func TestRubyValidationIntegration(t *testing.T) {
	if os.Getenv("DDTEST_RUN_RUBY_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_RUBY_INTEGRATION_TEST=1 with rspec, minitest, rake and datadog-ci installed")
	}
	runRubyValidationIntegration(t, "rspec", "minitest")
}

func runRubyValidationIntegration(t *testing.T, frameworks ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	metadata := integrationCommand(t, ctx, ".", nil, "ruby", "-rjson", "-e", `puts JSON.generate(ruby: RUBY_VERSION, rspec: Gem::Specification.find_by_name("rspec-core").version.to_s, minitest: Gem::Specification.find_by_name("minitest").version.to_s, tracer: Gem::Specification.find_by_name("datadog-ci").version.to_s, rake: Gem::Specification.find_by_name("rake").version.to_s)`)
	var gems struct{ Ruby, Rspec, Minitest, Tracer, Rake string }
	require.NoError(t, json.Unmarshal([]byte(metadata), &gems))
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	for _, framework := range frameworks {
		t.Run(framework, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project space")
			require.NoError(t, os.MkdirAll(root, 0755))
			retainIntegrationReports(t, root, framework)
			integrationCommand(t, ctx, root, nil, "git", "init", "-q")
			integrationFile(t, root, ".ruby-version", gems.Ruby)
			gem, version, command := "rspec-core", gems.Rspec, "bundle exec rake spec"
			if framework == "minitest" {
				gem, version, command = "minitest", gems.Minitest, "bundle exec rake test"
			}
			integrationFile(t, root, "Gemfile", "source 'https://rubygems.org'\ngem '"+gem+"', '"+version+"'\ngem 'datadog-ci', '"+gems.Tracer+"'\ngem 'rake', '"+gems.Rake+"'\n")
			integrationFile(t, root, "lib/customer.rb", "module Customer; def self.add; 1+1; end; end\n")
			if framework == "rspec" {
				integrationFile(t, root, ".rspec", "--format documentation\n")
				integrationFile(t, root, "Rakefile", "require 'rspec/core/rake_task'\nRSpec::Core::RakeTask.new(:spec)\n")
				integrationFile(t, root, "spec/one_spec.rb", "require_relative '../lib/customer'\nRSpec.describe 'customer suite' do\n it('passes [1, 2]') { raise 'broken' unless Customer.add == 2 }\n specify { Customer.add }\n it('existing failure') { Customer.add; raise 'customer expected failure' }\nend\n")
			} else {
				integrationFile(t, root, "Rakefile", "require 'rake/testtask'\n3.times { |i| Rake::TestTask.new(\"test_#{i}\") { |t| t.pattern = 'test/**/*_test.rb' } }\ntask test: [:test_0, :test_1, :test_2]\n")
				integrationFile(t, root, "test/one_test.rb", "require 'minitest/autorun'\nrequire_relative '../lib/customer'\nclass CustomerTest < Minitest::Test\n define_method('test_passes [1, 2]') { assert_equal 2, Customer.add }\n def test_existing_failure; Customer.add; flunk 'customer expected failure'; end\nend\n")
			}
			workflow := "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: ruby/setup-ruby@v1\n        with: {ruby-version: '" + gems.Ruby + "'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: ruby, ruby-tracer-version: '" + gems.Tracer + "'}\n      - run: " + command + "\n"
			integrationFile(t, root, ".github/workflows/test.yml", workflow)
			integrationCommand(t, ctx, root, nil, "bundle", "lock", "--local")
			before := map[string]string{}
			for _, name := range []string{"Gemfile", "Gemfile.lock", ".github/workflows/test.yml"} {
				data, err := os.ReadFile(filepath.Join(root, name))
				require.NoError(t, err)
				before[name] = string(data)
			}
			// Explicit synthetic Git metadata avoids creating unsigned fixture commits.
			env := []string{"DD_GIT_REPOSITORY_URL=https://example.com/ddtest-ruby-fixture.git", "DD_GIT_COMMIT_SHA=" + strings.Repeat("1", 40), "RUBYOPT=-W0", "BUNDLE_GEMFILE=" + filepath.Join(root, "Gemfile")}
			onboard := integrationCommand(t, ctx, root, env, binary, "onboard", "--framework", framework)
			require.Contains(t, onboard, "native baseline/reporting-only")
			output := integrationCommand(t, ctx, root, env, binary, "testdrive", "--framework", framework, "--all", "--yes")
			require.Contains(t, output, "Local validation: PASSED")
			type rubyReport struct {
				Success       bool
				Preflight     struct{ Ruby string }
				Compatibility struct{ Status string }
				Features      []struct{ Name, Status string }
				Runs          []struct {
					Name       string
					ExitCode   int                          `json:"exit_code"`
					TestCounts struct{ Passed, Failed int } `json:"test_counts"`
				}
				Retained any `json:"retained_execution"`
				Summary  struct {
					FinalResponse string `json:"final_response"`
				}
			}
			var result rubyReport
			path := filepath.Join(root, ".testoptimization/testdrive.json")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			result = rubyReport{}
			require.NoError(t, json.Unmarshal(data, &result))
			require.True(t, result.Success, string(data))
			require.Equal(t, gems.Ruby, result.Preflight.Ruby)
			require.Equal(t, "compatible", result.Compatibility.Status)
			require.Len(t, result.Features, 6)
			for _, feature := range result.Features {
				require.Equal(t, "passed", feature.Status, feature.Name)
			}
			for _, run := range result.Runs[:2] {
				require.Equal(t, 1, run.TestCounts.Failed, "matching existing failures are acceptable")
				require.NotZero(t, run.ExitCode)
			}
			minor := strings.Join(strings.Split(gems.Ruby, ".")[:2], ".")
			matrixWorkflow := strings.Replace(workflow, "    runs-on: ubuntu-latest", "    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        ruby: ['"+minor+"', 'head']", 1)
			matrixWorkflow = strings.Replace(matrixWorkflow, "ruby-version: '"+gems.Ruby+"'", "ruby-version: '${{ matrix.ruby }}'", 1)
			matrixWorkflow = strings.Replace(matrixWorkflow, "      - uses: datadog/", "      - if: matrix.ruby != 'head'\n        uses: datadog/", 1)
			integrationFile(t, root, ".github/workflows/test.yml", matrixWorkflow)
			output = integrationCommand(t, ctx, root, env, binary, "testdrive", "--framework", framework, "--all", "--yes")
			require.Contains(t, output, "Validation verdict: PASSED")
			data, err = os.ReadFile(path)
			require.NoError(t, err)
			result = rubyReport{}
			require.NoError(t, json.Unmarshal(data, &result))
			require.Contains(t, result.Summary.FinalResponse, "Ruby versions 1 instrumented, 1 excluded, 0 unverified")
			// Excluding every consumer must never manufacture completed onboarding.
			integrationFile(t, root, ".github/workflows/test.yml", strings.Replace(matrixWorkflow, "matrix.ruby != 'head'", "false", 1))
			excluded := exec.CommandContext(ctx, binary, "testdrive", "--framework", framework, "--all", "--check-only", "--yes")
			excluded.Dir, excluded.Env = root, append(os.Environ(), env...)
			excludedOutput, excludedErr := excluded.CombinedOutput()
			require.Error(t, excludedErr, string(excludedOutput))
			require.Contains(t, string(excludedOutput), "Every CI entry excludes instrumentation")
			integrationFile(t, root, ".github/workflows/test.yml", workflow)
			integrationCommand(t, ctx, root, env, binary, "testdrive", "--framework", framework, "--all", "--check-only", "--yes")
			data, err = os.ReadFile(path)
			require.NoError(t, err)
			result = rubyReport{}
			require.NoError(t, json.Unmarshal(data, &result))
			require.False(t, result.Success)
			require.NotNil(t, result.Retained)
			for _, modified := range []string{
				strings.Replace(workflow, "ruby-tracer-version: '"+gems.Tracer+"'", "ruby-tracer-version: '99.0.0'", 1),
				strings.Replace(workflow, "ruby-version: '"+gems.Ruby+"'", "ruby-version: '1.9.3'", 1),
			} {
				integrationFile(t, root, ".github/workflows/test.yml", modified)
				check := exec.CommandContext(ctx, binary, "testdrive", "--framework", framework, "--all", "--check-only", "--yes")
				check.Dir, check.Env = root, append(os.Environ(), env...)
				output, err := check.CombinedOutput()
				require.Error(t, err, string(output))
				require.Contains(t, string(output), "Validation verdict: INCOMPLETE")
				require.NotContains(t, string(output), "Running baseline")
			}
			integrationFile(t, root, ".github/workflows/test.yml", workflow)
			for name, expected := range before {
				data, err := os.ReadFile(filepath.Join(root, name))
				require.NoError(t, err)
				require.Equal(t, expected, string(data), name)
			}
			require.NoError(t, os.Remove(filepath.Join(root, "Gemfile.lock")))
			missingLock := exec.CommandContext(ctx, binary, "testdrive", "--framework", framework, "--all", "--check-only", "--yes")
			missingLock.Dir, missingLock.Env = root, append(os.Environ(), env...)
			lockOutput, lockErr := missingLock.CombinedOutput()
			require.Error(t, lockErr, string(lockOutput))
			require.NoFileExists(t, filepath.Join(root, "Gemfile.lock"), "check-only must not create a customer lockfile")
			integrationFile(t, root, "Gemfile.lock", before["Gemfile.lock"])
			entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
			require.NoError(t, err)
			require.Len(t, entries, 2)
			for _, directory := range []string{"spec", "test"} {
				probes, err := filepath.Glob(filepath.Join(root, directory, "ddtest_validation_*"))
				require.NoError(t, err)
				require.Empty(t, probes)
			}
			integrationFile(t, root, ".gitignore", "/.testoptimization/\nGemfile.lock\n")
			protocol := rubyProtocolCommand(t, ctx, root, env, binary, "testdrive", "--framework", framework, "--all", "--agent", "--tracer-version", gems.Tracer, "--yes")
			var state struct {
				Status        string
				NextCommand   []string `json:"next_command"`
				FinalResponse string   `json:"final_response"`
			}
			require.NoError(t, json.Unmarshal([]byte(protocol), &state), protocol)
			require.Equal(t, "complete_locally", state.Status, protocol)
			require.Contains(t, state.NextCommand, "--report")
			saved := rubyProtocolCommand(t, ctx, root, env, binary, "testdrive", "--report", "--format", "json")
			require.NoError(t, json.Unmarshal([]byte(saved), &state), saved)
			require.Equal(t, "complete_locally", state.Status, saved)
			require.Contains(t, state.FinalResponse, "COMPLETE LOCALLY")
			integrationFile(t, root, "Gemfile.lock", before["Gemfile.lock"]+"\n")
			stale := exec.CommandContext(ctx, binary, "testdrive", "--report", "--format", "json")
			stale.Dir = root
			stale.Env = append(os.Environ(), env...)
			staleOutput, staleErr := stale.CombinedOutput()
			require.Error(t, staleErr, string(staleOutput))
			require.Contains(t, string(staleOutput), "stale_report")

			if framework == "rspec" {
				// Equal descriptions and failure messages must not hide a swap
				// between two native examples with different scoped IDs.
				integrationFile(t, root, "spec/one_spec.rb", `require_relative '../lib/customer'
RSpec.describe 'customer suite' do
 it('same description') { Customer.add; raise 'same failure' unless defined?(Datadog::CI) }
 it('same description') { Customer.add; raise 'same failure' if defined?(Datadog::CI) }
end
`)
				regression := exec.CommandContext(ctx, binary, "testdrive", "--framework", "rspec", "--all", "--yes", "--tracer-version", gems.Tracer)
				regression.Dir, regression.Env = root, append(os.Environ(), env...)
				output, err := regression.CombinedOutput()
				require.Error(t, err, string(output))
				require.Contains(t, string(output), "suspected regression")
			}

		})
	}
}

func rubyProtocolCommand(t *testing.T, ctx context.Context, root string, env []string, name string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = root
	command.Env = append(os.Environ(), env...)
	data, err := command.Output()
	require.NoError(t, err, string(data))
	return string(data)
}
