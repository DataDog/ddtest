package integration

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func decodedRequestBody(r *http.Request) ([]byte, error) {
	var reader io.Reader = io.LimitReader(r.Body, 16<<20)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = io.LimitReader(gz, 16<<20)
	}
	return io.ReadAll(reader)
}

// Coverage report compression happens inside the multipart part. Decode it
// before comparison; retain the LCOV text and every event metadata attribute.
func decodedCoverageReport(body []byte, contentType string) ([]byte, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var event map[string]any
	var lcov string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch part.FormName() {
		case "event":
			err = json.NewDecoder(part).Decode(&event)
		case "coverage":
			var gz *gzip.Reader
			gz, err = gzip.NewReader(part)
			if err == nil {
				var data []byte
				data, err = io.ReadAll(io.LimitReader(gz, 16<<20))
				gz.Close()
				lcov = string(data)
			}
		default:
			err = fmt.Errorf("unknown coverage report part %q", part.FormName())
		}
		part.Close()
		if err != nil {
			return nil, err
		}
	}
	if event["type"] != "coverage_report" || event["format"] != "lcov" || !strings.Contains(lcov, "SF:") {
		return nil, fmt.Errorf("invalid LCOV report")
	}
	return json.Marshal(map[string]any{"event": event, "lcov": lcov})
}

func comparableSidePayloads(t *testing.T, receiver *parityReceiver) map[string][]string {
	t.Helper()
	result := map[string][]string{}
	for path, bodies := range receiver.side {
		if strings.Contains(path, "telemetry") {
			continue
		} // audited separately by namespace
		for _, body := range bodies {
			if strings.HasSuffix(path, "/logs") {
				var logs []map[string]any
				if err := json.Unmarshal(body, &logs); err != nil {
					t.Fatal(err)
				}
				for _, entry := range logs {
					id, err := strconv.ParseUint(fmt.Sprint(entry["dd.span_id"]), 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, event := range receiver.events {
						content := event["content"].(map[string]any)
						meta, _ := content["meta"].(map[string]any)
						if event["type"] == "test" && content["span_id"] == id && meta["test.name"] == entry["test.name"] {
							found = true
						}
					}
					if !found || entry["dd.trace_id"] != entry["dd.span_id"] {
						t.Fatalf("log lost test correlation: %v", entry)
					}
					delete(entry, "timestamp")
					delete(entry, "dd.trace_id")
					delete(entry, "dd.span_id")
					// Go's verbose output includes durations. Keep the log text and all lines,
					// replacing only elapsed-time tokens shared by the output comparator.
					entry["message"] = normalizedOutput(fmt.Sprint(entry["message"]))
					row, err := json.Marshal(entry)
					if err != nil {
						t.Fatal(err)
					}
					result["logs"] = append(result["logs"], string(row))
				}
			} else if strings.HasSuffix(path, "/cicovreprt") {
				result["coverage-report"] = append(result["coverage-report"], string(body))
			}
		}
	}
	for _, rows := range result {
		sort.Strings(rows)
	}
	return result
}
func assertSidePayloadParity(t *testing.T, want, got *parityReceiver) {
	t.Helper()
	a, b := comparableSidePayloads(t, want), comparableSidePayloads(t, got)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("side payloads differ: SDK %v Mini %v", a, b)
	}
}

