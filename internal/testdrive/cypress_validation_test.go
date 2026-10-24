package testdrive

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
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

func TestCypressNodeEngineUnions(t *testing.T) {
	requirement := "^22.22.2 || ^24.15.0 || >=26.0.0"
	for _, tc := range []struct{ node, want string }{
		{"22.22.1", "incompatible"}, {"22.22.2", "compatible"},
		{"23.0.0", "incompatible"}, {"24.14.1", "incompatible"},
		{"24.15.0", "compatible"}, {"25.9.0", "incompatible"},
		{"26.0.0", "compatible"}, {"22", "incompatible"},
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

func TestCypressDisclosesThatConfiguredComponentTestingWasNotExercised(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	requireWriteFile(t, path, `{"complete":true,"componentConfigured":true,"runs":[]}`)
	var run validationRun
	readCypressResults(path, &run)
	require.Equal(t, []string{cypressComponentNotExercised}, run.NotExercised)
	result := validationResult{Framework: "cypress", Runs: []runSummary{run.summary(), run.summary()}}
	require.Equal(t, []string{cypressComponentNotExercised}, summarizeValidation(result).NotExercised)
	model := buildReport(t.TempDir(), intake.Facts{TestEventCount: 1}, false, reportRuntime{Framework: "Cypress", NotExercised: run.NotExercised})
	require.True(t, slices.ContainsFunc(model.Cards, func(card reportCard) bool { return card.Context == cypressComponentNotExercised }))
}

func TestCypressObserverPreservesConfigAndAfterRunHandlers(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, mode := range []string{"baseline", "control", "plugin-retries", "customer-retries"} {
		probe := mode != "baseline"
		root := t.TempDir()
		directory := t.TempDir()
		t.Chdir(root)
		config := "module.exports={reporter:'custom',retries:3,e2e:{setupNodeEvents:async(on,cfg)=>{on('after:run',()=>{require('node:fs').writeFileSync('user-after-run','called')});on('task',{existing:()=>1});if(process.env.DDTEST_OBSERVER_RETRY_TEST==='plugin-retries'){if(cfg.retries!==0)throw new Error('native probe retries must be disabled before plugin setup');cfg.retries=2};return {...cfg,custom:'kept',...process.env.DDTEST_OBSERVER_RETRY_TEST==='customer-retries'&&{retries:2}}}}}"
		requireWriteFile(t, filepath.Join(root, "cypress.config.cjs"), config)
		observer := filepath.Join(root, "observer.cjs")
		requireWriteFile(t, observer, cypressObserver)
		cli := filepath.Join(root, "node_modules/cypress/bin/cypress")
		require.NoError(t, os.MkdirAll(filepath.Dir(cli), 0700))
		requireWriteFile(t, cli, "const assert=require('node:assert/strict'); const fs=require('node:fs'); (async()=>{const args=process.argv.slice(3);assert.ok(args.includes('--browser'));assert.ok(args.includes('chrome'));assert.ok(!args.includes('--record'));assert.ok(!args.includes('--parallel'));const configPath=args[args.indexOf('--config-file')+1];assert.equal(fs.realpathSync(require('node:path').dirname(configPath)),fs.realpathSync(process.cwd()));assert.ok(args.indexOf('--config-file')<args.indexOf('--'));const config=require(configPath);assert.equal(config.reporter,'custom');const handlers={};const effective=await config.e2e.setupNodeEvents((event,fn)=>{handlers[event]=event==='task'?{...handlers[event],...fn}:fn},{retries:config.retries});assert.equal(effective.custom,'kept');assert.equal(effective.retries,process.env.DDTEST_OBSERVER_RETRY_TEST==='plugin-retries'?2:process.env.DDTEST_CYPRESS_PROBE?0:3);assert.equal(process.env.CYPRESS_RETRIES,process.env.DDTEST_CYPRESS_PROBE?undefined:'2');if(process.env.DDTEST_CYPRESS_PROBE)assert.equal(args[args.indexOf('--config')+1],'retries=0');assert.equal(handlers.task.existing(),1);await handlers['after:run']({status:'finished',runs:[]});assert.equal(fs.readFileSync('user-after-run','utf8'),'called')})().catch(e=>{console.error(e);process.exitCode=1})")
		command := exec.CommandContext(t.Context(), node, "--require", observer, cli, "run", "--browser", "chrome", "--record", "--parallel", "--group", "CI group", "-P"+root, "-Ccypress.config.cjs", "--")
		command.Dir = root
		probePath := ""
		if probe {
			probePath = filepath.Join(root, "probe.cy.js")
		}
		// Only the auto-retries probe lets the tracer keep a native retry budget.
		probeMode := map[string]string{"control": "pass", "plugin-retries": "fail-once", "customer-retries": "fail"}[mode]
		command.Env = append(os.Environ(), "NODE_OPTIONS=", "CYPRESS_RETRIES=2", "DDTEST_PROBE_MODE="+probeMode, "DDTEST_CYPRESS_DIRECTORY="+directory, "DDTEST_CYPRESS_PROBE="+probePath, "DDTEST_OBSERVER_RETRY_TEST="+mode)
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

func TestCypressReportsExecutableStartFailureWithoutOutput(t *testing.T) {
	drive := &Testdrive{repositoryRoot: t.TempDir(), language: "javascript", command: "COVERAGE=true", args: []string{"npm", "run", "test:ci"},
		executor: &fakeTestdriveExecutor{err: &exec.Error{Name: "COVERAGE=true", Err: exec.ErrNotFound}}, nodeVersion: func() string { return "v22.23.1" },
		startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
	session := planSession()
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	run, err := drive.runCypress(t.Context(), &bytes.Buffer{}, session, "", runSpec{Name: "baseline", Role: roleBaseline})
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
	drive, err := Prepare(PrepareOptions{TracerVersion: "6.21.0"})
	require.NoError(t, err)
	require.False(t, drive.allConfigurations, "default validates the selected invocation; --all is explicit")
	require.Equal(t, "true", drive.configurationEnvironment["COVERAGE"])
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
	run, err := drive.runCypress(t.Context(), &bytes.Buffer{}, session, "", runSpec{Name: "baseline", Role: roleBaseline})
	require.ErrorContains(t, err, "Cypress execution setup failed")
	require.Contains(t, run.ResultError, "Startup process exited")
	require.Len(t, run.Tests, 1, "complete native results must not hide setup failure")
}

func TestCypressTIAIsRequired(t *testing.T) {
	result := completeValidationFixture("cypress")
	result.Runs[1].Coverage = &coverageSummary{Status: "observed", CoveredTests: 1, TotalTests: 1}
	root := t.TempDir()
	var output bytes.Buffer
	require.NoError(t, finishValidation(&output, root, result))
	report := readValidationReport(t, root)
	require.True(t, report.LocalSuccess)
	require.Equal(t, "PASSED", report.Summary.Status)
	require.Len(t, report.Features, 6)
	for i, feature := range result.Features {
		require.Equal(t, "passed", feature.Status)
		for _, status := range []string{"failed", "inconclusive", "deferred", "not supported"} {
			result.Features[i].Status = status
			require.False(t, featureChecksPassed("cypress", result.Features), "%s %s must block completion", feature.Name, status)
		}
		result.Features[i].Status = feature.Status
	}

	result.Runs[1].Coverage = &coverageSummary{Status: "not observed", Reason: cypressMissingCoverageReason}
	require.True(t, missingRealCoverage(result))
	require.Contains(t, strings.Join(summarizeValidation(result).BlockingChecks, "\n"), "Cypress real-suite coverage was not observed: "+cypressMissingCoverageReason)
}

func TestCypressReportWarnsAboutMissingCoverage(t *testing.T) {
	model := buildReport(t.TempDir(), intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true}, false, reportRuntime{Framework: "Cypress"})
	require.True(t, slices.ContainsFunc(model.Cards, func(card reportCard) bool { return card.Kind == intake.MissingCoverageFinding }))
}

func TestCypressSkippingAcceptsTestLevelITREvidence(t *testing.T) {
	identity := intake.Test{Module: "cypress", Suite: "cypress/e2e/probe.cy.js", SourceFile: "cypress/e2e/probe.cy.js", Name: "ddtest " + probeName}
	skipped := map[string]string{"test.module": identity.Module, "test.suite": identity.Suite, "test.name": identity.Name, "test.status": "skip", "test.skipped_by_itr": "true"}
	zero := 0
	run := validationRun{framework: "cypress", ProbeExecutions: &zero, Tests: []jestTest{{Name: identity.Name, Status: "skipped"}},
		Facts: intake.Facts{TestEventCount: 1, Events: []intake.Event{{Type: "test", Tags: skipped}}}}
	feature := evaluateFeature("skipping", run, identity)
	require.Equal(t, "passed", feature.Status, feature.Reason)
	diagnostic := diagnoseSkipping(run, intake.Scenario{Feature: "skipping", Module: identity.Module, Suite: identity.Suite, Test: identity.Name, SourceFile: identity.SourceFile})
	require.True(t, diagnostic.SkippedByITR)
	require.True(t, diagnostic.IdentityMatched)

	executed := 1
	run.ProbeExecutions = &executed
	require.Equal(t, "failed", evaluateFeature("skipping", run, identity).Status, "a skipped event cannot hide an executed body")
	run.ProbeExecutions = &zero

	notITR := map[string]string{}
	for key, value := range skipped {
		notITR[key] = value
	}
	delete(notITR, "test.skipped_by_itr")
	run.Facts.Events = []intake.Event{{Type: "test", Tags: notITR}}
	require.Equal(t, "failed", evaluateFeature("skipping", run, identity).Status, "a plain skip is not ITR evidence")

	run.Facts = intake.Facts{TestEventCount: 2, Events: []intake.Event{{Type: "test", Tags: skipped}, {Type: "test", Tags: map[string]string{"test.module": identity.Module, "test.suite": identity.Suite, "test.name": identity.Name, "test.status": "fail"}}}}
	require.Equal(t, "failed", evaluateFeature("skipping", run, identity).Status, "a reported execution contradicts the skip")
}

func TestCypressSourceCoverageCountsOnlyApplicationFiles(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"src/app.js", "cypress/e2e/todo.cy.js", "cypress/support/e2e.js", "node_modules/lib/index.js", "e2e/todo.cy.js"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0755))
		requireWriteFile(t, filepath.Join(root, file), "")
	}
	native := []jestTest{{File: filepath.Join(root, "e2e/todo.cy.js"), Name: "adds"}}
	facts := func(files ...string) intake.Facts {
		return intake.Facts{TestCount: 2, CoverageLevel: "test", Tests: []intake.Test{{Name: "adds", CoveredFiles: files}, {Name: "removes"}}}
	}

	coverage := cypressSourceCoverage(root, cypressTestCode{}, facts("cypress/support/e2e.js", "src/app.js"), native)
	require.Equal(t, "observed", coverage.Status)
	require.Equal(t, 1, coverage.CoveredTests)
	require.Equal(t, 2, coverage.TotalTests)
	require.InDelta(t, 0.5, coverage.Ratio, 0.001)

	coverage = cypressSourceCoverage(root, cypressTestCode{}, facts(filepath.Join(root, "cypress/e2e/todo.cy.js"), "e2e/todo.cy.js", "node_modules/lib/index.js"), native)
	require.Equal(t, "not observed", coverage.Status)
	require.Contains(t, coverage.Reason, "only Cypress specs, support code or dependencies")

	coverage = cypressSourceCoverage(root, cypressTestCode{}, facts("src/missing.js"), native)
	require.Equal(t, "invalid", coverage.Status)
	require.Contains(t, coverage.Reason, "src/missing.js")

	coverage = cypressSourceCoverage(root, cypressTestCode{}, facts(), native)
	require.Equal(t, "not observed", coverage.Status)
	require.Equal(t, cypressMissingCoverageReason, coverage.Reason)

	coverage = cypressSourceCoverage(root, cypressTestCode{}, intake.Facts{TestCount: 1, CoverageLevel: "suite", Tests: []intake.Test{{Module: "cypress", Suite: "e2e/todo.cy.js", Name: "adds"}},
		SuiteCoverages: []intake.SuiteCoverage{{Module: "cypress", Suite: "e2e/todo.cy.js", Files: []string{"src/app.js"}}}}, native)
	require.Equal(t, "observed", coverage.Status, "suite coverage is associated with the suite's tests")
	// A Cypress package in a monorepo keeps its own cypress folder.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/web/cypress"), 0755))
	requireWriteFile(t, filepath.Join(root, "packages/web/cypress/support.js"), "")
	require.Equal(t, "not observed", cypressSourceCoverage(root, cypressTestCode{Project: "packages/web"}, facts("packages/web/cypress/support.js"), native).Status)
	// Cypress reports the effective project and support file: a --project
	// folder keeps its own cypress folder, and a support file can live anywhere.
	for _, file := range []string{"e2e/cypress/support/e2e.js", "test/support.js"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0755))
		requireWriteFile(t, filepath.Join(root, file), "")
	}
	require.Equal(t, "not observed", cypressSourceCoverage(root, cypressTestCode{Project: filepath.Join(root, "e2e")}, facts("e2e/cypress/support/e2e.js"), native).Status)
	coverage = cypressSourceCoverage(root, cypressTestCode{Project: root, SupportFile: filepath.Join(root, "test/support.js")}, facts("test/support.js"), native)
	require.Equal(t, "not observed", coverage.Status)
	require.Contains(t, coverage.Reason, "only Cypress specs, support code or dependencies")
	require.Equal(t, "observed", cypressSourceCoverage(root, cypressTestCode{Project: root, SupportFile: filepath.Join(root, "test/support.js")}, facts("test/support.js", "src/app.js"), native).Status)
}

