// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRunCleansScratchOnSetupFailureAndCancellation(t *testing.T) {
	for _, stage := range []string{"installation", "intake", "cancellation", "report write"} {
		t.Run(stage, func(t *testing.T) {
			run := preparedTestdrive(t)
			installer := &fakeTracer{preloadPath: "/trace/ci/init.js"}
			run.platform = installer
			run.executor = &fakeTestdriveExecutor{}
			run.startIntake = func(string, intake.Scenario) (localIntake, error) { return nil, errors.New("intake unavailable") }
			ctx := t.Context()
			if stage == "installation" {
				installer.err = errors.New("registry unavailable")
			}
			if stage == "cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if stage == "report write" {
				requireWriteFile(t, filepath.Join(run.repositoryRoot, ".testoptimization"), "customer data")
			}
			var output bytes.Buffer
			require.Error(t, run.Run(ctx, &output))
			require.NotEmpty(t, installer.sessionDirectory)
			require.NoDirExists(t, installer.sessionDirectory)
			if stage != "report write" {
				data, err := os.ReadFile(validationPath(run.repositoryRoot))
				require.NoError(t, err)
				var result validationResult
				require.NoError(t, json.Unmarshal(data, &result))
				require.NotEmpty(t, result.Error)
				require.False(t, result.Success)
				require.Equal(t, "inconclusive", result.Compatibility.Status)
				files, err := os.ReadDir(filepath.Dir(validationPath(run.repositoryRoot)))
				require.NoError(t, err)
				require.Len(t, files, 1)
			} else {
				data, err := os.ReadFile(filepath.Join(run.repositoryRoot, ".testoptimization"))
				require.NoError(t, err)
				require.Equal(t, "customer data", string(data))
			}
		})
	}
}

func TestUnvalidatedFrameworkCleansScratchAndKeepsBothReports(t *testing.T) {
	run := preparedTestdrive(t)
	run.framework = &framework.Mocha{}
	installer := &fakeTracer{preloadPath: "/trace/ci/init.js"}
	run.platform = installer
	run.executor = &fakeTestdriveExecutor{output: []byte("useful diagnostics")}
	run.startIntake = func(string, intake.Scenario) (localIntake, error) {
		return &fakeIntake{url: "http://127.0.0.1:1234", findings: intake.Facts{TestEventCount: 1}}, nil
	}
	require.Error(t, run.Run(t.Context(), &bytes.Buffer{}))
	require.NoDirExists(t, installer.sessionDirectory)
	data, err := os.ReadFile(validationPath(run.repositoryRoot))
	require.NoError(t, err)
	require.NotContains(t, string(data), "useful diagnostics")
	var result validationResult
	require.NoError(t, json.Unmarshal(data, &result))
	require.False(t, result.Success)
	require.Len(t, result.Runs, 1)
	require.NotEmpty(t, result.Runs[0].Command)
	require.Equal(t, 0, *result.Runs[0].ExitCode)
	require.Equal(t, 1, result.Runs[0].TestEventCount)
	require.FileExists(t, htmlReportPath(run.repositoryRoot))
	files, err := os.ReadDir(filepath.Dir(validationPath(run.repositoryRoot)))
	require.NoError(t, err)
	require.Len(t, files, 2)
}

