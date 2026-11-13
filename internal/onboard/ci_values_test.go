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

// The two kinds of values validation leaves out, those only CI provides and
// those ciOnlyValue describes, are named once each, in one caveat for the
// step, and unset together.
func TestLeftOutCIValuesShareOneCaveat(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", `variables:
  IOS_DERIVED_DATA_PATH: '$CI_PROJECT_DIR/DerivedData'
  RELEASE: 'app@$CI_COMMIT_TAG'
test:
  image: node:22
  script:
    - if [[ "$CI_COMMIT_BRANCH" == "main" ]]; then export REPORT_LABEL=main; fi
    - npx jest
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, []string{"IOS_DERIVED_DATA_PATH", "RELEASE", "REPORT_LABEL"}, scope.Commands[0].WithoutCIValues)
	for _, name := range scope.Commands[0].WithoutCIValues {
		require.NotContains(t, scope.Commands[0].Environment, name)
	}
	var caveats []string
	for _, caveat := range scope.Caveats {
		if strings.Contains(caveat, "validated without") {
			caveats = append(caveats, caveat)
		}
	}
	require.Len(t, caveats, 1, "one caveat for the step")
	require.Equal(t, ".gitlab-ci.yml / test / step 2 (npx jest): validated without the CI values of RELEASE, which only CI provides, as secrets, CI contexts or step outputs, and without the CI variables IOS_DERIVED_DATA_PATH (a path in the CI checkout, $CI_PROJECT_DIR/DerivedData); REPORT_LABEL (set only on some paths of the CI script, under conditions ddtest cannot decide), which have no local value; the step's command line reads none of them, so tests that read them ran locally without them, and CI runs the tests with their CI values.", caveats[0])
}

func TestCIValuesCaveatNamesEachVariableOnce(t *testing.T) {
	caveat, names := ciValuesCaveat("job / step 1 (npm test)", []string{"SIMPLECOV", "TOKEN"}, []string{"SIMPLECOV (set only on some paths of the CI script, under conditions ddtest cannot decide)"})
	require.Equal(t, []string{"SIMPLECOV", "TOKEN"}, names)
	require.Equal(t, 1, strings.Count(caveat, "SIMPLECOV"))
	require.Contains(t, caveat, "CI values of TOKEN, which only CI provides")
	require.Contains(t, caveat, "and without the CI variable SIMPLECOV (set only on some paths of the CI script, under conditions ddtest cannot decide), which has no local value")
}

// A CI-only value that controls the shell, the runtime, the tools or the
// tracer requires review whichever kind it is.
func TestCIOnlyControlValuesStayUnresolved(t *testing.T) {
	useCIProvider(t, "gitlab")
	for name, script := range map[string]string{
		"a predefined value in a runner setting":    "  variables:\n    JEST_JUNIT_OUTPUT_NAME: '$CI_JOB_ID.xml'\n  script:\n    - npx jest\n",
		"a predefined value in the time zone":       "  variables:\n    TZ: '$CI_RUNNER_TAGS'\n  script:\n    - npx jest\n",
		"a conditional value in Node's environment": "  script:\n    - if [[ \"$CI_COMMIT_BRANCH\" == \"main\" ]]; then export NODE_ENV=production; fi\n    - npx jest\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := newJestRepository(t, jestWorkflow)
			writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n"+script)
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			unresolved := strings.Join(scope.Unresolved, "\n")
			for _, entry := range scope.Commands {
				unresolved += strings.Join(entry.UnvalidatedSetup, "\n")
				require.Empty(t, entry.WithoutCIValues)
			}
			require.Contains(t, unresolved, "requires review")
		})
	}
}

func TestCIControlVariablesExceptCredentials(t *testing.T) {
	for _, name := range []string{"IFS", "SHELLOPTS", "CDPATH", "TZ", "LANG", "LC_ALL", "LD_AUDIT", "NODE_ENV", "JEST_JUNIT_OUTPUT_DIR", "YARN_ENABLE_IMMUTABLE_INSTALLS", "PIP_INDEX_URL", "UV_INDEX_URL", "TOXENV", "RUBYOPT", "SEED", "TESTS", "GEM_HOST"} {
		require.True(t, ciControlVariable(name), name)
	}
	for _, name := range []string{"NODE_AUTH_TOKEN", "YARN_NPM_AUTH_TOKEN", "NPM_TOKEN", "PIP_PASSWORD", "MAVEN_USERNAME", "MAVEN_PASSWORD", "RUBYGEMS_API_KEY"} {
		require.False(t, ciControlVariable(name), name)
	}
}
