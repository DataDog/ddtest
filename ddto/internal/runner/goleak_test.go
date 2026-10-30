package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoleakCacheFlagPreservesEffectiveCompilerFlags(t *testing.T) {
	pkg := &goPackage{Dir: filepath.Join(t.TempDir(), "vendor", "go.uber.org", "goleak"), ImportPath: "go.uber.org/goleak"}
	dir := filepath.Dir(filepath.Dir(filepath.Dir(pkg.Dir)))
	for _, tc := range []struct {
		env      string
		args     []string
		want     string
		selected bool
	}{
		{"", []string{"."}, "-I=ddto-goleak-hash", false},
		{"-gcflags=all=-N", []string{"-gcflags=go.uber.org/goleak=-l", "."}, "-l -I=ddto-goleak-hash", false},
		{"'-gcflags=all=-N -l'", []string{"-gcflags=example.com/...=-B", "."}, "-N -l -I=ddto-goleak-hash", false},
		{"-gcflags=-N", []string{"."}, "-I=ddto-goleak-hash", false},
		{"", []string{"-gcflags=-N", "go.uber.org/goleak"}, "-N -I=ddto-goleak-hash", true},
		{"", []string{"-gcflags=-N -l", pkg.Dir}, "-N -l -I=ddto-goleak-hash", true},
		{"", []string{"-gcflags=./...=-N", "."}, "-I=ddto-goleak-hash", false},
		{"", []string{"-gcflags=./vendor/go.uber.org/...=-N", "."}, "-N -I=ddto-goleak-hash", false},
	} {
		pkg.commandLine = tc.selected
		t.Setenv("GOFLAGS", tc.env)
		opts, err := parseOptions(tc.args, tc.env)
		if err != nil {
			t.Fatal(err)
		}
		flag, err := goleakCacheFlag(dir, opts, pkg, "hash")
		if err != nil || strings.TrimPrefix(flag, "-gcflags=go.uber.org/goleak=") != tc.want {
			t.Fatal(tc, flag, err)
		}
	}
}

func TestGoleakCacheFlagIgnoresRelativePatternsOutsideTheTree(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	pkg := &goPackage{Dir: filepath.Join(root, "gomodcache", "go.uber.org", "goleak@v1.3.0"), ImportPath: "go.uber.org/goleak"}
	for _, args := range [][]string{
		{"-gcflags=-N -l", "./..."},
		{"-gcflags=./...=-N -l", "./..."},
		{"-gcflags=.=-N -l", "./..."},
	} {
		opts, err := parseOptions(args, "")
		if err != nil {
			t.Fatal(err)
		}
		flag, err := goleakCacheFlag(dir, opts, pkg, "hash")
		if err != nil || flag != "-gcflags=go.uber.org/goleak=-I=ddto-goleak-hash" {
			t.Fatalf("%v: %q, %v", args, flag, err)
		}
	}
	opts, err := parseOptions([]string{"-gcflags=go.uber.org/...=-N -l", "./..."}, "")
	if err != nil {
		t.Fatal(err)
	}
	if flag, err := goleakCacheFlag(dir, opts, pkg, "hash"); err != nil || flag != "-gcflags=go.uber.org/goleak='-N' '-l' -I=ddto-goleak-hash" && flag != "-gcflags=go.uber.org/goleak=-N -l -I=ddto-goleak-hash" {
		t.Fatalf("import pattern lost: %q, %v", flag, err)
	}
}

func TestGoleakToolDispatch(t *testing.T) {
	args := []string{"compile", "file.go"}
	for _, mode := range []string{"goleak", "testify-goleak", "goleak-cover", "testify-goleak-cover"} {
		if !ToolNeedsPlan(mode, args, "go.uber.org/goleak") || ToolNeedsPlan(mode, args, "example.com/client") || ToolNeedsPlan(mode, []string{"compile", "-V=full"}, "go.uber.org/goleak") {
			t.Fatal(mode)
		}
	}
	if ToolNeedsPlan("testify-cover", args, "go.uber.org/goleak") {
		t.Fatal("goleak enabled without detection")
	}
}

const goleakFindSource = "package goleak\ntype Option interface{}\nfunc Find(options ...Option) error { return nil }\nfunc IgnoreAnyFunction(f string) Option { return nil }\n"

// goleak v1.2.1 has the same Find but no IgnoreAnyFunction, which the edit calls.
const goleakV121Source = "package goleak\ntype Option interface{}\nfunc Find(options ...Option) error { return nil }\nfunc IgnoreTopFunction(f string) Option { return nil }\n"
const goleakForkVersion = "v0.0.0-20260702071827-065a2facff42"

