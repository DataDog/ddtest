package testdrive

import (
	"bytes"
	"fmt"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/tinylib/msgp/msgp"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestMavenSeparatesMatchingFailuresFromUnnamedTelemetry(t *testing.T) {
	baseline := validationRun{ExitCode: 1, Tests: []jestTest{{File: "core / ExampleTest", Name: "existingFailure", Status: "failed", Failure: "AssertionError: expected"}}}
	reporting := baseline
	reporting.mavenModules = map[string]string{"Core": "core"}
	reporting.Facts = intake.Facts{TestEventCount: 1, Tests: []intake.Test{{Module: "Core", Suite: "ExampleTest", Attempts: []intake.TestRun{{Status: "fail"}}}}}
	native, telemetry := compareMavenNative(baseline, reporting), verifyMavenTelemetry(reporting)
	require.Equal(t, "compatible", native.Status)
	require.Contains(t, native.Reason, "Matching existing failures are acceptable")
	require.Equal(t, "java_test_name_missing", telemetry.Code)
	require.Equal(t, 2, telemetry.DifferenceCount)
	require.Contains(t, strings.Join(telemetry.Differences, "\n"), `test="existingFailure"`)
	result := validationResult{Framework: "testng", HTMLReportCurrent: true, Cleanup: &verdict{Status: "passed"}, CISelection: &verdict{Status: "inconclusive"}, NativeComparison: &native, TelemetryComparison: &telemetry, Compatibility: telemetry,
		Runs: []runSummary{(validationRun{Role: roleBaseline, Command: "mvn test"}).summary(), (validationRun{Role: roleReporting, Command: "mvn test", Instrumented: true}).summary()}}
	unavailableFeatures(&result, mavenFeatureBlockReason(result))
	result.Summary = summarizeValidation(result)
	handoff := validationHandoff(result)
	require.Contains(t, handoff, "Native outcome parity: compatible")
	require.Contains(t, handoff, "Telemetry identity reconciliation: inconclusive")
	require.Contains(t, handoff, "Features: not run")
	require.Contains(t, handoff, "java_test_name_missing")
	require.NotContains(t, handoff, "0/6 supported checks passed")
	require.Len(t, result.Summary.BlockingChecks, 3, "comparison, one feature blocker, and CI selection; no six duplicated probe blockers")
}

func TestMavenDifferencesAreBoundedWithoutHidingMultiplicity(t *testing.T) {
	run := validationRun{mavenModules: map[string]string{"module": "."}}
	for i := range 25 {
		run.Tests = append(run.Tests, jestTest{File: ". / ExampleTest", Name: fmt.Sprintf("test%02d", i), Status: "passed"})
		run.Facts.Tests = append(run.Facts.Tests, intake.Test{Module: "module", Suite: "ExampleTest", Name: fmt.Sprintf("other%02d", i), Attempts: []intake.TestRun{{Status: "pass"}}})
	}
	result := verifyMavenTelemetry(run)
	require.Equal(t, "java_identity_mismatch", result.Code)
	require.Equal(t, 50, result.DifferenceCount)
	require.Len(t, result.Differences, maxReportDifferences)
	require.Equal(t, result, verifyMavenTelemetry(run), "bounded diagnostics must be deterministic")
	baseline := validationRun{Tests: []jestTest{{File: ". / T", Name: "test", Status: "failed", Failure: "Expected: " + strings.Repeat("x", 3000)}}}
	reporting := baseline
	reporting.Tests = []jestTest{{File: ". / T", Name: "test", Status: "failed", Failure: "Different exception"}}
	native := compareMavenNative(baseline, reporting)
	require.Equal(t, "suspected regression", native.Status)
	require.Equal(t, "native_outcome_mismatch", native.Code)
	require.Equal(t, 2, native.DifferenceCount)
	for _, difference := range native.Differences {
		require.LessOrEqual(t, len([]rune(difference)), diagnosticLimit+20)
	}
}

func TestMavenLocalPassDoesNotBecomeZeroPassedCIConfigurations(t *testing.T) {
	result := validationResult{Framework: "junit5", LocalSuccess: true, Compatibility: verdict{Status: "compatible"}, CISelection: &verdict{Status: "inconclusive"},
		Runs: []runSummary{(validationRun{Role: roleBaseline, Command: "mvn test"}).summary(), (validationRun{Role: roleReporting, Command: "mvn test", Instrumented: true}).summary()}}
	result.Summary = summarizeValidation(result)
	require.Nil(t, result.Summary.ConfigurationCoverage, "CI matrix discovery was not performed; zero counts are misleading")
	require.Equal(t, "selected Maven command", result.Summary.ExecutedValidation.Scope)
	require.Equal(t, 1, result.Summary.ExecutedValidation.Passed)
	require.Equal(t, "INCOMPLETE", result.Summary.Status)
	handoff := validationHandoff(result)
	require.Contains(t, handoff, "Selected Maven command validation: PASSED")
	require.Contains(t, handoff, "Onboarding validation: INCOMPLETE. Local validation: PASSED")
	require.NotContains(t, handoff, "INCOMPLETE (0/1)")
	result.CheckOnly = true
	summary := summarizeValidation(result)
	require.Zero(t, summary.ExecutedValidation.Configurations)
	require.Equal(t, "NOT EXERCISED", summary.ExecutedValidation.Status)
}

