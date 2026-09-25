// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	repositoryRoot := newJestRepository(t, `
name: tests
jobs:
  test:
    steps:
      - uses: datadog/test-visibility-github-action@v3
      - run: npm test
`)

	var output bytes.Buffer
	t.Chdir(repositoryRoot)
	if err := Run(&output); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	if !strings.Contains(output.String(), "already appears in every detected test workflow") {
		t.Fatalf("Run() did not recognize the existing setup:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "ddtest testdrive") {
		t.Fatalf("Run() did not print the next step:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "share the validation verdict") {
		t.Fatalf("Run() did not tell the agent to share the report:\n%s", output.String())
	}
}

func TestRunRequiresActionInEveryTestJob(t *testing.T) {
	repositoryRoot := newJestRepository(t, `
name: tests
jobs:
  unit:
    steps:
      - uses: datadog/test-visibility-github-action@v3
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
	repositoryRoot := newJestRepository(t, "name: lint\njobs:\n  lint:\n    steps:\n      - run: npx eslint .\n")

	t.Chdir(repositoryRoot)
	err := Run(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "could not find a GitHub Actions workflow that runs jest") {
		t.Fatalf("Run() error = %v", err)
	}
}

func newJestRepository(t *testing.T, workflow string) string {
	t.Helper()
	repositoryRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repositoryRoot, "package.json"), []byte(`{
  "scripts": {"test": "jest"},
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

func TestInstructionsUseV3WithoutTracerPinsOrHTML(t *testing.T) {
	for language, framework := range map[string]string{"javascript": "jest", "python": "pytest", "ruby": "rspec"} {
		output := instructions(language, framework)
		for _, expected := range []string{"datadog/test-visibility-github-action@v3", "Results JSON", "JavaScript and Python fallback installations are temporary", "Ruby fallback uses `bundle add datadog-ci`", "Fix known runtime incompatibilities", "preserve the workflow", "--check-only", "--tracer-version"} {
			if !strings.Contains(output, expected) {
				t.Errorf("%s instructions missing %q", language, expected)
			}
		}
		for _, absent := range []string{"-tracer-version:", "Open report:", "report.html", "__TRACER_SETTING__"} {
			if strings.Contains(output, absent) {
				t.Errorf("%s instructions still contain %q", language, absent)
			}
		}
	}
}
