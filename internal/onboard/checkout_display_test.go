// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Findings write the checkout the way the selected provider's configuration
// does, not in the shared model's GitHub Actions syntax.
func TestCheckoutIsDisplayedInTheProvidersSyntax(t *testing.T) {
	for provider, want := range map[string]string{
		"github":    "${{ github.workspace }}/venv",
		"gitlab":    "$CI_PROJECT_DIR/venv",
		"jenkins":   "${WORKSPACE}/venv",
		"circleci":  "$CIRCLE_WORKING_DIRECTORY/venv",
		"buildkite": "$BUILDKITE_BUILD_CHECKOUT_PATH/venv",
	} {
		useCIProvider(t, provider)
		require.Equal(t, want, displayCIText(ciProjectDirectory+"/venv"), provider)
		base := errors.New("cannot statically resolve " + ciProjectDirectory + "/venv")
		displayed := displayCIError(base)
		require.Equal(t, "cannot statically resolve "+want, displayed.Error(), provider)
		require.ErrorIs(t, displayed, base)
	}
}

// The dd-sdk-reactnative shape: a global GitLab variable holding a path in
// the checkout, which the test command does not read, no longer blocks the
// test job; one the command reads stays unresolved, named as written.
func TestGitLabCheckoutPathVariablesBlockOnlyTheirReaders(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := newJestRepository(t, jestWorkflow)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
	writeFile(t, root, ".gitlab-ci.yml", `variables:
  IOS_DERIVED_DATA_PATH: '$CI_PROJECT_DIR/DerivedData'
test:js:
  image: node:22
  script:
    - npm test
reads:
  image: node:22
  script:
    - npx jest --outputFile=$IOS_DERIVED_DATA_PATH/jest.json
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	var located []string
	for _, entry := range scope.Commands {
		located = append(located, entry.Locations...)
		require.NotContains(t, entry.Environment, "IOS_DERIVED_DATA_PATH")
	}
	require.Contains(t, strings.Join(located, "\n"), "test:js")
	caveats := strings.Join(scope.Caveats, "\n")
	require.Contains(t, caveats, "test:js / step 1 (npm test): validated without the CI variable IOS_DERIVED_DATA_PATH (a path in the CI checkout, $CI_PROJECT_DIR/DerivedData)")
	unresolved := strings.Join(scope.Unresolved, "\n")
	require.Contains(t, unresolved, "CI environment variable IOS_DERIVED_DATA_PATH requires review: cannot statically resolve $CI_PROJECT_DIR/DerivedData")
	require.NotContains(t, unresolved+caveats, "github.workspace")
}

// The A11 shape: a Jenkins environment path under ${WORKSPACE} that the
// tests do not read is left out; one they read is reported in Jenkins
// syntax.
func TestJenkinsWorkspacePathsAreShownAsWritten(t *testing.T) {
	root := jenkinsRepository(t, `pipeline {
  agent any
  environment {
    VENV_DIR = "${WORKSPACE}/venv"
  }
  stages {
    stage('Unit') { steps { sh 'npm test' } }
    stage('Reads') { steps { sh 'npx jest --cacheDirectory="$VENV_DIR"' } }
  }
}`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	caveats, unresolved := strings.Join(scope.Caveats, "\n"), strings.Join(scope.Unresolved, "\n")
	require.Contains(t, caveats, "Unit / step 1 (npm test): validated without the CI variable VENV_DIR (a path in the CI checkout, ${WORKSPACE}/venv)")
	require.Contains(t, unresolved, "Reads")
	require.Contains(t, unresolved, "VENV_DIR")
	require.NotContains(t, caveats+unresolved, "github.workspace")
}
