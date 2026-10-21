// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExplicitDependencyChangesStayAttachedToTests(t *testing.T) {
	for _, command := range []string{"npm install --no-save react@18", "npm i --save-dev react@18", "npm rm react", "yarn add react@18", "npm -g install jest@30"} {
		root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: "+command+"\n      - run: jest\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Equal(t, []string{"jest"}, commandNames(scope.Commands), command)
		require.NotEmpty(t, scope.Commands[0].UnvalidatedSetup, command)
		result := resolveJestCommand(root, command+" && jest", nil)
		require.Equal(t, []string{"jest"}, commandNames(result.Tests), command)
		require.NotEmpty(t, result.Tests[0].UnvalidatedSetup, command)
	}
}
