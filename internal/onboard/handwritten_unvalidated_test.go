// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// unvalidatedRepository writes a repository with files and provider's
// configuration, and selects that provider.
func unvalidatedRepository(t *testing.T, provider handWrittenProvider, config string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, contents := range files {
		writeFile(t, root, path, contents)
	}
	writeFile(t, root, provider.file, config)
	t.Chdir(root)
	useCIProvider(t, provider.name)
	return root
}

// firstJobHandWrittenSetup is the hand-written setup of the first job that
// runs framework's tests in root's provider configuration.
func firstJobHandWrittenSetup(t *testing.T, root, language, framework, site string) *handWrittenSetup {
	t.Helper()
	provider, err := currentCIProvider(root)
	require.NoError(t, err)
	workflows, err := readProviderWorkflows(provider, root)
	require.NoError(t, err)
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			if setup := firstHandWrittenSetup(root, workflow, workflow.Jobs[name], language, framework, site); setup != nil {
				return setup
			}
		}
	}
	return nil
}

const handWrittenRequirements = "pytest==8.3.5\nddtrace==4.16.1\n"

// A pytest job that enables the ddtrace plugin by hand is recognized on every
// provider without the shared syntax, with what it lacks; complete simple
// forms are checked by the pytest CI inventory.
func TestHandWrittenPythonSetups(t *testing.T) {
	agentless := map[string]string{"PYTEST_ADDOPTS": "--ddtrace", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			root := unvalidatedRepository(t, provider, provider.config(agentless, []string{"pip install -r requirements.txt"}, "pytest"), map[string]string{"requirements.txt": handWrittenRequirements})
			setup := firstJobHandWrittenSetup(t, root, "python", "pytest", "datadoghq.com")
			require.NotNil(t, setup)
			require.Equal(t, "hand-written ddtrace@4.16.1 setup (PYTEST_ADDOPTS)", setup.Reference)
			require.Equal(t, "requirements.txt", setup.Pin)
			require.Empty(t, setup.Problems)
			require.Empty(t, setup.Unresolved)
			require.Empty(t, unvalidatedHandWrittenJobs(root, activeOrDefaultCIProvider(), "python", "pytest"))
		})
	}
	provider := handWrittenProviders[0]
	for _, tc := range []struct {
		name    string
		env     map[string]string
		before  []string
		command string
		files   map[string]string
		// reference is the setup's; problem and unresolved, one of what it
		// lacks and of what ddtest cannot read.
		reference, problem, unresolved string
	}{
		{name: "inline option", env: with(agentless, "PYTEST_ADDOPTS", ""), command: "python -m pytest --ddtrace tests", reference: "hand-written ddtrace@4.16.1 setup (--ddtrace)"},
		{name: "inline variable", env: with(agentless, "PYTEST_ADDOPTS", ""), command: "PYTEST_ADDOPTS=--ddtrace pytest", reference: "hand-written ddtrace@4.16.1 setup (PYTEST_ADDOPTS)"},
		{name: "pytest configuration", env: with(agentless, "PYTEST_ADDOPTS", ""), command: "pytest", files: map[string]string{"pytest.ini": "[pytest]\naddopts = --ddtrace\n"}, reference: "hand-written ddtrace@4.16.1 setup (pytest addopts)"},
		{name: "install command", command: "pytest", before: []string{"pip install pytest==8.3.5 ddtrace==4.17.0"}, files: map[string]string{"requirements.txt": "pytest\n"}, reference: "hand-written ddtrace@4.17.0 setup (PYTEST_ADDOPTS)"},
		{name: "lockfile", command: "uv run pytest", before: []string{"uv sync"}, files: map[string]string{"requirements.txt": "", "pyproject.toml": "[project]\nname = \"app\"\ndependencies = [\"ddtrace>=4\"]\n", "uv.lock": "version = 1\n\n[[package]]\nname = \"ddtrace\"\nversion = \"4.16.1\"\n"}, reference: "hand-written ddtrace setup (PYTEST_ADDOPTS)", unresolved: "selected dependencies ddtest cannot resolve"},
		{name: "range", command: "pytest", files: map[string]string{"requirements.txt": "ddtrace>=4.0\n"}, reference: "hand-written ddtrace setup (PYTEST_ADDOPTS)", problem: "requirements.txt requires ddtrace >=4.0, which is not an exact release"},
		{name: "no ddtrace", command: "pytest", files: map[string]string{"requirements.txt": "pytest==8.3.5\n"}, reference: "hand-written ddtrace setup (PYTEST_ADDOPTS)", unresolved: "an installation ddtest cannot resolve statically"},
		{name: "literal key", env: with(agentless, "DD_API_KEY", "0123456789abcdef"), command: "pytest", problem: "never commit the key"},
		{name: "site", command: "pytest", env: with(agentless, "DD_SITE", "datadoghq.eu"), problem: "set DD_SITE=datadoghq.com"},
		{name: "disabled", env: with(agentless, "PYTEST_ADDOPTS", "--ddtrace --no-ddtrace"), command: "pytest", problem: "--no-ddtrace, which turns the ddtrace plugin off"},
		{name: "wrapper", command: "tox -e py312", files: map[string]string{"pyproject.toml": "[tool.tox.env_run_base]\ncommands = [['pytest']]\n"}, unresolved: "cannot tell whether --ddtrace and the DD_* variables reach the pytest it runs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			if env == nil {
				env = agentless
			}
			files := map[string]string{"requirements.txt": handWrittenRequirements}
			maps.Copy(files, tc.files)
			before := append([]string{"pip install -r requirements.txt"}, tc.before...)
			root := unvalidatedRepository(t, provider, provider.config(env, before, tc.command), files)
			setup := firstJobHandWrittenSetup(t, root, "python", "pytest", "datadoghq.com")
			require.NotNil(t, setup)
			if tc.reference != "" {
				require.Equal(t, tc.reference, setup.Reference)
			}
			if tc.problem != "" {
				require.Contains(t, strings.Join(setup.Problems, " "), tc.problem)
			} else {
				require.Empty(t, setup.Problems)
			}
			if tc.unresolved != "" {
				require.Contains(t, strings.Join(setup.Unresolved, " "), tc.unresolved)
			}
			require.Empty(t, unvalidatedHandWrittenJobs(root, activeOrDefaultCIProvider(), "python", "pytest"))
		})
	}
	// Agent mode requires the same explicit confirmation as other languages.
	root := unvalidatedRepository(t, provider, provider.config(with(agentless, "DD_CIVISIBILITY_AGENTLESS_ENABLED", ""), []string{"pip install -r requirements.txt"}, "pytest"), map[string]string{"requirements.txt": handWrittenRequirements})
	require.Contains(t, firstJobHandWrittenSetup(t, root, "python", "pytest", "").gaps(), "reports through a Datadog Agent")
	require.Empty(t, unvalidatedHandWrittenJobs(root, activeOrDefaultCIProvider(), "python", "pytest"))

	// Without the plugin there is no hand-written setup: the provider's
	// support decides.
	root = unvalidatedRepository(t, provider, provider.config(map[string]string{"DD_SITE": "datadoghq.com"}, []string{"pip install -r requirements.txt"}, "pytest"), map[string]string{"requirements.txt": handWrittenRequirements})
	require.Nil(t, firstJobHandWrittenSetup(t, root, "python", "pytest", ""))
	require.Empty(t, unvalidatedHandWrittenJobs(root, gitLabCI{}, "python", "pytest"))
	if !languageSupportsCIProvider("python", gitLabCI{}) {
		var unsupported *CIProviderUnsupportedError
		require.ErrorAs(t, CheckCIProviderSupport(root, "python", "pytest"), &unsupported)
		require.Empty(t, unsupported.HandWritten)
	}
}

