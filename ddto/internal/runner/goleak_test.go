package runner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGoleakCacheFlagPreservesEffectiveCompilerFlags(t *testing.T) {
	pkg := &goPackage{Dir: filepath.Join(t.TempDir(), "vendor", "go.uber.org", "goleak"), ImportPath: "go.uber.org/goleak"}
	dir := filepath.Dir(filepath.Dir(filepath.Dir(pkg.Dir)))
	for _, tc := range []struct {
		env  string
		args []string
		want string
	}{
		{"", []string{"."}, "-I=ddtest-goleak-hash"},
		{"-gcflags=all=-N", []string{"-gcflags=go.uber.org/goleak=-l", "."}, "-l -I=ddtest-goleak-hash"},
		{"'-gcflags=all=-N -l'", []string{"-gcflags=example.com/...=-B", "."}, "-N -l -I=ddtest-goleak-hash"},
		{"-gcflags=-N", []string{"."}, "-I=ddtest-goleak-hash"},
		{"", []string{"-gcflags=-N", "go.uber.org/goleak"}, "-N -I=ddtest-goleak-hash"},
		{"", []string{"-gcflags=./...=-N", "."}, "-I=ddtest-goleak-hash"},
		{"", []string{"-gcflags=./vendor/go.uber.org/...=-N", "."}, "-N -I=ddtest-goleak-hash"},
	} {
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
