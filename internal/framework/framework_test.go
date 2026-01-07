package framework

import (
	"testing"
)

func TestBaseDiscoveryEnv(t *testing.T) {
	env := BaseDiscoveryEnv()

	expectedVars := map[string]string{
		"DD_CIVISIBILITY_ENABLED":                "1",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED":      "true",
		"DD_API_KEY":                             "dummy_key",
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED": "1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE":    TestsDiscoveryFilePath,
	}

	for key, expectedValue := range expectedVars {
		if actualValue, exists := env[key]; !exists {
			t.Errorf("expected %q to be present in BaseDiscoveryEnv", key)
		} else if actualValue != expectedValue {
			t.Errorf("expected %q=%q, got %q", key, expectedValue, actualValue)
		}
	}

	// Verify no extra unexpected keys
	if len(env) != len(expectedVars) {
		t.Errorf("expected %d env vars, got %d", len(expectedVars), len(env))
	}
}
