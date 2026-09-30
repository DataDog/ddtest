// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestHTMLReportReplacesFindingsWithoutRetainingRawArtifacts(t *testing.T) {
	run := preparedTestdrive(t)
	result := validationResult{Tracer: "dd-trace@6.17.0", TracerSource: "project installation (reused)"}
	var output bytes.Buffer
	for _, name := range []string{"earlier-suite-test", "latest-suite-test"} {
		facts := intake.Facts{TestEventCount: 1, Tests: []intake.Test{{Name: name, Status: "pass"}}}
		require.NoError(t, run.writeHTMLReport(&output, &result, validationRun{Facts: facts}))
	}
	data, err := os.ReadFile(htmlReportPath(run.repositoryRoot))
	require.NoError(t, err)
	html := string(data)
	require.Contains(t, html, "latest-suite-test")
	require.NotContains(t, html, "earlier-suite-test")
	require.Contains(t, html, `href="testdrive.json"`)
	require.NotContains(t, html, `href="intake/"`)
	require.NotContains(t, html, `href="test-output.txt"`)
	require.Contains(t, html, "project installation (reused)")
	require.Contains(t, html, "Datadog library")
	require.Contains(t, output.String(), "Open report:")
	require.Contains(t, output.String(), "\x1b\\"+htmlReportPath("")+"\x1b]8;;")
	require.Contains(t, output.String(), "Compatibility, feature checks and the onboarding verdict")
	files, err := os.ReadDir(filepath.Dir(validationPath(run.repositoryRoot)))
	require.NoError(t, err)
	require.Len(t, files, 1, "repeated reports must not create history or raw assets")
}

func TestCheckOnlyPreservesPreviousHTML(t *testing.T) {
	run := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	run.checkOnly = true
	require.NoError(t, os.MkdirAll(filepath.Dir(htmlReportPath(run.repositoryRoot)), 0755))
	requireWriteFile(t, htmlReportPath(run.repositoryRoot), "previous full run")
	var output bytes.Buffer
	require.NoError(t, run.Run(t.Context(), &output))
	html, err := os.ReadFile(htmlReportPath(run.repositoryRoot))
	require.NoError(t, err)
	require.Equal(t, "previous full run", string(html))
	require.NotContains(t, output.String(), "Open report:")
	report := readValidationReport(t, run.repositoryRoot)
	require.False(t, report.Success)
	require.False(t, report.HTMLReportCurrent)
	require.Contains(t, validationHandoff(report), "Previous HTML report (not regenerated)")
	require.Equal(t, "passed", report.Cleanup.Status)
}

func TestHTMLWriteFailureStillKeepsJSONAndCleansSession(t *testing.T) {
	run := preparedTestdrive(t)
	run.framework = &framework.Mocha{}
	installer := &fakeTracer{preloadPath: "/trace/ci/init.js"}
	run.platform = installer
	run.executor = &fakeTestdriveExecutor{}
	run.startIntake = func(string, intake.Scenario) (localIntake, error) {
		return &fakeIntake{findings: intake.Facts{TestEventCount: 1}}, nil
	}
	// A customer directory at the report path must not be removed or overwritten.
	require.NoError(t, os.MkdirAll(htmlReportPath(run.repositoryRoot), 0755))
	customer := filepath.Join(htmlReportPath(run.repositoryRoot), "customer-file")
	requireWriteFile(t, customer, "keep")
	require.ErrorContains(t, run.Run(t.Context(), &bytes.Buffer{}), "save testdrive HTML report")
	require.NoDirExists(t, installer.sessionDirectory)
	require.FileExists(t, customer)
	data, err := os.ReadFile(validationPath(run.repositoryRoot))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.Contains(t, report.Error, "save testdrive HTML report")
	require.Len(t, report.Runs, 1)
	require.False(t, report.Success)
	files, err := os.ReadDir(filepath.Dir(validationPath(run.repositoryRoot)))
	require.NoError(t, err)
	require.Len(t, files, 2, "no incomplete report scratch file may remain")
}
