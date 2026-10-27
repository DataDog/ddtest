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

func TestRubyReceiptReviewIntegration(t *testing.T) {
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
	retainIntegrationReports(t, root, "minitest-receipts")
	integrationCommand(t, ctx, root, nil, "git", "init", "-q")
	manifest := "source 'https://rubygems.org'\ngem 'minitest', '" + gems.Minitest + "'\ngem 'rake', '" + gems.Rake + "'\ngem 'datadog-ci', '" + gems.Tracer + "'\n"
	integrationFile(t, root, "Gemfile", "raise 'the unselected root Gemfile must not load'\n")
	integrationFile(t, root, "bundles/integration.rb", manifest)
	integrationFile(t, root, ".gitignore", "/.testoptimization/\n*.lock\n*.locked\n.bundle/\ncustom-config/\n")
	integrationFile(t, root, "bundles/custom-config/config", "---\nBUNDLE_BUILD__FIXTURE: retained\n")
	integrationFile(t, root, "lib/customer.rb", "module Customer; def self.add; 1+1; end; end\n")
	// A gemspec makes the coordinated path run its packaging check in the
	// check copy that dependency readiness created.
	integrationFile(t, root, "customer.gemspec", "Gem::Specification.new do |s|\n s.name='customer';s.version='1.0';s.summary='fixture';s.authors=['ddtest'];s.files=Dir['lib/**/*']\nend\n")
	integrationFile(t, root, "test/one_test.rb", "require 'minitest/autorun'\nrequire_relative '../lib/customer'\nclass CustomerTest < Minitest::Test\n def test_one; assert_equal 'retained',Bundler.settings['build.fixture']; assert_equal 2,Customer.add; end\nend\n")
	integrationFile(t, root, "Rakefile", "require 'rake/testtask'\nRake::TestTask.new(:test) { |task| task.pattern='test/**/*_test.rb' }\n")
	workflow := "jobs:\n  tests:\n    steps:\n      - uses: ruby/setup-ruby@v1\n        with: {ruby-version: '" + gems.Ruby + "'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: ruby, ruby-tracer-version: '" + gems.Tracer + "'}\n      - run: bundle exec rake test\n        env: {BUNDLE_GEMFILE: bundles/integration.rb, BUNDLE_APP_CONFIG: custom-config}\n"
	integrationFile(t, root, ".github/workflows/test.yml", workflow)
	integrationCommand(t, ctx, root, []string{"BUNDLE_GEMFILE=bundles/integration.rb"}, "bundle", "lock", "--local")
	// Force observer paths to contain both shell-sensitive characters, while
	// retaining a short parent for Ruby's Unix sockets.
	temporary, err := os.MkdirTemp("/tmp", "ddt-r3-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(temporary)) })
	quoted := filepath.Join(temporary, "space's")
	require.NoError(t, os.Mkdir(quoted, 0700))
	env := []string{"TMPDIR=" + quoted, "DD_GIT_REPOSITORY_URL=https://example.com/ddtest-ruby-receipts.git", "DD_GIT_COMMIT_SHA=" + strings.Repeat("1", 40)}
	for _, tc := range []struct {
		scope  []string
		status string
	}{
		{[]string{"--command", "bundle exec rake test"}, "selected_passed"},
		{[]string{"--all"}, "complete_locally"},
	} {
		args := append([]string{"testdrive", "--framework", "minitest", "--agent", "--yes", "--tracer-version", gems.Tracer}, tc.scope...)
		result := rubyProtocolCommand(t, ctx, root, env, binary, args...)
		require.Contains(t, result, `"status":"`+tc.status+`"`, result)
	}
	receiptArgs := []string{"testdrive", "--report", "--framework", "minitest", "--agent"}
	require.Contains(t, rubyProtocolCommand(t, ctx, root, env, binary, receiptArgs...), `"status":"complete_locally"`)
	for _, path := range []string{"bundles/integration.rb.lock", "bundles/custom-config/config"} {
		original, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)
		integrationFile(t, root, path, string(original)+"\n")
		command := exec.CommandContext(ctx, binary, receiptArgs...)
		command.Dir, command.Env = root, append(os.Environ(), env...)
		data, err := command.CombinedOutput()
		require.Error(t, err, string(data))
		require.Contains(t, string(data), "stale_report")
		integrationFile(t, root, path, string(original))
	}
	// Implicit lookup must prefer gems.rb over the invalid Gemfile, including
	// when the CI process starts two directories below that manifest.
	integrationFile(t, root, "gems.rb", manifest)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app/jobs"), 0700))
	integrationCommand(t, ctx, root, nil, "bundle", "lock", "--local")
	workflow = strings.Replace(workflow, "        env: {BUNDLE_GEMFILE: bundles/integration.rb, BUNDLE_APP_CONFIG: custom-config}", "        working-directory: app/jobs", 1)
	integrationFile(t, root, ".github/workflows/test.yml", workflow)
	result := rubyProtocolCommand(t, ctx, root, env, binary, "testdrive", "--framework", "minitest", "--all", "--check-only", "--agent", "--yes", "--tracer-version", gems.Tracer)
	require.Contains(t, result, `"status":"ready"`, result)
}
