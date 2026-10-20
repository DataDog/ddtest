// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestRunPrintsJestGitHubInstructions(t *testing.T) {
	repositoryRoot := newJestRepository(t, `
name: tests
jobs:
  test:
    steps:
      - run: npm test
`)

	var output bytes.Buffer
	t.Chdir(repositoryRoot)
	if err := Run(&output); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	for _, expected := range []string{
		"DDTest found javascript, jest, and GitHub Actions.",
		".github/workflows/test.yml",
		"datadog/test-visibility-github-action@v3",
		"api_key: ${{ secrets.DD_API_KEY }}",
		"NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }}",
		"ddtest testdrive",
		"share local compatibility",
		"Ask a human to connect Datadog",
		"without sharing the key itself",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("Run() output does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Index(output.String(), "ddtest testdrive") > strings.Index(output.String(), "Ask a human to connect Datadog") {
		t.Fatalf("API key setup must come after the local testdrive:\n%s", output.String())
	}
}

func TestRunRecognizesExistingGitHubAction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		languages  string
		configured bool
	}{
		{name: "JavaScript", languages: "js", configured: true},
		{name: "multiple languages", languages: "python js", configured: true},
		{name: "all languages", languages: "all", configured: true},
		{name: "only Python", languages: "python"},
		{name: "missing languages"},
		{name: "dynamic languages", languages: "${{ matrix.languages }}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repositoryRoot := newJestRepository(t, fmt.Sprintf(`
name: tests
jobs:
  test:
    steps:
      - uses: datadog/test-visibility-github-action@v3
        with:
          languages: %q
      - run: npm test
`, tc.languages))

			var output bytes.Buffer
			t.Chdir(repositoryRoot)
			if err := Run(&output); err != nil {
				t.Fatalf("Run() unexpected error: %v", err)
			}

			if strings.Contains(output.String(), "already appears in every detected test workflow") != tc.configured {
				t.Fatalf("Run() recognized setup incorrectly:\n%s", output.String())
			}
			if !tc.configured && !strings.Contains(output.String(), "languages: js") {
				t.Fatalf("Run() did not print JavaScript setup instructions:\n%s", output.String())
			}
			if !strings.Contains(output.String(), "ddtest testdrive") {
				t.Fatalf("Run() did not print the next step:\n%s", output.String())
			}
			if tc.configured && !strings.Contains(output.String(), "share the validation verdict") {
				t.Fatalf("Run() did not tell the agent to share the report:\n%s", output.String())
			}
		})
	}
}

func TestRunRequiresActionInEveryTestJob(t *testing.T) {
	repositoryRoot := newJestRepository(t, `
name: tests
jobs:
  unit:
    steps:
      - uses: datadog/test-visibility-github-action@v3
        with:
          languages: js
      - run: npm test
  integration:
    steps:
      - run: npm test
`)
	var output bytes.Buffer
	t.Chdir(repositoryRoot)
	if err := Run(&output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "already appears in every detected test workflow") {
		t.Fatalf("Run() treated a partially configured workflow as complete:\n%s", output.String())
	}
}

func TestRunTreatsRepositoryRootAsLiteralPath(t *testing.T) {
	parent := t.TempDir()
	repositoryRoot := filepath.Join(parent, "project[old]")
	if err := os.Mkdir(repositoryRoot, 0755); err != nil {
		t.Fatal(err)
	}
	fixture := newJestRepository(t, "name: tests\njobs:\n  test:\n    steps:\n      - run: npm test\n")
	if err := os.Rename(filepath.Join(fixture, "package.json"), filepath.Join(repositoryRoot, "package.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(fixture, ".github"), filepath.Join(repositoryRoot, ".github")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repositoryRoot)
	if err := Run(&bytes.Buffer{}); err != nil {
		t.Fatalf("Run() failed for literal repository path: %v", err)
	}
}

func TestRunRequiresGitHubJestWorkflow(t *testing.T) {
	for _, workflow := range []string{
		"name: lint\njobs:\n  lint:\n    steps:\n      - run: npm run lint\n",
		"name: Jest lint\njobs:\n  lint:\n    steps:\n      - name: Jest lint\n        run: npm run lint\n",
		"jobs:\n  lint:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: js\n      - run: npm run lint\n",
	} {
		t.Run(workflow, func(t *testing.T) {
			repositoryRoot := newJestRepository(t, workflow)

			t.Chdir(repositoryRoot)
			err := Run(&bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "could not find a GitHub Actions workflow that runs jest") {
				t.Fatalf("Run() error = %v", err)
			}
		})
	}
}

func TestRunRequiresRepositoryCIConfiguration(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitlab-ci.yml"), []byte("test:\n  script: npm test\n"), 0644))
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Chdir(root)
	var output bytes.Buffer
	require.ErrorContains(t, Run(&output), "no supported CI provider detected in repository")
	require.Empty(t, output.String())
}

func TestRunChecksActionOrderAndCondition(t *testing.T) {
	for _, tc := range []struct {
		name, steps string
		configured  bool
	}{
		{"before tests", "- uses: datadog/test-visibility-github-action@v3\n  with: {languages: js}\n- run: npm test", true},
		{"after tests", "- run: npm test\n- uses: datadog/test-visibility-github-action@v3\n  with: {languages: js}", false},
		{"between test steps", "- run: npm test\n- uses: datadog/test-visibility-github-action@v3\n  with: {languages: js}\n- run: npm test", false},
		{"disabled", "- uses: datadog/test-visibility-github-action@v3\n  if: false\n  with: {languages: js}\n- run: npm test", false},
		{"disabled expression", "- uses: datadog/test-visibility-github-action@v3\n  if: ${{ false }}\n  with: {languages: js}\n- run: npm test", false},
		{"enabled", "- uses: datadog/test-visibility-github-action@v3\n  if: true\n  with: {languages: js}\n- run: npm test", true},
		{"unknown condition", "- uses: datadog/test-visibility-github-action@v3\n  if: ${{ matrix.instrument }}\n  with: {languages: js}\n- run: npm test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newJestRepository(t, "jobs:\n  tests:\n    steps:\n      "+strings.ReplaceAll(tc.steps, "\n", "\n      ")+"\n")
			t.Chdir(root)
			var output bytes.Buffer
			require.NoError(t, Run(&output))
			require.Equal(t, tc.configured, strings.Contains(output.String(), "already appears in every detected test workflow"), output.String())
		})
	}
}

func TestRunIdentifiesJavaScriptInvocations(t *testing.T) {
	for _, tc := range []struct {
		command string
		matched bool
	}{
		{"npm run ci", true}, {"yarn ci", true}, {"pnpm run ci", true}, {"bun run ci", true},
		{"npm run nested", true}, {"npm run cycle", false},
		{"npx jest --ci", true}, {"npm exec -- jest --ci", true},
		{"npm install --no-save jest\nnpm run lint", false},
		{"echo jest\nnpm run lint", false}, {"# npx jest\nnpm run lint", false},
		{"npm run lint", false}, {"npm ci && npm run ci", true},
		{"npm ci", false}, {"npm install", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := newJestRepository(t, "jobs:\n  tests:\n    steps:\n      - run: |\n          "+strings.ReplaceAll(tc.command, "\n", "\n          ")+"\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"ci":"jest --ci","nested":"npm run ci","cycle":"npm run cycle","lint":"eslint ."},"devDependencies":{"jest":"30.0.0"}}`), 0644))
			t.Chdir(root)
			var output bytes.Buffer
			err := Run(&output)
			if tc.matched {
				require.NoError(t, err)
				require.Contains(t, output.String(), "languages: js")
			} else if tc.command == "npm run cycle" {
				require.NoError(t, err)
				require.Contains(t, output.String(), "CI command discovery is inconclusive")
				require.NotContains(t, output.String(), "already appears in every detected test workflow")
			} else {
				require.ErrorContains(t, err, "could not find a GitHub Actions workflow that runs jest")
			}
		})
	}
}

