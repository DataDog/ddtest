package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyModuleFileUsesOverlayContentsAndDeletion(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum", "alternate.mod", "alternate.sum"} {
		t.Run(name, func(t *testing.T) {
			logical, backing, output := filepath.Join(dir, name), filepath.Join(dir, name+".backing"), filepath.Join(dir, name+".copy")
			for path, contents := range map[string]string{logical: "physical", backing: "overlaid"} {
				if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := copyModuleFile(logical, output, map[string]string{logical: backing}); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(output); err != nil || string(data) != "overlaid" {
				t.Fatalf("copied %q, %v", data, err)
			}
			if err := copyModuleFile(logical, output, map[string]string{logical: ""}); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deleted overlay: %v", err)
			}
		})
	}
}

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