func TestMavenCoverageDistinguishesNativeResultsAndUnreachedModules(t *testing.T) {
	projects := []maven.Project{{Directory: ".", Artifact: "parent", Name: "Parent", Packaging: "pom"}, {Directory: "core", Artifact: "core", Name: "Core"}, {Directory: "later", Artifact: "later", Name: "Later"}, {Directory: "optional", Artifact: "optional"}}
	modules := observeMavenModules(projects, []maven.Test{{Module: "core", Name: "test", Status: "failed"}}, "[INFO] Core ................ FAILURE [ 3.0 s]\n[INFO] Later ............... SKIPPED\n")
	require.Equal(t, "aggregator (no native results)", modules[0].Status)
	require.Equal(t, "observed", modules[1].Status)
	require.Equal(t, "failure", modules[1].ReactorStatus)
	require.Equal(t, "not reached", modules[2].Status)
	require.Equal(t, "not observed", modules[3].Status, "a profile candidate is not proof of an active/excluded module")
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	for _, name := range []string{"test", "deploy"} {
		action := ""
		if name == "test" {
			action = "      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: java\n"
		}
		text := "jobs:\n  tests:\n    steps:\n" + action + "      - run: mvn " + name + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows", name+".yml"), []byte(text), 0600))
	}
	coverage := discoverMavenCoverage(root)
	require.Len(t, coverage.Workflows, 2)
	require.Equal(t, "missing", coverage.Workflows[0].Instrumentation)
	require.Equal(t, "present (static only)", coverage.Workflows[1].Instrumentation)
	for _, workflow := range coverage.Workflows {
		require.Equal(t, "not exercised", workflow.Execution)
	}
	var output bytes.Buffer
	coverage.Modules = modules
	writeMavenCoverage(&output, coverage)
	require.Contains(t, output.String(), "later: not reached")
	require.Contains(t, output.String(), "deploy.yml: instrumentation missing")
}

func TestMavenUnresolvedWorkflowDiscoveryDoesNotClaimZeroCoverage(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte("jobs: [invalid"), 0600))
	coverage := discoverMavenCoverage(root)
	require.Equal(t, "inconclusive", coverage.WorkflowDiscovery)
	require.Contains(t, coverage.Reason, "discovery is unresolved")
	require.Contains(t, coverage.Reason, "selected command")
}

func TestMavenCannotSubstituteParameterIndexForMissingMethodName(t *testing.T) {
	run := validationRun{mavenModules: map[string]string{"module": "."}, Tests: []jestTest{{File: ". / ExampleTest", Name: "[1]", Status: "passed"}},
		Facts: intake.Facts{Tests: []intake.Test{{Module: "module", Suite: "ExampleTest", Parameters: `{"metadata":{"test_name":"[1]"}}`, Attempts: []intake.TestRun{{Status: "pass"}}}}}}
	// Aggregate counts and the derived index agree, but the actual method is unknown.
	result := verifyMavenTelemetry(run)
	require.Equal(t, "inconclusive", result.Status)
	require.Equal(t, "java_test_name_missing", result.Code)
	require.Contains(t, result.Reason, "empty test.name")
}

func TestMavenMissingNameDiagnosticUsesActualIntakeProvenance(t *testing.T) {
	server, err := intake.StartScenario(t.TempDir(), intake.Scenario{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Close()) })
	var events []any
	for i, status := range []string{"pass", "fail"} {
		events = append(events, map[string]any{"type": "test", "content": map[string]any{"span_id": uint64(i + 1), "meta": map[string]any{"test.module": "Core", "test.suite": "ExampleTest", "test.status": status}}})
	}
	payload, err := msgp.AppendIntf(nil, map[string]any{"events": events})
	require.NoError(t, err)
	response, err := http.Post(server.URL()+constants.TestCycleURLPath, "application/msgpack", bytes.NewReader(payload))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.NoError(t, server.Close())
	facts, err := server.Facts()
	require.NoError(t, err)
	require.Len(t, facts.Tests, 2, "different unnamed spans in one class must not become retries of one fabricated test")
	require.Empty(t, facts.FlakyTests, "a pass and failure of unknown methods do not establish a flaky test")
	for _, test := range facts.Tests {
		require.True(t, test.NameMissing)
		require.NotEmpty(t, test.Name, "report display labels still need a fallback")
	}
	run := validationRun{mavenModules: map[string]string{"Core": "core"}, Tests: []jestTest{{File: "core / ExampleTest", Name: "first", Status: "passed"}, {File: "core / ExampleTest", Name: "second", Status: "failed"}}, Facts: facts}
	verdict := verifyMavenTelemetry(run)
	require.Equal(t, "java_test_name_missing", verdict.Code)
	require.Contains(t, verdict.Reason, "2 Java test executions")
}
