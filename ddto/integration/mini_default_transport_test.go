package integration

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Tests often replace http.DefaultTransport, for example with a mock that
// fails on any request the test did not expect. Mini's events never go
// through it: the tests pass and report as under go test.
func TestMiniIgnoresAReplacedDefaultTransport(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"sample_test.go": `package fixture_test

import (
	"log"
	"net/http"
	"os"
	"testing"
)

type forbidden struct{}

func (forbidden) RoundTrip(r *http.Request) (*http.Response, error) {
	log.Fatalf("unexpected request to %s", r.URL)
	return nil, nil
}

func TestMain(m *testing.M) {
	http.DefaultTransport = forbidden{}
	os.Exit(m.Run())
}

func TestFirst(t *testing.T) {}
`})
	receiver := &capture{}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	env := testEnv("DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
	out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "-count=1", "-v", ".")
	if code != 0 || strings.Contains(out+stderr, "unexpected request") || !strings.Contains(out+stderr, "test-cycle: delivery uses Mini's own default transport") {
		t.Fatalf("exit %d\n%s\n%s", code, out, stderr)
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	var tests, sessions []string
	for _, event := range receiver.events {
		content, _ := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		switch event["type"] {
		case "test":
			tests = append(tests, fmt.Sprint(meta["test.name"]))
		case "test_session_end":
			sessions = append(sessions, fmt.Sprint(meta["test.status"]))
		}
	}
	if len(receiver.failures) != 0 || !reflect.DeepEqual(tests, []string{"TestFirst"}) || !reflect.DeepEqual(sessions, []string{"pass"}) {
		t.Fatalf("tests %v, sessions %v, failures %v\n%s\n%s", tests, sessions, receiver.failures, out, stderr)
	}
}