func TestCypressObserverReportsEffectiveProjectAndSupportFile(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "cypress.config.cjs"), "module.exports={e2e:{}}")
	requireWriteFile(t, filepath.Join(root, "observer.cjs"), cypressObserver)
	script := `const fs=require('node:fs'),path=require('node:path'),[observer,root]=process.argv.slice(1)
 const config=require(require(observer)(root,null,root,false,false))
 ;(async()=>{
   for (const supportFile of [path.join(root,'test/support.js'),false]) {
     const handlers={}
     await config.e2e.setupNodeEvents((event,handler)=>{handlers[event]=handler},{projectRoot:path.join(root,'e2e'),supportFile})
     await handlers['after:run']({runs:[]})
     const {projectRoot,supportFile:reported}=JSON.parse(fs.readFileSync(path.join(root,'results.json'),'utf8'))
     console.log(JSON.stringify({projectRoot,supportFile:reported}))
   }
 })()`
	output, err := exec.CommandContext(t.Context(), "node", "-e", script, filepath.Join(root, "observer.cjs"), root).CombinedOutput()
	require.NoError(t, err, string(output))
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	require.Len(t, lines, 2)
	project, _ := json.Marshal(filepath.Join(root, "e2e"))
	require.Contains(t, lines, `{"projectRoot":`+string(project)+`,"supportFile":`+strconv.Quote(filepath.Join(root, "test/support.js"))+`}`)
	require.Contains(t, lines, `{"projectRoot":`+string(project)+`}`, "supportFile: false has no support code to exclude")
}

