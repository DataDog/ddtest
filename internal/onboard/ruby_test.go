package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/environment"
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
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.40.0'}
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
		require.NotEmpty(t, job.Runtime)
	}
	_, err = RubyValidationCommand(root, "rspec", "")
	require.ErrorContains(t, err, "multiple")
}

func TestRubyScopeDoesNotExecuteUnresolvedSetupOrDefaultRake(t *testing.T) {
	for _, command := range []string{"bundle exec rake", "bundle exec rake custom", "bundle exec rspec && echo setup", "bundle exec rspec $(find spec -name '*.rb')", "bundle exec ${{ matrix.command }}"} {
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
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.40.0'}
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
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
      - run: bundle exec rake test
`)
	scope, err := DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 4, "instrumented and excluded consumers retain separate configurations")
	for i, gemfile := range []string{"gemfiles/rails_7.1.gemfile", "gemfiles/rails_8.0.gemfile"} {
		for j := range 2 {
			entry := scope.Commands[i*2+j]
			require.Equal(t, gemfile, entry.Environment["BUNDLE_GEMFILE"])
			require.Empty(t, entry.UnvalidatedSetup)
			require.Len(t, entry.Locations, 1)
			require.Equal(t, j == 1, entry.CIInstrumentationExcluded)
		}
	}
	ci := RubyCIConfiguration(root, "minitest")
	require.Len(t, ci.Jobs, 4)
	for _, job := range ci.Jobs {
		if job.Runtime == "head" {
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
		require.Equal(t, blocked, rubyBootstrapProblem(ciWorkflow{}, runtimeJob{}, step, nil) != "", value)
	}
	resolved, env, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "bundle exec rspec", Env: map[string]string{"RUBYOPT": "${{ env.RUBYOPT }} -W0"}}, nil, "rspec")
	require.NoError(t, err)
	require.Equal(t, "bundle exec rspec", resolved.Run)
	require.Equal(t, "-W0", env["RUBYOPT"])
	// Inherited workflow or job options are not dropped silently.
	for _, job := range []runtimeJob{{Env: map[string]string{"RUBYOPT": "-r./ci/boot.rb"}}} {
		_, _, err = resolveVariantStep(ciWorkflow{}, job, runtimeStep{Run: "bundle exec rspec", Env: map[string]string{"RUBYOPT": "${{ env.RUBYOPT }} -W0"}}, nil, "rspec")
		require.ErrorContains(t, err, "workflow or job Ruby options")
	}
	_, _, err = resolveVariantStep(ciWorkflow{Env: map[string]string{"RUBYOPT": "-r./ci/boot.rb"}}, runtimeJob{}, runtimeStep{Run: "bundle exec rspec", Env: map[string]string{"RUBYOPT": "${{ env.RUBYOPT }} -W0"}}, nil, "rspec")
	require.ErrorContains(t, err, "workflow or job Ruby options")
	require.Empty(t, rubyBootstrapProblem(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: map[string]string{"DD_TRACE_MINITEST_ENABLED": "false"}}, nil, "rspec"))
	require.NotEmpty(t, rubyBootstrapProblem(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: map[string]string{"DD_TRACE_RSPEC_ENABLED": "false"}}, nil, "rspec"))
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
	for alias, targets := range map[string][]string{
		"task default: [:test, :lint]":                              {"test", "lint"},
		"task :default => %w[spec features]":                        {"spec", "features"},
		`task default: ["spec", 'features'] # both`:                 {"spec", "features"},
		"task default: :custom":                                     {"custom"},
		"task default: :test\ntask default: :lint":                  nil,
		"task default: :test\nRake::Task[:default].enhance [:lint]": nil,
		"task default: ENV['CI'] ? :ci : :test":                     nil,
	} {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "Rakefile"), []byte(alias), 0600))
		require.Equal(t, targets, rubyDefaultTargets(root, "."), alias)
	}
}

// httparty's CI runs bundle exec rake, whose default task is [:spec, :features].
func TestRubyRakeTargetsValidateTheFrameworkTaskAndReportTheRest(t *testing.T) {
	root := rubyWorkflowFixture(t, `jobs:
  build:
    strategy:
      matrix:
        ruby: ['3.3', '3.4']
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}', bundler-cache: true}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
      - run: bundle exec rake
`)
	require.NoError(t, os.WriteFile(filepath.Join(root, "Rakefile"), []byte("require 'rspec/core/rake_task'\nRSpec::Core::RakeTask.new(:spec)\nrequire 'cucumber/rake/task'\nCucumber::Rake::Task.new(:features)\n\ntask default: [:spec, :features]\n"), 0600))
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	entry := scope.Commands[0]
	require.Equal(t, "bundle exec rake spec", entry.Command, "only the framework's own task is validated")
	require.Len(t, entry.Locations, 2, "bound to the original CI step in every matrix entry")
	require.Contains(t, entry.Locations[0], "(bundle exec rake)")
	require.Contains(t, strings.Join(entry.StepScopeNotes, "\n"), "features")
	require.Contains(t, strings.Join(scope.Review, "\n"), "also runs Rake task(s) features")
	ci := RubyCIConfiguration(root, "rspec")
	require.Len(t, ci.Jobs, 2)
	require.Equal(t, "not checked", ci.Jobs[0].Status, ci.Jobs[0].Reason)
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	var guidance strings.Builder
	require.NoError(t, onboardGitHub(&guidance, &environment.GitHub{}, root, "ruby", "rspec", &agentHandoff{}))
	require.Contains(t, guidance.String(), "Shared CI step: This CI step also runs Rake task(s) features", "instructions name the other framework's tasks")

	for command, expected := range map[string]string{
		"bundle exec rake spec features": "bundle exec rake spec",
		"rake spec:unit lint":            "rake spec:unit",
		"bundle exec rake spec test":     "bundle exec rake spec",
	} {
		resolution := resolveRubyStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "rspec")
		require.True(t, resolution.Matched, command)
		require.Equal(t, expected, resolution.Tests[0].Command, command)
		require.NotEmpty(t, resolution.Tests[0].StepScopeNotes, command)
	}
	minitest := resolveRubyStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "bundle exec rake spec test"}, "minitest")
	require.Equal(t, "bundle exec rake test", minitest.Tests[0].Command)
	require.Contains(t, strings.Join(minitest.Tests[0].StepScopeNotes, "\n"), "other Ruby framework's spec task")
	for _, command := range []string{"bundle exec rake spec features --trace", "bundle exec rake spec SEED=1 features", "bundle exec rake features"} {
		resolution := resolveRubyStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "rspec")
		require.False(t, resolution.Matched, command)
		require.NotEmpty(t, resolution.Reason, command)
	}
	require.Equal(t, "bundle exec rake spec --trace", resolveRubyStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "bundle exec rake spec --trace"}, "rspec").Tests[0].Command, "a single-framework invocation keeps its options")
}

