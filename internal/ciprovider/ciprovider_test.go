package ciprovider

import (
	"os"
	"testing"
)

func clearCIEnvVars() {
	ciEnvVars := []string{
		"GITHUB_SHA", "GITLAB_CI", "CIRCLECI", "JENKINS_URL", "BUILDKITE",
		"TF_BUILD", "BITBUCKET_COMMIT", "BUDDY", "TRAVIS", "BITRISE_BUILD_SLUG",
		"CF_BUILD_ID", "APPVEYOR", "TEAMCITY_VERSION", "CODEBUILD_INITIATOR",
	}

	for _, envVar := range ciEnvVars {
		_ = os.Unsetenv(envVar)
	}
}

func TestDetectCIProvider_GitHub(t *testing.T) {
	clearCIEnvVars()
	_ = os.Setenv("GITHUB_SHA", "test-sha")
	defer func() { _ = os.Unsetenv("GITHUB_SHA") }()

	provider, err := DetectCIProvider()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if provider.Name() != "github" {
		t.Errorf("Expected provider name 'github', got '%s'", provider.Name())
	}
}

func TestDetectCIProvider_NoProvider(t *testing.T) {
	t.Skip("Skipping test as CI detection may use git repository context which interferes with testing")
}

func TestDatadogCIProviderDetector_DetectCIProvider(t *testing.T) {
	clearCIEnvVars()
	_ = os.Setenv("GITHUB_SHA", "test-sha")
	defer func() { _ = os.Unsetenv("GITHUB_SHA") }()

	detector := &DatadogCIProviderDetector{}
	provider, err := detector.DetectCIProvider()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if provider.Name() != "github" {
		t.Errorf("Expected provider name 'github', got '%s'", provider.Name())
	}
}

func TestNewCIProviderDetector(t *testing.T) {
	detector := NewCIProviderDetector()
	if detector == nil {
		t.Fatal("Expected detector to be non-nil")
	}

	_, ok := detector.(*DatadogCIProviderDetector)
	if !ok {
		t.Errorf("Expected detector to be of type *DatadogCIProviderDetector")
	}
}

func TestCIProviderConfigure(t *testing.T) {
	provider := NewGitHub()
	err := provider.Configure(4) // Test with 4 parallel runners
	if err != nil {
		t.Errorf("Expected Configure(4) to return nil, got %v", err)
	}
}

func TestDetectCIProvider_UnsupportedProvider(t *testing.T) {
	t.Skip("Skipping test as CI detection may use git repository context which interferes with testing")
}
