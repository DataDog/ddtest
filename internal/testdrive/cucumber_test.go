// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
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
	for _, file := range []string{"cucumber.js", "test/steps.js", "test/suite.feature", "lib/index.js"} {
		path := filepath.Join(root, file)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte("source"), 0600))
	}
	facts := intake.Facts{TestCount: 2, CoverageLevel: "suite", Tests: []intake.Test{{Module: "api", Suite: "test/suite.feature"}, {Module: "api", Suite: "test/suite.feature"}}, SuiteCoverages: []intake.SuiteCoverage{{Module: "api", Suite: "test/suite.feature", Files: []string{"cucumber.js", "test/steps.js", "test/suite.feature"}}}}
	native := []jestTest{{File: filepath.Join(root, "test/suite.feature")}}
	require.Equal(t, "not observed", cucumberSourceCoverage(root, facts, native).Status)
	facts.SuiteCoverages[0].Files = append(facts.SuiteCoverages[0].Files, "lib/index.js")
	coverage := cucumberSourceCoverage(root, facts, native)
	require.Equal(t, "observed", coverage.Status)
	require.Equal(t, "suite", coverage.Level)
	require.Equal(t, 2, coverage.CoveredTests)
	facts.SuiteCoverages[0].Files = append(facts.SuiteCoverages[0].Files, "/outside/project.js")
	require.Equal(t, "invalid", cucumberSourceCoverage(root, facts, native).Status)
}

func TestCucumberUnknownNativeOutcomesAreNotPresentedAsZeroPasses(t *testing.T) {
	run := validationRun{framework: "cucumber", Name: "reporting-only", Command: "cucumber-js", ResultError: "native messages are incomplete"}
	result := validationResult{Runs: []runSummary{run.summary()}}
	require.Equal(t, "unavailable", result.Runs[0].NativeResults)
	require.Contains(t, validationFacts(result)[2], "native outcome counts unavailable")
	require.NotContains(t, validationFacts(result)[2], "0 passed")
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
			readCucumberResults(file, &run)
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
		{"7.0.0", "24.14.1", ">=18", "compatible"},
		{"12.9.0", "20.19.0", ">=22", "incompatible"},
		{"13.2.1", "24.14.1", ">=22", "compatible"},
		{"99.0.0", "24.14.1", ">=22", "compatible"},
	} {
		require.Equal(t, tc.want, checkCucumberSupport(jestPreflight{Version: tc.framework, Node: tc.node}, platform.JSSelection{Version: "6.21.0", Node: tc.tracerNode}).Status)
	}
}
