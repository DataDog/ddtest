package runner

import (
	"slices"
	"testing"
)

func TestSupportedGoToolchains(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"go1.24.9", false},
		{"go1.25rc1", false},
		{"go1.25.0", true},
		{"go1.25.14", true},
		{"go1.26.0", true},
		{"go1.27.1", true},
		{"devel go1.28-7b536ca2f2ba linux/amd64", true},
		{"", false},
		{"unknown", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			if got := supportsGoToolchain(tc.version); got != tc.want {
				t.Fatalf("supportsGoToolchain(%q)=%t, want %t", tc.version, got, tc.want)
			}
		})
	}
}

func TestWorkspaceModuleFlags(t *testing.T) {
	for _, tc := range []struct{ flags, want []string }{
		{[]string{"-modfile=client.mod", "-mod=mod", "-race"}, []string{"-mod=readonly", "-race"}},
		{[]string{"--modfile", "client.mod", "--mod", "mod", "-race"}, []string{"--mod", "readonly", "-race"}},
		{[]string{"-mod=vendor", "-gcflags=all=-N -l"}, []string{"-mod=vendor", "-gcflags=all=-N -l"}},
	} {
		got := workspaceModuleFlags(tc.flags)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("workspace flags: %v", got)
		}
	}
}
