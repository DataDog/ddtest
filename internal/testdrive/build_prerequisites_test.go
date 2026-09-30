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
	t        *testing.T
	commands []string
	fail     bool
}

func (e *buildExecutor) CombinedOutput(_ context.Context, command string, args []string, env map[string]string) ([]byte, error) {
	e.commands = append(e.commands, filepath.Base(command))
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
			drive := Testdrive{repositoryRoot: t.TempDir(), executor: executor, checkOnly: checkOnly}
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
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"tsup && jest"}}`)
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	executor := &buildExecutor{t: t, fail: true}
	drive.executor = executor
	err := drive.runAllConfigurations(t.Context(), &bytes.Buffer{})
	require.ErrorContains(t, err, "build prerequisite tsup")
	require.Equal(t, []string{"tsup"}, executor.commands, "no preflight, tracer installation or tests after setup fails")
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var result validationResult
	require.NoError(t, json.Unmarshal(data, &result))
	require.False(t, result.Success)
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
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "vitest run", Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsup"}}}}}
	result := validationResult{Framework: "vitest", Preflight: &jestPreflight{Command: "vitest run"}, Compatibility: verdict{Status: "compatible"}, Features: []featureResult{{Name: "retries", Status: "passed"}}}
	setSingleConfigurationScope(root, &result, scope)
	require.Equal(t, "not exercised", result.Configurations[0].Status)
	prepareValidationResult(root, &result)
	require.False(t, result.Success)
}
