package version

import "testing"

func TestParseValid(t *testing.T) {
	tests := []struct {
		input              string
		expectedComponents []int
		expectedPreRelease string
		expectedBuildMeta  string
		expectedString     string
	}{
		// Standard semantic versioning
		{"1.23.4", []int{1, 23, 4}, "", "", "1.23.4"},
		{"1.0.0", []int{1, 0, 0}, "", "", "1.0.0"},
		{"0.9.8", []int{0, 9, 8}, "", "", "0.9.8"},
		{"2.1.3", []int{2, 1, 3}, "", "", "2.1.3"},

		// Pre-release versions
		{"1.0.0-alpha", []int{1, 0, 0}, "alpha", "", "1.0.0-alpha"},
		{"1.0.0-beta", []int{1, 0, 0}, "beta", "", "1.0.0-beta"},
		{"1.0.0-beta.2", []int{1, 0, 0}, "beta.2", "", "1.0.0-beta.2"},
		{"2.1.3-rc.1", []int{2, 1, 3}, "rc.1", "", "2.1.3-rc.1"},
		{"3.0.0-alpha.fah2345", []int{3, 0, 0}, "alpha.fah2345", "", "3.0.0-alpha.fah2345"},
		{"3.0-beta1", []int{3, 0}, "beta1", "", "3.0-beta1"},
		{"1.0.0-alpha.1.11.28", []int{1, 0, 0}, "alpha.1.11.28", "", "1.0.0-alpha.1.11.28"},

		// Build metadata
		{"1.0.0+20130313144700", []int{1, 0, 0}, "", "20130313144700", "1.0.0+20130313144700"},
		{"2.1.3+exp.sha.5114f85", []int{2, 1, 3}, "", "exp.sha.5114f85", "2.1.3+exp.sha.5114f85"},
		{"1.0.0+build.123", []int{1, 0, 0}, "", "build.123", "1.0.0+build.123"},

		// Pre-release with build metadata
		{"1.0.0-alpha+001", []int{1, 0, 0}, "alpha", "001", "1.0.0-alpha+001"},
		{"1.0.0-beta.2+exp.sha.5114f85", []int{1, 0, 0}, "beta.2", "exp.sha.5114f85", "1.0.0-beta.2+exp.sha.5114f85"},
		{"3.0.0-alpha.fah2345+build.123", []int{3, 0, 0}, "alpha.fah2345", "build.123", "3.0.0-alpha.fah2345+build.123"},

		// Date-based versions
		{"2025.11.12", []int{2025, 11, 12}, "", "", "2025.11.12"},
		{"2025.11.12.1", []int{2025, 11, 12, 1}, "", "", "2025.11.12.1"},
		{"2025.11.12-alpha", []int{2025, 11, 12}, "alpha", "", "2025.11.12-alpha"},

		// Short versions
		{"1.0", []int{1, 0}, "", "", "1.0"},
		{"2", []int{2}, "", "", "2"},
		{"10.5-rc1", []int{10, 5}, "rc1", "", "10.5-rc1"},

		// Complex examples
		{"1.0.0-x.7.z.92", []int{1, 0, 0}, "x.7.z.92", "", "1.0.0-x.7.z.92"},
		{"1.0.0-alpha.beta", []int{1, 0, 0}, "alpha.beta", "", "1.0.0-alpha.beta"},
		{"1.0.0-rc.1.2.3", []int{1, 0, 0}, "rc.1.2.3", "", "1.0.0-rc.1.2.3"},
		{"1.2.3-DEV-SNAPSHOT", []int{1, 2, 3}, "DEV-SNAPSHOT", "", "1.2.3-DEV-SNAPSHOT"},
		{"1.2.3-SNAPSHOT-123", []int{1, 2, 3}, "SNAPSHOT-123", "", "1.2.3-SNAPSHOT-123"},

		// Ruby/Rails style versions
		{"2.7.6", []int{2, 7, 6}, "", "", "2.7.6"},
		{"3.1.0", []int{3, 1, 0}, "", "", "3.1.0"},
		{"7.0.4.3", []int{7, 0, 4, 3}, "", "", "7.0.4.3"},

		// Python style versions
		{"3.10.0", []int{3, 10, 0}, "", "", "3.10.0"},
		{"3.11.5", []int{3, 11, 5}, "", "", "3.11.5"},
		{"2.7.18", []int{2, 7, 18}, "", "", "2.7.18"},

		// Node.js style versions
		{"18.12.1", []int{18, 12, 1}, "", "", "18.12.1"},
		{"20.0.0", []int{20, 0, 0}, "", "", "20.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.input, err)
			}

			if v.String() != tt.expectedString {
				t.Fatalf("expected String() to return %q, got %q", tt.expectedString, v.String())
			}

			actual := v.Components()
			if len(actual) != len(tt.expectedComponents) {
				t.Fatalf("expected %d components, got %d", len(tt.expectedComponents), len(actual))
			}

			for i, expected := range tt.expectedComponents {
				if actual[i] != expected {
					t.Fatalf("expected component %d to be %d, got %d", i, expected, actual[i])
				}
			}

			if v.PreRelease() != tt.expectedPreRelease {
				t.Fatalf("expected PreRelease() to return %q, got %q", tt.expectedPreRelease, v.PreRelease())
			}

			if v.BuildMeta() != tt.expectedBuildMeta {
				t.Fatalf("expected BuildMeta() to return %q, got %q", tt.expectedBuildMeta, v.BuildMeta())
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"1..2",
		"1.2.a",
		"1.-2",
		"1.0.0-",           // Empty pre-release
		"1.0.0+",           // Empty build metadata
		"1.0.0+build+",     // Trailing plus in build metadata
		"1.0.0-alpha@beta", // Invalid character in pre-release
		"1.0.0+build#123",  // Invalid character in build metadata
		"1.0.0-beta$1",     // Invalid character in pre-release
		".1.2",             // Leading dot
		"1.2.",             // Trailing dot
		"a.b.c",            // Non-numeric components
		"-1.0.0",           // Negative version
	}

	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(input); err == nil {
				t.Fatalf("expected Parse(%q) to fail", input)
			}
		})
	}
}

