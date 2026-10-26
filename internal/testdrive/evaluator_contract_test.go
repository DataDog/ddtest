// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Python evaluator consumes the product writer's real JSON, including the
// generated handoff. This deliberately does not maintain a parallel JSON schema.
func TestExportEvaluatorReportContract(t *testing.T) {
	var reports []json.RawMessage
	for framework := range frameworkFeaturePolicies {
		root := t.TempDir()
		result := focusedValidationFixture(framework)
		result.Preflight.Node = "24.19.0"
		result.Configurations[0].Preflight = result.Preflight
		result.Runs[0].FrameworkNode = "22.0.0"
		identities := 32
		result.ReportingCounts = &executionCounts{TestExecutions: 96, SourceTestIdentities: &identities}
		_ = finishValidation(io.Discard, root, result)
		data, err := os.ReadFile(validationPath(root))
		require.NoError(t, err)
		current := readValidationReport(t, root)
		require.NotNil(t, current.Summary.BlockingChecks, "%s: blocking_checks must be a JSON array, including on success", framework)
		reports = append(reports, data)
		result.CheckOnly = true
		_ = finishValidation(io.Discard, root, result)
		historical := readValidationReport(t, root)
		require.NotNil(t, historical.LastExecution)
		require.False(t, historical.LastExecution.Current)
		require.NotNil(t, historical.Summary.BlockingChecks, "%s: check-only blocking_checks must be a JSON array", framework)
		data, err = os.ReadFile(validationPath(root))
		require.NoError(t, err)
		reports = append(reports, data)
	}
	if output := os.Getenv("DDTEST_EVAL_CONTRACT_OUTPUT"); output != "" {
		data, err := json.Marshal(reports)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, data, 0600))
	}
}
