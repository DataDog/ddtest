package framework

import "testing"

func TestFileLevelSuiteBoundaries(t *testing.T) {
	for _, tc := range []struct {
		fw   Framework
		want bool
	}{
		{&RSpec{}, false}, {&Minitest{}, false}, {&JVM{}, false},
		{&PyTest{}, true}, {&Jest{}, true}, {&Vitest{}, true},
		{&Mocha{}, true}, {&Cucumber{}, true}, {&Cypress{}, true},
		{&Playwright{}, true}, {&WebdriverIO{}, true},
	} {
		t.Run(tc.fw.Name(), func(t *testing.T) {
			if got := tc.fw.HasFileLevelSuites(); got != tc.want {
				t.Fatalf("file-level suites = %t, want %t", got, tc.want)
			}
		})
	}
}
