// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestJestConfigurationUsesNativeNodeEntryPoint(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "eslint . && jest --runInBand --config unit.cjs"})
	for _, command := range []string{"", "npm test", "yarn test", "jest --runInBand --config unit.cjs"} {
		selected, err := JestConfigurationCommand(root, command)
		require.NoError(t, err)
		words, err := shellquote.Split(selected.Command)
		require.NoError(t, err)
		require.Equal(t, []string{"node", filepath.Join(root, "node_modules", "jest", "bin", "jest.js"), "--runInBand", "--config", "unit.cjs"}, words)
	}
	writeScripts(t, root, map[string]string{"test": "NODE_ENV=ci jest"})
	selected, err := JestConfigurationCommand(root, "")
	require.NoError(t, err)
	require.Equal(t, "ci", selected.Environment["NODE_ENV"])
	for _, script := range []string{"mocha && jest", "jest && mocha", "node setup.js && jest", "jest && jest"} {
		writeScripts(t, root, map[string]string{"test": script})
		_, err = JestConfigurationCommand(root, "npm test")
		require.Error(t, err, script)
	}
}

func TestJestConfigurationPreservesExplicitNodeExecutable(t *testing.T) {
	root := t.TempDir()
	node := filepath.Join(root, "runtime", "node")
	command := shellquote.Join(node, "--experimental-vm-modules", "node_modules/jest/bin/jest.js", "--runInBand")
	selected, err := JestConfigurationCommand(root, command)
	require.NoError(t, err)
	require.Equal(t, command, selected.Command)
}
