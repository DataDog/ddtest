// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const gitLabPythonSetup = `LANGUAGES="python" SITE="datadoghq.com" API_KEY="$DD_API_KEY" PYTHON_TRACER_VERSION="4.16.0"`

const circlePythonOrb = "test-optimization-circleci-orb/autoinstrument:\n    languages: python\n    site: datadoghq.com\n    python_tracer_version: 4.16.0\n"

// pythonFindings lists a check's findings as status: reason, one per line.
func pythonFindings(check RuntimeCheck) string {
	var lines []string
	for _, finding := range check.Jobs {
		lines = append(lines, finding.Workflow+" / "+finding.Job+": "+finding.Status+": "+finding.Reason)
	}
	return strings.Join(lines, "\n")
}

// pythonRuntimeCheckForSite is pythonRuntimeCheck for the selected site.
func pythonRuntimeCheckForSite(t *testing.T, root, site string) RuntimeCheck {
	t.Helper()
	return checkPythonCIRuntimes(t.Context(), root, "4.16.0", site, func(_ context.Context, version string) (PythonRelease, error) {
		if version == "latest" {
			version = "4.16.0"
		}
		return PythonRelease{version, ">=3.9,<3.15"}, nil
	}, func(context.Context, string) (string, error) {
		t.Fatal("explicit version must not resolve action default")
		return "", nil
	})
}

func TestPythonImageRuntime(t *testing.T) {
	for image, expected := range map[string][2]string{
		"python:3.12":                                   {"3.12", ""},
		"python:3.12.7-slim-bookworm":                   {"3.12.7", ""},
		"docker.io/library/python:3.13-alpine":          {"3.13", ""},
		"cimg/python:3.11":                              {"3.11", ""},
		"cimg/python:3.12.4-node":                       {"3.12.4", ""},
		"circleci/python:3.9-browsers":                  {"3.9", ""},
		"ghcr.io/astral-sh/uv:python3.12-bookworm":      {"3.12", ""},
		"ghcr.io/astral-sh/uv:0.10.0-python3.13-trixie": {"3.13", ""},
		"pypy:3.10":     {"", "PyPy"},
		"python:latest": {"", ""},
		"python:3":      {"", ""},
		"python":        {"", ""},
		"ghcr.io/astral-sh/uv@sha256:1382c1a95c61": {"", ""},
		"ubuntu:24.04": {"", ""},
		"registry.example.com/team/python-ci:3.12": {"", ""},
	} {
		version, engine := pythonImageRuntime(image)
		require.Equal(t, expected, [2]string{version, engine}, image)
	}
	// A floating tag is the image's to select, not the user's to state.
	require.Equal(t, "latest", pythonImageSelection(runtimeJob{Image: "python"}))
	require.Empty(t, pythonImageSelection(runtimeJob{Image: "ghcr.io/astral-sh/uv@sha256:1382c1a95c61"}))
}

func gitLabPythonRepository(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	writeFile(t, root, ".gitlab-ci.yml", config)
	t.Chdir(root)
	useCIProvider(t, "gitlab")
	return root
}

func gitLabPythonJob(image, before, after string) string {
	return "pytest:\n  image: " + image + "\n  script:\n    - pip install -r requirements.txt\n" + before + gitLabSetupEntry(gitLabPythonSetup) + after + "    - python -m pytest tests\n"
}

