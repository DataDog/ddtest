// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestCypressSubstitutesOnlyACompatibleLocalRuntime(t *testing.T) {
	selection := platform.JSSelection{Version: "5.132.0", Node: ">=18"}
	require.Equal(t, "compatible", cypressRuntimeVerdict(jestPreflight{Version: "15.21.1", Node: "24.14.1"}, []string{"^20.0.0 || >=22.0.0"}, selection).Status)
	unsupported := cypressRuntimeVerdict(jestPreflight{Version: "15.21.1", Node: "18.20.0"}, []string{"^20.0.0 || >=22.0.0"}, selection)
	require.NotEqual(t, "compatible", unsupported.Status)
	require.Contains(t, unsupported.Reason, cypressRequirementPrefix)
}

func substitutedFixture(accepted bool) validationResult {
	result := completeValidationFixture("cypress")
	result.RuntimeSubstitution = &runtimeSubstitution{Source: "The Cypress action API (cypress-io/github-action@v6)", CI: "20", Local: "v24.14.1", Accepted: accepted, Reason: "compatible"}
	return result
}

func TestSubstituteRuntimeCompletesOnlyWhenAccepted(t *testing.T) {
	result := substitutedFixture(false)
	prepareValidationResult(t.TempDir(), &result)
	require.True(t, result.LocalSuccess, "the substitute evidence itself passed")
	require.False(t, result.Success)
	summary := summarizeValidation(result)
	require.NotEqual(t, "COMPLETE LOCALLY", summary.Status)
	require.Contains(t, summary.BlockingChecks, result.RuntimeSubstitution.blocker())
	require.Contains(t, result.RuntimeSubstitution.blocker(), AcceptSubstituteRuntimeFlag)

	accepted := substitutedFixture(true)
	prepareValidationResult(t.TempDir(), &accepted)
	require.True(t, accepted.Success)
	require.Empty(t, summarizeValidation(accepted).BlockingChecks)
}

func TestWorkflowAsksTheUserToAcceptASubstituteRuntime(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion}
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		return finishValidation(out, root, substitutedFixture(false))
	}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "substitute_runtime_unaccepted", result.Code, result.Reason)
	require.Equal(t, "supply_input", result.NextAction)
	require.True(t, slices.Contains(result.ResumeCommand, AcceptSubstituteRuntimeFlag), result.ResumeCommand)
	require.Contains(t, result.Needs, "Ask the user")
	require.NotEqual(t, "complete_locally", result.Status)
	// The turn ends here, so the canonical text travels with the result.
	require.True(t, strings.HasPrefix(result.FinalResponse, "NOT COMPLETE"), result.FinalResponse)
	require.Contains(t, result.Instruction, onboard.FinalResponseInstruction)
}

// tradukisto's decision named "set JAVA_HOME to an existing Java 21 JDK and
// set JAVA_HOME to an existing Java 24 JDK": one run has one JAVA_HOME, so
// the remedy names it once and offers CI's versions as alternatives.
func TestSubstituteChoiceNamesEachRuntimeAndRemedyOnce(t *testing.T) {
	java := func(ci string) runtimeSubstitution {
		return runtimeSubstitution{CI: ci, Local: "17.0.20.1", Runtime: "Java", Remedy: "set JAVA_HOME to an existing Java " + ci + " JDK"}
	}
	evidence, remedy := substituteChoice([]runtimeSubstitution{java("21"), java("24"), java("24")})
	require.Equal(t, "Java 17.0.20.1 in place of CI's Java 21 and 24", evidence)
	require.Equal(t, "set JAVA_HOME to an existing Java 21 or 24 JDK", remedy)

	evidence, remedy = substituteChoice([]runtimeSubstitution{{CI: "20", Local: "v24.14.1"}, java("21")})
	require.Equal(t, "Node v24.14.1 in place of CI's Node 20 and Java 17.0.20.1 in place of CI's Java 21", evidence)
	require.Equal(t, "put an existing Node 20 first on PATH and set JAVA_HOME to an existing Java 21 JDK", remedy)
}

func TestWorkflowDecisionDoesNotRepeatTheRemedy(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
	fixture := substitutedFixture(false)
	fixture.RuntimeSubstitutions = []runtimeSubstitution{*fixture.RuntimeSubstitution, *fixture.RuntimeSubstitution}
	fixture.RuntimeSubstitutions[1].CI = "22"
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion}
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		return finishValidation(out, root, fixture)
	}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "substitute_runtime_unaccepted", result.Code, result.Reason)
	require.Equal(t, []string{"User decision: accept the evidence from Node v24.14.1 in place of CI's Node 20 and 22 by running resume_command, or put an existing Node 20 or 22 first on PATH and rerun resume_command without " + AcceptSubstituteRuntimeFlag + "."}, result.RequiredInput)
}
