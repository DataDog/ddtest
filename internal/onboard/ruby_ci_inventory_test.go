package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/environment"
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
        with: {languages: ruby, ruby-tracer-version: '1.42.0'}
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
        with: {languages: '${{ matrix.tracer-languages }}', ruby-tracer-version: '1.42.0'}
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
        with: {languages: '${{ matrix.tracer-languages }}', ruby-tracer-version: '1.42.0'}
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

// Ruby testdrive cannot validate configurations yet, so a configured workflow
// with several Ruby commands is not pointed at --all.
func TestConfiguredRubyWorkflowDoesNotRecommendAll(t *testing.T) {
	root := rubyWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: ruby}\n      - run: bundle exec rspec spec/a\n      - run: bundle exec rspec spec/b\n")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	var output strings.Builder
	require.NoError(t, onboardGitHub(&output, &environment.GitHub{}, root, "ruby", "rspec"))
	require.Contains(t, output.String(), "Run `ddtest testdrive --framework rspec` to check the setup locally.")
}
