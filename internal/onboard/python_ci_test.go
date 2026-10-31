package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonReleaseReadsOnlyPackageInfo(t *testing.T) {
	metadata, err := readPythonRelease(strings.NewReader(`{"info":{"version":"4.16.0","requires_python":">=3.9"},"releases":`))
	require.NoError(t, err)
	require.Equal(t, "4.16.0", metadata.Version)
	for _, text := range []string{`{}`, `{"info":{}}`, `{"info":null}`, `[]`, `{"info":`} {
		_, err := readPythonRelease(strings.NewReader(text))
		require.Error(t, err)
	}
}

func TestPythonRuntimeRequirement(t *testing.T) {
	for _, tc := range []struct{ python, requirement, status string }{
		{"3.12", "<3.15,>=3.9", "compatible"},
		{"3.8", ">=3.9", "incompatible"},
		{"3.15", ">=3.9,<3.15", "incompatible"},
		{"3", ">=3.9", "inconclusive"},
		{"pypy3.10", ">=3.9", "inconclusive"},
		{"3.12", ">=3.9,!=3.12.*", "inconclusive"},
	} {
		status, _ := ComparePythonRequirement(tc.python, tc.requirement)
		require.Equal(t, tc.status, status, "%s / %s", tc.python, tc.requirement)
	}
}

func TestPythonInvocationStopsAtTheExecutionTarget(t *testing.T) {
	for _, tc := range []struct {
		words []string
		match bool
	}{
		{[]string{"python", "-m", "pytest", "tests"}, true},
		{[]string{"python", "-mpytest", "tests"}, true},
		{[]string{"python3.12", "-W", "error", "-mpytest"}, true},
		{[]string{"python3.13d", "-m", "pytest"}, true},
		{[]string{"python3.13t", "-m", "pytest"}, true},
		{[]string{"python_d.exe", "-m", "pytest"}, true},
		{[]string{"uv", "run", "--", "python", "-mpytest"}, true},
		{[]string{"poetry", "run", "python", "-mpytest"}, true},
		{[]string{"python3.12", "-I", "-W", "error", "-Xdev", "-m", "pytest"}, true},
		{[]string{"python", "--check-hash-based-pycs", "always", "-m", "pytest"}, true},
		{[]string{"python", "wrapper.py", "-m", "pytest"}, false},
		{[]string{"python", "wrapper.py", "-mpytest"}, false},
		{[]string{"python", "-c", "print('wrapper')", "-mpytest"}, false},
		{[]string{"python", "--", "-mpytest"}, false},
		{[]string{"python", "-W", "-mpytest"}, false},
		{[]string{"python", "-mpip", "pytest"}, false},
		{[]string{"python", "-c", "print('wrapper')", "-m", "pytest"}, false},
		{[]string{"python", "-", "-m", "pytest"}, false},
		{[]string{"python", "--", "-m", "pytest"}, false},
		{[]string{"python", "-W", "-m", "pytest"}, false},
		{[]string{"python", "-X"}, false},
		{[]string{"python", "--version", "-m", "pytest"}, false},
		{[]string{"uv", "run", "--locked", "python", "wrapper.py", "-m", "pytest"}, false},
	} {
		require.Equal(t, tc.match, pythonInvocation(tc.words), "%v", tc.words)
	}
	root := pythonWorkflow(t, `jobs:
  tests:
    steps:
      - run: python -m pytest
      - run: python wrapper.py -m pytest
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "Unresolved Python entry point: python wrapper.py -m pytest")
	check := pythonRuntimeCheck(t, root)
	require.Len(t, check.Jobs, 2)
	require.Equal(t, "inconclusive", check.Jobs[1].Status)
}

func TestPythonAttachedModulesPreserveCIScopeAndOptions(t *testing.T) {
	root := pythonWorkflow(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: python -mpip install pytest
      - run: python -W error -mpytest -k selected
      - run: python -mtox -e py312
`)
	config := "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS']\ncommands = [['python', '-mpytest', '-q']]\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(config), 0644))
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Len(t, scope.Unresolved, 1)
	require.Contains(t, scope.Unresolved[0], "Local tox execution is not supported")
	require.Len(t, scope.Commands, 2)
	require.Equal(t, "python -W error -mpytest -k selected", scope.Commands[0].Command)
	require.Equal(t, "python -mtox -e py312", scope.Commands[1].Command)
	check := pythonRuntimeCheck(t, root)
	require.Equal(t, "compatible", check.Status, "%+v", check)
	require.Len(t, check.Jobs, 2)
	options, _, err := PytestCommandOptions("--ddtrace", []string{"python", "-W", "error", "-mpytest", "-k", "selected"}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"--ddtrace", "-k", "selected"}, options)

	unknown := pythonWorkflow(t, "jobs:\n  tests:\n    steps:\n      - run: python-helper -mpytest\n")
	check = pythonRuntimeCheck(t, unknown)
	require.Equal(t, "inconclusive", check.Status)
	require.Len(t, check.Jobs, 1)
	require.Contains(t, check.Jobs[0].Reason, "Unresolved Python entry point")
}

