package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestCypressSupportFollowsTracerBoundaries(t *testing.T) {
	for _, tc := range []struct{ framework, tracer, want string }{
		{"6.6.0", "5.130.0", "incompatible"},
		{"6.7.0", "5.130.0", "compatible"},
		{"11.9.0", "6.21.0", "incompatible"},
		{"12.0.0", "6.21.0", "compatible"},
		{"16.1.1", "6.21.0", "compatible"},
		{"99.0.0", "6.21.0", "compatible"},
	} {
		require.Equal(t, tc.want, checkCypressSupport(jestPreflight{Version: tc.framework, Node: "22.23.1"}, platform.JSSelection{Version: tc.tracer, Node: ">=18"}).Status)
	}
}

func TestCypressRepeatedFeaturesRequireBodyExecutions(t *testing.T) {
	identity := intake.Test{Module: "cypress", Suite: "probe.cy.js", SourceFile: "probe.cy.js", Name: probeName}
	for _, feature := range []string{"early-flake-detection", "attempt-to-fix"} {
		first := map[string]string{"test.module": identity.Module, "test.suite": identity.Suite, "test.name": identity.Name, "test.status": "pass", "test.test_management.is_attempt_to_fix": "true"}
		retry := map[string]string{}
		for k, v := range first {
			retry[k] = v
		}
		retry["test.is_retry"] = "true"
		retry["test.retry_reason"] = map[string]string{"early-flake-detection": "early_flake_detection", "attempt-to-fix": "attempt_to_fix"}[feature]
		count := 2
		run := validationRun{framework: "cypress", ProbeExecutions: &count, Tests: []jestTest{{Name: probeName, Status: "passed"}},
			Facts: intake.Facts{Events: []intake.Event{{Type: "test", Tags: first}, {Type: "test", Tags: retry}}}}
		require.Equal(t, "passed", evaluateFeature(feature, run, identity).Status)
		count = 1
		require.Equal(t, "failed", evaluateFeature(feature, run, identity).Status, "duplicate telemetry cannot stand in for real body executions")
		run.ProbeExecutions = nil
		require.Equal(t, "failed", evaluateFeature(feature, run, identity).Status)
	}
}

func TestCypressSkippingRequiresTestIdentityAndNoBody(t *testing.T) {
	identity := intake.Test{Module: "cypress", Suite: "probe.cy.js", SourceFile: "probe.cy.js", Name: probeName}
	tags := map[string]string{"test.module": identity.Module, "test.suite": identity.Suite, "test.name": identity.Name, "test.source.file": identity.SourceFile, "test.status": "skip", "test.skipped_by_itr": "true"}
	count := 0
	run := validationRun{framework: "cypress", ProbeExecutions: &count, Tests: []jestTest{{Name: probeName, Status: "skipped"}},
		Facts: intake.Facts{TestEventCount: 1, Events: []intake.Event{{Type: "test", Tags: tags}}}}
	require.Equal(t, "passed", evaluateFeature("skipping", run, identity).Status)
	count = 1
	require.Equal(t, "failed", evaluateFeature("skipping", run, identity).Status)
	count = 0
	tags["test.skipped_by_itr"] = "false"
	require.Equal(t, "failed", evaluateFeature("skipping", run, identity).Status)
	tags["test.skipped_by_itr"] = "true"
	tags["test.name"] = "different test"
	require.Equal(t, "failed", evaluateFeature("skipping", run, identity).Status)
}

func TestCypressNodeEngineUnions(t *testing.T) {
	requirement := "^22.22.2 || ^24.15.0 || >=26.0.0"
	for _, tc := range []struct{ node, want string }{
		{"22.22.1", "incompatible"}, {"22.22.2", "compatible"},
		{"23.0.0", "incompatible"}, {"24.14.1", "incompatible"},
		{"24.15.0", "compatible"}, {"25.9.0", "incompatible"},
		{"26.0.0", "compatible"}, {"22", "inconclusive"},
	} {
		status, _ := checkCypressNodeEngine(tc.node, requirement)
		require.Equal(t, tc.want, status)
	}
	for _, requirement := range []string{"^22.0.0 || invalid", "^0.1.0", "^22.0.0 ||", "^22.0.0 || $(command)"} {
		status, _ := checkCypressNodeEngine("22.23.1", requirement)
		require.Equal(t, "inconclusive", status)
	}
}

