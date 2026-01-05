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

func TestMergeEnvMaps(t *testing.T) {
	t.Run("merges two maps", func(t *testing.T) {
		platform := map[string]string{"A": "1", "B": "2"}
		additional := map[string]string{"C": "3", "D": "4"}

		result := mergeEnvMaps(platform, additional)

		if len(result) != 4 {
			t.Errorf("expected 4 keys, got %d", len(result))
		}
		if result["A"] != "1" || result["B"] != "2" || result["C"] != "3" || result["D"] != "4" {
			t.Errorf("unexpected result: %v", result)
		}
	})

	t.Run("additional overrides platform", func(t *testing.T) {
		platform := map[string]string{"A": "platform", "B": "2"}
		additional := map[string]string{"A": "additional", "C": "3"}

		result := mergeEnvMaps(platform, additional)

		if result["A"] != "additional" {
			t.Errorf("expected additional to override platform, got %q", result["A"])
		}
		if result["B"] != "2" {
			t.Errorf("expected B to be preserved, got %q", result["B"])
		}
		if result["C"] != "3" {
			t.Errorf("expected C to be present, got %q", result["C"])
		}
	})

	t.Run("handles nil maps", func(t *testing.T) {
		result := mergeEnvMaps(nil, nil)
		if result == nil {
			t.Error("expected non-nil result")
		}
		if len(result) != 0 {
			t.Errorf("expected empty map, got %v", result)
		}
	})

	t.Run("handles nil platform", func(t *testing.T) {
		additional := map[string]string{"A": "1"}
		result := mergeEnvMaps(nil, additional)

		if result["A"] != "1" {
			t.Errorf("expected A=1, got %q", result["A"])
		}
	})

	t.Run("handles nil additional", func(t *testing.T) {
		platform := map[string]string{"A": "1"}
		result := mergeEnvMaps(platform, nil)

		if result["A"] != "1" {
			t.Errorf("expected A=1, got %q", result["A"])
		}
	})
}
