package testdrive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestSelectedCommandDoesNotCertifyOtherConfigurations(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0644))
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "jest"}, {Directory: ".", Command: "jest --config production.ts"}}}
	result := completeValidationFixture("jest")
	result.Preflight.Command = "npx jest"
	setSingleConfigurationScope(root, &result, scope, result.Preflight.Command, nil)
	require.Equal(t, "passed", result.Configurations[0].Status)
	require.Equal(t, "not exercised", result.Configurations[1].Status)
	prepareValidationResult(root, &result)
	require.False(t, result.Success)
	require.False(t, result.LocalSuccess)
	summary := summarizeValidation(result)
	require.Equal(t, "INCOMPLETE", summary.Status)
	require.Contains(t, strings.Join(summary.BlockingChecks, "\n"), "production.ts: not exercised")
}

func TestAggregationKeepsFailedAndPendingConfigurations(t *testing.T) {
	for _, status := range []string{"passed", "incomplete", "not exercised"} {
		t.Run(status, func(t *testing.T) {
			result := completeValidationFixture("jest")
			result.Scope = &onboard.ValidationScope{}
			result.Configurations = []configurationResult{{TestCommand: onboard.TestCommand{Directory: ".", Command: "jest"}, Status: "passed", PairedExecution: true, ExecutionAttempted: true}, {TestCommand: onboard.TestCommand{Directory: ".", Command: "jest --coverage"}, Status: status, PairedExecution: true, ExecutionAttempted: true}}
			prepareValidationResult(t.TempDir(), &result)
			require.Equal(t, status == "passed", result.Success)
			if status == "passed" {
				result.Scope.Unresolved = []string{"unknown setup may run additional tests"}
				prepareValidationResult(t.TempDir(), &result)
				require.False(t, result.Success)
				require.Contains(t, strings.Join(summarizeValidation(result).BlockingChecks, "\n"), "unresolved CI scope")
			}
		})
	}
}

func TestConfigurationReportsRemainOnePairAndKeepCommandLabels(t *testing.T) {
	root := t.TempDir()
	models := []reportModel{{Tests: []reportTest{{Label: "suite › test", Name: "test"}}, Suites: []reportSuite{{Name: "suite"}}}, {Tests: []reportTest{{Label: "suite › test", Name: "test"}}, Suites: []reportSuite{{Name: "suite"}}}}
	labelConfigurationModel(&models[0], "jest")
	labelConfigurationModel(&models[1], "jest --config production.ts")
	var output bytes.Buffer
	require.NoError(t, writeConfigurationHTML(&output, root, models))
	require.Contains(t, output.String(), "Open report:")
	url, err := fileURL(htmlReportPath(root))
	require.NoError(t, err)
	require.Contains(t, output.String(), url)
	code := 0
	result := completeValidationFixture("jest")
	result.Runs = []runSummary{{Role: roleBaseline, Name: "baseline", Configuration: "jest", Command: "jest", ExitCode: &code}, {Role: roleReporting, Name: "reporting-only", Configuration: "jest", Command: "jest", Instrumented: true, ExitCode: &code}}
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, result))
	html, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Contains(t, string(html), "jest --config production.ts")
	require.NotContains(t, string(html), "intake/")
	require.Contains(t, string(html), `href="testdrive.json"`)
	entries, err := os.ReadDir(filepath.Dir(htmlReportPath(root)))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, validationResult{Framework: "jest", CheckOnly: true, Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var current validationResult
	require.NoError(t, json.Unmarshal(data, &current))
	require.NotNil(t, current.Retained)
	require.Equal(t, "jest", current.Retained.Result.Runs[0].Configuration)
	require.False(t, current.Success)
}

func TestCombinedHTMLPreservesCoverageLinksAndFullCommandOutput(t *testing.T) {
	root := t.TempDir()
	var models []reportModel
	for i := range 2 {
		facts := intake.Facts{
			TestEventCount: 1, TestCount: 1, CoverageLevel: "suite",
			Tests:          []intake.Test{{Module: "module", Suite: "shared", Name: "test", Status: "pass"}},
			SuiteCoverages: []intake.SuiteCoverage{{Module: "module", Suite: "shared", Files: []string{"source.js"}, CoveredTests: 1}},
			SlowSuites:     []intake.SlowSuite{{Module: "module", Suite: "shared", Duration: 6 * time.Second}},
		}
		model := buildReport(root, facts, false, reportRuntime{Framework: "Jest", Tracer: "dd-trace", Command: fmt.Sprintf("jest --config variant-%d.ts", i), Output: strings.Repeat(fmt.Sprintf("variant-%d output\n", i), 200)})
		labelConfigurationModel(&model, model.Runtime.Command)
		models = append(models, model)
	}
	for range 2 {
		require.NoError(t, writeConfigurationHTML(&bytes.Buffer{}, root, models))
		page, err := os.ReadFile(htmlReportPath(root))
		require.NoError(t, err)
		for i := range 2 {
			require.Equal(t, 1, strings.Count(string(page), fmt.Sprintf(`id="test-detail-%d"`, i)))
			require.Equal(t, 2, strings.Count(string(page), fmt.Sprintf(`data-open-test="test-detail-%d"`, i)))
			require.Equal(t, 200, strings.Count(string(page), fmt.Sprintf("variant-%d output", i)))
			require.Equal(t, 0, models[i].Suites[0].Tests[0].TestIndex, "aggregation must not mutate shared source rows")
		}
		require.Contains(t, string(page), "Suite coverage")
		require.Contains(t, string(page), "source.js")
	}
}

