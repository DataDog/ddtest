// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFailingBuiltinsDoNotProduceCleanConditionalTests(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "false && jest"})
	for _, command := range []string{"false && jest", "npm test"} {
		result := resolveJestCommand(root, command, nil)
		require.NotEmpty(t, result.Reason, command)
		for _, test := range result.Tests {
			require.NotEmpty(t, test.UnvalidatedSetup, command)
		}
	}
	result := resolveJestCommand(root, "true && jest", nil)
	require.Empty(t, result.Reason)
	require.Equal(t, []string{"jest"}, commandNames(result.Tests))
}
