package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func rubyWorkflowFixture(t *testing.T, workflow string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile"), []byte("gem 'rspec'\ngem 'minitest'\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(workflow), 0644))
	return root
}

func TestRubyScopeKeepsFrameworkCommandsAndMatrixEnvironment(t *testing.T) {
	root := rubyWorkflowFixture(t, `jobs:
  tests:
    strategy:
      matrix:
        ruby: ['3.2.2', '3.3.0']
        suite: ['unit', 'integration']
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: ruby, ruby-tracer-version: '1.40.0'}
      - run: bundle exec rspec spec/${{ matrix.suite }} --format documentation
        env: {APP_ENV: test, RUBYOPT: '-W0 -rdatadog/ci/auto_instrument'}
      - run: bundle exec rake test
      - run: bundle exec rake rubocop
`)
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2, "Ruby versions share local commands, but suite selections remain distinct")
	require.Equal(t, "bundle exec rspec spec/unit --format documentation", scope.Commands[0].Command)
	require.Equal(t, "-W0 -rdatadog/ci/auto_instrument", scope.Commands[0].Environment["RUBYOPT"])
	require.Len(t, scope.Commands[0].Locations, 2)
	require.Contains(t, strings.Join(scope.Review, "\n"), "other Ruby framework")
	ci := RubyCIConfiguration(root, "rspec")
	require.Len(t, ci.Jobs, 4)
	for _, job := range ci.Jobs {
		require.Equal(t, "not checked", job.Status)
		require.Equal(t, "1.40.0", job.TracerRequested)
		require.NotEmpty(t, job.Ruby)
	}
	_, err = RubyValidationCommand(root, "rspec", "")
	require.ErrorContains(t, err, "multiple")
}

func TestRubyScopeDoesNotExecuteUnresolvedSetupOrDefaultRake(t *testing.T) {
	for _, command := range []string{"bundle exec rake", "bundle exec rake custom", "bundle exec rspec && echo setup", "bundle exec rspec $(find spec -name '*.rb')"} {
		resolution := resolveRubyStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "rspec")
		require.NotEmpty(t, resolution.Reason, command)
		require.Empty(t, resolution.Tests, command)
	}
	root := rubyWorkflowFixture(t, "jobs:\n  test:\n    steps:\n      - run: bundle exec rake test\n")
	scope, err := DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	selected, err := RubyValidationCommand(root, "minitest", "")
	require.NoError(t, err)
	require.Equal(t, "bundle exec rake test", selected)
	require.Equal(t, scope.Commands[0].Command, ValidationCommands(root, selected, "minitest")[0].Command)
}

func TestRubyCIRequiresInstallationBeforeTestsAndUnconditionalConditions(t *testing.T) {
	root := rubyWorkflowFixture(t, `jobs:
  tests:
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '3.2.2'}
      - run: bundle exec rspec
      - uses: datadog/test-visibility-github-action@v3
        if: false
        with: {languages: ruby, ruby-tracer-version: '1.40.0'}
      - run: bundle exec rspec spec/unit
`)
	check := RubyCIConfiguration(root, "rspec")
	require.Len(t, check.Jobs, 2)
	require.Equal(t, "inconclusive", check.Jobs[0].Status, "a later install cannot instrument an earlier test")
	require.Equal(t, "excluded", check.Jobs[1].Status, "explicitly skipped instrumentation leaves the test running")
	require.Empty(t, check.Jobs[1].Action)
}

func TestRubyMatrixGemfilesResolveWithoutRunnerPathsOrShellEvaluation(t *testing.T) {
	root := rubyWorkflowFixture(t, `env:
  BUNDLE_GEMFILE: ${{ github.workspace }}/gemfiles/rails_${{ matrix.rails }}.gemfile
jobs:
  tests:
    strategy:
      matrix:
        ruby: ['3.4', 'head']
        rails: ['7.1', '8.0']
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.ruby != 'head'
        with: {languages: ruby, ruby-tracer-version: '1.42.0'}
      - run: bundle exec rake test
`)
	scope, err := DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	for i, gemfile := range []string{"gemfiles/rails_7.1.gemfile", "gemfiles/rails_8.0.gemfile"} {
		require.Equal(t, gemfile, scope.Commands[i].Environment["BUNDLE_GEMFILE"])
		require.Empty(t, scope.Commands[i].UnvalidatedSetup)
		require.Len(t, scope.Commands[i].Locations, 2, "both original runtime consumers remain in the inventory")
	}
	ci := RubyCIConfiguration(root, "minitest")
	require.Len(t, ci.Jobs, 4)
	for _, job := range ci.Jobs {
		if job.Ruby == "head" {
			require.Equal(t, "excluded", job.Status)
		} else {
			require.Equal(t, "not checked", job.Status)
		}
	}
	for _, value := range []string{"${{ secrets.GEMFILE }}", "${{ github.workspace }}/../escape", "${{ github.workspace }}/gemfiles/$(touch bad)", "/runner/Gemfile", "${{ runner.temp }}/Gemfile"} {
		_, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "bundle exec rake test", Env: map[string]string{"BUNDLE_GEMFILE": value}}, nil, "minitest")
		require.Error(t, err, value)
	}
}

