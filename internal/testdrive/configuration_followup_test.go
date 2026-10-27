package testdrive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
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

func TestConfigurationsReplayGitHubActionsDefaults(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	installFakeBuildTool(t, root, "tsc")
	for _, key := range []string{"CI", "GITHUB_ACTIONS"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"defaults", nil, "true"},
		{"explicit override", map[string]string{"CI": "false"}, "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The test step: --all inspects the configuration with its CI environment.
			executor := &fakeTestdriveExecutor{err: errors.New("stop after inspection")}
			drive := Testdrive{repositoryRoot: root, executor: executor, checkOnly: true, framework: framework.NewJest(platform.NewJavaScript()), command: filepath.Join(root, "jest-cli"),
				preflight: func(context.Context, io.Writer, *validationResult) error { return nil }}
			item := configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Command: "jest", Environment: tc.env}}
			_, err := drive.runPreparedConfiguration(t.Context(), &bytes.Buffer{}, &item)
			require.Error(t, err)
			require.Equal(t, tc.want, executor.env["CI"])
			require.Equal(t, "true", executor.env["GITHUB_ACTIONS"])
			require.Equal(t, tc.env, item.Environment, "the reported configuration keeps only explicit values")

			// Its preparation step.
			builds := &buildExecutor{t: t}
			preparation := Testdrive{repositoryRoot: root, executor: builds}
			item = configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc", Environment: tc.env}}}}
			require.NoError(t, preparation.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item))
			require.Equal(t, tc.want, builds.environments[0]["CI"])
			require.Equal(t, "true", builds.environments[0]["GITHUB_ACTIONS"])
		})
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
	require.Equal(t, "true", drive.configurationEnvironment["CI"], "a discovered CI configuration runs with GitHub's defaults")
	require.Equal(t, "true", drive.configurationEnvironment["GITHUB_ACTIONS"])
	scope, err := onboard.DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	result := completeValidationFixture("jest")
	setSingleConfigurationScope(root, &result, scope, drive.scopeCommand, drive.configurationEnvironment)
	require.Len(t, result.Configurations, 1)
	require.Equal(t, "passed", result.Configurations[0].Status)
	// Explicit local assignments win over CI defaults and remain a scope finding.
	local := "TZ=Europe/Madrid npm test"
	env := matchingConfigurationEnvironment(scope, nil, root, "jest", local)
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
		require.ErrorContains(t, validateConfigurationFiles(root, root, args, nil, nil), "outside checkout")
	}
}

func TestCypressInlineConfigIsNotAConfigurationFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "cypress.config.js")
	requireWriteFile(t, outside, "module.exports={}")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked.config.js")))
	cypress := frameworkFeaturePolicies["cypress"].configurationOptions()
	for _, args := range [][]string{{"run", "--config", "retries=2"}, {"run", "--config=retries=2,video=false"}, {"run", "-c", "retries=2"}, {"run", "-cretries=2"}} {
		require.NoError(t, validateConfigurationFiles(root, root, args, nil, cypress), args)
	}
	for _, args := range [][]string{{"run", "--config-file", "linked.config.js"}, {"run", "--config-file=linked.config.js"}, {"run", "-C", "linked.config.js"}, {"run", "-Clinked.config.js"}} {
		require.ErrorContains(t, validateConfigurationFiles(root, root, args, nil, cypress), "outside checkout", args)
	}
	jest := frameworkFeaturePolicies["jest"].configurationOptions()
	require.ErrorContains(t, validateConfigurationFiles(root, root, []string{"--config", "linked.config.js"}, nil, jest), "outside checkout")
	require.NoError(t, validateConfigurationFiles(root, root, []string{"--config-file", "linked.config.js"}, nil, jest), "--config-file is not a Jest option")
}