func TestSourceRunDoesNotValidateCIPreparedConfiguration(t *testing.T) {
	root := t.TempDir()
	result := completeValidationFixture("jest")
	result.Preflight.Command = "jest"
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{
		{Directory: ".", Command: "jest"},
		{Directory: ".", Command: "jest", UnvalidatedSetup: []string{"React dependency replacement"}},
	}}
	setSingleConfigurationScope(root, &result, scope, result.Preflight.Command, nil)
	require.Equal(t, "passed", result.Configurations[0].Status)
	require.Equal(t, "not exercised", result.Configurations[1].Status)
	result.Success, result.LocalSuccess = true, true
	applyScopeVerdict(&result)
	require.False(t, result.Success)
	require.False(t, result.LocalSuccess)
}

func TestAllDoesNotExecuteAConfigurationWithoutItsCISetup(t *testing.T) {
	drive := preparedTestdrive(t)
	root := drive.repositoryRoot
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest"}}`)
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: sed -i~ 's/src/dist/e' jest.config.js\n      - run: npm test\n")
	executor := &buildExecutor{t: t}
	drive.executor = executor
	require.ErrorContains(t, drive.runAllConfigurations(t.Context(), &bytes.Buffer{}), "requires separate CI setup")
	require.Empty(t, executor.commands, "must not certify source tests as a built-package configuration")
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var result validationResult
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, "INCOMPLETE", result.Summary.Status)
	require.Equal(t, "blocked", result.Configurations[0].Status)
	require.Empty(t, result.Runs)
	require.NotEmpty(t, result.Configurations[0].UnvalidatedSetup)
}

func TestAllRecoversAnOwnedProbeBeforeResolvingSetup(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.allConfigurations = true
	require.NoError(t, os.MkdirAll(filepath.Join(drive.repositoryRoot, ".github", "workflows"), 0755))
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, ".github", "workflows", "ci.yml"), "jobs:\n  test:\n    steps:\n      - run: cp source.js jest.config.js\n      - run: npm test\n")
	probe := filepath.Join(drive.repositoryRoot, "abandoned.test.js")
	require.NoError(t, createOwnedProbe(drive.repositoryRoot, probe, []byte(probeSource)))
	require.Error(t, drive.Run(t.Context(), &bytes.Buffer{}))
	require.NoFileExists(t, probe, "an interrupted probe must not leak into a subsequent --all baseline")
}

func TestConfigurationScopeUsesExecutionRatherThanInspection(t *testing.T) {
	root := t.TempDir()
	result := completeValidationFixture("jest")
	result.Preflight.Command = "jest --showConfig"
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "jest"}}}
	setSingleConfigurationScope(root, &result, scope, "jest", nil)
	require.Equal(t, "passed", result.Configurations[0].Status)
	require.True(t, result.Configurations[0].PairedExecution)
}

func TestCheckOnlyConfigurationPreservesPassingChecks(t *testing.T) {
	result := validationResult{Framework: "jest", CheckOnly: true, Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}, Configurations: []configurationResult{{Status: "not exercised"}}}
	require.NoError(t, finishValidation(&bytes.Buffer{}, t.TempDir(), result))
	prepareValidationResult(t.TempDir(), &result)
	require.True(t, result.ChecksPassed)
	require.False(t, result.Success)
	require.False(t, result.LocalSuccess)
}

func TestPackageNativeIdentitiesKeepCheckoutPrefixes(t *testing.T) {
	root := t.TempDir()
	run := validationRun{root: root, framework: "jest", Tests: []jestTest{{File: filepath.Join(root, "packages/a/one.test.js"), Name: "test"}, {File: filepath.Join(root, "packages/b/one.test.js"), Name: "test"}}}
	counts := nativeExecutionCounts(run)
	require.Equal(t, 2, *counts.SourceTestIdentities)
}
