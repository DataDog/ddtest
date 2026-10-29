package testdrive

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func installFakeBuildTool(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, "node_modules", ".bin", name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	requireWriteFile(t, path, "")
}
func TestScopeAppliesUnambiguousCIEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TZ", "different")
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  test:\n    steps:\n      - run: npm test\n        env: {TZ: UTC}\n")
	scope, err := onboard.DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	env, _ := matchingConfigurationEnvironment(scope, nil, root, "jest", "npm test")
	require.Equal(t, map[string]string{"TZ": "UTC"}, env)
	result := completeValidationFixture("jest")
	setSingleConfigurationScope(root, &result, scope, "npm test", env)
	require.Equal(t, "passed", result.Configurations[0].Status)
	var out bytes.Buffer
	require.NoError(t, finishValidation(&out, root, result))
	require.True(t, readValidationReport(t, root).Success)
	result = completeValidationFixture("jest")
	setSingleConfigurationScope(root, &result, scope, "npm test", nil)
	require.Equal(t, []string{"TZ"}, result.Configurations[0].EnvironmentDifferences)
	require.Equal(t, "not exercised", result.Configurations[0].Status)
}
func TestConfigurationErrorCannotProducePassedStatus(t *testing.T) {
	result := completeValidationFixture("jest")
	for _, cause := range []error{errors.New("cleanup failed"), errors.New("HTML publication failed")} {
		item := configurationResult{}
		completeConfigurationItem(&item, result, cause)
		require.Equal(t, "incomplete", item.Status)
		require.Equal(t, cause.Error(), item.Error)
	}
}
func TestFocusedExitAgreesWithSavedSuccess(t *testing.T) {
	for _, fail := range []bool{false, true} {
		result := focusedValidationFixture("jest")
		if fail {
			result.Cleanup.Status = "failed"
		}
		root := t.TempDir()
		err := finishValidation(&bytes.Buffer{}, root, result)
		require.Equal(t, !fail, err == nil)
		require.Equal(t, err == nil, readValidationReport(t, root).Success)
	}
}
func TestCopySkipsIgnoredOutputsPreservesDependenciesAndTimes(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	command := exec.Command("git", "init", "-q", source)
	require.NoError(t, command.Run())
	requireWriteFile(t, filepath.Join(source, ".gitignore"), "cache/\nnode_modules/\n")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "cache"), 0700))
	requireWriteFile(t, filepath.Join(source, "cache", "huge"), strings.Repeat("x", 4096))
	installFakeBuildTool(t, source, "tsc")
	file := filepath.Join(source, "input.js")
	requireWriteFile(t, file, "original")
	stamp := time.Unix(1700000000, 0)
	require.NoError(t, os.Chtimes(file, stamp, stamp))
	require.NoError(t, os.Symlink("missing", filepath.Join(source, "node_modules", "dangling")))
	size, err := configurationCopyEstimate(t.Context(), source, skipIgnoredOutput)
	require.NoError(t, err)
	require.Greater(t, size, int64(0))
	require.NoError(t, workspace.Copy(t.Context(), source, target, skipIgnoredOutput))
	require.NoDirExists(t, filepath.Join(target, "cache"))
	require.FileExists(t, filepath.Join(target, "node_modules", ".bin", "tsc"))
	link, err := os.Readlink(filepath.Join(target, "node_modules", "dangling"))
	require.NoError(t, err)
	require.Equal(t, "missing", link)
	info, err := os.Stat(filepath.Join(target, "input.js"))
	require.NoError(t, err)
	require.Equal(t, stamp, info.ModTime())
	requireWriteFile(t, filepath.Join(target, "input.js"), "copy changed")
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
	require.ErrorContains(t, checkConfigurationSpace(target, 1<<62), "insufficient disk space")
}
func TestPreparationRequiresOptInAndReportsMissingTool(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, build := range []onboard.BuildCommand{
		{Directory: ".", Command: "webpack"},
		{Directory: ".", Command: "pnpm run build", Kind: onboard.PreparationPackageBuild},
		{Directory: ".", Command: "pnpm add -D package@1.0.0", Kind: onboard.PreparationDependencies},
	} {
		executor := &buildExecutor{t: t}
		drive := Testdrive{repositoryRoot: root, executor: executor}
		item := configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{build}}}
		require.ErrorContains(t, drive.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item), "--allow-reviewed-builds")
		require.Equal(t, "preparation_review_required", item.Preparation[0].Code)
		require.Empty(t, executor.commands)
	}
	drive := Testdrive{repositoryRoot: root, executor: &buildExecutor{t: t}}
	item := configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc"}}}}
	require.ErrorContains(t, drive.runBuildPrerequisites(t.Context(), &bytes.Buffer{}, &item), "tool not installed")
	require.Equal(t, "tool_not_installed", item.Preparation[0].Code)
}
func TestLiteralPatchIsPortableAndCannotFollowEscapingTargets(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/a"), 0700))
	file := filepath.Join(root, "packages/a/input.js")
	requireWriteFile(t, file, "src/src\nsrc\n")
	require.NoError(t, applyLiteralPatch(root, filepath.Dir(file), []string{"sed", "-i", "s#src#lib#g", "input.js"}))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "lib/lib\nlib\n", string(data))
	require.NoError(t, applyLiteralPatch(root, filepath.Dir(file), []string{"sed", "-i~", "1s/lib/first/", "input.js"}))
	data, err = os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "first/lib\nlib\n", string(data))
	outside := filepath.Join(t.TempDir(), "outside")
	requireWriteFile(t, outside, "preserve")
	require.NoError(t, os.Symlink(outside, filepath.Join(filepath.Dir(file), "outside")))
	require.ErrorContains(t, applyLiteralPatch(root, filepath.Dir(file), []string{"sed", "-i", "s/preserve/bad/", "outside"}), "escapes")
	data, err = os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
	require.ErrorContains(t, applyLiteralPatch(root, filepath.Dir(file), []string{"sed", "-i", "s/.*/bad/", "input.js"}), "regular expression")
}
func TestRealBuildRebasesWorkspaceAndTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture; shared executor directory/deadline has portable coverage")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("GITHUB_WORKSPACE", root)
	path := filepath.Join(root, "node_modules/.bin/tsc")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	script := "#!/bin/sh\nprintf copied > \"$GITHUB_WORKSPACE/marker\"\ncat \"$GITHUB_WORKSPACE/marker\"\nexit 2\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0700))
	drive := Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{Timeout: 5 * time.Second}}
	item := configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc"}}}}
	_, err := drive.runPreparedConfiguration(t.Context(), &bytes.Buffer{}, &item)
	require.Error(t, err)
	require.Contains(t, item.Preparation[0].Diagnostic, "copied")
	require.NoFileExists(t, filepath.Join(root, "marker"))
	require.Equal(t, root, os.Getenv("GITHUB_WORKSPACE"))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nsleep 30 &\nwait\n"), 0700))
	drive.executor = &ext.DefaultCommandExecutor{Timeout: 200 * time.Millisecond}
	item = configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "tsc"}}}}
	started := time.Now()
	err = drive.runBuildPrerequisites(context.Background(), &bytes.Buffer{}, &item)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "preparation timed out")
	require.Equal(t, "preparation_timed_out", item.Preparation[0].Code)
	require.Less(t, time.Since(started), 3*time.Second)
}