func TestCypressCodeCoverage3SkipCrashIsNamed(t *testing.T) {
	crash := `1) "after each" hook for "validation probe": TypeError: Cannot read properties of undefined (reading 'forEach')
      at Context.eval (webpack:///./node_modules/@cypress/code-coverage/support.js:145:0)`
	features := func() []featureResult {
		return []featureResult{{Name: "skipping", Status: "failed", Reason: "generic"}, {Name: "disabled", Status: "failed", Reason: "generic"}, {Name: "quarantine", Status: "failed", Reason: "generic"}}
	}
	outputs := map[string]string{"skipping": crash, "disabled": crash, "quarantine": crash}

	named := features()
	explainCypressCodeCoverageSkips(named, "3.14.7", outputs)
	require.Contains(t, named[0].Reason, "@cypress/code-coverage 3.14.7 fails its afterEach hook")
	require.Contains(t, named[1].Reason, "Upgrade to @cypress/code-coverage 4 or later")
	require.Equal(t, "failed", named[0].Status)
	require.Equal(t, "generic", named[2].Reason, "only runtime skips trigger the crash")

	for _, version := range []string{"4.0.3", ""} {
		unchanged := features()
		explainCypressCodeCoverageSkips(unchanged, version, outputs)
		require.Equal(t, "generic", unchanged[0].Reason, version)
	}
	unchanged := features()
	explainCypressCodeCoverageSkips(unchanged, "3.14.7", map[string]string{"skipping": "AssertionError: expected skip"})
	require.Equal(t, "generic", unchanged[0].Reason, "other failures keep their own reason")
}

