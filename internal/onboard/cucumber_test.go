package onboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCucumberLiteralAliasesPreserveCoverageAndProfiles(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "nyc -- cucumber-js features --profile ci"})
	commands := ValidationCommands(root, "npm test", "cucumber")
	require.Len(t, commands, 1)
	require.Empty(t, commands[0].UnvalidatedSetup)
	require.Equal(t, "npm test", commands[0].Command)
	require.True(t, CucumberUsesNYC(root, "npm test"))
	probe, err := CucumberProbeCommand(root, "npm test")
	require.NoError(t, err)
	require.Equal(t, "cucumber-js features --profile ci", probe)
	_, err = CucumberValidationCommand(root, "npm test")
	require.NoError(t, err)
}
func TestCucumberAmbiguousAliasesStayUnvalidated(t *testing.T) {
	for _, script := range []string{"MODE=inner cucumber-js features", "cd nested && cucumber-js features", `DEBUG="any,$DEBUG" cucumber-js features`, "run-s test:*", "cucumber-js one && cucumber-js two"} {
		t.Run(script, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0700))
			writeScripts(t, root, map[string]string{"test": script, "test:one": "cucumber-js features"})
			_, err := CucumberValidationCommand(root, "npm test")
			require.Error(t, err)
		})
	}
}
func TestCucumberCIRequiresCoverageAndEarlyInitialization(t *testing.T) {
	for _, coverage := range []bool{false, true} {
		t.Run(map[bool]string{false: "coverage missing", true: "coverage configured"}[coverage], func(t *testing.T) {
			root := t.TempDir()
			script := "cucumber-js features"
			if coverage {
				script = "nyc -- " + script
			}
			writeScripts(t, root, map[string]string{"test": script})
			path := filepath.Join(root, ".github/workflows/ci.yml")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			require.NoError(t, os.WriteFile(path, []byte(`jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`), 0600))
			check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
			}, "cucumber")
			if coverage {
				require.Equal(t, "compatible", check.Status, check.Jobs)
			} else {
				require.Equal(t, "inconclusive", check.Status, check.Jobs)
				require.Contains(t, check.Jobs[0].Reason, "coverage")
			}
		})
	}
}