func TestRunDistinguishesToxTestEnvironments(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		matched       bool
	}{
		{"inherited pytest", "uv run --locked tox run -e py3.13", true},
		{"typing", "uv run --locked tox run -e typing", false},
		{"typing long flag", "tox run --environment=typing", false},
		{"typing short flag", "tox -etyping", false},
		{"mixed environments", "tox -e typing,py3.13", true},
		{"default environments", "tox", true},
		{"direct pytest", "python -m pytest", true},
		{"dependency installation", "python -m pip install pytest", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			contents, err := os.ReadFile("../platform/testdata/detection/itsdangerous/pyproject.toml")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), contents, 0644))
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
			workflow := "jobs:\n  tests:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python}\n      - run: " + tc.command + "\n  typing:\n    steps:\n      - run: uv run --locked tox run -e typing\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(workflow), 0644))
			t.Chdir(root)
			var output bytes.Buffer
			err = Run(&output)
			if tc.matched {
				require.NoError(t, err)
				require.Contains(t, output.String(), "already appears in every detected test workflow")
			} else {
				require.ErrorContains(t, err, "could not find a GitHub Actions workflow that runs pytest")
			}
		})
	}
}

func newJestRepository(t *testing.T, workflow string) string {
	t.Helper()
	repositoryRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repositoryRoot, "package.json"), []byte(`{
  "scripts": {"test": "jest", "lint": "eslint ."},
  "devDependencies": {"jest": "30.0.0"}
}`), 0644); err != nil {
		t.Fatal(err)
	}
	workflowDirectory := filepath.Join(repositoryRoot, ".github", "workflows")
	if err := os.MkdirAll(workflowDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDirectory, "test.yml"), []byte(workflow), 0644); err != nil {
		t.Fatal(err)
	}
	return repositoryRoot
}

