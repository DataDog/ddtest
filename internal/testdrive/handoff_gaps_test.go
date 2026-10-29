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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestWorkflowKeepsTheSubstituteDecisionAmongOtherBlockers(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
	fixture := substitutedFixture(false)
	// Four ordinary failures: a capped list used to hide the later ones.
	for i := range fixture.Features[:4] {
		fixture.Features[i].Status = "failed"
	}
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion}
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		return finishValidation(out, root, fixture)
	}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "validation_incomplete", result.Code, result.Reason)
	require.Contains(t, result.Needs, "ask the user whether to accept the evidence from substitute Node v24.14.1")
	require.Contains(t, result.Needs, "run accept_resume_command instead of resume_command")
	require.Contains(t, result.Needs, "Do not accept it yourself")
	require.NotContains(t, result.ResumeCommand, AcceptSubstituteRuntimeFlag, "plain resume must not accept the substitute")
	// The accepting command is derived from the final resume command, so it
	// carries the same (current) checkpoint id.
	require.NotEmpty(t, result.RunID)
	require.Contains(t, result.ResumeCommand, result.RunID)
	require.Equal(t, append(append([]string{}, result.ResumeCommand...), AcceptSubstituteRuntimeFlag), result.AcceptResumeCommand)
	require.NotContains(t, result.RequiredInput, fixture.RuntimeSubstitution.blocker())
	failed := 0
	for _, item := range result.RequiredInput {
		if strings.HasSuffix(item, ": failed") {
			failed++
		}
	}
	require.Equal(t, 4, failed, result.RequiredInput)
}

func TestScopeBlockersCollapseOneCause(t *testing.T) {
	cause := "Unsupported CI shell: pwsh [affects 1 matrix entries]"
	got := scopeBlockers([]string{
		".github/workflows/example.yaml / examples / step 3 (npm ci): " + cause,
		".github/workflows/example.yaml / examples / step 4 (npx playwright install): " + cause,
		".github/workflows/test.yaml / test: matrix axis playwrightVersion is not a bounded static list",
	})
	require.Equal(t, []string{
		"unresolved CI scope (2 steps: .github/workflows/example.yaml / examples / step 3 (npm ci); .github/workflows/example.yaml / examples / step 4 (npx playwright install)): " + cause,
		"unresolved CI scope: .github/workflows/test.yaml / test: matrix axis playwrightVersion is not a bounded static list",
	}, got)
}

func TestConfigurationLineIsDroppedWhenItsErrorIsListed(t *testing.T) {
	result := completeValidationFixture("playwright")
	result.Scope = &onboard.ValidationScope{Unresolved: []string{".github/workflows/test.yaml / test / step 4 (npm install): Unsupported CI shell: pwsh"}}
	result.Configurations = []configurationResult{
		{TestCommand: onboard.TestCommand{Command: "npx playwright test"}, Status: "blocked", Error: "Unsupported CI shell: pwsh"},
		{TestCommand: onboard.TestCommand{Command: "npm run example"}, Status: "incomplete"},
	}
	summary := summarizeValidation(result)
	require.NotContains(t, summary.BlockingChecks, "configuration npx playwright test: blocked")
	require.Contains(t, summary.BlockingChecks, "configuration npm run example: incomplete")
}

func TestCombinedCountsKeepASingleSpecificIdentityReason(t *testing.T) {
	repeat := "Source-test identities are unavailable because native file/name identities repeat within a configuration."
	one := &executionCounts{TestExecutions: 2, Reason: repeat}
	require.Equal(t, repeat, combineExecutionCounts([]*executionCounts{one}).Reason)
	other := &executionCounts{TestExecutions: 1, Reason: "Source-test identities are unavailable because native results are incomplete."}
	require.Equal(t, genericIdentityReason, combineExecutionCounts([]*executionCounts{one, other}).Reason)
}

func TestHandoffNamesTheAPIKeySecretWhenCIIsInstrumented(t *testing.T) {
	result := completeValidationFixture("jest")
	result.CIRuntime = &onboard.RuntimeCheck{Status: onboard.StatusCompatible, Jobs: []onboard.RuntimeFinding{{Node: "20", Status: "compatible"}}}
	result.Summary = summarizeValidation(result)
	require.Equal(t, []string{onboard.CIProviderAPIKeyAction(false)}, result.Summary.HumanActions)
	require.Contains(t, validationHandoff(result), "Human action: Add the DD_API_KEY repository secret")
	result.CIRuntime = nil
	require.Empty(t, summarizeValidation(result).HumanActions)
}

func TestInstalledJasmineCoreLimitationsReachThePlan(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "@wdio", "jasmine-framework"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "jasmine-core"), 0o700))
	requireWriteFile(t, filepath.Join(root, "node_modules", "@wdio", "jasmine-framework", "package.json"), `{"version":"9.31.2"}`)
	requireWriteFile(t, filepath.Join(root, "node_modules", "jasmine-core", "package.json"), `{"version":"5.9.0"}`)
	limitations := installedFeatureLimitations(root, "webdriverio", "")
	require.ElementsMatch(t, webdriverIOJasmineLimitedFeatures, mapKeys(limitations))
	require.Contains(t, limitations["auto-retries"], "jasmine-core 5.9.0")
	require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", "jasmine-core", "package.json"), []byte(`{"version":"6.3.0"}`), 0o600))
	require.Nil(t, installedFeatureLimitations(root, "webdriverio", ""))
	require.Nil(t, installedFeatureLimitations(root, "jest", ""))

	var output bytes.Buffer
	require.NoError(t, onboard.RenderAgentResult(&output, onboard.AgentResult{Status: "needs_input", Plan: &onboard.ExecutionPlan{Framework: "webdriverio", KnownLimitations: limitations}}, "json"))
	require.Contains(t, output.String(), `"known_limitations"`)
}