// A Jenkins stage that enables the ddtrace plugin by hand is reported the
// same way, before any container or plugin rule.
func TestHandWrittenPythonJenkinsSetup(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "requirements.txt", handWrittenRequirements)
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent { docker { image 'python:3.12' } }\n  environment {\n    PYTEST_ADDOPTS = '--ddtrace'\n    DD_CIVISIBILITY_AGENTLESS_ENABLED = 'true'\n    DD_API_KEY = credentials('datadog-api-key')\n  }\n  stages {\n    stage('Test') {\n      steps {\n        sh 'pip install -r requirements.txt'\n        sh 'pytest'\n      }\n    }\n  }\n}\n")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	jobs := unvalidatedHandWrittenJobs(root, activeOrDefaultCIProvider(), "python", "pytest")
	require.Empty(t, jobs)
}

// Messages about setting Test Optimization up by hand say, per language,
// whether ddtest then checks that setup.
func TestHandWrittenSetupOptionNamesValidatedLanguages(t *testing.T) {
	require.Equal(t, "JavaScript, Python, Ruby and Java", handWrittenValidatedLanguages())
	for _, tc := range []struct {
		language, reason, instruction string
	}{
		{"javascript", "which ddtest then checks like the Datadog Jenkins plugin", "Once the user sets it up by hand, rerun onboarding, which checks that setup."},
		{"ruby", "which ddtest then checks like the Datadog Jenkins plugin", "Once the user sets it up by hand, rerun onboarding, which checks that setup."},
		{"python", "which ddtest then checks like the Datadog Jenkins plugin", "Once the user sets it up by hand, rerun onboarding, which checks that setup."},
		{"java", "which ddtest then checks like the Datadog Jenkins plugin", "Once the user sets it up by hand, rerun onboarding, which checks that setup."},
	} {
		err := &CIProviderUnsupportedError{Language: tc.language, Framework: "tests", Provider: "Jenkins", Reason: "Jenkinsfile / Test: it runs in a container.", Setup: "Datadog Jenkins plugin"}
		require.Contains(t, err.Error(), tc.reason, tc.language)
		result := ProviderUnsupportedResult(err)
		require.Equal(t, "ci_setup_unsupported", result.Code)
		require.Contains(t, result.Instruction, tc.instruction, tc.language)
	}
	for _, fact := range jenkinsFacts {
		require.NotContains(t, fact.Remedy, "cannot validate", fact.Key)
	}
}
