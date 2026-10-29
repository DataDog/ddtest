package testdrive

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRebasePytestPathsPreservesExpressionsAndNodeSelectors(t *testing.T) {
	root, copied := t.TempDir(), t.TempDir()
	file := filepath.Join(root, "test_app.py")
	config := filepath.Join(root, "pytest.ini")
	requireWriteFile(t, file, "")
	requireWriteFile(t, config, "")
	outside := filepath.Join(t.TempDir(), "outside.py")
	args := []string{"-k", file, "-o", "addopts=" + config, "-c", config, "--config-file=" + config, "-c" + config, "--", file + "::test_app[a/b]", outside}
	wantFile, wantConfig := filepath.Join(copied, "test_app.py"), filepath.Join(copied, "pytest.ini")
	want := []string{"-k", file, "-o", "addopts=" + config, "-c", wantConfig, "--config-file=" + wantConfig, "-c" + wantConfig, "--", wantFile + "::test_app[a/b]", outside}
	require.Equal(t, want, rebasePytestPaths(args, root, copied))
}

func TestPytestAbsolutePathsExecuteOnlyInsideCopy(t *testing.T) {
	python := feedbackPython(t)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, exec.CommandContext(t.Context(), "git", "init", "-q").Run())
	requireWriteFile(t, "pytest.ini", "[pytest]\npythonpath = .\n")
	requireWriteFile(t, "app.py", "def add(a,b):\n    return a+b\n")
	require.NoError(t, os.Mkdir("tests", 0755))
	file := filepath.Join(root, "tests", "test_app.py")
	requireWriteFile(t, file, "from pathlib import Path\nfrom app import add\ndef test_app():\n    Path(__file__).with_suffix('.executed').write_text('ran')\n    assert add(1, 2) == 3\n")
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://example.com/ddtest-python-fixture.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "0123456789012345678901234567890123456789")
	t.Setenv("DD_GIT_BRANCH", "main")
	t.Setenv("DD_GIT_COMMIT_MESSAGE", "pytest fixture")
	drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), command: python, args: []string{"-m", "pytest", "-c", filepath.Join(root, "pytest.ini"), file + "::test_app"}, executor: &ext.DefaultCommandExecutor{}, startIntake: func(dir string, scenario intake.Scenario) (localIntake, error) {
		return intake.StartScenario(dir, scenario)
	}}
	drive.preflight = drive.frameworkPreflight()
	var output bytes.Buffer
	result, err := drive.runPreparedConfiguration(t.Context(), &output, &configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Environment: map[string]string{"PYTEST_ADDOPTS": "--rootdir=" + root}}})
	require.NoError(t, err, output.String())
	require.Equal(t, "compatible", result.Compatibility.Status, output.String())
	require.Len(t, result.Features, 6)
	for _, feature := range result.Features {
		require.Equal(t, "passed", feature.Status, "%s: %s", feature.Name, feature.Reason)
	}
	require.NoFileExists(t, filepath.Join(root, "tests", "test_app.executed"))
}
