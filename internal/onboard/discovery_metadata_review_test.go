// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVariableSettingPrintfStaysUnresolvedInAllParsers(t *testing.T) {
	for _, option := range []string{"-v", "'-v'", `"-v"`} {
		command := `printf ` + option + ` PATH '%s' "$(npm config get cache)" && jest`
		result := resolveTestStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.NotEmpty(t, result.Reason, command)
		require.False(t, result.Review, command)
	}
}

func TestNestedResolutionKeepsReviewedTools(t *testing.T) {
	for _, command := range []string{
		`if npm -ps ls nyc | grep -q nyc; then eslint . && jest; else jest --ci; fi`,
		`echo "$(eslint . && jest)"`,
	} {
		result := resolveTestStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.True(t, result.Matched, command)
		require.NotEmpty(t, result.ReviewReason, command)
	}
}

func TestPathWritesBlockDependentCommands(t *testing.T) {
	for _, target := range []string{`"$GITHUB_PATH"`, `"${GITHUB_PATH}"`} {
		root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: echo /custom/bin >> "+target+"\n      - run: jest\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
		require.NotEmpty(t, scope.Commands[0].UnvalidatedSetup)
		require.NotEmpty(t, scope.Unresolved)
	}
}
