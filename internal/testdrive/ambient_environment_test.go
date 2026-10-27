package testdrive

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAmbientDatadogSettingsAreRecorded(t *testing.T) {
	t.Setenv("DD_TRACE_DISABLED_INSTRUMENTATIONS", "router")
	t.Setenv("DD_SERVICE", "from-ci")
	t.Setenv("DD_CIVISIBILITY_ENABLED", "true")
	t.Setenv("DD_CUSTOM_TOKEN", "ambient-secret")
	ambient := reportedEnvironment(ambientDatadogEnvironment(map[string]string{"DD_SERVICE": "from-ci"}))
	require.Equal(t, "router", ambient["DD_TRACE_DISABLED_INSTRUMENTATIONS"])
	require.Equal(t, "[REDACTED]", ambient["DD_CUSTOM_TOKEN"])
	require.NotContains(t, ambient, "DD_SERVICE", "the CI configuration sets it")
	require.NotContains(t, ambient, "DD_CIVISIBILITY_ENABLED", "ddtest sets it for every run")

	// main sets this on ddtest's own process; it is not the user's setting.
	t.Setenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "0")
	require.NotContains(t, ambientDatadogEnvironment(nil), "DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED")
	t.Setenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "1")
	require.Contains(t, ambientDatadogEnvironment(nil), "DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "a different value is the user's")

	result := pairedReport("ambient", "passed")
	result.AmbientDatadogEnvironment = map[string]string{"DD_TRACE_DISABLED_INSTRUMENTATIONS": "router"}
	require.Contains(t, validationHandoff(result), "Ambient Datadog settings (not from CI) reached local runs: DD_TRACE_DISABLED_INSTRUMENTATIONS=router.")
}
