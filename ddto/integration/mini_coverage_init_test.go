package integration

import (
	"net/http"
	"strings"
	"testing"
)

// ddto's testing overlay runs while testing initializes, before Mini. It sets
// Mini's variables instead of calling Mini: a Mini function that -coverpkg
// instruments and that runs before Mini initializes records its counters
// without a package, and Go then prints "internal error in coverage meta-data
// tracking" when it writes the profile.
func TestMiniCoverageOfMiniItselfHasNoCounterErrors(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"sample_test.go": `package fixture_test

import "testing"

func TestParallel(t *testing.T) { t.Parallel() }

func TestSerial(t *testing.T) {}
`})
	receiver := &capture{}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
	for _, pattern := range []string{"all", "github.com/tonyredondo/dd-ci-testing-poc/..."} {
		out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "-count=1", "-v", "-coverpkg="+pattern, ".")
		if code != 0 || !strings.Contains(out, "--- PASS: TestSerial") || strings.Contains(out+stderr, "internal error in coverage") {
			t.Fatalf("-coverpkg=%s: exit %d\n%s\n%s", pattern, code, out, stderr)
		}
	}
}