func TestCommandFailureOutputSurvivesCleanup(t *testing.T) {
	for _, tc := range []struct {
		name, output, want string
		events             int
	}{
		{"assertion", "Expected: true\nReceived: false\n", "Expected: true\nReceived: false", 1},
		{"setup", "Cannot find module './missing' from 'setup.js'\n", "Cannot find module './missing'", 1363},
		{"no events", "SyntaxError: unexpected token\n", "SyntaxError: unexpected token", 0},
		{"empty", "", "The command produced no output.", 1},
		{"80 lines", strings.Repeat("log line\n", 79) + "final failure", "final failure", 1},
		{"long", "initial failure\n" + strings.Repeat("log line\n", 79) + "final failure\n", "... 1 line omitted ...", 1},
		{"large", "initial failure\n" + strings.Repeat("log line\n", 2000) + "final failure\n", "final failure", 1},
		{"HTML", "<script>alert(1)</script>\n", "<script>alert(1)</script>", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := preparedTestdrive(t)
			run.framework = &framework.Mocha{}
			installer := &fakeTracer{preloadPath: "/trace/ci/init.js"}
			run.platform = installer
			run.executor = &fakeTestdriveExecutor{output: []byte(tc.output), err: errors.New("exit status 1")}
			run.startIntake = func(string, intake.Scenario) (localIntake, error) {
				return &fakeIntake{findings: intake.Facts{TestEventCount: tc.events}}, nil
			}
			var output bytes.Buffer
			require.Error(t, run.Run(t.Context(), &output), "Mocha remains unvalidated")
			require.Contains(t, output.String(), "Mocha command output:")
			require.Contains(t, output.String(), tc.want)
			require.Contains(t, output.String(), "Bounded diagnostic: "+validationPath(""))
			require.NotContains(t, output.String(), "Full test output:")
			require.NotContains(t, output.String(), "see the full test output")
			if tc.name == "long" {
				require.Contains(t, output.String(), "initial failure")
				require.Contains(t, output.String(), "final failure")
				require.Equal(t, 78, strings.Count(output.String(), "log line"))
			}
			require.NoDirExists(t, installer.sessionDirectory)
			report := readValidationReport(t, run.repositoryRoot)
			require.Len(t, report.Runs, 1)
			require.Equal(t, -1, *report.Runs[0].ExitCode, "generic executor errors have no process exit code")
			require.Equal(t, tc.events, report.Runs[0].TestEventCount)
			require.LessOrEqual(t, len([]rune(report.Runs[0].Diagnostic)), 1024)
			page, err := os.ReadFile(htmlReportPath(run.repositoryRoot))
			require.NoError(t, err)
			require.Contains(t, html.UnescapeString(string(page)), tc.output)
			require.Contains(t, html.UnescapeString(string(page)), report.Runs[0].Command)
			require.Contains(t, string(page), "Command failed: exit status 1")
			require.NotContains(t, string(page), "<script>alert(1)</script>")
			if tc.output == "" {
				require.Contains(t, string(page), "The command produced no output.")
			}
			if tc.output != "" {
				require.NotEmpty(t, report.Runs[0].Diagnostic)
			}
			files, err := os.ReadDir(filepath.Dir(validationPath(run.repositoryRoot)))
			require.NoError(t, err)
			require.Len(t, files, 2, "retain HTML and JSON, without raw output files")
		})
	}
}

func TestReportReplacementKeepsOneFileAndPreservesOtherProjectData(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Dir(validationPath(root))
	require.NoError(t, os.MkdirAll(directory, 0755))
	requireWriteFile(t, filepath.Join(directory, "customer-plan.json"), "original plan")
	for _, name := range []string{"first", "second"} {
		result := validationResult{Session: name, Compatibility: verdict{Status: "compatible"}}
		require.NoError(t, finishValidation(&bytes.Buffer{}, root, result))
	}
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var result validationResult
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, "second", result.Session)
	files, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, files, 2)
	data, err = os.ReadFile(filepath.Join(directory, "customer-plan.json"))
	require.NoError(t, err)
	require.Equal(t, "original plan", string(data))
}

func TestAtomicReportFailureRemovesOnlyItsScratchFile(t *testing.T) {
	root := t.TempDir()
	target := validationPath(root)
	require.NoError(t, os.MkdirAll(target, 0755))
	requireWriteFile(t, filepath.Join(target, "customer-file"), "keep")
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{}))
	files, err := os.ReadDir(filepath.Dir(target))
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.FileExists(t, filepath.Join(target, "customer-file"))
}

func TestConcurrentReportWritesLeaveOneCompleteReport(t *testing.T) {
	root := t.TempDir()
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		group.Go(func() {
			failures <- finishValidation(&bytes.Buffer{}, root, validationResult{Compatibility: verdict{Status: "compatible"}})
		})
	}
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	files, err := os.ReadDir(filepath.Dir(validationPath(root)))
	require.NoError(t, err)
	require.Len(t, files, 1)
}

func TestProbeRemovedWhenScenarioSetupFails(t *testing.T) {
	run := preparedTestdrive(t)
	source := filepath.Join(run.repositoryRoot, "existing.test.js")
	requireWriteFile(t, source, "original test")
	run.executor = discoveryExecutor(func(args []string) ([]byte, error) {
		path := source
		for i, arg := range args {
			if arg == "--runTestsByPath" {
				path = args[i+1]
			}
		}
		return json.Marshal([]string{path})
	})
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	run.startIntake = func(string, intake.Scenario) (localIntake, error) { return nil, errors.New("listener failed") }
	result := validationResult{Preflight: &jestPreflight{Projects: []jestProject{{Root: run.repositoryRoot}}}}
	require.ErrorContains(t, run.runJestFeatures(t.Context(), &bytes.Buffer{}, session, "/trace/ci/init.js", &result), "listener failed")
	probes, err := filepath.Glob(filepath.Join(run.repositoryRoot, "ddtest*"))
	require.NoError(t, err)
	require.Empty(t, probes)
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, "original test", string(data))
}
