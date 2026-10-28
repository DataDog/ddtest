package onboard

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscardedDependencyCheckDoesNotHideCucumber(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "npm-run-all --parallel lint:* test:*", "lint:peer": "npm ls >/dev/null", "test:integration": "cucumber-js test/integration"})
	remaining := 256
	result := resolveJestSequence(root, root, "npm test", nil, &remaining, "cucumber")
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	require.Contains(t, result.ReviewReason, "Dependency inspection")
	require.Equal(t, "npm test", result.Tests[0].Command)
	for _, command := range []string{"jest >/dev/null", "npm ls > file", "npm ls >$(npm test)", "npm ls </dev/null", "npm ls 3>/dev/null", "npm ls 2>&1", "npm ls >/dev/null && node hidden.js"} {
		remaining = 256
		resolution := resolveJestSequence(root, root, command, nil, &remaining, "cucumber")
		require.NotEmpty(t, resolution.Reason, command)
	}
}

func TestMultipleWDIOAliasesRetainOrderedLeaves(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"first": "wdio run ./wdio.conf.js --spec first.js", "second": "wdio run ./wdio.conf.js --spec second.js", "test": "run-s first second"})
	remaining := 256
	result := resolveJestSequence(root, root, "npm test", nil, &remaining, "webdriverio")
	require.Empty(t, result.Reason)
	require.Equal(t, []string{"npm run first", "npm run second"}, commandNames(result.Tests))
	_, err := WebdriverIOValidationCommand(root, "npm test")
	require.Error(t, err, "focused execution must still require one invocation")
}

func TestReportingConditionsDoNotBlockSelectedTests(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: jest
      - run: bash <(curl -s https://codecov.io/bash)
      - uses: actions/upload-artifact@v4
        if: failure()
      - uses: coverallsapp/github-action@v2
        if: steps.env.outputs.nyc != ''
  admin:
    if: github.actor == 'dependabot[bot]'
    steps:
      - run: gh pr merge --auto --squash "$PR_URL"
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.NotEmpty(t, scope.Review)
	require.False(t, externalCIReporting(`gh pr merge "$(npm test)"`))
	require.False(t, externalCIReporting(`bash <(curl -s https://example.test/custom)`))
}

func TestPersistedNVMPathEstablishesLaterRuntime(t *testing.T) {
	command := `nvm install --default 22.0
if [[ "22.0" == 0.* && "$(cut -d. -f2 <<< "22.0")" -lt 10 ]]; then
  nvm install --alias=npm 0.10
  nvm use 22.0
  ` + npmShebangRepair + `
  npm config set strict-ssl false
fi
dirname "$(nvm which 22.0)" >> "$GITHUB_PATH"`
	require.Equal(t, "22.0", persistedNVMRuntime(command))
	state := runtimeState{}
	state.trackNode(runtimeJob{RunsOn: "ubuntu-latest"}, runtimeStep{Run: command, Source: "setup"}, nil)
	require.Equal(t, "22.0", state.node)
	resolution := resolveTestStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "mocha")
	require.Empty(t, resolution.Setup)
	for _, bad := range []string{`nvm install 22`, command + "\necho /other/bin >> \"$GITHUB_PATH\"", `nvm install 22\ndirname "$(nvm which 20)" >> "$GITHUB_PATH"`, `nvm install 22\ndirname "$(nvm which 22)" > "$GITHUB_PATH"`} {
		require.Empty(t, persistedNVMRuntime(bad), bad)
	}
}

