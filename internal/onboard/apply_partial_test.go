package onboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSimpleStepConditionReadsOnlyMatrixAndEnvValues(t *testing.T) {
	for condition, simple := range map[string]bool{
		"matrix.node != '20'":                              true,
		"${{ matrix.node != '20' }}":                       true,
		"matrix.os == 'ubuntu-latest' && env.CI == 'true'": true,
		"!(matrix.legacy)":                                 true,
		"matrix.node >= 22":                                true,
		"":                                                 false,
		"success() && matrix.node != '20'":                 false,
		"contains(matrix.node, '2')":                       false,
		"github.event_name == 'push'":                      false,
		"steps.build.outcome == 'success'":                 false,
		"'literal' == 'literal'":                           false,
		"matrix.node != '20":                               false,
	} {
		require.Equal(t, simple, simpleStepCondition(condition), condition)
	}
}

// Modeled on astrologyjs: the test step skips one matrix row with its own
// guard. Setup copies that guard onto the action instead of refusing the step.
func TestApplyCopiesASimpleStepGuardOntoTheAction(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    strategy:\n      matrix: {node: ['20', '24']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n        if: matrix.node != '20'\n")
	_, err := ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	workflow := string(data)
	action := workflow[strings.Index(workflow, "- name: Configure Datadog Test Optimization"):]
	action = action[:strings.Index(action, "uses:")]
	require.Contains(t, action, "if: matrix.node != '20'", workflow)
	require.Equal(t, 2, strings.Count(workflow, "if: matrix.node != '20'"), "the test step keeps its own guard:\n%s", workflow)

	check := CheckCIRuntimes(t.Context(), root, "jest")
	for _, job := range check.Jobs {
		require.NotEqual(t, CodeMissingInstrumentation, job.Code, "%+v", check.Jobs)
	}
}

// Manual guidance names the same verified commit setup writes, so a workflow
// that pins its actions isn't handed a floating tag.
func TestJavaScriptInstructionsPinTheVerifiedAction(t *testing.T) {
	for _, framework := range []string{"jest", "vitest", "mocha", "playwright", "cypress", "cucumber", "webdriverio"} {
		text := instructions("javascript", framework)
		require.Contains(t, text, setupActionV3, framework)
		require.NotContains(t, text, "test-visibility-github-action@v3", framework)
	}
	require.Contains(t, instructions("python", "pytest"), "test-visibility-github-action@v3", "Python and Ruby guidance stays unchanged")
}

func TestWindowsOnlyConfigurationResolvesEachLocationsRunner(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    strategy: {matrix: {os: [ubuntu-latest, windows-latest]}}\n    runs-on: ${{ matrix.os }}\n    steps:\n      - run: npm test\n")
	windows := commandLocation{Workflow: ".github/workflows/test.yml", Job: "test", Matrix: map[string]any{"os": "windows-latest"}, Step: 1}
	ubuntu := commandLocation{Workflow: ".github/workflows/test.yml", Job: "test", Matrix: map[string]any{"os": "ubuntu-latest"}, Step: 1}
	require.True(t, WindowsOnlyConfiguration(root, TestCommand{locations: []commandLocation{windows}}))
	require.False(t, WindowsOnlyConfiguration(root, TestCommand{locations: []commandLocation{windows, ubuntu}}))
	require.False(t, WindowsOnlyConfiguration(root, TestCommand{}), "an entry without known locations is never skipped")
}

func TestApplyStillRefusesStepGuardsOnJobStatus(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n        if: success() && github.event_name == 'push'\n")
	_, err := ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
	var gap *SetupGap
	require.True(t, errors.As(err, &gap), "%v", err)
	require.Contains(t, gap.Reason, "needs explicit loader placement")
}
