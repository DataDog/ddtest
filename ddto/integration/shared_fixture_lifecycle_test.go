package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real subprocesses exercise testing's cleanup and Fatal/Goexit behavior. The
// builder is small so these ownership checks do not compile another SDK binary.
func TestSharedParityFixtureLifetime(t *testing.T) {
	if os.Getenv("DDTO_SHARED_FIXTURE_PROBE") == "lifetime" {
		var fixture sharedParityFixture
		build := func(t *testing.T, tempDir func() string) *parityFixture {
			dir := tempDir()
			if err := os.WriteFile(filepath.Join(dir, "input.go"), []byte("package fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			return &parityFixture{dir: dir}
		}
		var first *parityFixture
		t.Run("normal", func(t *testing.T) { first = fixture.get(t, "probe", build) })
		t.Run("deferred", func(t *testing.T) {
			if got := fixture.get(t, "probe", build); got != first {
				t.Fatal("normal and deferred modes did not share their inputs")
			}
			if _, err := os.ReadFile(filepath.Join(first.dir, "input.go")); err != nil {
				t.Fatal("first consumer removed the shared inputs:", err)
			}
		})
		if err := os.WriteFile(os.Getenv("DDTO_SHARED_FIXTURE_PATH"), []byte(sharedParityRoot), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "workspace.txt")
	code, out := runSharedFixtureProbe(t, "lifetime", t.Name(), "DDTO_SHARED_FIXTURE_PATH="+path)
	if code != 0 {
		t.Fatal(out)
	}
	workspace, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(workspace)); !os.IsNotExist(err) {
		t.Fatalf("TestMain did not remove its shared workspace: %s (%v)", workspace, err)
	}
}

func TestSharedParityFixtureFailedBuild(t *testing.T) {
	if os.Getenv("DDTO_SHARED_FIXTURE_PROBE") == "failure" {
		var fixture sharedParityFixture
		build := func(t *testing.T, _ func() string) *parityFixture {
			t.Fatal("intentional fixture build failure")
			return nil
		}
		t.Run("first", func(t *testing.T) { fixture.get(t, "probe", build) })
		t.Run("second", func(t *testing.T) { fixture.get(t, "probe", build) })
		return
	}
	code, out := runSharedFixtureProbe(t, "failure", t.Name())
	if code != 1 || !strings.Contains(out, "intentional fixture build failure") || !strings.Contains(out, "fixture unavailable after a failed build") || strings.Contains(out, "panic:") {
		t.Fatalf("failed build was hidden, retried or panicked: exit %d\n%s", code, out)
	}
}

func runSharedFixtureProbe(t *testing.T, mode, name string, extra ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.v", "-test.run=^"+name+"$")
	cmd.Env = testEnv(append([]string{"DDTO_SHARED_FIXTURE_PROBE=" + mode}, extra...)...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	if err == nil {
		return 0, string(out)
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), string(out)
	}
	t.Fatal(err)
	return -1, string(out)
}
