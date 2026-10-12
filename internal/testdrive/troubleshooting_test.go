// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRunFailureFindingsAcrossReportConsoleAndJSON(t *testing.T) {
	failed := intake.Test{Name: "assertion", Status: "fail"}
	for _, tc := range []struct {
		name, command, log, kind, advice string
		facts                            intake.Facts
		err                              error
	}{
		{name: "UFO bare node regression", command: "node", kind: "no-test-events", advice: "No test runner was launched"},
		{name: "successful runner without instrumentation", command: "vitest", log: "Tests passed", kind: "no-test-events", advice: "Verify test execution and instrumentation"},
		{name: "startup failure", command: "vitest", log: "Error: cannot load config <config.ts>", err: errors.New("exit status 1"), kind: "command-failed", advice: "Check the command error"},
		{name: "failure after passing events", command: "vitest", log: "Error: teardown failed", err: errors.New("exit status 1"), facts: intake.Facts{TestEventCount: 1}, kind: "command-failed", advice: "Check the command error"},
		{name: "reported assertion needs no extra finding", command: "vitest", err: errors.New("exit status 1"), facts: intake.Facts{TestEventCount: 1, FailedTests: []intake.Test{failed}}, kind: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := preparedTestdrive(t)
			drive.framework = framework.NewVitest(platform.NewJavaScript())
			drive.command, drive.args = tc.command, nil
			installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
			drive.platform = installer
			drive.executor = &fakeTestdriveExecutor{output: []byte(tc.log), err: tc.err}
			drive.startIntake = func(string) (localIntake, error) {
				return &fakeIntake{url: "http://127.0.0.1:1234", findings: tc.facts}, nil
			}
			var console bytes.Buffer
			require.Error(t, drive.Run(t.Context(), &console))
			data, err := os.ReadFile(filepath.Join(installer.sessionDirectory, diagnosticsFilename))
			require.NoError(t, err)
			var diagnostics runDiagnostics
			require.NoError(t, json.Unmarshal(data, &diagnostics))
			require.True(t, diagnostics.RunFailed)
			require.Equal(t, tc.err != nil, diagnostics.CommandFailed)
			require.Len(t, diagnostics.Findings, 1)
			require.Equal(t, tc.kind, diagnostics.Findings[0].Kind)
			require.Equal(t, "1 finding.", diagnostics.Summary)
			require.Contains(t, console.String(), "1 finding.")
			page, err := os.ReadFile(filepath.Join(installer.sessionDirectory, reportFilename))
			require.NoError(t, err)
			report := string(page)
			require.Contains(t, report, `<article class="problem-card" data-kind="`+tc.kind+`">`)
			require.Contains(t, report, `<details class="command-output" open>`)
			require.Contains(t, html.UnescapeString(report), tc.log)
			require.NotContains(t, report, "Check the instrumentation setup.")
			if tc.advice != "" {
				require.Len(t, diagnostics.Troubleshooting, 1)
				require.Equal(t, tc.advice, diagnostics.Troubleshooting[0].Title)
				for _, text := range []string{diagnostics.Findings[0].Context, tc.advice, diagnostics.Troubleshooting[0].Text} {
					require.Contains(t, console.String(), text)
					require.Contains(t, html.UnescapeString(report), text)
				}
			} else {
				require.Empty(t, diagnostics.Troubleshooting)
			}
			if strings.Contains(tc.log, "<config.ts>") {
				require.Contains(t, report, "&lt;config.ts&gt;")
			}
		})
	}
}
