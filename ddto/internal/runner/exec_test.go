package runner

import "testing"

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
