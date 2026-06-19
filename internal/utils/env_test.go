package utils

import (
	"os"
	"testing"
)

func TestBoolEnv(t *testing.T) {
	const key = "DDTEST_BOOL_ENV_TEST"
	originalValue, originallySet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if originallySet {
			_ = os.Setenv(key, originalValue)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	if got := BoolEnv(key, true); !got {
		t.Fatal("BoolEnv() should return default for unset env")
	}

	t.Setenv(key, "false")
	if got := BoolEnv(key, true); got {
		t.Fatal("BoolEnv() should parse false")
	}

	t.Setenv(key, "invalid")
	if got := BoolEnv(key, true); !got {
		t.Fatal("BoolEnv() should return default for invalid env")
	}
}

func TestIntEnv(t *testing.T) {
	const key = "DDTEST_INT_ENV_TEST"
	originalValue, originallySet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if originallySet {
			_ = os.Setenv(key, originalValue)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	if got := IntEnv(key, 7); got != 7 {
		t.Fatalf("IntEnv() = %d, want default 7 for unset env", got)
	}

	t.Setenv(key, "42")
	if got := IntEnv(key, 7); got != 42 {
		t.Fatalf("IntEnv() = %d, want parsed value 42", got)
	}

	t.Setenv(key, "invalid")
	if got := IntEnv(key, 7); got != 7 {
		t.Fatalf("IntEnv() = %d, want default 7 for invalid env", got)
	}
}
