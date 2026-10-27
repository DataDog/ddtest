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

// Go's module parser reads a use module's replacements only when its file can
// name Mini. The remaining queries still find a client's replacement of Mini.
func TestMiniWorkspaceQueriesOnlyModulesNamingMini(t *testing.T) {
	realGo, err := exec.LookPath("go")
	if err != nil || strings.Contains(realGo, "'") {
		t.Skipf("go cannot be wrapped: %q %v", realGo, err)
	}
	root := t.TempDir()
	files := map[string]string{
		"go.work":            "go 1.25.0\nuse (\n./client\n./helper-0\n./helper-1\n./replacing\n)\n",
		"client/go.mod":      "module example.com/client\ngo 1.25.0\n",
		"helper-0/go.mod":    "module example.com/helper0\ngo 1.25.0\n",
		"helper-1/go.mod":    "module example.com/helper1\ngo 1.25.0\nreplace example.com/other => ../other\n",
		"replacing/go.mod":   "module example.com/replacing\ngo 1.25.0\nreplace " + miniModule + " => ../mini\n",
		"mini/go.mod":        "module " + miniModule + "\ngo 1.25.0\n",
		"mini/testopt/x.go":  "package testopt\n",
		"other/go.mod":       "module example.com/other\ngo 1.25.0\n",
		"client/client.go":   "package client\n",
		"helper-0/helper.go": "package helper0\n",
	}
	for file, data := range files {
		path := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(t.TempDir(), "commands")
	t.Setenv("GOPROXY", "off")
	fakeGo(t, "if [ \"$1 $2 $3\" = 'mod edit -json' ]; then pwd >>'"+log+"'; fi\nexec '"+realGo+"' \"$@\"\n")
	work, err := provideMiniWorkspace(t.Context(), filepath.Join(root, "client"), filepath.Join(root, "go.work"), t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	queried, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := filepath.EvalSymlinks(strings.TrimSpace(string(queried))); err != nil || strings.Count(string(queried), "\n") != 1 || filepath.Base(got) != "replacing" {
		t.Fatalf("queried module directories: %q %v", queried, err)
	}
	data, err := os.ReadFile(work)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.Join(root, "mini")) {
		t.Fatalf("client replacement of Mini not used:\n%s", data)
	}
}
