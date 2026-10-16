package planner

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

// Wait for backend processing so the test exercises cancellation even when
// discovery would otherwise finish before settings are available.
type discoveryAfterBackendFramework struct {
	MockFramework
	backendDone <-chan struct{}
}

func (m *discoveryAfterBackendFramework) DiscoverTests(ctx context.Context, files discovery.TestFileSet) ([]testoptimization.Test, error) {
	select {
	case <-m.backendDone:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.MockFramework.DiscoverTests(ctx, files)
}

type backendCompletionClient struct {
	MockTestOptimizationClient
	backendDone chan struct{}
}

func (m *backendCompletionClient) StoreCacheAndExit() {
	m.MockTestOptimizationClient.StoreCacheAndExit()
	close(m.backendDone)
}

func TestTestPlanner_PreparePlanningData_ForceFullDiscoveryWhenSkippingDisabled(t *testing.T) {
	for _, level := range []settings.TestSkippingLevel{settings.TestSkippingLevelTest, settings.TestSkippingLevelSuite} {
		t.Run(level.String(), func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				tiaEnabled    bool
				testsSkipping bool
			}{
				{name: "test skipping disabled", tiaEnabled: true},
				{name: "TIA disabled", testsSkipping: true},
				{name: "both disabled"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, force := range []bool{false, true} {
						name := "default discovery"
						if force {
							name = "forced full discovery"
						}
						t.Run(name, func(t *testing.T) {
							t.Chdir(t.TempDir())
							setPlannerForceFullTestDiscovery(t, force)
							ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
							defer cancel()

							backendDone := make(chan struct{})
							mockFramework := &discoveryAfterBackendFramework{
								MockFramework: MockFramework{
									FrameworkName: "rspec",
									TestFiles:     []string{"spec/local_spec.rb"},
									Tests: []testoptimization.Test{
										{Module: "rspec", Suite: "Local", Name: "test", SuiteSourceFile: "spec/local_spec.rb"},
									},
								},
								backendDone: backendDone,
							}
							mockPlatform := &MockPlatform{
								PlatformName: "ruby",
								Tags:         map[string]string{"language": "ruby"},
								Framework:    mockFramework,
								TestLevel:    level,
							}
							client := &backendCompletionClient{
								MockTestOptimizationClient: MockTestOptimizationClient{
									Settings: testOptimizationSettings(tc.tiaEnabled, tc.testsSkipping, false),
								},
								backendDone: backendDone,
							}
							planner := NewWithDependencies(mockPlatform, mockFramework, client, newDefaultMockCIProviderDetector())
							if err := planner.PreparePlanningData(ctx); err != nil {
								t.Fatalf("PreparePlanningData() failed: %v", err)
							}
							if client.GetSkippablesCalled || planner.tiaSkippingEnabled {
								t.Fatal("disabled TIA skipping should not be enabled by forcing discovery")
							}
							if _, ok := planner.testFileWeights["spec/local_spec.rb"]; !ok {
								t.Fatalf("expected discovered file to remain runnable, got %v", planner.testFileWeights)
							}
							report := planner.newPlanReportData(splitScore{})
							wantMode := discoveryModeFast
							wantTests := 0
							if force {
								wantMode = discoveryModeFull
								wantTests = 1
							}
							if report.Planning.Discovery.Mode != wantMode || report.Planning.Discovery.Tests != wantTests {
								t.Fatalf("discovery = %+v, want mode %s with %d tests", report.Planning.Discovery, wantMode, wantTests)
							}
						})
					}
				})
			}
		})
	}
}
