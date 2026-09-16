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
	if err := Run(repositoryRoot, &output); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	for _, expected := range []string{
		"DDTest found JavaScript, Jest, and GitHub Actions.",
		".github/workflows/test.yml",
		"datadog/test-visibility-github-action@v3",
		"api_key: ${{ secrets.DD_API_KEY }}",
		"NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }}",
		"ddtest testdrive",
		"post every `Open report:` link",
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
	if err := Run(repositoryRoot, &output); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	if !strings.Contains(output.String(), "already appears in every detected test workflow") {
		t.Fatalf("Run() did not recognize the existing setup:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "ddtest testdrive") {
		t.Fatalf("Run() did not print the next step:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "post every `Open report:` link") {
		t.Fatalf("Run() did not tell the agent to share the report:\n%s", output.String())
	}
}

func TestRunRequiresGitHubJestWorkflow(t *testing.T) {
	repositoryRoot := newJestRepository(t, "name: lint\njobs:\n  lint:\n    steps:\n      - run: npm run lint\n")

	err := Run(repositoryRoot, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "could not find a GitHub Actions workflow that runs Jest") {
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
