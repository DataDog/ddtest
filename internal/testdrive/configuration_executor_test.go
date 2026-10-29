// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/stretchr/testify/require"
)

func TestConfigurationExecutorRunsWithoutOmittedSecrets(t *testing.T) {
	executor := configurationExecutor{environment: map[string]string{"CI": "true"}, without: []string{"GITHUB_TOKEN", "SONAR_TOKEN"}}
	env := executor.commandEnvironment(map[string]string{"DD_TRACE_AGENT_URL": "http://127.0.0.1:1"})
	require.Equal(t, map[string]string{"CI": "true", "DD_TRACE_AGENT_URL": "http://127.0.0.1:1", "GITHUB_TOKEN": ext.UnsetEnvironment, "SONAR_TOKEN": ext.UnsetEnvironment}, env, "a host value must not replace a secret the report says was left out")
	require.Equal(t, map[string]string{"CI": "true"}, configurationExecutor{environment: map[string]string{"CI": "true"}}.commandEnvironment(nil))
	require.Equal(t, map[string]string{"NPM_TOKEN": ext.UnsetEnvironment}, configurationExecutor{without: []string{"NPM_TOKEN"}}.commandEnvironment(nil))
}
