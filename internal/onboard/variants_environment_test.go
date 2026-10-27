package onboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCIStepKeepsTracerPluginSettings(t *testing.T) {
	step := runtimeStep{Run: "npm run test-ci", Env: map[string]string{
		"DD_TRACE_DISABLED_INSTRUMENTATIONS": "router",
		"DD_TRACE_EXPRESS_ENABLED":           "false",
		"DD_ENV":                             "ci",
		"DD_API_KEY":                         "${{ secrets.DD_API_KEY }}",
		"DD_TRACE_ENABLED":                   "false",
	}}
	_, env, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, step, nil, "mocha")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"DD_TRACE_DISABLED_INSTRUMENTATIONS": "router", "DD_TRACE_EXPRESS_ENABLED": "false"}, env)
}

func TestTracerPluginSetting(t *testing.T) {
	for _, key := range []string{"DD_TRACE_DISABLED_INSTRUMENTATIONS", "DD_TRACE_DISABLED_PLUGINS", "DD_TRACE_ROUTER_ENABLED"} {
		require.True(t, TracerPluginSetting(key), key)
	}
	for _, key := range []string{"DD_TRACE_ENABLED", "DD_TRACE__ENABLED", "DD_ENV", "DD_TRACE_AGENT_URL", "DD_CIVISIBILITY_ENABLED"} {
		require.False(t, TracerPluginSetting(key), key)
	}
}