func TestCypressPublishedReportRedactsKeysWithoutDroppingConfigurationEvidence(t *testing.T) {
	result := validationResult{Framework: "cypress", Runs: []runSummary{{Command: "cypress run --key fake-secret", NativeCommand: "cypress run -kfake-secret"}}, Configurations: []configurationResult{{TestCommand: onboard.TestCommand{Command: "cypress run --key fake-secret", Cypress: &onboard.CypressCommand{CLI: []string{"cypress", "run", "--key", "fake-secret"}}}, Status: "passed", PairedExecution: true}}}
	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(data), "fake-secret")
	var decoded validationResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Len(t, decoded.Configurations, 1)
	require.True(t, decoded.Configurations[0].PairedExecution)
	require.Equal(t, "passed", decoded.Configurations[0].Status)
	require.Equal(t, "[REDACTED]", decoded.Configurations[0].Cypress.CLI[3])
}

func TestCypressPublishedReportRedactsRecordingVariablesAndKeepsProse(t *testing.T) {
	prose := "Use the -kitten option; --key fake-long-key is documented."
	result := validationResult{Framework: "cypress", Error: prose,
		CommandEnvironment: map[string]string{"CYPRESS_RECORD_KEY": "fake-env-key"},
		Runs:               []runSummary{{Command: "CYPRESS_CLOUD_TOKEN=fake-token npx cypress run"}},
		Configurations: []configurationResult{{TestCommand: onboard.TestCommand{Command: "CYPRESS_RECORD_KEY='fake-env-key' npx cypress run -k fake-short-key",
			Environment: map[string]string{"CYPRESS_RECORD_KEY": "fake-env-key", "CYPRESS_CLOUD_TOKEN": "fake-token", "TERM": "xterm"},
			Locations:   []string{"ci.yml / test / step 1 (CYPRESS_RECORD_KEY=fake-env-key npx cypress run -kfake-short-key)"}}}}}
	data, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"fake-env-key", "fake-token", "fake-short-key", "fake-long-key"} {
		require.NotContains(t, string(data), secret)
	}
	var decoded validationResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "Use the -kitten option; --key [REDACTED] is documented.", decoded.Error)
	require.Equal(t, "xterm", decoded.Configurations[0].Environment["TERM"])
	require.Equal(t, "[REDACTED]", decoded.Configurations[0].Environment["CYPRESS_RECORD_KEY"])
}

