// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"io"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func computedAxisFixture(status, preflight string) validationResult {
	result := completeValidationFixture("playwright")
	result.Preflight = &jestPreflight{Verdict: verdict{Status: "compatible"}, Version: "1.55.1"}
	result.Workflow = &WorkflowEvidence{Checks: []onboard.WorkflowCheck{{Name: "git_report_exclusions", Status: "passed"}, {Name: "validation_cleanup", Status: "passed"}}}
	result.Scope = &onboard.ValidationScope{ComputedAxes: []onboard.ComputedAxis{{Workflow: ".github/workflows/test.yaml", Job: "test", Axis: "playwrightVersion"}}}
	result.Configurations = []configurationResult{
		{TestCommand: onboard.TestCommand{Command: "npm test", Locations: []string{`.github/workflows/test.yaml / test (os: "ubuntu-22.04", playwrightVersion: "${{ matrix.playwrightVersion }}") / step 6 (npm test)`}},
			Status: status, PairedExecution: true, ExecutionAttempted: true, Compatibility: verdict{Status: "compatible"},
			Preflight: &jestPreflight{Verdict: verdict{Status: preflight}, Version: "1.55.1"}},
		// A Windows row ddtest doesn't instrument never decides the axis.
		{TestCommand: onboard.TestCommand{Command: "npm test", CIInstrumentationExcluded: true, WindowsRunner: true, Locations: []string{`.github/workflows/test.yaml / test (os: "windows-latest", playwrightVersion: "latest") / step 6 (npm test)`}},
			Status: "excluded", Compatibility: verdict{Status: "not exercised"}},
	}
	return result
}

func TestComputedAxisIsACaveatOnceItsJobValidated(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, finishValidation(io.Discard, root, computedAxisFixture("passed", "compatible")))
	report := readValidationReport(t, root)
	require.Equal(t, "COMPLETE LOCALLY", report.Summary.Status, report.Summary.BlockingChecks)
	require.Empty(t, report.Summary.BlockingChecks)
	require.Len(t, report.Summary.Caveats, 1)
	require.Contains(t, report.Summary.Caveats[0], "matrix axis playwrightVersion is computed at runtime; tests were validated with the installed Playwright 1.55.1")
	require.Contains(t, report.Summary.FinalResponse, "Caveat (not a blocker): .github/workflows/test.yaml / test: matrix axis playwrightVersion")
	require.Contains(t, report.Summary.FinalResponse, "Windows runners: 1 not instrumented")
	require.NotContains(t, report.Summary.FinalResponse, "CI opt-outs:")
}

func TestComputedAxisBlocksWhenItsJobDidNotValidate(t *testing.T) {
	for name, fixture := range map[string]validationResult{
		"failed configuration":          computedAxisFixture("incomplete", "compatible"),
		"unsupported framework version": computedAxisFixture("passed", "incompatible"),
		"no configuration in the job": func() validationResult {
			r := computedAxisFixture("passed", "compatible")
			r.Configurations = r.Configurations[1:]
			return r
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			caveats, blockers := ciFidelity(&fixture)
			require.Empty(t, caveats)
			require.Len(t, blockers, 1)
			require.Contains(t, blockers[0], "other playwrightVersion values remain unverified")
			summary := summarizeValidation(fixture)
			require.NotEqual(t, "COMPLETE LOCALLY", summary.Status)
			require.Contains(t, summary.BlockingChecks, "unresolved CI scope: "+blockers[0])
		})
	}
}

func TestAcceptedSubstituteRuntimeIsReportedAsACaveat(t *testing.T) {
	result := completeValidationFixture("cypress")
	result.RuntimeSubstitution = &runtimeSubstitution{Source: "cypress-io/github-action@v6", CI: "20", Local: "v24.19.0", Accepted: true}
	caveats, blockers := ciFidelity(&result)
	require.Empty(t, blockers)
	require.Equal(t, []string{"runtime fidelity: validated on substitute Node v24.19.0; cypress-io/github-action@v6 runs on Node 20 (accepted)."}, caveats)
	result.RuntimeSubstitution.Accepted = false
	caveats, _ = ciFidelity(&result)
	require.Empty(t, caveats, "an unaccepted substitute stays a blocker, not a caveat")
}

func TestScopeCheckIsRefreshedAfterSetupAndNotRepeatedAsABlocker(t *testing.T) {
	before := []onboard.WorkflowCheck{{Name: "preflight_preparation", Status: "passed"}, scopeCheck([]string{"windows row setup"})}
	refreshed := refreshScopeCheck(before, &onboard.ValidationScope{})
	require.Equal(t, "passed", refreshed[1].Status, "ddtest's own Windows guard resolved the entry")
	require.Equal(t, onboard.StatusInconclusive, before[1].Status, "the input is not mutated")
	require.Equal(t, before, refreshScopeCheck(before, nil))

	result := completeValidationFixture("playwright")
	result.Scope = &onboard.ValidationScope{Unresolved: []string{"job: unknown wrapper"}}
	result.Workflow = &WorkflowEvidence{Checks: refreshScopeCheck(before, result.Scope)}
	summary := summarizeValidation(result)
	require.Equal(t, []string{"unresolved CI scope: job: unknown wrapper"}, summary.BlockingChecks, "the scope check doesn't repeat the entry")
	require.Contains(t, validationHandoff(result), "Git/package exclusions and owned cleanup: passed")
}
