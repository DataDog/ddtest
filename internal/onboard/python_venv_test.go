// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pythonVenvWorkflow is a GitHub Actions job on setup-python 3.12 whose
// steps, the given run steps, follow the Datadog action.
func pythonVenvWorkflow(t *testing.T, steps string) string {
	t.Helper()
	root := pythonWorkflow(t, `jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - uses: datadog/test-visibility-github-action@v3
        with:
          languages: python
          python-tracer-version: '4.16.0'
`+steps)
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	t.Chdir(root)
	return root
}

// The CI checks read a step's commands as discovery does: without the
// statements that activate a virtual environment, and with installation
// extras. A pytest command after either keeps its options and environment
// checked, so an uninstrumented invocation is never compatible.
func TestPythonChecksNormalizeActivatedCommands(t *testing.T) {
	for _, tc := range []struct {
		name, run, status, reason string
	}{
		{"activation", "source venv/bin/activate && python -m pytest --no-ddtrace tests", "incompatible", "explicitly disables Datadog instrumentation"},
		{"activation on its own line", "source venv/bin/activate\npytest -p no:ddtrace tests", "incompatible", "explicitly block the Datadog plugin"},
		{"installation extras", "pip install .[dev] && pytest --no-ddtrace tests", "incompatible", "explicitly disables Datadog instrumentation"},
		{"interpreter flags", "source venv/bin/activate && python -X dev -m pytest --no-ddtrace tests", "incompatible", "explicitly disables Datadog instrumentation"},
		{"ignored environment", "source venv/bin/activate && python -I -m pytest tests", "inconclusive", "ignores PYTHONPATH"},
		{"inline path", "source venv/bin/activate && PYTHONPATH=src pytest tests", "incompatible", "`PYTHONPATH=src pytest tests` replaces the PYTHONPATH"},
		{"own interpreter", "source venv/bin/activate && python3.11 -m pytest tests", "inconclusive", "selects its own Python interpreter"},
		{"instrumented", "source venv/bin/activate && python -m pytest tests", "compatible", ""},
		{"longer chain", "source venv/bin/activate && pip install -r requirements.txt && python -m pytest tests", "compatible", ""},
		{"longer chain disabled", "source venv/bin/activate && pip install .[dev] && pytest --no-ddtrace tests", "incompatible", "explicitly disables Datadog instrumentation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pythonVenvWorkflow(t, "      - run: python -m venv venv\n      - run: |\n"+indentLines(tc.run, "          "))
			check := pythonRuntimeCheck(t, ".")
			require.Len(t, check.Jobs, 1, pythonFindings(check))
			require.Equal(t, tc.status, check.Jobs[0].Status, pythonFindings(check))
			require.Contains(t, check.Jobs[0].Reason, tc.reason)
		})
	}
	// The same holds on a provider whose setup is a script.
	root := gitLabPythonRepository(t, gitLabPythonJob("python:3.12", "    - python -m venv venv\n", "    - source venv/bin/activate && python -m pytest --no-ddtrace tests\n"))
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "incompatible", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "explicitly disables Datadog instrumentation")
}

func indentLines(text, indent string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, indent+line+"\n")
	}
	return strings.Join(lines, "")
}

// GitHub Actions and CircleCI run every step in a new shell, so what a
// tooling step activates is gone when a later step runs pytest; the
// environments it created stay. Script entries that share a shell (GitLab
// CI) keep the activation.
func TestPythonVenvActivationEndsWithItsShell(t *testing.T) {
	pythonVenvWorkflow(t, "      - run: |\n          uv venv tools\n          source tools/bin/activate && ruff check .\n      - run: pytest tests\n")
	check := pythonRuntimeCheck(t, ".")
	require.Equal(t, "compatible", check.Status, pythonFindings(check))

	root := circleRepository(t, "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n    docker:\n      - image: cimg/python:3.12\n    steps:\n      - checkout\n      - "+strings.ReplaceAll(circlePythonOrb, "\n    ", "\n          ")+"      - run: |\n          uv venv tools\n          source tools/bin/activate && ruff check .\n      - run: pytest tests\nworkflows:\n  main:\n    jobs: [test]\n")
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	check = pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "compatible", check.Status, pythonFindings(check))

	root = gitLabPythonRepository(t, gitLabPythonJob("python:3.12", "    - uv venv tools\n    - source tools/bin/activate && ruff check .\n", ""))
	check = pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "inconclusive", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "virtual environment tools")
}