func TestCIRuntimeTracerFitsEveryCINode(t *testing.T) {
	resolve := func(_ context.Context, version string) (platform.JSSelection, error) {
		return map[string]platform.JSSelection{
			"5.128.0": {Version: "5.128.0", Node: ">=18"},
			"6.17.0":  {Version: "6.17.0", Node: ">=22"},
		}[version], nil
	}
	ci := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Node: "20.18.2"}}}
	got, _ := recommendJavaScriptTracer(t.Context(), jestPreflight{}, ci, resolve, recommendationTags, "")
	require.NotNil(t, got)
	require.Equal(t, "5.128.0", got.Selection.Version)
}

func mapKeys(values map[string]string) []string {
	var keys []string
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestEveryAgentResultSaysToReadItWhole(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, onboard.RenderAgentResult(&output, onboard.AgentResult{Status: "validation_failed", Code: "validation_incomplete"}, "json"))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Contains(t, result.Instruction, onboard.ReadWholeInstruction)
	require.Contains(t, onboard.AgentScope, onboard.PrescribedEditInstruction)
	require.Contains(t, onboard.AgentScope, "Any edit ddtest did not prescribe needs the user's approval first.")
}

func TestPlanLimitationsFollowTheSelectedTracer(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "@wdio", "jasmine-framework"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "jasmine-core"), 0o700))
	requireWriteFile(t, filepath.Join(root, "node_modules", "@wdio", "jasmine-framework", "package.json"), `{"version":"9.31.2"}`)
	requireWriteFile(t, filepath.Join(root, "node_modules", "jasmine-core", "package.json"), `{"version":"5.9.0"}`)
	t.Cleanup(func() { webdriverIOJasmineFixedMinor = map[int]int{} })
	webdriverIOJasmineFixedMinor = map[int]int{6: 22}

	testdrive := &Testdrive{repositoryRoot: root}
	plan := &onboard.ExecutionPlan{Framework: "webdriverio"}
	testdrive.setPlanTracer(plan, "")
	require.Len(t, plan.KnownLimitations, len(webdriverIOJasmineLimitedFeatures), "an unselected tracer stays conservative")
	testdrive.setPlanTracer(plan, "6.22.0")
	require.Equal(t, "6.22.0", plan.Tracer)
	require.Empty(t, plan.KnownLimitations, "a fixed release lifts the limitations")
	testdrive.setPlanTracer(plan, "6.21.0")
	require.Len(t, plan.KnownLimitations, len(webdriverIOJasmineLimitedFeatures))
}

func TestWorkflowBlockersQuoteOneLineOfMultiLineCIScripts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
	fixture := substitutedFixture(false)
	fixture.Scope = &onboard.ValidationScope{Unresolved: []string{"docs.yml / generate / step 4 (gem install rack\ncd rack-3.2*\n): Repository script could not be resolved: gem install rack\ncd rack-3.2*\n"}}
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion}
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		return finishValidation(out, root, fixture)
	}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Contains(t, result.RequiredInput, "unresolved CI scope: docs.yml / generate / step 4 (gem install rack …): Repository script could not be resolved: gem install rack …")
	for _, item := range result.RequiredInput {
		require.NotContains(t, item, "\n")
	}
	require.NotContains(t, result.Reason, "cd rack-3.2*")
}

// Manual CI edits go into the steps ddtest could not resolve, so the Node
// those steps run on decides the release they pin: on node:18, the newest
// release Node 18 runs, not latest.
func TestManualCITracerFitsUnresolvedStepsWithAKnownNode(t *testing.T) {
	releases := map[string]platform.JSSelection{"6.22.0": {Version: "6.22.0", Node: ">=22"}, "5.133.0": {Version: "5.133.0", Node: ">=18"}}
	resolve := func(_ context.Context, version string) (platform.JSSelection, error) { return releases[version], nil }
	tags := func(context.Context) (map[string]string, error) {
		return map[string]string{"latest": "6.22.0", "latest-node18": "5.133.0"}, nil
	}
	ci := onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Workflow: ".buildkite/pipeline.yml", Job: ":mocha: Test", Node: "18", NodeSource: "image node:18", Code: onboard.CodeUnresolvedCommand}}}
	require.Equal(t, "5.133.0", manualCITracer(t.Context(), ci, resolve, tags))
	require.Equal(t, onboard.CodeUnresolvedCommand, ci.Jobs[0].Code, "the caller's check is unchanged")
	ci.Jobs[0].Node = ""
	require.Empty(t, manualCITracer(t.Context(), ci, resolve, tags), "no known CI Node constrains the release")
}

// Sending unresolved CI to the manual instructions keeps the release every
// CI Node runs, which those edits pin.
func TestScopeUnresolvedNeedsKeepTheManualTracer(t *testing.T) {
	prior := "Facts can add prerequisites." + manualCITracerPrefix + `DD_SET_TRACER_VERSION_JS="5.133.0" (every detected CI Node runtime supports it) and pass --tracer-version 5.133.0 to validation.`
	needs := scopeUnresolvedNeeds(prior)
	require.True(t, strings.HasPrefix(needs, "Run next_command for the unsupported CI integration"))
	require.True(t, strings.HasSuffix(needs, `For manual CI edits use DD_SET_TRACER_VERSION_JS="5.133.0" (every detected CI Node runtime supports it) and pass --tracer-version 5.133.0 to validation.`))
	require.NotContains(t, scopeUnresolvedNeeds("Facts can add prerequisites."), "manual CI edits")
}
