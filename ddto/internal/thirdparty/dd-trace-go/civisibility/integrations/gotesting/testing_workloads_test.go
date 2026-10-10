// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

func TestRestoreTestingMWorkloadsRestoresFuzzTargetsAndExamples(t *testing.T) {
	originalFuzz := func(*testing.F) {}
	originalExample := func() {}
	fuzzTargets := []testing.InternalFuzzTarget{{Name: "FuzzOriginal", Fn: func(*testing.F) {}}}
	examples := []testing.InternalExample{{Name: "ExampleOriginal", F: func() {}, Output: "output\n", Unordered: true}}
	claim := &testingMInstrumentationClaim{
		fuzzDescriptors:    &fuzzTargets,
		exampleDescriptors: &examples,
		fuzzTargets:        map[string]func(*testing.F){"FuzzOriginal": originalFuzz},
		examples:           map[string]func(){"ExampleOriginal": originalExample},
	}

	restoreTestingMWorkloads(&testing.M{}, claim)

	require.Equal(t, reflectFuncPointer(originalFuzz), reflectFuncPointer(fuzzTargets[0].Fn))
	require.Equal(t, reflectFuncPointer(originalExample), reflectFuncPointer(examples[0].F))
	require.Equal(t, "output\n", examples[0].Output)
	require.True(t, examples[0].Unordered)
}

func TestFuzzAndExampleDescriptorsReserveWorkloadCounters(t *testing.T) {
	fuzzFunc := func(*testing.F) {}
	exampleFunc := func() {}
	fuzzTargets := []testing.InternalFuzzTarget{{Name: "FuzzCounter", Fn: fuzzFunc}}
	examples := []testing.InternalExample{{Name: "ExampleCounter", F: exampleFunc, Output: "output\n"}}

	type counterDelta struct {
		modules map[string]int
		suites  map[testSuiteCounterKey]int
	}
	want := counterDelta{modules: map[string]int{}, suites: map[testSuiteCounterKey]int{}}
	for _, fn := range []any{fuzzFunc, exampleFunc} {
		function := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
		moduleName, suiteName := utils.GetModuleAndSuiteName(function.Entry())
		want.modules[moduleName]++
		want.suites[testSuiteCounterKey{moduleName: moduleName, suiteName: suiteName}]++
	}
	moduleCountersBefore := make(map[string]int, len(want.modules))
	for name := range want.modules {
		moduleCountersBefore[name] = addModulesCounters(name, 0)
	}
	suiteCountersBefore := make(map[testSuiteCounterKey]int, len(want.suites))
	for key := range want.suites {
		suiteCountersBefore[key] = addSuitesCounters(key.moduleName, key.suiteName, 0)
	}
	t.Cleanup(func() {
		for name, delta := range want.modules {
			addModulesCounters(name, -delta)
		}
		for key, delta := range want.suites {
			addSuitesCounters(key.moduleName, key.suiteName, -delta)
		}
	})

	ddm := new(M)
	ddm.instrumentInternalFuzzTargets(&fuzzTargets, nil)
	ddm.instrumentInternalExamples(&examples, nil)

	for name, delta := range want.modules {
		require.Equal(t, moduleCountersBefore[name]+delta, addModulesCounters(name, 0))
	}
	for key, delta := range want.suites {
		require.Equal(t, suiteCountersBefore[key]+delta, addSuitesCounters(key.moduleName, key.suiteName, 0))
	}
}

func reflectFuncPointer(fn any) uintptr {
	return reflect.ValueOf(fn).Pointer()
}
