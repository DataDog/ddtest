// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestCIRuntimeReportBoundsDiagnostics(t *testing.T) {
	root := t.TempDir()
	text := strings.Repeat("dynamic workflow input ", 10000)
	check := &onboard.RuntimeCheck{Status: "inconclusive", Reason: text, Jobs: []onboard.RuntimeFinding{{Status: "inconclusive", Node: text, Tracer: text, Requirement: text, Reason: text}}}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{CIRuntime: check}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.Less(t, len(data), 9500) // Includes the bounded, copyable final response. // Includes bounded final-response facts and runtime scope.
	require.Equal(t, text, check.Reason)
	require.Equal(t, text, check.Jobs[0].Reason)
}

func TestConfigurationChecksRequireCIAndTracerAgreement(t *testing.T) {
	for _, tc := range []struct {
		status, version string
		passed          bool
	}{
		{"compatible", "5.128.0", true},
		{"incompatible", "5.128.0", false},
		{"inconclusive", "", false},
		{"compatible", "5.127.0", false},
	} {
		t.Run(tc.status+tc.version, func(t *testing.T) {
			root := t.TempDir()
			check := onboard.RuntimeCheck{Status: tc.status, Jobs: []onboard.RuntimeFinding{{Status: tc.status, Tracer: "dd-trace@" + tc.version}}}
			result := validationResult{CheckOnly: true, Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}, CIRuntime: &check, CISelection: compareCISelection(check, "5.128.0")}
			err := finishValidation(&bytes.Buffer{}, root, result)
			require.Equal(t, tc.passed, err == nil)
			data, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			var report validationResult
			require.NoError(t, json.Unmarshal(data, &report))
			require.Equal(t, tc.passed, report.ChecksPassed)
			require.False(t, report.Success)
			require.False(t, report.LocalSuccess)
		})
	}
}