func TestCucumberDebugBeforeScopedPreload(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{}}`), 0600))
	require.True(t, cucumberScopedScript(`DEBUG="any,$DEBUG" `+cucumberScopedOptions+` cucumber-js test`))
	require.False(t, cucumberScopedScript(`PATH=/tmp `+cucumberScopedOptions+` cucumber-js test`))
}

func TestNumericNodeFallbackUsesDistributionMetadata(t *testing.T) {
	calls := map[string]int{}
	resolver := setupNodeResolver(&http.Client{Transport: runtimeTransport(func(r *http.Request) (*http.Response, error) {
		calls[r.URL.String()]++
		data := `[]`
		if r.URL.String() == nodeDistributionIndex {
			data = `[{"version":"v25.8.1","files":["linux-x64"]},{"version":"v25.9.0","files":["linux-arm64"]}]`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data))}, nil
	})})
	for range 2 {
		result, err := resolver(context.Background(), "25.8.1", "linux-x64")
		require.NoError(t, err)
		require.Equal(t, "25.8.1", result.Version)
		require.Equal(t, nodeDistributionIndex, result.Source)
	}
	require.Equal(t, 1, calls[nodeVersionsManifest])
	require.Equal(t, 1, calls[nodeDistributionIndex])
	_, err := resolver(context.Background(), "25.9.0", "linux-x64")
	require.Error(t, err, "a release without this platform must stay unresolved")
}

func TestNpmDependencyVariantsAreExplicitPreparation(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"npm install --save-dev mocha@3.5.3 nyc@10.3.2", "npm rm --silent --save-dev nyc"} {
		words, err := staticCommands(command)
		require.NoError(t, err)
		build, ok := localPreparation(root, root, words[0])
		require.True(t, ok)
		require.Equal(t, PreparationDependencies, build.Kind)
		resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "mocha")
		require.Empty(t, resolution.Setup)
		require.Empty(t, resolution.Reason)
		require.Len(t, resolution.Builds, 1)
		require.Equal(t, PreparationDependencies, resolution.Builds[0].Kind)
	}
	for _, command := range []string{"npm install --save-dev ../package", "npm install --save-dev mocha@latest", "npm install --save-dev mocha@3.5.3 --prefix /tmp", "npm rm"} {
		words, err := staticCommands(command)
		require.NoError(t, err)
		_, ok := localPreparation(root, root, words[0])
		require.False(t, ok, command)
	}
}

func TestWDIOAliasSpecGlobKeepsOriginalExecution(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "cross-env EXAMPLE_RECIPE=api wdio run ./wdio.conf.js --spec ./api/*.js"})
	remaining := 256
	result := resolveJestSequence(root, root, "npm test", nil, &remaining, "webdriverio")
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	require.Equal(t, "npm test", result.Tests[0].Command)
	for _, script := range []string{"wdio run $CONFIG --spec ./api/*.js", "wdio run ./wdio.conf.js --spec $(npm test)", "wdio run ./wdio.conf.js --spec ../*.js", "wdio run ./wdio.conf.js --spec ./api/*.js && node hidden.js"} {
		writeScripts(t, root, map[string]string{"test": script})
		remaining = 256
		result = resolveJestSequence(root, root, "npm test", nil, &remaining, "webdriverio")
		require.NotEmpty(t, result.Reason, script)
	}
}

func TestDependencyMatrixScopeRetainsReplayablePreparation(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix:
        dependency: [mocha@3.5.3, mocha@11.4.0]
    steps:
      - run: npm install --save-dev ${{ matrix.dependency }}
      - run: mocha test/
`)
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	for _, entry := range scope.Commands {
		require.Empty(t, entry.UnvalidatedSetup)
		require.Len(t, entry.Prerequisites, 1)
		require.Equal(t, PreparationDependencies, entry.Prerequisites[0].Kind)
	}
}

func TestConditionalInstallerPartitionsScopeWithoutDroppingLegacyTests(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix: {node: ['18', '22']}
    steps:
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.node == '22'
        with: {languages: js}
      - run: mocha test/
`)
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2, "identical commands with different instrumentation scope cannot merge")
	require.True(t, scope.Commands[0].CIInstrumentationExcluded)
	require.False(t, scope.Commands[1].CIInstrumentationExcluded)
	AssignConfigurationIDs(&scope)
	require.NotEqual(t, scope.Commands[0].ID, scope.Commands[1].ID)
	// No installer is not an opt-out, and an earlier active installer remains active.
	plain := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "mocha test/"}, "javascript", "mocha")
	require.False(t, plain.Tests[0].CIInstrumentationExcluded)
	path := filepath.Join(root, ".github/workflows/test.yml")
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, workflow := range []string{
		strings.Replace(string(original), "    steps:\n", "    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js}\n", 1),
		strings.Replace(string(original), "      - run: mocha test/", "      - run: mocha test/\n        env: {NODE_OPTIONS: '-r dd-trace/ci/init'}", 1),
	} {
		require.NoError(t, os.WriteFile(path, []byte(workflow), 0600))
		current, err := DiscoverValidationScope(root, "mocha")
		require.NoError(t, err)
		for _, entry := range current.Commands {
			require.False(t, entry.CIInstrumentationExcluded)
		}
	}
}

func TestExplicitCIOptOutSurvivesEnvironmentSanitization(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    env: {DD_CIVISIBILITY_ENABLED: 'false'}
    steps:
      - run: mocha test/
`)
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.NotContains(t, scope.Commands[0].Environment, "DD_CIVISIBILITY_ENABLED", "CI tracer controls are not forwarded into controlled execution")
	require.True(t, scope.Commands[0].CIInstrumentationExcluded, "keep the opt-out as metadata rather than forwarding its environment")
}
