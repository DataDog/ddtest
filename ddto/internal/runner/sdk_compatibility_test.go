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
	if warning, _ := preflightSDKMirror(pkg, nil); !strings.Contains(warning, "SDK span copies disabled") {
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

// An SDK with the copy hooks but not adoption's keeps copies, and the CLI
// warns that its spans under tests also go to APM, unless
// DD_CIVISIBILITY_APM_SPANS_ENABLED=true asks for that.
func TestSDKMirrorWithoutAdoptionWarns(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "instrument", "testdata", "sdkmirror", "sdk.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sdk.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	pkg := &goPackage{Dir: dir, GoFiles: []string{"sdk.go"}}
	t.Setenv("DD_CIVISIBILITY_APM_SPANS_ENABLED", "")
	if warning, apm := preflightSDKMirror(pkg, nil); warning != "" || !strings.Contains(apm, "SDK spans under tests also go to APM") {
		t.Fatalf("warning=%q apm=%q", warning, apm)
	}
	t.Setenv("DD_CIVISIBILITY_APM_SPANS_ENABLED", "true")
	if warning, apm := preflightSDKMirror(pkg, nil); warning != "" || apm != "" {
		t.Fatalf("warning=%q apm=%q with DD_CIVISIBILITY_APM_SPANS_ENABLED=true", warning, apm)
	}
}
