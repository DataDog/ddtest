package onboard

import (
	"bytes"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestAgentHandoffDoesNotRepeatLargeMatrix(t *testing.T) {
	plan := &ExecutionPlan{Tracer: "5.132.0", Site: "datadoghq.com", Framework: "mocha", Initialization: "preserve hooks"}
	for range 25 {
		plan.Scope.Commands = append(plan.Scope.Commands, TestCommand{Command: strings.Repeat("configuration detail ", 100)})
	}
	var output bytes.Buffer
	require.NoError(t, RenderAgentResult(&output, AgentResult{RunID: "abcdefghijklmnopqrstuv", Status: "needs_input", Plan: plan, Reason: "one missing prerequisite", ResumeCommand: []string{"ddtest", "onboard", "--resume", "abcdefghijklmnopqrstuv"}}, "json"))
	require.Less(t, output.Len(), 8192)
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Nil(t, result.Plan)
	require.Equal(t, 25, result.PlanSummary.Configurations)
	require.Equal(t, "5.132.0", result.PlanSummary.Tracer)
	require.Contains(t, result.DetailsCommand, "--resume")
	require.NotEmpty(t, result.ResumeCommand)
}

func TestPersistentExecutableRejectsGoRunHandoffs(t *testing.T) {
	require.False(t, persistentExecutable("/private/tmp/go-build123456/b001/exe/main"))
	require.False(t, persistentExecutable("/tmp/go-build123456/exe/main"))
	require.True(t, persistentExecutable("/usr/local/bin/ddtest"))
	require.True(t, persistentExecutable("/tmp/customer-build/ddtest"))
}
