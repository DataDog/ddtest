package runner

import (
	"os"
	"os/exec"
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

// The Git worker requests with the test binaries' runtime version, which
// includes the experiments that GOEXPERIMENT sets: ddto predicts what a binary
// that the selected toolchain links reports.
func TestTestRuntimeVersionMatchesBinaries(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":  "module example.com/probe\n\ngo 1.25\n",
		"main.go": "package main\n\nimport (\n\t\"fmt\"\n\t\"runtime\"\n)\n\nfunc main() { fmt.Print(runtime.Version()) }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, experiment := range []string{"unset", "", "cgocheck2"} {
		t.Run("GOEXPERIMENT="+experiment, func(t *testing.T) {
			t.Setenv("GOFLAGS", "")
			t.Setenv("GOEXPERIMENT", experiment)
			if experiment == "unset" {
				if err := os.Unsetenv("GOEXPERIMENT"); err != nil {
					t.Fatal(err)
				}
			}
			environment, err := readGoEnvironment(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "run", ".")
			cmd.Dir = dir
			want, err := cmd.Output()
			if err != nil {
				t.Fatalf("go run: %v", err)
			}
			if got := testRuntimeVersion(t.Context(), dir, environment.GOVERSION, environment.GOEXPERIMENT); got != string(want) {
				t.Fatalf("predicted %q; the binary reports %q (go env: %q, %q)", got, want, environment.GOVERSION, environment.GOEXPERIMENT)
			}
		})
	}
}

// The linker's separator before the experiments: the go command's version
// shows it; otherwise it depends on the Go version (go.dev/issue/75953).
func TestExperimentSeparator(t *testing.T) {
	for _, tc := range []struct{ goVersion, base, want string }{
		{"go1.27.0-X:nodwarf5", "go1.27.0", "-"},
		{"go1.25.4 X:nodwarf5", "go1.25.4", " "},
		{"go1.27.0", "go1.27.0", "-"},
		{"go1.26rc1", "go1.26rc1", "-"},
		{"go1.25.4", "go1.25.4", " "},
		{"devel go1.28-abcdef Mon Oct 1 00:00:00 2026 +0000", "devel go1.28-abcdef Mon Oct 1 00:00:00 2026 +0000", " "},
	} {
		if got := experimentSeparator(tc.goVersion, tc.base); got != tc.want {
			t.Errorf("experimentSeparator(%q, %q) = %q, want %q", tc.goVersion, tc.base, got, tc.want)
		}
	}
	for value, want := range map[string][2]string{
		"go1.27.0-X:nodwarf5":         {"go1.27.0", "nodwarf5"},
		"go1.25.4 X:cgocheck2,boring": {"go1.25.4", "cgocheck2,boring"},
		"go1.27.0":                    {"go1.27.0", ""},
	} {
		if base, experiments, _ := cutExperiments(value); base != want[0] || experiments != want[1] {
			t.Errorf("cutExperiments(%q) = %q, %q", value, base, experiments)
		}
	}
}
