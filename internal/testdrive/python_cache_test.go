package testdrive

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestPytestPairedRunsPreserveStartingCache(t *testing.T) {
	python := feedbackPython(t)
	for _, mode := range []string{"default", "custom", "clear"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, "pytest.ini", "[pytest]\n")
			requireWriteFile(t, "test_app.py", "def test_now_passing():\n    pass\ndef test_still_failing():\n    assert False\ndef test_not_previously_failed():\n    pass\n")
			cache := ".pytest_cache"
			args := []string{"-m", "pytest", "--lf"}
			if mode == "custom" {
				cache = "custom cache"
				args = append(args, "-o", "cache_dir="+filepath.Join(root, cache))
			}
			wantCount := 2
			if mode == "clear" {
				args = append(args, "--cache-clear")
				wantCount = 3
			}
			cacheFile := filepath.Join(root, cache, "v", "cache", "lastfailed")
			require.NoError(t, os.MkdirAll(filepath.Dir(cacheFile), 0755))
			seed := "{\"test_app.py::test_now_passing\": true, \"test_app.py::test_still_failing\": true}"
			requireWriteFile(t, cacheFile, seed)
			session := workspace.PlanSession()
			require.NoError(t, session.Create())
			t.Cleanup(func() { require.NoError(t, session.Close()) })
			drive := &Testdrive{repositoryRoot: root, command: python, args: args, executor: &ext.DefaultCommandExecutor{}, startIntake: func(dir string, scenario intake.Scenario) (localIntake, error) {
				return intake.StartScenario(dir, scenario)
			}}
			for _, spec := range []runSpec{{Name: "baseline", Role: roleBaseline}, {Name: "reporting-only", Role: roleReporting, Instrumented: true}, {Name: "repeat", Role: roleRepeat, Instrumented: true}} {
				run, err := drive.runPytest(t.Context(), io.Discard, session, "", spec)
				require.NoError(t, err)
				require.Empty(t, run.ResultError, run.commandOutput)
				require.Len(t, run.Tests, wantCount, run.commandOutput)
				contents, err := os.ReadFile(cacheFile)
				require.NoError(t, err)
				require.Equal(t, seed, string(contents), spec.Name)
			}
		})
	}
}
