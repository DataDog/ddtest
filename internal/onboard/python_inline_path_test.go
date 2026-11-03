// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// qwedPipeline is qwed-ucp's Buildkite step: PYTHONPATH=src pytest in a
// python image, here after the Datadog setup snippet when setup is set.
func qwedPipeline(setup bool, test string) string {
	commands := "      - pip install -e \".[dev]\"\n"
	if setup {
		commands += buildkiteSnippetText("python", "4.16.0", "datadoghq.com", "      ")
	}
	return "steps:\n" + buildkiteDockerStep(`":python: Test (3.12)"`, "python:3.12", commands+test)
}

// An inline PYTHONPATH=src pytest is the command's environment: discovery
// validates it with that path, and once the setup precedes it the CI check
// prescribes the exact extending rewrite, which it then accepts. Every
// provider's setup puts the tracer on PYTHONPATH, so the command's own value
// would drop it.
func TestPythonInlinePythonpathNeedsTheExactEdit(t *testing.T) {
	jenkinsStage := func(script string) string {
		return "pipeline {\n  agent { label 'python' }\n  options { datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['PYTHON']]) }\n  stages {\n    stage('Test') { steps { sh '''\n" + script + "    ''' } }\n  }\n}\n"
	}
	circleConfig := func(command string) string {
		persist := ""
		if !strings.Contains(command, "PYTHONPATH=") {
			persist = "      " + circleCI{}.setupTerms().PersistEntry(`export PYTHONPATH="$PYTHONPATH:src:lib"`) + "\n"
		}
		return "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n    docker:\n      - image: cimg/python:3.12\n    steps:\n      - checkout\n      - " + strings.ReplaceAll(circlePythonOrb, "\n    ", "\n          ") + persist + "      - run:\n          name: tests\n          command: |\n" + command + "workflows:\n  main:\n    jobs: [test]\n"
	}
	for _, tc := range []struct {
		name, edit  string
		repository  func(t *testing.T, test string) string
		inline, fix string
	}{
		{
			name: "gitlab",
			repository: func(t *testing.T, test string) string {
				return gitLabPythonRepository(t, "pytest:\n  image: python:3.12\n  script:\n"+gitLabSetupEntry(gitLabPythonSetup)+test)
			},
			inline: "    - PYTHONPATH=src python -m pytest tests\n",
			fix:    "    - export PYTHONPATH=\"$PYTHONPATH:src\"\n    - python -m pytest tests\n",
			edit:   "`PYTHONPATH=src python -m pytest tests` replaces the PYTHONPATH the Datadog setup script sets, which carries the tracer, so its tests run untraced. Replace it, in the same place of the same script, with the two commands `export PYTHONPATH=\"$PYTHONPATH:src\"` and `python -m pytest tests`.",
		},
		{
			name: "buildkite",
			repository: func(t *testing.T, test string) string {
				root := buildkiteRepository(t, qwedPipeline(true, test))
				writeFile(t, root, "pytest.ini", "[pytest]\n")
				return root
			},
			inline: "      - PYTHONPATH=src pytest tests/ -q --timeout=30 --cov=qwed_ucp --cov-report=xml\n",
			fix:    "      - export PYTHONPATH=\"$$PYTHONPATH:src\"\n      - pytest tests/ -q --timeout=30 --cov=qwed_ucp --cov-report=xml\n",
			edit:   "Replace it, in the same place of the step's commands (every $ written as $$), with the two commands `export PYTHONPATH=\"$$PYTHONPATH:src\"` and `pytest tests/ -q --timeout=30 --cov=qwed_ucp --cov-report=xml`.",
		},
		{
			name: "circleci",
			repository: func(t *testing.T, test string) string {
				root := circleRepository(t, circleConfig(test))
				writeFile(t, root, "pytest.ini", "[pytest]\n")
				return root
			},
			inline: "            PYTHONPATH=src:lib pytest tests\n",
			fix:    "            pytest tests\n",
			edit:   "Run `pytest tests` in its place, and add the step `- run: echo 'export PYTHONPATH=\"$PYTHONPATH:src:lib\"' >> \"$BASH_ENV\"` before that step, after the orb step",
		},
		{
			name: "jenkins",
			repository: func(t *testing.T, test string) string {
				root := jenkinsRepository(t, jenkinsStage(test))
				writeFile(t, root, "pytest.ini", "[pytest]\n")
				useCIStatements(t, []string{"python=3.12"}, jenkinsPluginFact, jenkinsModeFact+"=agent")
				return root
			},
			inline: "      PYTHONPATH=src pytest tests\n",
			fix:    "      export PYTHONPATH=\"$PYTHONPATH:src\"\n      pytest tests\n",
			edit:   "the PYTHONPATH the Datadog Jenkins plugin sets, which carries the tracer, so its tests run untraced. Replace it, in the same place of the same sh script (inside a double-quoted Groovy string, write each $ as \\$), with the two commands `export PYTHONPATH=\"$PYTHONPATH:src\"` and `pytest tests`.",
		},
		{
			name: "github",
			repository: func(t *testing.T, test string) string {
				root := pythonReviewWorkflow(t, "{}", "", test)
				t.Chdir(root)
				useCIProvider(t, "github")
				return root
			},
			inline: "PYTHONPATH=src pytest",
			fix:    "pytest\n        env: {PYTHONPATH: '${{ env.PYTHONPATH }}:src'}",
			edit:   "`PYTHONPATH=src pytest` replaces the PYTHONPATH the Datadog action sets, which carries the tracer, so its tests run untraced. Run `pytest` instead, and add PYTHONPATH: ${{ env.PYTHONPATH }}:src to the step's env, which extends the action's path.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.repository(t, tc.inline)
			scope, err := DiscoverValidationScope(root, "pytest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "src", strings.Split(scope.Commands[0].Environment["PYTHONPATH"], ":")[0])
			check := pythonRuntimeCheckForSite(t, root, "")
			require.Equal(t, "incompatible", check.Status, pythonFindings(check))
			require.Len(t, check.Jobs, 1)
			require.Equal(t, CodeCIEditRequired, check.Jobs[0].Code)
			require.Contains(t, check.Jobs[0].Reason, tc.edit)
			required := RequiredCIEdits(t.Context(), root, "python", "pytest", false)
			require.NotNil(t, required)
			require.Len(t, required.Locations, 1)
			require.Contains(t, required.Error(), tc.edit)

			// The prescribed form keeps the tracer and validates the same path.
			root = tc.repository(t, tc.fix)
			scope, err = DiscoverValidationScope(root, "pytest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "src", strings.Split(scope.Commands[0].Environment["PYTHONPATH"], ":")[0])
			check = pythonRuntimeCheckForSite(t, root, "")
			require.Equal(t, "compatible", check.Status, pythonFindings(check))
			require.Nil(t, RequiredCIEdits(t.Context(), root, "python", "pytest", false))
		})
	}
}

// A path that keeps the tracer, an empty assignment, a command that runs no
// tests and PYTHONHOME: only the empty one is an edit, PYTHONHOME stays
// unverified.
func TestPythonInlinePathEditForms(t *testing.T) {
	gitlab := gitLabCI{}
	for _, tc := range []struct {
		command []string
		edit    string
	}{
		{[]string{"PYTHONPATH=src", "COVERAGE_FILE=.c", "pytest", "-x"}, "with the two commands `export PYTHONPATH=\"$PYTHONPATH:src\"` and `COVERAGE_FILE=.c pytest -x`."},
		{[]string{"PYTHONPATH=", "pytest"}, "Remove its empty PYTHONPATH= assignment: run `pytest` instead."},
		{[]string{"PYTHONPATH=my src", "tox", "-e", "py312"}, "`export PYTHONPATH=\"$PYTHONPATH\":'my src'` and `tox -e py312`."},
		{[]string{"PYTHONPATH=" + pythonTracerPath + ":src", "pytest"}, ""},
		{[]string{"PYTHONPATH=src", "python", "-m", "mypy"}, ""},
		{[]string{"PYTHONPATH=src", "pytest", "--version"}, ""},
	} {
		edit := pythonInlinePathEdit(gitlab, [][]string{tc.command})
		if tc.edit == "" {
			require.Empty(t, edit, tc.command)
			continue
		}
		require.Contains(t, edit, tc.edit, tc.command)
	}
	_, _, err := pythonCommandEnvironment([]string{"PYTHONHOME=/opt/python", "pytest"})
	require.ErrorContains(t, err, "PYTHONHOME")
}

// The agent handoff reaches the edit instead of a scope decision: before
// the setup, ci_setup_required prescribes it with the setup; once the setup
// is there, ci_edit_required is the edit, then resume; after it, validation.
func TestPythonAgentOnboardingPrescribesThePathEdit(t *testing.T) {
	test := "      - PYTHONPATH=src pytest tests/ -q --timeout=30 --cov=qwed_ucp --cov-report=xml\n"
	root := t.TempDir()
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	writeFile(t, root, buildkiteFile, qwedPipeline(false, test))
	t.Chdir(root)
	useCIProvider(t, "buildkite")
	start := func() (AgentResult, error) {
		t.Helper()
		var output bytes.Buffer
		err := StartWithOptions(&output, StartOptions{Format: "agent"})
		var result AgentResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
		return result, err
	}
	edit := "with the two commands `export PYTHONPATH=\"$$PYTHONPATH:src\"` and `pytest tests/ -q --timeout=30 --cov=qwed_ucp --cov-report=xml`."

	result, err := start()
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", result.Code)
	require.Equal(t, "apply_source_edit", result.NextAction)
	require.Contains(t, result.Reason, "With the setup, make exactly these edits to the test commands: .buildkite/pipeline.yml / :python: Test (3.12) / step 2:")
	require.Contains(t, result.Reason, edit)

	writeFile(t, root, buildkiteFile, qwedPipeline(true, test))
	result, err = start()
	require.Error(t, err)
	require.Equal(t, "needs_input", result.Status)
	require.Equal(t, CodeCIEditRequired, result.Code)
	require.Equal(t, "apply_source_edit", result.NextAction)
	require.Empty(t, result.NextCommand)
	require.Equal(t, []string{"testdrive", "--framework", "pytest", "--all", "--agent", "--yes"}, result.ResumeCommand[1:7])
	require.Equal(t, ".buildkite/pipeline.yml / :python: Test (3.12) / step 3", result.Location)
	require.Contains(t, result.Reason, edit)
	require.Contains(t, result.Instruction, "Make only the edits reason names, exactly as given")

	writeFile(t, root, buildkiteFile, qwedPipeline(true, "      - export PYTHONPATH=\"$$PYTHONPATH:src\"\n      - pytest tests/ -q --timeout=30 --cov=qwed_ucp --cov-report=xml\n"))
	result, err = start()
	require.NoError(t, err)
	require.Equal(t, "ready", result.Status)
	require.Equal(t, "run_command", result.NextAction)
}

// A GitHub Actions step's env holds one PYTHONPATH, so a step whose commands
// set different ones is split first, each command keeping its own path; the
// split steps then get their own env edits. Commands with the same path keep
// the single env edit.
func TestPythonInlinePathEditSplitsConflictingSteps(t *testing.T) {
	github := gitHubActions{}
	edit := pythonInlinePathEdit(github, [][]string{{"PYTHONPATH=src/a", "pytest", "tests/a"}, {"PYTHONPATH=src/b", "pytest", "tests/b"}})
	require.Contains(t, edit, "`PYTHONPATH=src/a pytest tests/a`, `PYTHONPATH=src/b pytest tests/b` set different PYTHONPATH values in one step")
	require.Contains(t, edit, "move each of these commands into its own step")
	require.NotContains(t, edit, "${{ env.PYTHONPATH }}")
	edit = pythonInlinePathEdit(github, [][]string{{"PYTHONPATH=src", "pytest", "tests/a"}, {"PYTHONPATH=src", "pytest", "tests/b"}})
	require.NotContains(t, edit, "set different PYTHONPATH values")
	require.Contains(t, edit, "add PYTHONPATH: ${{ env.PYTHONPATH }}:src to the step's env")

	root := pythonReviewWorkflow(t, "{}", "", "|\n          PYTHONPATH=src/a pytest tests/a\n          PYTHONPATH=src/b pytest tests/b")
	t.Chdir(root)
	useCIProvider(t, "github")
	check := pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "incompatible", check.Status, pythonFindings(check))
	require.Equal(t, CodeCIEditRequired, check.Jobs[0].Code)
	require.Contains(t, check.Jobs[0].Reason, "set different PYTHONPATH values in one step")

	root = pythonReviewWorkflow(t, "{}", "      - run: PYTHONPATH=src/a pytest tests/a\n", "PYTHONPATH=src/b pytest tests/b")
	t.Chdir(root)
	check = pythonRuntimeCheckForSite(t, root, "")
	require.Equal(t, "incompatible", check.Status, pythonFindings(check))
	require.Contains(t, pythonFindings(check), "add PYTHONPATH: ${{ env.PYTHONPATH }}:src/b to the step's env")
	require.NotContains(t, pythonFindings(check), "set different PYTHONPATH values")
}
