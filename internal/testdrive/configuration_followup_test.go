package testdrive

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestExplicitNonProviderWorkspaceValuesArePreserved(t *testing.T) {
	original, copy := t.TempDir(), t.TempDir()
	overrides := map[string]string{"WORKSPACE": "fixtures", "CI_PROJECT_DIR": "/other/fixtures", "GITHUB_WORKSPACE": original}
	actual := isolatedCIEnvironment(overrides, original, copy)
	require.Equal(t, "fixtures", actual["WORKSPACE"])
	require.Equal(t, "/other/fixtures", actual["CI_PROJECT_DIR"])
	require.Equal(t, copy, actual["GITHUB_WORKSPACE"])
	require.Equal(t, original, overrides["GITHUB_WORKSPACE"])
}

func TestPreparationDoesNotInheritLaterTestEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("NODE_ENV", "parent")
	t.Setenv("ONLY_IN_TEST", "")
	t.Setenv("GITHUB_WORKSPACE", root)
	compiler := filepath.Join(root, "node_modules/.bin/tsc")
	require.NoError(t, os.MkdirAll(filepath.Dir(compiler), 0700))
	require.NoError(t, os.WriteFile(compiler, []byte("#!/bin/sh\nprintf '%s:%s:%s' \"$NODE_ENV\" \"${ONLY_IN_TEST:-unset}\" \"$WORKSPACE\"\nprintf copy > \"$GITHUB_WORKSPACE/marker\"\nexit 1\n"), 0700))
	for _, configured := range []bool{false, true} {
		buildEnv := map[string]string{"WORKSPACE": "fixtures"}
		want := "parent:unset:fixtures"
		if configured {
			buildEnv["NODE_ENV"] = "build"
			want = "build:unset:fixtures"
		}
		drive := Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{}}
		item := configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Environment: map[string]string{"NODE_ENV": "test", "ONLY_IN_TEST": "later"}, Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc", Environment: buildEnv}}}}
		_, err := drive.runPreparedConfiguration(t.Context(), &bytes.Buffer{}, &item)
		require.Error(t, err)
		require.Contains(t, item.Preparation[0].Diagnostic, want)
		require.NoFileExists(t, filepath.Join(root, "marker"))
	}
}

func TestLiteralPatchKeepsEscapedAmpersands(t *testing.T) {
	for _, tc := range []struct{ script, want string }{
		{`s/old/\&/`, "&"}, {`s/old/&/`, "old"}, {`s/old/\&&/`, "&old"}, {`s/old/\&x/g`, "&x"},
	} {
		root := t.TempDir()
		file := filepath.Join(root, "input")
		requireWriteFile(t, file, "old")
		require.NoError(t, applyLiteralPatch(root, root, []string{"sed", "-i", tc.script, "input"}))
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, tc.want, string(data))
	}
}

func TestForwardedScriptKeepsScopeAndCIEnvironment(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"eslint . && jest"},"devDependencies":{"jest":"30.5.1"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm test\n        env: {TZ: UTC}\n")
	viper.Reset()
	settings.Init()
	viper.Set("framework", "jest")
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	drive, err := Prepare(PrepareOptions{})
	require.NoError(t, err)
	require.Equal(t, "sh", drive.command)
	require.Equal(t, "npm test", drive.scopeCommand)
	require.Equal(t, "UTC", drive.configurationEnvironment["TZ"])
	scope, err := onboard.DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	result := completeValidationFixture("jest")
	setSingleConfigurationScope(root, &result, scope, drive.scopeCommand, drive.configurationEnvironment)
	require.Len(t, result.Configurations, 1)
	require.Equal(t, "passed", result.Configurations[0].Status)
	// Explicit local assignments win over CI defaults and remain a scope finding.
	local := "TZ=Europe/Madrid npm test"
	env := matchingConfigurationEnvironment(root, "jest", local)
	require.Equal(t, "Europe/Madrid", env["TZ"])
	result = completeValidationFixture("jest")
	setSingleConfigurationScope(root, &result, scope, local, env)
	require.Equal(t, "not exercised", result.Configurations[0].Status)
	require.Equal(t, []string{"TZ"}, result.Configurations[0].EnvironmentDifferences)
}

func TestPreparationResolvesInstalledPnPCompiler(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	root := t.TempDir()
	t.Chdir(root)
	packageRoot := filepath.Join(root, "cache/typescript")
	require.NoError(t, os.MkdirAll(filepath.Join(packageRoot, "bin"), 0700))
	requireWriteFile(t, filepath.Join(packageRoot, "package.json"), `{"bin":{"tsc":"bin/tsc"}}`)
	requireWriteFile(t, filepath.Join(packageRoot, "bin/tsc"), `require('fs').writeFileSync('compiled',process.argv[2]);console.log('compiled via PnP')`)
	loader := fmt.Sprintf(`const M=require('module'),resolve=M._resolveFilename;M._resolveFilename=function(r,...args){if(r==='typescript/package.json')return %q;return resolve.call(this,r,...args)}`, filepath.Join(packageRoot, "package.json"))
	requireWriteFile(t, filepath.Join(root, ".pnp.cjs"), loader)
	drive := Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{}}
	item := configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc expected"}}}}
	require.NoError(t, drive.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item))
	require.Equal(t, "passed", item.Preparation[0].Status)
	data, err := os.ReadFile(filepath.Join(root, "compiled"))
	require.NoError(t, err)
	require.Equal(t, "expected", string(data))
	require.NoDirExists(t, filepath.Join(root, "node_modules"))
}

func TestConfigurationGuardIncludesJestShortAliases(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "config.js")
	requireWriteFile(t, outside, "module.exports={}")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked.js")))
	for _, args := range [][]string{{"--config", "linked.js"}, {"--config=linked.js"}, {"-c", "linked.js"}, {"-c=linked.js"}, {"-clinked.js"}} {
		require.ErrorContains(t, validateConfigurationFiles(root, root, args, nil), "outside checkout")
	}
}
