package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupPlanDescribesLocalImpactBeforeApplying(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm run build && npm test\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"build":"node -e 1","test":"mocha"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
	plan, err := PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	require.False(t, plan.Configured())
	impact := strings.Join(plan.LocalImpact(), "\n")
	require.Contains(t, impact, "step 2 also runs other commands (npm run build && npm test)")
	require.Contains(t, impact, ".nyc_output/")
	diff, err := plan.Diff()
	require.NoError(t, err)
	require.Contains(t, diff, "+/.testoptimization/\n+.nyc_output/\n")

	_, err = plan.Apply(true)
	require.NoError(t, err)
	plan, err = PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	require.True(t, plan.Configured(), "a second plan has nothing left to edit")
}

func TestSetupIgnoresNYCOutputInNestedWorkingDirectories(t *testing.T) {
	for existing, want := range map[string]string{
		"":                  "/.testoptimization/\n.nyc_output/\n",
		"/.nyc_output/\n":   "/.nyc_output/\n/.testoptimization/\n.nyc_output/\n",
		".nyc_output\n":     ".nyc_output\n/.testoptimization/\n",
		"**/.nyc_output/\n": "**/.nyc_output/\n/.testoptimization/\n",
	} {
		root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n        working-directory: sub\n")
		require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "sub/package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0"}}`), 0600))
		if existing != "" {
			require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte(existing), 0600))
		}
		_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
		require.NoError(t, err)
		ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
		require.NoError(t, err)
		require.Equal(t, want, string(ignore), "a root-only pattern doesn't cover sub/.nyc_output/")
	}
}

func TestScopedSetupImpactNamesPOSIXScripts(t *testing.T) {
	root := mixedCucumberSetupRepository(t)
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	updated := strings.Replace(string(data), `"devDependencies":{`, `"devDependencies":{"nyc":"18.0.0","@istanbuljs/esm-loader-hook":"0.3.0",`, 1)
	require.NotEqual(t, string(data), updated)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(updated), 0600))
	plan, err := PlanJavaScriptSetup(root, "cucumber", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	impact := strings.Join(plan.LocalImpact(), "\n")
	require.Contains(t, impact, "including local npm test")
	require.Contains(t, impact, "POSIX shell")
	require.Contains(t, impact, ".nyc_output/")
}

func TestSetupDirtyRefusalListsFiles(t *testing.T) {
	root := newJestRepository(t, ordinaryVitestCI)
	writeScripts(t, root, map[string]string{"build": "tsc"})
	output, err := exec.Command("git", "init", "-q", root).CombinedOutput()
	require.NoError(t, err, string(output))
	plan, err := PlanJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", "")
	require.NoError(t, err)
	_, err = plan.Apply(false)
	require.ErrorContains(t, err, "uncommitted changes (.github/")
}

func TestSetupAnchorRefusalNamesItsJobAndLine(t *testing.T) {
	workflow := "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '22'}\n      - run: npx vitest run\n  lint:\n    env: &shared {LANG: C}\n    steps:\n      - run: echo lint\n"
	root := newJestRepository(t, workflow)
	_, err := PlanJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", "")
	require.ErrorContains(t, err, "/ lint / line 8: YAML anchors or aliases")
}

func TestJestLifecycleHookNamesTheUnsupportedRunner(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	writeScripts(t, root, map[string]string{"pretest": "jest", "test": "node check.js"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "Package lifecycle script pretest runs Jest; Jest validation only follows the main script, not npm pre/post hooks")
}
