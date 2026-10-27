package cli

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestHandles checks which process arguments a program that runs ddto as
// its instrument command hands to Main: that command, the modes in which
// ddto starts its own executable, and the go shim; not the program's other
// commands.
func TestHandles(t *testing.T) {
	bin := filepath.Join("bin", "ddtest")
	shim := filepath.Join("cache", "ddtest", "instrument", "go-shim", "0123456789abcdef", "go")
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{bin, "instrument", "go", "test", "./..."}, true},
		{[]string{bin, "instrument"}, true},
		{[]string{bin, "test-exec", "program"}, true},
		{[]string{bin, "go-shim-exec", "program"}, true},
		{[]string{bin, "ci-git-worker", "state"}, true},
		{[]string{bin, "tool-overlay", "mode", "plan", "compile"}, true},
		// Without the arguments of its mode, a name stays the program's.
		{[]string{bin, "test-exec"}, false},
		{[]string{bin, "go-shim-exec"}, false},
		{[]string{bin, "ci-git-worker"}, false},
		{[]string{bin, "ci-git-worker", "state", "id", "1m", "extra"}, false},
		{[]string{bin, "tool-overlay", "mode", "plan"}, false},
		{[]string{shim, "test", "-json", "./..."}, true},
		{[]string{bin, "plan"}, false},
		{[]string{bin, "run", "instrument"}, false},
		{[]string{bin}, false},
		{nil, false},
	} {
		if got := Handles(tc.args, "instrument"); got != tc.want {
			t.Errorf("Handles(%q) = %t, want %t", tc.args, got, tc.want)
		}
	}
	if runtime.GOOS == "windows" && !Handles([]string{shim + ".exe", "test"}, "instrument") {
		t.Error("the go shim's .exe is not handled")
	}
	// ddto's own executable has no command name: only its modes and the
	// shim are distinct from the commands that it runs.
	if !Handles([]string{"ddto", "ci-git-worker", "state"}, "") || Handles([]string{"ddto", "go", "test"}, "") {
		t.Error("ddto without a command name")
	}
}
