// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package gotesting

import (
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/net"
)

// ddto's local TIA names each candidate's module: a test of another package
// whose file base name and test name are the same is not skipped, while
// Datadog's candidates, which name no module, still match any.
func TestITRCandidatesMatchLocalTIAModule(t *testing.T) {
	state := &itrState{
		settings: &net.SettingsResponseData{ItrEnabled: true, TestsSkipping: true},
		response: &net.SkippableTestsResponse{Skippables: map[string]map[string][]net.SkippableResponseDataAttributes{
			"same_test.go": {
				"TestSame":    {{Suite: "same_test.go", Name: "TestSame", Module: "example.com/alpha"}},
				"TestBackend": {{Suite: "same_test.go", Name: "TestBackend"}},
			},
		}},
	}
	info := func(module, name string) *testingTInfo {
		return &testingTInfo{commonInfo: commonInfo{moduleName: module, suiteName: "same_test.go", testName: name}}
	}
	for _, tc := range []struct {
		module, name string
		skip         bool
	}{
		{"example.com/alpha", "TestSame", true},
		{"example.com/beta", "TestSame", false},
		{"example.com/alpha_test", "TestSame", false},
		{"example.com/beta", "TestBackend", true},
	} {
		if got := state.decisionFor(info(tc.module, tc.name), &testExecutionMetadata{}, false); got.skip != tc.skip {
			t.Errorf("%s %s: skip=%t, want %t", tc.module, tc.name, got.skip, tc.skip)
		}
	}
}