func TestPythonCIReleaseAndMatrixValidation(t *testing.T) {
	for _, tc := range []struct{ name, python, version, options, status string }{
		{"compatible", "3.12", "4.16.0", "--ddtrace -q", "compatible"},
		{"old runtime", "3.8", "4.16.0", "--ddtrace", "incompatible"},
		{"different tracer", "3.12", "4.15.0", "--ddtrace", "incompatible"},
		{"bootstrap overwritten", "3.12", "4.16.0", "-q", "incompatible"},
		{"runtime alias", "3.x", "4.16.0", "--ddtrace", "inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
			contents := "jobs:\n  test:\n    strategy:\n      matrix:\n        python: ['" + tc.python + "']\n    steps:\n      - uses: actions/setup-python@v6\n        with:\n          python-version: ${{ matrix.python }}\n      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: python\n          python-tracer-version: '" + tc.version + "'\n      - run: python -m pytest\n        env:\n          PYTEST_ADDOPTS: '" + tc.options + "'\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(contents), 0644))
			check := checkPythonCIRuntimes(t.Context(), root, "4.16.0", "", func(_ context.Context, version string) (PythonRelease, error) {
				return PythonRelease{version, ">=3.9,<3.15"}, nil
			}, func(context.Context, string) (string, error) {
				t.Fatal("explicit version must not resolve action default")
				return "", nil
			})
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			require.Equal(t, tc.python, check.Jobs[0].Runtime)
		})
	}
}