// On GitLab the pytest CI check reads the setup script: Python, an API key
// from a CI/CD variable, an exact ddtrace release and the selected site,
// before every pytest command, on the Python the image selects. A later
// command must extend the installer's PYTHONPATH and PYTEST_ADDOPTS.
func TestPythonGitLabSetupCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name, config, site, status, reason string
	}{
		{"complete", gitLabPythonJob("python:3.12", "", ""), "", "compatible", ""},
		{"selected site", gitLabPythonJob("python:3.12", "", ""), "datadoghq.com", "compatible", ""},
		{"other site", gitLabPythonJob("python:3.12", "", ""), "datadoghq.eu", "inconclusive", `set SITE="datadoghq.eu"`},
		{"missing", "pytest:\n  image: python:3.12\n  script:\n    - python -m pytest tests\n", "", "incompatible", "No Datadog setup script selecting python runs before this pytest command"},
		{"other language", strings.Replace(gitLabPythonJob("python:3.12", "", ""), `LANGUAGES="python"`, `LANGUAGES="js"`, 1), "", "incompatible", "No Datadog setup script selecting python"},
		{"all languages", strings.Replace(gitLabPythonJob("python:3.12", "", ""), `LANGUAGES="python"`, `LANGUAGES="all"`, 1), "", "compatible", ""},
		{"latest tracer", strings.Replace(gitLabPythonJob("python:3.12", "", ""), `"4.16.0"`, `"latest"`, 1), "", "inconclusive", `PYTHON_TRACER_VERSION "latest" is not an exact release`},
		{"no tracer version", strings.Replace(gitLabPythonJob("python:3.12", "", ""), ` PYTHON_TRACER_VERSION="4.16.0"`, "", 1), "", "inconclusive", "is not an exact release"},
		{"other release", strings.Replace(gitLabPythonJob("python:3.12", "", ""), `"4.16.0"`, `"4.15.6"`, 1), "", "incompatible", "CI selects ddtrace@4.15.6; local validation selected ddtrace@4.16.0"},
		{"literal key", strings.Replace(gitLabPythonJob("python:3.12", "", ""), `"$DD_API_KEY"`, `"0123456789abcdef"`, 1), "", "inconclusive", "not a CI/CD variable reference"},
		{"after the tests", "pytest:\n  image: python:3.12\n  script:\n    - python -m pytest tests\n" + gitLabSetupEntry(gitLabPythonSetup), "", "incompatible", "No Datadog setup script"},
		{"floating image", gitLabPythonJob("python:latest", "", ""), "", "inconclusive", "selects no Python minor version"},
		{"unsupported Python", gitLabPythonJob("python:3.15", "", ""), "", "incompatible", "3.15"},
		{"replaced path", gitLabPythonJob("python:3.12", "", "    - export PYTHONPATH=src\n"), "", "incompatible", "replaces the one the Datadog setup script exported"},
		{"extended path", gitLabPythonJob("python:3.12", "", "    - export PYTHONPATH=\"$PYTHONPATH:src\"\n"), "", "compatible", ""},
		{"path before the setup", gitLabPythonJob("python:3.12", "    - export PYTHONPATH=src\n", ""), "", "compatible", ""},
		{"replaced options", gitLabPythonJob("python:3.12", "", "    - export PYTEST_ADDOPTS=-x\n"), "", "incompatible", "replaces the Datadog setup script's PYTEST_ADDOPTS"},
		{"extended options", gitLabPythonJob("python:3.12", "", "    - export PYTEST_ADDOPTS=\"$PYTEST_ADDOPTS -x\"\n"), "", "compatible", ""},
		{"virtual environment", gitLabPythonJob("python:3.12", "    - python -m venv venv\n    - source venv/bin/activate\n", ""), "", "compatible", ""},
		{"uv environment", gitLabPythonJob("python:3.12", "    - uv venv venv\n    - source venv/bin/activate\n", ""), "", "inconclusive", "The tests run in the virtual environment venv, which this job does not create with its own Python"},
		{"other interpreter", gitLabPythonJob("python:3.12", "    - python3.11 -m venv venv\n    - . venv/bin/activate\n", ""), "", "inconclusive", "virtual environment venv"},
		{"unknown environment", gitLabPythonJob("python:3.12", "    - source .cache/venv/bin/activate\n", ""), "", "inconclusive", "virtual environment .cache/venv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := gitLabPythonRepository(t, tc.config)
			check := pythonRuntimeCheckForSite(t, root, tc.site)
			require.Len(t, check.Jobs, 1, pythonFindings(check))
			require.Equal(t, tc.status, check.Jobs[0].Status, pythonFindings(check))
			require.Contains(t, check.Jobs[0].Reason, tc.reason)
		})
	}
}

