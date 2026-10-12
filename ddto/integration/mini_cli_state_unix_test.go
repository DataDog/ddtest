//go:build unix

package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With per-test coverage, a test process takes its module from ddto's state
// instead of starting go list. A go command first in PATH records each call;
// Windows cannot run this script as go.
func TestMiniCLIStateGivesModuleToCoverage(t *testing.T) {
	dir, driver, _ := gitStateFixture(t)
	bin := filepath.Join(t.TempDir(), "coverage.test")
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-c", "-covermode=count", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %d %s %s", code, out, stderr)
	}
	module, stderr, code := command(t, dir, testEnv(), "go", "list", "-f", "{{.Module.Path}};{{.Module.Dir}}")
	if code != 0 {
		t.Fatalf("go list: %s", stderr)
	}
	modulePath, moduleDir, _ := strings.Cut(strings.TrimSpace(module), ";")

	shim, calls := t.TempDir(), filepath.Join(t.TempDir(), "go-calls")
	if err := os.WriteFile(filepath.Join(shim, "go"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+calls+"\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	intake := &featureIntake{coverage: true}
	server := newServer(http.HandlerFunc(intake.handler))
	defer server.Close()
	env := featureEnv(t, server.URL, "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The debug log says where the module came from.
	env = append(env, "DD_TRACE_DEBUG=true")
	var output string
	run := func(extra ...string) string {
		t.Helper()
		_ = os.Remove(calls)
		out, stderr, code := command(t, dir, append(env, extra...), bin, "-test.run=^TestPass$")
		if code != 0 {
			t.Fatalf("fixture: %d %s %s", code, out, stderr)
		}
		output = out + stderr
		data, _ := os.ReadFile(calls)
		return string(data)
	}

	if got := run(); !strings.Contains(got, "{{.Module.Path}};{{.Module.Dir}}") {
		t.Fatalf("alone, the test process did not ask go list for its module: %q", got)
	}
	if !strings.Contains(output, "civisibility.coverage: module from go list") {
		t.Fatalf("the debug log does not say that the module came from go list:\n%s", output)
	}

	state := runGitWorker(t, dir, driver, "", "DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=")
	tests := fmt.Sprintf(`{"packages":[{"dir":%q,"module_path":%q,"module_dir":%q}]}`, dir, modulePath, moduleDir)
	if err := os.WriteFile(filepath.Join(state, "tests.json"), []byte(tests), 0600); err != nil {
		t.Fatal(err)
	}
	if got := run("DDTO_GIT_STATE=" + state); strings.Contains(got, "list") {
		t.Fatalf("with ddto's state, the test process started go list: %q", got)
	}
	if !strings.Contains(output, "civisibility.coverage: module from ddto's test description [module:"+modulePath+"]") {
		t.Fatalf("the debug log does not say that the module came from ddto:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(state, "used")); err != nil {
		t.Fatalf("the test process did not use the state: %v", err)
	}
}
