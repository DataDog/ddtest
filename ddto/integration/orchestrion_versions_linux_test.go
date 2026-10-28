//go:build linux

package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// Each supported Orchestrion version composes with ddtest instrument: Mini
// stays the only test reporter, and SDK spans under tests keep to Test
// Optimization, with the pinned SDK. The deeper Orchestrion tests use the
// pinned reference, ORCHESTRION_BIN. Orchestrion is not edited, so every
// version runs; it runs on Linux only to bound the suite's time.
func TestMiniOrchestrionSupportedVersions(t *testing.T) {
	window := loadSupportedVersions(t).Orchestrion
	for _, version := range window.versions() {
		t.Run(version, func(t *testing.T) {
			bin := buildOrchestrion(t, window.Module, version)
			dir, driver := prepareMiniFixture(t)
			if err := os.WriteFile(filepath.Join(dir, "sdk_context_test.go"), []byte(sdkMirrorContextSource), 0600); err != nil {
				t.Fatal(err)
			}
			if out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-require=go.uber.org/goleak@v1.3.0"); code != 0 {
				t.Fatal(out, stderr)
			}
			configureOrchestrionFixture(t, dir, version, bin)
			test := filepath.Join(t.TempDir(), executableName("context.test"))
			out, stderr, code := orchestrionBuild(t, dir, testEnv(), driver, "test", "-mod=mod", "-c", "-o", test, "-toolexec="+quoteToolArgument(t, bin)+" toolexec", ".")
			if code != 0 {
				t.Fatal(out, stderr)
			}
			for _, apmSpans := range []bool{false, true} {
				events, apm := runSDKMirror(t, dir, test, "TestSDKMirrorScopes", false, true, sdkMirrorMode(apmSpans))
				assertSDKMirrorScopes(t, events, apm, apmSpans)
				assertSDKMirrorHTTP(t, dir, test, false, apmSpans)
			}
		})
	}
}

// buildOrchestrion builds Orchestrion version in a module of its own, with
// an x/tools that reads Go 1.27.2's export data.
func buildOrchestrion(t *testing.T, module, version string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/orchestrionbuild\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	get := func(target string) {
		t.Helper()
		if out, stderr, code := command(t, dir, testEnv(), "go", "get", target); code != 0 {
			t.Fatal(out, stderr)
		}
	}
	get(module + "@" + version)
	if raiseOrchestrionTools(t, dir) {
		get("golang.org/x/tools@" + orchestrionToolsVersion)
	}
	bin := filepath.Join(dir, executableName("orchestrion"))
	if out, stderr, code := orchestrionBuild(t, dir, testEnv(), "go", "build", "-o", bin, module); code != 0 {
		t.Fatal(out, stderr)
	}
	return bin
}