func TestIsValid(t *testing.T) {
	if !IsValid("1.0.0") {
		t.Fatal("expected 1.0.0 to be valid")
	}

	if IsValid("1.0.beta") {
		t.Fatal("expected 1.0.beta to be invalid")
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name     string
		v1       string
		v2       string
		expected int // -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2
	}{
		// Equal versions
		{"equal standard versions", "1.2.3", "1.2.3", 0},
		{"equal short versions", "1.0", "1.0", 0},
		{"equal single component", "2", "2", 0},
		{"equal with pre-release", "1.0.0-alpha", "1.0.0-alpha", 0},
		{"equal with build metadata", "1.0.0+build1", "1.0.0+build2", 0}, // Build metadata ignored
		{"equal pre-release and build", "1.0.0-beta+build1", "1.0.0-beta+build2", 0},

		// Major version differences
		{"major version less", "1.0.0", "2.0.0", -1},
		{"major version greater", "2.0.0", "1.0.0", 1},
		{"major version much greater", "10.0.0", "2.0.0", 1},

		// Minor version differences
		{"minor version less", "1.2.0", "1.3.0", -1},
		{"minor version greater", "1.3.0", "1.2.0", 1},
		{"minor version much greater", "1.10.0", "1.2.0", 1},

		// Patch version differences
		{"patch version less", "1.2.3", "1.2.4", -1},
		{"patch version greater", "1.2.4", "1.2.3", 1},
		{"patch version much greater", "1.2.10", "1.2.3", 1},

		// Short vs long versions
		{"short less than long", "1.2", "1.2.3", -1},
		{"long greater than short", "1.2.3", "1.2", 1},
		{"single equal to double with zero", "1", "1.0", 0},     // Missing components treated as 0
		{"double equal to single with zero", "1.0", "1", 0},     // Missing components treated as 0
		{"triple equal to double with zero", "1.0.0", "1.0", 0}, // Missing components treated as 0

		// Pre-release versions
		{"release greater than pre-release", "1.0.0", "1.0.0-alpha", 1},
		{"pre-release less than release", "1.0.0-alpha", "1.0.0", -1},
		{"pre-release alpha less than beta", "1.0.0-alpha", "1.0.0-beta", -1},
		{"pre-release beta greater than alpha", "1.0.0-beta", "1.0.0-alpha", 1},
		{"pre-release rc greater than beta", "1.0.0-rc", "1.0.0-beta", 1},
		{"pre-release with version less", "1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"pre-release with version greater", "1.0.0-beta.2", "1.0.0-beta.1", 1},
		{"pre-release complex less", "1.0.0-alpha.beta", "1.0.0-alpha.gamma", -1},

		// Build metadata (should be ignored)
		{"build metadata ignored equal", "1.0.0+build1", "1.0.0+build999", 0},
		{"build metadata ignored with pre-release", "1.0.0-alpha+b1", "1.0.0-alpha+b2", 0},

		// Date-based versions
		{"date versions less", "2025.11.12", "2025.11.13", -1},
		{"date versions greater", "2025.12.01", "2025.11.30", 1},
		{"date versions equal", "2025.11.12", "2025.11.12", 0},

		// Mixed length comparisons
		{"four component less", "7.0.4.3", "7.0.5.1", -1},
		{"four component greater", "7.0.5.0", "7.0.4.9", 1},
		{"three vs four components", "7.0.4", "7.0.4.1", -1},

		// Edge cases
		{"zero major less", "0.9.0", "1.0.0", -1},
		{"zero minor less", "1.0.0", "1.1.0", -1},
		{"large version numbers", "100.200.300", "100.200.301", -1},
		{"very different lengths", "1", "2.0.0.0", -1},

		// Real-world version comparisons
		{"ruby 2.7.6 vs 3.0.0", "2.7.6", "3.0.0", -1},
		{"python 3.10.0 vs 3.11.0", "3.10.0", "3.11.0", -1},
		{"node 18.12.1 vs 20.0.0", "18.12.1", "20.0.0", -1},
		{"rails 7.0.4.3 vs 7.0.5", "7.0.4.3", "7.0.5", -1},

		// Pre-release ordering
		{"alpha before beta before rc", "1.0.0-alpha", "1.0.0-beta", -1},
		{"beta before rc", "1.0.0-beta", "1.0.0-rc", -1},
		{"dev snapshot versions", "1.2.3-SNAPSHOT", "1.2.3", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v1 := MustParse(tt.v1)
			v2 := MustParse(tt.v2)

			result := v1.Compare(v2)

			if result != tt.expected {
				t.Fatalf("Compare(%q, %q) = %d, expected %d", tt.v1, tt.v2, result, tt.expected)
			}

			// Verify symmetry: if v1 < v2, then v2 > v1
			if tt.expected != 0 {
				reverseResult := v2.Compare(v1)
				expectedReverse := -tt.expected
				if reverseResult != expectedReverse {
					t.Fatalf("Symmetry check failed: Compare(%q, %q) = %d, but Compare(%q, %q) = %d (expected %d)",
						tt.v1, tt.v2, result, tt.v2, tt.v1, reverseResult, expectedReverse)
				}
			}
		})
	}
}

func TestCompareStrings(t *testing.T) {
	cmp, err := CompareStrings("1.2.3", "1.2.4")
	if err != nil {
		t.Fatalf("CompareStrings returned error: %v", err)
	}
	if cmp >= 0 {
		t.Fatal("expected 1.2.3 < 1.2.4")
	}

	if _, err := CompareStrings("1.2", "1.a"); err == nil {
		t.Fatal("expected invalid version comparison to fail")
	}
}
