package planner

import (
	"context"
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/stretchr/testify/require"
)

func TestSuiteSelectionKeepsNewAndUnskippableFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("tests", 0755))
	for _, name := range []string{"test_old.py", "test_new.py", "test_marked.py"} {
		content := "raise AssertionError('must not import')\n"
		if name == "test_marked.py" {
			content += "# datadog_itr_unskippable\n"
		}
		require.NoError(t, os.WriteFile("tests/"+name, []byte(content), 0644))
	}
	client := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		Skippables: api.Skippables{Suites: api.SkippableSuites{
			{Module: "tests", Suite: "test_old.py"}:     true,
			{Module: "tests", Suite: "test_marked.py"}:  true,
			{Module: "tests", Suite: "test_deleted.py"}: true,
		}},
		Durations: map[string]map[string]api.TestSuiteDurationInfo{"tests": {
			"test_old.py":     {SourceFile: "tests/test_old.py"},
			"test_marked.py":  {SourceFile: "tests/test_marked.py"},
			"test_deleted.py": {SourceFile: "tests/test_deleted.py"},
		}},
	}
	fw := &collectionCountingPytest{Framework: framework.NewPytest(nil)}
	selected, err := selectSuiteFiles(t.Context(), []string{"tests/test_old.py", "tests/test_new.py", "tests/test_marked.py"}, fw, client)
	require.NoError(t, err)
	require.Equal(t, []string{"tests/test_marked.py", "tests/test_new.py"}, selected.RunnableFiles)
	require.Equal(t, 1, selected.UnskippableSuites)
	require.Zero(t, fw.collections.Load())
	require.NoDirExists(t, ".testoptimization")
}

func TestSuiteSelectionWithoutSettingsRunsAllFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	files := []string{"tests/test_new.py"}
	result, err := selectSuiteFiles(t.Context(), files, framework.NewPytest(nil), &MockTestOptimizationClient{})
	require.NoError(t, err)
	require.Equal(t, files, result.RunnableFiles)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = selectSuiteFiles(ctx, files, framework.NewPytest(nil), nil)
	require.ErrorIs(t, err, context.Canceled)
}
