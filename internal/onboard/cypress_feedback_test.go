package onboard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCypressDisabledActionDoesNotInventTestsOrDiscardSetup(t *testing.T) {
	for _, value := range []string{"false", "0", " 0 "} {
		step := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"runTests": value}}
		result := resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, step)
		require.False(t, result.Matched)
		require.Empty(t, result.Reason)
		for _, key := range []string{"build", "start", "start-windows", "wait-on", "install-command"} {
			step.With[key] = "node setup.cjs"
			result = resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, step)
			require.Contains(t, result.Reason, "setup-only")
			delete(step.With, key)
		}
	}
	for _, value := range []string{"true", "1", "", "invalid"} {
		require.True(t, cypressInputBool(value, true))
	}
}

func TestCypressDoesNotGuessACommand(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "echo no browser tests"})
	_, err := CypressExecutionCommand(root, "")
	require.ErrorContains(t, err, "no Cypress command found")
	writeScripts(t, root, map[string]string{"arbitrary-name": "cypress run"})
	entry, err := CypressExecutionCommand(root, "")
	require.NoError(t, err)
	require.Equal(t, "npm run arbitrary-name", entry.Command)
}

func TestCypressDoesNotFallBackToAScriptWhenCIHasAnUnresolvedCommand(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "cypress run"})
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("jobs:\n  e2e:\n    steps:\n      - run: npx start-server-and-test 'node server.js' http://localhost:3000 'cypress run'\n"), 0644))
	_, err := CypressExecutionCommand(root, "")
	require.ErrorContains(t, err, "the Cypress CI command could not be resolved; select the command to validate with --command")
	require.ErrorContains(t, err, "start-server-and-test")
}

func TestCypressComponentCommandsAreExplicitlyUnsupported(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"cypress run --component", "node node_modules/cypress/bin/cypress run --component"} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "cypress")
		require.Contains(t, result.Reason, "component testing is not supported")
		_, err := CypressExecutionCommand(root, command)
		require.ErrorContains(t, err, "component testing is not supported")
	}
	step := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"component": "1"}}
	result := resolveCypressAction(root, ciWorkflow{}, runtimeJob{}, step)
	require.Contains(t, result.Reason, "component testing is not supported")
	writeScripts(t, root, map[string]string{"test": "cypress run"})
	step.With["command"] = "npm test"
	result = resolveCypressAction(root, ciWorkflow{}, runtimeJob{}, step)
	require.Empty(t, result.Reason, "custom commands ignore API-only component input")
}

func TestCypressManagedCommandsSeparateLiteralAssignments(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"start": "node server.cjs", "test": "cypress run"})
	for _, command := range []string{"node server.cjs & CYPRESS_BASE_URL=http://localhost:3000 npx cypress run", "node server.cjs & CYPRESS_BASE_URL=http://localhost:3000 npm test"} {
		result, ok := cypressBackgroundPlan(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command})
		require.True(t, ok)
		require.Empty(t, result.Reason)
		require.Equal(t, "http://localhost:3000", result.Tests[0].Environment["CYPRESS_BASE_URL"])
		require.NotContains(t, result.Tests[0].Cypress.CLI[0], "=")
	}
	result := resolveCypressAction(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"command": "CYPRESS_BASE_URL=http://localhost:3000 npm test", "start": "node server.cjs"}})
	require.Empty(t, result.Reason)
	require.Equal(t, []string{"npm", "test"}, result.Tests[0].Cypress.CLI)
	require.Equal(t, "http://localhost:3000", result.Tests[0].Environment["CYPRESS_BASE_URL"])
}

