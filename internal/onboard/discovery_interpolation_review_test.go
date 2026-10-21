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

func TestInterpolationPreservesStatementBoundariesAndTildeReview(t *testing.T) {
	for _, command := range []string{`jest --config ~/$CONFIG`, `jest --config ~user/$CONFIG`} {
		_, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command, Env: map[string]string{"CONFIG": "ci.js"}}, nil)
		require.Error(t, err, command)
	}
	for _, command := range []string{"tsc; jest --config \"$CONFIG\"", "tsc\njest --config \"$CONFIG\"", "echo \"dir=$(npm config get cache)\" && tsc; jest"} {
		root := newJestRepository(t, "jobs:\n  test:\n    env: {CONFIG: ci.js}\n    steps:\n      - shell: bash {0}\n        run: |\n          "+strings.ReplaceAll(command, "\n", "\n          ")+"\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Commands, command)
		require.Empty(t, scope.Commands[0].Prerequisites, command)
		require.NotEmpty(t, scope.Commands[0].UnvalidatedSetup, command)
	}
}
