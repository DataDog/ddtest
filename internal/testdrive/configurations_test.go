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
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"vitest run"}}`), 0644))
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "vitest run"}, {Directory: ".", Command: "vitest run --config production.ts"}}}
	result := validationResult{Framework: "vitest", Preflight: &jestPreflight{Command: "npx vitest run", Verdict: verdict{Status: "compatible"}}, Compatibility: verdict{Status: "compatible"}, Features: []featureResult{{Name: "auto-retries", Status: "passed"}}}
	setSingleConfigurationScope(root, &result, scope)
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
			result := validationResult{Scope: &onboard.ValidationScope{}, Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}, Compatibility: verdict{Status: "compatible"}, Features: []featureResult{{Name: "early-flake-detection", Status: "passed"}}, Configurations: []configurationResult{{TestCommand: onboard.TestCommand{Directory: ".", Command: "vitest run"}, Status: "passed"}, {TestCommand: onboard.TestCommand{Directory: ".", Command: "vitest run --coverage"}, Status: status}}}
			prepareValidationResult(t.TempDir(), &result)
			require.Equal(t, status == "passed", result.Success)
			if status == "passed" {
				result.Scope.Unresolved = []string{"unknown setup may run additional tests"}
				prepareValidationResult(t.TempDir(), &result)
				require.False(t, result.Success)
				require.Contains(t, summarizeValidation(result).OnboardingResponse, "unresolved validation scope")
			}
		})
	}
}

func TestConfigurationReportsRemainOnePairAndKeepCommandLabels(t *testing.T) {
	root := t.TempDir()
	models := []reportModel{{Tests: []reportTest{{Label: "suite › test", Name: "test"}}, Suites: []reportSuite{{Name: "suite"}}}, {Tests: []reportTest{{Label: "suite › test", Name: "test"}}, Suites: []reportSuite{{Name: "suite"}}}}
	labelConfigurationModel(&models[0], "vitest run")
	labelConfigurationModel(&models[1], "vitest run --config production.ts")
	require.NoError(t, writeConfigurationHTML(root, models))
	code := 0
	result := validationResult{Framework: "vitest", Compatibility: verdict{Status: "compatible"}, Features: []featureResult{{Name: "early-flake-detection", Status: "passed"}}, Runs: []runSummary{{Name: "baseline", Configuration: "vitest run", Command: "vitest run", ExitCode: &code}, {Name: "reporting-only", Configuration: "vitest run", Command: "vitest run", Instrumented: true, ExitCode: &code}}}
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, result))
	html, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Contains(t, string(html), "vitest run --config production.ts")
	require.NotContains(t, string(html), "intake/")
	require.Contains(t, string(html), `href="testdrive.json"`)
	entries, err := os.ReadDir(filepath.Dir(htmlReportPath(root)))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, validationResult{Framework: "vitest", CheckOnly: true, Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var current validationResult
	require.NoError(t, json.Unmarshal(data, &current))
	require.NotNil(t, current.Retained)
	require.Equal(t, "vitest run", current.Retained.Result.Runs[0].Configuration)
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
		model := buildReport(root, facts, false, reportRuntime{Framework: "Vitest", Tracer: "dd-trace", Command: fmt.Sprintf("vitest run --config variant-%d.ts", i), Output: strings.Repeat(fmt.Sprintf("variant-%d output\n", i), 200)})
		labelConfigurationModel(&model, model.Runtime.Command)
		models = append(models, model)
	}
	for range 2 {
		require.NoError(t, writeConfigurationHTML(root, models))
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
	result := validationResult{Framework: "vitest", Preflight: &jestPreflight{Command: "vitest run"}, Compatibility: verdict{Status: "compatible"}, Features: []featureResult{{Name: "auto-retries", Status: "passed"}}}
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{
		{Directory: ".", Command: "vitest run"},
		{Directory: ".", Command: "vitest run", UnvalidatedSetup: []string{"React dependency replacement"}},
	}}
	setSingleConfigurationScope(root, &result, scope)
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
	require.Equal(t, "not exercised", result.Configurations[0].Status)
	require.Empty(t, result.Runs)
	require.NotEmpty(t, result.Configurations[0].UnvalidatedSetup)
}
