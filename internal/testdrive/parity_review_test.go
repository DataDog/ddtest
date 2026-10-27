package testdrive

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUncontrolledFrameworkEnvironmentKeepsMainFeatureSettings(t *testing.T) {
	for _, language := range []string{"python", "ruby", "javascript"} {
		env := (&Testdrive{language: language}).environment("/trace/init", "http://127.0.0.1:1234", "session")
		for _, key := range []string{"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED", "DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED", "DD_TEST_FAILED_TEST_REPLAY_ENABLED", "DD_TEST_MANAGEMENT_ENABLED", "DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED"} {
			require.Equal(t, "true", env[key], key)
		}
		require.Equal(t, "5", env["DD_CIVISIBILITY_FLAKY_RETRY_COUNT"])
		require.NotContains(t, env, "DDTEST_JEST_PROBE")
	}
}

func TestUnknownFrameworkHasNoImplicitValidationCapabilities(t *testing.T) {
	for _, name := range []string{"", "pytest", "unknown"} {
		require.False(t, supportsValidationFramework(name))
		require.False(t, featureChecksPassed(name, nil))
		require.Empty(t, featurePolicies(name))
		require.Equal(t, frameworkPolicy{}, frameworkFeaturePolicies[name])
	}
}
