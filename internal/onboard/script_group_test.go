package onboard

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestParallelScriptDiscoveryKeepsBuildsInTheirOwnBranch(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "npm-run-all --print-label --parallel lint:* --parallel test:*", "lint:js": "eslint .", "test:unit": "vitest run", "pretest:integration": "npm run build", "test:integration": "cucumber-js features", "build": "rollup -c"})
	remaining := 256
	result := resolveJestSequence(root, root, "npm test", nil, &remaining, "cucumber")
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	require.True(t, result.OtherFramework)
	require.NotEmpty(t, result.Tests[0].LifecyclePrerequisites)
	require.Equal(t, "rollup -c", result.Tests[0].LifecyclePrerequisites[0].Command)
}
func TestParallelBuildDoesNotBecomeATestPrerequisite(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"build": "rollup -c", "test": "cucumber-js features"})
	remaining := 256
	result := resolveJestSequence(root, root, "npm-run-all --parallel build test", nil, &remaining, "cucumber")
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	require.Empty(t, result.Tests[0].Prerequisites)
}

func TestScriptSelectorsRespectHierarchyAndManifestOrder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"build:z":"tsc --outDir z","build:a":"tsc --outDir a","build:a:nested":"tsc --outDir nested"}}`), 0600))
	remaining := 256
	result := resolveJestSequence(root, root, "run-s build:*", nil, &remaining, "cucumber")
	require.Empty(t, result.Reason)
	require.Len(t, result.Builds, 2)
	require.Equal(t, "tsc --outDir z", result.Builds[0].Command)
	require.Equal(t, "tsc --outDir a", result.Builds[1].Command)
}

func TestCucumberLoggingPrefixAndOrchestratedLifecycleBuild(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"prebuild": "run-s clean", "clean": "rimraf ./lib", "build": "npm-run-all --print-label --parallel build:*", "build:js": "rollup --config", "pretest:integration": "run-s build", "test:integration": "run-s 'test:integration:base -- --profile noWip'", "test:integration:base": `DEBUG="any,$DEBUG" cucumber-js test/integration`})
	remaining := 256
	result := resolveJestSequence(root, root, "npm run test:integration", nil, &remaining, "cucumber")
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	require.Empty(t, result.Tests[0].UnvalidatedSetup)
	require.Equal(t, "npm run test:integration", result.Tests[0].Command)
	require.NotEmpty(t, result.Tests[0].LifecyclePrerequisites)
	require.Equal(t, "npm run build", result.Tests[0].LifecyclePrerequisites[0].Command)
	require.Equal(t, PreparationPackageBuild, result.Tests[0].LifecyclePrerequisites[0].Kind)
	probe, err := CucumberProbeCommand(root, result.Tests[0].Command)
	require.NoError(t, err)
	require.Contains(t, probe, "--profile noWip")
}