func TestPythonExpandsCommandMatrixBeforeFiltering(t *testing.T) {
	root := pythonWorkflow(t, `jobs:
  tests:
    strategy:
      matrix:
        command: ['python -m pytest tests', 'python3.12 -m pytest integration']
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: ${{ matrix.command }}
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	check := pythonRuntimeCheck(t, root)
	require.Equal(t, "compatible", check.Status, "%+v", check)
	require.Len(t, check.Jobs, 2)
}

func TestPythonUVDependencySelectors(t *testing.T) {
	selectors := []string{"--no-default-groups", "--all-groups", "--no-dev", "--only-dev", "--all-extras"}
	for _, option := range []string{"--group", "--no-group", "--only-group", "--extra", "--no-extra"} {
		selectors = append(selectors, option+" test", option+"=test")
	}
	for _, selector := range selectors {
		t.Run(selector, func(t *testing.T) {
			command := "uv run " + selector + " -- python -m pytest -k selected"
			options, _, err := PytestCommandOptions("--ddtrace", strings.Fields(command), nil)
			require.NoError(t, err)
			require.Equal(t, []string{"--ddtrace", "-k", "selected"}, options)
			root := pythonWorkflow(t, "jobs:\n  tests:\n    steps:\n      - run: "+command+"\n")
			scope, err := DiscoverValidationScope(root, "pytest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, command, scope.Commands[0].Command)
		})
	}
}

func TestPythonDoesNotCertifyCommandRuntimeOverrides(t *testing.T) {
	for _, command := range []string{"uv run --python 3.8 python -m pytest", "uv run --python=3.8 pytest", "python3.8 -m pytest", "poetry run python3.8 -m pytest", ".venv/bin/python -m pytest"} {
		root := pythonWorkflow(t, "jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: "+command+"\n")
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status, "%s: %+v", command, check)
		require.Contains(t, check.Jobs[0].Reason, "selects its own Python interpreter")
	}
}

func TestPythonRetainsApplicationPathAroundManagedTracer(t *testing.T) {
	for _, tc := range []struct{ parent, path, expected string }{
		{"", "${{ env.PYTHONPATH }}:src", "src"},
		{"lib", "${{env.PYTHONPATH}}:src", "lib:src"},
		{"lib", "src:${{ env.PYTHONPATH }}:tests", "src:lib:tests"},
		{"", "src::tests", "src::tests"},
	} {
		root := pythonWorkflow(t, "env:\n  PYTHONPATH: '"+tc.parent+"'\njobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: python -m pytest\n        env:\n          PYTHONPATH: '"+tc.path+"'\n")
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Equal(t, tc.expected, scope.Commands[0].Environment["PYTHONPATH"])
		if strings.Contains(tc.path, "${{") {
			require.Equal(t, "compatible", pythonRuntimeCheck(t, root).Status)
		}
	}
}

func TestPythonEffectiveInstrumentationOptions(t *testing.T) {
	for _, tc := range []struct{ name, parent, stepEnv, command, status string }{
		{"step overrides parent", "  PYTEST_ADDOPTS: '-q'\n", "          PYTEST_ADDOPTS: '--ddtrace -q'\n", "python -m pytest", "compatible"},
		{"step replaces unknown parent", "  PYTEST_ADDOPTS: '${{ secrets.OPTIONS }}'\n", "          PYTEST_ADDOPTS: '--ddtrace -q'\n", "python -m pytest", "compatible"},
		{"action options extended", "", "          PYTEST_ADDOPTS: '${{ env.PYTEST_ADDOPTS }} -q'\n", "python -m pytest", "compatible"},
		{"inherited disable flag", "  PYTEST_ADDOPTS: '--no-ddtrace'\n", "          PYTEST_ADDOPTS: '${{ env.PYTEST_ADDOPTS }} -q'\n", "python -m pytest", "incompatible"},
		{"inherited blocked plugin", "  PYTEST_ADDOPTS: '--ddtrace -p no:ddtrace'\n", "          PYTEST_ADDOPTS: '${{env.PYTEST_ADDOPTS}} -q'\n", "python -m pytest", "incompatible"},
		{"unknown inherited options", "  PYTEST_ADDOPTS: '${{ secrets.OPTIONS }}'\n", "          PYTEST_ADDOPTS: '${{ env.PYTEST_ADDOPTS }} -q'\n", "python -m pytest", "inconclusive"},
		{"environment disables tracing", "", "          PYTEST_ADDOPTS: '--ddtrace --no-ddtrace'\n", "python -m pytest", "incompatible"},
		{"autoload disabled without plugin", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_ADDOPTS: '--ddtrace'\n", "python -m pytest", "incompatible"},
		{"autoload disabled explicit command plugin", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_ADDOPTS: '--ddtrace'\n", "python -m pytest -p ddtrace", "compatible"},
		{"autoload disabled explicit environment plugin", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_ADDOPTS: '--ddtrace -p ddtrace'\n", "python -m pytest", "compatible"},
		{"plugin explicitly blocked", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_ADDOPTS: '--ddtrace -p no:ddtrace'\n", "python -m pytest", "incompatible"},
		{"autoload restored by step", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_DISABLE_PLUGIN_AUTOLOAD: ''\n          PYTEST_ADDOPTS: '--ddtrace'\n", "python -m pytest", "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonWorkflow(t, fmt.Sprintf("env:\n%sjobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n        env:\n%s", tc.parent, tc.command, tc.stepEnv))
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
		})
	}
}

func TestPythonCIReadsEffectiveConfigAddopts(t *testing.T) {
	for _, tc := range []struct {
		name, command, directory, status string
		files                            map[string]string
	}{
		{"ini plugin blocker", "pytest", ".", "incompatible", map[string]string{"pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"ini tracer disabled", "pytest", ".", "incompatible", map[string]string{"pytest.ini": "[pytest]\naddopts = --no-ddtrace\n"}},
		{"multiline options", "pytest", ".", "incompatible", map[string]string{"pytest.ini": "[pytest]\naddopts =\n    -q\n    -p no:ddtrace\n"}},
		{"toml options", "pytest", ".", "incompatible", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\naddopts = '-p no:ddtrace'\n"}},
		{"toml array", "pytest", ".", "incompatible", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\naddopts = ['-p', 'no:ddtrace']\n"}},
		{"tox ini", "pytest", ".", "incompatible", map[string]string{"tox.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"setup cfg", "pytest", ".", "incompatible", map[string]string{"setup.cfg": "[tool:pytest]\naddopts = -p no:ddtrace\n"}},
		{"ordinary filters", "pytest -k test_add", ".", "compatible", map[string]string{"pytest.ini": "[pytest]\naddopts = -q\n"}},
		{"empty ini wins", "pytest", ".", "compatible", map[string]string{"pytest.ini": "", "setup.cfg": "[tool:pytest]\naddopts = -p no:ddtrace\n"}},
		{"working directory", "pytest", "package", "incompatible", map[string]string{"package/pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"explicit config", "pytest -c ci.ini", ".", "incompatible", map[string]string{"ci.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"explicit toml config", "pytest -c ci.toml", ".", "incompatible", map[string]string{"ci.toml": "[tool.pytest.ini_options]\naddopts = '-p no:ddtrace'\n"}},
		{"explicit native toml config", "pytest -c ci.toml", ".", "inconclusive", map[string]string{"ci.toml": "[pytest]\naddopts = ['-p', 'no:ddtrace']\n"}},
		{"explicit config overrides discovery", "pytest --config-file=ci.ini", ".", "compatible", map[string]string{"pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n", "ci.ini": "[pytest]\naddopts = -q\n"}},
		{"override clears file options", "pytest -o addopts=", ".", "compatible", map[string]string{"pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"missing config", "pytest -c missing.ini", ".", "inconclusive", nil},
		{"malformed toml", "pytest", ".", "inconclusive", map[string]string{"pyproject.toml": "[broken\n"}},
		{"duplicate options", "pytest", ".", "inconclusive", map[string]string{"pytest.ini": "[pytest]\naddopts = -q\naddopts = -p no:ddtrace\n"}},
		{"nested config selection", "pytest tests", ".", "inconclusive", map[string]string{"pytest.ini": "[pytest]\n", "tests/pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"version dependent config", "pytest", ".", "inconclusive", map[string]string{"pytest.toml": "[pytest]\naddopts = ['-p', 'no:ddtrace']\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonWorkflow(t, fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n        working-directory: %s\n", tc.command, tc.directory))
			for name, body := range tc.files {
				path := filepath.Join(root, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
				require.NoError(t, os.WriteFile(path, []byte(body), 0644))
			}
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
		})
	}
}

func TestPythonCIIgnoresReadOnlySteps(t *testing.T) {
	for _, command := range []string{"python -m pytest", "echo before\npython -m pytest", "python -m pytest\necho after"} {
		root := pythonWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    steps:
      - run: echo preparing
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: |
          %s
      - run: printf 'done\n'
`, strings.ReplaceAll(command, "\n", "\n          ")))
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "compatible", check.Status, "%+v", check)
		require.Len(t, check.Jobs, 1)
	}
}

