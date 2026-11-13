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
	require.True(t, result.LocalSuccess, "completed local evidence is separate from unvalidated CI configurations")
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
	models := []reportModel{{Runtime: reportRuntime{Framework: "Jest"}, Tests: []reportTest{{Label: "suite › test", Name: "test"}}, Suites: []reportSuite{{Name: "suite"}}}, {Runtime: reportRuntime{Framework: "Jest"}, Tests: []reportTest{{Label: "suite › test", Name: "test"}}, Suites: []reportSuite{{Name: "suite"}}}}
	models[0].Configuration = "jest"
	models[1].Configuration = "jest --config production.ts"
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
	require.NotContains(t, string(html), "Test results with Datadog enabled.")
	require.NotContains(t, string(html), "Test runs with Datadog")
	require.NotContains(t, string(html), `class="empty-findings"`)
	_, subtitle, found := strings.Cut(string(html), `<p class="subtitle">`)
	require.True(t, found)
	subtitle, _, _ = strings.Cut(subtitle, "</p>")
	require.Equal(t, "Jest", strings.TrimSpace(subtitle))
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
		model.Configuration = model.Runtime.Command
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

func TestConfigurationReportLabelsDisambiguatePreparation(t *testing.T) {
	build := func(command string) []onboard.BuildCommand {
		return []onboard.BuildCommand{{Directory: ".", Command: command}}
	}
	for _, tc := range []struct {
		name     string
		commands []onboard.TestCommand
		want     []string
	}{
		{name: "one build stays plain", commands: []onboard.TestCommand{{Command: "npm test", Prerequisites: build("npm run build:cjs")}}, want: []string{"npm test"}},
		{name: "different builds", commands: []onboard.TestCommand{
			{Command: "npm test", Prerequisites: build("npm run build:cjs")},
			{Command: "npm test", Prerequisites: build("npm run build:esm")},
		}, want: []string{"npm test (after npm run build:cjs)", "npm test (after npm run build:esm)"}},
		{name: "with and without preparation", commands: []onboard.TestCommand{
			{Command: "npm test"},
			{Command: "npm test", Prerequisites: build("npm run build:cjs")},
		}, want: []string{"npm test (without preparation)", "npm test (after npm run build:cjs)"}},
		{name: "different environments stay plain", commands: []onboard.TestCommand{
			{Command: "npm test", Environment: map[string]string{"MODE": "cjs"}, Prerequisites: build("npm run build:cjs")},
			{Command: "npm test", Environment: map[string]string{"MODE": "esm"}, Prerequisites: build("npm run build:esm")},
		}, want: []string{"npm test (MODE=cjs)", "npm test (MODE=esm)"}},
		{name: "different preparation environments", commands: []onboard.TestCommand{
			{Command: "npm test", Prerequisites: []onboard.BuildCommand{{Command: "npm run build", Environment: map[string]string{"MODE": "cjs"}}}},
			{Command: "npm test", Prerequisites: []onboard.BuildCommand{{Command: "npm run build", Environment: map[string]string{"MODE": "esm"}}}},
		}, want: []string{"npm test (after npm run build (MODE=cjs))", "npm test (after npm run build (MODE=esm))"}},
		{name: "fallback for identical display text", commands: []onboard.TestCommand{
			{Command: "npm test", Prerequisites: []onboard.BuildCommand{{Command: "npm run build", Kind: onboard.PreparationPackageBuild}}},
			{Command: "npm test", Prerequisites: []onboard.BuildCommand{{Command: "npm run build", Kind: onboard.PreparationDependencies}}},
		}, want: []string{"npm test (after npm run build) [CI entry 1]", "npm test (after npm run build) [CI entry 2]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 2 {
				require.Equal(t, tc.want, configurationReportLabels(tc.commands, "jest"))
			}
		})
	}
}

func TestConfigurationReportLabelsRedactPreparationSecrets(t *testing.T) {
	commands := []onboard.TestCommand{
		{Command: "cypress run --key first-recording-secret", Environment: map[string]string{"CYPRESS_RECORD_KEY": "first-environment-secret"}, Prerequisites: []onboard.BuildCommand{{Command: "cp cjs.json config.json", Environment: map[string]string{"CYPRESS_RECORD_KEY": "first-build-secret"}}}},
		{Command: "cypress run --key second-recording-secret", Environment: map[string]string{"CYPRESS_RECORD_KEY": "second-environment-secret"}, Prerequisites: []onboard.BuildCommand{{Command: "cp esm.json config.json", Environment: map[string]string{"CYPRESS_RECORD_KEY": "second-build-secret"}}}},
	}
	labels := configurationReportLabels(commands, "cypress")
	require.NotEqual(t, labels[0], labels[1])
	require.Contains(t, labels[0], "cp cjs.json config.json")
	require.Contains(t, labels[1], "cp esm.json config.json")
	for _, secret := range []string{"first-recording-secret", "second-recording-secret", "first-environment-secret", "second-environment-secret", "first-build-secret", "second-build-secret"} {
		require.NotContains(t, strings.Join(labels, "\n"), secret)
	}
	require.Equal(t, "first-environment-secret", commands[0].Environment["CYPRESS_RECORD_KEY"])
	require.Equal(t, "first-build-secret", commands[0].Prerequisites[0].Environment["CYPRESS_RECORD_KEY"])
}

