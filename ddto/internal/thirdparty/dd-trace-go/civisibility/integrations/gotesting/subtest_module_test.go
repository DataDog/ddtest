// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

// A subtest outside a Testify suite takes its parent's module, when the parent
// has one: an imported helper package can create it. Ported from dd-trace-go
// #5401's TestProcessRetrySubtestModuleName.
func TestSubtestModuleName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parent *testExecutionMetadata
		want   string
	}{
		{"no parent metadata", nil, "helper/module"},
		{"no parent identity", &testExecutionMetadata{}, "helper/module"},
		{"empty parent module", &testExecutionMetadata{identity: newTestIdentity("", "parent_test.go", "TestParent")}, "helper/module"},
		{"parent module", &testExecutionMetadata{identity: newTestIdentity("consumer/module", "parent_test.go", "TestParent")}, "consumer/module"},
	} {
		if got := subtestModuleName("helper/module", tc.parent); got != tc.want {
			t.Errorf("%s: module %q, want %q", tc.name, got, tc.want)
		}
	}
}

func quarantinedIdentityTestCallback(*testing.T) {}

func quarantinedIdentityTestMethod() {}

// In a quarantined child process, a Testify suite keeps its module, suite and
// method source; another subtest takes its parent's module, or the selected
// root's, and keeps the callback's suite and source.
func TestQuarantinedSubtestIdentity(t *testing.T) {
	callback := runtime.FuncForPC(reflect.ValueOf(quarantinedIdentityTestCallback).Pointer())
	method := runtime.FuncForPC(reflect.ValueOf(quarantinedIdentityTestMethod).Pointer())
	withRoot := func(module string) *quarantinedRaceChildState {
		return &quarantinedRaceChildState{cfg: &processRetrySubtreeConfig{Root: processRetrySubtreeDirective{ModuleName: module}}}
	}
	parent := &testExecutionMetadata{identity: newTestIdentity("consumer/module", "parent_test.go", "TestParent")}
	check := func(t *testing.T, state *quarantinedRaceChildState, parent *testExecutionMetadata, module, suite string, source *runtime.Func) {
		t.Helper()
		gotModule, gotSuite, gotSource := quarantinedSubtestIdentity(t, state, parent, "helper/module", "callback.go", callback)
		if gotModule != module || gotSuite != suite || gotSource != source {
			t.Fatalf("identity %q, %q, %s; want %q, %q, %s", gotModule, gotSuite, gotSource.Name(), module, suite, source.Name())
		}
	}
	t.Run("parent", func(t *testing.T) {
		check(t, withRoot("root/module"), parent, "consumer/module", "callback.go", callback)
	})
	t.Run("root", func(t *testing.T) {
		check(t, withRoot("root/module"), &testExecutionMetadata{}, "root/module", "callback.go", callback)
	})
	t.Run("callback", func(t *testing.T) { check(t, withRoot(""), nil, "helper/module", "callback.go", callback) })

	key := reflect.ValueOf(t).UnsafePointer()
	setTestifyTestsByParentT(key, []TestifyTest{
		{methodName: "TestSource", suiteName: "suite.go/Suite", moduleName: "testify/module", methodFunc: method},
		{methodName: "TestNoMethod", suiteName: "suite.go/Suite", moduleName: "testify/module"},
	})
	t.Cleanup(func() {
		testifyTestsByParentTMutex.Lock()
		defer testifyTestsByParentTMutex.Unlock()
		delete(testifyTestsByParentT, key)
	})
	t.Run("TestSource", func(t *testing.T) {
		check(t, withRoot("root/module"), parent, "testify/module", "suite.go/Suite", method)
	})
	t.Run("TestNoMethod", func(t *testing.T) {
		check(t, withRoot("root/module"), parent, "testify/module", "suite.go/Suite", callback)
	})
}

// testing's common.Fail hook passes the failing test's *testing.common: in a
// quarantined child process, its failure is still the test's own unmasked
// failure, which a managed descendant's restore keeps.
func TestProcessRetryChildFailureThroughCommon(t *testing.T) {
	field := reflect.TypeFor[testing.T]().Field(0)
	if field.Name != "common" || field.Offset != 0 {
		t.Fatalf("testing.T starts with %s at %d", field.Name, field.Offset)
	}
	common := reflect.NewAt(field.Type, unsafe.Pointer(t)).Interface().(testing.TB)
	state := &quarantinedRaceChildState{cfg: &processRetrySubtreeConfig{SelectedRoot: t.Name()}}
	execMeta := createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	execMeta.quarantinedRaceChild = state
	markProcessRetryChildFailed(common)
	if !state.hasUnmaskedFailure(t.Name()) {
		t.Fatal("a failure through *testing.common is not recorded as unmasked")
	}
}
