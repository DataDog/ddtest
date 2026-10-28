package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRubyRepositoryScriptsStayInRequiredScope(t *testing.T) {
	for _, command := range []string{"./ci/run", "sh ci/run.sh", "bash ./script/cibuild", "ruby ci/run.rb", "bundle exec ruby ci/run.rb", "bin/ci", "./bin/ci", `./ci/run "$(nproc)"`, "ruby ci.rb", "bash ci.sh", "bundle exec $RUNNER"} {
		root := rubyWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - run: bundle exec rspec spec/unit\n      - run: "+command+"\n")
		scope, err := DiscoverValidationScope(root, "rspec")
		require.NoError(t, err)
		require.Len(t, scope.Commands, 1, command)
		require.Len(t, scope.Unresolved, 1, command)
		require.Contains(t, scope.Unresolved[0], command)
		ci := RubyCIConfiguration(root, "rspec")
		require.Len(t, ci.Jobs, 2, command)
		require.Equal(t, "inconclusive", ci.Jobs[1].Status)
		require.Contains(t, ci.Jobs[1].Reason, "Repository script")
	}
	// A dynamic setup step before the tests may choose their bundle.
	root := rubyWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - run: echo \"BUNDLE_GEMFILE=gemfiles/$(cat .gemfile)\" >> \"$GITHUB_ENV\"\n      - run: bundle exec rspec\n")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Unresolved, 1)
	require.Contains(t, scope.Unresolved[0], "BUNDLE_GEMFILE")
	root = rubyWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - run: bundle exec rspec\n      - run: echo done\n      - run: bin/rubocop\n")
	scope, err = DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved, "lint binstubs and plain commands are not test wrappers")
}

func TestRubyMatrixInstrumentationSettingsApplyPerRow(t *testing.T) {
	root := rubyWorkflowFixture(t, `jobs:
  tests:
    strategy:
      matrix:
        tracing: ['true', 'false']
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '3.4'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
      - run: bundle exec rspec
        env: {DD_TRACE_ENABLED: '${{ matrix.tracing }}'}
`)
	ci := RubyCIConfiguration(root, "rspec")
	require.Len(t, ci.Jobs, 2)
	reasons := map[string]string{}
	for _, job := range ci.Jobs {
		tracing := "true"
		if strings.Contains(job.Location, `tracing: "false"`) {
			tracing = "false"
		}
		reasons[tracing] = job.Reason
	}
	require.Contains(t, reasons["false"], "disabled by DD_TRACE_ENABLED")
	require.NotContains(t, reasons["true"], "disabled")

	unresolved := rubyBootstrapProblem(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: map[string]string{"DD_TRACE_ENABLED": "${{ vars.TRACING }}"}}, nil, "rspec")
	require.Contains(t, unresolved, "DD_TRACE_ENABLED needs review")
}

func TestRubyActionLanguagesResolveMatrixExpressions(t *testing.T) {
	root := rubyWorkflowFixture(t, `jobs:
  tests:
    strategy:
      matrix:
        tracer-languages: [ruby]
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '3.4'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: '${{ matrix.tracer-languages }}', api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
      - run: bundle exec rspec
`)
	ci := RubyCIConfiguration(root, "rspec")
	require.Len(t, ci.Jobs, 1)
	require.Equal(t, "not checked", ci.Jobs[0].Status, ci.Jobs[0].Reason)
	require.Equal(t, "1.42.0", ci.Jobs[0].TracerRequested)
	require.Equal(t, "3.4", ci.Jobs[0].Runtime)

	skipped := rubyWorkflowFixture(t, `jobs:
  tests:
    strategy:
      matrix:
        ruby: ['3.4', head]
        tracer-languages: [ruby]
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.ruby != 'head'
        with: {languages: '${{ matrix.tracer-languages }}', api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
      - run: bundle exec rspec
`)
	statuses := map[string]string{}
	for _, job := range RubyCIConfiguration(skipped, "rspec").Jobs {
		statuses[job.Runtime] = job.Status
	}
	require.Equal(t, map[string]string{"3.4": "not checked", "head": "excluded"}, statuses)

	// Step Ruby options still break the instrumented row, but the excluded row
	// has no preload to lose.
	withOptions := rubyWorkflowFixture(t, strings.Replace(mustRead(t, skipped), "      - run: bundle exec rspec\n", "      - run: bundle exec rspec\n        env: {RUBYOPT: -W0}\n", 1))
	statuses = map[string]string{}
	for _, job := range RubyCIConfiguration(withOptions, "rspec").Jobs {
		statuses[job.Runtime] = job.Status
	}
	require.Equal(t, map[string]string{"3.4": "inconclusive", "head": "excluded"}, statuses)
}

func mustRead(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	return string(data)
}