func TestCypressNativeResultsKeepDuplicatesAndFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	requireWriteFile(t, path, "{\"complete\":true,\"runs\":[{\"spec\":{\"absolute\":\"/repo/todo.cy.js\"},\"tests\":[{\"title\":[\"Todo\",\"same\"],\"state\":\"passed\"},{\"title\":[\"Todo\",\"same\"],\"state\":\"failed\",\"attempts\":[{\"error\":{\"message\":\"expected 1 to equal 2\\n at test.js\"}}]},{\"title\":[\"pending\"],\"state\":\"pending\"}]}]}")
	var run validationRun
	readCypressResults(path, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 3)
	require.Equal(t, run.Tests[0].Name, run.Tests[1].Name)
	require.Equal(t, "expected 1 to equal 2", run.Tests[1].Failure)
	require.Equal(t, "skipped", run.Tests[2].Status)
	for _, content := range []string{"invalid", "{\"status\":\"failed\",\"runs\":[]}", "{\"status\":\"finished\"}"} {
		requireWriteFile(t, path, content)
		run = validationRun{}
		readCypressResults(path, &run)
		require.NotEmpty(t, run.ResultError)
	}
}

func TestCypressObserverPreservesConfigAndAfterRunHandlers(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, mode := range []string{"baseline", "control", "plugin-retries"} {
		probe := mode != "baseline"
		root := t.TempDir()
		directory := t.TempDir()
		t.Chdir(root)
		config := "module.exports={reporter:'custom',retries:3,e2e:{setupNodeEvents:async(on,cfg)=>{on('after:run',()=>{require('node:fs').writeFileSync('user-after-run','called')});on('task',{existing:()=>1});if(process.env.DDTEST_OBSERVER_RETRY_TEST==='plugin-retries'){if(cfg.retries!==0)throw new Error('native probe retries must be disabled before plugin setup');cfg.retries=2};return {...cfg,custom:'kept'}}}}"
		requireWriteFile(t, filepath.Join(root, "cypress.config.cjs"), config)
		observer := filepath.Join(root, "observer.cjs")
		requireWriteFile(t, observer, cypressObserver)
		cli := filepath.Join(root, "node_modules/cypress/bin/cypress")
		require.NoError(t, os.MkdirAll(filepath.Dir(cli), 0700))
		requireWriteFile(t, cli, "const assert=require('node:assert/strict'); const fs=require('node:fs'); (async()=>{const args=process.argv.slice(3);assert.ok(args.includes('--browser'));assert.ok(args.includes('chrome'));assert.ok(!args.includes('--record'));assert.ok(!args.includes('--parallel'));const configPath=args[args.indexOf('--config-file')+1];assert.equal(fs.realpathSync(require('node:path').dirname(configPath)),fs.realpathSync(process.cwd()));assert.ok(args.indexOf('--config-file')<args.indexOf('--'));const config=require(configPath);assert.equal(config.reporter,'custom');const handlers={};const effective=await config.e2e.setupNodeEvents((event,fn)=>{handlers[event]=event==='task'?{...handlers[event],...fn}:fn},{retries:config.retries});assert.equal(effective.custom,'kept');assert.equal(effective.retries,process.env.DDTEST_OBSERVER_RETRY_TEST==='plugin-retries'?2:process.env.DDTEST_CYPRESS_PROBE?0:3);assert.equal(handlers.task.existing(),1);await handlers['after:run']({status:'finished',runs:[]});assert.equal(fs.readFileSync('user-after-run','utf8'),'called')})().catch(e=>{console.error(e);process.exitCode=1})")
		command := exec.CommandContext(t.Context(), node, "--require", observer, cli, "run", "--browser", "chrome", "--record", "--parallel", "--group", "CI group", "-P"+root, "-Ccypress.config.cjs", "--")
		command.Dir = root
		probePath := ""
		if probe {
			probePath = filepath.Join(root, "probe.cy.js")
		}
		command.Env = append(os.Environ(), "NODE_OPTIONS=", "DDTEST_CYPRESS_DIRECTORY="+directory, "DDTEST_CYPRESS_PROBE="+probePath, "DDTEST_OBSERVER_RETRY_TEST="+mode)
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		original, err := os.ReadFile(filepath.Join(root, "cypress.config.cjs"))
		require.NoError(t, err)
		require.Equal(t, config, string(original))
		var invocation struct{ Args []string }
		content, err := os.ReadFile(filepath.Join(directory, "invocation.json"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(content, &invocation))
		if probe {
			require.Contains(t, invocation.Args, probePath)
		}
	}
}

func TestCypressCoverageRequiresRepositoryApplicationFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"app/source.js", "test.cy.js"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), "// source")
	}
	native := []jestTest{{File: filepath.Join(root, "test.cy.js")}}
	facts := intake.Facts{TestCount: 2, CoveredTestCount: 1, CoverageLevel: "test", Tests: []intake.Test{{SourceFile: "test.cy.js", CoveredFiles: []string{"test.cy.js"}}}}
	coverage := cypressSourceCoverage(root, facts, native)
	require.Equal(t, "not observed", coverage.Status, "SDK-added spec coverage is not application coverage")
	facts.Tests[0].CoveredFiles = []string{"test.cy.js", "../kitchensink-coverage-example/app/source.js"}
	coverage = cypressSourceCoverage(root, facts, native)
	require.Equal(t, "invalid", coverage.Status)
	require.Zero(t, coverage.CoveredTests)
	require.Equal(t, 1, coverage.InvalidCount)
	require.Contains(t, coverage.Reason, "original repository source filename")
	for _, file := range []string{"app/source.js", filepath.Join(root, "app/source.js")} {
		facts.Tests[0].CoveredFiles = []string{"test.cy.js", file}
		coverage = cypressSourceCoverage(root, facts, native)
		require.Equal(t, "observed", coverage.Status)
		require.Equal(t, 1, coverage.CoveredTests)
	}
	outside := filepath.Join(t.TempDir(), "external.js")
	requireWriteFile(t, outside, "// outside")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked.js")))
	facts.Tests[0].CoveredFiles = []string{"linked.js"}
	require.Equal(t, "invalid", cypressSourceCoverage(root, facts, native).Status)
	facts.Tests[0].CoveredFiles = []string{"missing1", "missing2", "missing3", "missing4", "missing5", "missing6"}
	coverage = cypressSourceCoverage(root, facts, native)
	require.Equal(t, 6, coverage.InvalidCount)
	require.Len(t, coverage.InvalidFiles, 5, "diagnostics must stay bounded")
	result := validationResult{Framework: "cypress", Runs: []runSummary{{Coverage: coverage}}}
	require.False(t, missingRealCoverage(result), "Cypress TIA is deferred; coverage does not block the other controls")
	result.Framework = "mocha"
	require.True(t, missingRealCoverage(result), "other frameworks must retain coverage requirements")
}

