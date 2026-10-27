package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	if got, err := crossExecHelper(cross, t.TempDir()); got != filepath.Join(dir, name) || err != nil {
		t.Fatalf("cross helper=%q, %v", got, err)
	}
	native := &goEnvironment{GOOS: "linux", GOARCH: "amd64", GOHOSTOS: "linux", GOHOSTARCH: "amd64"}
	if got, err := crossExecHelper(native, t.TempDir()); got != "" || err != nil {
		t.Fatalf("native build selected %q, %v", got, err)
	}
	// go test looks the helper up from its working directory. It runs test
	// binaries directly instead of a match for a relative PATH entry there,
	// unless GODEBUG=execerrdot=0 makes it run that match.
	work := t.TempDir()
	if err := os.Mkdir(filepath.Join(work, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "bin", name), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "bin"+string(os.PathListSeparator)+dir)
	if got, err := crossExecHelper(cross, work); got != "" || err != nil {
		t.Fatalf("relative match selected %q, %v", got, err)
	}
	if got, err := crossExecHelper(cross, t.TempDir()); got != filepath.Join(dir, name) || err != nil {
		t.Fatalf("cross helper=%q, %v", got, err)
	}
	t.Setenv("GODEBUG", "execerrdot=0")
	if _, err := crossExecHelper(cross, work); !errors.Is(err, errRelativeProgram) {
		t.Fatalf("a relative helper that go test runs: %v", err)
	}
	t.Setenv("GODEBUG", "execerrdot=0#y")
	if _, err := crossExecHelper(cross, work); !errors.Is(err, errUnknownProgram) {
		t.Fatalf("a relative helper that go test may run: %v", err)
	}
}

// writeProgram creates an executable named prog in dir.
func writeProgram(t *testing.T, dir string) string {
	t.Helper()
	name := "prog"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// lookPathFrom finds what exec.LookPath finds with dir as the current
// directory, while this process runs elsewhere: the same absolute path, the
// same refusal of a match relative to dir, or errRelativeProgram where
// GODEBUG=execerrdot=0 lets Go run such a match.
func TestLookPathFromMatchesLookPath(t *testing.T) {
	root := t.TempDir()
	from, elsewhere := filepath.Join(root, "from"), filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0700); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(root, "abs")
	sep := string(os.PathListSeparator)
	for _, tc := range []struct {
		name     string
		programs []string
		path     string
	}{
		{"absolute", []string{abs}, abs},
		{"missing", nil, abs},
		{"relative entry", []string{filepath.Join(from, "rel"), abs}, "rel" + sep + abs},
		{"relative entry without a match", []string{abs}, "rel" + sep + abs},
		{"empty entry", []string{from, abs}, sep + abs},
		{"current directory", []string{from, abs}, abs},
		{"current directory on PATH", []string{from}, from},
		{"dot", []string{from}, "."},
		{"dot, then the same directory", []string{from}, "." + sep + from},
		{"dot, then another directory", []string{from, abs}, "." + sep + abs},
		// Rooted on the current drive on Windows; absolute elsewhere.
		{"rooted entry", []string{abs}, abs[len(filepath.VolumeName(abs)):]},
		// Relative to the current drive's directory on Windows.
		{"drive-relative entry", []string{filepath.Join(from, "rel"), abs}, filepath.VolumeName(from) + "rel" + sep + abs},
	} {
		for _, godebug := range []string{"", "execerrdot=1", "execerrdot=0", "execerrdot=0,execerrdot=1"} {
			for _, noDot := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", tc.name, godebug, noDot), func(t *testing.T) {
					if err := os.RemoveAll(from); err != nil {
						t.Fatal(err)
					}
					if err := os.RemoveAll(abs); err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(from, 0700); err != nil {
						t.Fatal(err)
					}
					for _, dir := range tc.programs {
						writeProgram(t, dir)
					}
					t.Setenv("PATH", tc.path)
					t.Setenv("GODEBUG", godebug)
					if noDot {
						t.Setenv("NoDefaultCurrentDirectoryInExePath", "")
					} else {
						t.Setenv("NoDefaultCurrentDirectoryInExePath", "unset")
						os.Unsetenv("NoDefaultCurrentDirectoryInExePath")
					}
					t.Chdir(from)
					want, wantErr := exec.LookPath("prog")
					t.Chdir(elsewhere)
					got, err := lookPathFrom(from, "prog")
					switch {
					case wantErr == nil && !filepath.IsAbs(want):
						if !errors.Is(err, errRelativeProgram) {
							t.Fatalf("lookPathFrom = %q, %v; exec.LookPath from dir = %q", got, err, want)
						}
					case wantErr == nil:
						if err != nil || got != want {
							t.Fatalf("lookPathFrom = %q, %v; exec.LookPath from dir = %q", got, err, want)
						}
					default:
						for _, kind := range []error{exec.ErrDot, exec.ErrNotFound} {
							if errors.Is(err, kind) != errors.Is(wantErr, kind) {
								t.Fatalf("lookPathFrom = %q, %v; exec.LookPath from dir = %q, %v", got, err, want, wantErr)
							}
						}
					}
				})
			}
		}
	}
}

