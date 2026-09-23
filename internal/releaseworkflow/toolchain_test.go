package releaseworkflow

import (
	"os/exec"
	"testing"
)

func TestToolchainUpdater(t *testing.T) {
	cmd := exec.Command("python3", "-B", "-m", "unittest", "discover", "-s", "../../.github/scripts", "-p", "test_update_toolchain.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("toolchain updater tests failed: %v\n%s", err, output)
	}
}
