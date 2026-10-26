package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

type buildExecutor struct {
	t            *testing.T
	commands     []string
	paths        []string
	directories  []string
	nodeOptions  []string
	environments []map[string]string
	arguments    [][]string
	fail         bool
}

func (e *buildExecutor) CombinedOutput(_ context.Context, command string, args []string, env map[string]string) ([]byte, error) {
	e.environments = append(e.environments, env)
	e.arguments = append(e.arguments, args)
	e.commands = append(e.commands, filepath.Base(command))
	e.paths = append(e.paths, command)
	e.nodeOptions = append(e.nodeOptions, env["NODE_OPTIONS"])
	require.Equal(e.t, "false", env["DD_TRACE_ENABLED"])
	require.Equal(e.t, "false", env["DD_CIVISIBILITY_ENABLED"])
	require.NotContains(e.t, env["NODE_OPTIONS"], "dd-trace")
	if e.fail {
		return []byte("missing build input"), errors.New("build failed")
	}
	return nil, nil
}

func TestBuildPrerequisiteExecutionAndCheckOnly(t *testing.T) {
	for _, checkOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "execution", true: "check-only"}[checkOnly], func(t *testing.T) {
			executor := &buildExecutor{t: t}
			root := t.TempDir()
			t.Chdir(root)
			installFakeBuildTool(t, root, "tsc")
			installFakeBuildTool(t, root, "tsup")
			drive := Testdrive{repositoryRoot: root, executor: executor, checkOnly: checkOnly, allowReviewedBuilds: true}
			item := configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc"}, {Directory: ".", Command: "tsup"}}}}
			require.NoError(t, drive.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item))
			if checkOnly {
				require.Empty(t, executor.commands)
				require.Nil(t, item.Preparation[0].ExitCode)
				require.Equal(t, "not exercised", item.Preparation[0].Status)
			} else {
				require.Equal(t, []string{"tsc", "tsup"}, executor.commands)
				for _, build := range item.Preparation {
					require.Equal(t, "passed", build.Status)
					require.Equal(t, 0, *build.ExitCode)
				}
			}
		})
	}
}

