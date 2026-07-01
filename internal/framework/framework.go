package framework

import (
	"context"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

type Framework interface {
	Name() string
	TestPattern() string
	DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error)
	DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error)
	RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error
	SetPlatformEnv(platformEnv map[string]string)
	GetPlatformEnv() map[string]string
	SupportsFullTestDiscovery() bool
	SourceFileForSuite(suite string) (string, bool)
	HasUnskippableMarker(testFile string) bool
}
