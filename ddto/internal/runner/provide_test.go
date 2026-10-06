package runner

import "testing"

// A Windows checkout can convert go.mod to CRLF line endings.
func TestModulePathToleratesLineEndingsAndComments(t *testing.T) {
	for _, data := range []string{
		"module " + miniModule + "\n\ngo 1.26.0\n",
		"module " + miniModule + "\r\n\r\ngo 1.26.0\r\n",
		"// comment\r\nmodule \"" + miniModule + "\"\r\n",
	} {
		if got := modulePath([]byte(data)); got != miniModule {
			t.Errorf("modulePath(%q) = %q", data, got)
		}
	}
	if got := modulePath([]byte("go 1.26.0\n")); got != "" {
		t.Errorf("module path without a directive: %q", got)
	}
}
