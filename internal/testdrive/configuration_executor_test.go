// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
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
	// A step's own value, in the configuration or the command, is kept: the
	// setup step that read the secret runs without it, the test step with
	// its literal.
	executor = configurationExecutor{environment: map[string]string{"NPM_TOKEN": "local-value"}, without: []string{"NPM_TOKEN"}}
	require.Equal(t, "local-value", executor.commandEnvironment(nil)["NPM_TOKEN"])
	executor = configurationExecutor{without: []string{"AUTH_TOKEN"}}
	require.Equal(t, "build-value", executor.commandEnvironment(map[string]string{"AUTH_TOKEN": "build-value"})["AUTH_TOKEN"])
	require.Equal(t, ext.UnsetEnvironment, executor.commandEnvironment(map[string]string{"OTHER": "x"})["AUTH_TOKEN"])
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

// envCapture records the environment each executor method receives.
type envCapture struct{ seen []map[string]string }

func (c *envCapture) CombinedOutput(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	c.seen = append(c.seen, env)
	return nil, nil
}

func (c *envCapture) CombinedOutputInDirectory(_ context.Context, _, _ string, _ []string, env map[string]string) ([]byte, error) {
	c.seen = append(c.seen, env)
	return nil, nil
}

func (c *envCapture) ServiceOutput(_ context.Context, _, _ string, _ []string, env map[string]string) ([]byte, error) {
	c.seen = append(c.seen, env)
	return nil, nil
}

func (c *envCapture) Output(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, []byte, error) {
	c.seen = append(c.seen, env)
	return nil, nil, nil
}

func TestEveryConfigurationCommandRunsWithoutOmittedSecrets(t *testing.T) {
	capture := &envCapture{}
	executor := configurationExecutor{commandExecutor: capture, environment: map[string]string{"CI": "true"}, without: []string{"GITHUB_TOKEN"}}
	ctx := context.Background()
	_, err := executor.CombinedOutput(ctx, "mvn", nil, nil)
	require.NoError(t, err)
	_, err = executor.CombinedOutputInDirectory(ctx, ".", "mvn", nil, nil)
	require.NoError(t, err)
	_, err = executor.ServiceOutput(ctx, ".", "npm", []string{"start"}, nil)
	require.NoError(t, err)
	_, _, err = executor.Output(ctx, "git", nil, nil)
	require.NoError(t, err)
	require.Len(t, capture.seen, 4)
	for _, env := range capture.seen {
		require.Equal(t, ext.UnsetEnvironment, env["GITHUB_TOKEN"], "tests, setup, application services and checks all run without it")
		require.Equal(t, "true", env["CI"])
	}
}

func TestFocusedCommandsKeepOmittedSecretsFromAdoptedOrAmbiguousConfigurations(t *testing.T) {
	plan := onboard.TestCommand{Command: "npm test", Environment: map[string]string{"TZ": "UTC"}}
	adoptConfigurationPreparation(&plan, onboard.TestCommand{Command: "npm test", Prerequisites: []onboard.BuildCommand{{Command: "npm run build"}}, Environment: map[string]string{"CI": "true"}, WithoutCIValues: []string{"SONAR_TOKEN"}})
	require.Equal(t, []string{"SONAR_TOKEN"}, plan.WithoutCIValues, "adopted CI preparation brings its omitted secrets")
	require.Equal(t, map[string]string{"CI": "true", "TZ": "UTC"}, plan.Environment)
	require.Len(t, plan.Prerequisites, 1)

	// Two variants with different environments are ambiguous, but the command
	// still runs without the secret either omits.
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  utc:\n    steps:\n      - run: npm test\n        env: {TZ: UTC}\n  paris:\n    steps:\n      - run: npm test\n        env: {TZ: Europe/Paris, SONAR_TOKEN: '${{ secrets.SONAR_TOKEN }}'}\n")
	scope, err := onboard.DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	env, without := matchingConfigurationEnvironment(scope, nil, root, "jest", "npm test")
	require.Nil(t, env)
	require.Equal(t, []string{"SONAR_TOKEN"}, without)
}
