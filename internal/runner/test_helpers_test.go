package runner

import (
	"bytes"
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	originalLogger := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(originalLogger)
	})
	return &buf
}

type MockPlatformDetector struct {
	Platform platform.Platform
	Err      error
}

func (m *MockPlatformDetector) DetectPlatform() (platform.Platform, error) {
	return m.Platform, m.Err
}

type MockPlatform struct {
	PlatformName string
	Tags         map[string]string
	TagsErr      error
	Framework    framework.Framework
	FrameworkErr error
	SanityErr    error
}

func (m *MockPlatform) Name() string {
	return m.PlatformName
}

func (m *MockPlatform) CreateTagsMap() (map[string]string, error) {
	return m.Tags, m.TagsErr
}

func (m *MockPlatform) DetectFramework() (framework.Framework, error) {
	return m.Framework, m.FrameworkErr
}

func (m *MockPlatform) SanityCheck() error {
	return m.SanityErr
}

type MockFramework struct {
	FrameworkName            string
	TestPatternValue         string
	Tests                    []testoptimization.Test
	Err                      error
	DiscoverTestsErr         error
	RunTestsCalls            []RunTestsCall
	FullDiscoveryUnsupported bool
	mu                       sync.Mutex
}

type RunTestsCall struct {
	TestFiles []string
	EnvMap    map[string]string
}

func (m *MockFramework) Name() string {
	return m.FrameworkName
}

func (m *MockFramework) TestPattern() string {
	return m.TestPatternValue
}

func (m *MockFramework) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	if m.DiscoverTestsErr != nil {
		return nil, m.DiscoverTestsErr
	}
	return m.Tests, m.Err
}

func (m *MockFramework) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RunTestsCalls = append(m.RunTestsCalls, RunTestsCall{
		TestFiles: slices.Clone(testFiles),
		EnvMap:    maps.Clone(envMap),
	})
	return m.Err
}

func (m *MockFramework) SetPlatformEnv(platformEnv map[string]string) {
}

func (m *MockFramework) GetPlatformEnv() map[string]string {
	return nil
}

func (m *MockFramework) SupportsFullTestDiscovery() bool {
	return !m.FullDiscoveryUnsupported
}

func (m *MockFramework) GetRunTestsCallsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RunTestsCalls)
}

func (m *MockFramework) GetRunTestsCalls() []RunTestsCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.RunTestsCalls)
}

type roundRobinTestPlanner struct{}

func (roundRobinTestPlanner) DistributeTestFiles(testFiles []string, parallelRunners int) [][]string {
	if parallelRunners <= 0 {
		parallelRunners = 1
	}

	groups := make([][]string, parallelRunners)
	for i := range groups {
		groups[i] = []string{}
	}
	for index, testFile := range testFiles {
		groups[index%parallelRunners] = append(groups[index%parallelRunners], testFile)
	}
	return groups
}
