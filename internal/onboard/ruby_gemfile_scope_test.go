package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRubyMixedRakeConsumersStayUnresolved(t *testing.T) {
	for _, framework := range []string{"rspec", "minitest"} {
		for _, command := range []string{"bundle exec rake spec test", "bundle exec rake test spec", "bundle exec rake spec:unit test:unit"} {
			root := rubyWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - run: "+command+"\n")
			scope, err := DiscoverValidationScope(root, framework)
			require.NoError(t, err)
			require.NotEmpty(t, scope.Unresolved, framework+": "+command)
			ci := RubyCIConfiguration(root, framework)
			require.NotEmpty(t, ci.Jobs, "the mixed CI consumer must remain required")
			require.Equal(t, "inconclusive", ci.Jobs[0].Status)
		}
	}
}

func TestRubyExcludedGemfileConfigurationsStayInScope(t *testing.T) {
	root := rubyWorkflowFixture(t, `jobs:
  tests:
    strategy:
      matrix:
        ruby: ['3.3', 'head']
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.ruby != 'head'
        with: {languages: ruby, ruby-tracer-version: '1.42.0'}
      - run: bundle exec rake test
        env: {BUNDLE_GEMFILE: 'gemfiles/${{ matrix.ruby }}.gemfile'}
`)
	scope, err := DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	for _, command := range scope.Commands {
		require.Equal(t, command.Environment["BUNDLE_GEMFILE"] == "gemfiles/head.gemfile", command.CIInstrumentationExcluded)
	}
}

func TestRubyWorkspaceGemfilesResolveFromWorkingDirectory(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"${{ github.workspace }}/app/Gemfile", "Gemfile"},
		{"${{github.workspace}}/Gemfile", "../Gemfile"},
		{"gemfiles/suite.gemfile", "gemfiles/suite.gemfile"},
	} {
		root := rubyWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - run: bundle exec rake test\n        working-directory: app\n        env: {BUNDLE_GEMFILE: '"+tc.input+"'}\n")
		scope, err := DiscoverValidationScope(root, "minitest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved, tc.input)
		require.Len(t, scope.Commands, 1)
		require.Equal(t, "app", scope.Commands[0].Directory)
		require.Equal(t, tc.want, scope.Commands[0].Environment["BUNDLE_GEMFILE"])
	}
}
