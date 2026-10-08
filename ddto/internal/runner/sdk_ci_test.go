package runner

import (
	"os"
	"path/filepath"
	"reflect"
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
	args := []string{"compile", "-o", "output.a", original}
	got, cleanup, err := prepareSDKCICompile(args, sdkCIEnvironmentPackage)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !reflect.DeepEqual(args, []string{"compile", "-o", "output.a", original}) || !reflect.DeepEqual(got[:3], args[:3]) || got[3] == original {
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
