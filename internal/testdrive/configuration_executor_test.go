// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestConfigurationExecutorRunsWithoutOmittedSecrets(t *testing.T) {
	executor := configurationExecutor{environment: map[string]string{"CI": "true"}, without: []string{"GITHUB_TOKEN", "SONAR_TOKEN"}}
	env := executor.commandEnvironment(map[string]string{"DD_TRACE_AGENT_URL": "http://127.0.0.1:1"})
	require.Equal(t, map[string]string{"CI": "true", "DD_TRACE_AGENT_URL": "http://127.0.0.1:1", "GITHUB_TOKEN": ext.UnsetEnvironment, "SONAR_TOKEN": ext.UnsetEnvironment}, env, "a host value must not replace a secret the report says was left out")
	require.Equal(t, map[string]string{"CI": "true"}, configurationExecutor{environment: map[string]string{"CI": "true"}}.commandEnvironment(nil))
	require.Equal(t, map[string]string{"NPM_TOKEN": ext.UnsetEnvironment}, configurationExecutor{without: []string{"NPM_TOKEN"}}.commandEnvironment(nil))
}

func TestFocusedCommandRunsWithoutTheMatchedConfigurationsSecrets(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  test:\n    steps:\n      - run: npm test\n        env: {TZ: UTC, GITHUB_TOKEN: '${{ secrets.GITHUB_TOKEN }}'}\n")
	scope, err := onboard.DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	env, without := matchingConfigurationEnvironment(scope, nil, root, "jest", "npm test")
	require.Equal(t, map[string]string{"TZ": "UTC"}, env)
	require.Equal(t, []string{"GITHUB_TOKEN"}, without, "a command matched to CI runs without the configuration's omitted secrets")
	_, without = matchingConfigurationEnvironment(scope, nil, root, "jest", "npx jest --ci")
	require.Empty(t, without, "a command that matches no configuration omits nothing")
}
