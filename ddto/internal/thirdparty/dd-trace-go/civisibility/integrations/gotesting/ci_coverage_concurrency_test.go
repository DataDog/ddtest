// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.
// Extracted coverage coordinator tests from dd-trace-go v2.11.0-rc.1.
package gotesting

import (
	"fmt"
	"testing"

	assert "github.com/tonyredondo/dd-ci-testing-poc/internal/testassert"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
)

func TestQuarantinedRaceCoverageCoordinatorDiscardsOnlySiblingOverlap(t *testing.T) {
	var coordinator quarantinedRaceCoverageCoordinator
	parent := coordinator.begin("TestCheckout/card")
	child := coordinator.begin("TestCheckout/card/visa")
	assert.True(t, coordinator.finish(child))
	assert.True(t, coordinator.finish(parent))

	first := coordinator.begin("TestCheckout/card/visa")
	second := coordinator.begin("TestCheckout/card/mastercard")
	assert.False(t, coordinator.finish(first))
	assert.False(t, coordinator.finish(second))

	isolated := coordinator.begin("TestCheckout/card/amex")
	assert.True(t, coordinator.finish(isolated))
}

func TestQuarantinedRaceParallelContinuationDisablesCoverage(t *testing.T) {
	const root, owner = "TestCheckout/root", "TestCheckout/root/owner"
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%t", parallel), func(t *testing.T) {
			cfg := &processRetrySubtreeConfig{
				Version: processRetrySubtreeVersion, SelectedRoot: root, AttemptToFixRetries: 2, CollectPerTest: true, CollectAggregate: true,
				Root: processRetrySubtreeDirective{TestName: root, ModuleName: "module", SuiteName: "suite", Quarantined: true},
			}
			attempt := processRetryAttemptResult{Result: processRetryResult{Subtests: []processRetrySubtreeResult{{
				TestName: owner, ModuleName: "module", SuiteName: "suite", Parallel: parallel, AttemptToFix: true, AttemptToFixOwn: true,
			}}}}
			var runCfg *processRetrySubtreeConfig
			_, failure := continueQuarantinedRaceDescendantFamilies(cfg, attempt, &[]quarantinedRaceInvocation{}, func(got *processRetrySubtreeConfig, runRoot string, _ int) processRetryAttemptResult {
				runCfg = got
				assert.Equal(t, map[bool]string{false: owner, true: root}[parallel], runRoot)
				return processRetryAttemptResult{Result: processRetryResult{Status: processRetryStatusPass}, ExitStatusObserved: true}
			})
			require.Nil(t, failure)
			require.NotNil(t, runCfg)
			assert.Equal(t, !parallel, runCfg.CollectPerTest)
			assert.Equal(t, !parallel, runCfg.CollectAggregate)
		})
	}
}

func TestQuarantinedRaceAggregateCoverageStartsAtSelectedRoot(t *testing.T) {
	starts, finishes := 0, 0
	state := newQuarantinedRaceChildState(&processRetrySubtreeConfig{SelectedRoot: "TestCheckout/card"})
	state.beginAggregate = func() { starts++ }
	state.finishAggregate = func() { finishes++ }

	state.beginAggregateCoverage("TestCheckout")
	state.beginAggregateCoverage("TestCheckout/card/visa")
	state.finishAggregateCoverage("TestCheckout")
	state.finishAggregateCoverage("TestCheckout/card/visa")
	assert.Zero(t, starts)
	assert.Zero(t, finishes)

	state.beginAggregateCoverage("TestCheckout/card")
	state.beginAggregateCoverage("TestCheckout/card")
	state.finishAggregateCoverage("TestCheckout/card")
	state.finishAggregateCoverage("TestCheckout/card")
	assert.Equal(t, 1, starts)
	assert.Equal(t, 1, finishes)
}