func TestCypressOSMatrixKeepsEveryDeclaredPlatform(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix: {os: [ubuntu-latest, macos-latest, windows-latest]}
    steps:
      - uses: cypress-io/github-action@v7
`)
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	platforms := map[string]bool{}
	for _, entry := range scope.Commands {
		platforms[entry.Cypress.Platform] = true
		require.Len(t, entry.Locations, 1)
	}
	require.Equal(t, map[string]bool{"linux": true, "darwin": true, "windows": true}, platforms)
}

func TestCypressAliasRetainsItsConsumerPackage(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "npm --prefix frontend test"})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "frontend"), 0700))
	writeScripts(t, filepath.Join(root, "frontend"), map[string]string{"test": "cypress run"})
	entry, err := CypressExecutionCommand(root, "npm test")
	require.NoError(t, err)
	require.Equal(t, ".", entry.Directory)
	require.Equal(t, "frontend", entry.CypressPackage)
	require.Equal(t, "npm test", entry.Command)
}

func TestCypressMetadataCachesFailuresWithinOneInvocation(t *testing.T) {
	ctx := WithCypressMetadataCache(t.Context())
	calls := 0
	fetch := func() ([]byte, error) { calls++; return nil, errors.New("network unavailable") }
	for range 3 {
		_, err := cachedCypressMetadata(ctx, "action@sha", fetch)
		require.ErrorContains(t, err, "network unavailable")
	}
	require.Equal(t, 1, calls)
	_, err := cachedCypressMetadata(WithCypressMetadataCache(context.Background()), "action@sha", fetch)
	require.Error(t, err)
	require.Equal(t, 2, calls)
}

func TestCypressGuardedPreloadsResolveWithoutPrematureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, workflowEnv, jobOptions, options, want string
	}{
		{name: "empty inheritance", options: "${{ steps.dd.outcome == 'success' && format('-r {0}', env.DD_TRACE_PACKAGE) || env.NODE_OPTIONS }}"},
		{name: "overridden unknown workflow", workflowEnv: "env: {NODE_OPTIONS: '${{ vars.DEFAULT_NODE_OPTIONS }}'}\n", options: "${{ steps.dd.outcome == 'success' && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}"},
		{name: "job overrides unknown workflow", workflowEnv: "env: {NODE_OPTIONS: '${{ vars.DEFAULT_NODE_OPTIONS }}'}\n", jobOptions: ", NODE_OPTIONS: '--trace-warnings'", options: "${{ steps.dd.outcome == 'success' && format('{0} -r {1}', env.NODE_OPTIONS, env.DD_TRACE_PACKAGE) || env.NODE_OPTIONS }}", want: "--trace-warnings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newJestRepository(t, `on: push
`+tc.workflowEnv+`jobs:
  tests:
    env: {DD_API_KEY: '${{ secrets.DD_API_KEY }}'`+tc.jobOptions+`}
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        id: dd
        if: env.DD_API_KEY != ''
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npx cypress run
        env:
          NODE_OPTIONS: `+tc.options+`
`)
			initializeCypressContextRepository(t, root)
			scope, err := DiscoverValidationScope(root, "cypress")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Equal(t, tc.want, scope.Commands[0].Environment["NODE_OPTIONS"])
			check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
			}, "cypress")
			require.Equal(t, StatusCompatible, check.Status, check.Jobs)
			require.Len(t, check.Jobs, 2)
			for _, finding := range check.Jobs {
				require.NotContains(t, finding.Reason, "unresolved NODE_OPTIONS expression")
			}
		})
	}
}

func TestCypressPushContextsHaveNoPullRequestFields(t *testing.T) {
	root := t.TempDir()
	initializeCypressContextRepository(t, root)
	values := cypressCIContexts(root)
	require.Len(t, values, 7)
	for _, value := range values {
		if value["github.event_name"] != "pull_request" {
			require.Empty(t, value["github.event.pull_request.head.repo.full_name"])
		}
	}
}

func initializeCypressContextRepository(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "--initial-branch=juan-fernandez/cypress-context", root}, {"-C", root, "remote", "add", "origin", "https://github.com/example/project.git"}} {
		output, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		require.NoError(t, err, string(output))
	}
}

func TestCypressSecretRedactionPreservesNonKeyOptions(t *testing.T) {
	for _, command := range []string{"cypress run --key secret --browser electron", "cypress run -ksecret", "cypress run -k 'secret value'", "cypress run --key=secret"} {
		redacted := RedactCypressCommand(command)
		require.NotContains(t, redacted, "secret")
		require.Contains(t, redacted, "REDACTED")
	}
}

func TestCypressRecordingVariablesAreRedactedButProseKeepsShortOptions(t *testing.T) {
	for _, command := range []string{"CYPRESS_RECORD_KEY=secret npx cypress run", "env CYPRESS_CLOUD_TOKEN='secret value' cypress run"} {
		require.NotContains(t, RedactCypressCommand(command), "secret")
		require.NotContains(t, RedactCypressText(command), "secret")
	}
	require.Equal(t, "the -kitten option", RedactCypressText("the -kitten option"))
	require.Equal(t, "failed: npx cypress run -k[REDACTED]", RedactCypressText("failed: npx cypress run -ksecret"))
	require.Equal(t, "failed: npx wdio run wdio.conf.js -k[REDACTED]", RedactCypressText("failed: npx wdio run wdio.conf.js -ksecret"))
	scope := ValidationScope{Commands: []TestCommand{{Command: "npm test -- -k secret", Environment: map[string]string{"CYPRESS_RECORD_KEY": "secret", "TERM": "xterm"}, Locations: []string{"ci.yml / test / step 1 (npm test -- -k secret)"}}}, Unresolved: []string{"ci.yml / test / step 2 (CYPRESS_RECORD_KEY=secret cypress run): unresolved"}}
	redacted := RedactCypressScope(scope)
	var output bytes.Buffer
	WriteValidationScope(&output, redacted)
	require.NotContains(t, output.String(), "secret")
	require.Equal(t, "[REDACTED]", redacted.Commands[0].Environment["CYPRESS_RECORD_KEY"])
	require.Equal(t, "xterm", redacted.Commands[0].Environment["TERM"])
	require.Equal(t, "secret", scope.Commands[0].Environment["CYPRESS_RECORD_KEY"], "the executed scope keeps its values")
}

func TestCypressLiteralStatusGatesRemainOutsideExecution(t *testing.T) {
	for _, command := range []string{"exit", "exit 0", "exit 1", "exit 42"} {
		require.True(t, cypressStatusGate(command))
	}
	for _, command := range []string{"exit 1 && cypress run", "exit $CODE", "exit 300"} {
		require.False(t, cypressStatusGate(command))
	}
}
