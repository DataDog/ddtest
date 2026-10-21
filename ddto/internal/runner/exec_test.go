package runner

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// The tail of a Windows command line splits into the arguments after the
// first, as Go splits os.Args.
func TestCommandLineTail(t *testing.T) {
	for _, tc := range []struct{ cmd, want string }{
		{`ddto go build ./...`, `go build ./...`},
		{`  ddto.exe` + "\t\t" + `script.cmd "one&echo.INJECTED"`, `script.cmd "one&echo.INJECTED"`},
		{`"C:\Program Files\ddto.exe" go build`, `go build`},
		{`"C:\x\ddto.exe"script.cmd x`, `x`},
		{`"" go`, `go`},
		{`"a""b c" d`, `c" d`},
		{`a\"b c`, `c`},
		{`"a\\" b`, `b`},
		{`"a\" b" c`, `c`},
		{`"a b`, ``},
		{`ddto`, ``},
		{``, ``},
	} {
		if got := commandLineTail(tc.cmd); got != tc.want {
			t.Errorf("commandLineTail(%q) = %q; want %q", tc.cmd, got, tc.want)
		}
	}
}

func TestNestGoCommand(t *testing.T) {
	t.Setenv(nestedGoVariable, "")
	for depth := 1; depth <= maxNestedGo; depth++ {
		if err := NestGoCommand(); err != nil {
			t.Fatalf("depth %d: %v", depth, err)
		}
		if got := os.Getenv(nestedGoVariable); got != strconv.Itoa(depth) {
			t.Fatalf("%s=%q; want %d", nestedGoVariable, got, depth)
		}
	}
	if err := NestGoCommand(); err == nil || !strings.Contains(err.Error(), "starts ddto again") {
		t.Fatalf("past %d nested ddto commands: %v", maxNestedGo, err)
	}
	for args, want := range map[string]bool{"go build": true, "/usr/local/go/bin/go version": true, "go.exe env": true, "gotestsum --": false, "": false} {
		if got := IsGoCommand(strings.Fields(args)); got != want {
			t.Errorf("IsGoCommand(%q) = %t; want %t", args, got, want)
		}
	}
}
