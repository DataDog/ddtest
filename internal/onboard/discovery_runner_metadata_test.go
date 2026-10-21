// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestJestHelpAndVersionCommandsAreNotTests(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    steps:
      - run: npx jest --version
      - run: node --experimental-vm-modules node_modules/jest/bin/jest.js --help
      - run: npm test
`)
	writeScripts(t, root, map[string]string{"test": "jest"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
	_, err = JestValidationCommand(root)
	require.NoError(t, err)
	for _, command := range []string{"jest --help", "jest -h", "jest --version", "jest -v", "node node_modules/jest/bin/jest.js --version"} {
		result := resolveJestCommand(root, command, nil)
		require.Empty(t, result.Reason, command)
		require.Empty(t, result.Tests, command)
	}
}
