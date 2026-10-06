package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestCIVisibilityMultiplePackages(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	var captures []*parityReceiver
	var wallNS []int64
	for _, backend := range []string{"sdk", "mini"} {
		receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}
		server := httptest.NewServer(http.HandlerFunc(receiver.handler))
		scratch := t.TempDir()
		env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "TMPDIR="+scratch, "TMP="+scratch, "TEMP="+scratch, "XDG_CACHE_HOME="+scratch)
		out, stderr, code, wall := commandWithTiming(t, dir, env, driver, "test", "--runtime="+backend, "-count=1", "-run=^Test(Pass|Other)$", "./...")
		server.Close()
		if code != 0 {
			t.Fatalf("multiple packages %s: %d %s %s", backend, code, out, stderr)
		}
		if len(receiver.failures) > 0 {
			t.Fatal(receiver.failures)
		}
		counts, err := countCIEvents(receiver.events)
		if err != nil {
			t.Fatal(err)
		}
		if counts != (eventCounts{Sessions: 2, Modules: 2, Suites: 2, Tests: 2}) {
			t.Fatalf("multiple package counts: %+v", counts)
		}
		if err = validateEventGraph(receiver.events); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, receiver)
		wallNS = append(wallNS, wall.Nanoseconds())
	}
	// Each binary has its own command. Both backends used identical Go args.
	assertMiniCIAttributes(t, captures[0].events, captures[1].events)
	writeParityEvidence(t, "packages", map[string]any{"timing": parityTiming{"CLI: preparation, compilation, execution and shutdown/flush", wallNS[0], wallNS[1]}, "status": "passed", "sdk": eventCounts{2, 2, 2, 2, 0}, "mini": eventCounts{2, 2, 2, 2, 0}, "scope": "one session per package binary; packages without tests emit no events"})
}

func TestCIVisibilityFuzzCampaign(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	bins := compileMiniPair(t, dir, driver)
	// A fixed iteration budget and one worker bound the experiment. The cache
	// path is identical so test.command is comparable, and this is not a timing test.
	cache := filepath.Join(t.TempDir(), "fuzz-cache")
	tc := parityCase{Args: []string{"-test.run=^$", "-test.fuzz=FuzzAdd", "-test.fuzztime=1x", "-test.parallel=1", "-test.fuzzcachedir=" + cache}}
	captures := []*parityReceiver{}
	var wallNS []int64
	for _, bin := range bins {
		receiver, result := runParityCase(t, dir, bin, tc)
		if result.code != 0 {
			t.Fatalf("fuzz execution: %s %s", result.out, result.stderr)
		}
		captures = append(captures, receiver)
		wallNS = append(wallNS, result.wall.Nanoseconds())
	}
	assertMiniCIAttributes(t, captures[0].events, captures[1].events)
	sdk, err := countCIEvents(captures[0].events)
	if err != nil {
		t.Fatal(err)
	}
	mini, err := countCIEvents(captures[1].events)
	if err != nil {
		t.Fatal(err)
	}
	if sdk != mini || sdk.Sessions == 0 {
		t.Fatalf("fuzz counts: SDK %+v Mini %+v", sdk, mini)
	}
	writeParityEvidence(t, "fuzz", map[string]any{"timing": parityTiming{binaryTimingScope, wallNS[0], wallNS[1]}, "status": "passed", "sdk": sdk, "mini": mini, "scope": "one-iteration campaign; SDK does not emit seed/case test events"})
	t.Log(fmt.Sprintf("Fuzz SDK=Mini %+v", sdk))
}