func TestPythonCIWrapperEnvironmentIsNotEstablishedBySetupPython(t *testing.T) {
	for _, command := range []string{"uv run --no-sync python -m pytest", "uv run pytest", "poetry run pytest"} {
		root := pythonWorkflow(t, fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n", command))
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status, "%s: %+v", command, check)
		require.Contains(t, check.Jobs[0].Reason, "selects its own Python interpreter")
	}
}

func TestPythonCIConfigSelectionUsesCommandOverEnvironment(t *testing.T) {
	for _, tc := range []struct{ config, status string }{{"good.ini", "compatible"}, {"bad.ini", "incompatible"}} {
		root := pythonWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: pytest -c %s
        env: {PYTEST_ADDOPTS: '--ddtrace -c good.ini'}
`, tc.config))
		require.NoError(t, os.WriteFile(filepath.Join(root, "good.ini"), []byte("[pytest]\naddopts = -q\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "bad.ini"), []byte("[pytest]\naddopts = -p no:ddtrace\n"), 0644))
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, tc.status, check.Status, "%+v", check)
	}
}

func TestPythonCILoggingDoesNotHideUnknownCommands(t *testing.T) {
	root := pythonWorkflow(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: |
          python -m pytest
          echo done
          custom-test-wrapper
`)
	check := pythonRuntimeCheck(t, root)
	require.Equal(t, "inconclusive", check.Status, "%+v", check)
	require.Contains(t, check.Jobs[0].Reason, "Unresolved Python entry point")
}

