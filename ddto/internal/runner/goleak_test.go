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
		{"", []string{"."}, "-I=ddtest-goleak-hash", false},
		{"-gcflags=all=-N", []string{"-gcflags=go.uber.org/goleak=-l", "."}, "-l -I=ddtest-goleak-hash", false},
		{"'-gcflags=all=-N -l'", []string{"-gcflags=example.com/...=-B", "."}, "-N -l -I=ddtest-goleak-hash", false},
		{"-gcflags=-N", []string{"."}, "-I=ddtest-goleak-hash", false},
		{"", []string{"-gcflags=-N", "go.uber.org/goleak"}, "-N -I=ddtest-goleak-hash", true},
		{"", []string{"-gcflags=-N -l", pkg.Dir}, "-N -l -I=ddtest-goleak-hash", true},
		{"", []string{"-gcflags=./...=-N", "."}, "-I=ddtest-goleak-hash", false},
		{"", []string{"-gcflags=./vendor/go.uber.org/...=-N", "."}, "-N -I=ddtest-goleak-hash", false},
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
	for _, version := range []string{"v1.3.0", "v1.3.1", "v1.4.0", "v1.4.0-rc.1"} {
		if !supportedGoleakVersion(version) {
			t.Fatal(version)
		}
	}
	for _, version := range []string{"", "v1.2.1", "v1.3.0-rc.1", "v2.0.0", "v1.13"} {
		if supportedGoleakVersion(version) {
			t.Fatal(version)
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
		if err != nil || flag != "-gcflags=go.uber.org/goleak=-I=ddtest-goleak-hash" {
			t.Fatalf("%v: %q, %v", args, flag, err)
		}
	}
	opts, err := parseOptions([]string{"-gcflags=go.uber.org/...=-N -l", "./..."}, "")
	if err != nil {
		t.Fatal(err)
	}
	if flag, err := goleakCacheFlag(dir, opts, pkg, "hash"); err != nil || flag != "-gcflags=go.uber.org/goleak='-N' '-l' -I=ddtest-goleak-hash" && flag != "-gcflags=go.uber.org/goleak=-N -l -I=ddtest-goleak-hash" {
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

const goleakFindSource = "package goleak\ntype Option interface{}\nfunc Find(options ...Option) error { return nil }\n"
const goleakForkVersion = "v0.0.0-20260702071827-065a2facff42"

func goleakPackage(t *testing.T, version, source string) *goPackage {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leaks.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := &goPackage{Dir: dir, ImportPath: "go.uber.org/goleak", GoFiles: []string{"leaks.go"}}
	pkg.Module = &struct {
		Path, Version string
		Main          bool
		Replace       *struct{ Path, Dir, Version string }
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

func TestGoleakReplacementVersionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, required, path, version string
		want                          bool
	}{
		{"fork independent pseudo-version", "v1.3.0", "github.com/tonyredondo/goleak", goleakForkVersion, true},
		{"other fork independent version", "v1.3.0", "example.com/goleak", "v0.1.0", true},
		{"fork independent major", "v1.3.0", "example.com/goleak/v2", "v2.0.0", true},
		{"fork original too old", "v1.2.1", "example.com/goleak", "v1.3.0", false},
		{"fork unknown original", "", "example.com/goleak", "v1.3.0", false},
		{"fork unsupported original major", "v2.0.0", "example.com/goleak", "v1.3.0", false},
		{"same module downgrade", "v1.3.0", "go.uber.org/goleak", "v1.2.1", false},
		{"same module upgrade", "v1.2.1", "go.uber.org/goleak", "v1.3.0", true},
		{"local replacement", "v1.3.0", "../goleak", "", true},
		{"local old original", "v1.2.1", "../goleak", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg := goleakPackage(t, tc.required, goleakFindSource)
			pkg.Module.Replace = &struct{ Path, Dir, Version string }{Path: tc.path, Version: tc.version}
			entry, warning, err := prepareGoleak(pkg, nil, t.TempDir())
			if err != nil || (entry != nil) != tc.want || (warning == "") != tc.want {
				t.Fatalf("want supported=%t: entry=%v warning=%q err=%v", tc.want, entry, warning, err)
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
