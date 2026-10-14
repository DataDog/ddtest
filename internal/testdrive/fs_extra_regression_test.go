// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestOwnedTemporaryPathsDoNotChangeNativeOutcomes(t *testing.T) {
	left := validationRun{temporaryRoot: "/private/ddtest/baseline/tmp", Tests: []jestTest{{File: "/repo/test.js", Name: "copies /private/ddtest/baseline/tmp/src to /private/ddtest/baseline/tmp/dst", Status: "failed", Failure: "missing /private/ddtest/baseline/tmp/src"}}}
	right := validationRun{temporaryRoot: "/private/ddtest/reporting/tmp", Tests: []jestTest{{File: "/repo/test.js", Name: "copies /private/ddtest/reporting/tmp/src to /private/ddtest/reporting/tmp/dst", Status: "failed", Failure: "missing /private/ddtest/reporting/tmp/src"}}}
	require.Equal(t, outcomeKeys(left), outcomeKeys(right))
	right.Tests[0].Failure = "missing /private/ddtest/reporting/tmp/other"
	require.NotEqual(t, outcomeKeys(left), outcomeKeys(right))
	right.Tests[0].Failure = left.Tests[0].Failure
	right.Tests[0].Name = "copies /some/customer/path"
	require.NotEqual(t, outcomeKeys(left), outcomeKeys(right))
	require.Equal(t, "/private/ddtest/baseline/tmp-other/x", normalizeOwnedTemporaryPath(left, "/private/ddtest/baseline/tmp-other/x"))
	require.Contains(t, left.Tests[0].Name, left.temporaryRoot, "comparison must not mutate native or telemetry identities")
}

func TestMochaDiscoveryFailuresRetainReport(t *testing.T) {
	for _, all := range []bool{true, false} {
		drive := preparedTestdrive(t)
		drive.framework = framework.NewMocha(platform.NewJavaScript())
		drive.allConfigurations = all
		if !all {
			drive.preparationError = errors.New("opaque command")
		}
		err := drive.Run(t.Context(), &bytes.Buffer{})
		require.Error(t, err)
		report := readValidationReport(t, drive.repositoryRoot)
		require.Equal(t, "INCOMPLETE", report.Summary.Status)
		require.Empty(t, report.Runs)
		require.NotEmpty(t, report.Error)
		require.False(t, report.HTMLReportCurrent)
	}
}

func TestMochaCIInventorySurvivesChecksAndNewExecutions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github/workflows")
	require.NoError(t, os.MkdirAll(dir, 0700))
	write := func(matrix string) {
		requireWriteFile(t, filepath.Join(dir, "ci.yml"), "jobs:\n  test:\n    strategy:\n      matrix:\n        node: ["+matrix+"]\n        os: [linux, windows]\n    steps:\n      - run: npm test\n")
	}
	write("14, 18, 24")
	result := pairedReport("original", "passed")
	result.Framework = "mocha"
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, result))
	original := readValidationReport(t, root)
	require.Len(t, original.CIBaseline, 6)
	write("18, 24")
	for _, checkOnly := range []bool{true, false, true} {
		result.CheckOnly = checkOnly
		require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
		report := readValidationReport(t, root)
		require.Equal(t, "failed", report.CIPreservation.Status)
		require.Equal(t, 2, report.CIPreservation.DifferenceCount)
		require.Equal(t, original.CIBaseline, report.CIBaseline)
		require.Equal(t, "INCOMPLETE", report.Summary.Status)
		require.Contains(t, report.Summary.OnboardingResponse, "CI matrix preservation")
	}
	write("14, 18, 24, 26")
	result.CheckOnly = false
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, result))
	require.Equal(t, "passed", readValidationReport(t, root).CIPreservation.Status)
}

func TestCIPreservationAllowsAddedMetadataButNotChangedCoverage(t *testing.T) {
	original := `ci.yml / test / {"name":"Node 18","node":"18.14","os":"linux"}`
	for _, tc := range []struct {
		row  string
		want bool
	}{
		{`ci.yml / test / {"name":"Node 18","node":"18.14","os":"linux","dd-instrument":true}`, true},
		{`ci.yml / test / {"name":"Node 18","node":"22","os":"linux","dd-instrument":true}`, false},
		{`ci.yml / test / {"name":"Node 18","node":"18.14","os":"windows"}`, false},
		{`ci.yml / other / {"name":"Node 18","node":"18.14","os":"linux"}`, false},
		{`ci.yml / test / {"name":"Node 18","node":"18.14"}`, false},
		{`ci.yml / test / {"name":"Node 18","node":{},"os":"linux"}`, false},
	} {
		require.Equal(t, tc.want, preservedMatrixRow(original, []string{tc.row}), tc.row)
	}
	require.False(t, preservedMatrixRow(original, nil))
}
