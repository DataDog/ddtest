package onboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCypressSetupNodeWithoutInputsIsInconclusive(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-node@v4
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npx cypress run
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
	}, "cypress")
	require.Equal(t, "inconclusive", check.Status, check.Jobs)
	require.Len(t, check.Jobs, 1)
}

func TestCypressAPIRuntimeDoesNotResolveSetupNodeAlias(t *testing.T) {
	job := runtimeJob{Steps: []runtimeStep{
		{Uses: "actions/setup-node@v4", With: map[string]string{"node-version": "lts/*"}},
		{Uses: "datadog/test-visibility-github-action@v3", With: map[string]string{"languages": "js", "js-tracer-version": "6.20.1"}},
		{Uses: "cypress-io/github-action@v6", ActionNode: "20", Env: map[string]string{"NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }}"}},
	}}
	resolutions := []commandResolution{{}, {}, {Matched: true, Tests: []TestCommand{{Cypress: &CypressCommand{Action: "cypress-io/github-action@v6"}}}}}
	findings := checkRuntimeJob(t.Context(), "", ciWorkflow{}, "tests", job, resolutions, nil,
		func(context.Context, string, string) (NodeResolution, error) {
			t.Fatal("API runtime must not resolve setup-node's alias")
			return NodeResolution{}, nil
		},
		func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
		}, "cypress")
	require.Len(t, findings, 1)
	require.Equal(t, "20", findings[0].Node)
	require.Equal(t, "incompatible", findings[0].Status)
	require.Nil(t, findings[0].NodeResolution)
}

func TestCypressActionUsesItsOwnDirectoryAndPreservesCustomSetup(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "frontend"), 0700))
	workflow := ciWorkflow{Defaults: ciDefaults{Run: runDefaults{WorkingDirectory: "wrong"}}}
	job := runtimeJob{Defaults: ciDefaults{Run: runDefaults{WorkingDirectory: "also-wrong"}}}
	action := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"build": "node build.cjs", "start": "node server.cjs", "wait-on": "http://127.0.0.1:8080"}}
	result := resolveCypressAction(root, workflow, job, action)
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	entry := result.Tests[0]
	require.Equal(t, ".", entry.Directory)
	require.Equal(t, ".", entry.Prerequisites[0].Directory)
	action.With["command"] = "npx cypress run --browser electron"
	action.With["browser"] = "ignored-by-custom-command"
	action.With["working-directory"] = "frontend"
	result = resolveCypressAction(root, workflow, job, action)
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	entry = result.Tests[0]
	require.Equal(t, "frontend", entry.Directory)
	require.Equal(t, "frontend", entry.Prerequisites[0].Directory)
	require.Equal(t, action.With["command"], entry.Command)
	require.Empty(t, entry.Cypress.Action)
	require.Empty(t, entry.Cypress.Options)
	require.Equal(t, []string{"npx", "cypress", "run", "--browser", "electron"}, entry.Cypress.CLI)
	require.Equal(t, [][]string{{"node", "server.cjs"}}, entry.Cypress.Start)
	require.Equal(t, []string{"http://127.0.0.1:8080"}, entry.Cypress.WaitOn)
}

func TestCypressCustomInstallCommandsRemainUnvalidated(t *testing.T) {
	result := resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"install-command": "node generate-config.cjs"}})
	require.False(t, result.Matched)
	require.Contains(t, result.Reason, "install-command")
}

func TestCypressActionMatrixKeepsWindowsStartupSeparate(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix: {os: [ubuntu-latest, windows-latest]}
    steps:
      - uses: cypress-io/github-action@v7
        with:
          start: node unix.cjs
          start-windows: node windows.cjs
`)
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	platforms := map[string][]string{}
	for _, entry := range scope.Commands {
		require.Len(t, entry.Locations, 1)
		platforms[entry.Cypress.Platform] = entry.Cypress.Start[0]
	}
	require.Equal(t, []string{"node", "unix.cjs"}, platforms["linux"])
	require.Equal(t, []string{"node", "windows.cjs"}, platforms["windows"])
}

func TestCypressMultipleAliasInvocationsKeepTheirOrderUnresolved(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    steps:
      - run: npm test
`)
	writeScripts(t, root, map[string]string{"test": "cypress run --spec a.cy.js && cypress run --spec b.cy.js"})
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	require.Empty(t, scope.Commands)
	require.NotEmpty(t, scope.Unresolved)
}

func TestCypressActionSerializedOptionsMatchTheAction(t *testing.T) {
	result := resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"config": "viewportWidth=800", "env": "feature=true,number=7", "expose": "publicValue=true"}})
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	require.Equal(t, "viewportWidth=800", result.Tests[0].Cypress.Options["config"])
	require.Equal(t, "feature=true,number=7", result.Tests[0].Cypress.Options["env"])
	require.Equal(t, "publicValue=true", result.Tests[0].Cypress.Options["expose"])
}