func TestCypressPreviewRedactsRecordingKeys(t *testing.T) {
	for _, all := range []bool{false, true} {
		root := t.TempDir()
		requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"cypress":"16.1.1"},"scripts":{"test:ci":"cypress run --record --key fake-key-123"}}`)
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
		requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - run: npx cypress run --record --key fake-key-123\n        env: {CYPRESS_RECORD_KEY: fake-env-key}\n")
		t.Chdir(root)
		drive, err := Prepare(PrepareOptions{TracerVersion: "6.21.0", All: all})
		require.NoError(t, err)
		var output bytes.Buffer
		drive.Preview(&output)
		require.NotContains(t, output.String(), "fake-key-123")
		require.NotContains(t, output.String(), "fake-env-key")
		require.Contains(t, output.String(), "[REDACTED]")
	}
}

func TestCypressPreviewWithoutACommandDoesNotGuessOne(t *testing.T) {
	for _, all := range []bool{false, true} {
		root := t.TempDir()
		requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"cypress":"16.1.1"}}`)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", ".bin"), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", ".bin", "cypress"), []byte("#!/bin/sh\n"), 0700))
		t.Chdir(root)
		drive, err := Prepare(PrepareOptions{TracerVersion: "6.21.0", All: all})
		require.NoError(t, err)
		var output bytes.Buffer
		drive.Preview(&output)
		require.NotContains(t, output.String(), "- command:")
		require.NotContains(t, output.String(), "cypress run")
	}
}

