package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCypressDiscoveryPreservesServerAliases(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"start": "node scripts/start.js", "cy:run": "cypress run", "e2e": "cypress run --browser chrome", "local:run": "start-test 8080 cy:run", "test:ci": "run-p --race start e2e"})
	_, err := CypressValidationCommand(root, "")
	require.ErrorContains(t, err, "multiple Cypress package scripts")
	command, err := CypressValidationCommand(root, "npm run local:run")
	require.NoError(t, err)
	require.Equal(t, "npm run local:run", command)
	for _, command := range []string{"npm run local:run", "npm run test:ci", "npx --no-install cypress run --browser chrome", "node -C development node_modules/cypress/bin/cypress run", "node --conditions=development --require ./setup.cjs node_modules/.bin/cypress run"} {
		remaining := 256
		result := resolveJestSequence(root, root, command, nil, &remaining, "cypress")
		require.Empty(t, result.Reason)
		require.Len(t, result.Tests, 1)
		if command[:3] == "npm" {
			require.Equal(t, command, result.Tests[0].Command)
		}
	}
	for _, command := range []string{"cypress verify", "cypress open", "cypress version", "cypress info"} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "cypress")
		require.False(t, result.Matched)
		require.True(t, result.Review)
	}
}

func TestCypressActionAndStatusGateAreClassifiedSeparately(t *testing.T) {
	root := t.TempDir()
	action := runtimeStep{Uses: "cypress-io/github-action@v7"}
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, action, "javascript", "cypress")
	require.True(t, result.Matched)
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	require.NotNil(t, result.Tests[0].Cypress)
	require.Equal(t, action.Uses, result.Tests[0].Cypress.Action)
	gate := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "exit 1"}, "javascript", "cypress")
	require.False(t, gate.Matched)
	require.True(t, gate.Review)
	condition := "github.repository_owner == 'cypress-io' && (github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository)"
	active, err := possibleJobCondition(condition, nil)
	require.NoError(t, err)
	require.True(t, active)
	_, err = runtimeCondition(condition, nil)
	require.Error(t, err, "the possible-job shortcut must not apply to step-level installation conditions")
}

func TestCypressNodeDiscoveryDoesNotTreatInlineScriptsAsCLI(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"node -e node_modules/cypress/bin/cypress run", "node scripts/runner.js run", "node --require node_modules/cypress/bin/cypress run", "node --import"} {
		_, err := CypressValidationCommand(root, command)
		require.Error(t, err, command)
	}
}

func TestCypressLocalEnvironmentDoesNotResolveCloudSecretsOrRunnerCache(t *testing.T) {
	workflow := ciWorkflow{Env: map[string]string{"CYPRESS_CACHE_FOLDER": "${{ github.workspace }}/.cache/Cypress"}}
	step := runtimeStep{Run: "npx cypress run --record", Env: map[string]string{"CYPRESS_RECORD_KEY": "${{ secrets.recordKey }}", "TERM": "xterm"}}
	_, env, err := resolveVariantStep(workflow, runtimeJob{}, step, nil, "cypress")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"TERM": "xterm"}, env)
	require.Equal(t, "${{ secrets.recordKey }}", step.Env["CYPRESS_RECORD_KEY"], "CI configuration must remain unchanged")
	_, _, err = resolveVariantStep(workflow, runtimeJob{}, step, nil, "jest")
	require.Error(t, err, "other frameworks retain their existing environment review")
	step.Env["CYPRESS_BASE_URL"] = "${{ secrets.applicationUrl }}"
	_, _, err = resolveVariantStep(workflow, runtimeJob{}, step, nil, "cypress")
	require.Error(t, err, "unknown application settings must still block local execution")
}

func TestCypressScopeKeepsMetadataAndStatusJobsOutsideExecution(t *testing.T) {
	root := newJestRepository(t, `jobs:
  metadata:
    if: github.repository_owner == 'example'
    steps:
      - uses: example/issue-board@v1
  gate:
    if: always()
    steps:
      - run: exit 1
  tests:
    steps:
      - run: |
          npm i -g @bahmutov/print-env@2.1.2
          print-env GITHUB BUILD ACTIONS
      - run: npx cypress run
  unknown:
    steps:
      - run: custom-test-wrapper
`)
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.NotEmpty(t, scope.Commands[0].UnvalidatedSetup, "unknown environment-printing utilities remain unresolved setup")
	require.NotContains(t, scope.Unresolved, "always()")
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "custom-test-wrapper")
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "print-env")
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "dependency changes require separate validation")
	require.NotEmpty(t, scope.Review)
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "print-env GITHUB && npx cypress run"}, "javascript", "cypress")
	require.False(t, result.Review, "metadata must not hide an accompanying test command")
	require.True(t, result.Matched)
	result = resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "print-env $(npx cypress run)"}, "javascript", "cypress")
	require.False(t, result.Review, "metadata must not discard command substitutions")
}

func TestCypressDefaultPrefersTheActualCICoverageAlias(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  tests:\n    steps:\n      - run: npm run test:ci:record:coverage\n")
	writeScripts(t, root, map[string]string{"start": "node start.js", "start:coverage": "node start.js --coverage", "e2e": "cypress run", "e2e:record": "cypress run --record", "local:run": "start-test 8080 e2e", "test:ci:record:coverage": "run-p --race start:coverage e2e:record"})
	command, err := CypressValidationCommand(root, "")
	require.NoError(t, err)
	require.Equal(t, "npm run test:ci:record:coverage", command)
	command, err = CypressValidationCommand(root, "npm run local:run")
	require.NoError(t, err)
	require.Equal(t, "npm run local:run", command, "an explicit customer command still overrides CI selection")
}

func TestCypressLocalPlanKeepsApplicationFlagsButSeparatesCIInstallation(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    env:
      DD_API_KEY: ${{ secrets.DD_API_KEY }}
    steps:
      - uses: datadog/test-visibility-github-action@v3
        if: env.DD_API_KEY != ''
        with:
          languages: js
      - run: npx cypress run
        env:
          COVERAGE: 'true'
          DD_TEST_COVERAGE: '1'
          TERM: xterm
      - uses: actions/upload-artifact@v4
        if: failure()
`)
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Commands[0].UnvalidatedSetup)
	require.Equal(t, map[string]string{"COVERAGE": "true", "DD_TEST_COVERAGE": "1", "TERM": "xterm"}, scope.Commands[0].Environment)
	require.Empty(t, scope.Unresolved)
	require.NotEmpty(t, scope.Review)
	// Static CI validation must still reject an unresolved installation guard.
	_, err = runtimeCondition("env.DD_API_KEY != ''", nil)
	require.Error(t, err)
}
