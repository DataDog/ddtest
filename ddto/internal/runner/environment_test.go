package runner

import (
	"os"
	"path/filepath"
	"runtime"
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

// go test runs cross-compiled test binaries through go_$GOOS_$GOARCH_exec from
// PATH when no -exec is given; ddto must wrap that program too.
func TestCrossExecHelperMatchesGoTest(t *testing.T) {
	dir := t.TempDir()
	name := "go_js_wasm_exec"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	cross := &goEnvironment{GOOS: "js", GOARCH: "wasm", GOHOSTOS: "linux", GOHOSTARCH: "amd64"}
	if got := crossExecHelper(cross); got != filepath.Join(dir, name) {
		t.Fatalf("cross helper=%q", got)
	}
	native := &goEnvironment{GOOS: "linux", GOARCH: "amd64", GOHOSTOS: "linux", GOHOSTARCH: "amd64"}
	if got := crossExecHelper(native); got != "" {
		t.Fatalf("native build selected %q", got)
	}
}

// ddto's -exec wrapper replaces the caller's -exec, in any position.
func TestGoTestArgumentsReplaceExec(t *testing.T) {
	opts, err := parseOptions([]string{"-exec", "user wrapper", "-v", "-exec=other", "."}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := goTestArguments(Plan{File: "plan.json", execWrapper: "'ddto' 'test-exec' 'other'"}, opts, "")
	want := []string{"test", "-overlay=plan.json", "-exec='ddto' 'test-exec' 'other'", "-v", "."}
	if !slices.Equal(got, want) {
		t.Fatalf("arguments:\n%q\nwant:\n%q", got, want)
	}
	if opts.exec != "other" {
		t.Fatalf("last -exec=%q", opts.exec)
	}
}