func TestI18nOriginalRakeAndNumericMatrixNeedOnlyInstrumentation(t *testing.T) {
	data, err := os.ReadFile("testdata/i18n-ruby.yml")
	require.NoError(t, err)
	workflow := strings.Replace(string(data), "      - name: Build and test with Rake", `      - uses: datadog/test-visibility-github-action@v3
        if: matrix.ruby_version != 'head' && !contains(matrix.ruby_version, 'jruby')
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
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
	instrumented, consumers, excludedConsumers := 0, 0, 0
	for _, entry := range scope.Commands {
		require.Equal(t, "bundle exec rake", entry.Command)
		consumers += len(entry.Locations)
		if entry.CIInstrumentationExcluded {
			excludedConsumers += len(entry.Locations)
		} else {
			instrumented++
		}
	}
	require.Equal(t, 6, instrumented)
	require.Equal(t, 31, consumers, "all original runtime consumers remain in the inventory")
	require.Equal(t, 9, excludedConsumers, "explicit opt-outs remain separate from instrumented configurations")
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

func TestMinitestInstructionsKeepIsolationAndConfigureBothEnvironments(t *testing.T) {
	text := instructions("ruby", "minitest")
	for _, expected := range []string{"ActiveSupport::Testing::Isolation", "Datadog::Tracing::SyncWriter", "${{ env.RUBYOPT }} -r./ci/datadog_test_optimization.rb", "Keep isolation", "3.4.22", "--check-only --tracer-version"} {
		require.Contains(t, text, expected)
	}
	require.NotContains(t, text, "__FORK_SETUP__")
	require.Contains(t, text, "matrix entries where the Datadog action is skipped; there it does nothing", "excluded rows load the initializer harmlessly")
	require.NotContains(t, instructions("ruby", "rspec"), "SyncWriter")
}

// rack runs both '2.7' and '2.7.3'; an equality pin misses the patch entry.
func TestRubyGemsResolverPinMustCoverEveryRuby27Entry(t *testing.T) {
	workflow := func(pin string) string {
		return `jobs:
  tests:
    strategy:
      matrix:
        ruby: ['2.7', '2.7.3', '3.3']
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'` + pin + `}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
      - run: bundle exec rspec
`
	}
	statuses := func(pin string) map[string]string {
		result := map[string]string{}
		for _, job := range RubyCIConfiguration(rubyWorkflowFixture(t, workflow(pin)), "rspec").Jobs {
			result[job.Runtime] = job.Status
		}
		return result
	}
	require.Equal(t, map[string]string{"2.7": "inconclusive", "2.7.3": "inconclusive", "3.3": "not checked"}, statuses(""))
	require.Equal(t, map[string]string{"2.7": "not checked", "2.7.3": "inconclusive", "3.3": "not checked"}, statuses(`, rubygems: "${{ matrix.ruby == '2.7' && '3.4.22' || 'default' }}"`))
	require.Equal(t, map[string]string{"2.7": "not checked", "2.7.3": "not checked", "3.3": "not checked"}, statuses(`, rubygems: "${{ startsWith(matrix.ruby, '2.7') && '3.4.22' || 'default' }}"`))
	require.Equal(t, map[string]string{"2.7": "not checked", "2.7.3": "not checked", "3.3": "not checked"}, statuses(`, rubygems: '3.3.27'`))
	require.Contains(t, RubyCIConfiguration(rubyWorkflowFixture(t, workflow("")), "rspec").Jobs[1].Reason, "startsWith(matrix.ruby, '2.7')")
	for _, framework := range []string{"rspec", "minitest"} {
		require.Contains(t, rubyDiscovery{}.agentInstructions(framework, ""), "startsWith(matrix.ruby, '2.7')")
		require.Contains(t, instructions("ruby", framework), "startsWith(matrix.ruby, '2.7')")
	}
}

func TestRubyMergedConfigurationsKeepEveryStepScopeNote(t *testing.T) {
	root := rubyWorkflowFixture(t, "jobs:\n  unit:\n    steps:\n      - run: bundle exec rake spec\n  all:\n    steps:\n      - run: bundle exec rake spec features\n  lint:\n    steps:\n      - run: bundle exec rake spec rubocop\n")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1, "all three steps validate bundle exec rake spec")
	require.Len(t, scope.Commands[0].Locations, 3)
	notes := strings.Join(scope.Commands[0].StepScopeNotes, "\n")
	require.Contains(t, notes, "Rake task(s) features")
	require.Contains(t, notes, "Rake task(s) rubocop")
}
