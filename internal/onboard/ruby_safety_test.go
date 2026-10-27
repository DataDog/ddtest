package onboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRubySafetyKeepsOpaqueWrappersRequired(t *testing.T) {
	for _, wrapper := range []string{"./ci/run", "bundle exec ./ci/run", "sh ./ci/run", "ruby ci/run.rb"} {
		root := rubyWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - run: bundle install\n      - run: gem install bundler\n      - run: ruby --version\n      - run: bundle exec rubocop\n      - run: bundle exec rspec\n      - run: "+wrapper+"\n")
		scope, err := DiscoverValidationScope(root, "rspec")
		require.NoError(t, err)
		require.Len(t, scope.Commands, 1)
		require.Len(t, scope.Unresolved, 1)
		require.Contains(t, scope.Unresolved[0], wrapper)
		ci := RubyCIConfiguration(root, "rspec")
		require.Len(t, ci.Jobs, 2)
		require.Equal(t, wrapper, ci.Jobs[1].Command)
		require.Equal(t, "inconclusive", ci.Jobs[1].Status)
	}
}

func TestRubySafetyResolvesMatrixInstrumentationFlags(t *testing.T) {
	for _, key := range []string{"DD_TRACE_ENABLED", "DD_CIVISIBILITY_ENABLED", "DD_TRACE_RSPEC_ENABLED"} {
		root := rubyWorkflowFixture(t, `env:
  RUBYOPT: '${{ env.RUBYOPT }} -rcustomer'
jobs:
  tests:
    strategy:
      matrix:
        tracing: [true, false]
    env:
      FLAG: '${{ matrix.tracing }}'
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '3.3'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: ruby, ruby-tracer-version: '1.42.0'}
      - run: bundle exec rspec
        env: {`+key+`: '${{ matrix.tracing }}'}
`)
		ci := RubyCIConfiguration(root, "rspec")
		require.Len(t, ci.Jobs, 2)
		require.Equal(t, "not checked", ci.Jobs[0].Status)
		require.Equal(t, "inconclusive", ci.Jobs[1].Status)
		require.Contains(t, ci.Jobs[1].Reason, key)
	}
}

func TestRubySafetyResolvesActionLanguagesBeforeConditions(t *testing.T) {
	root := rubyWorkflowFixture(t, `jobs:
  tests:
    strategy:
      matrix:
        include:
          - {ruby: '3.3', language: ruby, tracing: true}
          - {ruby: head, language: ruby, tracing: false}
          - {ruby: '3.3', language: all, tracing: true}
          - {ruby: head, language: all, tracing: false}
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.ruby != 'head'
        with: {languages: '${{ matrix.language }}', ruby-tracer-version: '1.42.0'}
      - run: bundle exec rspec
        env: {DD_TRACE_ENABLED: '${{ matrix.tracing }}'}
`)
	ci := RubyCIConfiguration(root, "rspec")
	require.Len(t, ci.Jobs, 4)
	for _, job := range ci.Jobs {
		if job.Ruby == "head" {
			require.Equal(t, "excluded", job.Status)
		} else {
			require.Equal(t, "not checked", job.Status)
			require.Equal(t, "1.42.0", job.TracerRequested)
		}
	}
	unknown := "${{ needs.setup.outputs.tracing }}"
	require.NotEmpty(t, rubyBootstrapProblem(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: map[string]string{"DD_TRACE_ENABLED": unknown}}, "rspec"))
}