// A bare -exec program resolves as go test resolves it. One that go test
// cannot run, or runs relative to its directory, fails.
func TestResolveExecProgram(t *testing.T) {
	bin := t.TempDir()
	program := writeProgram(t, bin)
	dir := t.TempDir()
	writeProgram(t, filepath.Join(dir, "rel"))
	sep := string(os.PathListSeparator)
	for _, tc := range []struct {
		name    string
		path    string
		godebug string
		words   []string
		want    string
		err     error
	}{
		{"name", bin, "", []string{"prog", "-x"}, program, nil},
		{"missing", bin, "", []string{"missing"}, "", exec.ErrNotFound},
		{"relative to the directory", "rel" + sep + bin, "", []string{"prog"}, "", exec.ErrDot},
		{"run relative to the directory", "rel" + sep + bin, "execerrdot=0", []string{"prog"}, "", errRelativeProgram},
		{"maybe run relative to the directory", "rel" + sep + bin, "execerrdot=0#y", []string{"prog"}, "", errUnknownProgram},
		{"absolute with a GODEBUG pattern", bin, "execerrdot=0#y", []string{"prog"}, program, nil},
		{"path", bin, "", []string{filepath.Join("sub", "prog")}, filepath.Join("sub", "prog"), nil},
		{"empty name", bin, "", []string{""}, "", errNoCommand},
		{"dot", bin, "", []string{"."}, "", exec.ErrNotFound},
		{"dot dot", bin, "", []string{".."}, "", exec.ErrNotFound},
		{"root", program, "", []string{"/"}, "", errUnknownProgram},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
			t.Setenv("GODEBUG", tc.godebug)
			got, err := resolveExecProgram(dir, tc.words)
			if got != tc.want || !errors.Is(err, tc.err) || (err == nil) != (tc.err == nil) {
				t.Fatalf("resolveExecProgram = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
		})
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

// Windows forms that depend on a process's drives: a -exec path rooted on the
// current drive or relative to a drive's directory, and a PATH entry relative
// to another drive's directory, leave the run to go test.
func TestWindowsProgramForms(t *testing.T) {
	if runtime.GOOS != "windows" {
		return
	}
	dir := t.TempDir()
	volume := filepath.VolumeName(dir)
	other := "Z:"
	if strings.EqualFold(volume, other) {
		other = "Y:"
	}
	for _, path := range []string{`\executors\prog.exe`, `\`, volume + `prog.exe`, other + `prog.exe`} {
		if _, err := resolveExecProgram(dir, []string{path}); !errors.Is(err, errUnknownProgram) {
			t.Fatalf("resolveExecProgram(%q): %v", path, err)
		}
	}
	if got, err := resolveExecProgram(dir, []string{`sub\prog`}); err != nil || got != `sub\prog` {
		t.Fatalf(`resolveExecProgram(sub\prog) = %q, %v`, got, err)
	}
	// Go rejects . and .. before it searches; joined to a PATH entry, they
	// would otherwise find a program beside it with a PATHEXT extension.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tools"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tools.com"), nil, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "tools"))
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "")
	for _, name := range []string{".", ".."} {
		if _, err := lookPathFrom(t.TempDir(), name); !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("lookPathFrom(%q): %v", name, err)
		}
	}
	t.Setenv("PATH", other+"tools")
	if _, err := lookPathFrom(dir, "prog"); !errors.Is(err, errUnknownProgram) {
		t.Fatalf("lookPathFrom: %v", err)
	}
}