// A job the tracer cannot instrument keeps its tests uninstrumented in a job
// without the setup: reported as excluded, not as missing instrumentation.
// At least one entry must be instrumented.
func TestPythonGitLabExcludedJobs(t *testing.T) {
	root := gitLabPythonRepository(t, gitLabPythonJob("python:3.12", "", "")+"next:\n  image: python:3.15-rc\n  script:\n    - python -m pytest tests\npypy:\n  image: pypy:3.10\n  script:\n    - python -m pytest tests\n")
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "compatible", check.Status, pythonFindings(check))
	findings := pythonFindings(check)
	require.Contains(t, findings, ".gitlab-ci.yml / next: excluded: This job runs Python 3.15, which ddtrace@4.16.0 does not support")
	require.Contains(t, findings, ".gitlab-ci.yml / pypy: excluded: This job runs PyPy (image pypy:3.10)")

	root = gitLabPythonRepository(t, "next:\n  image: python:3.15-rc\n  script:\n    - python -m pytest tests\n")
	check = pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "incompatible", check.Status)
	require.Contains(t, check.Reason, "at least one must run the Datadog setup")
}

// A job whose image names no Python version, or no image, runs a Python
// only the user can state; the statement is checked like an image tag and
// reported as stated.
func TestPythonStatedRuntime(t *testing.T) {
	config := "pytest:\n  image: ghcr.io/astral-sh/uv@sha256:1382c1a95c61\n  script:\n" + gitLabSetupEntry(gitLabPythonSetup) + "    - python -m pytest tests\n"
	root := gitLabPythonRepository(t, config)
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "inconclusive", check.Jobs[0].Status)
	require.Contains(t, check.Jobs[0].Reason, "Ask the user which Python version it runs")
	require.Contains(t, check.Jobs[0].RuntimeHost, "whose name selects no Python version")
	required := RequiredCIRuntimes(t.Context(), root, "python", "pytest")
	require.NotNil(t, required)
	require.Equal(t, []string{".gitlab-ci.yml / pytest"}, required.Jobs)
	require.Equal(t, "python", required.Flag)

	statement, err := ParseCIRuntime("pytest:python=3.12")
	require.NoError(t, err)
	require.NoError(t, UseCIStatements([]CIRuntimeStatement{statement}, nil))
	t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
	require.NoError(t, CheckCIRuntimeStatements(root))
	check = pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "compatible", check.Jobs[0].Status, pythonFindings(check))
	require.True(t, check.Jobs[0].UserStated)
	stated, unused := StatedFacts(&check, "python")
	require.Empty(t, unused)
	require.Equal(t, []string{"Python 3.12 for .gitlab-ci.yml / pytest (--ci-runtime). ddtest checked the tracer against it as it checks a CI image's version."}, stated)

	// A statement never overrides what the image selects.
	writeFile(t, root, ".gitlab-ci.yml", gitLabPythonJob("python:3.12", "", ""))
	statement, err = ParseCIRuntime("pytest:python=3.11")
	require.NoError(t, err)
	require.NoError(t, UseCIStatements([]CIRuntimeStatement{statement}, nil))
	var conflict *CIRuntimeConflict
	require.ErrorAs(t, CheckCIRuntimeStatements(root), &conflict)
	require.Contains(t, conflict.Error(), "contradicts Python 3.12, which image python:3.12 selects")
}

