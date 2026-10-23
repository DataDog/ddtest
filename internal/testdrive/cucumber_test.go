// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func cucumberMessages() string {
	return `{"pickle":{"id":"p","uri":"test/example.feature","name":"same"}}
{"testCase":{"id":"c","pickleId":"p"}}
{"testCaseStarted":{"id":"a","testCaseId":"c","attempt":0}}
{"testStepFinished":{"testCaseStartedId":"a","testStepResult":{"status":"FAILED","message":"first attempt failed"}}}
{"testCaseFinished":{"testCaseStartedId":"a","willBeRetried":true}}
{"testCaseStarted":{"id":"b","testCaseId":"c","attempt":1}}
{"testStepFinished":{"testCaseStartedId":"b","testStepResult":{"status":"PASSED"}}}
{"testCaseFinished":{"testCaseStartedId":"b","willBeRetried":false}}
{"testRunFinished":{"success":true}}
`
}

func TestCucumberSourceCoverageRequiresApplicationFiles(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"cucumber.js", "support/steps.js", "test/suite.feature", "src/features/payments/service.js"} {
		path := filepath.Join(root, file)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte("source"), 0600))
	}
	facts := intake.Facts{TestCount: 2, CoverageLevel: "suite", Tests: []intake.Test{{Module: "api", Suite: "test/suite.feature"}, {Module: "api", Suite: "test/suite.feature"}}, SuiteCoverages: []intake.SuiteCoverage{{Module: "api", Suite: "test/suite.feature", Files: []string{"cucumber.js", "support/steps.js", "test/suite.feature"}}}}
	native := []jestTest{{File: filepath.Join(root, "test/suite.feature")}}
	support := &cucumberSupport{RequirePaths: []string{filepath.Join(root, "support/steps.js")}, ConfigFiles: []string{filepath.Join(root, "cucumber.js")}}
	require.Equal(t, "not observed", cucumberSourceCoverage(root, facts, native, support).Status)
	facts.SuiteCoverages[0].Files = append(facts.SuiteCoverages[0].Files, "src/features/payments/service.js")
	coverage := cucumberSourceCoverage(root, facts, native, support)
	require.Equal(t, "observed", coverage.Status)
	require.Equal(t, "suite", coverage.Level)
	require.Equal(t, 2, coverage.CoveredTests)
	facts.SuiteCoverages[0].Files = append(facts.SuiteCoverages[0].Files, "/outside/project.js")
	invalid := cucumberSourceCoverage(root, facts, native, support)
	require.Equal(t, "invalid", invalid.Status)
	require.Contains(t, invalid.Reason, "/outside/project.js")
}

func TestCucumberSourceCoverageClassifiesSupportCode(t *testing.T) {
	for _, tc := range []struct {
		name, covered    string
		support          []string
		packageRoot      string
		observed         bool
		reasonNamesFiles string
	}{
		// The default glob loads features/**/*.js, so this file is support code.
		{name: "default support glob", covered: "features/support/lib/app.js", support: []string{"features/support/lib/app.js", "features/step_definitions/steps.js"}, reasonNamesFiles: "features/support/lib/app.js"},
		{name: "helper next to a required step file", covered: "support/assertions.cjs", support: []string{"support/steps.cjs"}, reasonNamesFiles: "support/assertions.cjs"},
		{name: "application next to a root-level step file", covered: "app.js", support: []string{"steps.js"}, observed: true},
		{name: "application next to a package-level step file", covered: "frontend/app.js", support: []string{"frontend/steps.js"}, packageRoot: "frontend", observed: true},
		{name: "application outside the support folder", covered: "lib/app.js", support: []string{"features/support/steps.js"}, observed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			files := append([]string{tc.covered, "features/a.feature"}, tc.support...)
			if tc.packageRoot != "" {
				files = append(files, tc.packageRoot+"/package.json")
			}
			for _, file := range files {
				path := filepath.Join(root, file)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
				require.NoError(t, os.WriteFile(path, []byte("source"), 0600))
			}
			support := &cucumberSupport{}
			for _, file := range tc.support {
				support.RequirePaths = append(support.RequirePaths, filepath.Join(root, file))
			}
			facts := intake.Facts{TestCount: 1, CoverageLevel: "suite", Tests: []intake.Test{{Module: "m", Suite: "features/a.feature"}}, SuiteCoverages: []intake.SuiteCoverage{{Module: "m", Suite: "features/a.feature", Files: append([]string{tc.covered, "features/a.feature"}, tc.support...)}}}
			coverage := cucumberSourceCoverage(root, facts, []jestTest{{File: filepath.Join(root, "features/a.feature")}}, support)
			if tc.observed {
				require.Equal(t, "observed", coverage.Status, coverage.Reason)
				return
			}
			require.Equal(t, "not observed", coverage.Status)
			require.Contains(t, coverage.Reason, "Coverage was observed only in Cucumber support code")
			require.Contains(t, coverage.Reason, tc.reasonNamesFiles)
			require.NotContains(t, coverage.Reason, "Install and configure NYC")
		})
	}
}

