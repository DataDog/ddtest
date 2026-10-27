// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package environment

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitHubFindTestWorkflowsWorkingDirectories(t *testing.T) {
	for _, tc := range []struct {
		name, workflow, job, step string
		found                     bool
	}{
		{"workflow default", "packages/web", "", "", true},
		{"job default", "", "packages/web", "", true},
		{"step directory", "", "", "packages/web", true},
		{"job overrides workflow", "packages/lint", "packages/web", "", true},
		{"step overrides job", "packages/lint", "packages/lint", "packages/web", true},
		{"job lint overrides tests", "packages/web", "packages/lint", "", false},
		{"step lint overrides tests", "packages/web", "packages/web", "packages/lint", false},
		{"root script", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"", "packages/web", "packages/lint"} {
				command := "eslint ."
				if dir == "packages/web" {
					command = "jest --ci"
				}
				require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
				require.NoError(t, os.WriteFile(filepath.Join(root, dir, "package.json"), fmt.Appendf(nil, `{"scripts":{"ci":%q}}`, command), 0644))
			}
			workflow := fmt.Sprintf(`defaults:
  run:
    working-directory: %q
jobs:
  tests:
    defaults:
      run:
        working-directory: %q
    steps:
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js}
      - run: npm run ci
        working-directory: %q
`, tc.workflow, tc.job, tc.step)
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(workflow), 0644))
			workflows, configured, err := NewGitHub().FindTestWorkflows(root, "javascript", "jest")
			require.NoError(t, err)
			require.Equal(t, tc.found, len(workflows) == 1)
			require.Equal(t, tc.found, len(configured) == 1)
		})
	}
}

func TestGitHubFindTestWorkflowsCommandKinds(t *testing.T) {
	for _, tc := range []struct {
		language, framework, command string
		tests                        bool
	}{
		{"javascript", "cypress", "npx cypress verify", false},
		{"javascript", "cypress", "npx cypress install", false},
		{"javascript", "cypress", "npx cypress cache list", false},
		{"javascript", "cypress", "npx cypress run", true},
		{"ruby", "rspec", "bundle exec rake rubocop", false},
		{"ruby", "rspec", "bundle exec rake rubocop:autocorrect", false},
		{"ruby", "rspec", "bundle exec rake -f Rakefile lint", false},
		{"ruby", "rspec", "bundle exec rake --tasks", false},
		{"ruby", "rspec", "bundle exec rake", true},
		{"ruby", "rspec", "bundle exec rake spec", true},
		{"ruby", "rspec", "bundle exec rake rubocop spec", true},
		{"ruby", "minitest", "bundle exec rake test", true},
		{"ruby", "minitest", "bundle exec rake test:unit", true},
		{"ruby", "minitest", "bundle exec rake default", true},
		{"python", "pytest", "uv run --locked pytest", true},
		{"python", "pytest", "uv run --no-default-groups --group dev pytest", true},
		{"python", "pytest", "uv run --group=dev --frozen python -m pytest", true},
		{"python", "pytest", "uv run --no-sync -- pytest", true},
		{"python", "pytest", "uv run --group dev ruff check", false},
		{"python", "pytest", "uv run --group", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
			path := filepath.Join(root, ".github/workflows/test.yml")
			workflow := fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - run: %s\n", tc.command)
			require.NoError(t, os.WriteFile(path, []byte(workflow), 0644))
			workflows, configured, err := NewGitHub().FindTestWorkflows(root, tc.language, tc.framework)
			require.NoError(t, err)
			require.Equal(t, tc.tests, len(workflows) == 1)
			require.Empty(t, configured)

			// A preparation step before setup must not invalidate the actual tests
			// that run after the Datadog action.
			workflow += "      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: all}\n      - run: " + tc.framework + " run\n"
			require.NoError(t, os.WriteFile(path, []byte(workflow), 0644))
			workflows, configured, err = NewGitHub().FindTestWorkflows(root, tc.language, tc.framework)
			require.NoError(t, err)
			require.Len(t, workflows, 1)
			require.Equal(t, !tc.tests, len(configured) == 1)
		})
	}
}

func TestGitHubFindTestWorkflowsRecognizesMavenTestPhases(t *testing.T) {
	for _, tc := range []struct {
		command string
		tests   bool
	}{
		{"mvn -B verify", true},
		{"./mvnw test -pl service", true},
		{"MAVEN_OPTS=-Xmx1g mvn package --file pom.xml", true},
		{"mvn -B compile && mvn -B test", true},
		{"mvn dependency:go-offline", false},
		{"mvn -f test compile", false},
		{"gradle test", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
			workflow := fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java}\n      - run: %s\n", tc.command)
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(workflow), 0644))
			workflows, configured, err := NewGitHub().FindTestWorkflows(root, "java", "junit5")
			require.NoError(t, err)
			require.Equal(t, tc.tests, len(workflows) == 1)
			require.Equal(t, tc.tests, len(configured) == 1, "the action selects java")
		})
	}
}