// Local replay keeps the job's own PYTHONPATH and pytest options, never the
// installer's tracer path, and other languages' commands after a setup for
// several keep their environment.
func TestPythonProviderReplayKeepsTheJobsEnvironment(t *testing.T) {
	config := "pytest:\n  image: python:3.12\n  variables:\n    PYTHONPATH: src\n  script:\n" + gitLabSetupEntry(gitLabPythonSetup) + "    - export PYTHONPATH=\"$PYTHONPATH:lib\"\n    - python -m pytest tests\n"
	root := gitLabPythonRepository(t, config)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, map[string]string{"PYTHONPATH": "src:lib"}, scope.Commands[0].Environment)

	exported, references := pythonSetupExports(map[string]string{})
	require.Empty(t, exported)
	require.Equal(t, map[string]string{"PYTHONPATH": pythonTracerPath, "PYTEST_ADDOPTS": "--ddtrace"}, references)
	exported, _ = pythonSetupExports(map[string]string{"PYTHONPATH": "src", "PYTEST_ADDOPTS": "-x"})
	require.Equal(t, map[string]string{"PYTHONPATH": pythonTracerPath + ":src", "PYTEST_ADDOPTS": "--ddtrace -x"}, exported)
}

// A step that only activates a virtual environment, prints the Python
// version or runs a linter or uploader is not an unresolved test entry
// point, here on GitHub Actions too; the environment's Python is checked.
func TestPythonVirtualEnvironmentsAndToolSteps(t *testing.T) {
	root := pythonWorkflow(t, `jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - run: |
          python --version
          python -m venv venv
          source venv/bin/activate
          pip install .[dev]
      - uses: datadog/test-visibility-github-action@v3
        with:
          languages: python
          python-tracer-version: '4.16.0'
      - run: |
          . venv/bin/activate && pytest tests
          coverage xml
          codecov
          python setup.py sdist bdist_wheel
          twine check dist/*
`)
	t.Chdir(root)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "pytest tests", scope.Commands[0].Command)
	check := pythonRuntimeCheck(t, root)
	require.Equal(t, "compatible", check.Status, pythonFindings(check))

	rest, activated := pythonWithoutActivations("source venv/bin/activate\n. .venv/bin/activate && pytest -x\necho done")
	require.Equal(t, "pytest -x\necho done", rest)
	require.Equal(t, []string{"venv", ".venv"}, activated)
	rest, activated = pythonWithoutActivations("source $VENV/bin/activate")
	require.Equal(t, "source $VENV/bin/activate", rest)
	require.Empty(t, activated)
}

// The CircleCI fixture is einsteinpy's configuration: a reusable command
// creates a virtual environment and runs pytest for a Python matrix, one of
// whose versions ddtrace does not support yet, and a coverage job on it.
func TestPythonCircleCIFixture(t *testing.T) {
	root := copyFixture(t, "circleci-pytest")
	t.Chdir(root)
	useCIProvider(t, "")
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "circleci", selected)
	useCIProvider(t, selected)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "incompatible", check.Status)
	require.Contains(t, pythonFindings(check), "No Datadog Test Optimization orb selecting python runs before this pytest command")

	// The setup goes into the command every test job uses, before its test
	// step; the coverage job on Python 3.15 stays uninstrumented.
	config := readFixtureFile(t, root, ".circleci/config.yml")
	config = strings.Replace(config, "version: 2.1\n", "version: 2.1\n\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n", 1)
	config = strings.Replace(config, "      - run:\n          name: run tests\n", "      - "+strings.ReplaceAll(circlePythonOrb, "\n    ", "\n          ")+"      - run:\n          name: run tests\n", 1)
	writeFile(t, root, ".circleci/config.yml", config)
	check = pythonRuntimeCheckForSite(t, root, "datadoghq.com")
	findings := pythonFindings(check)
	require.Contains(t, findings, "build_and_test/test_py3.12: compatible")
	require.Contains(t, findings, "build_and_test/test_py3.15: incompatible: Python 3.15 does not satisfy")
	require.Contains(t, findings, "build_and_test/coverage: excluded: This job runs Python 3.15, which ddtrace@4.16.0 does not support")

	// Skipping the orb for 3.15 with a condition on the matrix parameter
	// keeps the matrix entry and its tests, uninstrumented.
	orb := "      - " + strings.ReplaceAll(circlePythonOrb, "\n    ", "\n          ")
	config = strings.Replace(config, orb, "      - unless:\n          condition:\n            equal: [\"3.15\", << parameters.python_version >>]\n          steps:\n            - "+strings.ReplaceAll(circlePythonOrb, "\n    ", "\n                "), 1)
	config = strings.Replace(config, "    description: Install einsteinpy with dev extras and run the test suite\n", "    description: Install einsteinpy with dev extras and run the test suite\n    parameters:\n      python_version:\n        type: string\n", 1)
	config = strings.Replace(config, "    steps:\n      - install_and_test\n", "    steps:\n      - install_and_test:\n          python_version: << parameters.python_version >>\n", 1)
	writeFile(t, root, ".circleci/config.yml", config)
	check = pythonRuntimeCheckForSite(t, root, "datadoghq.com")
	require.Equal(t, "compatible", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "build_and_test/test_py3.15: excluded: This job runs Python 3.15")
	require.Contains(t, pythonFindings(check), "build_and_test/test_py3.14: compatible")

	var output bytes.Buffer
	require.NoError(t, Run(&output))
	text := output.String()
	require.Contains(t, text, "python_tracer_version: <exact ddtrace release>")
	require.Contains(t, text, "ddtest testdrive --framework pytest --all --check-only --yes")
	require.Contains(t, text, "ddtest testdrive --framework pytest --all --yes --tracer-version <exact ddtrace release>")
	require.NotContains(t, text, "--agent")
}

