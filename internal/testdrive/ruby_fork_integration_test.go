package testdrive_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// Uses installed gems and real fork/exit! isolation, without editing test hooks.
func TestRubyMinitestForkValidationIntegration(t *testing.T) {
	if os.Getenv("DDTEST_RUN_RUBY_FORK_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_RUBY_FORK_INTEGRATION_TEST=1 with minitest, activesupport, rake and datadog-ci installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	metadata := integrationCommand(t, ctx, ".", nil, "ruby", "-rjson", "-e", `puts JSON.generate(ruby: RUBY_VERSION, gems: %w[minitest activesupport rake datadog-ci].to_h { |name| [name, Gem::Specification.find_by_name(name).version.to_s] })`)
	var installed struct {
		Ruby string
		Gems map[string]string
	}
	require.NoError(t, json.Unmarshal([]byte(metadata), &installed))
	root := t.TempDir()
	retainIntegrationReports(t, root, "minitest-fork")
	integrationCommand(t, ctx, root, nil, "git", "init", "-q")
	gemfile := "source 'https://rubygems.org'\n"
	for _, name := range []string{"minitest", "activesupport", "rake", "datadog-ci"} {
		gemfile += "gem '" + name + "', '" + installed.Gems[name] + "'\n"
	}
	integrationFile(t, root, "Gemfile", gemfile)
	integrationFile(t, root, "Rakefile", "require 'rake/testtask'\nRake::TestTask.new(:test) { |task| task.pattern='test/**/*_test.rb' }\n")
	integrationFile(t, root, "lib/customer.rb", "module Customer; def self.add; 1+1; end; end\n")
	tests := "require 'minitest/autorun'\nrequire 'active_support'\nrequire 'active_support/test_case'\nrequire 'active_support/testing/isolation'\nrequire_relative '../lib/customer'\nclass IsolatedTest < ActiveSupport::TestCase\n include ActiveSupport::Testing::Isolation\n 7.times { |i| define_method(\"test_isolated_#{i}\") { assert_equal 2, Customer.add } }\nend\n"
	integrationFile(t, root, "test/isolation_test.rb", tests)
	integrationFile(t, root, "ci/datadog_test_optimization.rb", onboard.RubyMinitestForkSetup)
	integrationFile(t, root, ".gitignore", "/.testoptimization/\nGemfile.lock\n")
	integrationFile(t, root, ".github/workflows/test.yml", "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: ruby/setup-ruby@v1\n        with: {ruby-version: '"+installed.Ruby+"'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: ruby, ruby-tracer-version: '"+installed.Gems["datadog-ci"]+"'}\n      - run: bundle exec rake test\n        env:\n          RUBYOPT: '${{ env.RUBYOPT }} -r./ci/datadog_test_optimization.rb'\n")
	integrationCommand(t, ctx, root, nil, "bundle", "lock", "--local")
	integrationCommand(t, ctx, root, nil, "git", "add", ".")
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	env := []string{"DD_GIT_REPOSITORY_URL=https://example.com/ddtest-isolation-fixture.git", "DD_GIT_COMMIT_SHA=" + strings.Repeat("1", 40), "RUBYOPT=-W0"}
	protocol := rubyProtocolCommand(t, ctx, root, env, binary, "testdrive", "--framework", "minitest", "--all", "--agent", "--yes", "--tracer-version", installed.Gems["datadog-ci"])
	var state struct{ Status string }
	require.NoError(t, json.Unmarshal([]byte(protocol), &state))
	require.Equal(t, "complete_locally", state.Status, protocol)
	report := rubyProtocolCommand(t, ctx, root, env, binary, "testdrive", "--report", "--format", "json")
	require.NoError(t, json.Unmarshal([]byte(report), &state))
	require.Equal(t, "complete_locally", state.Status, report)
	data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
	require.NoError(t, err)
	var result struct {
		Runs []struct {
			Name       string
			TestCounts struct{ Passed, Failed int } `json:"test_counts"`
		}
		Summary struct{ Status string }
	}
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, "COMPLETE LOCALLY", result.Summary.Status)
	for _, run := range result.Runs[:2] {
		require.Equal(t, 7, run.TestCounts.Passed, run.Name)
		require.Zero(t, run.TestCounts.Failed)
	}
	// The same CI-relative initializer must also work when ddtest installs the
	// tracer in its disposable bundle: native gem compilation changes cwd.
	withoutTracer := strings.Replace(gemfile, "gem 'datadog-ci', '"+installed.Gems["datadog-ci"]+"'\n", "", 1)
	integrationFile(t, root, "Gemfile", withoutTracer)
	integrationCommand(t, ctx, root, nil, "bundle", "lock", "--local")
	lockBefore, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	protocol = rubyProtocolCommand(t, ctx, root, env, binary, "testdrive", "--framework", "minitest", "--all", "--agent", "--yes", "--tracer-version", installed.Gems["datadog-ci"])
	require.NoError(t, json.Unmarshal([]byte(protocol), &state))
	require.Equal(t, "complete_locally", state.Status, protocol)
	lockAfter, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	require.Equal(t, lockBefore, lockAfter)
	manifestAfter, err := os.ReadFile(filepath.Join(root, "Gemfile"))
	require.NoError(t, err)
	require.Equal(t, withoutTracer, string(manifestAfter))
	unchanged, err := os.ReadFile(filepath.Join(root, "test/isolation_test.rb"))
	require.NoError(t, err)
	require.Equal(t, tests, string(unchanged))
}
