// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"sync"
	"testing"

	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	internalenv "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalCiVisibilityInitializationParentModeRewritesEnvAfterTracerInitialization(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	disableAdditionalFeaturesForBootstrapTest()
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")

	var valueDuringTracerInit string
	internalCiVisibilityInitialization(func(_ []tracer.StartOption) {
		var ok bool
		valueDuringTracerInit, ok = internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
		require.True(t, ok)
	})

	valueAfterTracerInit, ok := internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
	require.True(t, ok)
	assert.Equal(t, "1", valueDuringTracerInit)
	assert.Equal(t, "false", valueAfterTracerInit)
}

func TestInternalCiVisibilityInitializationExplicitTrueDoesNotRewriteEnvToFalse(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	disableAdditionalFeaturesForBootstrapTest()
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "true")

	var valueDuringTracerInit string
	internalCiVisibilityInitialization(func(_ []tracer.StartOption) {
		var ok bool
		valueDuringTracerInit, ok = internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
		require.True(t, ok)
	})

	valueAfterTracerInit, ok := internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
	require.True(t, ok)
	assert.Equal(t, "1", valueDuringTracerInit)
	assert.Equal(t, "1", valueAfterTracerInit)
}

// resetCIVisibilityBootstrapStateForTesting clears bootstrap-only global state while preserving package test mode.
func resetCIVisibilityBootstrapStateForTesting() {
	resetCIVisibilityStateForTesting()
	testMode := civisibility.IsTestMode()
	civisibility.ResetForTesting()
	if testMode {
		civisibility.SetTestMode()
	}
	ciVisibilityInitializationOnce = sync.Once{}
}

// restoreCIVisibilityBootstrapForTesting clears the native bootstrap and client state.
func restoreCIVisibilityBootstrapForTesting() {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	// No network writer is installed by the lifecycle-only test callback.
	tracer.Stop()
}

// disableAdditionalFeaturesForBootstrapTest prevents bootstrap tests from starting asynchronous backend setup.
func disableAdditionalFeaturesForBootstrapTest() {
	additionalFeaturesInitializationOnce = sync.Once{}
	additionalFeaturesInitializationOnce.Do(func() {})
}

// Lifecycle-only upstream tests inject a writer-free bootstrap. Real delivery
// and shutdown are covered by the native client and wire integration tests.
func initializeCIVisibilityLifecycleForTesting() {
	internalCiVisibilityInitialization(func([]tracer.StartOption) {})
}
