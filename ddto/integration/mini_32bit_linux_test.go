//go:build linux && amd64

package integration

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// For a 32-bit target, ddto builds Mini's runtime and its overlays, and the
// test process reports as on a 64-bit one. Linux runs 386 executables on
// amd64. As for any cross-compilation, which a go_linux_386_exec launcher
// could run, the test process reports its own session.
func TestMini32BitTarget(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"sample_test.go": `package fixture_test

import "testing"

func TestPass(t *testing.T) {}

func TestFail(t *testing.T) { t.Fatal("fails on purpose") }
`})
	receiver := &capture{}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	env := testEnv("GOARCH=386", "DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
	// The panic hook and coverage add the runtime's and testing's overlays.
	out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "--panic-hook", "-cover", "-count=1", "-v", ".")
	if code != 1 || !strings.Contains(out, "fails on purpose") || !strings.Contains(out, "FAIL\texample.com/dd-ci-testing-fixture\t") {
		t.Fatalf("exit %d\n%s\n%s", code, out, stderr)
	}
	got := summarizeSessionEvents(receiver)
	if len(receiver.failures) != 0 || !reflect.DeepEqual(got.tests, []string{"TestFail fail", "TestPass pass"}) || !reflect.DeepEqual(got.sessions, []string{"fail"}) || got.modules != 1 || got.suites != 1 {
		t.Fatalf("tests %v, sessions %v, modules %d, suites %d, failures %v\n%s\n%s", got.tests, got.sessions, got.modules, got.suites, receiver.failures, out, stderr)
	}
	for _, event := range got.events {
		if event["type"] != "test" {
			continue
		}
		meta, _ := event["content"].(map[string]any)["meta"].(map[string]any)
		if arch := fmt.Sprint(meta["os.architecture"]); arch != "386" {
			t.Errorf("test %v: os.architecture %s", meta["test.name"], arch)
		}
	}
}
