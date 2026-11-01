// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCIPredefinedReads(t *testing.T) {
	for _, value := range []string{"$CI_COMMIT_TAG", "renewedtab@$CI_COMMIT_TAG", "${CI_PROJECT_DIR}/build", "$CI_JOB_ID-$GITLAB_USER_LOGIN"} {
		require.True(t, ciPredefinedReads(value, "CI_", "GITLAB_"), value)
	}
	for _, value := range []string{"$DEPLOY_ENV", "$CI_COMMIT_TAG-$SUITE", "$(cat ci/suite)", "`date`-$CI_JOB_ID", "literal"} {
		require.False(t, ciPredefinedReads(value, "CI_", "GITLAB_"), value)
	}
}

func TestOnlyProviderDefinedValuesAreLeftOut(t *testing.T) {
	require.True(t, unresolvedCIProvided(unresolvedCIProvidedValue("$CI_COMMIT_TAG")))
	require.False(t, unresolvedCIProvided(unresolvedCIValue("$(cat ci/suite)")))
	require.False(t, unresolvedCIProvided(unresolvedCIProvidedValue("$CI_JOB_ID")+unresolvedCIValue("$SUITE")), "every marker must be the provider's")
	require.Equal(t, unresolvedCIProvidedValue("$BUILDKITE_BUILD_NUMBER"), buildkiteUnresolved("$BUILDKITE_BUILD_NUMBER", []string{"BUILDKITE_BUILD_NUMBER"}))
	require.Equal(t, unresolvedCIValue("$SUITE"), buildkiteUnresolved("$SUITE", []string{"SUITE"}))
}

// GitLab job variables that read a project variable, rather than one GitLab
// predefines, are the project's own settings and still need review.
func TestGitLabProjectVariableReadsStayUnresolved(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  variables:\n    MODE: $DEPLOY_ENV\n  script:\n    - npx jest\n")
	useCIProvider(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "MODE requires review")
}

// A Jenkins value a command computes, as sh(returnStdout: true, …), is the
// job's own setup and still needs review.
func TestJenkinsCommandOutputStaysUnresolved(t *testing.T) {
	jenkinsRepository(t, "pipeline {\n  agent any\n  environment {\n    SUITE = sh(returnStdout: true, script: 'cat ci/suite').trim()\n  }\n  stages {\n    stage('test') {\n      steps {\n        sh 'npx jest'\n      }\n    }\n  }\n}\n")
	scope, err := DiscoverValidationScope(".", "jest")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	for _, entry := range scope.Commands {
		unresolved += strings.Join(entry.UnvalidatedSetup, "\n")
		require.NotContains(t, entry.WithoutCIValues, "SUITE")
	}
	require.Contains(t, unresolved, "SUITE")
}