func TestCombinedHTMLLabelsOnlyResultsThatNeedDistinguishing(t *testing.T) {
	primary := reportModel{
		Configuration: "npm test", EvidenceRole: roleReporting, EvidenceName: "reporting-only",
		Runtime: reportRuntime{Framework: "Jest", Command: "npm test", Output: "Test results written to: /tmp/reporting-only/jest-results.json", Error: "exit status 1"},
		Tests:   []reportTest{{Label: "cart.test.js › rejects invalid coupons", Name: "rejects invalid coupons", Status: "Failed"}},
		Suites:  []reportSuite{{Name: "cart.test.js"}},
		Cards:   []reportCard{{Title: "Failed tests", Kind: "failed", Count: 1}},
	}
	other := primary
	other.Configuration = "npm run test:integration"
	other.Runtime.Command = other.Configuration
	repeated := primary
	repeated.EvidenceRole, repeated.EvidenceName = roleRepeat, "reporting-only-repeat"
	repeated.Runtime.Output = "repeat output"
	buildLabels := configurationReportLabels([]onboard.TestCommand{
		{Command: "npm test", Prerequisites: []onboard.BuildCommand{{Command: "npm run build:cjs"}}},
		{Command: "npm test", Prerequisites: []onboard.BuildCommand{{Command: "npm run build:esm"}}},
	}, "jest")
	cjs, esm, cjsRepeat := primary, primary, repeated
	cjs.Configuration, esm.Configuration, cjsRepeat.Configuration = buildLabels[0], buildLabels[1], buildLabels[0]
	for _, tc := range []struct {
		name   string
		models []reportModel
		titles []string
	}{
		{name: "one command", models: []reportModel{primary}, titles: []string{"Failed tests"}},
		{name: "two commands", models: []reportModel{primary, other}, titles: []string{"npm test — Failed tests", "npm run test:integration — Failed tests"}},
		{name: "repeated command", models: []reportModel{primary, repeated}, titles: []string{"npm test — Failed tests", "npm test (repeat) — Failed tests"}},
		{name: "different builds with a repeated comparison", models: []reportModel{cjs, cjsRepeat, esm}, titles: []string{"npm test (after npm run build:cjs) — Failed tests", "npm test (after npm run build:cjs) (repeat) — Failed tests", "npm test (after npm run build:esm) — Failed tests"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for range 2 {
				require.NoError(t, writeConfigurationHTML(&bytes.Buffer{}, root, tc.models))
				data, err := os.ReadFile(htmlReportPath(root))
				require.NoError(t, err)
				report := string(data)
				for _, title := range tc.titles {
					require.Contains(t, report, `<h2 class="card-name">`+title+`</h2>`)
				}
				require.NotContains(t, report, "configuration 1")
				_, commandLine, found := strings.Cut(report, `<p class="command-line">`)
				require.True(t, found)
				commandLine, _, _ = strings.Cut(commandLine, "</p>")
				require.NotContains(t, commandLine, "reporting-only")
				require.NotContains(t, report, "Test run 1")
				if len(tc.models) == 1 {
					require.Contains(t, report, `<code>npm test</code>`)
					require.Contains(t, report, "Command failed: exit status 1")
					require.NotContains(t, report, "npm test —")
				}
				for _, model := range tc.models {
					require.Contains(t, report, model.Runtime.Output)
					require.Equal(t, "Failed tests", model.Cards[0].Title)
					require.Equal(t, "cart.test.js", model.Suites[0].Name)
					require.Equal(t, "cart.test.js › rejects invalid coupons", model.Tests[0].Label)
				}
			}
		})
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
	require.True(t, result.LocalSuccess, "completed local evidence is separate from unvalidated CI configurations")
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

func TestAllKeepsASuspectedRegressionFailedLikeASingleRun(t *testing.T) {
	regression := verdict{Status: "suspected regression", Reason: "differs"}
	inconclusive := verdict{Status: "inconclusive", Reason: "not compared"}
	require.Equal(t, regression, combineConfigurationCompatibility(regression, inconclusive), "a later blocked configuration must not hide the regression")
	require.Equal(t, inconclusive, combineConfigurationCompatibility(verdict{Status: "compatible"}, inconclusive))

	result := completeValidationFixture("jest")
	result.Compatibility = combineConfigurationCompatibility(regression, inconclusive)
	result.Scope = &onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "jest a"}, {Directory: ".", Command: "jest b"}}}
	result.Configurations = []configurationResult{{TestCommand: result.Scope.Commands[0], Status: "incomplete"}, {TestCommand: result.Scope.Commands[1], Status: "blocked"}}
	root := t.TempDir()
	require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
	report := readValidationReport(t, root)
	require.Equal(t, "FAILED", report.Summary.Status)
	require.Equal(t, "FAILED", report.Summary.LocalValidation)
}

func TestTracerMismatchNamesBothConfigurations(t *testing.T) {
	err := tracerMismatchError("npm run test:local", "dd-trace@5.132.0", "npm run test:sauce", "dd-trace@6.21.0")
	require.EqualError(t, err, "tracer selection differs between configurations: npm run test:local uses dd-trace@5.132.0, npm run test:sauce uses dd-trace@6.21.0; rerun with an exact --tracer-version that both support")
}