func TestNpxCIStepIsExercisedByDefaultAndCommandRuns(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"jest":"30.5.1"}}`)
	requireWriteFile(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  test:\n    steps:\n      - run: npx jest u.test.js\n")
	scope, err := onboard.DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	// Default and --command runs execute the same step with testdrive's --no-install.
	for _, executed := range []string{"npx --no-install jest u.test.js", "npx jest u.test.js"} {
		result := completeValidationFixture("jest")
		setSingleConfigurationScope(root, &result, scope, executed, nil)
		require.Len(t, result.Configurations, 1)
		require.Equal(t, "passed", result.Configurations[0].Status, executed)
	}
}

func TestTracerDescriptionMarksAMissingSource(t *testing.T) {
	require.Equal(t, "not selected", tracerDescription(validationResult{Tracer: "not selected"}))
	require.Equal(t, "dd-trace@5.132.0 · project", tracerDescription(validationResult{Tracer: "dd-trace@5.132.0", TracerSource: "project"}))
	// A run blocked before tracer resolution shows the request, not a version.
	require.Equal(t, "dd-trace@latest · not resolved", tracerDescription(validationResult{Tracer: "dd-trace@latest"}))
}

func TestUnpreparedConfigurationCopyKeepsIgnoredInputs(t *testing.T) {
	source := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", source).Run())
	requireWriteFile(t, filepath.Join(source, ".gitignore"), "dist/\n.testoptimization/\n")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "dist"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(source, ".testoptimization"), 0700))
	requireWriteFile(t, filepath.Join(source, "dist", "lib.js"), "module.exports=1")
	requireWriteFile(t, filepath.Join(source, ".testoptimization", "testdrive.json"), "{}")
	requireWriteFile(t, filepath.Join(source, "lib.test.js"), "require('./dist/lib.js')")

	kept := filepath.Join(t.TempDir(), "kept")
	require.NoError(t, workspace.Copy(t.Context(), source, kept, nil))
	require.FileExists(t, filepath.Join(kept, "dist", "lib.js"), "a locally compiled input imported by tests")
	require.NoDirExists(t, filepath.Join(kept, ".testoptimization"))

	rebuilt := filepath.Join(t.TempDir(), "rebuilt")
	require.NoError(t, workspace.Copy(t.Context(), source, rebuilt, skipIgnoredOutput))
	require.NoDirExists(t, filepath.Join(rebuilt, "dist"), "preparation rebuilds ignored outputs")
}

func TestPreparedConfigurationCopyKeepsIgnoredDependencies(t *testing.T) {
	source := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", source).Run())
	requireWriteFile(t, filepath.Join(source, ".gitignore"), "node_modules/\n.yarn/\n.pnp.cjs\n.env.local\n")
	inputs := []string{"node_modules/pkg/index.js", ".yarn/install-state.gz", ".pnp.cjs", ".env.local"}
	for _, path := range inputs {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, path)), 0700))
		requireWriteFile(t, filepath.Join(source, path), path)
	}

	target := filepath.Join(t.TempDir(), "rebuilt")
	require.NoError(t, workspace.Copy(t.Context(), source, target, skipIgnoredOutput))
	for _, path := range inputs {
		require.FileExists(t, filepath.Join(target, path), "ignored install state is a test input")
	}
}

func TestFocusedVitestRunnerCopyKeepsPreparedInputs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", root).Run())
	requireWriteFile(t, filepath.Join(root, ".gitignore"), "dist/\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dist"), 0700))
	const ignoredSize = 1 << 16
	requireWriteFile(t, filepath.Join(root, "dist", "lib.js"), strings.Repeat("x", ignoredSize))
	requireWriteFile(t, filepath.Join(root, "lib.test.js"), "import '../dist/lib.js'")
	installFakeBuildTool(t, root, "tsc")
	t.Chdir(root)
	isolated := func(item configurationResult) int {
		drive := Testdrive{repositoryRoot: root, executor: &fakeTestdriveExecutor{}, framework: framework.NewVitest(platform.NewJavaScript()),
			command: "ddtest", args: []string{"run", "--framework", "vitest"}, discoveryError: errors.New("stop after isolation"), nodeVersion: func() string { return "v24.0.0" },
			preflight: func(context.Context, io.Writer, *validationResult) error { return nil }}
		var output bytes.Buffer
		_, _ = drive.runPreparedConfiguration(t.Context(), &output, &item)
		var size int
		_, err := fmt.Sscanf(output.String()[strings.Index(output.String(), "Isolating configuration:"):], "Isolating configuration: up to %d bytes", &size)
		require.NoError(t, err, output.String())
		return size
	}
	require.GreaterOrEqual(t, isolated(configurationResult{TestCommand: onboard.TestCommand{Directory: "."}}), ignoredSize, "no preparation recreates dist/")
	require.Less(t, isolated(configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc"}}}}), ignoredSize)
}