func TestCypressReportsExecutableStartFailureWithoutOutput(t *testing.T) {
	drive := &Testdrive{repositoryRoot: t.TempDir(), language: "javascript", command: "COVERAGE=true", args: []string{"npm", "run", "test:ci"},
		executor: &fakeTestdriveExecutor{err: &exec.Error{Name: "COVERAGE=true", Err: exec.ErrNotFound}}, nodeVersion: func() string { return "v22.23.1" },
		startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
	session := planSession()
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	run, err := drive.runCypress(t.Context(), &bytes.Buffer{}, session, "", "baseline", false, intake.Scenario{}, "", "")
	require.NoError(t, err)
	require.Equal(t, -1, run.ExitCode)
	require.Contains(t, run.Diagnostic, "COVERAGE=true")
	require.Contains(t, run.Diagnostic, exec.ErrNotFound.Error())
	require.NotEmpty(t, run.ResultError)
}

func TestCypressDefaultExecutesTheUniqueCIConfiguration(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"cypress":"16.1.1"},"scripts":{"test:ci":"cypress run"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - run: npm run test:ci\n        env: {COVERAGE: 'true', TERM: xterm}\n")
	t.Chdir(root)
	drive, err := Prepare("6.21.0")
	require.NoError(t, err)
	require.True(t, drive.allConfigurations, "automatic CI selection must use the configuration executor, including its environment")
	require.Equal(t, "npm", drive.command)
	require.Equal(t, []string{"run", "test:ci"}, drive.args)
}

func TestCypressRejectsSetupFailureDespitePassingNativeResults(t *testing.T) {
	session := planSession()
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	directory := filepath.Join(session.Directory(), "baseline")
	require.NoError(t, os.MkdirAll(directory, 0700))
	requireWriteFile(t, filepath.Join(directory, "results.json"), `{"complete":true,"runs":[{"tests":[{"title":["native"],"state":"passed"}]}]}`)
	requireWriteFile(t, filepath.Join(directory, "execution-error.txt"), "Startup process exited: npm start (2)")
	drive := &Testdrive{repositoryRoot: t.TempDir(), command: "node", executor: &fakeTestdriveExecutor{},
		startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
	run, err := drive.runCypress(t.Context(), &bytes.Buffer{}, session, "", "baseline", false, intake.Scenario{}, "", "")
	require.ErrorContains(t, err, "Cypress execution setup failed")
	require.Contains(t, run.ResultError, "Startup process exited")
	require.Len(t, run.Tests, 1, "complete native results must not hide setup failure")
}

func TestCypressTIAExceptionKeepsOtherControlsMandatory(t *testing.T) {
	result := completeValidationFixture("cypress")
	result.Runs[1].Coverage = &coverageSummary{Status: "not observed"}
	root := t.TempDir()
	var output bytes.Buffer
	require.NoError(t, finishValidation(&output, root, result))
	report := readValidationReport(t, root)
	require.True(t, report.LocalSuccess)
	require.Equal(t, "COMPLETE LOCALLY", report.Summary.Status)
	require.Contains(t, report.Summary.FinalResponse, "Features: 5/5 supported checks passed.")
	require.Contains(t, report.Summary.FinalResponse, cypressTIADeferredReason)
	for i, feature := range result.Features {
		if feature.Name == "skipping" {
			require.Equal(t, "deferred", feature.Status)
			require.False(t, featureSatisfied("jest", feature))
			require.False(t, featureSatisfied("vitest", feature))
			require.False(t, featureSatisfied("mocha", feature))
			continue
		}
		for _, status := range []string{"failed", "inconclusive", "deferred", "not supported"} {
			result.Features[i].Status = status
			require.False(t, featureChecksPassed("cypress", result.Features), "%s %s must block completion", feature.Name, status)
		}
		result.Features[i].Status = feature.Status
	}
	require.False(t, featureSatisfied("cypress", featureResult{Name: "skipping", Status: "not supported"}))
}

func TestCypressReportDisclosesDeferredTIAWithoutCoverageWarning(t *testing.T) {
	model := buildReport(t.TempDir(), intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true}, false, reportRuntime{Framework: "Cypress"})
	var output bytes.Buffer
	require.NoError(t, testdriveReport.Execute(&output, model))
	require.Contains(t, output.String(), cypressTIADeferredReason)
	for _, card := range model.Cards {
		require.NotEqual(t, intake.MissingCoverageFinding, card.Kind)
	}
}
