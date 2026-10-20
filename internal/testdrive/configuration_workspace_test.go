package testdrive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigurationCopyIsIndependentAndRejectsEscapingLinks(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	requireWriteFile(t, filepath.Join(source, "package.json"), "original")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "node_modules/pkg"), 0755))
	requireWriteFile(t, filepath.Join(source, "node_modules/pkg/index.js"), "original dependency")
	require.NoError(t, os.Symlink("pkg", filepath.Join(source, "node_modules/link")))
	require.NoError(t, os.Mkdir(filepath.Join(source, ".testoptimization"), 0755))
	requireWriteFile(t, filepath.Join(source, ".testoptimization/testdrive.json"), "old evidence")
	require.NoError(t, copyConfiguration(t.Context(), source, target))
	requireWriteFile(t, filepath.Join(target, "package.json"), "changed")
	requireWriteFile(t, filepath.Join(target, "node_modules/link/index.js"), "changed dependency")
	for path, expected := range map[string]string{"package.json": "original", "node_modules/pkg/index.js": "original dependency"} {
		data, err := os.ReadFile(filepath.Join(source, path))
		require.NoError(t, err)
		require.Equal(t, expected, string(data))
	}
	require.NoFileExists(t, filepath.Join(target, ".testoptimization/testdrive.json"))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(source, "outside")))
	require.ErrorContains(t, copyConfiguration(t.Context(), source, t.TempDir()), "escapes repository")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, copyConfiguration(ctx, source, t.TempDir()), context.Canceled)
}

func TestIsolatedCIEnvironmentUsesCopiedCheckout(t *testing.T) {
	t.Setenv("GITHUB_WORKSPACE", "/original/checkout")
	t.Setenv("CI_PROJECT_DIR", "/original/gitlab")
	t.Setenv("GITHUB_SHA", "unchanged-commit")
	environment := map[string]string{"CIRCLE_WORKING_DIRECTORY": "/original/circle", "CI_PROJECT_DIR": "", "CUSTOM_SETTING": "preserved"}
	workspace := t.TempDir()
	result := isolatedCIEnvironment(environment, workspace)
	require.Equal(t, workspace, result["GITHUB_WORKSPACE"])
	require.Equal(t, workspace, result["CIRCLE_WORKING_DIRECTORY"])
	require.Empty(t, result["CI_PROJECT_DIR"], "an explicit empty override remains empty")
	require.Equal(t, "preserved", result["CUSTOM_SETTING"])
	require.NotContains(t, result, "GITHUB_SHA", "commit/provider metadata must remain inherited")
	require.Equal(t, "/original/checkout", os.Getenv("GITHUB_WORKSPACE"))
	require.Equal(t, "/original/circle", environment["CIRCLE_WORKING_DIRECTORY"])
}
