package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/stretchr/testify/require"
)

// writeSplitFileTestsPlan writes the plan's tests of split files in a new
// current directory.
func writeSplitFileTestsPlan(t *testing.T, listed map[string][]string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	data, err := json.Marshal(listed)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(constants.SplitFileTestsPath), 0o755))
	require.NoError(t, os.WriteFile(constants.SplitFileTestsPath, data, 0o644))
	return dir
}

func TestSelectSplitFileTests(t *testing.T) {
	listed := map[string][]string{
		"tests/test_long.py": {"tests/test_long.py::test_a", "tests/test_long.py::test_b", "tests/test_long.py::TestC::test_m"},
	}
	dir := writeSplitFileTestsPlan(t, listed)
	long := filepath.Join(dir, "tests/test_long.py")

	for _, tt := range []struct {
		name           string
		lines          []string
		args           []string
		filters        map[string]map[string][]string
		onlySplitFiles bool
	}{
		{
			name:           "last part drops the tests of the other parts",
			lines:          []string{"tests/test_long.py"},
			args:           []string{"tests/test_long.py"},
			filters:        map[string]map[string][]string{long: {"drop": listed["tests/test_long.py"]}},
			onlySplitFiles: true,
		},
		{
			name:           "other parts keep their tests, passed as the file",
			lines:          []string{"tests/test_long.py::test_a", "tests/test_other.py", "tests/test_long.py::TestC::test_m"},
			args:           []string{"tests/test_long.py", "tests/test_other.py"},
			filters:        map[string]map[string][]string{long: {"keep": {"tests/test_long.py::test_a", "tests/test_long.py::TestC::test_m"}}},
			onlySplitFiles: false,
		},
		{
			name:           "last part keeps the tests of parts in the same batch",
			lines:          []string{"tests/test_long.py::test_b", "tests/test_long.py"},
			args:           []string{"tests/test_long.py"},
			filters:        map[string]map[string][]string{long: {"drop": {"tests/test_long.py::test_a", "tests/test_long.py::TestC::test_m"}}},
			onlySplitFiles: true,
		},
		{
			name:           "last part with all other parts drops nothing",
			lines:          []string{"tests/test_long.py::test_a", "tests/test_long.py::test_b", "tests/test_long.py::TestC::test_m", "tests/test_long.py"},
			args:           []string{"tests/test_long.py"},
			filters:        map[string]map[string][]string{long: {"drop": {}}},
			onlySplitFiles: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			selection, err := selectSplitFileTests(tt.lines)
			require.NoError(t, err)
			require.Equal(t, tt.args, selection.args)
			require.Equal(t, tt.filters, selection.filters)
			require.Equal(t, tt.onlySplitFiles, selection.onlySplitFiles)
		})
	}
}

func TestSelectSplitFileTestsLeavesLinesAloneWithoutSplitFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	lines := []string{"tests/test_a.py", "tests/test_b.py::test_x"}

	selection, err := selectSplitFileTests(lines)

	require.NoError(t, err)
	require.Equal(t, lines, selection.args)
	require.Empty(t, selection.filters)
}

func TestSelectSplitFileTestsRejectsAnUnreadablePlan(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Dir(constants.SplitFileTestsPath), 0o755))
	require.NoError(t, os.WriteFile(constants.SplitFileTestsPath, []byte("{"), 0o644))

	_, err := selectSplitFileTests([]string{"tests/test_a.py"})

	require.ErrorContains(t, err, "parse split file tests")
}

func TestPrependEnvList(t *testing.T) {
	t.Setenv("DDTEST_TEST_LIST", "/inherited")
	require.Equal(t, "/new:/set", prependEnvList(map[string]string{"DDTEST_TEST_LIST": "/set"}, "DDTEST_TEST_LIST", ":", "/new"))
	require.Equal(t, "/new:/inherited", prependEnvList(map[string]string{}, "DDTEST_TEST_LIST", ":", "/new"))
	require.Equal(t, "/new", prependEnvList(map[string]string{"DDTEST_TEST_LIST": ext.UnsetEnvironment}, "DDTEST_TEST_LIST", ":", "/new"))
	require.Equal(t, "/new", prependEnvList(map[string]string{"DDTEST_TEST_LIST": ""}, "DDTEST_TEST_LIST", ":", "/new"))
}

// splitFilterExecutor stands in for pytest: it records the run and, when
// apply is set, marks the filter applied as the plugin does.
type splitFilterExecutor struct {
	mockCommandExecutor
	apply   bool
	args    []string
	filters map[string]map[string][]string
	env     map[string]string
}

