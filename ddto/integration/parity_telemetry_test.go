package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func writeParityEvidence(t *testing.T, name string, value any) {
	t.Helper()
	path := os.Getenv("PARITY_REPORT_PATH")
	if path == "" {
		return
	}
	path = strings.TrimSuffix(path, filepath.Ext(path)) + "-" + name + ".json"
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}

// Aggregate count/rate points in the CI namespace across message batches.
// Timestamps, batch boundaries and APM namespaces cannot affect CI counts.
func ciTelemetryCounts(t *testing.T, receiver *parityReceiver) map[string]float64 {
	t.Helper()
	counts := map[string]float64{}
	var visit func(any, string)
	visit = func(value any, namespace string) {
		switch obj := value.(type) {
		case []any:
			for _, v := range obj {
				visit(v, namespace)
			}
		case map[string]any:
			if n, ok := obj["namespace"].(string); ok {
				namespace = n
			}
			if metric, ok := obj["metric"].(string); ok && namespace == "civisibility" && (obj["type"] == "count" || obj["type"] == "rate") {
				tags := []string{}
				if raw, ok := obj["tags"].([]any); ok {
					for _, tag := range raw {
						tags = append(tags, fmt.Sprint(tag))
					}
				}
				sort.Strings(tags)
				key := metric + "|" + strings.Join(tags, ",")
				if points, ok := obj["points"].([]any); ok {
					for _, point := range points {
						p := point.([]any)
						counts[key] += p[1].(float64)
					}
				}
			}
			for _, v := range obj {
				visit(v, namespace)
			}
		}
	}
	for path, bodies := range receiver.side {
		if strings.Contains(path, "telemetry") {
			for _, body := range bodies {
				var payload any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				visit(payload, "")
			}
		}
	}
	return counts
}

func TestCIVisibilityTelemetryInventory(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	bins := compileMiniPair(t, dir, driver)
	tc := parityCase{Args: []string{"-test.run=^Test(Pass|Skip)$"}, Env: []string{"DD_INSTRUMENTATION_TELEMETRY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false"}}
	want, sdk := runParityCase(t, dir, bins[0], tc)
	got, mini := runParityCase(t, dir, bins[1], tc)
	if sdk.code != 0 || mini.code != 0 {
		t.Fatalf("telemetry fixture: %s %s", sdk.stderr, mini.stderr)
	}
	a, b := ciTelemetryCounts(t, want), ciTelemetryCounts(t, got)
	// Request counts depend on batch boundaries. Check them against each
	// receiver's observed HTTP requests, then compare all other CI counts exactly.
	stable := func(values map[string]float64, receiver *parityReceiver) map[string]float64 {
		t.Helper()
		copy := map[string]float64{}
		for k, v := range values {
			if strings.HasPrefix(k, "endpoint_payload.requests|") {
				actual := 0
				for path, n := range receiver.requests {
					if strings.HasSuffix(path, "/citestcycle") {
						actual += n
					}
				}
				if v != float64(actual) || actual == 0 {
					t.Fatalf("request telemetry %v does not match %d HTTP requests", v, actual)
				}
			} else {
				copy[k] = v
			}
		}
		return copy
	}
	sa, sb := stable(a, want), stable(b, got)
	if len(sa) == 0 || !reflect.DeepEqual(sa, sb) {
		t.Fatalf("CI telemetry mismatch SDK %v Mini %v", a, b)
	}
	writeParityEvidence(t, "telemetry", map[string]any{"timing": parityTiming{binaryTimingScope, sdk.wall.Nanoseconds(), mini.wall.Nanoseconds()}, "sdk": a, "mini": b, "semantic_counts_equal": true, "request_counts_match_http": true})
	t.Logf("CI telemetry inventory: SDK %v Mini %v", a, b)
}
