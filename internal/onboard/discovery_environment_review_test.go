// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCrossEnvShellDoesNotHideInnerShellCommands(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{
		`cross-env-shell MODE=test echo 'ok && jest --ci'`,
		`cross-env-shell MODE=test 'jest --ci'`,
		`cross-env-shell MODE=test echo '$(jest)'`,
	} {
		result := resolveJestCommand(root, command, nil)
		require.NotEmpty(t, result.Reason, command)
		require.False(t, result.Review, command)
	}
}

func TestPrefixedMutationsStayAttachedToTests(t *testing.T) {
	for _, prefix := range []string{"CI=true ", "cross-env CI=true ", "cross-env-shell CI=true "} {
		for _, separator := range []string{" && ", "\n"} {
			command := prefix + "rimraf src"
			root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: |\n          "+command+separator+"          jest\n")
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
			require.NotEmpty(t, scope.Commands[0].UnvalidatedSetup, prefix)
		}
	}
}