func (e *splitFilterExecutor) Run(ctx context.Context, name string, args []string, env map[string]string) error {
	e.args, e.env = args, env
	if path := env[splitFilterEnvVar]; path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &e.filters); err != nil {
			return err
		}
		if e.apply {
			if err := os.WriteFile(path+".applied", nil, 0o600); err != nil {
				return err
			}
		}
		plugin, err := os.ReadFile(filepath.Join(filepath.Dir(path), splitFilterModule+".py"))
		if err != nil || string(plugin) != pytestSplitFilterPlugin {
			return fmt.Errorf("plugin not beside the filters: %v", err)
		}
	}
	return e.err
}

func TestPyTest_RunTests_FiltersSplitFiles(t *testing.T) {
	dir := writeSplitFileTestsPlan(t, map[string][]string{"tests/test_long.py": {"tests/test_long.py::test_a"}})
	t.Setenv("PYTHONPATH", "/project/src")
	t.Setenv("PYTEST_PLUGINS", "")
	executor := &splitFilterExecutor{apply: true}
	pytest := &PyTest{executor: executor, platform: &testPlatform{}}

	require.NoError(t, pytest.RunTests(t.Context(), []string{"tests/test_long.py", "tests/test_other.py"}, nil))

	require.Equal(t, []string{"-m", "pytest", "tests/test_long.py", "tests/test_other.py"}, executor.args)
	require.Equal(t, map[string]map[string][]string{
		filepath.Join(dir, "tests/test_long.py"): {"drop": {"tests/test_long.py::test_a"}},
	}, executor.filters)
	require.Equal(t, splitFilterModule, executor.env["PYTEST_PLUGINS"])
	pythonPath := strings.Split(executor.env["PYTHONPATH"], string(os.PathListSeparator))
	require.Equal(t, []string{filepath.Dir(executor.env[splitFilterEnvVar]), "/project/src"}, pythonPath)
	_, err := os.Stat(filepath.Dir(executor.env[splitFilterEnvVar]))
	require.True(t, os.IsNotExist(err), "the filter's directory outlives the run")
}

func TestPyTest_RunTests_LeavesRunsWithoutSplitFilesAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	executor := &splitFilterExecutor{}
	pytest := &PyTest{executor: executor, platform: &testPlatform{}}

	require.NoError(t, pytest.RunTests(t.Context(), []string{"tests/test_a.py::test_x"}, nil))

	require.Equal(t, []string{"-m", "pytest", "tests/test_a.py::test_x"}, executor.args)
	require.NotContains(t, executor.env, splitFilterEnvVar)
	require.NotContains(t, executor.env, "PYTEST_PLUGINS")
}

func TestPyTest_RunTests_FailsWhenTheSplitFilterDidNotLoad(t *testing.T) {
	writeSplitFileTestsPlan(t, map[string][]string{"tests/test_long.py": {"tests/test_long.py::test_a"}})
	pytest := &PyTest{executor: &splitFilterExecutor{apply: false}, platform: &testPlatform{}}

	err := pytest.RunTests(t.Context(), []string{"tests/test_long.py"}, nil)

	require.ErrorContains(t, err, "without ddtest's split filter plugin")
}

func TestPyTest_RunTests_EmptyBatchOfSplitFiles(t *testing.T) {
	writeSplitFileTestsPlan(t, map[string][]string{"tests/test_long.py": {"tests/test_long.py::test_a"}})
	cmd := exec.Command(os.Args[0], "-test.run=^TestPyTest_ExitHelper$")
	cmd.Env = append(os.Environ(), "DDTEST_PYTEST_EXIT_HELPER=5")
	noTests := cmd.Run()
	require.Error(t, noTests)

	for _, tt := range []struct {
		lines   []string
		wantErr bool
	}{
		{lines: []string{"tests/test_long.py::test_a"}, wantErr: false},
		{lines: []string{"tests/test_long.py::test_a", "tests/test_other.py"}, wantErr: true},
	} {
		pytest := &PyTest{
			executor: &splitFilterExecutor{apply: true, mockCommandExecutor: mockCommandExecutor{err: noTests}},
			platform: &testPlatform{},
		}
		err := pytest.RunTests(t.Context(), tt.lines, nil)
		if tt.wantErr {
			require.ErrorIs(t, err, noTests, "lines %v", tt.lines)
		} else {
			require.NoError(t, err, "lines %v", tt.lines)
		}
	}
}
