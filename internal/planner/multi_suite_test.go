package planner

import (
	"bytes"
	"context"
	"testing"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/stretchr/testify/require"
)

const multiSuiteFile = "spec/multiple_spec.rb"

func newMultiSuitePlanner(t *testing.T, siblingDuration string, skip bool) *TestPlanner {
	t.Helper()
	t.Chdir(t.TempDir())
	setPlannerForceFullTestDiscovery(t, true)
	environment.ResetCITags()
	t.Cleanup(environment.ResetCITags)
	captureLogs(t)
	fw := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{multiSuiteFile},
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "A", Name: "a", SuiteSourceFile: multiSuiteFile},
			{Module: "rspec", Suite: "B", Name: "b", SuiteSourceFile: multiSuiteFile},
		},
	}
	p := &MockPlatform{PlatformName: "ruby", Framework: fw, TestLevel: settings.TestSkippingLevelSuite}
	durations := map[string]api.TestSuiteDurationInfo{
		"A": {SourceFile: multiSuiteFile, Duration: api.DurationPercentiles{P50: "10000000000"}},
	}
	if siblingDuration != "missing" {
		durations["B"] = api.TestSuiteDurationInfo{SourceFile: multiSuiteFile, Duration: api.DurationPercentiles{P50: siblingDuration}}
	}
	skippables := api.NewSkippables()
	if skip {
		skippables.Suites[api.SkippableSuite{Module: "rspec", Suite: "A"}] = true
	}
	client := &MockTestOptimizationClient{
		Settings:   testOptimizationSettings(true, true, false),
		Skippables: skippables,
		Durations:  map[string]map[string]api.TestSuiteDurationInfo{"rspec": durations},
	}
	return NewWithDependencies(p, fw, client, newDefaultMockCIProviderDetector())
}

// The planner must handle multiple suite identities independently of the framework.
func TestMultiSuiteFullDiscovery(t *testing.T) {
	for _, frameworkName := range []string{"rspec", "minitest", "pytest"} {
		for _, tc := range []struct {
			name     string
			duration string
			skipA    bool
			skipB    bool
			weight   int
			source   testFileDurationSource
		}{
			{"sum", "30000000000", false, false, 40000, testFileDurationSourceKnown},
			{"runnable sibling", "30000000000", true, false, 30000, testFileDurationSourceKnown},
			{"missing sibling timing", "missing", true, false, 1000, testFileDurationSourceDefault},
			{"zero sibling timing", "0", true, false, 1000, testFileDurationSourceDefault},
			{"invalid sibling timing", "invalid", true, false, 1000, testFileDurationSourceDefault},
			{"skipped default contribution", "missing", false, true, 10000, testFileDurationSourceKnown},
			{"all skipped", "30000000000", true, true, 0, ""},
		} {
			t.Run(frameworkName+"/"+tc.name, func(t *testing.T) {
				runner := newMultiSuitePlanner(t, tc.duration, tc.skipA)
				runner.framework.(*MockFramework).FrameworkName = frameworkName
				if tc.skipB {
					runner.optimizationClient.(*MockTestOptimizationClient).Skippables.Suites[api.SkippableSuite{Module: "rspec", Suite: "B"}] = true
					// Full discovery also verifies a skippable suite's source mapping.
					runner.framework.(*MockFramework).SuiteSourceFiles = map[string]string{"B": multiSuiteFile}
				}
				require.NoError(t, runner.PreparePlanningData(context.Background()))
				require.Equal(t, discoveryModeFull, runner.reportStats.discoveryMode)
				require.Len(t, runner.suiteAggregates, 2)
				if tc.weight == 0 {
					require.NotContains(t, runner.testFileWeights, multiSuiteFile)
				} else {
					require.Equal(t, tc.weight, runner.testFileWeights[multiSuiteFile])
					require.Equal(t, tc.source, runner.testFileDurationSources[multiSuiteFile])
				}
			})
		}
	}
}

func TestMultiSuiteMixedDurationClassification(t *testing.T) {
	for _, backendName := range []string{"A", "Z"} {
		t.Run(backendName, func(t *testing.T) {
			runner := newMultiSuitePlanner(t, "missing", false)
			setPlannerForceFullTestDiscovery(t, true)
			fw := runner.framework.(*MockFramework)
			fw.Tests[0].Suite = backendName
			durations := runner.optimizationClient.(*MockTestOptimizationClient).Durations["rspec"]
			info := durations["A"]
			delete(durations, "A")
			durations[backendName] = info
			require.NoError(t, runner.PreparePlanningData(context.Background()))
			require.Equal(t, 11000, runner.testFileWeights[multiSuiteFile])
			require.Equal(t, testFileDurationSourceMixed, runner.testFileDurationSources[multiSuiteFile])
			report := runner.newPlanReportData(splitScore{})
			require.Equal(t, 1, report.Planning.Durations.FilesWithMixedDurations)
			require.Zero(t, report.Planning.Durations.FilesWithoutDurations)
			var output bytes.Buffer
			printDurationEstimatesPlanningReport(&output, report.Planning.Durations)
			require.Contains(t, output.String(), "Mixed backend/default durations: 1 file")
			client := newPlannerTelemetryClient()
			runner.telemetryClient = client
			runner.recordPlanningTelemetry(splitSelection{})
			tags := []string{"platform:ruby", "framework:rspec", "test_skipping_mode:suite", "discovery_mode:full", "tia_enabled:true"}
			require.Equal(t, float64(1), client.value("ddtest.planning.test_file_durations", append(tags, "source:mixed")...))
			require.Zero(t, client.value("ddtest.planning.test_file_durations", append(tags, "source:backend")...))
			require.Zero(t, client.value("ddtest.planning.test_file_durations", append(tags, "source:default")...))
		})
	}
}

func TestMultiSuiteFastDiscoveryKeepsRunnableSibling(t *testing.T) {
	for _, duration := range []string{"30000000000", "0", "-1", "invalid"} {
		t.Run(duration, func(t *testing.T) {
			runner := newMultiSuitePlanner(t, duration, true)
			setPlannerForceFullTestDiscovery(t, false)
			runner.framework.(*MockFramework).OnDiscoverTests = func() {
				t.Fatal("suite mode must not invoke full discovery")
			}
			require.NoError(t, runner.PreparePlanningData(context.Background()))
			require.Equal(t, discoveryModeFast, runner.reportStats.discoveryMode)
			weight := 1000
			if duration == "30000000000" {
				weight = 30000
			}
			require.Equal(t, weight, runner.testFileWeights[multiSuiteFile])

			// The same file can be omitted when both known suites are skippable.
			runner.optimizationClient.(*MockTestOptimizationClient).Skippables.Suites[api.SkippableSuite{Module: "rspec", Suite: "B"}] = true
			require.NoError(t, runner.PreparePlanningData(context.Background()))
			require.NotContains(t, runner.testFileWeights, multiSuiteFile)
		})
	}
}
