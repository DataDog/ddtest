package testdrive

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFeaturePolicyRequiresObservedResultsForEveryProject(t *testing.T) {
	var complete []featureResult
	for _, policy := range featurePolicies("jest") {
		require.Equal(t, "required", policy.Status, "the table must never preset a passing observation")
		complete = append(complete, featureResult{Project: "unit", Name: policy.Name, Status: "passed"})
	}
	require.False(t, featureChecksPassed("jest", nil))
	require.True(t, featureChecksPassed("jest", complete))
	require.False(t, featureChecksPassed("jest", complete[:len(complete)-1]))
	second := append(append([]featureResult{}, complete...), featureResult{Project: "server", Name: "auto-retries", Status: "passed"})
	require.False(t, featureChecksPassed("jest", second), "a passing unit project cannot certify a partially checked server project")
	for _, status := range []string{"failed", "inconclusive", "not exercised", "not supported", "deferred"} {
		incomplete := append([]featureResult{}, complete...)
		incomplete[0].Status = status
		require.False(t, featureChecksPassed("jest", incomplete), status)
	}
	require.True(t, supportsValidationFramework("vitest"))
	require.True(t, supportsValidationFramework("mocha"))
	require.False(t, supportsValidationFramework("playwright"), "adapters register only when their validation code lands")
}

func TestValidationFrameworkNamesFollowRegisteredPolicies(t *testing.T) {
	previous, exists := frameworkFeaturePolicies["example"]
	t.Cleanup(func() {
		if exists {
			frameworkFeaturePolicies["example"] = previous
		} else {
			delete(frameworkFeaturePolicies, "example")
		}
	})
	frameworkFeaturePolicies["example"] = frameworkPolicy{Paired: true}
	require.Contains(t, ValidationFrameworkNames(), "example")
	require.Contains(t, ValidationFrameworkNames(), "Jest")
	require.NotContains(t, ValidationFrameworkNames(), "Python")
}
