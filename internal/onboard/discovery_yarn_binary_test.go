// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestYarnBinaryFallbackKeepsEnvironmentPrefixes(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{})
	for _, command := range []string{"CI=true yarn jest --ci", "cross-env CI=true yarn jest --ci"} {
		result := resolveJestCommand(root, command, nil)
		require.Empty(t, result.Reason, command)
		require.Equal(t, []TestCommand{{Directory: ".", Command: "jest --ci", Environment: map[string]string{"CI": "true"}}}, result.Tests)
	}
}
