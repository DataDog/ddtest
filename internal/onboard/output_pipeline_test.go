// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A pipeline whose later commands only format or save the first command's
// output is that command; any other pipeline stays unresolved.
func TestOutputPipelinesResolveToTheirFirstCommand(t *testing.T) {
	for command, want := range map[string][2]string{
		"./node_modules/.bin/cucumber-js features --format json | ./node_modules/.bin/cucumber-junit > testresults_cucumber.xml": {"./node_modules/.bin/cucumber-js features --format json", "./node_modules/.bin/cucumber-junit >testresults_cucumber.xml"},
		"npx jest | tee out.txt":                                              {"npx jest", "tee out.txt"},
		"npx jest 2>&1 | tee -a logs/jest.log":                                {"npx jest", "tee -a logs/jest.log"},
		"pytest -q |& tee pytest.log":                                         {"pytest -q", "tee pytest.log"},
		"npx tape test/*.js | npx tap-xunit > out.xml":                        {"npx tape test/*.js", "npx tap-xunit >out.xml"},
		"node test.js | tap-spec":                                             {"node test.js", "tap-spec"},
		"node test.js | npx --no-install faucet":                              {"node test.js", "npx --no-install faucet"},
		"node test.js | pnpm exec tap-junit --output reports | tee junit.log": {"node test.js", "pnpm exec tap-junit --output reports | tee junit.log"},
	} {
		fragments, err := discoverCommands(command)
		require.NoError(t, err, command)
		require.Len(t, fragments, 1, command)
		require.Equal(t, want[0], strings.Join(fragments[0].Words, " "), command)
		require.Equal(t, want[1], fragments[0].Pipe, command)
	}
	for _, command := range []string{
		"npx jest | grep -v warning",
		"npx jest | tee $LOG",
		"npx jest | tee --output-error=exit out.txt",
		"npx jest | sh",
		"cat input | npx jest",
		"npx jest | cucumber-junit < other.json",
		"npx jest | tee out.txt &",
		"! npx jest | tee out.txt",
		"(npx jest) | tee out.txt",
		"npx jest > out.txt | tee x",
		"npx jest | tap-",
		"npx jest | pnpm",
	} {
		fragments, err := discoverCommands(command)
		if err == nil {
			for _, fragment := range fragments {
				require.Empty(t, fragment.Pipe, command)
				require.NotEqual(t, "npx jest", strings.Join(fragment.Words, " "), command)
			}
		}
	}
	// Each pipeline command runs in a subshell: a piped cd changes nothing
	// for the commands after it, so it is never read as a directory change.
	for _, command := range []string{"cd app | tee cd.log && npx jest", "cd app 2>&1 | tee cd.log && npx jest", "export CI=1 | tee x && npx jest", "pushd app | tee x && npx jest"} {
		fragments, err := discoverCommands(command)
		require.Error(t, err, command)
		require.Empty(t, fragments, command)
	}
	fragments, err := discoverCommands("npm ci && npx jest | tee out.txt")
	require.NoError(t, err)
	require.Len(t, fragments, 2)
	require.Empty(t, fragments[0].Pipe)
	require.Equal(t, "tee out.txt", fragments[1].Pipe)
}

