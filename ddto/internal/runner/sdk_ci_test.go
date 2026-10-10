package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSDKCIGateDispatch(t *testing.T) {
	for _, mode := range []string{"mini-sdk", "mini-sdk-goleak", "mini-sdk-orchestrion", "mini-sdk-orchestrion-testify-goleak-cover"} {
		if !ToolNeedsPlan(mode, []string{"compile", "env.go"}, sdkCIEnvironmentPackage) {
			t.Fatal(mode)
		}
		if ToolNeedsPlan(mode, []string{"compile", "-V=full"}, sdkCIEnvironmentPackage) {
			t.Fatal("version probe altered")
		}
		if ToolNeedsPlan(mode, []string{"link", "env.go"}, sdkCIEnvironmentPackage) {
			t.Fatal("link selected")
		}
	}
	if ToolNeedsPlan("orchestrion", []string{"compile", "env.go"}, sdkCIEnvironmentPackage) {
		t.Fatal("SDK backend was gated")
	}
	if ToolNeedsPlan("mini-sdk", []string{"compile", "env.go"}, sdkCIEnvironmentPackage+"-other") {
		t.Fatal("wrong package gated")
	}
	// Go applies a package's gcflags, and so the cache marker, to its test
	// variants: each must reach the wrapper, which removes the marker.
	for _, pkg := range []string{sdkCIEnvironmentPackage, sdkCIConfigPackage, sdkTracerPackage} {
		for _, variant := range []string{pkg + " [" + pkg + ".test]", pkg + "_test [" + pkg + ".test]", pkg + ".test"} {
			if !ToolNeedsPlan("mini-sdk", []string{"compile", "file.go"}, variant) {
				t.Fatal("test variant bypassed:", variant)
			}
			if ToolNeedsPlan("mini-sdk", []string{"compile", "-V=full"}, variant) || ToolNeedsPlan("testify", []string{"compile", "file.go"}, variant) {
				t.Fatal("test variant selected without the SDK guard:", variant)
			}
			if got := ToolNeedsPlan("mini-sdk-nomirror", []string{"compile", "file.go"}, variant); got != (pkg != sdkTracerPackage) {
				t.Fatalf("%s without span copies: %v", variant, got)
			}
		}
	}
	for _, other := range []string{"example.com/client [" + sdkCIEnvironmentPackage + ".test]", sdkCIEnvironmentPackage + "/inner", sdkCIEnvironmentPackage + "_test/inner"} {
		if ToolNeedsPlan("mini-sdk", []string{"compile", "file.go"}, other) {
			t.Fatal("unrelated package gated:", other)
		}
	}
}

func TestSDKToolHelperProcess(t *testing.T) {
	if os.Getenv("DDTO_SDK_TOOL_HELPER") != "1" {
		return
	}
	args := os.Args[slices.Index(os.Args, "--")+1:]
	for _, arg := range args {
		fmt.Println(arg)
		if strings.HasSuffix(arg, ".go") {
			if data, err := os.ReadFile(arg); err == nil && strings.Contains(string(data), "return EnabledModeDisabled, false") {
				fmt.Println("gated")
			}
		}
	}
	os.Exit(0)
}