func TestPackageReportUsesCheckoutSourceRoot(t *testing.T) {
	drive := preparedTestdrive(t)
	root := drive.repositoryRoot
	pkg := filepath.Join(root, "packages/app")
	require.NoError(t, os.MkdirAll(pkg, 0700))
	requireWriteFile(t, filepath.Join(pkg, "one.test.js"), "const sourceMarkerForPackageReport = 1;\n")
	drive.repositoryRoot, drive.configurationRoot = pkg, root
	run := validationRun{Role: roleReporting, root: root, Name: "reporting-only", Facts: intake.Facts{TestEventCount: 1, TestCount: 1, Tests: []intake.Test{{Suite: "suite", Name: "test", Status: "pass", SourceFile: "packages/app/one.test.js", SourceStart: 1, SourceEnd: 1}}}}
	var result validationResult
	require.NoError(t, drive.writeHTMLReport(&bytes.Buffer{}, &result, run))
	data, err := os.ReadFile(htmlReportPath(pkg))
	require.NoError(t, err)
	require.Contains(t, string(data), "sourceMarkerForPackageReport")
	require.NotContains(t, string(data), "Source could not be read")
}
func TestRepeatedReportingModelsRemainVisibleWithoutDoubleCounting(t *testing.T) {
	primary := reportModel{EvidenceRole: roleReporting, EvidenceName: "reporting-only", Configuration: "A", Runtime: reportRuntime{Output: "first output"}, ExecutionCounts: &executionCounts{TestExecutions: 1}}
	repeated := primary
	repeated.EvidenceRole = roleRepeat
	repeated.EvidenceName = "reporting-only-repeat"
	repeated.Runtime.Output = "repeat output"
	root := t.TempDir()
	require.NoError(t, writeConfigurationHTML(&bytes.Buffer{}, root, []reportModel{primary, repeated}))
	page, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Contains(t, string(page), "first output")
	require.Contains(t, string(page), "repeat output")
	require.Contains(t, string(page), "each primary reporting run once")
	require.Contains(t, string(page), "1 reported test executions")
	require.NotContains(t, string(page), "2 reported test executions")
}

func TestSnapshotPreservesUnrelatedLinksButBlocksEscapingConfiguration(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	requireWriteFile(t, filepath.Join(outside, "config.js"), "preserve")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "external")))
	require.NoError(t, validateConfigurationFiles(root, root, nil, []string{"jest.config.js"}, nil))
	require.ErrorContains(t, validateConfigurationFiles(root, root, []string{"--config", "external/config.js"}, nil, nil), "outside checkout")
	require.NoError(t, os.Symlink(filepath.Join(outside, "config.js"), filepath.Join(root, "jest.config.js")))
	require.ErrorContains(t, validateConfigurationFiles(root, root, nil, []string{"jest.config.js"}, nil), "outside checkout")
}
