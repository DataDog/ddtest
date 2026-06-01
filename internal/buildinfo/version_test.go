package buildinfo

import "testing"

func TestCurrentVersionUsesInjectedVersion(t *testing.T) {
	originalVersion := Version
	t.Cleanup(func() {
		Version = originalVersion
	})

	Version = "v1.2.3"

	if got := CurrentVersion(); got != "v1.2.3" {
		t.Fatalf("expected injected version, got %q", got)
	}
}

func TestCurrentVersionFallsBackToDefault(t *testing.T) {
	originalVersion := Version
	t.Cleanup(func() {
		Version = originalVersion
	})

	Version = ""

	if got := CurrentVersion(); got != defaultVersion {
		t.Fatalf("expected default version, got %q", got)
	}
}
