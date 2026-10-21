package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A command that runs no test, as with a -run filter that matches nothing,
// reports its session as skipped with test.session.empty_reason, as in
// dd-trace-go #5697. ddto's own session is empty only when every test process
// that joined it ran nothing.
func TestMiniEmptyTestSession(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	files := map[string]map[string]string{
		// Fuzz targets and examples create modules too, so they do not keep
		// the session from being empty.
		"alpha": {"alpha_test.go": "package alpha\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\nfunc TestAlpha(t *testing.T) {}\n\nfunc FuzzAlpha(f *testing.F) {\n\tf.Add(1)\n\tf.Fuzz(func(t *testing.T, n int) {})\n}\n\nfunc Example() {\n\tfmt.Println(\"alpha\")\n\t// Output: alpha\n}\n"},
		"beta":  {"beta_test.go": "package beta\n\nimport \"testing\"\n\nfunc TestBeta(t *testing.T) {}\n"},
	}
	for pkg, sources := range files {
		if err := os.MkdirAll(filepath.Join(dir, pkg), 0o700); err != nil {
			t.Fatal(err)
		}
		writeBuildFixture(t, filepath.Join(dir, pkg), sources)
	}
	receiver := &capture{}
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { receiver.handler(w, r) }))
	defer server.Close()
	for _, tc := range []struct {
		name          string
		args          []string
		tests         []string
		sessions      []string
		emptySessions int
		modules       int
	}{
		{name: "nothing ran", args: []string{"-run=^$", "./alpha", "./beta"}, sessions: []string{"skip"}, emptySessions: 1},
		{name: "one package ran", args: []string{"-run=^TestAlpha$", "./alpha", "./beta"}, tests: []string{"TestAlpha pass"}, sessions: []string{"pass"}, modules: 1},
		{name: "session per binary", args: []string{"--session-per-binary", "-run=^$", "./alpha", "./beta"}, sessions: []string{"skip", "skip"}, emptySessions: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver.mu.Lock()
			receiver.events, receiver.failures = nil, nil
			receiver.mu.Unlock()
			env := testEnv("DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
			out, stderr, code := command(t, dir, env, driver, append([]string{"test", "--runtime=mini", "-count=1"}, tc.args...)...)
			got := summarizeSessionEvents(receiver)
			emptySessions := 0
			for _, event := range got.events {
				content, _ := event["content"].(map[string]any)
				meta, _ := content["meta"].(map[string]any)
				if event["type"] != "test_session_end" {
					continue
				}
				reason, skipReason := meta["test.session.empty_reason"], meta["test.skip_reason"]
				switch {
				case reason == "zero_tests" && skipReason == "No tests were executed":
					emptySessions++
				case reason != nil || skipReason != nil:
					t.Errorf("session with empty reason %v and skip reason %v", reason, skipReason)
				}
			}
			if code != 0 || !reflect.DeepEqual(got.tests, tc.tests) || !reflect.DeepEqual(got.sessions, tc.sessions) || emptySessions != tc.emptySessions || got.modules != tc.modules {
				t.Fatalf("exit %d, tests %v, sessions %v, empty sessions %d, modules %d; want tests %v, sessions %v, empty sessions %d, modules %d\n%s", code, got.tests, got.sessions, emptySessions, got.modules, tc.tests, tc.sessions, tc.emptySessions, tc.modules, fmt.Sprint(out, stderr))
			}
		})
	}
}