func TestPythonRejectsInterpreterWrappers(t *testing.T) {
	for _, command := range []string{"python-wrapper -m pytest", "uv run python-wrapper -m pytest", ".venv/bin/python-helper -m pytest"} {
		t.Run(command, func(t *testing.T) {
			root := pythonWorkflow(t, "jobs:\n  tests:\n    steps:\n      - run: "+command+"\n")
			scope, err := DiscoverValidationScope(root, "pytest")
			require.NoError(t, err)
			require.Empty(t, scope.Commands)
			require.Contains(t, strings.Join(scope.Unresolved, "\n"), "Unresolved Python entry point")
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, "inconclusive", check.Status)
			require.Len(t, check.Jobs, 1)
		})
	}
}

func TestPythonUVEnvironmentSelectsRuntime(t *testing.T) {
	for _, tc := range []struct{ name, workflowEnv, jobEnv, stepEnv, command, status string }{
		{name: "workflow selector", workflowEnv: "env: {UV_PYTHON: '3.8'}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "job selector", jobEnv: "    env: {UV_PYTHON: '3.8'}\n", command: "uv run pytest", status: "inconclusive"},
		{name: "step selector", stepEnv: "        env: {UV_PYTHON: '3.8'}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "cleared selector", workflowEnv: "env: {UV_PYTHON: '3.8'}\n", stepEnv: "        env: {UV_PYTHON: ''}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "direct Python unaffected", workflowEnv: "env: {UV_PYTHON: '3.8'}\n", command: "python -m pytest", status: "compatible"},
		{name: "workflow project environment", workflowEnv: "env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "job project environment", jobEnv: "    env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", command: "uv run pytest", status: "inconclusive"},
		{name: "step project environment", stepEnv: "        env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "cleared project environment", workflowEnv: "env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", stepEnv: "        env: {UV_PROJECT_ENVIRONMENT: ''}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "direct Python ignores project environment", workflowEnv: "env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", command: "python -m pytest", status: "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonWorkflow(t, fmt.Sprintf("%sjobs:\n  tests:\n%s    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n%s", tc.workflowEnv, tc.jobEnv, tc.command, tc.stepEnv))
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			if tc.status == "inconclusive" {
				require.Contains(t, check.Jobs[0].Reason, "selects its own Python interpreter")
			}
		})
	}
}

func TestPythonObjectCommandMatrixStaysUnresolved(t *testing.T) {
	for _, command := range []string{"python -m pytest", "python3.12 -m pytest", "uv run python -m pytest"} {
		root := pythonWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    strategy:
      matrix:
        config:
          - {python: '3.12', command: '%s'}
    steps:
      - run: ${{ matrix.config.command }}
  docs:
    strategy:
      matrix:
        config:
          - {label: pytest, command: 'mkdocs build'}
    steps:
      - run: ${{ matrix.config.command }}
`, command))
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.Empty(t, scope.Commands)
		require.Len(t, scope.Unresolved, 1)
		require.Contains(t, scope.Unresolved[0], " / tests:")
		require.Contains(t, scope.Unresolved[0], "non-scalar value")
		check := pythonRuntimeCheck(t, root)
		require.Equal(t, "inconclusive", check.Status)
		require.Len(t, check.Jobs, 1)
		require.Equal(t, "tests", check.Jobs[0].Job)
		_, err = CIMatrixRows(root, "pytest")
		require.ErrorContains(t, err, "non-scalar value")
	}
}

func TestPythonCIInstrumentationIsCheckedPerInvocation(t *testing.T) {
	for _, tc := range []struct {
		name, command, toxCommands, autoload, addopts, status string
	}{
		{"direct missing second plugin", "pytest -p ddtrace tests/unit\npytest tests/integration", "", "1", "--ddtrace", "incompatible"},
		{"direct missing first plugin", "pytest tests/unit\npytest -p ddtrace tests/integration", "", "1", "--ddtrace", "incompatible"},
		{"direct both load plugin", "pytest -p ddtrace tests/unit\npytest -p ddtrace tests/integration", "", "1", "--ddtrace", "compatible"},
		{"command disables first autoload", "pytest --disable-plugin-autoload\npytest -p ddtrace", "", "", "--ddtrace", "incompatible"},
		{"command disables second autoload", "pytest -p ddtrace\npytest --disable-plugin-autoload", "", "", "--ddtrace", "incompatible"},
		{"autoload flag stays in its process", "pytest --disable-plugin-autoload -p ddtrace\npytest", "", "", "--ddtrace", "compatible"},
		{"tracing flag stays in its process", "pytest --ddtrace -p ddtrace\npytest -p ddtrace", "", "1", "", "incompatible"},
		{"tox missing second plugin", "tox -e py312", `[["pytest", "-p", "ddtrace"], ["pytest"]]`, "1", "--ddtrace", "incompatible"},
		{"tox missing first plugin", "tox -e py312", `[["pytest"], ["pytest", "-p", "ddtrace"]]`, "1", "--ddtrace", "incompatible"},
		{"tox both load plugin", "tox -e py312", `[["pytest", "-p", "ddtrace"], ["pytest", "-p", "ddtrace"]]`, "1", "--ddtrace", "compatible"},
		{"direct then tox", "pytest -p ddtrace\ntox -e py312", `[["pytest"]]`, "1", "--ddtrace", "incompatible"},
		{"tox then direct", "tox -e py312\npytest", `[["pytest", "-p", "ddtrace"]]`, "1", "--ddtrace", "incompatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    env: {PYTEST_DISABLE_PLUGIN_AUTOLOAD: '%s'}
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: |
          %s
        env: {PYTEST_ADDOPTS: '%s'}
`, tc.autoload, strings.ReplaceAll(tc.command, "\n", "\n          "), tc.addopts))
			if tc.toxCommands != "" {
				config := "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS', 'PYTEST_DISABLE_PLUGIN_AUTOLOAD']\ncommands = " + tc.toxCommands + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(config), 0644))
			}
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			if tc.status == "incompatible" {
				reason := "autoload is disabled"
				if tc.addopts == "" {
					reason = "preserve --ddtrace"
				}
				require.Contains(t, check.Jobs[0].Reason, reason)
			}
		})
	}
}