// Ruby testdrive validates configurations, so a configured workflow with
// several Ruby commands is pointed at --all.
func TestConfiguredRubyWorkflowRecommendsAll(t *testing.T) {
	root := rubyWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: ruby}\n      - run: bundle exec rspec spec/a\n      - run: bundle exec rspec spec/b\n")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	var output strings.Builder
	require.NoError(t, onboardCI(&output, gitHubActions{}, root, "ruby", "rspec", nil))
	require.Contains(t, output.String(), "Run `ddtest testdrive --framework rspec --all` to check the setup locally.")
}

// rack's documentation and external-project workflows next to its Minitest CI.
func TestRubyMaintenanceAndUninstrumentedJobsDoNotBlockMinitest(t *testing.T) {
	root := rubyWorkflowFixture(t, `jobs:
  test:
    strategy:
      matrix:
        ruby: ['3.3', '3.4']
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
      - run: bundle exec rake test
`)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/documentation.yaml"), []byte(`jobs:
  generate:
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: ruby, bundler-cache: true}
      - run: |
          bundle exec rdoc --op docs/main --main README.md
          cp -r contrib docs/main
      - run: |
          gem install rack --version "< 3.3"
          gem unpack rack -v "< 3.3"
          cd rack-3.2*
          cp ../SECURITY.md .
          bundle exec rdoc --op ../docs/3.2 --main README.md
      - run: bundle exec rubocop && gem build rack.gemspec
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test-external.yaml"), []byte(`jobs:
  test:
    strategy:
      matrix:
        ruby: ['3.3', '3.4']
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - run: chmod -R o-w /opt/hostedtoolcache/Ruby
      - run: bundle exec bake test:external
`), 0644))
	scope, err := DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved, "documentation, release and uninstrumented non-consumer steps are not blockers")
	ci := RubyCIConfiguration(root, "minitest")
	require.Len(t, ci.Jobs, 2, "only the Minitest consumers carry Ruby versions")
	codes, maintenance := 0, 0
	for _, finding := range ci.Review {
		if finding.Code == CodeUninstrumentedJob {
			codes++
			require.Contains(t, finding.Reason, "Uninstrumented CI job with no recognized minitest step")
		}
		if strings.Contains(finding.Reason, "Documentation, lint or release tooling is maintenance") {
			maintenance++
		}
	}
	require.Equal(t, 2, codes, "each matrix entry of the external job is reported")
	require.Equal(t, 3, maintenance, "every documentation and release step is maintenance")

	// The same command blocks when its job is instrumented or runs the framework,
	// and a repository script or Rake task always stays in scope.
	for _, workflow := range []string{
		"jobs:\n  t:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: ruby}\n      - run: bundle exec bake test:external\n",
		"jobs:\n  t:\n    steps:\n      - run: bundle exec rake test\n      - run: bundle exec bake test:external\n",
		"jobs:\n  t:\n    steps:\n      - run: ./ci/run-tests\n",
		"jobs:\n  t:\n    strategy:\n      matrix:\n        suite: [unit]\n    steps:\n      - run: bundle exec ./bin/test-${{ matrix.suite }}\n",
	} {
		root := rubyWorkflowFixture(t, workflow)
		scope, err := DiscoverValidationScope(root, "minitest")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved, workflow)
	}
	// A focused local command has no job context and stays unresolved.
	require.NotEmpty(t, resolveRubyStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "bundle exec bake test"}, "minitest").Reason)
	for run, maintenance := range map[string]bool{
		"bundle install":                                           false,
		"gem install bundler && bundle install":                    false,
		"bundle exec rdoc && bundle exec rake test":                false,
		"yard doc $(git ls-files)":                                 false,
		"bundle exec yard doc":                                     true,
		"bundle exec rubocop & bundle exec rspec spec/integration": false,
		"bundle exec rubocop || bundle exec rspec":                 false,
		"bundle exec rubocop | tee log":                            false,
		"(bundle exec rubocop; bundle exec rspec)":                 false,
		"{ bundle exec rdoc; }":                                    false,
		"bundle exec rdoc > rdoc.log":                              false,
		"bundle exec rdoc \"$(bundle exec rspec)\"":                false,
		"gem install rack --version \"< 3.3\" && bundle exec rdoc": true,
		"gem build rack.gemspec":                                   true,
	} {
		require.Equal(t, maintenance, rubyMaintenanceStep(run), run)
	}
}

func TestRubyBackgroundedSuiteStaysInScope(t *testing.T) {
	root := rubyWorkflowFixture(t, "jobs:\n  t:\n    steps:\n      - run: bundle exec rubocop & bundle exec rspec spec/integration\n")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved, "a backgrounded RSpec suite cannot disappear as maintenance")
	require.NotContains(t, strings.Join(scope.Review, "\n"), "maintenance")
}