// Benchmark observations vary between executions. Preserve every metric name,
// run count and value type; compare measured means/statistics for validity
// instead of requiring identical timings or allocation measurements.
func comparableBenchmarkEvents(t *testing.T, events []map[string]any) []map[string]any {
	t.Helper()
	out := make([]map[string]any, len(events))
	for i, event := range events {
		out[i] = event
		content := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		if meta["test.type"] != "benchmark" {
			continue
		}
		copyEvent := map[string]any{}
		for k, v := range event {
			copyEvent[k] = v
		}
		copyContent := map[string]any{}
		for k, v := range content {
			copyContent[k] = v
		}
		copyEvent["content"] = copyContent
		metrics := map[string]any{}
		for key, value := range content["metrics"].(map[string]any) {
			if strings.HasPrefix(key, "benchmark.") && (strings.HasSuffix(key, ".mean") || strings.Contains(key, ".statistics.")) {
				switch n := value.(type) {
				case float64:
					if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
						t.Fatalf("invalid benchmark %s=%v", key, n)
					}
				case uint64:
				case int64:
					if n < 0 {
						t.Fatalf("invalid benchmark %s=%v", key, n)
					}
				default:
					t.Fatalf("benchmark measurement %s has type %T", key, value)
				}
				value = fmt.Sprintf("measurement:%T", value)
			}
			metrics[key] = value
		}
		copyContent["metrics"] = metrics
		out[i] = copyEvent
	}
	return out
}

// Orchestrion can give an injected testing frame the artificial location
// orchestrion/src/testing/<generated>:1. The POC keeps the native toolchain
// location. Compare that one library frame by function name while retaining
// every application/CI frame, frame order and error text.
func comparableOracleEvents(t *testing.T, events []map[string]any) []map[string]any {
	t.Helper()
	out := make([]map[string]any, len(events))
	methods := map[string]bool{"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true, "Fail": true, "FailNow": true}
	for i, event := range comparableBenchmarkEvents(t, events) {
		out[i] = event
		content := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		stack, ok := meta["error.stack"].(string)
		if !ok {
			continue
		}
		lines := strings.Split(stack, "\n")
		for j := 0; j+1 < len(lines); j++ {
			method := strings.TrimPrefix(lines[j], "testing.(*common).")
			if !methods[method] {
				continue
			}
			line := strings.ReplaceAll(lines[j+1], "\\", "/")
			if (line == "\t<generated>:1" || line == "\torchestrion/src/testing/<generated>:1") || strings.Contains(line, "/src/testing/testing.go:") {
				lines[j+1] = "\ttesting/common source location"
			}
		}
		copiedMeta := map[string]any{}
		for k, v := range meta {
			copiedMeta[k] = v
		}
		copiedMeta["error.stack"] = strings.Join(lines, "\n")
		copiedContent := map[string]any{}
		for k, v := range content {
			copiedContent[k] = v
		}
		copiedContent["meta"] = copiedMeta
		copiedEvent := map[string]any{}
		for k, v := range event {
			copiedEvent[k] = v
		}
		copiedEvent["content"] = copiedContent
		out[i] = copiedEvent
	}
	return out
}
func TestOracleStackComparisonRetainsApplication(t *testing.T) {
	wrap := func(stack string) []map[string]any {
		return []map[string]any{{"type": "test", "content": map[string]any{"meta": map[string]any{"error.stack": stack}}}}
	}
	mini := wrap("testing.(*common).Error\n\t/go/src/testing/testing.go:1349\napp.TestFailure\n\t/work/app_test.go:42")
	for _, generated := range []string{"<generated>:1", "orchestrion/src/testing/<generated>:1"} {
		sdk := wrap("testing.(*common).Error\n\t" + generated + "\napp.TestFailure\n\t/work/app_test.go:42")
		if !reflect.DeepEqual(comparableOracleEvents(t, sdk), comparableOracleEvents(t, mini)) {
			t.Fatal("artificial testing frame mismatch")
		}
		changed := wrap("testing.(*common).Error\n\t/go/src/testing/testing.go:1349\napp.TestFailure\n\t/work/app_test.go:43")
		if reflect.DeepEqual(comparableOracleEvents(t, sdk), comparableOracleEvents(t, changed)) {
			t.Fatal("application source line hidden")
		}
	}
	appGenerated := wrap("app.Error\n\t<generated>:1")
	appSource := wrap("app.Error\n\t/go/src/testing/testing.go:1349")
	if reflect.DeepEqual(comparableOracleEvents(t, appGenerated), comparableOracleEvents(t, appSource)) {
		t.Fatal("non-testing generated frame hidden")
	}
}