func TestCucumberNativeMessagesRequireCompletionAndRetainFinalResults(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "native.ndjson")
	for _, tc := range []struct {
		name, data string
		wantError  bool
		status     string
	}{
		{"retry recovers", cucumberMessages(), false, "passed"},
		{"missing final envelope", `{"pickle":{"id":"p"}}`, true, ""},
		{"malformed", `{invalid`, true, ""},
		{"unfinished scenario", `{"testCaseStarted":{"id":"a","testCaseId":"c"}}
{"testRunFinished":{"success":true}}`, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(file, []byte(tc.data), 0600))
			run := validationRun{root: root}
			readCucumberResults(file, root, &run)
			if tc.wantError {
				require.NotEmpty(t, run.ResultError)
				return
			}
			require.Empty(t, run.ResultError)
			require.Len(t, run.Tests, 1)
			require.Equal(t, tc.status, run.Tests[0].Status)
			require.Equal(t, filepath.Join(root, "test/example.feature"), run.Tests[0].File)
			require.Equal(t, 1, run.summary().TestCounts.Passed)
		})
	}
}

func TestCucumberFormatterOptionsReachRunnerThroughWrappers(t *testing.T) {
	for _, tc := range []struct {
		command    string
		args, want []string
	}{
		{"npm", []string{"run", "bdd"}, []string{"run", "bdd", "--", "--format", "message:result"}},
		{"npm", []string{"run", "bdd", "--", "--profile", "noWip"}, []string{"run", "bdd", "--", "--profile", "noWip", "--format", "message:result"}},
		{"nyc", []string{"--", "cucumber-js", "features"}, []string{"--", "cucumber-js", "features", "--format", "message:result"}},
		{"nyc", []string{"--", "npm", "run", "bdd"}, []string{"--", "npm", "run", "bdd", "--", "--format", "message:result"}},
	} {
		require.Equal(t, tc.want, appendCucumberOptions(tc.command, tc.args, "--format", "message:result"))
	}
}

func TestCucumberSupportUsesTracerRuntimeWithoutMajorAllowlist(t *testing.T) {
	for _, tc := range []struct{ framework, node, tracerNode, want string }{
		{"6.0.0", "24.14.1", ">=18", "incompatible"},
		{"7.0.0", "24.14.1", ">=18", "incompatible"},
		{"8.11.1", "24.14.1", ">=18", "compatible"},
		{"12.9.0", "20.19.0", ">=22", "incompatible"},
		{"13.2.1", "24.14.1", ">=22", "compatible"},
		{"99.0.0", "24.14.1", ">=22", "compatible"},
	} {
		require.Equal(t, tc.want, checkCucumberSupport(jestPreflight{Version: tc.framework, Node: tc.node}, platform.JSSelection{Version: "6.21.0", Node: tc.tracerNode}).Status)
	}
}

func TestUnsupportedCucumberSkipsTheTracerRecommendation(t *testing.T) {
	for _, tc := range []struct {
		version, node string
		recommend     bool
	}{
		// An incompatible Node is a tracer problem, so a recommendation helps.
		{"8.11.1", "18.0.0", true},
		{"7.3.2", "18.0.0", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			run := preflightFixture(t, "", "")
			run.executor = &preflightExecutor{config: `DDTEST_CUCUMBER_METADATA={"version":"` + tc.version + `","node":"` + tc.node + `"}`}
			var output bytes.Buffer
			require.Error(t, run.checkCucumberPreflight(t.Context(), &output, &validationResult{}))
			if tc.recommend {
				require.Contains(t, output.String(), "Tracer recommendation")
				return
			}
			require.Contains(t, output.String(), "requires @cucumber/cucumber >=8")
			require.NotContains(t, output.String(), "Tracer recommendation")
			require.NotContains(t, output.String(), "Recommended candidate")
		})
	}
}
