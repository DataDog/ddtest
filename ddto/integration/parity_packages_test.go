package integration

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	sdkEvents, err := comparableOtherSource(captures[0].events, false)
	if err != nil {
		t.Fatal(err)
	}
	miniEvents, err := comparableOtherSource(captures[1].events, true)
	if err != nil {
		t.Fatal(err)
	}
	assertMiniCIAttributes(t, sdkEvents, miniEvents)
	writeParityEvidence(t, "packages", map[string]any{"timing": parityTiming{"CLI: preparation, compilation, execution and shutdown/flush", wallNS[0], wallNS[1]}, "status": "passed", "sdk": eventCounts{2, 2, 2, 2, 0}, "mini": eventCounts{2, 2, 2, 2, 0}, "scope": "one session per package binary; packages without tests emit no events"})
}

// TestOther occupies lines 5-9 of other/other_test.go. Its constant expression
// can disappear during compilation. Mini must report the declaration; the
// pinned SDK still uses an instruction's line. Check both contracts before
// comparing the remaining fields, without altering the captured payloads.
func comparableOtherSource(events []map[string]any, mini bool) ([]map[string]any, error) {
	result := append([]map[string]any(nil), events...)
	found := 0
	for i, event := range events {
		content := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		if event["type"] != "test" || meta["test.name"] != "TestOther" {
			continue
		}
		found++
		metrics, _ := content["metrics"].(map[string]any)
		start, ok := metrics["test.source.start"].(float64)
		file, _ := meta["test.source.file"].(string)
		if !ok || start < 5 || start > 9 || start != float64(int(start)) || mini && start != 5 || metrics["test.source.end"] != float64(9) || filepath.Base(file) != "other_test.go" {
			return nil, fmt.Errorf("TestOther source (mini=%t): got %s:%v-%v; Mini requires 5-9, SDK entry must be within 5-9", mini, file, metrics["test.source.start"], metrics["test.source.end"])
		}
		copy := maps.Clone(content)
		copy["metrics"] = maps.Clone(metrics)
		copy["metrics"].(map[string]any)["test.source.start"] = float64(5)
		result[i] = maps.Clone(event)
		result[i]["content"] = copy
	}
	if found != 1 {
		return nil, fmt.Errorf("expected one TestOther event, got %d", found)
	}
	return result, nil
}

func TestOtherSourceComparisonRetainsRangeChecks(t *testing.T) {
	metrics := map[string]any{"test.source.start": float64(9), "test.source.end": float64(9)}
	events := []map[string]any{{"type": "test", "content": map[string]any{
		"meta": map[string]any{"test.name": "TestOther", "test.source.file": "other_test.go"}, "metrics": metrics,
	}}}
	if _, err := comparableOtherSource(events, false); err != nil {
		t.Fatal(err)
	}
	if metrics["test.source.start"] != float64(9) {
		t.Fatal("altered original SDK payload")
	}
	if _, err := comparableOtherSource(events, true); err == nil {
		t.Fatal("accepted the SDK's incorrect start in Mini")
	}
	metrics["test.source.start"] = float64(5)
	if _, err := comparableOtherSource(events, true); err != nil {
		t.Fatal(err)
	}
	metrics["test.source.end"] = float64(10)
	if _, err := comparableOtherSource(events, true); err == nil {
		t.Fatal("accepted an incorrect end")
	}
	metrics["test.source.end"] = float64(9)
	delete(metrics, "test.source.start")
	if _, err := comparableOtherSource(events, false); err == nil {
		t.Fatal("accepted a missing source start")
	}
}

func TestCIVisibilityFuzzCampaign(t *testing.T) {
	if os.Getenv("ORCHESTRION_BIN") == "" {
		t.Skip("set ORCHESTRION_BIN for SDK PR #5442 parity")
	}
	sdkFixture := prepareFuzzExampleFixture(t, "sdk", "orchestrion")
	miniFixture := prepareFuzzExampleFixture(t, "mini", "orchestrion")
	expected, sdkWall := runFuzzExampleScenario(t, sdkFixture, "active-fuzz", false)
	actual, miniWall := runFuzzExampleScenario(t, miniFixture, "active-fuzz", false)
	a := normalizeFuzzExampleEvents(expected, sdkFixture)
	b := alignMiniTestCommands(a, normalizeFuzzExampleEvents(actual, miniFixture), fuzzExampleInvocation(miniFixture, "active-fuzz"))
	if !reflect.DeepEqual(ciWireEvents(a), ciWireEvents(b)) {
		t.Fatal("active fuzz wire parity differs")
	}
	sdk, err := countCIEvents(a)
	if err != nil {
		t.Fatal(err)
	}
	mini, err := countCIEvents(b)
	if err != nil {
		t.Fatal(err)
	}
	if sdk != mini || sdk.Sessions == 0 || sdk.Tests == 0 {
		t.Fatalf("fuzz counts SDK=%+v Mini=%+v", sdk, mini)
	}
	writeParityEvidence(t, "fuzz", map[string]any{"timing": parityTiming{binaryTimingScope, sdkWall.Nanoseconds(), miniWall.Nanoseconds()}, "status": "passed", "sdk": sdk, "mini": mini, "sdk_commit": fuzzExampleSDKCommit, "scope": "SDK PR #5442: coordinator and non-selected seeds; mutations and workers emit no events"})
}
