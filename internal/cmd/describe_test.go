package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

const describeRuntimeTags = `{"runtime.name":"CPython","runtime.version":"3.12.9","os.platform":"Linux","os.architecture":"x86_64","os.version":"worker"}`

func describeTestSettings(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); settings.Init(); git.ResetCheckout() })
	viper.Set("runtime_tags", describeRuntimeTags)
	settings.Init()
	git.ResetCheckout()
}

func describeTestFile(t *testing.T, name, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0755))
	require.NoError(t, os.WriteFile(name, []byte(contents), 0644))
}

func TestDescribePytestFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	describeTestSettings(t)
	describeTestFile(t, "pytest.ini", "[pytest]\ntestpaths = defaults\npython_files = check_*.py\n")
	for _, name := range []string{"tests/check_one.py", "tests/nested/check_two.py", "tests/test_ignored.py",
		"tests/conftest.py", "tests/.venv/check_dependency.py", "other/check_other.py"} {
		describeTestFile(t, name, "raise RuntimeError('must not import tests')\n")
	}
	files, roots, err := describePytestFiles(context.Background(), []string{"tests/", "tests/../tests/nested/"})
	require.NoError(t, err)
	require.Equal(t, []string{"tests/check_one.py", "tests/nested/check_two.py"}, files)
	require.Equal(t, []string{"tests", "tests/nested"}, roots)
	viper.Set("tests_exclude_pattern", "**/nested/**")
	settings.Init()
	files, _, err = describePytestFiles(context.Background(), []string{"tests"})
	require.NoError(t, err)
	require.Equal(t, []string{"tests/check_one.py"}, files)
}

func TestDescribeRejectsInvalidRootsAndEmptyInventory(t *testing.T) {
	t.Chdir(t.TempDir())
	describeTestSettings(t)
	describeTestFile(t, "empty/helper.py", "")
	for _, root := range []string{"", "../outside", filepath.Join(string(filepath.Separator), "outside"), "missing", "empty/helper.py", "empty"} {
		_, _, err := describePytestFiles(context.Background(), []string{root})
		require.Error(t, err, root)
	}
}

func TestDescribePytestArgv(t *testing.T) {
	actual, err := describePytestArgv([]string{"pytest", "-k", "alpha and not beta", "tests"}, "--ddtrace -m 'fast or smoke'")
	require.NoError(t, err)
	require.Equal(t, []string{"pytest", "--ddtrace", "-m", "fast or smoke", "-k", "alpha and not beta", "tests"}, actual)
	actual, err = describePytestArgv([]string{"python", "-m", "pytest", "tests"}, "--ddtrace")
	require.NoError(t, err)
	require.Equal(t, []string{"python", "-m", "pytest", "--ddtrace", "tests"}, actual)
	for _, args := range [][]string{{}, {"bash", "-c", "pytest"}, {"pytest", "tests", "&&", "publish"}} {
		_, err = describePytestArgv(args, "")
		require.Error(t, err)
	}
	_, err = describePytestArgv([]string{"pytest"}, "'unclosed")
	require.Error(t, err)
}

func TestDescribeRequiresCompleteSuppliedRuntime(t *testing.T) {
	describeTestSettings(t)
	viper.Set("runtime_tags", `{"runtime.name":"CPython"}`)
	settings.Init()
	_, err := describeConfigurations(context.Background(), "", []string{"pytest"})
	require.ErrorContains(t, err, "missing runtime.version")
	viper.Set("runtime_tags", "invalid")
	settings.Init()
	_, err = describeConfigurations(context.Background(), "", []string{"pytest"})
	require.Error(t, err)
}