func TestCypressProbeOptionsOverrideOnlyNativeRetries(t *testing.T) {
	root := t.TempDir()
	observer := filepath.Join(root, "observer.cjs")
	requireWriteFile(t, observer, cypressObserver)
	script := `const assert=require('node:assert/strict'),{probeConfig}=require(process.argv[1]);
 const original={retries:2,viewportWidth:800,e2e:{retries:{runMode:2},baseUrl:'http://localhost'}};
 const overridden=probeConfig(original);assert.equal(original.retries,2);assert.equal(original.e2e.retries.runMode,2);
 assert.deepEqual(overridden,{retries:0,viewportWidth:800,e2e:{retries:0,baseUrl:'http://localhost'}});
 assert.equal(probeConfig('viewportWidth=800,retries=2'),'viewportWidth=800,retries=2,retries=0');
 assert.deepEqual(JSON.parse(probeConfig(JSON.stringify(original))),overridden);`
	output, err := exec.CommandContext(t.Context(), "node", "-e", script, observer).CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestCypressTypeScriptWrapperUsesSiblingSpecifier(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "cypress.config.ts"), "export default {e2e:{}}")
	requireWriteFile(t, filepath.Join(root, "observer.cjs"), cypressObserver)
	output, err := exec.CommandContext(t.Context(), "node", "-e", `const fs=require('node:fs');const wrapper=require(process.argv[1])(process.argv[2],null,process.argv[2],false,false);console.log(fs.readFileSync(wrapper,'utf8'))`, filepath.Join(root, "observer.cjs"), root).CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), `import original from "./cypress.config.ts"`)
	require.NotContains(t, string(output), "file://")
}

func TestCypressNativeRetryTelemetryDoesNotDependOnArrivalOrder(t *testing.T) {
	run := testRun("passed", 0)
	run.framework = "cypress"
	run.Tests[0].CypressAttempts = []string{"failed", "passed"}
	run.Facts.Tests[0].Attempts = []intake.TestRun{{Status: "pass", Retry: true, RetryReason: "external"}, {Status: "fail"}}
	require.Empty(t, verifyJestTelemetry(run).Status)
	require.True(t, run.Facts.Tests[0].Attempts[0].Retry, "comparison must not rewrite original evidence")
	run.Facts.Tests[0].Attempts[0].RetryReason = "early_flake_detection"
	require.Equal(t, "unexpected_retry", verifyJestTelemetry(run).Code)
	run.Facts.Tests[0].Attempts = run.Facts.Tests[0].Attempts[1:]
	require.Equal(t, "inconclusive", verifyJestTelemetry(run).Status, "every native attempt must have matching telemetry")
}

func TestCypressPairedComparisonIncludesNativeRetryHistory(t *testing.T) {
	retried := testRun("passed", 0)
	retried.framework = "cypress"
	retried.Tests[0].CypressAttempts = []string{"failed", "passed"}
	retried.Facts.Tests[0].Attempts = []intake.TestRun{{Status: "fail"}, {Status: "pass", Retry: true, RetryReason: "external"}}
	immediate := testRun("passed", 0)
	immediate.framework = "cypress"
	immediate.Tests[0].CypressAttempts = []string{"passed"}
	require.Equal(t, "compatible", compareJest(withoutTelemetry(retried), retried).Status)
	require.Equal(t, "suspected regression", compareJest(withoutTelemetry(retried), immediate).Status)
	// The same change in both pairs recurs; a history that changes between
	// repeats is reported as varying instead.
	require.Equal(t, "suspected regression", compareRepeatedJavaScript(withoutTelemetry(retried), immediate, withoutTelemetry(retried), immediate).Status)
	require.Equal(t, "varying_outcomes", compareRepeatedJavaScript(withoutTelemetry(retried), immediate, withoutTelemetry(immediate), immediate).Code)
}

func TestCypressBackgroundPortCheckUsesDeclaredBaseURL(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()
	plan := &onboard.CypressCommand{Start: [][]string{{"node", "server.cjs"}}}
	require.ErrorContains(t, checkCypressStartupPorts(plan, map[string]string{"CYPRESS_BASE_URL": "http://" + listener.Addr().String()}), "already occupied")
	connection, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	require.NoError(t, connection.Close())
}