// The GitLab fixture is flask_user_management_example's pipeline: a global
// before_script creates and activates a virtual environment, and two jobs run
// pytest in it.
func TestPythonGitLabFixture(t *testing.T) {
	root := copyFixture(t, "gitlab-pytest")
	t.Chdir(root)
	useCIProvider(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	config := readFixtureFile(t, root, ".gitlab-ci.yml")
	for _, test := range []string{"  - python -m pytest\n", "  - python -m pytest --cov-report term-missing --cov=project\n"} {
		config = strings.Replace(config, test, strings.ReplaceAll(gitLabSetupEntry(gitLabPythonSetup), "\n    ", "\n  ")[2:]+test, 1)
	}
	writeFile(t, root, ".gitlab-ci.yml", config)
	check := pythonRuntimeCheckForSite(t, root, "datadoghq.com")
	require.Equal(t, "compatible", check.Status, pythonFindings(check))
	require.Len(t, check.Jobs, 2)
}

// The Buildkite fixture is pybuildkite's pipeline: pytest in a virtual
// environment on python:3.8, which ddtrace no longer supports.
func TestPythonBuildkiteFixture(t *testing.T) {
	root := copyFixture(t, "buildkite-pytest")
	t.Chdir(root)
	useCIProvider(t, "buildkite")
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	pipeline := readFixtureFile(t, root, ".buildkite/pipeline.yml")
	pipeline = strings.Replace(pipeline, "      - python3 -m pytest\n", buildkiteSnippetText("python", "4.16.0", "datadoghq.com", "      ")+"      - python3 -m pytest\n", 1)
	pipeline = strings.Replace(pipeline, "        image: 'python:3.8'\n", "        image: 'python:3.8'\n        environment:\n          - DD_API_KEY\n", 1)
	writeFile(t, root, ".buildkite/pipeline.yml", pipeline)
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "incompatible", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "Python 3.8 does not satisfy")

	writeFile(t, root, ".buildkite/pipeline.yml", strings.Replace(pipeline, "image: 'python:3.8'", "image: 'python:3.12'", 1))
	check = pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "compatible", check.Status, pythonFindings(check))
}

