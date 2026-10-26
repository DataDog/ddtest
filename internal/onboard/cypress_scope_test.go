package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCypressRunStepOSMatrixKeepsEachRunnerPlatform(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix: {os: [ubuntu-latest, macos-latest, windows-latest]}
    steps:
      - run: npx cypress run
        shell: bash
`)
	scope, err := DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	platforms := map[string]bool{}
	for _, entry := range scope.Commands {
		require.Nil(t, entry.Cypress, "a run: step keeps its CLI execution path")
		platforms[entry.Platform] = true
		require.Len(t, entry.Locations, 1)
	}
	require.Equal(t, map[string]bool{"linux": true, "darwin": true, "windows": true}, platforms)

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no hosted runner row matches this host")
	}
	// Default mode selects this host's row; the others stay unvalidated.
	selected, err := CypressExecutionCommand(root, "")
	require.NoError(t, err)
	require.Equal(t, runtime.GOOS, selected.Platform)
}

func TestCypressActionSpecKeepsBraceGlobs(t *testing.T) {
	step := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"spec": "cypress/e2e/{a,b}.cy.js\ncypress/e2e/c.cy.js\n"}}
	result := resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, step)
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	// Like the action, newlines become commas and Cypress parses the braces.
	require.Equal(t, "cypress/e2e/{a,b}.cy.js,cypress/e2e/c.cy.js", result.Tests[0].Cypress.Options["spec"])
}

func TestCypressPrefixCommandRunsFromTheCallerDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{}`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "frontend"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "frontend", "package.json"), []byte(`{"devDependencies":{"cypress":"15.1.0"},"scripts":{"test":"cypress run"}}`), 0644))
	for _, command := range []string{"npm --prefix frontend test", "npm --prefix=frontend test"} {
		entry, err := CypressExecutionCommand(root, command)
		require.NoError(t, err, command)
		require.Equal(t, command, entry.Command)
		require.Equal(t, ".", entry.Directory, "the prefix is relative to the caller, not to frontend")
		require.Equal(t, "frontend", entry.CypressPackage)
	}
}

func TestGitHubRemotePathAcceptsSSHURLs(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/DataDog/ddtest.git":       "DataDog/ddtest.git",
		"git@github.com:DataDog/ddtest.git":           "DataDog/ddtest.git",
		"ssh://git@github.com/DataDog/ddtest.git":     "DataDog/ddtest.git",
		"ssh://git@github.com:22/DataDog/ddtest.git":  "DataDog/ddtest.git",
		"ssh://git@example.com/DataDog/ddtest.git":    "ssh://git@example.com/DataDog/ddtest.git",
		"https://gitlab.com/DataDog/ddtest.git":       "https://gitlab.com/DataDog/ddtest.git",
		"ssh://git@github.com.evil.test/DataDog/repo": "ssh://git@github.com.evil.test/DataDog/repo",
	} {
		require.Equal(t, want, githubRemotePath(remote), remote)
	}
}

func TestCypressCIContextsReadSSHOrigins(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "ssh://git@github.com/DataDog/ddtest.git"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	contexts := cypressCIContexts(root)
	require.NotEmpty(t, contexts, "fork and missing-secret contexts are still checked")
	require.Equal(t, "DataDog/ddtest", contexts[0]["github.repository"])
}
