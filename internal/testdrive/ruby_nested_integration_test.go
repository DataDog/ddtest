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

// Exercise the review findings through real Bundler, Minitest and the CLI.
func TestRubyNestedWorkingDirectoryIntegration(t *testing.T) {
	if os.Getenv("DDTEST_RUN_RUBY_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_RUBY_INTEGRATION_TEST=1 with minitest, rake and datadog-ci installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	metadata := integrationCommand(t, ctx, ".", nil, "ruby", "-rjson", "-e", `puts JSON.generate(ruby:RUBY_VERSION,minitest:Gem::Specification.find_by_name("minitest").version.to_s,rake:Gem::Specification.find_by_name("rake").version.to_s,tracer:Gem::Specification.find_by_name("datadog-ci").version.to_s)`)
	var gems struct{ Ruby, Minitest, Rake, Tracer string }
	require.NoError(t, json.Unmarshal([]byte(metadata), &gems))
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	root := t.TempDir()
	retainIntegrationReports(t, root, "minitest-nested-review")
	integrationCommand(t, ctx, root, nil, "git", "init", "-q")
	bundle := "source 'https://rubygems.org'\ngem 'minitest', '" + gems.Minitest + "'\ngem 'rake', '" + gems.Rake + "'\n"
	rootManifest := bundle + "gem 'datadog-ci', '" + gems.Tracer + "'\n"
	integrationFile(t, root, "Gemfile", rootManifest)
	integrationFile(t, root, "app/gems.rb", bundle)
	integrationFile(t, root, "lib/customer.rb", "module Customer; def self.add; 1+1; end; end\n")
	integrationFile(t, root, "app/Rakefile", "require 'rake/testtask'\nRake::TestTask.new(:test) { |task| task.pattern='test/**/*_test.rb' }\n")
	tests := `require 'minitest/autorun'
require_relative '../../lib/customer'
class FirstTest < Minitest::Test
 def test_same; assert_equal 'ddtest-config-fixture',Bundler.settings['build.fixture']; assert_equal 2,Customer.add; end
end
class SecondTest < Minitest::Test
 def test_same; assert_equal 'ddtest-config-fixture',Bundler.settings['build.fixture']; assert_equal 2,Customer.add; end
end
`
	integrationFile(t, root, "app/test/customer_test.rb", tests)
	integrationFile(t, root, ".gitignore", "/.testoptimization/\nGemfile.lock\n*.locked\n")
	workflow := `jobs:
  tests:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - {ruby: 'RUBY_VERSION', bundle: app/gems.rb}
          - {ruby: head, bundle: missing/gems.rb}
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.ruby != 'head'
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: 'TRACER_VERSION'}
      - run: bundle exec rake test
        working-directory: app
        env: {BUNDLE_GEMFILE: '${{ github.workspace }}/${{ matrix.bundle }}'}
`
	workflow = strings.ReplaceAll(strings.ReplaceAll(workflow, "RUBY_VERSION", gems.Ruby), "TRACER_VERSION", gems.Tracer)
	integrationFile(t, root, ".github/workflows/test.yml", workflow)
	integrationCommand(t, ctx, root, nil, "bundle", "lock", "--local")
	integrationCommand(t, ctx, filepath.Join(root, "app"), []string{"BUNDLE_GEMFILE=gems.rb"}, "bundle", "lock", "--local")
	// Both modes must retain the effective selected bundle's local settings,
	// including after fallback installs the tracer into its private config.
	bundleConfig := "---\nBUNDLE_BUILD__FIXTURE: ddtest-config-fixture\n"
	integrationFile(t, root, "app/.bundle/config", bundleConfig)
	manifestBefore, err := os.ReadFile(filepath.Join(root, "app/gems.rb"))
	require.NoError(t, err)
	lockBefore, err := os.ReadFile(filepath.Join(root, "app/gems.locked"))
	require.NoError(t, err)
	env := []string{"DD_GIT_REPOSITORY_URL=https://example.com/ddtest-nested-ruby.git", "DD_GIT_COMMIT_SHA=" + strings.Repeat("1", 40), "RUBYOPT=-W0"}
	protocol := rubyProtocolCommand(t, ctx, root, env, binary, "testdrive", "--framework", "minitest", "--all", "--agent", "--yes", "--tracer-version", gems.Tracer)
	var state struct{ Status string }
	require.NoError(t, json.Unmarshal([]byte(protocol), &state))
	require.Equal(t, "complete_locally", state.Status, protocol)
	receipt := rubyProtocolCommand(t, ctx, root, env, binary, "testdrive", "--report", "--framework", "minitest", "--agent")
	require.NoError(t, json.Unmarshal([]byte(receipt), &state))
	require.Equal(t, "complete_locally", state.Status, receipt)
	// The actual ignored gems.locked participates in the final receipt.
	integrationFile(t, root, "app/gems.locked", string(lockBefore)+"\n")
	stale := exec.CommandContext(ctx, binary, "testdrive", "--report", "--framework", "minitest", "--agent")
	stale.Dir, stale.Env = root, append(os.Environ(), env...)
	staleOutput, staleErr := stale.CombinedOutput()
	require.Error(t, staleErr, string(staleOutput))
	require.Contains(t, string(staleOutput), "stale_report")
	integrationFile(t, root, "app/gems.locked", string(lockBefore))
	reportPath := filepath.Join(root, ".testoptimization/testdrive.json")
	data, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var report struct {
		Configurations []struct{ Status string }
		Runs           []struct {
			Name           string
			TestEventCount int                  `json:"test_event_count"`
			TestCounts     struct{ Passed int } `json:"test_counts"`
			Coverage       *struct{ Status string }
		}
		Features []struct{ Status string }
	}
	require.NoError(t, json.Unmarshal(data, &report))
	require.Len(t, report.Configurations, 2)
	require.Len(t, report.Features, 6)
	excluded := 0
	for _, c := range report.Configurations {
		if c.Status == "excluded" {
			excluded++
		}
	}
	require.Equal(t, 1, excluded)
	for _, run := range report.Runs[:2] {
		require.Equal(t, 2, run.TestCounts.Passed)
		if run.Name == "reporting-only" {
			require.Equal(t, 2, run.TestEventCount)
			require.NotNil(t, run.Coverage)
			require.Equal(t, "observed", run.Coverage.Status)
		}
	}
	manifestAfter, err := os.ReadFile(filepath.Join(root, "app/gems.rb"))
	require.NoError(t, err)
	require.Equal(t, manifestBefore, manifestAfter)
	lockAfter, err := os.ReadFile(filepath.Join(root, "app/gems.locked"))
	require.NoError(t, err)
	require.Equal(t, lockBefore, lockAfter)
	configAfter, err := os.ReadFile(filepath.Join(root, "app/.bundle/config"))
	require.NoError(t, err)
	require.Equal(t, bundleConfig, string(configAfter))
	// A changed failing class must be a regression even with the same file/name/message.
	changed := strings.Replace(tests, "assert_equal 2,Customer.add; end", "assert_equal 2,Customer.add; flunk 'same failure' unless defined?(Datadog::CI); end", 1)
	changed = strings.Replace(changed, "assert_equal 2,Customer.add; end", "assert_equal 2,Customer.add; flunk 'same failure' if defined?(Datadog::CI); end", 1)
	integrationFile(t, root, "app/test/customer_test.rb", changed)
	command := exec.CommandContext(ctx, binary, "testdrive", "--framework", "minitest", "--all", "--yes", "--tracer-version", gems.Tracer)
	command.Dir = root
	command.Env = append(os.Environ(), env...)
	output, runErr := command.CombinedOutput()
	require.Error(t, runErr, string(output))
	require.Contains(t, string(output), "suspected regression")
	// A real Gemfile's load-time write during focused check-only stays in its copy.
	integrationFile(t, root, "Gemfile", rootManifest+"File.write('gemfile-generated', Dir.pwd)\n")
	focused := exec.CommandContext(ctx, binary, "testdrive", "--framework", "minitest", "--check-only", "--command", "bundle exec rake test", "--yes", "--tracer-version", gems.Tracer)
	focused.Dir, focused.Env = root, append(os.Environ(), env...)
	focusedOutput, focusedErr := focused.CombinedOutput()
	require.Error(t, focusedErr, string(focusedOutput))
	require.Contains(t, string(focusedOutput), "No CI consumer matches this Ruby command")
	require.NoFileExists(t, filepath.Join(root, "gemfile-generated"))
}