// The Jenkins fixture is python-jenkins-pipeline's: pytest in a virtual
// environment on any agent. The Datadog Jenkins plugin installs the latest
// ddtrace with the agent's default pip, so ddtest asks for the agents'
// Python, the plugin's facts and, since the stage activates another Python,
// that the agent's pip is the tests' Python; the floating release is a
// stated caveat.
func TestPythonJenkinsFixture(t *testing.T) {
	root := copyFixture(t, "jenkins-pytest")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "pytest -v test", scope.Commands[0].Command)

	var output bytes.Buffer
	require.NoError(t, Run(&output))
	require.Contains(t, output.String(), "languages: ['PYTHON']")
	require.Contains(t, output.String(), "pip install -U ddtrace")
	require.Contains(t, output.String(), "ddtest testdrive --framework pytest --all --yes\n")

	jenkinsfile := readFixtureFile(t, root, "Jenkinsfile")
	jenkinsfile = strings.Replace(jenkinsfile, "    agent any\n", "    agent any\n    options {\n        datadog(testOptimization: [enabled: true, serviceName: 'python-jenkins-pipeline', languages: ['PYTHON']])\n    }\n", 1)
	writeFile(t, root, "Jenkinsfile", jenkinsfile)
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "inconclusive", check.Status)
	reason := check.Jobs[0].Reason
	for _, fact := range []string{"--ci-confirm jenkins-plugin", "jenkins-plugin-mode=agentless:<site>|agent", "--ci-confirm " + jenkinsRuntimeMatches, "--ci-runtime python=<version>"} {
		require.Contains(t, reason, fact)
	}

	statement, err := ParseCIRuntime("python=3.11")
	require.NoError(t, err)
	require.NoError(t, UseCIStatements([]CIRuntimeStatement{statement}, []CIFact{{Key: jenkinsPluginFact}, {Key: jenkinsModeFact, Value: "agentless:datadoghq.com"}, {Key: jenkinsRuntimeMatches}}))
	t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
	check = pythonRuntimeCheckForSite(t, root, "datadoghq.com")
	require.Equal(t, "compatible", check.Status, pythonFindings(check))
	require.True(t, check.Jobs[0].TracerFloating)
	require.Equal(t, "latest", check.Jobs[0].TracerRequested)
	caveats := strings.Join(CISetupCaveats(&check), "\n")
	require.Contains(t, caveats, "runs pip install -U ddtrace on the build agent when each build runs and cannot pin a release")

	// The plugin reports to the confirmed Agentless site.
	check = pythonRuntimeCheckForSite(t, root, "datadoghq.eu")
	require.Equal(t, "inconclusive", check.Status)
	require.Contains(t, check.Jobs[0].Reason, "reports to datadoghq.com")
}

// The plugin joins its PYTHONPATH and the pipeline's with a space and sets
// it once per agent: a PYTHONPATH in an environment directive breaks.
func TestPythonJenkinsEnvironmentPythonpath(t *testing.T) {
	for name, jenkinsfile := range map[string]string{
		"pipeline": "pipeline {\n  agent { label 'python' }\n  options { datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['PYTHON']]) }\n  environment { PYTHONPATH = 'src' }\n  stages {\n    stage('Test') { steps { sh 'pytest tests' } }\n  }\n}\n",
		"stage":    "pipeline {\n  agent { label 'python' }\n  options { datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['PYTHON']]) }\n  stages {\n    stage('Test') {\n      environment { PYTEST_ADDOPTS = '-x' }\n      steps { sh 'pytest tests' }\n    }\n  }\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := jenkinsRepository(t, jenkinsfile)
			writeFile(t, root, "pytest.ini", "[pytest]\n")
			statement, err := ParseCIRuntime("python=3.12")
			require.NoError(t, err)
			require.NoError(t, UseCIStatements([]CIRuntimeStatement{statement}, []CIFact{{Key: jenkinsPluginFact}, {Key: jenkinsModeFact, Value: "agent"}}))
			t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
			check := pythonRuntimeCheckForSite(t, root, "")
			require.Equal(t, "incompatible", check.Status, pythonFindings(check))
			require.Contains(t, check.Jobs[0].Reason, "comes from an environment directive or withEnv, which the Datadog Jenkins plugin overwrites")
		})
	}
	// Extending it in the test's sh script keeps the tracer.
	root := jenkinsRepository(t, "pipeline {\n  agent { label 'python' }\n  options { datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['PYTHON']]) }\n  stages {\n    stage('Test') { steps { sh '''\n      export PYTHONPATH=\"$PYTHONPATH:src\"\n      pytest tests\n    ''' } }\n  }\n}\n")
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	statement, err := ParseCIRuntime("python=3.12")
	require.NoError(t, err)
	require.NoError(t, UseCIStatements([]CIRuntimeStatement{statement}, []CIFact{{Key: jenkinsPluginFact}, {Key: jenkinsModeFact, Value: "agent"}}))
	t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "compatible", check.Status, pythonFindings(check))
}

