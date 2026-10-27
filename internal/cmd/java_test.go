// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func useMavenProject(t *testing.T, extraFiles map[string]string) {
	t.Helper()
	t.Chdir(t.TempDir())
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	require.NoError(t, os.WriteFile("pom.xml", []byte(`<project><dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId></dependency></dependencies></project>`), 0600))
	for name, contents := range extraFiles {
		require.NoError(t, os.WriteFile(name, []byte(contents), 0600))
	}
}

func executeCommand(t *testing.T, command *cobra.Command, args ...string) (string, error) {
	t.Helper()
	command.SetArgs(args)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(io.Discard)
	err := command.ExecuteContext(t.Context())
	return output.String(), err
}

func TestJavaOnboardPrintsManualGuidance(t *testing.T) {
	for _, args := range [][]string{nil, {"--instructions"}} {
		useMavenProject(t, nil)
		output, err := executeCommand(t, newOnboardCommand(nil), args...)
		require.NoError(t, err)
		require.Contains(t, output, "DDTest found a Maven project with JUnit 4.")
		require.Contains(t, output, "languages: java")
		require.NoDirExists(t, ".testoptimization", "manual guidance writes nothing")
	}
}

func TestJavaOnboardAgentReturnsManualSetup(t *testing.T) {
	useMavenProject(t, nil)
	output, err := executeCommand(t, newOnboardCommand(nil), "--agent")
	require.NoError(t, err)
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal([]byte(output), &result), output)
	require.Equal(t, "manual_setup_required", result.Code)
	require.Contains(t, result.NextCommand, "--instructions")
}

func TestJavaOnboardRejectsAutomaticSetupOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"--apply", "--format", "json"}, "framework_unsupported"},
		{[]string{"--apply", "--yes", "--format", "json"}, "framework_unsupported"},
		{[]string{"--site", "datadoghq.eu", "--format", "json"}, "invalid_options"},
		{[]string{"--tracer-version", "1.67.1", "--format", "json"}, "invalid_options"},
		{[]string{"--instructions", "--agent"}, "invalid_options"},
	} {
		useMavenProject(t, nil)
		output, err := executeCommand(t, newOnboardCommand(nil), tc.args...)
		require.Error(t, err, "%v", tc.args)
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal([]byte(output), &result), output)
		require.Equal(t, tc.code, result.Code, "%v", tc.args)
	}
}

func TestJavaTestdriveIsUnsupported(t *testing.T) {
	useMavenProject(t, nil)
	output, err := executeCommand(t, newTestdriveCommand(), "--format", "json", "--yes")
	require.Error(t, err)
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal([]byte(output), &result), output)
	require.Equal(t, "framework_unsupported", result.Code)
	require.Contains(t, result.Reason, "--platform java")
	require.NoDirExists(t, ".testoptimization")
}

func TestMavenFilesDoNotChangeJavaScriptOnboarding(t *testing.T) {
	useMavenProject(t, map[string]string{"package.json": `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.11"}}`})
	output, err := executeCommand(t, newOnboardCommand(nil), "--format", "json")
	require.NoError(t, err)
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal([]byte(output), &result), output)
	require.Equal(t, "run_command", result.NextAction)
	require.Contains(t, result.NextCommand, "--apply", "the JavaScript project is onboarded as before")
	require.NotContains(t, output, "Maven")
}
