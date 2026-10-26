package onboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestMavenOnboardingUsesJavaActionAndNativeValidation(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile("pom.xml", []byte(`<project><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`), 0600))
	require.NoError(t, os.WriteFile(".github/workflows/tests.yml", []byte("jobs:\n  test:\n    steps:\n      - run: mvn -B -Pci verify\n"), 0600))
	var output bytes.Buffer
	require.NoError(t, Run(&output))
	for _, text := range []string{"DDTest found java, junit5", "languages: java", "MAVEN_OPTS", "--framework junit5", "plan/run", "Scala MUnit"} {
		require.Contains(t, output.String(), text)
	}
	require.NotContains(t, output.String(), "NODE_OPTIONS")
	instructions := output.String()
	output.Reset()
	require.NoError(t, Start(&output))
	require.Equal(t, instructions, output.String(), "the default onboarding entry point must preserve Maven guidance")
}

func TestMavenAgentReturnsManualSetupWithWorkingValidationCommands(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("pom.xml", []byte(`<project><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`), 0600))
	var output bytes.Buffer
	require.NoError(t, StartWithOptions(&output, StartOptions{Format: "json"}))
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "manual_setup_required", result.Code)
	require.Equal(t, "ready", result.Status)
	require.Equal(t, "run_command", result.NextAction)
	require.Contains(t, result.NextCommand, "--instructions")
	require.Contains(t, result.NextCommand, "junit5")
	require.Equal(t, "not supported", result.Checks[0].Status)
	require.Equal(t, "available", result.Checks[1].Status)
	require.Contains(t, result.Checks[1].Command, "testdrive")
	require.Contains(t, result.Instruction, "summary.final_response")
	require.Contains(t, result.Instruction, "unless summary.status is COMPLETE LOCALLY")
	require.Contains(t, result.Instruction, "CI edits prepared")
	require.NotContains(t, output.String(), "framework_unsupported")
	require.NoDirExists(t, ".testoptimization")
}

func TestMavenHandoffPreservesSelectedCommandTracerAndTimeout(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("pom.xml", []byte(`<project><dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId></dependency></dependencies></project>`), 0600))
	var output bytes.Buffer
	require.NoError(t, StartWithOptions(&output, StartOptions{Format: "agent", Command: "mvn -Pci verify", Tracer: "1.67.1", Timeout: "7m", Site: "datadoghq.eu"}))
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	for _, value := range []string{"--command", "mvn -Pci verify", "--tracer-version", "1.67.1", "--timeout", "7m"} {
		require.Contains(t, result.Checks[2].Command, value)
	}
	require.Contains(t, result.Instruction, "datadoghq.eu")
}