func TestPythonCIVersionedExecutableAliases(t *testing.T) {
	for _, tc := range []struct{ python, executable, status string }{
		{"3.12.8", "python", "compatible"},
		{"3.12.8", "python3", "compatible"},
		{"3.12.8", "python3.12", "compatible"},
		{"3.12.8", "python3.12.exe", "compatible"},
		{"3.12.8", "python3.12.8", "compatible"},
		{"3.12.8", "python3.12.9", "inconclusive"},
		{"3.12.8", "python3.1", "inconclusive"},
		{"3.12.8", "python3.11", "inconclusive"},
		{"3.12", "python3.12", "compatible"},
		{"3.12", "python3.12.8", "inconclusive"},
		{"3.12.8", ".venv/bin/python3.12", "inconclusive"},
	} {
		t.Run(tc.python+"/"+tc.executable, func(t *testing.T) {
			root := pythonWorkflow(t, fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '%s'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s -m pytest\n", tc.python, tc.executable))
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
		})
	}
}

func TestPythonInlineAddoptsInstrumentation(t *testing.T) {
	for _, tc := range []struct{ command, status, reason string }{
		{`python -m pytest -o addopts=--no-ddtrace`, "incompatible", "disables"},
		{`pytest -oaddopts=--no-ddtrace`, "incompatible", "disables"},
		{`pytest -o=addopts=--no-ddtrace`, "incompatible", "disables"},
		{`pytest --override-ini addopts=--no-ddtrace`, "incompatible", "disables"},
		{`uv run -- python -m pytest --override-ini=addopts=--no-ddtrace`, "inconclusive", "selects its own Python interpreter"},
		{`pytest -o 'addopts=-p no:ddtrace'`, "incompatible", "block"},
		{`pytest -o addopts=--disable-plugin-autoload`, "incompatible", "autoload"},
		{`pytest -o 'addopts=--disable-plugin-autoload -p ddtrace'`, "compatible", "satisfies"},
		{`pytest -o addopts=--no-ddtrace --override-ini=addopts=`, "compatible", "satisfies"},
		{`pytest -o addopts= --override-ini=addopts=--no-ddtrace`, "incompatible", "disables"},
		{`tox -e py312 -- -o addopts=--no-ddtrace`, "incompatible", "disables"},
		{`tox -e py312 -- --override-ini 'addopts=-p no:ddtrace'`, "incompatible", "block"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := pythonWorkflow(t, fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n", tc.command))
			writeToxProject(t, root, `pass_env = ["DD_*", "PYTHONPATH", "PYTEST_ADDOPTS"]`)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			require.Contains(t, check.Jobs[0].Reason, tc.reason)
		})
	}
}