// The B02 shape and its peers: the piped test command is the configuration,
// and the report says CI's step status may come from the last filter.
func TestPipedCITestCommandsAreValidatedWithAnExitStatusCaveat(t *testing.T) {
	note := func(command, filters string) string { return outputPipeNote(command, filters) }
	t.Run("jest", func(t *testing.T) {
		root := newJestRepository(t, "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npx jest --ci 2>&1 | tee jest.log\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		require.Equal(t, "jest --ci", scope.Commands[0].Command)
		require.Equal(t, []string{note("npx jest --ci", "tee jest.log")}, scope.Commands[0].StepScopeNotes)
		require.Contains(t, scope.Commands[0].StepScopeNotes[0], "Unless the step's shell sets pipefail")
	})
	t.Run("jest with an environment read", func(t *testing.T) {
		root := newJestRepository(t, "jobs:\n  test:\n    runs-on: ubuntu-latest\n    env: {SHARD: '1/2'}\n    steps:\n      - run: npx jest --shard=$SHARD | tee jest.log\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		require.Equal(t, "jest --shard=1/2", scope.Commands[0].Command)
	})
	t.Run("a package script is not forwarded through its filter", func(t *testing.T) {
		resolution := resolveJestSequence(t.TempDir(), t.TempDir(), "jest | tee out.txt", nil, &scriptBudget{left: 16})
		require.False(t, resolution.SingleJest)
	})
	t.Run("pytest", func(t *testing.T) {
		root := pythonWorkflow(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - run: pip install pytest
      - run: python -m pytest -q | tee pytest.log
`)
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		require.Equal(t, "python -m pytest -q", scope.Commands[0].Command)
		require.Equal(t, []string{note("python -m pytest -q", "tee pytest.log")}, scope.Commands[0].StepScopeNotes)
	})
	t.Run("rspec", func(t *testing.T) {
		root := rubyWorkflowFixture(t, `jobs:
  tests:
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '3.3'}
      - run: bundle exec rspec --format progress | tee rspec.log
`)
		scope, err := DiscoverValidationScope(root, "rspec")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		require.Equal(t, "bundle exec rspec --format progress", scope.Commands[0].Command)
		require.Equal(t, []string{note("bundle exec rspec --format progress", "tee rspec.log")}, scope.Commands[0].StepScopeNotes)
	})
	t.Run("a piped cd keeps the step's directory", func(t *testing.T) {
		root := newJestRepository(t, "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: cd app | tee cd.log && npx jest\n")
		writeFile(t, root, "app/package.json", `{"devDependencies":{"jest":"29.7.0"}}`)
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved)
		for _, entry := range scope.Commands {
			require.NotEqual(t, "app", entry.Directory)
			require.NotEmpty(t, entry.UnvalidatedSetup)
		}
		app := filepath.Join(root, "app")
		resolution := resolveJestSequence(root, root, "cd app | tee cd.log && jest", nil, &scriptBudget{left: 16})
		require.False(t, resolution.SingleJest, "a package script's piped cd does not move jest into %s", app)
	})
	t.Run("an unknown filter stays unresolved", func(t *testing.T) {
		root := newJestRepository(t, "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npx jest | ./filter.sh\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved)
	})
}

// GitLab's Cucumber step from proscrumdev/battleship-nodejs.
func TestGitLabCucumberPipedIntoAJUnitConverter(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"devDependencies": {"@cucumber/cucumber": "11.0.0", "cucumber-junit": "1.7.1"}}`)
	writeFile(t, root, "features/a.feature", "Feature: a\n  Scenario: b\n    Given c\n")
	writeFile(t, root, ".gitlab-ci.yml", "image: node:24\nacceptance test:\n  before_script:\n    - npm install\n  script:\n    - ./node_modules/.bin/cucumber-js ./features --format json | ./node_modules/.bin/cucumber-junit > testresults_cucumber.xml\n")
	t.Chdir(root)
	scope, err := DiscoverValidationScope(root, "cucumber")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, scope.Commands[0].Command, "cucumber-js ./features --format json")
	require.Len(t, scope.Commands[0].StepScopeNotes, 1)
	require.Contains(t, scope.Commands[0].StepScopeNotes[0], "into ./node_modules/.bin/cucumber-junit >testresults_cucumber.xml")
}

// Maven and Gradle steps piped into tee are their build command, with the
// same caveat.
func TestPipedJavaBuildsKeepTheExitStatusCaveat(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	resolution := javaStep(t, root, "mvn -B verify | tee build.log")
	require.True(t, resolution.Matched, resolution.Reason)
	require.Len(t, resolution.Tests, 1)
	require.Equal(t, "mvn -B verify", resolution.Tests[0].Command)
	require.Contains(t, resolution.Tests[0].StepScopeNotes, outputPipeNote("mvn -B verify", "tee build.log"))
}
