// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// An incomplete handoff with unresolved CI scope tells the user who decides
// and the agent not to edit CI until ddtest accepts the step; other blockers
// keep their usual text.
func TestIncompleteHandoffSaysScopeBlockersAreTheUsersDecision(t *testing.T) {
	result := completeValidationFixture("jest")
	result.Scope = &onboard.ValidationScope{Unresolved: []string{".github/workflows/test.yml / test / step 2 (custom-wrapper): Cannot statically resolve command: custom-wrapper"}}
	result.Summary = summarizeValidation(result)
	require.Equal(t, "INCOMPLETE", result.Summary.Status)
	require.Contains(t, validationHandoff(result), "Unresolved CI scope blockers are limits of ddtest's static analysis: the user decides whether to run the tests in a CI step ddtest can read or to keep those steps reported as not validated. Do not edit CI to make ddtest accept them unless a ddtest result prescribes that edit.")
	result.Scope = &onboard.ValidationScope{}
	result.Summary = summarizeValidation(result)
	require.NotContains(t, validationHandoff(result), "Unresolved CI scope blockers")
	require.True(t, isScopeBlocker("unresolved CI scope (2 steps: a; b): cause"))
	require.False(t, isScopeBlocker("configuration npm test: blocked"))
}