func TestInstructionsUseV3WithoutTracerPinsAndKeepBothReports(t *testing.T) {
	for language, framework := range map[string]string{"javascript": "jest"} {
		output := instructions(language, framework)
		for _, expected := range []string{"datadog/test-visibility-github-action@v3", "Results JSON", "report.html", "testdrive.json", "--tracer-version", "summary.final_response"} {
			if !strings.Contains(output, expected) {
				t.Errorf("%s instructions missing %q", language, expected)
			}
		}
		for _, absent := range []string{"-tracer-version:", "__TRACER_SETTING__"} {
			if strings.Contains(output, absent) {
				t.Errorf("%s instructions still contain %q", language, absent)
			}
		}
	}
}

func TestPythonInstructionsKeepSelectedLanguageSetup(t *testing.T) {
	for _, fixture := range []struct{ language, framework, initialization string }{
		{"python", "pytest", "PYTEST_ADDOPTS=--ddtrace"},
	} {
		t.Run(fixture.framework, func(t *testing.T) {
			output := instructions(fixture.language, fixture.framework)
			for _, required := range []string{fixture.initialization, "ddtest testdrive --framework " + fixture.framework, "-tracer-version: '' # Latest release"} {
				require.Contains(t, output, required)
			}
			for _, invalid := range []string{"NODE_OPTIONS", "js-tracer-version", "Jest", "Vitest", "package.json", "coverageThreshold", "__FRAMEWORK__", "[[if", "telemetry_only", "UNVALIDATED", "INCOMPLETE", "--agent", "--format", "summary.final_response", "Cleanup after local validation"} {
				require.NotContains(t, output, invalid)
			}
			_, yamlText, found := strings.Cut(output, "```yaml\n")
			require.True(t, found)
			yamlText, _, _ = strings.Cut(yamlText, "```")
			var steps []struct {
				Uses string
				With map[string]string
			}
			require.NoError(t, yaml.Unmarshal([]byte(yamlText), &steps))
			require.Len(t, steps, 1)
			require.Equal(t, fixture.language, steps[0].With["languages"])
		})
	}
}
