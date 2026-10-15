package framework

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"

	"github.com/stretchr/testify/require"
)

func TestWebdriverIORunPreservesCommandAndSelectsOnlyRequestedFiles(t *testing.T) {
	executor := &cucumberCommandExecutor{}
	runner := NewWebdriverIO(&testPlatform{})
	runner.executor = executor
	runner.commandOverride = []string{"npm", "run", "browser"}
	require.NoError(t, runner.RunTests(context.Background(), []string{"selected.spec.js"}, map[string]string{}))
	require.Equal(t, "npm", executor.capturedName)
	require.Equal(t, []string{"run", "browser"}, executor.capturedArgs)
	var files []string
	require.NoError(t, json.Unmarshal([]byte(executor.capturedEnv["DDTEST_WDIO_FILES"]), &files))
	require.Equal(t, []string{"selected.spec.js"}, files)
	require.Contains(t, executor.capturedEnv["NODE_OPTIONS"], "webdriverio_observer.mjs")
}

type webdriverIODiscoveryExecutor struct{ root string }

func (e webdriverIODiscoveryExecutor) CombinedOutput(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	body, _ := json.Marshal(WebdriverIOConfig{Files: []string{filepath.Join(e.root, "click", "example.js")}})
	return nil, os.WriteFile(env["DDTEST_WDIO_INSPECT"], body, 0600)
}
func (e webdriverIODiscoveryExecutor) Run(context.Context, string, []string, map[string]string) error {
	return nil
}
func TestWebdriverIODiscoveryKeepsConfiguredSpecPaths(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Mkdir(filepath.Join(root, "click"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "click/example.js"), []byte(""), 0600))
	runner := NewWebdriverIO(&testPlatform{})
	runner.executor = webdriverIODiscoveryExecutor{root: root}
	files, err := runner.DiscoverTestFiles(t.Context(), discovery.TestFileSet{})
	require.NoError(t, err)
	require.Equal(t, []string{"click/example.js"}, files)
}
