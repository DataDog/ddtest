package integration

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// Keep the declaration at line 5 and the closing brace at line 10. A compiler
// can eliminate the constant sum and place the entry PC on the closing brace.
const optimizedSourceTest = `package fixture

import "testing"

func TestSum(t *testing.T) {
	res := sum(4, 5)
	if res != 9 {
		t.Error("Expected 9, got ", res)
	}
}
`

func TestMiniCLIRuntimeSelectionAndSourceRange(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{
		"sample.go":      "package fixture\nfunc sum(a, b int) int { return a + b }\n",
		"sample_test.go": optimizedSourceTest,
	})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"default", []string{"test", "-count", "1"}},
		{"late-equals", []string{"test", "-count", "1", "--runtime=mini"}},
		{"double-count", []string{"test", "--count", "1", "--runtime=mini"}},
		{"separate", []string{"test", "-count=1", "--runtime", "mini"}},
		{"after-package", []string{"test", ".", "-count=1", "--runtime=mini"}},
		{"no-optimization", []string{"test", "-count=1", "-gcflags=-N -l"}},
		{"race", []string{"test", "-count=1", "-race"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}
			server := httptest.NewServer(http.HandlerFunc(receiver.handler))
			defer server.Close()
			env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL,
				"DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "DD_TRACE_DEBUG=true")
			out, stderr, code := command(t, dir, env, driver, append(append([]string(nil), tc.args...), "-v")...)
			if code != 0 {
				t.Fatalf("exit=%d\n%s\n%s", code, out, stderr)
			}
			logs := out + stderr
			if !strings.Contains(logs, version.RunLogPrefix+" DEBUG:") ||
				strings.Contains(logs, "Datadog Tracer") ||
				strings.Contains(logs, "TestOptimization Tracer") ||
				!strings.Contains(stderr, version.BuildLogPrefix+" DEBUG: ") {
				t.Fatalf("unexpected runtime/logger:\n%s\n%s", out, stderr)
			}
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.failures) != 0 {
				t.Fatal(receiver.failures)
			}
			count := 0
			for _, event := range receiver.events {
				if event["type"] != "test" {
					continue
				}
				content := event["content"].(map[string]any)
				meta := content["meta"].(map[string]any)
				metrics := content["metrics"].(map[string]any)
				if meta["test.name"] != "TestSum" {
					t.Fatalf("unexpected test %v", meta["test.name"])
				}
				count++
				if filepath.Base(meta["test.source.file"].(string)) != "sample_test.go" || metrics["test.source.start"] != float64(5) || metrics["test.source.end"] != float64(10) {
					t.Errorf("source=%v:%v-%v, want sample_test.go:5-10", meta["test.source.file"], metrics["test.source.start"], metrics["test.source.end"])
				}
				if strings.Contains(meta["test.command"].(string), "--runtime") {
					t.Error("runtime flag reached test binary")
				}
			}
			if count != 1 {
				t.Fatalf("got %d test events, want 1", count)
			}
		})
	}
}

func TestInvalidRuntimeFailsBeforeGo(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	for _, args := range [][]string{
		{"test", "--runtime"}, {"test", "--runtime="},
		{"test", "-count", "1", "--runtime=wrong"},
		{"test", ".", "--runtime", "wrong"},
	} {
		// No go executable is available: reaching preparation would produce a
		// different error. No module, compiler or network request is needed.
		out, stderr, code := command(t, t.TempDir(), testEnv("PATH="), driver, args...)
		if code != 2 || !strings.HasPrefix(stderr, version.BuildLogPrefix+" ERROR:") || !strings.Contains(stderr, "runtime") || strings.Contains(stderr, "executable file") || out != "" {
			t.Errorf("%q: exit=%d stdout=%q stderr=%q", args, code, out, stderr)
		}
	}
}
