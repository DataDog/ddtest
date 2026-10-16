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
      - run: pytest
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
			if !strings.Contains(output.String(), "post every `Open report:` link") {
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
