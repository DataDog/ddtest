package planner

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/telemetry"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

// SuiteFileSelection reports runnable files without writing a worker plan.
type SuiteFileSelection struct {
	RunnableFiles     []string
	UnskippableSuites int
}

// SelectPythonSuiteFiles uses declared worker tags and static pytest discovery.
func SelectPythonSuiteFiles(ctx context.Context, files []string, tags map[string]string, telemetryClient telemetry.Client) (SuiteFileSelection, error) {
	if !completeRuntimeTags(tags) {
		return SuiteFileSelection{}, fmt.Errorf("suite selection requires complete worker runtime tags")
	}
	return selectSuiteFiles(ctx, files, framework.NewPytest(nil),
		testoptimization.NewSuiteSelectionClient(tags, telemetryClient))
}

func selectSuiteFiles(ctx context.Context, files []string, fw framework.Framework, client testOptimizationClient) (SuiteFileSelection, error) {
	if err := ctx.Err(); err != nil {
		return SuiteFileSelection{}, err
	}
	// Each command owns its planner and backend client. No worker-plan cache is shared.
	tp := newTestPlannerWithDefaults()
	tp.optimizationClient = client
	for _, file := range files {
		tp.testFiles[file] = struct{}{}
	}
	if err := client.Initialize(nil); err != nil {
		return SuiteFileSelection{}, err
	}
	defer client.StoreCacheAndExit()
	repositorySettings := client.GetSettings()
	enabled := repositorySettings != nil && repositorySettings.ItrEnabled && repositorySettings.TestsSkipping
	if enabled {
		matcher := tp.fetchSkippables(true)
		if durations := client.GetTestSuiteDurations(); durations != nil {
			tp.testSuiteDurations = durations.TestSuites
		}
		tp.addDurationDataForFastDiscoveryFallback()
		tp.recordSuiteLevelSkippables(matcher, fw)
		tp.keepUnskippableMarkerSuitesRunnable(fw)
		tp.suitesBySourceFile = indexSuitesBySourceFile(tp.suiteAggregates)
	}
	if err := ctx.Err(); err != nil {
		return SuiteFileSelection{}, err
	}
	return SuiteFileSelection{
		RunnableFiles:     append([]string{}, slices.Sorted(maps.Keys(tp.calculateFileWeights()))...),
		UnskippableSuites: tp.reportStats.unskippableMarkerSuitesForced,
	}, nil
}