func TestDescribeExportsCatalogWithoutExecutingTestsOrChangingPlan(t *testing.T) {
	t.Chdir(t.TempDir())
	describeTestSettings(t)
	for _, args := range [][]string{
		{"init", "-q"}, {"remote", "add", "origin", "https://user:password@example.com/org/repo.git"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial"},
	} {
		output, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, string(output))
	}
	describeTestFile(t, "tests/test_one.py", "raise RuntimeError('must not collect tests')\n")
	describeTestFile(t, ".testoptimization/runner/parallel-runners.txt", "saved plan\n")
	t.Setenv("DD_SERVICE", "feature-tests")
	t.Setenv("DD_ENV", "ci")
	t.Setenv("_CI_DD_ENV", "worker-ci")
	t.Setenv("PYTEST_ADDOPTS", "--ddtrace")
	t.Setenv("_DD_CIVISIBILITY_ITR_SUITE_MODE", "true")
	t.Setenv("DD_API_KEY", "must-not-appear")
	root := &cobra.Command{Use: "ddtest"}
	root.PersistentFlags().String("test-skipping-mode", "test", "")
	root.AddCommand(newDescribeCommand())
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"describe", "--id", "feature/environment", "--test-root", "tests", "--output", "catalog.json", "--", "pytest", "tests"})
	require.NoError(t, root.Execute())
	var actual commandDescription
	require.NoError(t, json.Unmarshal(output.Bytes(), &actual))
	require.Equal(t, 2, actual.SchemaVersion)
	require.Equal(t, describedCommand{ID: "feature/environment", Argv: []string{"pytest", "--ddtrace", "tests"}, Cwd: ".", TestRoots: []string{"tests"}}, actual.Command)
	require.Equal(t, []string{"tests/test_one.py"}, actual.CandidateTestFiles)
	require.Equal(t, "feature-tests", actual.TIARequest.Service)
	require.Equal(t, "worker-ci", actual.TIARequest.Env)
	require.Equal(t, "https://example.com/org/repo.git", actual.TIARequest.RepositoryURL)
	require.Len(t, actual.TIARequest.SHA, 40)
	require.Equal(t, settings.TestSkippingLevelSuite, actual.TIARequest.TestLevel)
	require.Equal(t, "worker", actual.TIARequest.Configurations["os.version"])
	require.NotContains(t, output.String(), "must-not-appear")
	require.NotContains(t, output.String(), "password")
	written, err := os.ReadFile("catalog.json")
	require.NoError(t, err)
	require.Equal(t, output.Bytes(), written)
	saved, err := os.ReadFile(".testoptimization/runner/parallel-runners.txt")
	require.NoError(t, err)
	require.Equal(t, "saved plan\n", string(saved))
}

func TestDescribeRequiresExplicitCommandAndIdentity(t *testing.T) {
	for _, args := range [][]string{
		{"describe", "--id", "feature", "--test-root", "tests", "pytest", "tests"},
		{"describe", "--id", "feature", "--test-root", "tests", "--"},
		{"describe", "--test-root", "tests", "--", "pytest", "tests"},
		{"describe", "--id", "", "--test-root", "tests", "--", "pytest", "tests"},
	} {
		root := &cobra.Command{Use: "ddtest", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newDescribeCommand())
		root.SetArgs(args)
		require.Error(t, root.Execute())
	}
}

func TestDescribePytestEntryPointRequiresAnExplicitRuntime(t *testing.T) {
	describeTestSettings(t)
	viper.Set("runtime_tags", "")
	settings.Init()
	_, err := describeConfigurations(context.Background(), "", []string{"pytest"})
	require.ErrorContains(t, err, "--python or complete --runtime-tags")
}

func TestDescribeRuntimeOverrideAvoidsTheCommandInterpreter(t *testing.T) {
	describeTestSettings(t)
	tags, err := describeConfigurations(context.Background(), "", []string{"missing/python", "-m", "pytest"})
	require.NoError(t, err)
	require.Equal(t, "3.12.9", tags["runtime.version"])
}

