//go:build linux

package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A real switch: GOTOOLCHAIN selects another toolchain, downloaded into a
// module cache. CI downloads it before the tests.
func TestMiniGOROOTMirrorToolchainSwitch(t *testing.T) {
	version, modcache := os.Getenv("DDTO_TEST_SWITCH_TOOLCHAIN"), os.Getenv("DDTO_TEST_SWITCH_GOMODCACHE")
	if version == "" || modcache == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("CI downloads a toolchain for DDTO_TEST_SWITCH_TOOLCHAIN into DDTO_TEST_SWITCH_GOMODCACHE")
		}
		t.Skip("set DDTO_TEST_SWITCH_TOOLCHAIN and DDTO_TEST_SWITCH_GOMODCACHE to a downloaded toolchain")
	}
	cache := t.TempDir()
	env := testEnv(append(isolatedUserCache(t, cache), "GOMODCACHE="+modcache, "GOTOOLCHAIN="+version, "GOPROXY=off")...)
	dir := gorootMirrorFixtureDir(t)
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) == version {
		t.Fatalf("the running toolchain is already %s: no switch", version)
	}
	runMirroredToolchain(t, dir, env, "go", cache, modcache)
}