// Every provider's instructions carry Python's setup values and rules, and
// the standalone local check instead of the coordinated agent handoff.
func TestPythonProviderInstructions(t *testing.T) {
	for provider, expected := range map[string][]string{
		"gitlab":    {`LANGUAGES="python"`, `PYTHON_TRACER_VERSION="<exact ddtrace release>"`, ".dd_civis_env", "pass_env"},
		"circleci":  {"languages: python", "python_tracer_version:", "cimg/python:3.12"},
		"buildkite": {`DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES="python"`, `DD_SET_TRACER_VERSION_PYTHON="<exact ddtrace release>"`},
		"jenkins":   {"languages: ['PYTHON']", "cannot pin one", "PEP 668"},
	} {
		t.Run(provider, func(t *testing.T) {
			useCIProvider(t, provider)
			text := instructions("python", "pytest")
			for _, fragment := range append(expected, "ddtest testdrive --framework pytest --all --check-only --yes", "Exclude .testoptimization/ from Git") {
				require.Contains(t, text, fragment)
			}
			require.NotContains(t, text, "--agent --all")
		})
	}
}

// On a self-hosted GitHub runner, the setup-python version is the job's
// Python, so a statement that differs conflicts.
func TestPythonStatedRuntimeConflictsWithSetupPython(t *testing.T) {
	root := pythonWorkflow(t, "jobs:\n  test:\n    runs-on: [self-hosted, linux]\n    steps:\n      - uses: actions/setup-python@v5\n        with:\n          python-version: '3.11'\n      - run: python -m pytest\n")
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	t.Chdir(root)
	useCIProvider(t, "github")
	statement, err := ParseCIRuntime("test:python=3.12")
	require.NoError(t, err)
	require.NoError(t, UseCIStatements([]CIRuntimeStatement{statement}, nil))
	t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
	var conflict *CIRuntimeConflict
	require.ErrorAs(t, CheckCIRuntimeStatements(root), &conflict)
	require.Contains(t, conflict.Error(), "contradicts Python 3.11, which actions/setup-python selects")
}

// A stage's PYTHONPATH, when the pipeline sets one too, is reported as
// overwritten by the plugin, with how it composes the value.
func TestPythonJenkinsPluginComposesPythonpath(t *testing.T) {
	root := jenkinsRepository(t, "pipeline {\n  agent { label 'python' }\n  options { datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['PYTHON']]) }\n  environment { PYTHONPATH = 'src' }\n  stages {\n    stage('Test') {\n      environment { PYTHONPATH = 'lib' }\n      steps { sh 'pytest tests' }\n    }\n  }\n}\n")
	workflows, err := jenkins{}.readWorkflows(root)
	require.NoError(t, err)
	require.Contains(t, strings.Join(workflows[0].Review, "\n"), "PYTHONPATH set in")
	require.Contains(t, strings.Join(workflows[0].Review, "\n"), "followed by a space and the PYTHONPATH of the first step it configures there")
}