func TestRubyBootstrapOverridesRemainVisible(t *testing.T) {
	for value, blocked := range map[string]bool{"-W0": true, "": true, "-W0 -rbundler/setup -rdatadog/ci/auto_instrument": false, "${{ env.RUBYOPT }} -W0": false} {
		step := runtimeStep{Env: map[string]string{"RUBYOPT": value}}
		require.Equal(t, blocked, rubyBootstrapProblem(ciWorkflow{}, runtimeJob{}, step) != "", value)
	}
	resolved, env, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "bundle exec rspec", Env: map[string]string{"RUBYOPT": "${{ env.RUBYOPT }} -W0"}}, nil, "rspec")
	require.NoError(t, err)
	require.Equal(t, "bundle exec rspec", resolved.Run)
	require.Equal(t, "-W0", env["RUBYOPT"])
	require.Empty(t, rubyBootstrapProblem(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: map[string]string{"DD_TRACE_MINITEST_ENABLED": "false"}}, "rspec"))
	require.NotEmpty(t, rubyBootstrapProblem(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: map[string]string{"DD_TRACE_RSPEC_ENABLED": "false"}}, "rspec"))
}

func TestRubyScopeRetainsDirectMinitestCommandsAndUnknownWrappers(t *testing.T) {
	command := "bundle exec ruby -Itest test/one_test.rb"
	resolution := resolveRubyStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "minitest")
	require.True(t, resolution.Matched)
	require.Equal(t, command, resolution.Tests[0].Command)
	resolution = resolveRubyStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "bin/custom-test-wrapper"}, "minitest")
	require.NotEmpty(t, resolution.Reason)
	require.Empty(t, resolution.Tests)
}

func TestRubyInstructionsDescribeNativeValidationAndPreserveBothReports(t *testing.T) {
	for _, framework := range []string{"rspec", "minitest"} {
		text := instructions("ruby", framework)
		for _, expected := range []string{"--framework " + framework, "ruby-tracer-version", "native baseline/reporting-only", "Gemfile", "disposable", "report.html", "testdrive.json", "historical", "actual CI execution"} {
			require.Contains(t, text, expected)
		}
		require.NotContains(t, text, "NODE_OPTIONS")
		require.NotContains(t, text, "__FRAMEWORK__")
	}
}

func TestRubyDefaultAliasKeepsOriginalCICommand(t *testing.T) {
	for _, alias := range []string{"task :default => [:test]", "task default: :test", "task default: [:test] # original entry point"} {
		root := rubyWorkflowFixture(t, "jobs:\n  test:\n    steps:\n      - run: bundle exec rake\n")
		require.NoError(t, os.WriteFile(filepath.Join(root, "Rakefile"), []byte("require 'rake/testtask'\n"+alias+"\nRake::TestTask.new(:test)\n"), 0600))
		scope, err := DiscoverValidationScope(root, "minitest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		require.Equal(t, "bundle exec rake", scope.Commands[0].Command)
	}
	for _, alias := range []string{"task default: [:test, :lint]", "task default: :custom", "task default: :test\ntask default: :lint", "task default: :test\nRake::Task[:default].enhance [:lint]"} {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "Rakefile"), []byte(alias), 0600))
		require.False(t, rubyDefaultTask(root, ".", "test"), alias)
	}
}

func TestI18nOriginalRakeAndNumericMatrixNeedOnlyInstrumentation(t *testing.T) {
	data, err := os.ReadFile("testdata/i18n-ruby.yml")
	require.NoError(t, err)
	workflow := strings.Replace(string(data), "      - name: Build and test with Rake", `      - uses: datadog/test-visibility-github-action@v3
        if: matrix.ruby_version != 'head' && !contains(matrix.ruby_version, 'jruby')
        with: {languages: ruby, ruby-tracer-version: '1.42.0'}
      - name: Build and test with Rake`, 1)
	root := rubyWorkflowFixture(t, workflow)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gemfiles"), 0700))
	for _, name := range []string{"Gemfile.rails-7.0.x", "Gemfile.rails-7.1.x", "Gemfile.rails-7.2.x", "Gemfile.rails-8.0.x", "Gemfile.rails-main"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, "gemfiles", name), []byte("source 'https://rubygems.org'\n"), 0600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "Rakefile"), []byte("require 'rake/testtask'\ntask :default => [:test]\nRake::TestTask.new(:test) do |t|\n t.pattern = 'test/**/*_test.rb'\nend\n"), 0600))
	scope, err := DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 6)
	for _, entry := range scope.Commands {
		require.Equal(t, "bundle exec rake", entry.Command)
	}
	ci := RubyCIConfiguration(root, "minitest")
	require.Len(t, ci.Jobs, 31)
	excluded := 0
	for _, job := range ci.Jobs {
		if job.Status == "excluded" {
			excluded++
		} else {
			require.Equal(t, "not checked", job.Status, job.Reason)
		}
	}
	require.Equal(t, 9, excluded)
	after, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, workflow, string(after))
}
