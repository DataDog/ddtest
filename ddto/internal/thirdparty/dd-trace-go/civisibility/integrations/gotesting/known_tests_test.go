// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
)

// sdkMatchKnownTest is the SDK's isKnownTest lookup at the pinned base.
func sdkMatchKnownTest(knownTestsData *net.KnownTestsResponseData, moduleName, suiteName, name string) (bool, bool) {
	if knownTestsData != nil && len(knownTestsData.Tests) > 0 {
		if knownSuites, ok := knownTestsData.Tests[moduleName]; ok {
			if knownTests, ok := knownSuites[suiteName]; ok {
				return slices.Contains(knownTests, name), true
			}
		}
		return false, true
	}
	return false, false
}

func knownTestNamesFixture(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("TestSomething%d/case_%d", i/10, i%10)
	}
	return names
}

// Indexed lookups return the SDK's results, including whether there is any
// known-test data when the module or suite is missing.
func TestMatchKnownTestKeepsSDKResults(t *testing.T) {
	long := knownTestNamesFixture(knownTestScanLimit * 4)
	long = append(long, long[3], "")
	data := &net.KnownTestsResponseData{Tests: net.KnownTestsResponseDataModules{
		"mod": {
			"short_test.go": {"TestA", "TestB", "TestA"},
			"long_test.go":  long,
			"nil_test.go":   nil,
		},
		"other": {},
	}}
	cases := []struct {
		data                *net.KnownTestsResponseData
		module, suite, name string
	}{
		{nil, "mod", "short_test.go", "TestA"},
		{&net.KnownTestsResponseData{}, "mod", "short_test.go", "TestA"},
		{data, "mod", "short_test.go", "TestA"},
		{data, "mod", "short_test.go", "TestC"},
		{data, "mod", "long_test.go", long[0]},
		{data, "mod", "long_test.go", long[len(long)/2]},
		{data, "mod", "long_test.go", ""},
		{data, "mod", "long_test.go", "TestNew"},
		{data, "mod", "nil_test.go", "TestA"},
		{data, "mod", "missing_test.go", "TestA"},
		{data, "other", "short_test.go", "TestA"},
		{data, "missing", "short_test.go", "TestA"},
	}
	for _, tc := range cases {
		wantKnown, wantData := sdkMatchKnownTest(tc.data, tc.module, tc.suite, tc.name)
		for range 2 { // the second call uses the cached index
			if known, hasData := matchKnownTest(tc.data, tc.module, tc.suite, tc.name); known != wantKnown || hasData != wantData {
				t.Fatalf("%s/%s/%q: got (%t, %t), want (%t, %t)", tc.module, tc.suite, tc.name, known, hasData, wantKnown, wantData)
			}
		}
	}
}

// A new response has new lists, so a reload never reuses the old index.
func TestMatchKnownTestIndexFollowsNewResponse(t *testing.T) {
	first := knownTestNamesFixture(knownTestScanLimit + 1)
	second := knownTestNamesFixture(knownTestScanLimit + 1)
	second[0] = "TestReplaced"
	for _, tc := range []struct {
		list  []string
		name  string
		known bool
	}{
		{first, first[0], true},
		{second, first[0], false},
		{second, "TestReplaced", true},
		{first, "TestReplaced", false},
	} {
		data := &net.KnownTestsResponseData{Tests: net.KnownTestsResponseDataModules{"mod": {"a_test.go": tc.list}}}
		if known, _ := matchKnownTest(data, "mod", "a_test.go", tc.name); known != tc.known {
			t.Fatalf("%q: got %t, want %t", tc.name, known, tc.known)
		}
	}
}

// Instrumenting a test looks it up once: the known-test tag and Early Flake
// Detection reuse the result.
func TestLookupKnownTestReusesResult(t *testing.T) {
	calls := 0
	data := &net.KnownTestsResponseData{Tests: net.KnownTestsResponseDataModules{"mod": {"a_test.go": {"TestKnown"}}}}
	knownTests := func() *net.KnownTestsResponseData {
		calls++
		return data
	}
	info := &commonInfo{moduleName: "mod", suiteName: "a_test.go", testName: "TestNew"}
	for range 3 {
		if known, hasData := info.lookupKnownTest(knownTests); known || !hasData {
			t.Fatalf("got (%t, %t)", known, hasData)
		}
	}
	if calls != 1 {
		t.Fatalf("known tests read %d times", calls)
	}
}

func TestMatchKnownTestConcurrentIndex(t *testing.T) {
	list := knownTestNamesFixture(knownTestScanLimit * 8)
	data := &net.KnownTestsResponseData{Tests: net.KnownTestsResponseDataModules{"mod": {"a_test.go": list}}}
	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := list[worker*7%len(list)]
			if known, _ := matchKnownTest(data, "mod", "a_test.go", name); !known {
				t.Errorf("%q not known", name)
			}
		}()
	}
	wg.Wait()
}

// BenchmarkMatchKnownTest looks up a test that is not known, the common case
// for a new test, in a suite with n known names.
func BenchmarkMatchKnownTest(b *testing.B) {
	for _, n := range []int{10, 1000, 10000} {
		data := &net.KnownTestsResponseData{Tests: net.KnownTestsResponseDataModules{"mod": {"a_test.go": knownTestNamesFixture(n)}}}
		b.Run(fmt.Sprintf("sdk/names=%d", n), func(b *testing.B) {
			for b.Loop() {
				sdkMatchKnownTest(data, "mod", "a_test.go", "TestNew")
			}
		})
		b.Run(fmt.Sprintf("indexed/names=%d", n), func(b *testing.B) {
			for b.Loop() {
				matchKnownTest(data, "mod", "a_test.go", "TestNew")
			}
		})
	}
}
