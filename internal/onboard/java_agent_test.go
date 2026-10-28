// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const javaAgentWorkflow = `jobs:
  tests:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        java: ['17', '25-ea']
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-java@v4
        with: {distribution: temurin, java-version: '${{ matrix.java }}'}
ACTION      - run: ./mvnw -B install
`

const javaAgentAction = `      - uses: datadog/test-visibility-github-action@v3
        if: ${{ !contains(matrix.java, '-ea') }}
        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}
`

func javaAgentFixture(t *testing.T, action string) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile("pom.xml", []byte(javaJUnit5POM), 0600))
	require.NoError(t, os.WriteFile("mvnw", []byte("#!/bin/sh\n"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(".github", "workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(".github", "workflows", "test.yml"), []byte(strings.Replace(javaAgentWorkflow, "ACTION", action, 1)), 0600))
	return root
}

func javaAgentStart(t *testing.T, options StartOptions) (AgentResult, error) {
	t.Helper()
	var out bytes.Buffer
	err := StartWithOptions(&out, options)
	var state AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state), out.String())
	return state, err
}

func TestJavaAgentOnboardingRequiresCISetupBeforeValidation(t *testing.T) {
	javaAgentFixture(t, "")
	state, err := javaAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.eu"})
	require.Error(t, err)
	require.Equal(t, "needs_input", state.Status)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Equal(t, "apply_source_edit", state.NextAction)
	require.Empty(t, state.NextCommand, "the agent edits CI first")
	require.Equal(t, []string{"testdrive", "--framework", "junit5", "--all", "--agent", "--yes"}, state.ResumeCommand[1:7])
	require.Contains(t, state.ResumeCommand, "datadoghq.eu")
	require.Contains(t, state.Reason, AgentScope)
	require.Contains(t, state.Instruction, "[.] ./mvnw -B verify", "the CI install step validates as verify")
	for _, text := range []string{"datadog/test-visibility-github-action@v3", "languages: java", "`site: datadoghq.eu`", "exact `java-tracer-version`", "${{ env.MAVEN_OPTS }}", "/.testoptimization/", "never installs or publishes"} {
		require.Contains(t, state.Instruction, text)
	}
	// The handoff runs validation once; the instructions must not add runs.
	for _, extra := range []string{"--all --check-only", "ddtest testdrive", "PASSED", "__SITE_INPUT__", "__FRAMEWORK__"} {
		require.NotContains(t, state.Instruction, extra)
	}
}

func TestJavaAgentOnboardingIsReadyOnceTheActionIsConfigured(t *testing.T) {
	javaAgentFixture(t, javaAgentAction)
	state, err := javaAgentStart(t, StartOptions{Format: "agent"})
	require.NoError(t, err)
	require.Equal(t, "ready", state.Status)
	require.Equal(t, "run_command", state.NextAction)
	require.Equal(t, []string{"testdrive", "--framework", "junit5", "--all", "--agent", "--yes"}, state.NextCommand[1:])
	require.False(t, slices.Contains(state.NextCommand, "--format"), "--agent already selects the agent format")
	require.Empty(t, state.Code)
}

func TestJavaCISetupConfiguredNeedsACompleteInstrumentedStep(t *testing.T) {
	for _, tc := range []struct {
		name, action, site string
		configured         bool
	}{
		{"missing", "", "", false},
		{"early access excluded", javaAgentAction, "", true},
		{"every row excluded", strings.Replace(javaAgentAction, "${{ !contains(matrix.java, '-ea') }}", "false", 1), "", false},
		{"early access instrumented", strings.Replace(javaAgentAction, "        if: ${{ !contains(matrix.java, '-ea') }}\n", "", 1), "", false},
		{"no API key", strings.Replace(javaAgentAction, "api_key: '${{ secrets.DD_API_KEY }}', ", "", 1), "", false},
		{"floating agent", strings.Replace(javaAgentAction, ", java-tracer-version: '1.67.1'", "", 1), "", false},
		{"agent latest", strings.Replace(javaAgentAction, "'1.67.1'", "latest", 1), "", false},
		{"action v2", strings.Replace(javaAgentAction, "@v3", "@v2", 1), "", false},
		{"other site", javaAgentAction, "datadoghq.eu", false},
		{"selected site", strings.Replace(javaAgentAction, "languages: java,", "languages: java, site: datadoghq.eu,", 1), "datadoghq.eu", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := javaAgentFixture(t, tc.action)
			require.Equal(t, tc.configured, javaDiscovery{}.ciSetupConfigured(root, "junit5", tc.site))
		})
	}
}

func TestJavaHumanOnboardingPrintsManualGuidance(t *testing.T) {
	javaAgentFixture(t, "")
	var out bytes.Buffer
	require.NoError(t, StartWithOptions(&out, StartOptions{Human: true}))
	require.False(t, json.Valid(bytes.TrimSpace(out.Bytes())))
	require.Contains(t, out.String(), "ddtest testdrive --framework junit5")
}
