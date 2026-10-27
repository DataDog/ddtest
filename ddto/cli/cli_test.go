package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestHandles checks which process arguments a program that runs ddto as
// its instrument command hands to Main: that command, also when ddto runs
// its own modes again, and the go shim that ddto made of the executable;
// not the program's other commands, the names of ddto's modes alone, or
// another executable named go.
func TestHandles(t *testing.T) {
	bin := filepath.Join("bin", "ddtest")
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{bin, "instrument", "go", "test", "./..."}, true},
		{[]string{bin, "instrument"}, true},
		{[]string{bin, "instrument", "test-exec", "program"}, true},
		{[]string{bin, "instrument", "tool-overlay", "mode", "plan", "compile"}, true},
		{[]string{bin, "test-exec", "program"}, false},
		{[]string{bin, "go-shim-exec", "program"}, false},
		{[]string{bin, "ci-git-worker", "state"}, false},
		{[]string{bin, "tool-overlay", "mode", "plan", "compile"}, false},
		{[]string{bin, "plan"}, false},
		{[]string{bin, "run", "instrument"}, false},
		{[]string{bin}, false},
		{nil, false},
	} {
		if got := Handles(tc.args, "instrument"); got != tc.want {
			t.Errorf("Handles(%q) = %t, want %t", tc.args, got, tc.want)
		}
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	link := func(dir string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(dir, "go")
		if runtime.GOOS == "windows" {
			name += ".exe"
			err = os.Link(self, name)
		} else {
			err = os.Symlink(self, name)
		}
		if err != nil {
			t.Skip("cannot link the test executable:", err)
		}
		return name
	}
	// The go shim that ddto makes of this executable, as gotestsum runs it.
	shim := link(filepath.Join(t.TempDir(), "ddtest", "instrument", "go-shim", "0123456789abcdef"))
	if !Handles([]string{shim, "test", "-json", "./..."}, "instrument") {
		t.Error("the go shim is not handled")
	}
	// A link of the program named go elsewhere keeps the program's commands.
	other := link(filepath.Join(t.TempDir(), "bin"))
	if Handles([]string{other, "plan"}, "instrument") {
		t.Error("an executable named go outside the go shim directories is handled")
	}
	// Run by name, go is the one that PATH finds first, as its caller found it.
	t.Setenv("PATH", filepath.Dir(shim))
	if !Handles([]string{"go", "test"}, "instrument") {
		t.Error("the go shim that PATH finds is not handled")
	}
	t.Setenv("PATH", filepath.Dir(other))
	if Handles([]string{"go", "plan"}, "instrument") {
		t.Error("another go that PATH finds is handled")
	}
}
