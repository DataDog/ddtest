package onboard

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartHandoffPreservesAllDeclaredSetupOptions(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.11"}}`), 0600))
	var out bytes.Buffer
	require.NoError(t, StartWithOptions(&out, StartOptions{Format: "json", Site: "datadoghq.eu", Tracer: "6.20.1", Facts: "/external/facts.json", Platform: "javascript"}))
	var state AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	for _, value := range []string{"--format", "json", "--site", "datadoghq.eu", "--tracer-version", "6.20.1", "--facts", "/external/facts.json", "--platform", "javascript"} {
		require.Contains(t, state.NextCommand, value)
	}
}

func TestStartRejectsCommandOverrideBeforeApplyHandoff(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.11"}}`), 0600))
	var out bytes.Buffer
	require.Error(t, StartWithOptions(&out, StartOptions{Format: "json", Command: "npm test"}))
	var state AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Equal(t, "needs_input", state.Status)
	require.Equal(t, "invalid_options", state.Code)
	require.Empty(t, state.NextCommand, "onboard --apply validates every configuration and rejects --command")
}

func TestConfigurationIdentityIgnoresOnlyEmptyInstrumentationOptions(t *testing.T) {
	original := ValidationScope{Commands: []TestCommand{{Command: "jest", Directory: "."}}}
	AssignConfigurationIDs(&original)
	empty := ValidationScope{Commands: []TestCommand{{Command: "jest", Directory: ".", Environment: map[string]string{"NODE_OPTIONS": ""}}}}
	AssignConfigurationIDs(&empty)
	require.Equal(t, original.Commands[0].ID, empty.Commands[0].ID)
	empty.Commands[0].Environment["NODE_OPTIONS"] = "--conditions=custom"
	AssignConfigurationIDs(&empty)
	require.NotEqual(t, original.Commands[0].ID, empty.Commands[0].ID)
}

func TestConfigurationIdentitySeparatesRunnerPlatforms(t *testing.T) {
	scope := ValidationScope{Commands: []TestCommand{{Command: "cypress run", Directory: "."}, {Command: "cypress run", Directory: ".", Platform: "linux"}, {Command: "cypress run", Directory: ".", Platform: "windows"}}}
	AssignConfigurationIDs(&scope)
	require.NotEqual(t, scope.Commands[0].ID, scope.Commands[1].ID)
	require.NotEqual(t, scope.Commands[1].ID, scope.Commands[2].ID, "facts must target one platform variant")
	unchanged := ValidationScope{Commands: []TestCommand{{Command: "jest", Directory: "."}}}
	AssignConfigurationIDs(&unchanged)
	require.Equal(t, "configuration-87a556e2440b3268", unchanged.Commands[0].ID, "IDs without a platform are unchanged")
}

func TestPlainRenderingIsTextOnly(t *testing.T) {
	result := AgentResult{Status: "needs_input", Phase: "setup", Code: "setup_unresolved", Reason: "declare the build", NextAction: "supply_input",
		InputTemplate: &SetupFacts{SchemaVersion: 2, Fingerprint: "abc"}, ResumeCommand: []string{"ddtest", "onboard", "--resume", "id"},
		Changes: []SetupChange{{Path: ".github/workflows/test.yml"}}, FinalResponse: "Onboarding validation: INCOMPLETE."}
	var out bytes.Buffer
	require.NoError(t, RenderAgentResult(&out, result, "verbose"))
	text := out.String()
	require.False(t, json.Valid(bytes.TrimSpace(out.Bytes())))
	require.NotContains(t, text, `"status":`, "no structured result document in plain output")
	for _, want := range []string{"Result: needs_input (setup_unresolved), phase setup", "Reason: declare the build", "Changed: .github/workflows/test.yml", "pass --facts <path>", `"input_fingerprint": "abc"`, "Resume: ddtest onboard --resume id", "Summary:\nOnboarding validation: INCOMPLETE."} {
		require.Contains(t, text, want)
	}
	out.Reset()
	require.NoError(t, RenderAgentResult(&out, result, "agent"))
	require.True(t, json.Valid(out.Bytes()), "agent and json formats stay one structured document")
}

func TestFinalResponseCarriesVerbatimRelayInstruction(t *testing.T) {
	result := AgentResult{Status: "validation_failed", Phase: "complete", FinalResponse: "NOT COMPLETE: onboarding validation is INCOMPLETE.", Instruction: "Run next_command."}
	var out bytes.Buffer
	require.NoError(t, RenderAgentResult(&out, result, "agent"))
	var state AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Equal(t, FinalResponseInstruction+" Run next_command. "+ReadWholeInstruction, state.Instruction, "the relay rule comes first")
	out.Reset()
	require.NoError(t, RenderAgentResult(&out, AgentResult{Status: "ready", Phase: "onboarding"}, "agent"))
	state = AgentResult{}
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.NotContains(t, state.Instruction, FinalResponseInstruction, "the relay rule applies only to a final response")
	require.Equal(t, ReadWholeInstruction, state.Instruction, "every result is read whole")
}

func TestReadyHandoffBoundsTheAgentsChanges(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.11"}}`), 0600))
	var out bytes.Buffer
	require.NoError(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	var state AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Contains(t, state.Reason, AgentScope)
	require.NotContains(t, state.Reason, "README", "documentation edits are outside onboarding scope")
}
