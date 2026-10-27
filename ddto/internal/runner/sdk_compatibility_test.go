package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsupportedSDKMirrorKeepsCIOwnershipGuards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.go")
	source := "package tracer\nimport \"context\"\ntype Span struct{}\nfunc ContextWithSpan(ctx context.Context, span *Span) context.Context { return context.WithValue(ctx, 1, span) }\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := &goPackage{Dir: dir, GoFiles: []string{"context.go"}}
	if warning := preflightSDKMirror(pkg, nil); !strings.Contains(warning, "SDK span copies disabled") {
		t.Fatal(warning)
	}
	plan := Plan{sdkCI: true, sdkMirrorDisabled: true, testify: true, goleak: true, coverOverlay: true}
	mode := plan.toolMode()
	if !ValidToolMode(mode) {
		t.Fatal("invalid selective mode", mode)
	}
	if ToolNeedsPlan(mode, []string{"compile", path}, sdkTracerPackage) {
		t.Fatal("unsupported mirror still selected")
	}
	for _, guard := range []string{sdkCIConfigPackage, sdkCIEnvironmentPackage} {
		if !ToolNeedsPlan(mode, []string{"compile", path}, guard) {
			t.Fatal("mandatory ownership guard disabled", guard)
		}
	}
	if sdkCompilerCacheMarker(sdkCIConfigPackage) == sdkCompilerCacheMarker(sdkTracerPackage) {
		t.Fatal("mirror modes share a compiler cache contract")
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != source {
		t.Fatal("preflight modified SDK sources", err)
	}
}