func TestDescribeGitLabCatalogWithoutWorkerRuntime(t *testing.T) {
	t.Chdir(t.TempDir())
	describeTestSettings(t)
	viper.Set("runtime_tags", "")
	settings.Init()
	for _, args := range [][]string{
		{"init", "-q"}, {"remote", "add", "origin", "https://example.com/org/repo.git"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	describeTestFile(t, "tests/test_one.py", "raise RuntimeError('must not execute')\n")
	describeTestFile(t, "ci.yml", "feature:\n  script: [scripts/run-tests -- --ddtrace]\n  variables:\n    TEST_SUITE: feature\n    TEST_ENVIRONMENTS_1: fixed\n")
	describeTestFile(t, "envs.json", "{\"suites\":[{\"name\":\"feature\",\"venvs\":[{\"hash\":\"fixed\",\"python_version\":\"3.12\",\"command\":\"pytest {cmdargs} tests\"}]}]}")
	describeTestFile(t, ".testoptimization/runner/parallel-runners.txt", "saved plan")
	t.Setenv("PYTEST_ADDOPTS", "--unknown-host-option")
	root := &cobra.Command{Use: "ddtest"}
	root.AddCommand(newDescribeCommand())
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"describe", "--ci-provider", "gitlab", "--ci-config", "ci.yml", "--command-environments", "envs.json", "--output", "catalog.json"})
	require.NoError(t, root.Execute())
	var catalog onboard.PythonCICommandCatalog
	require.NoError(t, json.Unmarshal(output.Bytes(), &catalog))
	require.Len(t, catalog.Jobs, 1)
	require.Len(t, catalog.Jobs[0].Commands, 1)
	entry := catalog.Jobs[0].Commands[0]
	require.Empty(t, entry.Unresolved)
	require.Equal(t, []string{"pytest", "--ddtrace", "tests"}, entry.Argv)
	require.Equal(t, []string{"tests/test_one.py"}, entry.CandidateTestFiles)
	require.Equal(t, "https://example.com/org/repo.git", catalog.RepositoryURL)
	require.Len(t, catalog.SHA, 40)
	require.NotContains(t, output.String(), "runtime.version")
	written, err := os.ReadFile("catalog.json")
	require.NoError(t, err)
	require.Equal(t, output.Bytes(), written)
	saved, err := os.ReadFile(".testoptimization/runner/parallel-runners.txt")
	require.NoError(t, err)
	require.Equal(t, "saved plan", string(saved))
}

func TestDescribeRejectsIncompleteOrMixedCIFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--ci-provider", "gitlab"},
		{"--ci-config", "ci.yml"},
		{"--command-environments", "envs.json"},
		{"--ci-config", "ci.yml", "--command-environments", "envs.json", "--id", "manual"},
		{"--ci-config", "ci.yml", "--command-environments", "envs.json", "--python", "python"},
		{"--ci-config", "ci.yml", "--command-environments", "envs.json", "--", "pytest", "tests"},
		{"--ci-config", "ci.yml", "--command-environments", "envs.json", "--ci-provider", "other"},
	} {
		root := &cobra.Command{Use: "ddtest", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(newDescribeCommand())
		root.SetArgs(append([]string{"describe"}, args...))
		require.Error(t, root.Execute(), args)
	}
}

func TestDescribePreservesCustomConfigurations(t *testing.T) {
	describeTestSettings(t)
	t.Setenv("DD_TAGS", "ordinary:ignored,test.configuration.variant:worker,test.configuration.empty:")
	tags, err := describeConfigurations(context.Background(), "", []string{"pytest"})
	require.NoError(t, err)
	require.Equal(t, "worker", tags["test.configuration.variant"])
	require.Contains(t, tags, "test.configuration.empty")
	require.NotContains(t, tags, "ordinary")
	viper.Set("runtime_tags", `{"runtime.name":"CPython","runtime.version":"3.12.9","os.platform":"Linux","os.architecture":"x86_64","os.version":"worker","test.configuration.variant":"declared"}`)
	settings.Init()
	tags, err = describeConfigurations(context.Background(), "", []string{"pytest"})
	require.NoError(t, err)
	require.Equal(t, "declared", tags["test.configuration.variant"])
}
