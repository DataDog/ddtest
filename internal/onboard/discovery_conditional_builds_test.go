// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestConditionalBuildsRemainBranchLocal(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: |
          if npm -ps ls nyc | grep -q nyc; then
            tsc --outDir first && jest --config first.js
          else
            tsc --outDir second && jest --config second.js
          fi
      - run: jest --config later.js
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	byCommand := map[string]TestCommand{}
	for _, command := range scope.Commands {
		byCommand[command.Command] = command
	}
	require.Equal(t, []string{"tsc --outDir first"}, buildNames(byCommand["jest --config first.js"].Prerequisites))
	require.Equal(t, []string{"tsc --outDir second"}, buildNames(byCommand["jest --config second.js"].Prerequisites))
	require.Contains(t, byCommand, "jest --config later.js")
	require.Empty(t, byCommand["jest --config later.js"].Prerequisites)
	require.NotEmpty(t, byCommand["jest --config later.js"].UnvalidatedSetup)
}
