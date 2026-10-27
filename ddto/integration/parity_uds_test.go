//go:build unix

package integration

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A short socket path avoids the Unix sockaddr limit even when the test's
// temporary root is deeply nested. Each invocation owns and removes its directory.
func TestCIVisibilityUnixAgent(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	bins := compileMiniPair(t, dir, driver)
	socketDir, err := os.MkdirTemp("/var/tmp", "ci-poc-uds-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "agent.sock")
	var captures []*parityReceiver
	var wallNS []int64
	for _, bin := range bins {
		receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}, policy: policySettings{Coverage: true}}
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: http.HandlerFunc(receiver.handler)}
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		scratch := t.TempDir()
		env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL=unix://"+socket, "DD_API_KEY=fixture", "TMPDIR="+scratch, "TMP="+scratch, "TEMP="+scratch, "XDG_CACHE_HOME="+scratch)
		out, stderr, code, wall := commandWithTiming(t, dir, env, bin, "-test.run=^TestPass$")
		server.Close()
		<-done
		if code != 0 {
			t.Fatalf("UDS execution: %d %s %s", code, out, stderr)
		}
		if len(receiver.failures) > 0 {
			t.Fatal(receiver.failures)
		}
		counts, err := countCIEvents(receiver.events)
		if err != nil {
			t.Fatal(err)
		}
		if counts != (eventCounts{1, 1, 1, 1, 0}) {
			t.Fatalf("UDS lost CI events: %+v\n%s", counts, stderr)
		}
		if err = validateEventGraph(receiver.events); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, receiver)
		wallNS = append(wallNS, wall.Nanoseconds())
	}
	assertMiniCIAttributes(t, captures[0].events, captures[1].events)
	writeParityEvidence(t, "uds", map[string]any{"timing": parityTiming{binaryTimingScope, wallNS[0], wallNS[1]}, "status": "passed", "sdk": eventCounts{1, 1, 1, 1, 0}, "mini": eventCounts{1, 1, 1, 1, 0}, "scope": "Unix Agent: settings and test-cycle delivery over a real socket"})
}
