//go:build !windows

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Preparation already read GOMODCACHE with the rest of Go's environment. The
// temporary workspace must not start another go env to find Mini's sources.
func TestMiniWorkspaceReusesPreparedModuleCache(t *testing.T) {
	realGo, err := exec.LookPath("go")
	if err != nil || strings.Contains(realGo, "'") {
		t.Skipf("go cannot be wrapped: %q %v", realGo, err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "client"), 0700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string]string{
		"go.work":       "go 1.25.0\nuse ./client\n",
		"client/go.mod": "module example.com/client\ngo 1.21\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOPROXY", "off")
	fakeGo(t, "if [ \"$1\" = env ]; then echo 'unexpected go env' >&2; exit 1; fi\nexec '"+realGo+"' \"$@\"\n")
	if _, err := provideMiniWorkspace(t.Context(), filepath.Join(root, "client"), filepath.Join(root, "go.work"), t.TempDir(), "", nil); err != nil {
		t.Fatal(err)
	}
}
