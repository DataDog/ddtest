// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --all can validate Cypress configurations whose actions use different Node
// majors; each substitution is reported and accepted, none is dropped.
func TestEveryRuntimeSubstitutionIsKept(t *testing.T) {
	var result validationResult
	node20 := runtimeSubstitution{Source: "cypress-io/github-action@v6", CI: "20", Local: "v24.19.0"}
	node22 := runtimeSubstitution{Source: "cypress-io/github-action@v6", CI: "22", Local: "v24.19.0"}
	result.addSubstitution(node20)
	result.addSubstitution(node22)
	result.addSubstitution(node20)
	require.Equal(t, []runtimeSubstitution{node20, node22}, result.substitutions())
	require.Equal(t, node20, *result.RuntimeSubstitution)

	summary := summarizeValidation(result)
	require.Contains(t, summary.BlockingChecks, node20.blocker())
	require.Contains(t, summary.BlockingChecks, node22.blocker())

	for i := range result.RuntimeSubstitutions {
		result.RuntimeSubstitutions[i].Accepted = true
	}
	caveats, _ := ciFidelity(&result)
	require.Len(t, caveats, 2, "both accepted substitutions are named")
}
