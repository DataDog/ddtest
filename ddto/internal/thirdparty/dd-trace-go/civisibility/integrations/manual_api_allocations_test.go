// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"testing"
	"time"
)

// Without options, an existing module or suite, as every test after the first
// finds, returns without allocating options or reading the clock. Given
// options still run, and an existing module or suite ignores them as it did.
func TestExistingModulesAndSuitesAllocateNothing(t *testing.T) {
	session := CreateTestSession()
	module := session.GetOrCreateModule("m")
	suite := module.GetOrCreateSuite("s")
	start := time.Unix(1, 0)
	var moduleOptions, suiteOptions int
	moduleOption := func(o *tslvTestModuleStartOptions) { moduleOptions++; WithTestModuleStartTime(start)(o) }
	suiteOption := func(o *tslvTestSuiteStartOptions) { suiteOptions++; WithTestSuiteStartTime(start)(o) }
	for range 2 {
		if session.GetOrCreateModule("m", moduleOption) != module || module.GetOrCreateSuite("s", suiteOption) != suite {
			t.Fatal("an existing module or suite was replaced")
		}
	}
	if moduleOptions != 2 || suiteOptions != 2 {
		t.Fatalf("options ran %d and %d times for 2 lookups each", moduleOptions, suiteOptions)
	}
	if module.StartTime().Equal(start) || suite.StartTime().Equal(start) {
		t.Fatal("options changed an existing module or suite")
	}
	allocs := testing.AllocsPerRun(100, func() {
		session.GetOrCreateModule("m")
		module.GetOrCreateSuite("s")
	})
	if allocs != 0 {
		t.Fatalf("finding an existing module and suite allocated %v objects", allocs)
	}
}

// Starting or closing a test without options allocates no configuration, and
// options still apply.
func TestTestOptionsWithoutOptionsAllocateNothing(t *testing.T) {
	if allocs := testing.AllocsPerRun(100, func() {
		_ = testStartOptions(nil)
		_ = testCloseOptions(nil)
	}); allocs != 0 {
		t.Fatalf("configurations without options allocated %v objects", allocs)
	}
	start := time.Unix(2, 0)
	if got := testStartOptions([]TestStartOption{WithTestStartTime(start)}); !got.startTime.Equal(start) {
		t.Fatalf("start time %v", got.startTime)
	}
	if got := testCloseOptions([]TestCloseOption{WithTestSkipReason("reason"), WithTestFinishTime(start)}); got.skipReason != "reason" || !got.finishTime.Equal(start) {
		t.Fatalf("close options %+v", got)
	}
}