func goleakPackage(t *testing.T, version, source string) *goPackage {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leaks.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := &goPackage{Dir: dir, ImportPath: "go.uber.org/goleak", GoFiles: []string{"leaks.go"}}
	pkg.Module = &struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
	}{Path: "go.uber.org/goleak", Version: version}
	return pkg
}

func TestGoleakForkReplacement(t *testing.T) {
	pkg := goleakPackage(t, "v1.3.0", goleakFindSource)
	metadata := `{"Path":"go.uber.org/goleak","Version":"v1.3.0","Replace":{"Path":"github.com/tonyredondo/goleak","Version":"v0.0.0-20260702071827-065a2facff42"}}`
	if err := json.Unmarshal([]byte(metadata), &pkg.Module); err != nil {
		t.Fatal(err)
	}
	entry, warning, err := prepareGoleak(pkg, nil, t.TempDir())
	if err != nil || entry == nil || warning != "" {
		t.Fatalf("fork not instrumented: entry=%v warning=%q err=%v", entry, warning, err)
	}
}

func TestGoleakReplacementsFollowTheirCode(t *testing.T) {
	for _, tc := range []struct {
		name, required, path, version, source string
		want                                  bool
	}{
		{"fork independent pseudo-version", "v1.3.0", "github.com/tonyredondo/goleak", goleakForkVersion, goleakFindSource, true},
		{"fork independent major", "v1.3.0", "example.com/goleak/v2", "v2.0.0", goleakFindSource, true},
		{"fork of an older original", "v1.2.1", "example.com/goleak", "v1.3.0", goleakFindSource, true},
		{"fork of an unknown original", "", "example.com/goleak", "v1.3.0", goleakFindSource, true},
		{"same module upgrade", "v1.2.1", "go.uber.org/goleak", "v1.3.0", goleakFindSource, true},
		{"same module downgrade", "v1.3.0", "go.uber.org/goleak", "v1.2.1", goleakV121Source, false},
		{"local replacement without IgnoreAnyFunction", "v1.3.0", "../goleak", "", goleakV121Source, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg := goleakPackage(t, tc.required, tc.source)
			pkg.Module.Replace = &struct{ Path, Dir, Version string }{Path: tc.path, Version: tc.version}
			entry, warning, err := prepareGoleak(pkg, nil, t.TempDir())
			if err != nil || (entry != nil) != tc.want || (warning == "") != tc.want {
				t.Fatalf("want instrumented=%t: entry=%v warning=%q err=%v", tc.want, entry, warning, err)
			}
			if !tc.want && !strings.Contains(warning, "IgnoreAnyFunction") {
				t.Fatalf("warning does not name the missing API: %s", warning)
			}
		})
	}
}

func TestGoleakForkReplacementGuards(t *testing.T) {
	const incompatible = "package goleak\ntype Option interface{}\nfunc Find(options []Option) error {return nil}\n"
	for _, tc := range []struct {
		name, source string
		overlay      bool
	}{
		{"changed API", incompatible, false},
		{"missing entry", "package goleak\n", false},
		{"overlay changes API", incompatible, true},
		{"overlay removes entry", "package goleak\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.source
			if tc.overlay {
				source = goleakFindSource
			}
			pkg := goleakPackage(t, "v1.3.0", source)
			pkg.Module.Replace = &struct{ Path, Dir, Version string }{Path: "github.com/tonyredondo/goleak", Version: goleakForkVersion}
			var replacements map[string]string
			if tc.overlay {
				backing := filepath.Join(t.TempDir(), "leaks.go")
				if err := os.WriteFile(backing, []byte(tc.source), 0600); err != nil {
					t.Fatal(err)
				}
				replacements = map[string]string{filepath.Join(pkg.Dir, "leaks.go"): backing}
			}
			entry, warning, err := prepareGoleak(pkg, replacements, t.TempDir())
			if err != nil || entry != nil || !strings.Contains(warning, "unsupported library API") {
				t.Fatalf("API validation bypassed: entry=%v warning=%q err=%v", entry, warning, err)
			}
			if !strings.Contains(warning, "goleak v1.3.0 is not instrumented") || !strings.Contains(warning, "replacement github.com/tonyredondo/goleak "+goleakForkVersion) {
				t.Fatalf("warning omits replacement identity: %s", warning)
			}
		})
	}
	pkg := goleakPackage(t, "v1.3.0", goleakFindSource)
	pkg.Module.Replace = &struct{ Path, Dir, Version string }{Path: "github.com/tonyredondo/goleak", Version: goleakForkVersion}
	pkg.GoFiles = []string{"missing.go"}
	if _, _, err := prepareGoleak(pkg, nil, t.TempDir()); err == nil {
		t.Fatal("unreadable fork source was ignored")
	}
}