// Test variants of a guarded SDK package receive its compiler cache marker.
// The internal test variant compiles the package's own sources, which the
// guard rewrites as for the package itself; the external test package and the
// test main only lose the marker. None of them reaches the compiler with it.
func TestSDKCITestVariantsRemoveCompilerCacheMarker(t *testing.T) {
	t.Setenv("DDTO_SDK_TOOL_HELPER", "1")
	t.Setenv(userToolexecEnv, "")
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	source := filepath.Join(dir, "env.go")
	if err := os.WriteFile(source, []byte("package envconfig\ntype EnabledMode int\nconst EnabledModeDisabled=0\nfunc FromEnv()(EnabledMode,bool){return 1,true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	test := filepath.Join(dir, "env_test.go")
	if err := os.WriteFile(test, []byte("package envconfig\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := sdkCompilerCacheMarker(sdkCIEnvironmentPackage)
	tool := []string{os.Args[0], "-test.run=^TestSDKToolHelperProcess$", "--", "-I=client-path", marker}
	for _, tc := range []struct {
		importPath string
		inputs     []string
		gated      bool
	}{
		{sdkCIEnvironmentPackage + " [" + sdkCIEnvironmentPackage + ".test]", []string{source, test}, true},
		{sdkCIEnvironmentPackage + "_test [" + sdkCIEnvironmentPackage + ".test]", []string{test}, false},
		{sdkCIEnvironmentPackage + ".test", []string{test}, false},
	} {
		t.Setenv("TOOLEXEC_IMPORTPATH", tc.importPath)
		var stdout, stderr bytes.Buffer
		if code := RunTool(context.Background(), "missing-overlay", append(slices.Clone(tool), tc.inputs...), nil, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: exit=%d %s", tc.importPath, code, stderr.String())
		}
		got := strings.Fields(stdout.String())
		if slices.Contains(got, marker) || !slices.Contains(got, "-I=client-path") {
			t.Fatalf("%s: compiler arguments %q", tc.importPath, got)
		}
		if slices.Contains(got, "gated") != tc.gated || tc.gated && slices.Contains(got, source) || !slices.Contains(got, test) {
			t.Fatalf("%s: gated=%t, compiler arguments %q", tc.importPath, tc.gated, got)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 2 {
		t.Fatal("temporary compiler sources remain", entries, err)
	}
}

func TestSDKCIGateCompilerInputs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	original := filepath.Join(dir, "env.go")
	source := []byte("package envconfig\ntype EnabledMode int\nconst EnabledModeDisabled=0\nfunc FromEnv()(EnabledMode,bool){return 1,true}\n")
	if err := os.WriteFile(original, source, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"compile", "-o", "output.a", sdkCompilerCacheMarker(sdkCIEnvironmentPackage), original}
	got, cleanup, err := prepareSDKCICompile(args, sdkCIEnvironmentPackage)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !reflect.DeepEqual(args, []string{"compile", "-o", "output.a", sdkCompilerCacheMarker(sdkCIEnvironmentPackage), original}) || !reflect.DeepEqual(got[:3], args[:3]) || got[3] == original {
		t.Fatal(args, got)
	}
	rewritten, err := os.ReadFile(got[3])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(rewritten), "//line "+filepath.ToSlash(original)+":1:1\n") || !strings.Contains(string(rewritten), "return EnabledModeDisabled, false") {
		t.Fatalf("%s", rewritten)
	}
	unchanged, err := os.ReadFile(original)
	if err != nil || string(unchanged) != string(source) {
		t.Fatal("SDK original changed", err)
	}
	cleanup()
	if _, err := os.Stat(got[3]); !os.IsNotExist(err) {
		t.Fatal("temporary source retained", err)
	}
	if _, _, err := prepareSDKCICompile([]string{"compile", original, original}, sdkCIEnvironmentPackage); err == nil {
		t.Fatal("ambiguous sources accepted")
	}
	if _, _, err := prepareSDKCICompile([]string{"compile"}, sdkCIEnvironmentPackage); err == nil {
		t.Fatal("missing API accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("failure left temporary sources", entries, err)
	}
}

func TestSDKCompilerCacheMarkerKeepsUserFlags(t *testing.T) {
	t.Setenv("GOFLAGS", "-gcflags=all=-N")
	for _, path := range []string{sdkCIConfigPackage, sdkCIEnvironmentPackage, sdkTracerPackage} {
		opts, err := parseOptions([]string{"-gcflags=" + path + "=-l", "."}, os.Getenv("GOFLAGS"))
		if err != nil {
			t.Fatal(err)
		}
		marker := sdkCompilerCacheMarker(path)
		flag, err := packageCompilerCacheFlag(t.TempDir(), opts, &goPackage{ImportPath: path}, marker)
		if err != nil || flag != "-gcflags="+path+"=-l "+marker {
			t.Fatalf("%s: %q %v", path, flag, err)
		}
		args := []string{"compile", "-l", marker, "-I=client-path", "source.go"}
		got := removeCompilerCacheMarker(append([]string(nil), args...), marker)
		if !reflect.DeepEqual(got, []string{"compile", "-l", "-I=client-path", "source.go"}) || args[2] != marker {
			t.Fatal(got, args)
		}
	}
}