// Each pytest invocation is checked in the environment active when it runs,
// not the one the step ends in.
func TestPythonVenvPerInvocation(t *testing.T) {
	pythonVenvWorkflow(t, "      - run: python -m venv venv\n      - run: |\n          source cached/bin/activate\n          pytest tests/unit\n          source venv/bin/activate\n          pytest tests/integration\n")
	check := pythonRuntimeCheck(t, ".")
	require.Equal(t, "inconclusive", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "virtual environment cached")

	// The other order runs the first suite on the job's environment.
	pythonVenvWorkflow(t, "      - run: python -m venv venv\n      - run: |\n          source venv/bin/activate\n          pytest tests/unit\n          source cached/bin/activate\n          pytest tests/integration\n")
	check = pythonRuntimeCheck(t, ".")
	require.Equal(t, "inconclusive", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "virtual environment cached")
}

// The tracker itself: an activation lasts until deactivate or the end of
// its shell, follows cd, and anything it cannot resolve is unresolved for
// the invocations after it, never the job's Python.
func TestPythonVenvTracker(t *testing.T) {
	var venvs pythonVenvs
	require.Empty(t, venvs.step("python -m venv venv\nsource venv/bin/activate\npytest", "3.12", "", nil))
	require.Empty(t, venvs.step("pytest", "3.12", "", nil))
	require.Contains(t, venvs.step("pytest", "3.11", "", nil), "created with Python 3.12, but the job selects Python 3.11")
	require.Contains(t, venvs.step("pytest", "", "", nil), "a Python version ddtest cannot tell")

	require.Contains(t, venvs.step("source cached/bin/activate\ntox -e py312", "3.12", "", nil), "virtual environment cached")
	require.Empty(t, venvs.step("deactivate\npytest", "3.12", "", nil))
	require.Empty(t, venvs.step("source cached/bin/activate", "3.12", "", nil))
	venvs.shell()
	require.Empty(t, venvs.step("pytest", "3.12", "", nil))

	// cd within the step: the environment is the one in app.
	require.Empty(t, venvs.step("cd app && python -m venv .venv\nsource .venv/bin/activate && pytest", "3.12", "", nil))
	require.Contains(t, venvs.step("source .venv/bin/activate && pytest", "3.12", "", nil), "virtual environment .venv")
	venvs.shell()
	require.Empty(t, venvs.step("source .venv/bin/activate && pytest", "3.12", "app", nil))
	venvs.shell()
	require.Contains(t, venvs.step("cd \"$SOMEWHERE\" && source .venv/bin/activate && pytest", "3.12", "", nil), "cannot resolve")
	venvs.shell()

	// An interpreter option, uv, or a Python the job did not select.
	for _, create := range []string{"python -m venv --python=3.11 env", "uv venv env", "python3.11 -m venv env", "PYTHONHOME=/opt python -m venv env"} {
		var venvs pythonVenvs
		require.Contains(t, venvs.step(create+"\nsource env/bin/activate\npytest", "3.12", "", nil), "does not create with its own Python", create)
	}
	for _, activation := range []string{"source /opt/env/bin/activate", "source ../env/bin/activate", ". \"$ENV/bin/activate\"", "conda activate test", "[ -d env ] && source env/bin/activate || true", "for env in a b; do source $env/bin/activate; done"} {
		var venvs pythonVenvs
		require.Contains(t, venvs.step(activation+"\npytest", "3.12", "", nil), "cannot resolve", activation)
	}
	// An environment created inside an active one runs that one's Python,
	// not the job's; inside one ddtest cannot resolve, a Python it cannot
	// tell.
	venvs = pythonVenvs{}
	require.Empty(t, venvs.step("python3.11 -m venv outer", "3.11", "", nil))
	require.Contains(t, venvs.step("source outer/bin/activate\npython -m venv inner\ndeactivate\nsource inner/bin/activate\npytest", "3.12", "", nil), "created with Python 3.11, but the job selects Python 3.12")
	venvs = pythonVenvs{}
	require.Contains(t, venvs.step("source cached/bin/activate\npython -m venv fresh\nsource fresh/bin/activate\npytest", "3.12", "", nil), "virtual environment fresh, which this job does not create with its own Python")
	venvs = pythonVenvs{}
	require.Contains(t, venvs.step("source \"$ENV/bin/activate\"\npython -m venv fresh\nsource fresh/bin/activate\npytest", "3.12", "", nil), "virtual environment fresh, which this job does not create with its own Python")
	venvs = pythonVenvs{}
	require.Empty(t, venvs.step("python -m venv outer\nsource outer/bin/activate\npython -m venv inner\nsource inner/bin/activate\npytest", "3.12", "", nil))
	// A pytest inside control flow is checked against what precedes it.
	venvs = pythonVenvs{}
	require.Contains(t, venvs.step("source cached/bin/activate\nif true; then pytest; fi", "3.12", "", nil), "virtual environment cached")
}

