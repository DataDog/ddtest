package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
)

// ddtoToolexec reports whether a go -x trace runs ddto's -toolexec: its
// wrapper executable, or ddto's own tool-overlay.
func ddtoToolexec(trace string) bool {
	return strings.Contains(trace, "tool-overlay") || strings.Contains(trace, toolwrap.Executable)
}

// ddtoToolexecMode reports whether a go -x trace runs ddto's -toolexec in mode.
func ddtoToolexecMode(trace, mode string) bool {
	for _, line := range strings.Split(trace, "\n") {
		if strings.Contains(line, "tool-overlay "+mode+" ") || strings.Contains(line, toolwrap.Executable) && strings.Contains(line, " "+mode+" ") {
			return true
		}
	}
	return false
}

// ddto builds its -toolexec wrapper executable once in the user cache and
// reuses it. Without a usable cache, ddto itself is the wrapper and the run
// is the same.
func TestToolWrapperLifecycle(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	cache := t.TempDir()
	unusable := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(unusable, nil, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(cache, wantState string) string {
		t.Helper()
		env := testEnv(append(isolatedUserCache(t, cache), "DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG=true")...)
		// -x shows the fixture's compile, whose directory is new, through
		// the -toolexec wrapper.
		out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "-count=1", "-x", "-coverpkg=testing,./...", "-covermode=atomic", ".")
		if code != 0 {
			t.Fatal(out, stderr)
		}
		logs := cliDebugLines(stderr)
		if !strings.Contains(logs, "tool wrapper state="+wantState) || !strings.Contains(logs, "tool selection testify=false goleak=false cover=true") {
			t.Fatalf("want tool wrapper state=%s:\n%s", wantState, logs)
		}
		var trace []string
		for _, line := range strings.Split(stderr, "\n") {
			if !strings.Contains(line, " DEBUG: ") {
				trace = append(trace, line)
			}
		}
		x := strings.Join(trace, "\n")
		if wrapped := strings.Contains(x, toolwrap.Executable); wrapped != (wantState != "unavailable") || !ddtoToolexecMode(x, "cover") {
			t.Fatalf("state %s, but the wrapper executable ran=%t:\n%s", wantState, wrapped, x)
		}
		return out
	}
	created := run(cache, "created")
	executable := filepath.Join(userCacheIn(cache), "ddto", "toolwraps")
	if entries, err := os.ReadDir(executable); err != nil || len(entries) == 0 {
		t.Fatalf("no wrapper in %s: %v", executable, err)
	}
	reused := run(cache, "reused")
	fallback := run(unusable, "unavailable")
	for _, out := range []string{created, reused, fallback} {
		if !strings.Contains(out, "ok ") || !strings.Contains(out, "coverage:") {
			t.Fatalf("unexpected output:\n%s", out)
		}
	}
}
