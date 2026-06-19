package osinfo

import "testing"

func TestOSVersionReturnsCachedValue(t *testing.T) {
	originalVersion := osVersion
	t.Cleanup(func() {
		osVersion = originalVersion
	})

	osVersion = "test-version"

	if got := OSVersion(); got != "test-version" {
		t.Fatalf("OSVersion() = %q, want %q", got, "test-version")
	}
}
