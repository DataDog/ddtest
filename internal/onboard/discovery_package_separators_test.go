// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPackageManagerSeparatorsMatchNativeSemantics(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "jest"})
	for _, tc := range []struct {
		command, want string
		unresolved    bool
	}{
		{command: "npm test -- --runInBand", want: "jest --runInBand"},
		{command: "pnpm test -- --runInBand", want: "jest -- --runInBand"},
		{command: "pnpm run test -- --runInBand", want: "jest -- --runInBand"},
		{command: "npm exec jest --runInBand", unresolved: true},
		{command: "npm exec jest -- --runInBand", want: "jest --runInBand"},
		{command: "npm exec -- jest --runInBand", want: "jest --runInBand"},
	} {
		result := resolveJestCommand(root, tc.command, nil)
		if tc.unresolved {
			require.NotEmpty(t, result.Reason, tc.command)
			continue
		}
		require.Empty(t, result.Reason, tc.command)
		require.Equal(t, []string{tc.want}, commandNames(result.Tests), tc.command)
	}
}
