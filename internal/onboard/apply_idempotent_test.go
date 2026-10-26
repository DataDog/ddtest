package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// A resume reruns apply on ddtest's own output. Trials showed that rejecting
// the combined guard it wrote led agents to delete the guard by hand, which
// instrumented Windows rows. A second apply must accept the first unchanged.
func TestApplyAcceptsItsOwnGuardedActionOnResume(t *testing.T) {
	for name, workflow := range map[string]string{
		"step guard":       "jobs:\n  test:\n    strategy:\n      matrix: {node: ['20', '24']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n        if: matrix.node != '20'\n",
		"windows rows":     "jobs:\n  test:\n    strategy: {matrix: {os: [ubuntu-latest, windows-latest]}}\n    runs-on: ${{ matrix.os }}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n",
		"step and windows": "jobs:\n  test:\n    strategy: {matrix: {os: [ubuntu-latest, windows-latest], node: ['20', '24']}}\n    runs-on: ${{ matrix.os }}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n        if: matrix.node != '20'\n",
		"unsupported node": "jobs:\n  test:\n    strategy:\n      matrix: {node: ['16', '24']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := newJestRepository(t, workflow)
			_, err := ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			path := filepath.Join(root, ".github/workflows/test.yml")
			first, err := os.ReadFile(path)
			require.NoError(t, err)
			_, err = ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
			require.NoError(t, err, "a second apply must accept the first:\n%s", first)
			second, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, string(first), string(second))
		})
	}
}

// A real difference names the exact replacement and never asks to drop a
// Windows guard.
func TestApplyNamesTheExactGuardForADifferentAction(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    strategy: {matrix: {os: [ubuntu-latest, windows-latest]}}\n    runs-on: ${{ matrix.os }}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - uses: "+setupActionV3+"\n        if: matrix.os == 'ubuntu-latest'\n        continue-on-error: true\n        with: {languages: js, api_key: '${{ secrets.DD_API_KEY }}', js-tracer-version: 5.132.0}\n      - run: npm test\n")
	_, err := ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
	require.Error(t, err)
	require.Contains(t, err.Error(), "replace it with `if: matrix.os != 'windows-latest'`")
	require.NotContains(t, err.Error(), "align it")
}

// jasmine-boilerplate names its test step "🧪 Test"; re-encoding the step to
// add the preload wrote it back as "\U0001F9EA Test".
func TestApplyKeepsTheCustomersScalarsAsWritten(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - name: 🧪 Test\n        run: npm test\n      - name: 'it''s ✅'\n        run: echo done\n")
	_, err := ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "name: 🧪 Test\n")
	require.NotContains(t, string(data), `\U0001F9EA`)
}

func TestRestoreEscapedScalarsKeepsQuotedStyles(t *testing.T) {
	step := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "name"}, {Kind: yaml.ScalarNode, Value: "🧪 Test"},
		{Kind: yaml.ScalarNode, Value: "id"}, {Kind: yaml.ScalarNode, Value: "it's 🧪", Style: yaml.SingleQuotedStyle},
	}}
	encoded := "name: \"\\U0001F9EA Test\"\nid: \"it's \\U0001F9EA\"\nother: \"\\U0001F9EA new\"\n"
	require.Equal(t, "name: 🧪 Test\nid: 'it''s 🧪'\nother: \"\\U0001F9EA new\"\n", restoreEscapedScalars(encoded, step))
}
