package onboard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestSetupPlanPreviewsExactEditsWithoutWriting(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "color.ui")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	root := newJestRepository(t, ordinaryVitestCI)
	writeScripts(t, root, map[string]string{"build": "tsc"})
	plan, err := PlanJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	diff, err := plan.Diff()
	require.NoError(t, err)
	require.NotContains(t, diff, "\x1b[")
	require.Contains(t, diff, `--- "a/.github/workflows/test.yml"`)
	require.Contains(t, diff, "+/.testoptimization/")
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, ordinaryVitestCI, string(data))
	require.NoFileExists(t, filepath.Join(root, ".gitignore"))
	changes, err := plan.Apply(false)
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	// The printed reverse diff is itself executable and restores original bytes.
	command := exec.Command("git", "apply", "-R", "--whitespace=nowarn")
	command.Dir = root
	command.Stdin = strings.NewReader(diff)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	data, err = os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, ordinaryVitestCI, string(data))
	require.NoFileExists(t, filepath.Join(root, ".gitignore"))
}

func TestSetupPlanRefusesDirtyTargetsAndRechecksOriginals(t *testing.T) {
	root := newJestRepository(t, ordinaryVitestCI)
	writeScripts(t, root, map[string]string{"build": "tsc"})
	output, err := exec.Command("git", "init", "-q", root).CombinedOutput()
	require.NoError(t, err, string(output))
	plan, err := PlanJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", "")
	require.NoError(t, err)
	_, err = plan.Apply(false)
	require.ErrorContains(t, err, "uncommitted changes")
	require.NoFileExists(t, filepath.Join(root, ".gitignore"))
	path := filepath.Join(root, ".github/workflows/test.yml")
	require.NoError(t, os.WriteFile(path, []byte("customer edited after preview\n"), 0600))
	_, err = plan.Apply(true)
	require.ErrorContains(t, err, "changed during planning")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "customer edited after preview\n", string(data))
}

func TestSetupPlanRollsBackEarlierFilesAfterRenameFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "first")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0600))
	plan := SetupPlan{root: root, edits: []setupEdit{{path: "first", before: []byte("original"), after: []byte("updated"), mode: 0600}, {path: "second", after: []byte("created"), mode: 0644}}}
	calls := 0
	_, err := plan.apply(true, func(a, b string) error {
		calls++
		if calls == 2 {
			return errors.New("injected write failure")
		}
		return os.Rename(a, b)
	})
	require.ErrorContains(t, err, "write second")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
	require.NoFileExists(t, filepath.Join(root, "second"))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".ddtest-setup-"))
	}
}

func TestApplyRefusesReusableWorkflowBeforeAnyEdits(t *testing.T) {
	for _, event := range []string{"workflow_call", "[push, workflow_call]", "\n  workflow_call:\n    secrets:\n      DD_API_KEY: {required: true}"} {
		workflow := "on: " + event + "\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: npx vitest run\n"
		root := newJestRepository(t, workflow)
		changes, err := applyVitestSetup(root, "6.20.1")
		require.ErrorContains(t, err, "Reusable workflows are applied only when on.workflow_call.secrets declares DD_API_KEY")
		require.Empty(t, changes)
		data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
		require.NoError(t, err)
		require.Equal(t, workflow, string(data))
		require.NoFileExists(t, filepath.Join(root, ".gitignore"))
	}
}

func TestApplyReusableWorkflowRequiresEveryLocalCallerToForwardTheKey(t *testing.T) {
	callee := "on:\n  workflow_call:\n    secrets:\n      DD_API_KEY: {required: true}\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: npx vitest run\n"
	for secrets, forwarded := range map[string]bool{"inherit": true, "{DD_API_KEY: '${{ secrets.DD_API_KEY }}'}": true, "{OTHER: x}": false} {
		t.Run(secrets, func(t *testing.T) {
			root := newJestRepository(t, callee)
			caller := "on: push\njobs:\n  call:\n    uses: ./.github/workflows/test.yml\n    secrets: " + secrets + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/caller.yml"), []byte(caller), 0600))
			_, err := ApplyJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
			if !forwarded {
				require.ErrorContains(t, err, "every caller in this repository passes it")
				return
			}
			require.NoError(t, err)
			data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			require.Contains(t, string(data), "api_key: ${{ secrets.DD_API_KEY }}")
		})
	}
}

func TestApplyMochaESMPartitionsByRegistrationMinimum(t *testing.T) {
	workflow := "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix: {node: ['18.0.0','18.18.0','18.19.0','20.0.0','20.6.0','24.14.1']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npx nyc -- mocha test.mjs\n"
	root := newJestRepository(t, workflow)
	writeScripts(t, root, map[string]string{"test": "mocha test.mjs"})
	_, err := ApplyJavaScriptSetup(root, "mocha", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	var generated ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &generated))
	steps := generated.Jobs["test"].Steps
	for version, want := range map[string]bool{"18.0.0": false, "18.18.0": false, "18.19.0": true, "20.0.0": false, "20.6.0": true, "24.14.1": true} {
		active, err := runtimeCondition(steps[1].If, map[string]any{"node": version})
		require.NoError(t, err)
		require.Equal(t, want, active, version)
	}
}

func TestApplyFallbackPreservesOptionsWithoutActionOutputs(t *testing.T) {
	root := newJestRepository(t, strings.Replace(ordinaryVitestCI, "    runs-on: ubuntu-latest", "    runs-on: ubuntu-latest\n    env:\n      NODE_OPTIONS: --conditions=customer", 1))
	writeScripts(t, root, map[string]string{"build": "tsc"})
	_, err := applyVitestSetup(root, "6.20.1")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	var generated ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &generated))
	job := generated.Jobs["test"]
	step := job.Steps[4]
	for _, available := range []bool{false, true} {
		options, err := effectiveBootstrapOptions(generated, job, step, nil, available)
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(options, "--conditions=customer"))
		if available {
			require.Contains(t, options, actionPreload)
		} else {
			require.Equal(t, "--conditions=customer", options)
		}
	}
	require.Contains(t, string(data), "continue-on-error: true")
}

func TestApplyLifecycleHookOwnsScopedInitialization(t *testing.T) {
	root := newJestRepository(t, "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: npm test\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"pretest":"cucumber-js features","test":"echo done"},"devDependencies":{"@cucumber/cucumber":"12.2.0","nyc":"17.1.0"}}`), 0600))
	_, err := ApplyJavaScriptSetup(root, "cucumber", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	scope, err := DiscoverValidationScope(root, "cucumber")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.NotEmpty(t, scope.Commands)
	require.True(t, scope.Commands[0].cucumberScoped)
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"pretest":"NODE_OPTIONS=`)
	require.Contains(t, string(data), `"test":"echo done"`)
	changes, err := ApplyJavaScriptSetup(root, "cucumber", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	require.Empty(t, changes)
}
