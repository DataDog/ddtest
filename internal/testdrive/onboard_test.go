// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestOnboardUnresolvedScopeReturnsActionWithoutEditsOrTests(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.6"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	workflow := "jobs:\n  test:\n    steps:\n      - run: custom-setup && npx vitest run\n"
	path := filepath.Join(root, ".github/workflows/test.yml")
	requireWriteFile(t, path, workflow)
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}), "scope_unresolved")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "needs_input", result.Status)
	require.Equal(t, "discovery", result.Phase)
	require.Contains(t, result.Reason, "custom-setup")
	require.Contains(t, result.Needs, "do not rewrite CI or package scripts only so ddtest can parse them")
	require.Equal(t, "run_command", result.NextAction)
	require.Equal(t, []string{"onboard", "--framework", "vitest", "--instructions"}, result.NextCommand[1:])
	require.Empty(t, result.RequiredInput)
	require.Empty(t, result.ResumeCommand)
	require.Empty(t, result.Changes)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, workflow, string(data))
	_, err = os.Stat(validationPath(root))
	require.True(t, os.IsNotExist(err), "no execution or success report may be invented")
}

func TestOnboardRejectsHistoricalOrCheckOnlySuccessWithoutExposingItsHandoff(t *testing.T) {
	for _, checkOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(checkOnly), func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0755))
			started := time.Now()
			completed := started.Add(-time.Hour)
			if checkOnly {
				completed = started.Add(time.Second)
			}
			data, err := json.Marshal(validationResult{CheckOnly: checkOnly, CompletedAt: completed, Summary: validationSummary{Status: "COMPLETE LOCALLY", FinalResponse: "obsolete success"}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(validationPath(root), data, 0600))
			result, err := readCurrentOnboardingResult(root, started)
			require.ErrorContains(t, err, "cannot establish completion")
			require.Empty(t, result.Summary.FinalResponse, "never hand an obsolete success to the agent")
		})
	}
}

func TestOnboardMissingScopePreservesFramework(t *testing.T) {
	drive := &Testdrive{framework: &framework.Jest{}, language: "javascript", tracerVersion: "latest"}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}), "scope_unresolved")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Contains(t, result.NextCommand, "jest")
	require.Equal(t, "needs_input", result.Status)
	require.NotContains(t, result.NextCommand, "vitest")
	require.NotContains(t, result.NextCommand, "vitest")
}

func TestCypressPlanRequiresTIA(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run"},"devDependencies":{"cypress":"15.1.0"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: custom-setup && npx cypress run\n")
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Cypress{}, language: "javascript", allConfigurations: true}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}), "scope_unresolved")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.NotNil(t, result.PlanSummary)
	require.Len(t, result.PlanSummary.Features, 6)
	require.Contains(t, result.PlanSummary.Features, "skipping")
	require.Empty(t, result.PlanSummary.UnsupportedFeatures)
}

func TestUnsupportedWorkflowDoesNotRecommendItself(t *testing.T) {
	drive := &Testdrive{framework: framework.NewPytest(nil), language: "python"}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}), "framework_unsupported")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "report_result", result.NextAction)
	require.Empty(t, result.NextCommand)
}

func TestUnknownBuildOutputsAreNotDiscoveredByBuildingInTheCheckout(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest","build":"node build.js"}}`)
	requireWriteFile(t, filepath.Join(root, "build.js"), `require('fs').mkdirSync('dist',{recursive:true})`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npm run build\n      - run: npm test\n")
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Jest{}, language: "javascript", allConfigurations: true}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}), "setup_unresolved")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "setup_unresolved", result.Code)
	require.Contains(t, result.Needs, "do not run it in the checkout")
	require.NoDirExists(t, filepath.Join(root, "dist"))
}

func TestWorkflowStateSurvivesPreparingAgain(t *testing.T) {
	scope := &onboard.ValidationScope{}
	current := &Testdrive{selectedSite: "datadoghq.eu", workflowRoot: "/repo", workflowScope: scope}
	prepared := (&Testdrive{tracerVersion: "1.67.1"}).withWorkflowState(current)
	require.Equal(t, "datadoghq.eu", prepared.selectedSite, "a drive prepared for the CI pin still checks the selected site")
	require.Equal(t, "/repo", prepared.workflowRoot)
	require.Same(t, scope, prepared.workflowScope)
	require.Equal(t, "1.67.1", prepared.tracerVersion)
}
