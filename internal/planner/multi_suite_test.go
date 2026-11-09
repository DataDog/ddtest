package planner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	setPlannerForceFullTestDiscovery(t, false)
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

func TestMultiSuitePlanningKeepsRunnableSibling(t *testing.T) {
	for _, mode := range []string{"full", "failed", "unsupported"} {
		for _, duration := range []string{"missing", "0", "-1", "invalid", "30000000000"} {
			t.Run(mode+"/"+duration, func(t *testing.T) {
				runner := newMultiSuitePlanner(t, duration, true)
				fw := runner.framework.(*MockFramework)
				switch mode {
				case "failed":
					fw.DiscoverTestsErr = errors.New("discovery failed")
				case "unsupported":
					fw.FullDiscoveryUnsupported = true
				}
				require.NoError(t, runner.PreparePlanningData(context.Background()))
				require.Contains(t, runner.testFileWeights, multiSuiteFile)
				require.Positive(t, runner.testFileWeights[multiSuiteFile])
				if mode == "full" {
					require.Equal(t, discoveryModeFull, runner.reportStats.discoveryMode)
					require.True(t, runner.completeSuiteInventory[multiSuiteFile])
				} else {
					require.Equal(t, discoveryModeFast, runner.reportStats.discoveryMode)
					require.False(t, runner.completeSuiteInventory[multiSuiteFile])
					require.Zero(t, runner.skippablePercentage, "historical data cannot prove current suites were skipped")
					require.Empty(t, runner.reportStats.uniqueTIASkippableSuitesApplied)
				}
			})
		}
	}
}

func TestMultiSuitePlanningDropsFileOnlyWithCompleteInventory(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("discoveryFailed=%t", fail), func(t *testing.T) {
			runner := newMultiSuitePlanner(t, "30000000000", true)
			runner.optimizationClient.(*MockTestOptimizationClient).Skippables.Suites[api.SkippableSuite{Module: "rspec", Suite: "B"}] = true
			if fail {
				runner.framework.(*MockFramework).DiscoverTestsErr = errors.New("discovery failed")
			}
			require.NoError(t, runner.PreparePlanningData(context.Background()))
			if fail {
				require.Equal(t, 40000, runner.testFileWeights[multiSuiteFile])
			} else {
				require.NotContains(t, runner.testFileWeights, multiSuiteFile)
			}
		})
	}
}

func TestMultiSuiteFastDurationAggregation(t *testing.T) {
	for _, tc := range []struct {
		duration string
		weight   int
		source   testFileDurationSource
	}{
		{"30000000000", 40000, testFileDurationSourceKnown},
		{"0", 11000, testFileDurationSourceMixed},
		{"-1", 11000, testFileDurationSourceMixed},
		{"invalid", 11000, testFileDurationSourceMixed},
	} {
		t.Run(tc.duration, func(t *testing.T) {
			runner := newMultiSuitePlanner(t, tc.duration, false)
			runner.framework.(*MockFramework).FullDiscoveryUnsupported = true
			require.NoError(t, runner.PreparePlanningData(context.Background()))
			require.Len(t, runner.suiteAggregates, 2)
			require.Equal(t, tc.weight, runner.testFileWeights[multiSuiteFile])
			require.Equal(t, tc.source, runner.testFileDurationSources[multiSuiteFile])
		})
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
