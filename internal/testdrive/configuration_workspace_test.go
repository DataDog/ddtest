package testdrive

import (
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/stretchr/testify/require"
)

func TestIsolatedCIEnvironmentUsesCopiedCheckout(t *testing.T) {
	t.Setenv("GITHUB_WORKSPACE", "/original/checkout")
	t.Setenv("CI_PROJECT_DIR", "/original/gitlab")
	t.Setenv("GITHUB_SHA", "unchanged-commit")
	environment := map[string]string{"CIRCLE_WORKING_DIRECTORY": "/original/circle", "CI_PROJECT_DIR": "", "CUSTOM_SETTING": "preserved"}
	copyRoot := t.TempDir()
	result := isolatedCIEnvironment(environment, "/original/circle", copyRoot)
	require.Equal(t, copyRoot, result["GITHUB_WORKSPACE"])
	require.Equal(t, copyRoot, result["CIRCLE_WORKING_DIRECTORY"])
	require.Empty(t, result["CI_PROJECT_DIR"], "an explicit empty override remains empty")
	require.Equal(t, "preserved", result["CUSTOM_SETTING"])
	require.NotContains(t, result, "GITHUB_SHA", "commit/provider metadata must remain inherited")
	require.Equal(t, "/original/checkout", os.Getenv("GITHUB_WORKSPACE"))
	require.Equal(t, "/original/circle", environment["CIRCLE_WORKING_DIRECTORY"])
}

func TestIsolatedCIEnvironmentRebasesEveryProviderCheckout(t *testing.T) {
	names := environment.CIWorkspaceEnvironmentVariables()
	for _, name := range names {
		t.Setenv(name, "/original/checkout")
	}
	copyRoot := t.TempDir()
	result := isolatedCIEnvironment(nil, "/original/checkout", copyRoot)
	for _, name := range names {
		require.Equal(t, copyRoot, result[name], name)
		overrides := map[string]string{name: ""}
		require.Empty(t, isolatedCIEnvironment(overrides, "/original/checkout", copyRoot)[name], "explicit empty override for %s", name)
		require.Equal(t, "/original/checkout", os.Getenv(name))
	}
}

func TestConfigurationNodeOptionsOverrideInheritedShellFlags(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--max-old-space-size=4096")
	for _, value := range []string{"--max-old-space-size=1024", ""} {
		drive := Testdrive{configurationEnvironment: map[string]string{"NODE_OPTIONS": value}}
		require.Equal(t, value, drive.nodeOptions())
		options := drive.javascriptEnvironment("/tracer/ci/init.js")["NODE_OPTIONS"]
		require.NotContains(t, options, "4096")
		if value != "" {
			require.Contains(t, options, value)
		}
	}
	drive := Testdrive{}
	require.Equal(t, "--max-old-space-size=4096", drive.nodeOptions(), "a focused local command retains its actual shell settings")
}
