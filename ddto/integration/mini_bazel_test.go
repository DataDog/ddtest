package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func readBazelEvents(t *testing.T, root string) *miniWireCapture {
	t.Helper()
	c := &miniWireCapture{}
	files, err := filepath.Glob(filepath.Join(root, "payloads", "tests", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("missing test payloads: %v %v", files, err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&payload); err != nil {
			t.Fatal(err)
		}
		c.payloads = append(c.payloads, payload)
		events, err := ciMetadataEvents(payload)
		if err != nil {
			t.Fatal(err)
		}
		c.events = append(c.events, events...)
	}
	return c
}
func TestMiniBazelOfflineAndPayloadFiles(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	bins := compileMiniPair(t, dir, driver, "-cover", "-covermode=atomic", "-coverpkg=./...")
	manifestDir := t.TempDir()
	manifest := filepath.Join(manifestDir, "manifest.txt")
	if err := os.WriteFile(manifest, []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	httpCache := filepath.Join(manifestDir, "cache", "http")
	if err := os.MkdirAll(httpCache, 0700); err != nil {
		t.Fatal(err)
	}
	settings := `{"data":{"id":"offline","type":"ci_app_test_service_libraries_settings","attributes":{"require_git":true,"impacted_tests_enabled":true,"itr_enabled":false,"tests_skipping":false,"code_coverage":true,"known_tests_enabled":false,"flaky_test_retries_enabled":false,"early_flake_detection":{"enabled":false},"test_management":{"enabled":false}}}}`
	if err := os.WriteFile(filepath.Join(httpCache, "settings.json"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	var captures []*miniWireCapture
	for _, bin := range bins {
		output := t.TempDir()
		env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_API_KEY=offline-fixture", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_TEST_OPTIMIZATION_MANIFEST_FILE="+manifest, "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true", "TEST_UNDECLARED_OUTPUTS_DIR="+output, "DD_CIVISIBILITY_CODE_COVERAGE_ENABLED=true", "DD_INSTRUMENTATION_TELEMETRY_ENABLED=true")
		out, stderr, code := command(t, dir, env, bin, "-test.run=^Test(Pass|Skip)$")
		if code != 0 {
			t.Fatalf("offline test: %d %s %s", code, out, stderr)
		}
		capture := readBazelEvents(t, output)
		captures = append(captures, capture)
		for _, event := range capture.events {
			meta := event["content"].(map[string]any)["meta"].(map[string]any)
			for key := range meta {
				if strings.HasPrefix(key, "ci.") || strings.HasPrefix(key, "git.") || strings.HasPrefix(key, "os.") || strings.HasPrefix(key, "runtime.") {
					t.Fatalf("offline enrichment leaked: %s", key)
				}
			}
		}
		for _, kind := range []string{"coverage", "telemetry"} {
			files, err := filepath.Glob(filepath.Join(output, "payloads", kind, "*.json"))
			if err != nil || len(files) == 0 {
				t.Fatalf("missing %s payloads for %s: %v %v\n%s\n%s", kind, bin, files, err, out, stderr)
			}
		}
	}
	// JSON numeric IDs are compared through CI semantic attributes; native wire
	// hierarchy integrity has a separate MessagePack contract test.
	if a, b := ciWireEvents(captures[0].events), ciWireEvents(captures[1].events); !reflect.DeepEqual(a, b) {
		t.Fatalf("Bazel CI events SDK %v MINI %v", a, b)
	}
	if a, b := ciWireMetadata(t, captures[0]), ciWireMetadata(t, captures[1]); a != b {
		t.Fatalf("Bazel metadata SDK %s MINI %s", a, b)
	}
	if requests.Load() != 0 {
		t.Fatalf("offline flow sent %d HTTP requests", requests.Load())
	}
}