func TestFailedBuildStopsConfigurationBeforeTestsAndRetainsEvidence(t *testing.T) {
	drive := preparedTestdrive(t)
	root := drive.repositoryRoot
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"tsc && jest"}}`)
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	executor := &buildExecutor{t: t, fail: true}
	drive.executor = executor
	installFakeBuildTool(t, root, "tsc")
	err := drive.runAllConfigurations(t.Context(), &bytes.Buffer{})
	require.ErrorContains(t, err, "build prerequisite tsc")
	require.Equal(t, []string{"tsc"}, executor.commands, "no preflight, tracer installation or tests after setup fails")
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var result validationResult
	require.NoError(t, json.Unmarshal(data, &result))
	require.False(t, result.Success)
	require.Equal(t, "not selected", result.Tracer, "failed preparation must not claim a tracer was selected")
	require.Equal(t, "INCOMPLETE", result.Summary.Status)
	require.Equal(t, "blocked", result.Configurations[0].Status)
	require.Equal(t, "not exercised", result.Configurations[0].Compatibility.Status)
	require.Empty(t, result.Runs)
	require.Empty(t, result.Features)
	require.Equal(t, "failed", result.Configurations[0].Preparation[0].Status)
	require.Contains(t, result.Configurations[0].Preparation[0].Diagnostic, "missing build input")
}

func TestBuildReviewAndCancellationDoNotExecute(t *testing.T) {
	for _, build := range []onboard.BuildCommand{
		{Directory: "app", Command: "tsup"}, {Directory: ".", Command: "MODE=production tsup"},
		{Directory: ".", Command: "tsup --watch"}, {Directory: ".", Command: "tsup src/*.ts"},
	} {
		drive := Testdrive{repositoryRoot: t.TempDir()}
		item := configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{build}}}
		require.Error(t, drive.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item))
		require.Nil(t, item.Preparation[0].ExitCode)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	drive := Testdrive{repositoryRoot: t.TempDir()}
	item := configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsup"}}}}
	require.ErrorIs(t, drive.runBuildPrerequisites(ctx, &bytes.Buffer{}, &item), context.Canceled)
}

func TestBareTestCommandCannotCertifyRequiredBuild(t *testing.T) {
	root := t.TempDir()
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "jest", Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsup"}}}}}
	result := validationResult{Framework: "jest", Preflight: &jestPreflight{Command: "jest"}, Compatibility: verdict{Status: "compatible"}, Features: []featureResult{{Name: "retries", Status: "passed"}}}
	setSingleConfigurationScope(root, &result, scope, result.Preflight.Command, nil)
	require.Equal(t, "not exercised", result.Configurations[0].Status)
	prepareValidationResult(root, &result)
	require.False(t, result.Success)
}

func (e *buildExecutor) CombinedOutputInDirectory(ctx context.Context, directory, name string, args []string, env map[string]string) ([]byte, error) {
	e.directories = append(e.directories, directory)
	return e.CombinedOutput(ctx, name, args, env)
}

func TestPackageBuildUsesItsLocalCompilerAndEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("NODE_OPTIONS", "--max-old-space-size=4096")
	compiler := filepath.Join(root, "app", "node_modules", ".bin", "tsc")
	require.NoError(t, os.MkdirAll(filepath.Dir(compiler), 0755))
	requireWriteFile(t, compiler, "")
	executor := &buildExecutor{t: t}
	installFakeBuildTool(t, root, "tsc")
	drive := Testdrive{repositoryRoot: root, executor: executor}
	item := configurationResult{TestCommand: onboard.TestCommand{Environment: map[string]string{"NODE_OPTIONS": "--max-old-space-size=1024"}, Prerequisites: []onboard.BuildCommand{{Directory: "app", Command: "tsc", Environment: map[string]string{"NODE_OPTIONS": "--max-old-space-size=2048"}}}}}
	require.NoError(t, drive.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item))
	canonicalCompiler, err := filepath.EvalSymlinks(compiler)
	require.NoError(t, err)
	require.Equal(t, []string{canonicalCompiler}, executor.paths)
	directory, err := filepath.EvalSymlinks(filepath.Join(root, "app"))
	require.NoError(t, err)
	require.Equal(t, []string{directory}, executor.directories)
	require.Equal(t, []string{"--max-old-space-size=2048"}, executor.nodeOptions)
}

func TestBuildRebasesCheckoutAndPreservesEmptyNodeOptions(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("GITHUB_WORKSPACE", "/customer/checkout")
	t.Setenv("NODE_OPTIONS", "--conditions=development")
	executor := &buildExecutor{t: t}
	installFakeBuildTool(t, root, "tsc")
	drive := Testdrive{repositoryRoot: root, executor: executor}
	item := configurationResult{TestCommand: onboard.TestCommand{Environment: map[string]string{"NODE_OPTIONS": ""}, Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc", Environment: map[string]string{"NODE_OPTIONS": ""}}}}}
	require.NoError(t, drive.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item))
	require.Equal(t, root, executor.environments[0]["GITHUB_WORKSPACE"])
	require.Empty(t, executor.nodeOptions[0])
	require.Equal(t, "/customer/checkout", os.Getenv("GITHUB_WORKSPACE"))
}

func TestPatchTargetsUseBuildWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	packageDir := filepath.Join(root, "packages", "app")
	require.NoError(t, os.MkdirAll(packageDir, 0700))
	requireWriteFile(t, filepath.Join(packageDir, "jest.config.js"), "src src\nsrc")
	requireWriteFile(t, filepath.Join(root, "jest.config.js"), "src root")
	executor := &buildExecutor{t: t}
	drive := Testdrive{repositoryRoot: root, executor: executor}
	item := configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{{Directory: "packages/app", Command: "sed -i~ s/src/dist/ jest.config.js", Kind: "patch"}}}}
	require.NoError(t, drive.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item))
	data, err := os.ReadFile(filepath.Join(packageDir, "jest.config.js"))
	require.NoError(t, err)
	require.Equal(t, "dist src\ndist", string(data))
	data, err = os.ReadFile(filepath.Join(root, "jest.config.js"))
	require.NoError(t, err)
	require.Equal(t, "src root", string(data))
	require.Empty(t, executor.commands, "literal patches run in Go on every platform")
}

func TestZeroConfigBundlersRunWithoutReviewAndConfiguredOnesNameTheirConfig(t *testing.T) {
	root := t.TempDir()
	review, _ := preparationNeedsReview(root, onboard.BuildCommand{Command: "unbuild"})
	require.False(t, review, "zero-config unbuild only bundles the package sources")
	review, _ = preparationNeedsReview(root, onboard.BuildCommand{Command: "tsup"})
	require.False(t, review)
	review, found := preparationNeedsReview(root, onboard.BuildCommand{Command: "rollup -c"})
	require.True(t, review, "rollup always runs customer configuration")
	require.Contains(t, found, "--config")

	requireWriteFile(t, filepath.Join(root, "build.config.ts"), "export default {}\n")
	review, found = preparationNeedsReview(root, onboard.BuildCommand{Command: "unbuild"})
	require.True(t, review)
	require.Equal(t, "build.config.ts", found)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"tsup":{"entry":["src/index.ts"]}}`)
	review, found = preparationNeedsReview(root, onboard.BuildCommand{Command: "tsup"})
	require.True(t, review)
	require.Equal(t, `package.json "tsup" settings`, found)
}
