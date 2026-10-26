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
