// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

// The B03 shape from gitlab-styles: a Bash [[ ]] condition on a matrix
// variable decides the export on each row.
func TestGitLabMatrixConditionDecidesTheExport(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := gitLabRubyRepository(t, `specs:
  image: "${GITLAB_DEPENDENCY_PROXY}ruby:${RUBY_VERSION}"
  parallel:
    matrix:
      - RUBY_VERSION: ['3.1', '3.3']
  script:
    - if [[ "$RUBY_VERSION" != "3.1" ]]; then export SIMPLECOV=0; fi
    - bundle exec rspec
`)
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	byRuby := map[string]map[string]string{}
	for _, entry := range scope.Commands {
		for _, ruby := range []string{"3.1", "3.3"} {
			if strings.Contains(entry.Locations[0], "["+ruby+"]") {
				byRuby[ruby] = entry.Environment
			}
		}
	}
	require.Len(t, byRuby, 2)
	require.NotContains(t, byRuby["3.1"], "SIMPLECOV")
	require.Equal(t, "0", byRuby["3.3"]["SIMPLECOV"])
	check := RubyCIConfigurationForSite(root, "rspec", "")
	for _, job := range check.Jobs {
		require.NotEmpty(t, job.Runtime, "the dependency proxy image selects the matrix Ruby")
	}
}

// A condition ddtest cannot decide leaves the variable maybe set: a test
// that doesn't read it runs without it and the report says so; a test that
// reads it, a value derived from it or a runtime control stays unresolved.
func TestConditionalExportsBlockOnlyTheirReaders(t *testing.T) {
	useCIProvider(t, "gitlab")
	condition := `    - if [[ "$CI_COMMIT_BRANCH" == "main" ]]; then export SIMPLECOV=0; fi` + "\n"
	t.Run("unread", func(t *testing.T) {
		root := gitLabRubyRepository(t, "specs:\n  image: ruby:3.3\n  script:\n"+condition+"    - bundle exec rspec\n")
		scope, err := DiscoverValidationScope(root, "rspec")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		require.NotContains(t, scope.Commands[0].Environment, "SIMPLECOV")
		require.Equal(t, []string{"SIMPLECOV"}, scope.Commands[0].WithoutCIValues, "validation unsets a value this host sets")
		require.Contains(t, strings.Join(scope.Caveats, "\n"), "validated without the CI variable SIMPLECOV (set only on some paths of the CI script, under conditions ddtest cannot decide): it has no local value")
	})
	for name, script := range map[string]string{
		"read by the command line":     condition + "    - bundle exec rspec --tag $SIMPLECOV\n",
		"read through a derived value": condition + "    - export SUITE=\"spec/$SIMPLECOV\"\n    - bundle exec rspec $SUITE\n",
		"a runtime control":            `    - if [[ "$CI_COMMIT_BRANCH" == "main" ]]; then export RUBYOPT=-W0; fi` + "\n    - bundle exec rspec\n",
		"a runner setting":             `    - if [[ "$CI_COMMIT_BRANCH" == "main" ]]; then export SPEC_OPTS=--fail-fast; fi` + "\n    - bundle exec rspec\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := gitLabRubyRepository(t, "specs:\n  image: ruby:3.3\n  script:\n"+script)
			scope, err := DiscoverValidationScope(root, "rspec")
			require.NoError(t, err)
			require.Contains(t, strings.Join(scope.Unresolved, "\n"), "sets it only on some paths of")
			for _, entry := range scope.Commands {
				require.NotEmpty(t, entry.UnvalidatedSetup, "an unresolved read is never validated")
			}
		})
	}
	t.Run("a conditional cd still stops the script", func(t *testing.T) {
		root := gitLabRubyRepository(t, "specs:\n  image: ruby:3.3\n  script:\n    - if [[ \"$CI_COMMIT_BRANCH\" == \"main\" ]]; then cd spec; fi\n    - bundle exec rspec\n")
		jobs, _ := gitLabJobs(t, root)
		require.Contains(t, jobs["specs"].Steps[1].ResolutionError, "changes directory conditionally")
	})
}

// sh has no [[ ]]: a Jenkins sh script's [[ ]] condition is not decided.
func TestBashTestClausesOnlyInBash(t *testing.T) {
	file, ok := parseCIShell(`if [[ "$V" != "3.1" ]]; then export X=0; fi`)
	require.True(t, ok)
	clause := file.Stmts[0].Cmd.(*syntax.IfClause)
	known := map[string]string{"V": "3.3"}
	branch, decided := ciStaticBranch(clause, known, "", true)
	require.True(t, decided)
	require.Len(t, branch, 1)
	_, decided = ciStaticBranch(clause, known, "", false)
	require.False(t, decided)
	for condition, want := range map[string][2]bool{
		`[[ -n "$V" ]]`:            {true, true},
		`[[ -z "$V" ]]`:            {false, true},
		`[[ $V == 3.3 ]]`:          {true, true},
		`[[ $V = 3.1 ]]`:           {false, true},
		`[[ $V == 3.* ]]`:          {false, false},
		`[[ $V ]]`:                 {true, true},
		`[[ $UNKNOWN == 1 ]]`:      {false, false},
		`[[ $V == 3.3 && -n $V ]]`: {false, false},
	} {
		file, ok := parseCIShell("if " + condition + "; then :; fi")
		require.True(t, ok, condition)
		truth, decided := ciStaticTest(file.Stmts[0].Cmd.(*syntax.IfClause).Cond, known, "", true)
		require.Equal(t, want, [2]bool{truth, decided}, condition)
	}
}

// A script entry that is one if clause runs its branch only when the job's
// shell decides the condition as ddtest does: a POSIX Jenkins sh (dash)
// rejects [[ ]] and runs the else branch, so the clause stays as written;
// GitLab and Buildkite run Bash and take the then branch.
func TestEntryBranchesFollowTheShellDialect(t *testing.T) {
	clause := `if [[ "$V" == 1 ]]; then npm test; else npm run lint; fi`
	posix := `if [ "$V" = 1 ]; then npm test; else npm run lint; fi`
	known := map[string]string{"V": "1"}
	require.Equal(t, clause, jenkinsShellDialect.resolveBranches(clause, known))
	require.Equal(t, "npm test", jenkinsShellDialect.resolveBranches(posix, known))
	require.Equal(t, "npm test", gitLabShellDialect.resolveBranches(clause, known))
	require.Equal(t, "npm test", buildkiteShellDialect.resolveBranches(clause, known))

	jobs := jenkinsJobs(t, `pipeline {
  agent any
  environment { V = '1' }
  stages {
    stage('Bash test') { steps { sh '''`+clause+`''' } }
    stage('POSIX test') { steps { sh '''`+posix+`''' } }
  }
}`)
	require.NotEmpty(t, jenkinsRuns(jobs["Bash test"]))
	require.NotContains(t, jenkinsRuns(jobs["Bash test"]), "npm test", "Jenkins sh is not Bash")
	require.Equal(t, []string{"npm test"}, jenkinsRuns(jobs["POSIX test"]))
}