// Activation paths are followed after variables resolve; one ddtest cannot
// resolve keeps the tests unresolved.
func TestPythonVenvResolvesVariables(t *testing.T) {
	for _, tc := range []struct {
		name, steps, status, reason string
	}{
		{"cached", "      - run: source \"$VENV/bin/activate\" && pytest tests\n        env:\n          VENV: cached/venv\n", "inconclusive", "virtual environment cached/venv"},
		{"created", "      - run: python -m venv venv\n      - run: source \"$VENV/bin/activate\" && pytest tests\n        env:\n          VENV: venv\n", "compatible", ""},
		{"working directory", "      - run: python -m venv venv\n        working-directory: app\n      - run: source venv/bin/activate && pytest tests\n        working-directory: app\n", "compatible", ""},
		{"other directory", "      - run: python -m venv venv\n        working-directory: app\n      - run: source app/venv/bin/activate && pytest app/tests\n", "compatible", ""},
		{"absolute", "      - run: source /opt/venv/bin/activate && pytest tests\n", "inconclusive", ""},
		{"conditional", "      - run: |\n          if [ -d cached ]; then source cached/bin/activate; fi\n          pytest tests\n", "inconclusive", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonVenvWorkflow(t, tc.steps)
			writeFile(t, root, "app/pytest.ini", "[pytest]\n")
			check := pythonRuntimeCheck(t, ".")
			require.Len(t, check.Jobs, 1, pythonFindings(check))
			require.Equal(t, tc.status, check.Jobs[0].Status, pythonFindings(check))
			require.Contains(t, check.Jobs[0].Reason, tc.reason)
		})
	}
	// A shared shell's activations are resolved too.
	root := gitLabPythonRepository(t, gitLabPythonJob("python:3.12", "    - if [ -d cached ]; then source cached/bin/activate; fi\n", ""))
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "inconclusive", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "cannot resolve")
	root = gitLabPythonRepository(t, gitLabPythonJob("python:3.12", "    - source /opt/venv/bin/activate\n", ""))
	check = pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "inconclusive", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "cannot resolve")
}

// A virtual environment runs the Python that created it: one created before
// setup-python selects another version does not run the version the tracer
// is validated for.
func TestPythonVenvKeepsItsCreationVersion(t *testing.T) {
	workflow := func(first string) string {
		return `jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '` + first + `'
      - run: python -m venv venv
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - uses: datadog/test-visibility-github-action@v3
        with:
          languages: python
          python-tracer-version: '4.16.0'
      - run: source venv/bin/activate && pytest tests
`
	}
	root := pythonWorkflow(t, workflow("3.11"))
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	t.Chdir(root)
	check := pythonRuntimeCheck(t, root)
	require.Equal(t, "inconclusive", check.Status, pythonFindings(check))
	require.Contains(t, check.Jobs[0].Reason, "created with Python 3.11")
	require.Contains(t, check.Jobs[0].Reason, "Python 3.12")

	root = pythonWorkflow(t, workflow("3.12"))
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	t.Chdir(root)
	check = pythonRuntimeCheck(t, root)
	require.Equal(t, "compatible", check.Status, pythonFindings(check))
}
