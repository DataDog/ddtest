package framework

import (
	"context"
	"errors"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

type Framework interface {
	Name() string
	TestPattern() string
	TestExcludePattern() string
	DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error)
	RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error
	SetPlatformEnv(platformEnv map[string]string)
	GetPlatformEnv() map[string]string
}

var ErrFullTestDiscoveryUnsupported = errors.New("full test discovery is not supported")

type FullTestDiscoverySupporter interface {
	SupportsFullTestDiscovery() bool
}

// Asumme, by default, that every framework supports full test discovery.
// If it implements the FullTestDiscoverySupporter interface, then
// delegate the choice to SupportFullTestDiscovery()
func SupportsFullTestDiscovery(f Framework) bool {
	supporter, ok := f.(FullTestDiscoverySupporter)
	return !ok || supporter.SupportsFullTestDiscovery()
}

// BaseDiscoveryEnv returns environment variables required for all test discovery processes.
// These env vars ensure the test framework runs in discovery mode without requiring
// actual Datadog credentials or agent connectivity.
func BaseDiscoveryEnv() map[string]string {
	return map[string]string{
		"DD_CIVISIBILITY_ENABLED":                "1",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED":      "true",
		"DD_API_KEY":                             "dummy_key",
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED": "1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE":    discovery.TestsFilePath,
	}
}
