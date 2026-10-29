package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func pythonReviewWorkflow(t *testing.T, environment, before, command string) string {
	t.Helper()
	return pythonWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    env: %s
    steps:
%s      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: %s
`, environment, before, command))
}

func TestPythonReviewReplayDropsManagedControls(t *testing.T) {
	root := pythonReviewWorkflow(t, `{DD_CIVISIBILITY_ENABLED: 'false', DD_TRACE_ENABLED: 'false', DD_CIVISIBILITY_AGENTLESS_ENABLED: 'false', DD_AGENT_HOST: '${{ secrets.AGENT }}', DD_TRACE_AGENT_PORT: '8126', DD_API_KEY: '${{ secrets.DD_API_KEY }}', DD_TRACE_AGENT_URL: '${{ secrets.URL }}', DD_CIVISIBILITY_AGENTLESS_URL: '${{ secrets.URL }}', DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED: 'false', DD_ENV: ci}`, "", "python -m pytest")
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	env := scope.Commands[0].Environment
	for _, key := range []string{"DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED", "DD_CIVISIBILITY_AGENTLESS_ENABLED", "DD_AGENT_HOST", "DD_TRACE_AGENT_PORT", "DD_API_KEY", "DD_TRACE_AGENT_URL", "DD_CIVISIBILITY_AGENTLESS_URL"} {
		require.NotContains(t, env, key)
	}
	require.Equal(t, "false", env["DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED"])
	require.Equal(t, "ci", env["DD_ENV"])
}

func TestPythonReviewDisabledVisibilityIsNotCompatible(t *testing.T) {
	for _, layer := range []string{"workflow", "job", "step", "matrix"} {
		t.Run(layer, func(t *testing.T) {
			root := pythonReviewWorkflow(t, "{}", "", "python -m pytest")
			path := filepath.Join(root, ".github/workflows/test.yml")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			text := string(data)
			switch layer {
			case "workflow":
				text = "env: {DD_CIVISIBILITY_ENABLED: 'false'}\n" + text
			case "job":
				text = strings.Replace(text, "env: {}", "env: {DD_CIVISIBILITY_ENABLED: 'false'}", 1)
			case "step":
				text += "        env: {DD_CIVISIBILITY_ENABLED: 'false'}\n"
			case "matrix":
				text = strings.Replace(text, "    env: {}", "    strategy:\n      matrix: {tracing: ['false']}\n    env: {DD_CIVISIBILITY_ENABLED: '${{ matrix.tracing }}'}", 1)
			}
			require.NoError(t, os.WriteFile(path, []byte(text), 0644))
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, "incompatible", check.Status)
			require.Contains(t, check.Jobs[0].Reason, "DD_CIVISIBILITY_ENABLED")
		})
	}
}

func TestPythonReviewConditionalTestsStayUnresolved(t *testing.T) {
	for _, command := range []string{"if [ -f run-tests ]; then python -m pytest; fi", "if [ -f run-tests ]; then python -mpytest; fi"} {
		root := pythonReviewWorkflow(t, "{}", "", command)
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved)
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status)
		require.Len(t, check.Jobs, 1)
	}
}

func TestPythonReviewConditionsDoNotLeakToLaterTests(t *testing.T) {
	for _, command := range []string{"echo docs", "python -m pip --version"} {
		before := "      - if: needs.docs.outputs.ready\n        run: " + command + "\n"
		root := pythonReviewWorkflow(t, "{}", before, "python -m pytest")
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "compatible", check.Status, "%+v", check)
	}
	root := pythonReviewWorkflow(t, "{}", "", "python -m pytest\n        if: needs.tests.outputs.ready\n      - run: python -m pytest other")
	check := pythonRuntimeCheck(t, root)
	require.Len(t, check.Jobs, 2)
	require.Equal(t, "inconclusive", check.Jobs[0].Status)
	require.Equal(t, "compatible", check.Jobs[1].Status)
	for _, action := range []string{"actions/setup-python@v6", "datadog/test-visibility-github-action@v3"} {
		root := pythonReviewWorkflow(t, "{}", "", "python -m pytest")
		path := filepath.Join(root, ".github/workflows/test.yml")
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		text := strings.Replace(string(data), "      - uses: "+action, "      - if: needs.setup.outputs.ready\n        uses: "+action, 1)
		require.NoError(t, os.WriteFile(path, []byte(text), 0644))
		require.Equal(t, "inconclusive", pythonRuntimeCheck(t, root).Status, action)
	}
}

func TestPythonReviewMetadataQueriesAreNotTests(t *testing.T) {
	for _, command := range []string{"pytest --version", "python -m pytest --help", "python -mpytest -h", "uv run --only-group test pytest --version"} {
		root := pythonReviewWorkflow(t, "{}", "      - run: "+command+"\n", "python -m pytest")
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "compatible", check.Status)
		require.Len(t, check.Jobs, 1)
	}
	root := pythonReviewWorkflow(t, "{}", "", "tox -e py312")
	writeFile(t, root, "pyproject.toml", "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS']\ncommands = [['pytest', '--version'], ['pytest']]\n")
	require.Equal(t, "compatible", pythonRuntimeCheck(t, root).Status)
	require.True(t, pythonInvocation([]string{"pytest", "-k", "--help"}))
	require.True(t, pythonInvocation([]string{"pytest", "--", "--help"}))
}

func TestPythonReviewUnknownInterpreterAndIsolationModes(t *testing.T) {
	for _, command := range []string{".venv38/bin/pytest", "/opt/python/bin/pytest", "python -E -m pytest", "python -I -mpytest", "python -bEI -m pytest", "python -W error -E -m pytest"} {
		root := pythonReviewWorkflow(t, "{}", "", command)
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status, "%s: %+v", command, check)
	}
	for _, command := range []string{"python -W I -m pytest", "python -X devE -m pytest"} {
		root := pythonReviewWorkflow(t, "{}", "", command)
		require.Equal(t, "compatible", pythonRuntimeCheck(t, root).Status)
	}
}

func TestPythonReviewWrapperDirectoryStaysUnresolved(t *testing.T) {
	for _, selector := range []string{"--directory sub", "--directory=sub", "--project=sub"} {
		root := pythonReviewWorkflow(t, "{}", "", "uv run "+selector+" tox -e py312")
		writeFile(t, root, "pyproject.toml", "[tool.tox.env_run_base]\ncommands = [['mypy', 'src']]\n")
		writeFile(t, root, "sub/pyproject.toml", "[tool.tox.env_run_base]\ncommands = [['pytest']]\n")
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved)
		require.Empty(t, scope.Commands)
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status)
		require.Len(t, check.Jobs, 1)
	}
}

func TestPythonReviewToxNestedRuntime(t *testing.T) {
	for _, command := range []string{"uv run --python=3.8 pytest", "python3.8 -m pytest", ".venv38/bin/pytest", "python -IE -m pytest"} {
		root := pythonReviewWorkflow(t, "{}", "", "tox -e py312")
		commands, err := json.Marshal([][]string{strings.Fields(command)})
		require.NoError(t, err)
		writeFile(t, root, "pyproject.toml", "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS']\ncommands = "+string(commands)+"\n")
		require.Equal(t, "inconclusive", pythonRuntimeCheck(t, root).Status, command)
	}
}

func TestPythonReviewEffectivePluginEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, command, forwarded, status string }{
		{"direct plugin declaration", "python -m pytest", "", "compatible"},
		{"tox filters both", "tox -e py312", "", "compatible"},
		{"tox forwards autoload only", "tox -e py312", ", 'PYTEST_DISABLE_PLUGIN_AUTOLOAD'", "incompatible"},
		{"tox forwards both", "tox -e py312", ", 'PYTEST_*'", "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonReviewWorkflow(t, "{PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1', PYTEST_PLUGINS: 'ddtrace.testing.internal.pytest.entry_point'}", "", tc.command)
			writeFile(t, root, "pyproject.toml", "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS'"+tc.forwarded+"]\ncommands = [['pytest']]\n")
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
		})
	}
}

func TestPythonReviewFindingsKeepMatrixIdentity(t *testing.T) {
	root := pythonReviewWorkflow(t, "{}", "", "python -m pytest")
	path := filepath.Join(root, ".github/workflows/test.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	text := strings.Replace(string(data), "    env: {}", "    strategy:\n      matrix: {os: [linux, windows], dependency: [old, new]}\n    env: {}", 1)
	require.NoError(t, os.WriteFile(path, []byte(text), 0644))
	check := pythonRuntimeCheck(t, root)
	require.Len(t, check.Jobs, 4)
	locations := map[string]bool{}
	for _, finding := range check.Jobs {
		require.Contains(t, finding.Location, "os:")
		require.Contains(t, finding.Location, "dependency:")
		locations[finding.Location] = true
	}
	require.Len(t, locations, 4)
}

func TestPythonReviewDefiniteFailuresSurviveUnknownConfiguration(t *testing.T) {
	root := pythonReviewWorkflow(t, "{}", "", "python -m pytest -c missing.ini")
	path := filepath.Join(root, ".github/workflows/test.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data), "'3.12'", "'3.8'", 1)), 0644))
	check := pythonRuntimeCheck(t, root)
	require.Equal(t, "incompatible", check.Status)
	require.Contains(t, check.Jobs[0].Reason, "Python 3.8")
	require.Contains(t, check.Jobs[0].Reason, "missing.ini")
}

func TestPythonReviewMinorSelectorsKeepUnknownPatches(t *testing.T) {
	for _, tc := range []struct{ python, requirement, status string }{
		{"3.9", ">=3.9.1", "inconclusive"},
		{"3.9", "<3.9.7", "inconclusive"},
		{"3.9", "<=3.9.0", "inconclusive"},
		{"3.9", "==3.9", "inconclusive"},
		{"3.9", "<=3.9", "inconclusive"},
		{"3.9", ">3.9", "inconclusive"},
		{"3.9", "!=3.9.2", "inconclusive"},
		{"3.9", ">=3.9.0", "compatible"},
		{"3.10", ">=3.9.1,<3.15", "compatible"},
		{"3.8", ">=3.9.1", "incompatible"},
		{"3.9.10", ">=3.9.1", "compatible"},
		{"3.9.0", ">=3.9.1", "incompatible"},
	} {
		status, _ := ComparePythonRequirement(tc.python, tc.requirement)
		require.Equal(t, tc.status, status, "%s %s", tc.python, tc.requirement)
	}
}

func TestPythonPluginOptionsUseFinalBlockedState(t *testing.T) {
	for _, tc := range []struct{ options, command, status string }{
		{"-p no:ddtrace", "pytest -p ddtrace", "compatible"},
		{"-pno:ddtrace", "pytest -pddtrace", "compatible"},
		{"-p ddtrace", "pytest -p no:ddtrace", "incompatible"},
		{"-p no:ddtrace", "pytest -p ddtrace -p no:ddtrace", "incompatible"},
	} {
		root := pythonReviewWorkflow(t, "{}", "", tc.command)
		writeFile(t, root, "pytest.ini", "[pytest]\naddopts = "+tc.options+"\n")
		require.Equal(t, tc.status, pythonRuntimeCheck(t, root).Status, "%+v", tc)
	}
}

func TestPythonPluginEnvironmentRequiresModuleNames(t *testing.T) {
	for _, tc := range []struct{ declared, command, status string }{
		{"ddtrace", "pytest", "inconclusive"},
		{"ddtrace", "pytest -p ddtrace", "compatible"},
		{"ddtrace.contrib.pytest.plugin", "pytest", "compatible"},
		{"ddtrace.contrib.internal.pytest.plugin", "pytest", "compatible"},
		{"ddtrace.testing.internal.pytest.entry_point", "pytest", "compatible"},
	} {
		root := pythonReviewWorkflow(t, "{PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1', PYTEST_PLUGINS: '"+tc.declared+"'}", "", tc.command)
		require.Equal(t, tc.status, pythonRuntimeCheck(t, root).Status, "%+v", tc)
	}
}

func TestPythonArgumentFilesRemainUnverified(t *testing.T) {
	for _, tc := range []struct{ command, environment, config string }{
		{"pytest @ci-args.txt", "{}", ""},
		{"pytest -- @ci-args.txt", "{}", ""},
		{"pytest", "{PYTEST_ADDOPTS: '@ci-args.txt'}", ""},
		{"pytest", "{}", "[pytest]\naddopts = @ci-args.txt\n"},
	} {
		root := pythonReviewWorkflow(t, tc.environment, "", tc.command)
		writeFile(t, root, "ci-args.txt", "--no-ddtrace\n")
		if tc.config != "" {
			writeFile(t, root, "pytest.ini", tc.config)
		}
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status, "%+v", tc)
		require.Contains(t, check.Jobs[0].Reason, "argument files")
	}
	root := pythonReviewWorkflow(t, "{}", "", "tox -e py312")
	writeFile(t, root, "pyproject.toml", "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS']\ncommands = [['pytest', '@ci-args.txt']]\n")
	require.Equal(t, "inconclusive", pythonRuntimeCheck(t, root).Status)
	_, _, err := PytestCommandOptions("--ddtrace", []string{"pytest", "-o", "addopts="}, []string{"@overridden.txt"})
	require.NoError(t, err)
}

func TestPythonActionPrependsInheritedAddopts(t *testing.T) {
	for _, layer := range []string{"workflow", "job"} {
		root := pythonReviewWorkflow(t, "{}", "", "pytest")
		path := filepath.Join(root, ".github/workflows/test.yml")
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		text := string(data)
		if layer == "workflow" {
			text = "env: {PYTEST_ADDOPTS: '-q -k selected'}\n" + text
		} else {
			text = strings.Replace(text, "env: {}", "env: {PYTEST_ADDOPTS: '-q -k selected'}", 1)
		}
		require.NoError(t, os.WriteFile(path, []byte(text), 0644))
		require.Equal(t, "compatible", pythonRuntimeCheck(t, root).Status, layer)
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Equal(t, "--ddtrace -q -k selected", scope.Commands[0].Environment["PYTEST_ADDOPTS"])
	}
}

func TestPythonLiteralCommandEnvironmentIsScoped(t *testing.T) {
	root := pythonReviewWorkflow(t, "{COVERAGE_FILE: '.coverage-job'}", "", "|\n          COVERAGE_FILE='.coverage-unit' python -m pytest tests/unit\n          COVERAGE_FILE='.coverage-integration' pytest tests/integration\n          python -m pytest tests/other")
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 3)
	for i, expected := range []string{".coverage-unit", ".coverage-integration", ".coverage-job"} {
		require.Equal(t, expected, scope.Commands[i].Environment["COVERAGE_FILE"])
		require.NotContains(t, scope.Commands[i].Command, "COVERAGE_FILE=")
	}
	require.Equal(t, "compatible", pythonRuntimeCheck(t, root).Status)
	for _, tc := range []struct{ command, status string }{
		{"PYTEST_ADDOPTS='--no-ddtrace' pytest", "incompatible"},
		{"PYTEST_DISABLE_PLUGIN_AUTOLOAD=1 pytest", "incompatible"},
		{"PYTEST_DISABLE_PLUGIN_AUTOLOAD=1 PYTEST_PLUGINS=ddtrace.testing.internal.pytest.entry_point pytest", "compatible"},
		{"COVERAGE_FILE=.coverage python -E -m pytest", "inconclusive"},
		{"UV_PYTHON=3.8 uv run pytest", "inconclusive"},
		{"PYTHONPATH=/other pytest", "inconclusive"},
		{"DD_CIVISIBILITY_ENABLED=false pytest", "inconclusive"},
		{"COVERAGE_FILE=.coverage python -m pytest --version", "not applicable"},
		{"COVERAGE_FILE=.coverage tox -e py312", "compatible"},
	} {
		root := pythonReviewWorkflow(t, "{}", "", tc.command)
		writeFile(t, root, "pyproject.toml", "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS']\ncommands = [['pytest']]\n")
		require.Equal(t, tc.status, pythonRuntimeCheck(t, root).Status, tc.command)
	}
	root = pythonReviewWorkflow(t, "{}", "", "COVERAGE_FILE=.coverage pytest\n        shell: pwsh")
	scope, err = DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
	require.Equal(t, "inconclusive", pythonRuntimeCheck(t, root).Status)
}

func TestPythonUnresolvedActionLanguagesStayInconclusive(t *testing.T) {
	root := pythonReviewWorkflow(t, "{}", "", "pytest")
	path := filepath.Join(root, ".github/workflows/test.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data), "languages: python", "languages: '${{ vars.DD_LANGUAGES }}'", 1)), 0644))
	check := pythonRuntimeCheck(t, root)
	require.Equal(t, "inconclusive", check.Status)
	require.Len(t, check.Jobs, 1)
	require.Contains(t, check.Jobs[0].Reason, "DD_LANGUAGES")
	require.NotEqual(t, "missing_instrumentation", check.Jobs[0].Code)
}

func TestPythonCollectionOnlyCommandsAreNotTestRuns(t *testing.T) {
	for _, command := range []string{"pytest --collect-only", "python -m pytest --co", "uv run pytest --collect-only"} {
		root := pythonReviewWorkflow(t, "{}", "      - run: "+command+"\n", "pytest")
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "compatible", check.Status)
		require.Len(t, check.Jobs, 1)
	}
	root := pythonReviewWorkflow(t, "{}", "", "tox -e py312")
	writeFile(t, root, "pyproject.toml", "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS']\ncommands = [['pytest', '--collect-only'], ['pytest']]\n")
	require.Equal(t, "compatible", pythonRuntimeCheck(t, root).Status)
	require.True(t, pythonInvocation([]string{"pytest", "-k", "--collect-only"}))
	require.True(t, pythonInvocation([]string{"pytest", "--", "--co"}))
}

func TestPythonInheritedHomeIsUnverified(t *testing.T) {
	for _, layer := range []string{"workflow", "job", "step"} {
		root := pythonReviewWorkflow(t, "{}", "", "python -m pytest")
		path := filepath.Join(root, ".github/workflows/test.yml")
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		text := string(data)
		switch layer {
		case "workflow":
			text = "env: {PYTHONHOME: '/other/python'}\n" + text
		case "job":
			text = strings.Replace(text, "env: {}", "env: {PYTHONHOME: '/other/python'}", 1)
		case "step":
			text += "        env: {PYTHONHOME: '/other/python'}\n"
		}
		require.NoError(t, os.WriteFile(path, []byte(text), 0644))
		require.Equal(t, "inconclusive", pythonRuntimeCheck(t, root).Status, layer)
	}
	for _, command := range []string{"pytest", "uv run pytest", "tox -e py312"} {
		root := pythonReviewWorkflow(t, "{PYTHONHOME: '/other/python'}", "", command)
		writeFile(t, root, "pyproject.toml", "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS', 'PYTHONHOME']\ncommands = [['pytest']]\n")
		require.Equal(t, "inconclusive", pythonRuntimeCheck(t, root).Status, command)
	}
	root := pythonReviewWorkflow(t, "{PYTHONHOME: ''}", "", "pytest")
	require.Equal(t, "compatible", pythonRuntimeCheck(t, root).Status)
}

func TestPythonSetupWithoutEnvironmentUpdateIsUnverified(t *testing.T) {
	for _, tc := range []struct{ value, status string }{
		{"false", "inconclusive"},
		{"${{ matrix.update }}", "inconclusive"},
		{"${{ vars.UPDATE_PYTHON }}", "inconclusive"},
		{"true", "compatible"},
	} {
		root := pythonReviewWorkflow(t, "{}", "", "pytest")
		path := filepath.Join(root, ".github/workflows/test.yml")
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		text := strings.Replace(string(data), "env: {}", "strategy:\n      matrix: {update: [false]}\n    env: {}", 1)
		text = strings.Replace(text, "python-version: '3.12'", "python-version: '3.12', update-environment: '"+tc.value+"'", 1)
		require.NoError(t, os.WriteFile(path, []byte(text), 0644))
		require.Equal(t, tc.status, pythonRuntimeCheck(t, root).Status, tc.value)
	}
}

func TestPythonMatrixCommandsRequiringReviewRemainCandidates(t *testing.T) {
	for _, command := range []string{"pytest --cov=app", "python -m pytest -k 'unit or integration'"} {
		root := pythonWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    strategy:
      matrix:
        command: [%q]
    steps:
      - run: ${{ matrix.command }}
`, command))
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.Empty(t, scope.Commands)
		require.NotEmpty(t, scope.Unresolved)
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status)
		require.Len(t, check.Jobs, 1)
		rows, err := CIMatrixRows(root, "pytest")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Contains(t, rows[0], " / tests / ")
	}
}

func TestPythonConfigBasedActivationRequiresRuntimeValidation(t *testing.T) {
	for _, tc := range []struct{ command, name, config string }{
		{"pytest", "pytest.ini", "[pytest]\nddtrace = true\n"},
		{"pytest", "pyproject.toml", "[tool.pytest.ini_options]\nddtrace = true\n"},
		{"pytest", "setup.cfg", "[tool:pytest]\nddtrace = true\n"},
		{"pytest -o ddtrace=true", "pytest.ini", "[pytest]\n"},
		{"pytest --override-ini=ddtrace=true", "pytest.ini", "[pytest]\n"},
		{"pytest -oddtrace=false", "pytest.ini", "[pytest]\nddtrace = true\n"},
		{"pytest -o no-ddtrace=true", "pytest.ini", "[pytest]\n"},
		{"pytest", "pytest.ini", "[pytest]\nno-ddtrace = true\n"},
	} {
		root := pythonReviewWorkflow(t, "{}", "", tc.command+"\n        env: {PYTEST_ADDOPTS: '-q'}")
		writeFile(t, root, tc.name, tc.config)
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status, "%+v", tc)
		require.Contains(t, check.Jobs[0].Reason, "activation")
	}
}
